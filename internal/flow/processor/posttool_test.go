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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestPostToolRequestProcessor_NoToolResults(t *testing.T) {
	p := NewPostToolRequestProcessor()
	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("You are helpful."),
			model.NewUserMessage("Hello"),
		},
	}
	originalLen := len(req.Messages)
	p.ProcessRequest(context.Background(), &agent.Invocation{}, req, nil)

	assert.Len(t, req.Messages, originalLen)
	assert.Equal(t, "You are helpful.", req.Messages[0].Content)
}

func TestPostToolRequestProcessor_WithToolResults_DefaultPrompt(t *testing.T) {
	p := NewPostToolRequestProcessor()
	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("You are helpful."),
			model.NewUserMessage("Search for Go tutorials"),
			model.NewAssistantMessage(""),
			model.NewToolMessage("call_1", "search", `{"results": ["tutorial1"]}`),
		},
	}
	p.ProcessRequest(context.Background(), &agent.Invocation{}, req, nil)

	// Messages count should remain the same; prompt is appended to system message.
	require.Len(t, req.Messages, 4)
	assert.Equal(t, model.RoleSystem, req.Messages[0].Role)
	assert.Contains(t, req.Messages[0].Content, "You are helpful.")
	assert.Contains(t, req.Messages[0].Content, "[Tool Prompt]")
	assert.Contains(t, req.Messages[0].Content, "Analyze the tool result")
}

func TestPostToolRequestProcessor_WithToolResults_CustomPrompt(t *testing.T) {
	customPrompt := "[Tool Prompt] Be concise and direct."
	p := NewPostToolRequestProcessor(WithPostToolPrompt(customPrompt))
	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("You are helpful."),
			model.NewToolMessage("call_1", "search", "result"),
		},
	}
	p.ProcessRequest(context.Background(), &agent.Invocation{}, req, nil)

	require.Len(t, req.Messages, 2)
	assert.Equal(t, model.RoleSystem, req.Messages[0].Role)
	assert.Contains(t, req.Messages[0].Content, customPrompt)
}

func TestPostToolRequestProcessor_EmptyPrompt(t *testing.T) {
	p := NewPostToolRequestProcessor(WithPostToolPrompt(""))
	req := &model.Request{
		Messages: []model.Message{
			model.NewToolMessage("call_1", "fn", "data"),
		},
	}
	p.ProcessRequest(context.Background(), &agent.Invocation{}, req, nil)

	assert.Len(t, req.Messages, 1)
}

func TestPostToolRequestProcessor_NilRequest(t *testing.T) {
	p := NewPostToolRequestProcessor()
	p.ProcessRequest(context.Background(), &agent.Invocation{}, nil, nil)
}

func TestPostToolRequestProcessor_EmptyMessages(t *testing.T) {
	p := NewPostToolRequestProcessor()
	req := &model.Request{}
	p.ProcessRequest(context.Background(), &agent.Invocation{}, req, nil)
	assert.Empty(t, req.Messages)
}

func TestPostToolRequestProcessor_MultipleToolResults(t *testing.T) {
	p := NewPostToolRequestProcessor()
	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("You are helpful."),
			model.NewUserMessage("Find weather and news"),
			model.NewAssistantMessage(""),
			model.NewToolMessage("call_1", "weather", "sunny"),
			model.NewToolMessage("call_2", "news", "headlines"),
		},
	}
	p.ProcessRequest(context.Background(), &agent.Invocation{}, req, nil)

	// Messages count unchanged; prompt appended to system message.
	require.Len(t, req.Messages, 5)
	assert.Equal(t, model.RoleSystem, req.Messages[0].Role)
	assert.Contains(t, req.Messages[0].Content, "[Tool Prompt]")
}

func TestPostToolRequestProcessor_NoSystemMessage(t *testing.T) {
	p := NewPostToolRequestProcessor()
	req := &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("Search something"),
			model.NewAssistantMessage(""),
			model.NewToolMessage("call_1", "search", "result"),
		},
	}
	p.ProcessRequest(context.Background(), &agent.Invocation{}, req, nil)

	// A new system message should be prepended.
	require.Len(t, req.Messages, 4)
	assert.Equal(t, model.RoleSystem, req.Messages[0].Role)
	assert.Contains(t, req.Messages[0].Content, "[Tool Prompt]")
	// Original messages shifted.
	assert.Equal(t, model.RoleUser, req.Messages[1].Role)
}

func TestHasToolResultMessages(t *testing.T) {
	tests := []struct {
		name     string
		messages []model.Message
		want     bool
	}{
		{
			name:     "empty",
			messages: nil,
			want:     false,
		},
		{
			name: "no tool messages",
			messages: []model.Message{
				model.NewUserMessage("hi"),
				model.NewAssistantMessage("hello"),
			},
			want: false,
		},
		{
			name: "has tool message",
			messages: []model.Message{
				model.NewUserMessage("hi"),
				model.NewToolMessage("id", "fn", "result"),
			},
			want: true,
		},
		{
			name: "tool message in middle",
			messages: []model.Message{
				model.NewUserMessage("hi"),
				model.NewToolMessage("id", "fn", "result"),
				model.NewAssistantMessage("done"),
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasToolResultMessages(tt.messages)
			assert.Equal(t, tt.want, got)
		})
	}
}
