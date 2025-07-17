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

func TestInstructionProc_Request(t *testing.T) {
	tests := []struct {
		name         string
		instruction  string
		systemPrompt string
		request      *model.Request
		invocation   *agent.Invocation
		wantMessages int
	}{
		{
			name:         "adds instruction message",
			instruction:  "Be helpful and concise",
			systemPrompt: "",
			request: &model.Request{
				Messages: []model.Message{},
			},
			invocation: &agent.Invocation{
				AgentName:    "test-agent",
				InvocationID: "test-123",
			},
			wantMessages: 1,
		},
		{
			name:         "adds system prompt message",
			instruction:  "",
			systemPrompt: "You are a helpful assistant",
			request: &model.Request{
				Messages: []model.Message{},
			},
			invocation: &agent.Invocation{
				AgentName:    "test-agent",
				InvocationID: "test-123",
			},
			wantMessages: 1,
		},
		{
			name:         "adds both instruction and system prompt as one message",
			instruction:  "Be concise",
			systemPrompt: "You are helpful",
			request: &model.Request{
				Messages: []model.Message{},
			},
			invocation: &agent.Invocation{
				AgentName:    "test-agent",
				InvocationID: "test-123",
			},
			wantMessages: 1,
		},
		{
			name:         "no instruction or system prompt provided",
			instruction:  "",
			systemPrompt: "",
			request: &model.Request{
				Messages: []model.Message{},
			},
			invocation: &agent.Invocation{
				AgentName:    "test-agent",
				InvocationID: "test-123",
			},
			wantMessages: 0,
		},
		{
			name:         "doesn't duplicate instruction when already exists",
			instruction:  "Be helpful",
			systemPrompt: "",
			request: &model.Request{
				Messages: []model.Message{
					model.NewSystemMessage("Be helpful"),
				},
			},
			invocation: &agent.Invocation{
				AgentName:    "test-agent",
				InvocationID: "test-123",
			},
			wantMessages: 1,
		},
		{
			name:         "appends instruction to existing system message",
			instruction:  "Be concise",
			systemPrompt: "",
			request: &model.Request{
				Messages: []model.Message{
					model.NewSystemMessage("You are helpful"),
				},
			},
			invocation: &agent.Invocation{
				AgentName:    "test-agent",
				InvocationID: "test-123",
			},
			wantMessages: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processor := NewInstructionRequestProcessor(tt.instruction, tt.systemPrompt)
			eventCh := make(chan *event.Event, 10)
			ctx := context.Background()

			processor.ProcessRequest(ctx, tt.invocation, tt.request, eventCh)

			if len(tt.request.Messages) != tt.wantMessages {
				t.Errorf("ProcessRequest() got %d messages, want %d", len(tt.request.Messages), tt.wantMessages)
			}

			// Check if instruction was added correctly
			if tt.instruction != "" && tt.wantMessages > 0 {
				found := false
				for _, msg := range tt.request.Messages {
					if msg.Role == model.RoleSystem && strings.Contains(msg.Content, tt.instruction) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("ProcessRequest() instruction content not found in system messages")
				}
			}

			// Check if system prompt was added correctly
			if tt.systemPrompt != "" && tt.wantMessages > 0 {
				found := false
				for _, msg := range tt.request.Messages {
					if msg.Role == model.RoleSystem && strings.Contains(msg.Content, tt.systemPrompt) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("ProcessRequest() system prompt content not found in system messages")
				}
			}
		})
	}
}

func TestFindSystemMessageIndex(t *testing.T) {
	tests := []struct {
		name     string
		messages []model.Message
		want     int
	}{
		{
			name:     "empty messages",
			messages: []model.Message{},
			want:     -1,
		},
		{
			name: "no system message",
			messages: []model.Message{
				{Role: model.RoleUser, Content: "Hello"},
			},
			want: -1,
		},
		{
			name: "has system message at start",
			messages: []model.Message{
				model.NewSystemMessage("System prompt"),
				{Role: model.RoleUser, Content: "Hello"},
			},
			want: 0,
		},
		{
			name: "has system message in middle",
			messages: []model.Message{
				{Role: model.RoleUser, Content: "Hello"},
				model.NewSystemMessage("System prompt"),
				{Role: model.RoleAssistant, Content: "Hi"},
			},
			want: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := findSystemMessageIndex(tt.messages); got != tt.want {
				t.Errorf("findSystemMessageIndex() = %v, want %v", got, tt.want)
			}
		})
	}
}
