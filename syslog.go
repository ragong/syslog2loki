package main

import (
	"context"
	"fmt"
	"gopkg.in/mcuadros/go-syslog.v2"
	"log/slog"
	"os"
)

const SyslogChanDeep = 1024

type SyslogServer struct {
	c             config
	loki          *lokiClient
	ctx           context.Context
	syslogChannel syslog.LogPartsChannel
}

func newSyslogServer(cfg config, loki *lokiClient) (*SyslogServer, error) {
	return &SyslogServer{c: cfg, loki: loki}, nil
}

func (s *SyslogServer) Listen() {
	s.syslogChannel = make(syslog.LogPartsChannel, SyslogChanDeep)
	handler := syslog.NewChannelHandler(s.syslogChannel)
	server := syslog.NewServer()
	server.SetFormat(syslog.RFC3164)
	server.SetHandler(handler)
	if e := server.ListenUDP(s.c.SyslogBind); e != nil {
		slog.Error("[Syslog]"+"Failed to initialize Syslog service:", e)
		os.Exit(1)
	} else {
		slog.Info("[Syslog]" + "Successfully initialized Syslog service:,bind on " + s.c.SyslogBind)
	}
	server.Boot()
	go func(channel syslog.LogPartsChannel) {
		for logParts := range channel {
			slog.Debug("[Syslog]" + fmt.Sprint(logParts))
			s.loki.Push(logParts)
		}
	}(s.syslogChannel)
	server.Wait()
	return
}

func (s *SyslogServer) Close() {
	slog.Info("[Syslog] Exit Syslog listening.")
	close(s.syslogChannel)
}
