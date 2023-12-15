package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/mitchellh/go-homedir"
)

type Config struct {
	Miners      []string
	MonitorUrl  string
	MonitorSeed string
	MonitorName string
	LarkUrl     string
	FeishuUrl   string
}

var config Config

func LoadConfig() error {
	path, err := homedir.Expand("~/.block-checker/config.json")
	if err != nil {
		return fmt.Errorf("[Error] expanding local path: %+v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("[Error] read file: %+v", err)
	}

	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("[Error] json unmarshal: %+v", err)
	}

	return nil
}
