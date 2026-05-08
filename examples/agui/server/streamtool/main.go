//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates an AG-UI server that uses the built-in streamed
// tool-result activity semantics.
package main

import (
	"flag"
	"net/http"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const appName = "agui-streamtool-demo"

var (
	modelName            = flag.String("model", "deepseek-v4-flash", "OpenAI-compatible model name.")
	isStream             = flag.Bool("stream", true, "Whether to stream the model response.")
	address              = flag.String("address", "127.0.0.1:8080", "Listen address.")
	path                 = flag.String("path", "/agui", "HTTP path.")
	messagesSnapshotPath = flag.String("messages-snapshot-path", "/history", "Messages snapshot HTTP path.")
)

func main() {
	flag.Parse()
	modelInstance := openai.New(*modelName)
	generationConfig := model.GenerationConfig{
		MaxTokens:   intPtr(768),
		Temperature: floatPtr(0.1),
		Stream:      *isStream,
	}
	agent := newAgent(modelInstance, generationConfig)
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
		agui.WithStreamingToolResultActivityEnabled(true),
	)
	if err != nil {
		log.Fatalf("create AG-UI server failed: %v", err)
	}
	log.Infof("AG-UI: serving agent %q on http://%s%s", agent.Info().Name, *address, *path)
	log.Infof("AG-UI: messages snapshot available at http://%s%s", *address, *messagesSnapshotPath)
	if err := http.ListenAndServe(*address, server.Handler()); err != nil {
		log.Fatalf("server stopped with error: %v", err)
	}
}

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
