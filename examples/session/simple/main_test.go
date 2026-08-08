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
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

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
