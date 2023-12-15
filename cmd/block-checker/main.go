package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/lotus/api/v0api"
	"github.com/filecoin-project/lotus/build"
	"github.com/filecoin-project/lotus/chain/gen"
	"github.com/filecoin-project/lotus/chain/types"
	lcli "github.com/filecoin-project/lotus/cli"
	"github.com/urfave/cli/v2"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

func stampToEpoch(stamp int64) int64 {
	return (stamp - 1598306400) / 30
}

func epochToStamp(epoch abi.ChainEpoch) int64 {
	return int64(epoch)*30 + 1598306400
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile | log.Lmicroseconds)

	app := &cli.App{
		Name:    "block-checker",
		Usage:   "block checker",
		Version: build.UserVersion(),
		Commands: []*cli.Command{
			RunCmd,
		},
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "repo",
				EnvVars: []string{"LOTUS_PATH"},
				Hidden:  true,
				Value:   "~/.lotus", // TODO: Consider XDG_DATA_HOME
			},
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}

var RunCmd = &cli.Command{
	Name:  "run",
	Usage: "run block checker",
	Action: func(cctx *cli.Context) error {
		ctx, done := context.WithCancel(context.Background())
		sigChan := make(chan os.Signal, 2)
		go func() {
			<-sigChan
			done()
		}()
		signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)

		nodeAPI, acloser, err := lcli.GetFullNodeAPI(cctx)
		if err != nil {
			log.Printf("[Error] get fullnode api: %+v", err)
			return err
		}
		defer acloser()

		timeout, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		mongoClient, err := mongo.Connect(timeout)
		if err != nil {
			return fmt.Errorf("[Error] connect mongo: %+v", err)
		}
		dbTimeout, dbCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer dbCancel()
		err = mongoClient.Ping(dbTimeout, readpref.Primary())
		if err != nil {
			return fmt.Errorf("[Error] ping mongo: %+v", err)
		}
		defer func(mongoClient *mongo.Client, ctx context.Context) {
			err := mongoClient.Disconnect(ctx)
			if err != nil {
				log.Printf("[Error] mongo client disconnect: %+v", err)
			}
		}(mongoClient, ctx)
		db := &DB{Mongo: mongoClient.Database("block")}

		if err := LoadConfig(); err != nil {
			return fmt.Errorf("[Error] init miners: %+v", err)
		}
		log.Printf("[Info] block checker running with miners: %+v", config.Miners)

		var round int64
		sched := make(chan int64, 2)
		go func() {
			for {
				stamp := time.Now().Unix()
				epoch := stampToEpoch(stamp) - 60
				if round != epoch {
					round = epoch
					sched <- round
				}
				time.Sleep(time.Second * 5)
			}
		}()

		for {
			select {
			case round = <-sched:
				log.Printf("[Info] check block at round %d", round)
				for _, miner := range config.Miners {
					go check(cctx, nodeAPI, miner, abi.ChainEpoch(round), db)
				}
			case <-ctx.Done():
				return fmt.Errorf("[Error] block checker shutdown")
			}
		}
	},
}

func check(cctx *cli.Context, nodeAPI v0api.FullNode, miner string, round abi.ChainEpoch, db *DB) {
	maddr, err := address.NewFromString(miner)
	if err != nil {
		log.Printf("[Error] parsing address %s: %+v", miner, err)
		return
	}

	ctx := lcli.ReqContext(cctx)
	ts, err := nodeAPI.ChainGetTipSetByHeight(ctx, round, types.EmptyTSK)
	if err != nil {
		log.Printf("[Error] chain get tipset by height: %+v", err)
		return
	}

	mbi, err := nodeAPI.MinerGetBaseInfo(ctx, maddr, round, ts.Key())
	if err != nil {
		log.Printf("[Error] get mining base info: %+v", err)
		return
	}
	if mbi == nil {
		log.Printf("[Error] mining base info of %s is nil at round %d", maddr, round)
		return
	}

	if !mbi.EligibleForMining {
		// slashed or just have no power yet
		log.Printf("[Error] miner %s is not eligible for mining at round %d", maddr, round)
		return
	}

	bvals := mbi.BeaconEntries
	rbase := mbi.PrevBeaconEntry
	if len(bvals) > 0 {
		rbase = bvals[len(bvals)-1]
	}

	winner, err := gen.IsRoundWinner(ctx, nil, round, maddr, rbase, mbi, nodeAPI)
	if err != nil {
		log.Printf("[Error] check if %s win at round %d: %+v", maddr, round, err)
		return
	}

	if winner != nil {
		mined := false
		for _, block := range ts.Blocks() {
			if block.Miner == maddr {
				mined = true
				break
			}
		}

		stamp := epochToStamp(round)
		detail := BlockDetail{Stamp: stamp, Round: int64(round), Miner: miner, Mined: mined}
		filter := bson.D{{"miner", miner}, {"round", int64(round)}}
		err = db.InsertIfNotExist(DetailCollName, filter, detail)
		if err != nil {
			log.Printf("[Error] insert block detail %+v into mongo: %+v", detail, err)
		}

		if !mined {
			log.Printf("[Info] %s win at round %d but not mined, send to webhooks", miner, round)
			t := time.Unix(stamp, 0).Format("01月02日 15:04:05")
			coreMsg := fmt.Sprintf("filecoin-%s在高度%d(%s)", miner, round, t)
			msg := fmt.Sprintf("[BlockChecker] %s获得出块权但未成功出块，请及时处理", coreMsg)
			for _, send := range webhooks {
				send(msg)
			}

			bs, err := summary(db, round, miner)
			if err != nil {
				log.Printf("[Error] summary %s's block at round %d : %+v", miner, round, err)
				return
			}

			sum24h := bs.Lost24h + bs.Mined24h
			rate24h := float32(bs.Lost24h) / float32(sum24h) * 100
			msg24h := fmt.Sprintf("24h: %.3f%%(%d/%d)", rate24h, bs.Lost24h, sum24h)

			sum7d := bs.Lost7d + bs.Mined7d
			rate7d := float32(bs.Lost7d) / float32(sum7d) * 100
			msg7d := fmt.Sprintf("7d: %.3f%%(%d/%d)", rate7d, bs.Lost7d, sum7d)

			sum30d := bs.Lost30d + bs.Mined30d
			rate30d := float32(bs.Lost30d) / float32(sum30d) * 100
			msg30d := fmt.Sprintf("30d: %.3f%%(%d/%d)", rate30d, bs.Lost30d, sum30d)

			coreMsg = fmt.Sprintf("filecoin-%s丢块率汇总(截至%s) %s; %s; %s", miner, t, msg24h, msg7d, msg30d)
			msg = fmt.Sprintf("[BlockChecker] %s, 请及时处理", coreMsg)
			for _, send := range webhooks {
				send(msg)
			}

			sendToMonitor(miner)
		} else {
			log.Printf("[Info] %s win at round %d and mined", miner, round)
		}
	}
}

func summary(db *DB, round abi.ChainEpoch, miner string) (*BlockSummary, error) {
	stamp := epochToStamp(round)
	secPerDay := int64(24 * time.Hour / time.Second)
	sliceDays := [3]int64{1, 7, 30}
	sliceMined := [2]bool{true, false}
	sliceCount := make([]int64, 0, 6)

	for _, days := range sliceDays {
		for _, mined := range sliceMined {
			var filter bson.D
			if miner == "*" {
				filter = bson.D{{"mined", mined},
					{"stamp", bson.D{{"$gt", stamp - days*secPerDay}}}}
			} else {
				filter = bson.D{{"miner", miner}, {"mined", mined},
					{"stamp", bson.D{{"$gt", stamp - days*secPerDay}}}}
			}

			count, err := db.CountDocuments(DetailCollName, filter)
			if err != nil {
				return nil, err
			} else {
				sliceCount = append(sliceCount, count)
			}
		}
	}

	bs := &BlockSummary{Stamp: stamp, Round: int64(round), Miner: miner,
		Mined24h: sliceCount[0], Lost24h: sliceCount[1], Mined7d: sliceCount[2],
		Lost7d: sliceCount[3], Mined30d: sliceCount[4], Lost30d: sliceCount[5]}

	return bs, nil
}
