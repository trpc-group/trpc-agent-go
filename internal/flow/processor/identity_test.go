//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
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
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestIdentityProc_Request(t *testing.T) {
	invocation := &agent.Invocation{
		AgentName:    "test-agent",
		InvocationID: "test-123",
	}

	tests := []struct {
		name         string
		agentName    string
		description  string
		messages     []model.Message
		wantMessages int
		wantContent  string
	}{
		{
			name:         "adds identity with name and description",
			agentName:    "TestBot",
			description:  "A helpful testing assistant",
			messages:     []model.Message{},
			wantMessages: 1,
			wantContent:  "You are TestBot. A helpful testing assistant",
		},
		{
			name:         "adds identity with name only",
			agentName:    "TestBot",
			description:  "",
			messages:     []model.Message{},
			wantMessages: 1,
			wantContent:  "You are TestBot.",
		},
		{
			name:         "adds identity with description only",
			agentName:    "",
			description:  "A helpful assistant",
			messages:     []model.Message{},
			wantMessages: 1,
			wantContent:  "A helpful assistant",
		},
		{
			name:         "no identity information",
			agentName:    "",
			description:  "",
			messages:     []model.Message{},
			wantMessages: 0,
			wantContent:  "",
		},
		{
			name:         "prepends identity to existing system message",
			agentName:    "TestBot",
			description:  "A helpful assistant",
			messages:     []model.Message{model.NewSystemMessage("You have access to tools.")},
			wantMessages: 1,
			wantContent:  "You are TestBot. A helpful assistant",
		},
		{
			name:         "doesn't duplicate identity when already exists",
			agentName:    "TestBot",
			description:  "",
			messages:     []model.Message{model.NewSystemMessage("You are TestBot. You have access to tools.")},
			wantMessages: 1,
			wantContent:  "You are TestBot.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processor := NewIdentityRequestProcessor(tt.agentName, tt.description)
			eventCh := make(chan *event.Event, 10)
			ctx := context.Background()

			request := &model.Request{Messages: tt.messages}
			processor.ProcessRequest(ctx, invocation, request, eventCh)

			if len(request.Messages) != tt.wantMessages {
				t.Errorf("ProcessRequest() got %d messages, want %d", len(request.Messages), tt.wantMessages)
			}

			// Check if identity was added correctly
			if tt.wantContent != "" && tt.wantMessages > 0 {
				found := false
				for _, msg := range request.Messages {
					if msg.Role == model.RoleSystem && strings.Contains(msg.Content, tt.wantContent) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("ProcessRequest() identity content '%s' not found in system messages", tt.wantContent)
				}
			}
		})
	}
}
