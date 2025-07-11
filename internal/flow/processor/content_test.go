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
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestNewContentRequestProcessor(t *testing.T) {
	processor := NewContentRequestProcessor()

	assert.NotNil(t, processor)
	assert.Equal(t, IncludeContentsAll, processor.IncludeContents)
}

func TestContentProc_HasValidContent(t *testing.T) {
	processor := NewContentRequestProcessor()

	// Event with message content.
	eventWithContent := event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Index: 0,
					Message: model.Message{
						Role:    model.RoleUser,
						Content: "Test content",
					},
				},
			},
			Done: true,
		},
	}

	// Event with delta content.
	eventWithDelta := event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Index: 0,
					Delta: model.Message{
						Role:    model.RoleAssistant,
						Content: "Delta content",
					},
				},
			},
			Done: true,
		},
	}

	// Event with tool calls.
	eventWithToolCalls := event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Index: 0,
					Message: model.Message{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{
							{
								Type: "function",
								Function: model.FunctionDefinitionParam{
									Name: "test_function",
								},
								ID: "call-123",
							},
						},
					},
				},
			},
			Done: true,
		},
	}

	// Event without content.
	eventWithoutContent := event.Event{
		Response: &model.Response{
			Done: true,
		},
	}

	assert.True(t, processor.hasValidContent(&eventWithContent))
	assert.True(t, processor.hasValidContent(&eventWithDelta))
	assert.True(t, processor.hasValidContent(&eventWithToolCalls))
	assert.False(t, processor.hasValidContent(&eventWithoutContent))
}
