//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"context"
	"fmt"
	"time"

	officialagent "trpc.group/trpc-go/trpc-agent-go/agent"
	agentevent "trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
	agentmodel "trpc.group/trpc-go/trpc-agent-go/model"
	agentrunner "trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const officialReviewAgentName = "cr-agent"
const runnerEventBufferSize = 16

// RunWithEvents 通过官方 Runner 执行一次 review，并返回 event.Event 流。
// 内部仍复用本项目 runDirect，避免为了接 Runner 破坏报告、SQLite 和 fixture contract。
func (a *Agent) RunWithEvents(ctx context.Context, req Request) (<-chan *agentevent.Event, error) {
	if a == nil {
		return nil, fmt.Errorf("agent is required")
	}
	adapter := reviewRunnerAgent{base: a, req: req}
	r := agentrunner.NewRunner("cr-agent", adapter, agentrunner.WithArtifactService(a.artifactService))
	sessionID := fmt.Sprintf("review-%d", time.Now().UnixNano())
	events, err := r.Run(
		ctx,
		"local",
		sessionID,
		agentmodel.NewUserMessage("run code review"),
		officialagent.WithRequestID(sessionID),
	)
	if err != nil {
		_ = r.Close()
		return nil, err
	}
	out := make(chan *agentevent.Event, runnerEventBufferSize)
	go func() {
		defer close(out)
		defer r.Close()
		for ev := range events {
			if !forwardRunnerEvent(ctx, out, ev) {
				return
			}
		}
	}()
	return out, nil
}

type reviewRunnerAgent struct {
	base *Agent
	req  Request
}

func (a reviewRunnerAgent) Run(ctx context.Context, invocation *officialagent.Invocation) (<-chan *agentevent.Event, error) {
	if a.base == nil {
		return nil, fmt.Errorf("agent is required")
	}
	events := make(chan *agentevent.Event, runnerEventBufferSize)
	local := *a.base
	originalSink := local.cfg.EventSink
	local.cfg.EventSink = func(ctx context.Context, ev *agentevent.Event) {
		_ = ctx
		if ev == nil {
			return
		}
		if originalSink != nil {
			originalSink(ctx, ev.Clone())
		}
		officialagent.InjectIntoEvent(invocation, ev)
		_ = forwardRunnerEvent(ctx, events, ev)
	}
	go func() {
		defer close(events)
		if _, err := local.runDirect(ctx, a.req); err != nil {
			ev := agentevent.NewErrorEvent("", officialReviewAgentName, "run_error", review.RedactSecrets(err.Error()))
			officialagent.InjectIntoEvent(invocation, ev)
			_ = forwardRunnerEvent(ctx, events, ev)
		}
	}()
	_ = invocation
	return events, nil
}

func (a reviewRunnerAgent) Tools() []tool.Tool {
	return nil
}

func (a reviewRunnerAgent) Info() officialagent.Info {
	return officialagent.Info{
		Name:        officialReviewAgentName,
		Description: "Runs the CR Agent review pipeline and emits official review events.",
	}
}

func (a reviewRunnerAgent) SubAgents() []officialagent.Agent {
	return nil
}

func (a reviewRunnerAgent) FindSubAgent(name string) officialagent.Agent {
	_ = name
	return nil
}

func forwardRunnerEvent(ctx context.Context, ch chan<- *agentevent.Event, ev *agentevent.Event) bool {
	if ev == nil {
		return true
	}
	select {
	case ch <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}
