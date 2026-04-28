//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates AG-UI tool-call argument streaming.
package main

import (
	"flag"
	"net/http"
	"os"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const appName = "agui-toolcall-delta-demo"

var (
	modelName            = flag.String("model", "gpt-5.2", "OpenAI-compatible model name.")
	baseURL              = flag.String("base-url", os.Getenv("OPENAI_BASE_URL"), "OpenAI-compatible base URL.")
	apiKey               = flag.String("api-key", os.Getenv("OPENAI_API_KEY"), "API key for the model service.")
	isStream             = flag.Bool("stream", true, "Whether to stream the model response.")
	address              = flag.String("address", "127.0.0.1:8080", "Listen address.")
	path                 = flag.String("path", "/agui", "HTTP path.")
	messagesSnapshotPath = flag.String("messages-snapshot-path", "/history", "Messages snapshot HTTP path.")
)

func main() {
	flag.Parse()
	modelInstance := newLLMModel(*modelName, *baseURL, *apiKey)
	generationConfig := newGenerationConfig(*isStream)
	documents := newDocumentStore()
	agent := newAgent(modelInstance, generationConfig, documents)
	sessionService := sessioninmemory.NewSessionService()
	run := runner.NewRunner(appName, agent, runner.WithSessionService(sessionService))
	defer run.Close()
	server, err := agui.New(
		run,
		agui.WithAppName(appName),
		agui.WithPath(*path),
		agui.WithMessagesSnapshotEnabled(true),
		agui.WithMessagesSnapshotPath(*messagesSnapshotPath),
		agui.WithSessionService(sessionService),
		agui.WithToolCallDeltaStreamingEnabled(true),
	)
	if err != nil {
		log.Fatalf("create AG-UI server failed: %v", err)
	}
	if strings.TrimSpace(*apiKey) == "" {
		log.Warnf("OPENAI_API_KEY is empty; set it with -api-key or the OPENAI_API_KEY environment variable.")
	}
	log.Infof("AG-UI: serving agent %q on http://%s%s", agent.Info().Name, *address, *path)
	log.Infof("AG-UI: messages snapshot available at http://%s%s", *address, *messagesSnapshotPath)
	if err := http.ListenAndServe(*address, server.Handler()); err != nil {
		log.Fatalf("server stopped with error: %v", err)
	}
}
