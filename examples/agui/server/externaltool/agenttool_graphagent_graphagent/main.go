//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates GraphAgent interrupt and resume through AgentTool in an AG-UI server.
package main

import (
	"flag"
	"net/http"

	graphcheckpoint "trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const appName = "agui-agenttool-interrupt-demo"

var (
	modelName = flag.String("model", "gpt-5.2", "OpenAI-compatible model name.")
	isStream  = flag.Bool("stream", true, "Whether to stream the response.")
	address   = flag.String("address", "127.0.0.1:8080", "Listen address.")
	path      = flag.String("path", "/agui", "HTTP path.")
)

func main() {
	flag.Parse()
	saver := graphcheckpoint.NewSaver()
	modelInstance := openai.New(*modelName)
	generationConfig := newGenerationConfig()
	childAgent, err := newChildReviewAgent(saver, modelInstance, generationConfig)
	if err != nil {
		log.Fatalf("create child graph agent failed: %v", err)
	}
	parentAgent, err := newParentGraphAgent(childAgent, saver, modelInstance, generationConfig)
	if err != nil {
		log.Fatalf("create parent graph agent failed: %v", err)
	}
	sessionService := sessioninmemory.NewSessionService()
	run := runner.NewRunner(appName, parentAgent, runner.WithSessionService(sessionService))
	defer run.Close()
	server, err := newAGUIServer(run, sessionService)
	if err != nil {
		log.Fatalf("create AG-UI server failed: %v", err)
	}
	log.Infof("AG-UI: serving agent %q on http://%s%s", parentAgent.Info().Name, *address, *path)
	if err := http.ListenAndServe(*address, server.Handler()); err != nil {
		log.Fatalf("server stopped with error: %v", err)
	}
}
