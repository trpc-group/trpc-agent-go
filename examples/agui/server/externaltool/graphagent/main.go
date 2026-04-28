//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main is an AG-UI server example that demonstrates external tool execution with GraphAgent.
package main

import (
	"flag"
	"net/http"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const (
	appName   = "agui-externaltool-demo"
	agentName = "agui-externaltool"
)

var (
	modelName = flag.String("model", "deepseek-chat", "OpenAI-compatible model name.")
	isStream  = flag.Bool("stream", true, "Whether to stream the response.")
	address   = flag.String("address", "127.0.0.1:8080", "Listen address.")
	path      = flag.String("path", "/agui", "HTTP path.")
)

func main() {
	flag.Parse()
	modelInstance := openai.New(*modelName)
	generationConfig := newGenerationConfig()
	g, err := buildGraph(modelInstance, generationConfig)
	if err != nil {
		log.Fatalf("build graph failed: %v", err)
	}
	sessionService := sessioninmemory.NewSessionService()
	ga, err := newGraphAgent(g)
	if err != nil {
		log.Fatalf("create graph agent failed: %v", err)
	}
	r := runner.NewRunner(appName, ga, runner.WithSessionService(sessionService))
	defer r.Close()
	server, err := newAGUIServer(r, sessionService)
	if err != nil {
		log.Fatalf("create AG-UI server failed: %v", err)
	}
	log.Infof("AG-UI: serving agent %q on http://%s%s", ga.Info().Name, *address, *path)
	if err := http.ListenAndServe(*address, server.Handler()); err != nil {
		log.Fatalf("server stopped with error: %v", err)
	}
}
