//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestBasicReqProc_ProcessReq(t *testing.T) {
	tests := []struct {
		name       string
		processor  *BasicRequestProcessor
		request    *model.Request
		invocation *agent.Invocation
		wantStream bool
	}{
		{
			name: "sets generation config",
			processor: NewBasicRequestProcessor(
				WithGenerationConfig(model.GenerationConfig{
					MaxTokens:   intPtr(100),
					Temperature: floatPtr(0.7),
					Stream:      false,
				}),
			),
			request: &model.Request{
				Messages: []model.Message{},
			},
			invocation: &agent.Invocation{
				AgentName:    "test-agent",
				InvocationID: "test-123",
			},
			wantStream: false,
		},
		{
			name:      "run options override stream false",
			processor: NewBasicRequestProcessor(),
			request: &model.Request{
				Messages: []model.Message{},
			},
			invocation: &agent.Invocation{
				AgentName:    "test-agent",
				InvocationID: "test-123",
				RunOptions: agent.RunOptions{
					Stream: boolPtr(false),
				},
			},
			wantStream: false,
		},
		{
			name: "run options override stream true",
			processor: NewBasicRequestProcessor(
				WithGenerationConfig(model.GenerationConfig{Stream: false}),
			),
			request: &model.Request{
				Messages: []model.Message{},
			},
			invocation: &agent.Invocation{
				AgentName:    "test-agent",
				InvocationID: "test-123",
				RunOptions: agent.RunOptions{
					Stream: boolPtr(true),
				},
			},
			wantStream: true,
		},
		{
			name:      "default stream setting",
			processor: NewBasicRequestProcessor(),
			request: &model.Request{
				Messages: []model.Message{},
			},
			invocation: &agent.Invocation{
				AgentName:    "test-agent",
				InvocationID: "test-123",
			},
			wantStream: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eventCh := make(chan *event.Event, 10)
			ctx := context.Background()

			tt.processor.ProcessRequest(ctx, tt.invocation, tt.request, eventCh)

			if tt.request.Stream != tt.wantStream {
				t.Errorf("ProcessRequest() got stream %t, want %t", tt.request.Stream, tt.wantStream)
			}

			// Verify that an event was sent.
			select {
			case evt := <-eventCh:
				if evt.Object != "preprocessing.basic" {
					t.Errorf("ProcessRequest() got event object %s, want preprocessing.basic", evt.Object)
				}
			default:
				t.Error("ProcessRequest() expected an event to be sent")
			}
		})
	}
}

// Helper functions for test data
func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}

func boolPtr(b bool) *bool {
	return &b
}

// Cover early-return branches for robustness and full coverage.
func TestBasicReqProc_NilRequestAndInvocation(t *testing.T) {
	p := NewBasicRequestProcessor()
	ch := make(chan *event.Event, 1)

	// Nil request -> returns immediately without panic.
	p.ProcessRequest(context.Background(), &agent.Invocation{AgentName: "a"}, nil, ch)

	// Nil invocation -> no event, but should not panic.
	p.ProcessRequest(context.Background(), nil, &model.Request{}, ch)
}
