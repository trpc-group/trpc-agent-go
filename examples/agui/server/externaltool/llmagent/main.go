//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates AG-UI with LLMAgent using both internal and external tools.
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
	appName   = "agui-llmagent-externaltool-demo"
	agentName = "agui-externaltool-llmagent"
)

var (
	modelName = flag.String("model", "gpt-5.2", "OpenAI-compatible model name.")
	isStream  = flag.Bool("stream", true, "Whether to stream the response.")
	address   = flag.String("address", "127.0.0.1:8080", "Listen address.")
	path      = flag.String("path", "/agui", "HTTP path.")
)

func main() {
	flag.Parse()
	modelInstance := openai.New(*modelName)
	generationConfig := newGenerationConfig()
	sessionService := sessioninmemory.NewSessionService()
	ag := newAgent(modelInstance, generationConfig)
	run := runner.NewRunner(appName, ag, runner.WithSessionService(sessionService))
	defer run.Close()
	server, err := newAGUIServer(run, sessionService)
	if err != nil {
		log.Fatalf("failed to create AG-UI server: %v", err)
	}
	log.Infof("AG-UI: serving agent %q on http://%s%s", ag.Info().Name, *address, *path)
	log.Infof("AG-UI: message snapshot endpoint is available at http://%s/history", *address)
	if err := http.ListenAndServe(*address, server.Handler()); err != nil {
		log.Fatalf("server stopped with error: %v", err)
	}
}
