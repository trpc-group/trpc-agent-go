//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
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
