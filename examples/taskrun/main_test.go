//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestServiceOptions(t *testing.T) {
	t.Parallel()

	opts, err := serviceOptions("")
	if err != nil {
		t.Fatalf("serviceOptions empty path error: %v", err)
	}
	if opts != nil {
		t.Fatalf("serviceOptions empty path = %v, want nil", opts)
	}

	storePath := filepath.Join(t.TempDir(), "runs.json")
	opts, err = serviceOptions(" " + storePath + " ")
	if err != nil {
		t.Fatalf("serviceOptions file path error: %v", err)
	}
	if len(opts) != 1 {
		t.Fatalf("serviceOptions file path len = %d, want 1", len(opts))
	}
}

func TestReportAgentInfoAndTools(t *testing.T) {
	t.Parallel()

	a := &reportAgent{name: "tester"}
	info := a.Info()
	if info.Name != "tester" {
		t.Fatalf("Info.Name = %q, want tester", info.Name)
	}
	if info.Description != agentDesc {
		t.Fatalf("Info.Description = %q, want %q",
			info.Description, agentDesc)
	}
	if a.Tools() != nil {
		t.Fatalf("Tools = %v, want nil", a.Tools())
	}
	if a.SubAgents() != nil {
		t.Fatalf("SubAgents = %v, want nil", a.SubAgents())
	}
	if a.FindSubAgent("missing") != nil {
		t.Fatal("FindSubAgent returned non-nil agent")
	}
}

func TestReportAgentRunEmitsResponse(t *testing.T) {
	t.Parallel()

	a := &reportAgent{name: "tester"}
	inv := agent.NewInvocation(
		agent.WithInvocationID("inv-1"),
		agent.WithInvocationMessage(
			model.NewUserMessage("check screenshot"),
		),
	)
	events, err := a.Run(context.Background(), inv)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	evt, ok := <-events
	if !ok {
		t.Fatal("Run channel closed before emitting an event")
	}
	if evt.Author != "tester" {
		t.Fatalf("event author = %q, want tester", evt.Author)
	}
	got := evt.Response.Choices[0].Message.Content
	if !strings.Contains(got, "check screenshot") {
		t.Fatalf("response %q does not include task", got)
	}
	if _, ok := <-events; ok {
		t.Fatal("Run channel emitted more than one event")
	}
}

func TestReportAgentRunHonorsCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	events, err := (&reportAgent{name: "tester"}).Run(ctx, nil)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if evt, ok := <-events; ok {
		t.Fatalf("Run emitted event after cancellation: %v", evt)
	}
}

func TestResponseEventWithoutInvocation(t *testing.T) {
	t.Parallel()

	evt := responseEvent(nil, "tester")
	if evt.Author != "tester" {
		t.Fatalf("event author = %q, want tester", evt.Author)
	}
	got := evt.Response.Choices[0].Message.Content
	if !strings.Contains(got, "completed delegated task") {
		t.Fatalf("response %q does not include completion text", got)
	}
}
