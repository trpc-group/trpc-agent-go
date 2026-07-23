//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"flag"
	"net/http"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
)

var (
	modelName            = flag.String("model", "deepseek-v4-flash", "Model to use")
	isStream             = flag.Bool("stream", true, "Whether to stream the response")
	address              = flag.String("address", "127.0.0.1:8080", "Listen address")
	path                 = flag.String("path", "/agui", "HTTP path")
	cancelPath           = flag.String("cancel-path", "/cancel", "Cancel HTTP path")
	messagesSnapshotPath = flag.String("messages-snapshot-path", "/history", "Messages snapshot HTTP path")
	timeout              = flag.Duration("timeout", time.Minute, "Maximum AG-UI run duration")
)

type serverConfig struct {
	ModelName            string
	Stream               bool
	Address              string
	Path                 string
	CancelPath           string
	MessagesSnapshotPath string
	Timeout              time.Duration
}

func main() {
	flag.Parse()
	cfg := serverConfig{
		ModelName:            *modelName,
		Stream:               *isStream,
		Address:              *address,
		Path:                 *path,
		CancelPath:           *cancelPath,
		MessagesSnapshotPath: *messagesSnapshotPath,
		Timeout:              *timeout,
	}
	server, agentName, cleanup, err := newAGUIServer(cfg)
	if err != nil {
		log.Fatalf("failed to create AG-UI server: %v", err)
	}
	defer cleanup()
	log.Infof("AG-UI: serving agent %q on http://%s%s", agentName, cfg.Address, cfg.Path)
	log.Infof("AG-UI: messages snapshot available at http://%s%s", cfg.Address, cfg.MessagesSnapshotPath)
	log.Infof("AG-UI: cancel available at http://%s%s", cfg.Address, cfg.CancelPath)
	if err := http.ListenAndServe(cfg.Address, server.Handler()); err != nil {
		log.Fatalf("server stopped with error: %v", err)
	}
}
