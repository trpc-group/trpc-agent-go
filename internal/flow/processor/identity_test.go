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

func TestIdentityProc_Request(t *testing.T) {
	tests := []struct {
		name         string
		agentName    string
		description  string
		request      *model.Request
		invocation   *agent.Invocation
		wantMessages int
		wantContent  string
	}{
		{
			name:        "adds identity with name and description",
			agentName:   "TestBot",
			description: "A helpful testing assistant",
			request: &model.Request{
				Messages: []model.Message{},
			},
			invocation: &agent.Invocation{
				AgentName:    "test-agent",
				InvocationID: "test-123",
			},
			wantMessages: 1,
			wantContent:  "You are TestBot. A helpful testing assistant",
		},
		{
			name:        "adds identity with name only",
			agentName:   "TestBot",
			description: "",
			request: &model.Request{
				Messages: []model.Message{},
			},
			invocation: &agent.Invocation{
				AgentName:    "test-agent",
				InvocationID: "test-123",
			},
			wantMessages: 1,
			wantContent:  "You are TestBot.",
		},
		{
			name:        "adds identity with description only",
			agentName:   "",
			description: "A helpful assistant",
			request: &model.Request{
				Messages: []model.Message{},
			},
			invocation: &agent.Invocation{
				AgentName:    "test-agent",
				InvocationID: "test-123",
			},
			wantMessages: 1,
			wantContent:  "A helpful assistant",
		},
		{
			name:        "no identity information",
			agentName:   "",
			description: "",
			request: &model.Request{
				Messages: []model.Message{},
			},
			invocation: &agent.Invocation{
				AgentName:    "test-agent",
				InvocationID: "test-123",
			},
			wantMessages: 0,
			wantContent:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processor := NewIdentityRequestProcessor(tt.agentName, tt.description)
			eventCh := make(chan *event.Event, 10)
			ctx := context.Background()

			processor.ProcessRequest(ctx, tt.invocation, tt.request, eventCh)

			if len(tt.request.Messages) != tt.wantMessages {
				t.Errorf("ProcessRequest() got %d messages, want %d", len(tt.request.Messages), tt.wantMessages)
			}

			// Check if identity was added correctly
			if tt.wantContent != "" && tt.wantMessages > 0 {
				found := false
				for _, msg := range tt.request.Messages {
					if msg.Role == model.RoleSystem && msg.Content == tt.wantContent {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("ProcessRequest() identity message with content '%s' not found in messages", tt.wantContent)
				}
			}
		})
	}
}

func TestIdentityProc_HasIDMsg(t *testing.T) {
	tests := []struct {
		name     string
		messages []model.Message
		identity string
		want     bool
	}{
		{
			name:     "empty messages",
			messages: []model.Message{},
			identity: "You are TestBot.",
			want:     false,
		},
		{
			name: "no matching identity",
			messages: []model.Message{
				model.NewSystemMessage("Different identity"),
			},
			identity: "You are TestBot.",
			want:     false,
		},
		{
			name: "has matching identity",
			messages: []model.Message{
				model.NewSystemMessage("You are TestBot."),
			},
			identity: "You are TestBot.",
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasIdentityMessage(tt.messages, tt.identity); got != tt.want {
				t.Errorf("hasIdentityMessage() = %v, want %v", got, tt.want)
			}
		})
	}
}
