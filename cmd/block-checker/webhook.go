package main

import (
	"crypto/ed25519"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

var webhooks = [...]func(msg string){
	sendToFeiShu,
	sendToLark,
}

func sendToMonitor(miner string) {
	url := config.MonitorUrl
	seed := config.MonitorSeed
	name := config.MonitorName
	if url == "" || seed == "" || name == "" {
		return
	}
	signSeed := seed[:ed25519.SeedSize]
	signKey := ed25519.NewKeyFromSeed([]byte(signSeed))

	stamp := time.Now().Unix()
	sig := hex.EncodeToString(ed25519.Sign(signKey, []byte(fmt.Sprintf("filecoin-%s-%d", miner, stamp))))
	payload := strings.NewReader(`{"miner": "` + miner + `", "stamp": "` +
		fmt.Sprintf("%d", stamp) + `", "sig": "` + sig + `", "name": "` + name + `"}`)

	r, err := doRequest(url, payload)
	if err != nil {
		log.Printf("[Error] send to monitor failed: %+v", err)
		return
	}

	if r.StatusCode != 200 {
		err := fmt.Errorf("status code error: %d", r.StatusCode)
		log.Printf("[Error] send to monitor failed: %+v", err)
		return
	}
}

func sendToFeiShu(msg string) {
	url := config.FeishuUrl
	if url == "" {
		return
	}
	payload := strings.NewReader(`{"msg_type": "text", "content": {"text": "` + msg + `"}}`)

	r, err := doRequest(url, payload)
	if err != nil {
		log.Printf("[Error] send to feishu failed: %+v", err)
		return
	}

	if r.StatusCode != 200 {
		err := fmt.Errorf("status code error: %d", r.StatusCode)
		log.Printf("[Error] send to feishu failed: %+v", err)
		return
	}
}

func sendToLark(msg string) {
	url := config.LarkUrl
	if url == "" {
		return
	}
	payload := strings.NewReader(`{"msg_type": "text", "content": {"text": "` + msg + `"}}`)

	r, err := doRequest(url, payload)
	if err != nil {
		log.Printf("[Error] send to lark failed: %+v", err)
		return
	}

	if r.StatusCode != 200 {
		err := fmt.Errorf("status code error: %d", r.StatusCode)
		log.Printf("[Error] send to lark failed: %+v", err)
		return
	}
}

func doRequest(url string, payload *strings.Reader) (*http.Response, error) {
	req, _ := http.NewRequest("POST", url, payload)
	req.Close = true
	req.Header.Add("Content-Type", "application/json")
	transCfg := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := http.Client{
		Transport: transCfg,
		Timeout:   3 * time.Minute,
	}
	return client.Do(req)
}
