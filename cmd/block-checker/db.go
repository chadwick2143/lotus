package main

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	DetailCollName = "detail"
)

type DB struct {
	Mongo *mongo.Database
}

type BlockDetail struct {
	Stamp int64
	Round int64
	Miner string
	Mined bool
}

type BlockSummary struct {
	Stamp    int64
	Round    int64
	Miner    string
	Mined24h int64
	Lost24h  int64
	Mined7d  int64
	Lost7d   int64
	Mined30d int64
	Lost30d  int64
}

func (db *DB) collection(name string, opts ...*options.CollectionOptions) *mongo.Collection {
	return db.Mongo.Collection(name, opts...)
}

func (db *DB) InsertIfNotExist(collName string, filter interface{}, document interface{}) error {
	timeout, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	opts := options.Update().SetUpsert(true)
	_, err := db.collection(collName).UpdateOne(timeout, filter, bson.D{{"$setOnInsert", document}}, opts)
	return err
}

func (db *DB) CountDocuments(collName string, filter interface{}) (int64, error) {
	timeout, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return db.collection(collName).CountDocuments(timeout, filter)
}
