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
	"errors"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type retryRunner struct {
	options []agent.RunOptions
}

func (r *retryRunner) Run(
	_ context.Context,
	_, _ string,
	_ model.Message,
	opts ...agent.RunOption,
) (<-chan *event.Event, error) {
	r.options = append(r.options, agent.NewRunOptions(opts...))
	if len(r.options) == 1 {
		return nil, errors.New("outcome unknown")
	}
	events := make(chan *event.Event)
	close(events)
	return events, nil
}

func (*retryRunner) Close() error { return nil }

func TestProcessResponseDrainsAfterFinalResponse(t *testing.T) {
	events := make(chan *event.Event)
	sent := make(chan struct{})
	go func() {
		events <- event.NewResponseEvent(
			"invocation",
			"agent",
			&model.Response{Done: true, Choices: []model.Choice{{
				Message: model.NewAssistantMessage("response"),
			}}},
		)
		events <- &event.Event{}
		close(events)
		close(sent)
	}()

	chat := &multiTurnChat{}
	if err := chat.processResponse(events); err != nil {
		t.Fatalf("process response: %v", err)
	}
	select {
	case <-sent:
	case <-time.After(time.Second):
		t.Fatal("processResponse returned without draining the event channel")
	}
}

func TestReplaceLatestMessageReusesIdentityAfterRunError(t *testing.T) {
	r := &retryRunner{}
	chat := &multiTurnChat{
		runner:       r,
		userID:       "user",
		sessionID:    "session",
		requestIDs:   map[string]string{"session": "request-old"},
		pendingEdits: make(map[string]pendingEdit),
	}
	if err := chat.replaceLatestMessage(context.Background(), "edited"); err == nil {
		t.Fatal("first replacement unexpectedly succeeded")
	}
	if err := chat.replaceLatestMessage(context.Background(), "edited"); err != nil {
		t.Fatalf("retry replacement: %v", err)
	}
	if len(r.options) != 2 {
		t.Fatalf("Run calls = %d, want 2", len(r.options))
	}
	first := r.options[0].LatestTurnReplacement
	second := r.options[1].LatestTurnReplacement
	if first == nil || second == nil {
		t.Fatal("replacement options are nil")
	}
	if *first != *second {
		t.Fatalf("retry identity changed: first=%+v second=%+v", first, second)
	}
	if _, ok := chat.pendingEdits[chat.sessionID]; ok {
		t.Fatal("pending replacement was not cleared after Runner.Run started")
	}
}
