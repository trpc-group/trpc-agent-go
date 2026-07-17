//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates a GraphAgent AG-UI server where each graph node
// proactively reports progress through the current AG-UI run context.
package main

import (
	"flag"
	"net/http"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
)

const appName = "agui-graph-progress-demo"

var (
	address = flag.String("address", "127.0.0.1:8080", "Listen address.")
	path    = flag.String("path", "/agui", "HTTP path.")
)

func main() {
	flag.Parse()
	graphAgent, err := newGraphAgent()
	if err != nil {
		log.Fatalf("create graph agent failed: %v", err)
	}
	coreRunner := runner.NewRunner(appName, graphAgent)
	defer coreRunner.Close()
	server, err := agui.New(coreRunner, agui.WithPath(*path))
	if err != nil {
		log.Fatalf("create AG-UI server failed: %v", err)
	}
	log.Infof("AG-UI: serving graph agent %q on http://%s%s", graphAgent.Info().Name, *address, *path)
	if err := http.ListenAndServe(*address, server.Handler()); err != nil {
		log.Fatalf("server stopped with error: %v", err)
	}
}
