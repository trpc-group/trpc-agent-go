//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates exporting and printing a GraphAgent execution trace.
package main

import (
	"context"
	"log"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func main() {
	ctx := context.Background()
	ag, staticNodeIDs, err := buildAgent()
	if err != nil {
		log.Fatalf("Build graph agent failed: %v", err)
	}
	r := runner.NewRunner("graph-trace-example", ag, runner.WithSessionService(inmemory.NewSessionService()))
	eventCh, err := r.Run(
		ctx,
		"user-1",
		"session-1",
		model.NewUserMessage("hello graph trace"),
		agent.WithExecutionTraceEnabled(true),
	)
	if err != nil {
		log.Fatalf("Run graph agent failed: %v", err)
	}
	var completion *event.Event
	for evt := range eventCh {
		if evt != nil && evt.IsRunnerCompletion() {
			completion = evt
		}
	}
	if completion == nil || completion.ExecutionTrace == nil {
		log.Fatalf("Runner completion event did not carry an execution trace.")
	}
	printExecutionTrace(completion.ExecutionTrace, staticNodeIDs)
}
