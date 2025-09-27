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
	"time"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
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

	msgs := p.convertEventsToMessages(events, "agent-a")
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

			msgs := p.convertEventsToMessages(events, "agent-a")
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
	filtered := p.eventsInFilter(events, "main/feature")
	msgs := p.convertEventsToMessages(filtered, "agent-a")
	assert.Len(t, msgs, 1)
	assert.Equal(t, "kept", msgs[0].Content)
}

func TestContentRequestProcessor_WithAddSessionSummary_Option(t *testing.T) {
	p := NewContentRequestProcessor(WithAddSessionSummary(true))
	assert.True(t, p.AddSessionSummary)
}

func TestContentRequestProcessor_getSessionSummaryMessageWithTime(t *testing.T) {
	tests := []struct {
		name            string
		session         *session.Session
		includeContents string
		expectedMsg     *model.Message
		expectedTime    time.Time
	}{
		{
			name:            "nil session",
			session:         nil,
			includeContents: IncludeContentsFiltered,
			expectedMsg:     nil,
			expectedTime:    time.Time{},
		},
		{
			name:            "nil summaries",
			session:         &session.Session{},
			includeContents: IncludeContentsFiltered,
			expectedMsg:     nil,
			expectedTime:    time.Time{},
		},
		{
			name: "empty summary",
			session: &session.Session{
				Summaries: map[string]*session.Summary{
					"test-filter": {
						Summary:   "",
						UpdatedAt: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
					},
				},
			},
			includeContents: IncludeContentsFiltered,
			expectedMsg:     nil,
			expectedTime:    time.Time{},
		},
		{
			name: "valid summary with filtered content",
			session: &session.Session{
				Summaries: map[string]*session.Summary{
					"test-filter": {
						Summary:   "Test summary content",
						UpdatedAt: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
					},
				},
			},
			includeContents: IncludeContentsFiltered,
			expectedMsg: &model.Message{
				Role:    model.RoleSystem,
				Content: "Test summary content",
			},
			expectedTime: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
		},
		{
			name: "valid summary with all content",
			session: &session.Session{
				Summaries: map[string]*session.Summary{
					"": {
						Summary:   "Full session summary",
						UpdatedAt: time.Date(2023, 1, 1, 13, 0, 0, 0, time.UTC),
					},
					"test-filter": {
						Summary:   "Filtered summary",
						UpdatedAt: time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
					},
				},
			},
			includeContents: IncludeContentsAll,
			expectedMsg: &model.Message{
				Role:    model.RoleSystem,
				Content: "Full session summary",
			},
			expectedTime: time.Date(2023, 1, 1, 13, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewContentRequestProcessor(WithIncludeContents(tt.includeContents))

			inv := agent.NewInvocation(
				agent.WithInvocationSession(tt.session),
				agent.WithInvocationEventFilterKey("test-filter"),
			)

			msg, updatedAt := p.getSessionSummaryMessage(inv)

			if tt.expectedMsg == nil {
				assert.Nil(t, msg)
			} else {
				assert.NotNil(t, msg)
				assert.Equal(t, tt.expectedMsg.Role, msg.Role)
				assert.Equal(t, tt.expectedMsg.Content, msg.Content)
			}
			assert.Equal(t, tt.expectedTime, updatedAt)
		})
	}
}

func TestContentRequestProcessor_getFilterIncrementalMessagesWithTime(t *testing.T) {
	baseTime := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name             string
		session          *session.Session
		summaryUpdatedAt time.Time
		expectedCount    int
		expectedContent  []string
	}{
		{
			name:             "nil session",
			session:          nil,
			summaryUpdatedAt: baseTime,
			expectedCount:    0,
			expectedContent:  []string{},
		},
		{
			name: "no summaries, include all events",
			session: &session.Session{
				Events: []event.Event{
					{
						Author:    "user",
						Timestamp: baseTime.Add(-1 * time.Hour),
						Response: &model.Response{
							Choices: []model.Choice{
								{
									Message: model.Message{
										Role:    model.RoleUser,
										Content: "old message",
									},
								},
							},
						},
					},
					{
						Author:    "user",
						Timestamp: baseTime.Add(1 * time.Hour),
						Response: &model.Response{
							Choices: []model.Choice{
								{
									Message: model.Message{
										Role:    model.RoleUser,
										Content: "new message",
									},
								},
							},
						},
					},
				},
			},
			summaryUpdatedAt: time.Time{},
			expectedCount:    2,
			expectedContent:  []string{"old message", "new message"},
		},
		{
			name: "with summary, include events after summary time",
			session: &session.Session{
				Summaries: map[string]*session.Summary{
					"test-filter": {
						Summary:   "Test summary",
						UpdatedAt: baseTime.Add(-2 * time.Hour), // Older than baseTime
					},
				},
				Events: []event.Event{
					{
						Author:    "user",
						Timestamp: baseTime.Add(-1 * time.Hour),
						Response: &model.Response{
							Choices: []model.Choice{
								{
									Message: model.Message{
										Role:    model.RoleUser,
										Content: "before summary",
									},
								},
							},
						},
					},
					{
						Author:    "user",
						Timestamp: baseTime.Add(1 * time.Hour),
						Response: &model.Response{
							Choices: []model.Choice{
								{
									Message: model.Message{
										Role:    model.RoleUser,
										Content: "after summary",
									},
								},
							},
						},
					},
				},
			},
			summaryUpdatedAt: baseTime,
			expectedCount:    1,
			expectedContent:  []string{"after summary"},
		},
		{
			name: "use provided summaryUpdatedAt over session summary",
			session: &session.Session{
				Summaries: map[string]*session.Summary{
					"test-filter": {
						Summary:   "Session summary",
						UpdatedAt: baseTime.Add(-2 * time.Hour), // Older than provided time
					},
				},
				Events: []event.Event{
					{
						Author:    "user",
						Timestamp: baseTime.Add(-1 * time.Hour),
						Response: &model.Response{
							Choices: []model.Choice{
								{
									Message: model.Message{
										Role:    model.RoleUser,
										Content: "between times",
									},
								},
							},
						},
					},
					{
						Author:    "user",
						Timestamp: baseTime.Add(1 * time.Hour),
						Response: &model.Response{
							Choices: []model.Choice{
								{
									Message: model.Message{
										Role:    model.RoleUser,
										Content: "after provided time",
									},
								},
							},
						},
					},
				},
			},
			summaryUpdatedAt: baseTime,
			expectedCount:    1,
			expectedContent:  []string{"after provided time"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewContentRequestProcessor()

			inv := agent.NewInvocation(
				agent.WithInvocationSession(tt.session),
				agent.WithInvocationEventFilterKey("test-filter"),
			)

			messages := p.getFilterIncrementalMessages(inv, tt.summaryUpdatedAt)

			assert.Len(t, messages, tt.expectedCount)

			for i, expectedContent := range tt.expectedContent {
				assert.Equal(t, expectedContent, messages[i].Content)
			}
		})
	}
}
