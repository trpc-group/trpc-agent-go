//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"time"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/aggregator"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/translator"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	_ "trpc.group/trpc-go/trpc-agent-go/session/postgres"
	_ "trpc.group/trpc-go/trpc-agent-go/session/redis"
)

var (
	modelName            = flag.String("model", "deepseek-chat", "Model to use")
	isStream             = flag.Bool("stream", true, "Whether to stream the response")
	address              = flag.String("address", "127.0.0.1:8080", "Listen address")
	path                 = flag.String("path", "/agui", "HTTP path")
	messagesSnapshotPath = flag.String("messages-snapshot-path", "/history", "Messages snapshot HTTP path")
)

const appName = "demo-app"

func main() {
	flag.Parse()
	// New Agent.
	agent := newAgent()
	// New Session Service.
	sessionService := inmemory.NewSessionService()
	// New Runner.
	runner := runner.NewRunner(appName, agent, runner.WithSessionService(sessionService))
	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer runner.Close()
	// New AG-UI server.
	server, err := agui.New(
		runner,
		agui.WithPath(*path),
		agui.WithMessagesSnapshotPath(*messagesSnapshotPath),
		agui.WithMessagesSnapshotEnabled(true),
		agui.WithAppName(appName),
		agui.WithSessionService(sessionService),
		agui.WithAGUIRunnerOptions(
			aguirunner.WithUserIDResolver(userIDResolver),
			aguirunner.WithAggregationOption(
				aggregator.WithEnabled(true),
			),
			aguirunner.WithFlushInterval(1*time.Second),
			aguirunner.WithAggregatorFactory(newAggregator),
			aguirunner.WithTranslatorFactory(newTranslator),
			aguirunner.WithTranslateCallbacks(translator.NewCallbacks().RegisterBeforeTranslate(
				func(ctx context.Context, event *event.Event) (*event.Event, error) {
					data, _ := json.Marshal(event)
					log.Infof("before event: %s", string(data))
					return nil, nil
				},
			).RegisterAfterTranslate(func(ctx context.Context, event events.Event) (events.Event, error) {
				data, _ := json.Marshal(event)
				log.Infof("after event: %s", string(data))
				return nil, nil
			}),
			),
		),
	)
	if err != nil {
		log.Fatalf("failed to create AG-UI server: %v", err)
	}
	// Start server.
	log.Infof("AG-UI: serving agent %q on http://%s%s", agent.Info().Name, *address, *path)
	log.Infof("AG-UI: messages snapshot available at http://%s%s", *address, *messagesSnapshotPath)
	if err := http.ListenAndServe(*address, server.Handler()); err != nil {
		log.Fatalf("server stopped with error: %v", err)
	}
}

func userIDResolver(ctx context.Context, input *adapter.RunAgentInput) (string, error) {
	if user, ok := input.ForwardedProps["userId"].(string); ok && user != "" {
		return user, nil
	}
	return "anonymous", nil
}
