//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	testSystemContent = "You are helpful."
	testToolCallID    = "call_1"
	testToolName      = "search"
	testPromptMarker  = "[Tool Prompt]"
)

func TestPostToolRequestProcessor_WithoutToolResults(t *testing.T) {
	p := NewPostToolRequestProcessor()
	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage(testSystemContent),
			model.NewUserMessage("Hello"),
		},
	}

	p.ProcessRequest(context.Background(), &agent.Invocation{}, req, nil)

	require.Len(t, req.Messages, 2)
	assert.Equal(t, model.RoleSystem, req.Messages[0].Role)
	assert.Contains(t, req.Messages[0].Content, testSystemContent)
	assert.Contains(t, req.Messages[0].Content, testPromptMarker)
	assert.Contains(t, req.Messages[0].Content, "Analyze the tool result")
}

func TestPostToolRequestProcessor_WithToolResults_DefaultPrompt(
	t *testing.T,
) {
	p := NewPostToolRequestProcessor()
	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage(testSystemContent),
			model.NewUserMessage("Search for Go tutorials"),
			model.NewAssistantMessage(""),
			model.NewToolMessage(
				testToolCallID,
				testToolName,
				`{"results": ["tutorial1"]}`,
			),
		},
	}

	p.ProcessRequest(context.Background(), &agent.Invocation{}, req, nil)

	require.Len(t, req.Messages, 4)
	assert.Equal(t, model.RoleSystem, req.Messages[0].Role)
	assert.Contains(t, req.Messages[0].Content, testSystemContent)
	assert.Contains(t, req.Messages[0].Content, testPromptMarker)
	assert.Contains(t, req.Messages[0].Content, "Analyze the tool result")
}

func TestPostToolRequestProcessor_WithHistoricalToolResults(t *testing.T) {
	p := NewPostToolRequestProcessor()
	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage(testSystemContent),
			model.NewUserMessage("Search for Go tutorials"),
			{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					Type: "function",
					ID:   testToolCallID,
					Function: model.FunctionDefinitionParam{
						Name:      testToolName,
						Arguments: []byte(`{"q":"go tutorials"}`),
					},
				}},
			},
			model.NewToolMessage(
				testToolCallID,
				testToolName,
				`{"results":["tutorial1"]}`,
			),
			model.NewAssistantMessage("Here is one tutorial."),
			model.NewUserMessage("What about Rust?"),
		},
	}

	p.ProcessRequest(context.Background(), &agent.Invocation{}, req, nil)

	require.Len(t, req.Messages, 6)
	assert.Contains(t, req.Messages[0].Content, testSystemContent)
	assert.Contains(t, req.Messages[0].Content, testPromptMarker)
}

func TestPostToolRequestProcessor_StablePrefixAcrossToolLoop(
	t *testing.T,
) {
	p := NewPostToolRequestProcessor()
	firstReq := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage(testSystemContent),
			model.NewUserMessage("Search for Go tutorials"),
		},
	}
	nextReq := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage(testSystemContent),
			model.NewUserMessage("Search for Go tutorials"),
			model.NewAssistantMessage(""),
			model.NewToolMessage(testToolCallID, testToolName, "result"),
		},
	}

	p.ProcessRequest(context.Background(), &agent.Invocation{}, firstReq, nil)
	p.ProcessRequest(context.Background(), &agent.Invocation{}, nextReq, nil)

	require.Equal(
		t,
		firstReq.Messages[0].Content,
		nextReq.Messages[0].Content,
	)
}

func TestPostToolRequestProcessor_DoesNotDuplicatePrompt(t *testing.T) {
	p := NewPostToolRequestProcessor()
	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage(testSystemContent),
			model.NewUserMessage("Search something"),
		},
	}

	p.ProcessRequest(context.Background(), &agent.Invocation{}, req, nil)
	p.ProcessRequest(context.Background(), &agent.Invocation{}, req, nil)

	require.Len(t, req.Messages, 2)
	assert.Equal(t, 1, strings.Count(req.Messages[0].Content, testPromptMarker))
}

func TestPostToolRequestProcessor_CustomPrompt(t *testing.T) {
	const customPrompt = "[Tool Prompt] Be concise and direct."

	p := NewPostToolRequestProcessor(WithPostToolPrompt(customPrompt))
	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage(testSystemContent),
			model.NewUserMessage("Search something"),
		},
	}

	p.ProcessRequest(context.Background(), &agent.Invocation{}, req, nil)

	require.Len(t, req.Messages, 2)
	assert.Equal(t, model.RoleSystem, req.Messages[0].Role)
	assert.Contains(t, req.Messages[0].Content, customPrompt)
	assert.NotContains(t, req.Messages[0].Content, DefaultPostToolPrompt)
}

func TestPostToolRequestProcessor_EmptyPrompt(t *testing.T) {
	p := NewPostToolRequestProcessor(WithPostToolPrompt(""))
	req := &model.Request{
		Messages: []model.Message{
			model.NewToolMessage(testToolCallID, "fn", "data"),
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

	require.Len(t, req.Messages, 1)
	assert.Equal(t, model.RoleSystem, req.Messages[0].Role)
	assert.Contains(t, req.Messages[0].Content, testPromptMarker)
}

func TestPostToolRequestProcessor_MultipleToolResults(t *testing.T) {
	p := NewPostToolRequestProcessor()
	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage(testSystemContent),
			model.NewUserMessage("Find weather and news"),
			model.NewAssistantMessage(""),
			model.NewToolMessage(testToolCallID, "weather", "sunny"),
			model.NewToolMessage("call_2", "news", "headlines"),
		},
	}

	p.ProcessRequest(context.Background(), &agent.Invocation{}, req, nil)

	require.Len(t, req.Messages, 5)
	assert.Equal(t, model.RoleSystem, req.Messages[0].Role)
	assert.Contains(t, req.Messages[0].Content, testPromptMarker)
}

func TestPostToolRequestProcessor_NoSystemMessage(t *testing.T) {
	p := NewPostToolRequestProcessor()
	req := &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("Search something"),
			model.NewAssistantMessage(""),
			model.NewToolMessage(testToolCallID, testToolName, "result"),
		},
	}

	p.ProcessRequest(context.Background(), &agent.Invocation{}, req, nil)

	require.Len(t, req.Messages, 4)
	assert.Equal(t, model.RoleSystem, req.Messages[0].Role)
	assert.Contains(t, req.Messages[0].Content, testPromptMarker)
	assert.Equal(t, model.RoleUser, req.Messages[1].Role)
}

func TestPostToolRequestProcessor_RebuildRequestForContextCompaction(
	t *testing.T,
) {
	p := NewPostToolRequestProcessor()
	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage(testSystemContent),
			model.NewToolMessage(testToolCallID, testToolName, "result"),
		},
	}

	require.True(t, p.SupportsContextCompactionRebuild(&agent.Invocation{}))

	p.RebuildRequestForContextCompaction(
		context.Background(),
		&agent.Invocation{},
		req,
	)

	require.Len(t, req.Messages, 2)
	assert.Contains(t, req.Messages[0].Content, testPromptMarker)
}

func TestPostToolRequestProcessor_OptionsGateStableInjection(
	t *testing.T,
) {
	t.Run("before result disabled", func(t *testing.T) {
		p := NewPostToolRequestProcessor(
			WithPostToolPromptBeforeResult(false),
		)
		req := &model.Request{
			Messages: []model.Message{
				model.NewSystemMessage(testSystemContent),
				model.NewUserMessage("hello"),
			},
		}

		p.ProcessRequest(context.Background(), &agent.Invocation{}, req, nil)

		require.Len(t, req.Messages, 2)
		assert.Equal(t, testSystemContent, req.Messages[0].Content)
	})

	t.Run("system creation disabled before result", func(t *testing.T) {
		p := NewPostToolRequestProcessor(
			WithPostToolPromptCreateSystemMessage(false),
		)
		req := &model.Request{
			Messages: []model.Message{
				model.NewUserMessage("hello"),
			},
		}

		p.ProcessRequest(context.Background(), &agent.Invocation{}, req, nil)

		require.Len(t, req.Messages, 1)
		assert.Equal(t, model.RoleUser, req.Messages[0].Role)
	})

	t.Run("tool result still injects", func(t *testing.T) {
		p := NewPostToolRequestProcessor(
			WithPostToolPromptBeforeResult(false),
		)
		req := &model.Request{
			Messages: []model.Message{
				model.NewSystemMessage(testSystemContent),
				model.NewToolMessage(testToolCallID, testToolName, "result"),
			},
		}

		p.ProcessRequest(context.Background(), &agent.Invocation{}, req, nil)

		require.Len(t, req.Messages, 2)
		assert.Contains(t, req.Messages[0].Content, testPromptMarker)
	})
}

func TestPostToolRequestProcessor_EmptySystemMessage(t *testing.T) {
	p := NewPostToolRequestProcessor()
	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage(""),
			model.NewUserMessage("hello"),
		},
	}

	p.ProcessRequest(context.Background(), &agent.Invocation{}, req, nil)

	require.Len(t, req.Messages, 2)
	assert.Equal(t, DefaultPostToolPrompt, req.Messages[0].Content)
}

func TestPostToolRequestProcessor_HelperEdgeCases(t *testing.T) {
	require.False(t, hasPendingToolResultMessages([]model.Message{
		model.NewSystemMessage(testSystemContent),
	}))

	require.False(t, hasCompactedToolResultMessages(nil))
	require.False(t, hasCompactedToolResultMessages(&agent.Invocation{}))

	invocation := &agent.Invocation{}
	invocation.SetState(contentHasCompactedToolResultsStateKey, "true")
	require.False(t, hasCompactedToolResultMessages(invocation))

	invocation.SetState(contentHasCompactedToolResultsStateKey, true)
	require.True(t, hasCompactedToolResultMessages(invocation))
}
