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
			Branch: "chain",
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
			Branch: "other.branch",
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

	// Current branch chain.parallel should include events whose branch is
	// prefix of the current, i.e. "chain" only.
	msgs := p.getContents("chain.parallel", events, "agent-a")
	assert.Len(t, msgs, 1)
	assert.Equal(t, "kept", msgs[0].Content)
}

func TestContentRequestProcessor_isEventBelongsToBranch(t *testing.T) {
	p := NewContentRequestProcessor()

	tests := []struct {
		name             string
		invocationBranch string
		eventBranch      string
		expected         bool
		description      string
	}{
		// Original logic tests (backward compatibility).
		{
			name:             "empty branches",
			invocationBranch: "",
			eventBranch:      "",
			expected:         true,
			description:      "Empty branches should always return true",
		},
		{
			name:             "empty invocation branch",
			invocationBranch: "",
			eventBranch:      "some.branch",
			expected:         true,
			description:      "Empty invocation branch should see all events",
		},
		{
			name:             "empty event branch",
			invocationBranch: "some.branch",
			eventBranch:      "",
			expected:         true,
			description:      "Empty event branch should be visible to all",
		},
		{
			name:             "parent event visibility",
			invocationBranch: "root.chain.parallel.agent1",
			eventBranch:      "root.chain",
			expected:         true,
			description:      "Agent should see parent/ancestor events",
		},
		{
			name:             "self event visibility",
			invocationBranch: "root.chain.parallel.agent1",
			eventBranch:      "root.chain.parallel.agent1",
			expected:         true,
			description:      "Agent should see its own events",
		},

		// New logic tests (Sequential sees sub-agents).
		{
			name:             "sequential sees parallel sub-agents",
			invocationBranch: "root.chain.sequential",
			eventBranch:      "root.chain.sequential.parallel.agent1",
			expected:         true,
			description:      "Sequential agent should see its parallel sub-agent events",
		},
		{
			name:             "chain sees nested parallel agents",
			invocationBranch: "root.chain",
			eventBranch:      "root.chain.parallel.agent1",
			expected:         true,
			description:      "Chain agent should see nested parallel agent events",
		},
		{
			name:             "deep nesting sub-branch visibility",
			invocationBranch: "root",
			eventBranch:      "root.outerParallel.innerSequential.deepParallel.agent1",
			expected:         true,
			description:      "Root agent should see deeply nested sub-agent events",
		},

		// Isolation tests (parallel agents should not see each other).
		{
			name:             "parallel agent isolation",
			invocationBranch: "root.chain.parallel.agent1",
			eventBranch:      "root.chain.parallel.agent2",
			expected:         false,
			description:      "Parallel agents should not see each other's events",
		},
		{
			name:             "cross-branch isolation",
			invocationBranch: "root.branchA.agent1",
			eventBranch:      "root.branchB.agent2",
			expected:         false,
			description:      "Agents in different branches should not see each other",
		},
		{
			name:             "no common prefix",
			invocationBranch: "teamA.sequential",
			eventBranch:      "teamB.parallel.agent1",
			expected:         false,
			description:      "Agents with no common prefix should not see each other",
		},

		// Edge cases.
		{
			name:             "same depth different branch",
			invocationBranch: "root.teamA.agent1",
			eventBranch:      "root.teamB.agent2",
			expected:         false,
			description:      "Same depth but different branches should be isolated",
		},
		{
			name:             "partial prefix match",
			invocationBranch: "root.chainABC",
			eventBranch:      "root.chainA.agent1",
			expected:         false,
			description:      "Partial prefix matches should not be allowed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a test event with the specified branch.
			testEvent := &event.Event{
				Branch: tt.eventBranch,
			}

			// Test the branch filtering logic.
			result := p.isEventBelongsToBranch(tt.invocationBranch, testEvent)

			assert.Equal(t, tt.expected, result,
				"isEventBelongsToBranch(%q, %q) = %v, expected %v. %s",
				tt.invocationBranch, tt.eventBranch, result, tt.expected, tt.description)
		})
	}
}
