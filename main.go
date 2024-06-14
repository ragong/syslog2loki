package main

import (
	"encoding/json"
	"flag"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

var (
	configPath   string
	syslogServer *SyslogServer
	loki         *lokiClient
)

func init() {
	flag.StringVar(&configPath, "c", "./config.json", "path of config.json")
	flag.Parse()
}

func main() {
	slog.SetLogLoggerLevel(slog.LevelDebug)
	var cfg = &config{}
	err := loadJsonFile(configPath, cfg)
	if err == nil {
		slog.Info("Configuration file loaded successfully => " + configPath)
	} else {
		slog.Error("Configuration file failed to load: " + configPath + " => " + err.Error())
		os.Exit(1)
	}
	if lk, e := newLokiClient(*cfg); e != nil {
		os.Exit(1)
	} else {
		if sl, esl := newSyslogServer(*cfg, lk); esl != nil {
			sl.Close()
			os.Exit(1)
		} else {
			syslogServer = sl
			loki = lk
		}
	}
	signalChan := make(chan os.Signal, 1)
	go func() {
		for s := range signalChan {
			switch s {
			case syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT:
				slog.Info("Exiting the program.")
				syslogServer.Close()
				loki.Close()
				os.Exit(0)
			default:
			}
		}
	}()
	signal.Notify(signalChan)
	syslogServer.Listen()
}

func loadJsonFile(filePath string, obj interface{}) error {
	data, err := readBytes(filePath)
	if err != nil {
		return err
	}
	err = json.Unmarshal(data, obj)
	if err != nil {
		return err
	}
	return nil
}

func readBytes(filePath string) ([]byte, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = file.Close()
	}()
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	return data, nil
}
