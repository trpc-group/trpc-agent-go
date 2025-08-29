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
			if len(convertedEvent.Choices) == 0 {
				t.Fatal("Expected converted event to have choices")
			}

			actualContent := convertedEvent.Choices[0].Message.Content
			if actualContent != tt.expectedPrefix {
				t.Errorf("Expected content '%s', got '%s'", tt.expectedPrefix, actualContent)
			}
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

			if len(convertedEvent.Choices) == 0 {
				t.Fatal("Expected converted event to have choices")
			}

			actualContent := convertedEvent.Choices[0].Message.Content
			if actualContent != tt.expectedPrefix {
				t.Errorf("Expected content '%s', got '%s'", tt.expectedPrefix, actualContent)
			}
		})
	}
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

			if len(convertedEvent.Choices) == 0 {
				t.Fatal("Expected converted event to have choices")
			}

			actualContent := convertedEvent.Choices[0].Message.Content
			if actualContent != tt.expectedPrefix {
				t.Errorf("Expected content '%s', got '%s'", tt.expectedPrefix, actualContent)
			}
		})
	}
}
