//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates how to enable /history follow mode so a snapshot request can continue streaming persisted AG-UI events until the active run finishes.
package main

import (
	"context"
	"flag"
	"net/http"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

var (
	modelName            = flag.String("model", "deepseek-v3.2", "Model to use")
	isStream             = flag.Bool("stream", true, "Whether to stream the response")
	address              = flag.String("address", "127.0.0.1:8080", "Listen address")
	path                 = flag.String("path", "/agui", "HTTP path")
	messagesSnapshotPath = flag.String("messages-snapshot-path", "/history", "Messages snapshot HTTP path")
	flushInterval        = flag.Duration("flush-interval", 50*time.Millisecond, "Flush interval for persisting aggregated track events.")
	followEnabled        = flag.Bool("follow-enabled", true, "Whether to enable /history follow mode.")
	followMaxDuration    = flag.Duration("follow-max-duration", 60*time.Second, "Maximum duration to follow after emitting MESSAGES_SNAPSHOT.")
)

const appName = "demo-app"

func main() {
	flag.Parse()
	sessionService := inmemory.NewSessionService()
	agent := newAgent()
	r := runner.NewRunner(appName, agent, runner.WithSessionService(sessionService))
	defer r.Close()
	server, err := agui.New(
		r,
		agui.WithPath(*path),
		agui.WithMessagesSnapshotPath(*messagesSnapshotPath),
		agui.WithMessagesSnapshotEnabled(true),
		agui.WithFlushInterval(*flushInterval),
		agui.WithAppName(appName),
		agui.WithSessionService(sessionService),
		agui.WithMessagesSnapshotFollowEnabled(*followEnabled),
		agui.WithMessagesSnapshotFollowMaxDuration(*followMaxDuration),
		agui.WithAGUIRunnerOptions(
			aguirunner.WithUserIDResolver(userIDResolver),
		),
	)
	if err != nil {
		log.Fatalf("failed to create AG-UI server: %v", err)
	}
	log.Infof("AG-UI: serving agent %q on http://%s%s", agent.Info().Name, *address, *path)
	log.Infof("AG-UI: messages snapshot available at http://%s%s", *address, *messagesSnapshotPath)
	log.Infof("AG-UI: messages snapshot follow enabled=%v maxDuration=%s", *followEnabled, followMaxDuration.String())
	if err := http.ListenAndServe(*address, server.Handler()); err != nil {
		log.Fatalf("server stopped with error: %v", err)
	}
}

func userIDResolver(ctx context.Context, input *adapter.RunAgentInput) (string, error) {
	forwardedProps, ok := input.ForwardedProps.(map[string]any)
	if !ok {
		return "anonymous", nil
	}
	user, ok := forwardedProps["userId"].(string)
	if !ok {
		return "anonymous", nil
	}
	if user != "" {
		return user, nil
	}
	return "anonymous", nil
}
