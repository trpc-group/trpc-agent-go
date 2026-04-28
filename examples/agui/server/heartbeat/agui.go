//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"net/http"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
)

const appName = "agui-heartbeat-demo"

type serverConfig struct {
	ModelName         string
	GenerationConfig  model.GenerationConfig
	WaitDuration      time.Duration
	Address           string
	Path              string
	HeartbeatInterval time.Duration
}

func runServer(cfg serverConfig) {
	agent := newAgent(cfg.ModelName, cfg.GenerationConfig, cfg.WaitDuration)
	run := runner.NewRunner(appName, agent)
	defer run.Close()
	server, err := agui.New(
		run,
		agui.WithPath(cfg.Path),
		agui.WithHeartbeatInterval(cfg.HeartbeatInterval),
	)
	if err != nil {
		log.Fatalf("create AG-UI server failed: %v", err)
	}
	log.Infof("AG-UI: serving agent %q on http://%s%s", agent.Info().Name, cfg.Address, cfg.Path)
	log.Infof("AG-UI: SSE heartbeat interval: %s", durationLabel(cfg.HeartbeatInterval))
	log.Infof("AG-UI: tool quiet period: %s", cfg.WaitDuration)
	if err := http.ListenAndServe(cfg.Address, server.Handler()); err != nil {
		log.Fatalf("server stopped with error: %v", err)
	}
}

func durationLabel(d time.Duration) string {
	if d <= 0 {
		return "disabled"
	}
	return d.String()
}
