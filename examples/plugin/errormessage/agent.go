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

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// stopAgent is a minimal agent that emits a single raw error event, matching
// the shape llmflow produces when a StopError propagates out of a tool or
// callback. It has no dependency on any real model backend, so the demo is
// fully self-contained.
type stopAgent struct{}

func newStopAgent() agent.Agent {
	return &stopAgent{}
}

func (a *stopAgent) Info() agent.Info {
	return agent.Info{
		Name:        agentName,
		Description: "emits a single stop_agent_error event for demo purposes",
	}
}

func (a *stopAgent) Tools() []tool.Tool {
	return nil
}

func (a *stopAgent) SubAgents() []agent.Agent {
	return nil
}

func (a *stopAgent) FindSubAgent(string) agent.Agent {
	return nil
}

func (a *stopAgent) Run(
	_ context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	ch <- event.NewErrorEvent(
		inv.InvocationID,
		agentName,
		agent.ErrorTypeStopAgentError,
		"max iterations reached",
	)
	close(ch)
	return ch, nil
}
