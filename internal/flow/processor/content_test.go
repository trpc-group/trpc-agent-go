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
	"testing"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestContentRequestProcessor_WithAddContextPrefix(t *testing.T) {
	tests := []struct {
		name           string
		addPrefix      bool
		expectedPrefix string
	}{
		{
			name:           "with prefix enabled",
			addPrefix:      true,
			expectedPrefix: "For context: [test-agent] said: test content",
		},
		{
			name:           "with prefix disabled",
			addPrefix:      false,
			expectedPrefix: "test content",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create processor with the specified prefix setting.
			processor := NewContentRequestProcessor(
				WithAddContextPrefix(tt.addPrefix),
			)

			// Create a test event.
			testEvent := &event.Event{
				Author: "test-agent",
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.Message{
								Content: "test content",
							},
						},
					},
				},
			}

			// Convert the foreign event.
			convertedEvent := processor.convertForeignEvent(testEvent)

			// Check that the content matches expected.
			assert.NotEqual(t, 0, len(convertedEvent.Choices), "Expected converted event to have choices")

			actualContent := convertedEvent.Choices[0].Message.Content
			assert.Equalf(t, tt.expectedPrefix, actualContent, "Expected content '%s', got '%s'", tt.expectedPrefix, actualContent)
		})
	}
}

func TestContentRequestProcessor_DefaultBehavior(t *testing.T) {
	// Test that the default behavior includes the prefix.
	processor := NewContentRequestProcessor()

	testEvent := &event.Event{
		Author: "test-agent",
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						Content: "test content",
					},
				},
			},
		},
	}

	convertedEvent := processor.convertForeignEvent(testEvent)

	if len(convertedEvent.Choices) == 0 {
		t.Fatal("Expected converted event to have choices")
	}

	actualContent := convertedEvent.Choices[0].Message.Content
	expectedContent := "For context: [test-agent] said: test content"

	if actualContent != expectedContent {
		t.Errorf("Expected default content '%s', got '%s'", expectedContent, actualContent)
	}
}

func TestContentRequestProcessor_ToolCalls(t *testing.T) {
	tests := []struct {
		name           string
		addPrefix      bool
		expectedPrefix string
	}{
		{
			name:           "with prefix enabled",
			addPrefix:      true,
			expectedPrefix: "For context: [test-agent] called tool `test_tool` with parameters: {\"arg\":\"value\"}",
		},
		{
			name:           "with prefix disabled",
			addPrefix:      false,
			expectedPrefix: "Tool `test_tool` called with parameters: {\"arg\":\"value\"}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processor := NewContentRequestProcessor(
				WithAddContextPrefix(tt.addPrefix),
			)

			testEvent := &event.Event{
				Author: "test-agent",
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.Message{
								ToolCalls: []model.ToolCall{
									{
										Function: model.FunctionDefinitionParam{
											Name:      "test_tool",
											Arguments: []byte(`{"arg":"value"}`),
										},
									},
								},
							},
						},
					},
				},
			}

			convertedEvent := processor.convertForeignEvent(testEvent)

			assert.NotEqual(t, 0, len(convertedEvent.Choices), "Expected converted event to have choices")

			actualContent := convertedEvent.Choices[0].Message.Content
			assert.Equalf(t, tt.expectedPrefix, actualContent, "Expected content '%s', got '%s'", tt.expectedPrefix, actualContent)
		})
	}
}

func TestContentRequestProcessor_RearrangeAsyncFuncRespHist_DeduplicatesMergedResponses(t *testing.T) {
	processor := NewContentRequestProcessor()

	toolCallEvent := event.Event{
		Author: "assistant",
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{
							{
								ID:       "call_0",
								Function: model.FunctionDefinitionParam{Name: "calculator"},
							},
							{
								ID:       "call_1",
								Function: model.FunctionDefinitionParam{Name: "calculator"},
							},
						},
					},
				},
			},
		},
	}

	mergedToolResponse := event.Event{
		Author: "assistant",
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role:    model.RoleTool,
						ToolID:  "call_0",
						Content: "result 0",
					},
				},
				{
					Message: model.Message{
						Role:    model.RoleTool,
						ToolID:  "call_1",
						Content: "result 1",
					},
				},
			},
		},
	}

	result := processor.rearrangeAsyncFuncRespHist([]event.Event{toolCallEvent, mergedToolResponse})

	if len(result) != 2 {
		t.Fatalf("expected 2 events (tool call + single response), got %d", len(result))
	}

	toolResultEvent := result[1]
	resultIDs := toolResultEvent.GetToolResultIDs()
	assert.ElementsMatch(t, []string{"call_0", "call_1"}, resultIDs,
		"tool result IDs should match the original tool calls once each")
	assert.Len(t, toolResultEvent.Response.Choices, 2,
		"tool response event should contain one choice per tool ID without duplication")
}

func TestContentRequestProcessor_ToolResponses(t *testing.T) {
	tests := []struct {
		name           string
		addPrefix      bool
		expectedPrefix string
	}{
		{
			name:           "with prefix enabled",
			addPrefix:      true,
			expectedPrefix: "For context: [test-agent] said: tool result",
		},
		{
			name:           "with prefix disabled",
			addPrefix:      false,
			expectedPrefix: "tool result",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processor := NewContentRequestProcessor(
				WithAddContextPrefix(tt.addPrefix),
			)

			testEvent := &event.Event{
				Author: "test-agent",
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.Message{
								ToolID:  "test_tool",
								Content: "tool result",
							},
						},
					},
				},
			}

			convertedEvent := processor.convertForeignEvent(testEvent)

			assert.NotEqual(t, 0, len(convertedEvent.Choices), "Expected converted event to have choices")

			actualContent := convertedEvent.Choices[0].Message.Content
			assert.Equalf(t, tt.expectedPrefix, actualContent, "Expected content '%s', got '%s'", tt.expectedPrefix, actualContent)
		})
	}
}

// Tests for getContents (aka generate content pipeline).
func TestContentRequestProcessor_getContents_Basic(t *testing.T) {
	p := NewContentRequestProcessor()

	events := []event.Event{
		{
			Author: "user",
			Response: &model.Response{
				Choices: []model.Choice{
					{
						Message: model.Message{
							Role:    model.RoleUser,
							Content: "hello world",
						},
					},
				},
			},
		},
	}

	msgs := p.getContents("main", events, "agent-a")
	assert.Len(t, msgs, 1)
	assert.Equal(t, model.RoleUser, msgs[0].Role)
	assert.Equal(t, "hello world", msgs[0].Content)
}

func TestContentRequestProcessor_getContents_ForeignAgentConvert(t *testing.T) {
	tests := []struct {
		name      string
		addPrefix bool
		wantSub   string
	}{
		{
			name:      "with prefix",
			addPrefix: true,
			wantSub:   "For context:",
		},
		{
			name:      "no prefix",
			addPrefix: false,
			wantSub:   "foreign reply",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewContentRequestProcessor(
				WithAddContextPrefix(tt.addPrefix),
			)

			// Event authored by another agent should be converted to
			// user message content.
			events := []event.Event{
				{
					Author: "agent-b",
					Response: &model.Response{
						Choices: []model.Choice{
							{
								Message: model.Message{
									Role:    model.RoleAssistant,
									Content: "foreign reply",
								},
							},
						},
					},
				},
			}

			msgs := p.getContents("main", events, "agent-a")
			assert.Len(t, msgs, 1)
			assert.Equal(t, model.RoleUser, msgs[0].Role)
			assert.NotEmpty(t, msgs[0].Content)
			assert.Contains(t, msgs[0].Content, tt.wantSub)
		})
	}
}

func TestContentRequestProcessor_getContents_BranchFilter(t *testing.T) {
	p := NewContentRequestProcessor()

	events := []event.Event{
		{
			Author: "user",
			Branch: "main",
			Response: &model.Response{
				Choices: []model.Choice{
					{
						Message: model.Message{
							Role:    model.RoleUser,
							Content: "kept",
						},
					},
				},
			},
		},
		{
			Author: "user",
			Branch: "dev",
			Response: &model.Response{
				Choices: []model.Choice{
					{
						Message: model.Message{
							Role:    model.RoleUser,
							Content: "filtered",
						},
					},
				},
			},
		},
	}

	// Current branch main/feature should include events whose branch is
	// prefix of the current, i.e. "main" only.
	msgs := p.getContents("main/feature", events, "agent-a")
	assert.Len(t, msgs, 1)
	assert.Equal(t, "kept", msgs[0].Content)
}
