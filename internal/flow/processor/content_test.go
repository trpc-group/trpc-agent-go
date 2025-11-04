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
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
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
			assert.NotEqual(t, 0, len(convertedEvent.Response.Choices), "Expected converted event to have choices")

			actualContent := convertedEvent.Response.Choices[0].Message.Content
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

	if len(convertedEvent.Response.Choices) == 0 {
		t.Fatal("Expected converted event to have choices")
	}

	actualContent := convertedEvent.Response.Choices[0].Message.Content
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

			assert.NotEqual(t, 0, len(convertedEvent.Response.Choices), "Expected converted event to have choices")

			actualContent := convertedEvent.Response.Choices[0].Message.Content
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

			assert.NotEqual(t, 0, len(convertedEvent.Response.Choices), "Expected converted event to have choices")

			actualContent := convertedEvent.Response.Choices[0].Message.Content
			assert.Equalf(t, tt.expectedPrefix, actualContent, "Expected content '%s', got '%s'", tt.expectedPrefix, actualContent)
		})
	}
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
			includeContents: IncludeContentFilterKeyPrefix,

			expectedMsg:  nil,
			expectedTime: time.Time{},
		},
		{
			name:            "nil summaries",
			session:         &session.Session{},
			includeContents: IncludeContentFilterKeyPrefix,
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
			includeContents: IncludeContentFilterKeyPrefix,
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
			includeContents: IncludeContentFilterKeyPrefix,
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
			includeContents: IncludeContentFilterKeyAll,
			expectedMsg: &model.Message{
				Role:    model.RoleSystem,
				Content: "Full session summary",
			},
			expectedTime: time.Date(2023, 1, 1, 13, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewContentRequestProcessor(WithIncludeContentFilterMode(tt.includeContents))

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

			messages := p.getIncrementMessages(inv, tt.summaryUpdatedAt)

			assert.Len(t, messages, tt.expectedCount)

			for i, expectedContent := range tt.expectedContent {
				assert.Equal(t, expectedContent, messages[i].Content)
			}
		})
	}
}

func TestContentRequestProcessor_ConcurrentSummariesAccess(t *testing.T) {
	// Test concurrent access to Summaries field to ensure thread safety.
	baseTime := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)

	// Create a session with initial summaries.
	sess := &session.Session{
		Summaries: map[string]*session.Summary{
			"test-filter": {
				Summary:   "Initial summary",
				UpdatedAt: baseTime,
			},
		},
	}

	// Create processor.
	p := NewContentRequestProcessor()

	// Test basic functionality first
	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("test-filter"),
	)

	// Test single read
	msg, updatedAt := p.getSessionSummaryMessage(inv)
	assert.NotNil(t, msg, "Should get summary message")
	assert.Equal(t, "Initial summary", msg.Content)
	assert.Equal(t, baseTime, updatedAt)

	// Test single write
	sess.SummariesMu.Lock()
	sess.Summaries["test-filter"] = &session.Summary{
		Summary:   "Updated summary",
		UpdatedAt: baseTime.Add(time.Second),
	}
	sess.SummariesMu.Unlock()

	// Test read after write
	msg, updatedAt = p.getSessionSummaryMessage(inv)
	assert.NotNil(t, msg, "Should get updated summary message")
	assert.Equal(t, "Updated summary", msg.Content)
	assert.Equal(t, baseTime.Add(time.Second), updatedAt)

	// Test with minimal concurrency (2 goroutines only)
	var wg sync.WaitGroup
	results := make(chan bool, 4)

	// 2 readers
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			msg, _ := p.getSessionSummaryMessage(inv)
			results <- msg != nil
		}()
	}

	// 2 writers
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sess.SummariesMu.Lock()
			sess.Summaries["test-filter"] = &session.Summary{
				Summary:   "Concurrent summary",
				UpdatedAt: baseTime.Add(time.Duration(i) * time.Second),
			}
			sess.SummariesMu.Unlock()
			results <- true
		}(i)
	}

	// Wait with timeout
	done := make(chan bool)
	go func() {
		wg.Wait()
		close(results)
		done <- true
	}()

	select {
	case <-done:
		// Test completed successfully
	case <-time.After(5 * time.Second):
		t.Fatal("Test timed out - possible deadlock")
	}

	// Check results
	successCount := 0
	for result := range results {
		if result {
			successCount++
		}
	}

	assert.Equal(t, 4, successCount, "All operations should succeed")
}

func TestContentRequestProcessor_ConcurrentFilterIncrementalMessages(t *testing.T) {
	// Test concurrent access to getFilterIncrementalMessages.
	baseTime := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)

	// Create a session with summaries and events.
	sess := &session.Session{
		Summaries: map[string]*session.Summary{
			"test-filter": {
				Summary:   "Test summary",
				UpdatedAt: baseTime,
			},
		},
		Events: []event.Event{
			{
				Author:    "user",
				Timestamp: baseTime.Add(1 * time.Hour),
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.Message{
								Role:    model.RoleUser,
								Content: "test message",
							},
						},
					},
				},
			},
		},
	}

	// Create processor.
	p := NewContentRequestProcessor()

	// Test basic functionality first
	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationEventFilterKey("test-filter"),
	)

	// Test single call
	messages := p.getIncrementMessages(inv, time.Time{})
	assert.Len(t, messages, 1, "Should get one message")
	assert.Equal(t, "test message", messages[0].Content)

	// Test with minimal concurrency (2 goroutines only)
	var wg sync.WaitGroup
	results := make(chan int, 4)

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			messages := p.getIncrementMessages(inv, time.Time{})
			results <- len(messages)
		}()
	}

	// Wait with timeout
	done := make(chan bool)
	go func() {
		wg.Wait()
		close(results)
		done <- true
	}()

	select {
	case <-done:
		// Test completed successfully
	case <-time.After(5 * time.Second):
		t.Fatal("Test timed out - possible deadlock")
	}

	// Check results
	totalMessages := 0
	for count := range results {
		totalMessages++
		assert.Equal(t, 1, count, "Each call should return 1 message")
	}

	assert.Equal(t, 2, totalMessages, "Should have 2 calls")
}

func TestSession_SummariesConcurrentAccess(t *testing.T) {
	// Test direct concurrent access to Session.Summaries.
	sess := &session.Session{
		Summaries: make(map[string]*session.Summary),
	}

	// Test basic functionality first
	sess.SummariesMu.Lock()
	sess.Summaries["test-key"] = &session.Summary{
		Summary:   "Test summary",
		UpdatedAt: time.Now(),
	}
	sess.SummariesMu.Unlock()

	// Test read
	sess.SummariesMu.RLock()
	summary := sess.Summaries["test-key"]
	sess.SummariesMu.RUnlock()
	assert.NotNil(t, summary, "Should get summary")
	assert.Equal(t, "Test summary", summary.Summary)

	// Test with minimal concurrency (2 goroutines only)
	var wg sync.WaitGroup
	results := make(chan bool, 4)

	// 2 readers
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sess.SummariesMu.RLock()
			summary := sess.Summaries["test-key"]
			sess.SummariesMu.RUnlock()
			results <- summary != nil
		}()
	}

	// 2 writers
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sess.SummariesMu.Lock()
			sess.Summaries["test-key"] = &session.Summary{
				Summary:   "Concurrent summary",
				UpdatedAt: time.Now(),
			}
			sess.SummariesMu.Unlock()
			results <- true
		}(i)
	}

	// Wait with timeout
	done := make(chan bool)
	go func() {
		wg.Wait()
		close(results)
		done <- true
	}()

	select {
	case <-done:
		// Test completed successfully
	case <-time.After(5 * time.Second):
		t.Fatal("Test timed out - possible deadlock")
	}

	// Check results
	successCount := 0
	for result := range results {
		if result {
			successCount++
		}
	}

	assert.Equal(t, 4, successCount, "All operations should succeed")

	// Verify final state is consistent.
	sess.SummariesMu.RLock()
	finalSummary := sess.Summaries["test-key"]
	sess.SummariesMu.RUnlock()
	assert.NotNil(t, finalSummary, "Final summary should exist")
}

func TestContentRequestProcessor_WithMaxHistoryRuns_Option(t *testing.T) {
	p := NewContentRequestProcessor(WithMaxHistoryRuns(5))
	assert.Equal(t, 5, p.MaxHistoryRuns)
}

func TestContentRequestProcessor_getFilterHistoryMessages(t *testing.T) {
	baseTime := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name            string
		processor       *ContentRequestProcessor
		session         *session.Session
		expectedCount   int
		expectedContent []string
	}{
		{
			name:            "nil session",
			processor:       NewContentRequestProcessor(WithMaxHistoryRuns(3)),
			session:         nil,
			expectedCount:   0,
			expectedContent: []string{},
		},
		{
			name:      "no MaxHistoryRuns limit",
			processor: NewContentRequestProcessor(WithMaxHistoryRuns(0)),
			session: &session.Session{
				Events: []event.Event{
					createTestEvent("user", "message1", baseTime.Add(-2*time.Hour)),
					createTestEvent("user", "message2", baseTime.Add(-1*time.Hour)),
					createTestEvent("user", "message3", baseTime),
				},
			},
			expectedCount:   3,
			expectedContent: []string{"message1", "message2", "message3"},
		},
		{
			name:      "with MaxHistoryRuns limit",
			processor: NewContentRequestProcessor(WithMaxHistoryRuns(2)),
			session: &session.Session{
				Events: []event.Event{
					createTestEvent("user", "message1", baseTime.Add(-2*time.Hour)),
					createTestEvent("user", "message2", baseTime.Add(-1*time.Hour)),
					createTestEvent("user", "message3", baseTime),
				},
			},
			expectedCount:   2,
			expectedContent: []string{"message2", "message3"},
		},
		{
			name:      "MaxHistoryRuns greater than total messages",
			processor: NewContentRequestProcessor(WithMaxHistoryRuns(5)),
			session: &session.Session{
				Events: []event.Event{
					createTestEvent("user", "message1", baseTime.Add(-2*time.Hour)),
					createTestEvent("user", "message2", baseTime.Add(-1*time.Hour)),
				},
			},
			expectedCount:   2,
			expectedContent: []string{"message1", "message2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inv := agent.NewInvocation(
				agent.WithInvocationSession(tt.session),
				agent.WithInvocationEventFilterKey("test-filter"),
			)

			messages := tt.processor.getIncrementMessages(inv, time.Time{})

			assert.Equal(t, tt.expectedCount, len(messages))
			for i, expectedContent := range tt.expectedContent {
				assert.Equal(t, expectedContent, messages[i].Content)
			}
		})
	}
}

func TestContentRequestProcessor_ProcessRequest_WithMaxHistoryRuns(t *testing.T) {
	baseTime := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name          string
		processor     *ContentRequestProcessor
		session       *session.Session
		expectedCount int
	}{
		{
			name: "AddSessionSummary true - uses incremental messages",
			processor: NewContentRequestProcessor(
				WithAddSessionSummary(true),
				WithMaxHistoryRuns(2),
			),
			session: &session.Session{
				Events: []event.Event{
					createTestEvent("user", "message1", baseTime.Add(-2*time.Hour)),
					createTestEvent("user", "message2", baseTime.Add(-1*time.Hour)),
					createTestEvent("user", "message3", baseTime),
				},
				Summaries: map[string]*session.Summary{
					"test-filter": {
						Summary:   "Test summary",
						UpdatedAt: baseTime.Add(-3 * time.Hour), // Earlier than all events
					},
				},
			},
			expectedCount: 4, // Summary message + 3 events (incremental logic)
		},
		{
			name: "AddSessionSummary false - uses history messages with limit",
			processor: NewContentRequestProcessor(
				WithAddSessionSummary(false),
				WithMaxHistoryRuns(2),
			),
			session: &session.Session{
				Events: []event.Event{
					createTestEvent("user", "message1", baseTime.Add(-2*time.Hour)),
					createTestEvent("user", "message2", baseTime.Add(-1*time.Hour)),
					createTestEvent("user", "message3", baseTime),
				},
			},
			expectedCount: 2, // Limited by MaxHistoryRuns
		},
		{
			name: "AddSessionSummary false - no MaxHistoryRuns limit",
			processor: NewContentRequestProcessor(
				WithAddSessionSummary(false),
				WithMaxHistoryRuns(0),
			),
			session: &session.Session{
				Events: []event.Event{
					createTestEvent("user", "message1", baseTime.Add(-2*time.Hour)),
					createTestEvent("user", "message2", baseTime.Add(-1*time.Hour)),
					createTestEvent("user", "message3", baseTime),
				},
			},
			expectedCount: 3, // All events included (no limit)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inv := agent.NewInvocation(
				agent.WithInvocationSession(tt.session),
				agent.WithInvocationEventFilterKey("test-filter"),
			)

			req := &model.Request{
				Messages: []model.Message{},
			}

			tt.processor.ProcessRequest(context.Background(), inv, req, nil)

			// Count all messages (including summary if any)
			messageCount := len(req.Messages)

			assert.Equal(t, tt.expectedCount, messageCount)
		})
	}
}

// Helper function to create test events
func createTestEvent(author, content string, timestamp time.Time) event.Event {
	return event.Event{
		Author:    author,
		Timestamp: timestamp,
		FilterKey: "test-filter", // Set filter key to match test expectations
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role:    model.RoleUser,
						Content: content,
					},
				},
			},
		},
	}
}

// TestContentRequestProcessor_Integration_MaxHistoryRunsAndAddSessionSummary tests the interaction
// between MaxHistoryRuns and AddSessionSummary settings.
func TestContentRequestProcessor_Integration_MaxHistoryRunsAndAddSessionSummary(t *testing.T) {
	baseTime := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name              string
		addSessionSummary bool
		maxHistoryRuns    int
		session           *session.Session
		expectedCount     int
		description       string
	}{
		{
			name:              "AddSessionSummary=true ignores MaxHistoryRuns",
			addSessionSummary: true,
			maxHistoryRuns:    2, // Should be ignored
			session: &session.Session{
				Events: []event.Event{
					createTestEvent("user", "message1", baseTime.Add(-2*time.Hour)),
					createTestEvent("user", "message2", baseTime.Add(-1*time.Hour)),
					createTestEvent("user", "message3", baseTime),
				},
			},
			expectedCount: 3, // All events included (incremental logic)
			description:   "When AddSessionSummary=true, MaxHistoryRuns should be ignored and all events included",
		},
		{
			name:              "AddSessionSummary=false with MaxHistoryRuns=0 includes all",
			addSessionSummary: false,
			maxHistoryRuns:    0,
			session: &session.Session{
				Events: []event.Event{
					createTestEvent("user", "message1", baseTime.Add(-2*time.Hour)),
					createTestEvent("user", "message2", baseTime.Add(-1*time.Hour)),
					createTestEvent("user", "message3", baseTime),
				},
			},
			expectedCount: 3, // All events included (no limit)
			description:   "When AddSessionSummary=false and MaxHistoryRuns=0, all events should be included",
		},
		{
			name:              "AddSessionSummary=false with MaxHistoryRuns=2 limits to 2",
			addSessionSummary: false,
			maxHistoryRuns:    2,
			session: &session.Session{
				Events: []event.Event{
					createTestEvent("user", "message1", baseTime.Add(-2*time.Hour)),
					createTestEvent("user", "message2", baseTime.Add(-1*time.Hour)),
					createTestEvent("user", "message3", baseTime),
				},
			},
			expectedCount: 2, // Limited to last 2 messages
			description:   "When AddSessionSummary=false and MaxHistoryRuns=2, only last 2 messages should be included",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processor := NewContentRequestProcessor(
				WithAddSessionSummary(tt.addSessionSummary),
				WithMaxHistoryRuns(tt.maxHistoryRuns),
			)

			inv := agent.NewInvocation(
				agent.WithInvocationSession(tt.session),
				agent.WithInvocationEventFilterKey("test-filter"),
			)

			req := &model.Request{
				Messages: []model.Message{},
			}

			processor.ProcessRequest(context.Background(), inv, req, nil)

			// Count non-system messages (excluding summary if any)
			var messageCount int
			for _, msg := range req.Messages {
				if msg.Role != model.RoleSystem {
					messageCount++
				}
			}

			assert.Equal(t, tt.expectedCount, messageCount, tt.description)
		})
	}
}

func TestWithAppendHistoryMessage(t *testing.T) {
	type args struct {
		append bool
	}
	tests := []struct {
		name string
		args args
		want ContentOption
	}{
		{
			name: "set to true",
			args: args{append: true},
			want: func(p *ContentRequestProcessor) {
				p.AppendHistoryMessage = true
			},
		},
		{
			name: "set to false",
			args: args{append: false},
			want: func(p *ContentRequestProcessor) {
				p.AppendHistoryMessage = false
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			processor := &ContentRequestProcessor{}

			got := WithAppendHistoryMessage(tt.args.append)

			got(processor)

			if processor.AppendHistoryMessage != tt.args.append {
				t.Errorf("AppendHistoryMessage = %v, want %v",
					processor.AppendHistoryMessage, tt.args.append)
			}
		})
	}
}

func Test_toMap(t *testing.T) {
	type args struct {
		ids []string
	}
	tests := []struct {
		name string
		args args
		want map[string]bool
	}{
		{
			name: "empty slice",
			args: args{ids: []string{}},
			want: map[string]bool{},
		},
		{
			name: "single element",
			args: args{ids: []string{"a"}},
			want: map[string]bool{"a": true},
		},
		{
			name: "multiple unique elements",
			args: args{ids: []string{"a", "b", "c"}},
			want: map[string]bool{"a": true, "b": true, "c": true},
		},
		{
			name: "duplicate elements",
			args: args{ids: []string{"a", "a", "b"}},
			want: map[string]bool{"a": true, "b": true},
		},
		{
			name: "empty string element",
			args: args{ids: []string{"", "b"}},
			want: map[string]bool{"": true, "b": true},
		},
		{
			name: "nil slice",
			args: args{ids: nil},
			want: map[string]bool{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := toMap(tt.args.ids); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("toMap() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestContentRequestProcessor_getIncludeContentFilterMode(t *testing.T) {

	tests := []struct {
		name           string
		processorMode  string
		runtimeState   map[string]any
		expectedResult string
	}{
		{
			name:           "Default mode when no runtime config",
			processorMode:  IncludeContentFilterKeyPrefix,
			runtimeState:   nil,
			expectedResult: IncludeContentFilterKeyPrefix,
		},
		{
			name:          "Override to prefix mode",
			processorMode: IncludeContentFilterKeyAll,
			runtimeState: map[string]any{
				graph.CfgKeyIncludeFilterKeyMode: IncludeContentFilterKeyPrefix,
			},
			expectedResult: IncludeContentFilterKeyPrefix,
		},
		{
			name:          "Override to all mode",
			processorMode: IncludeContentFilterKeyPrefix,
			runtimeState: map[string]any{
				graph.CfgKeyIncludeFilterKeyMode: IncludeContentFilterKeyAll,
			},
			expectedResult: IncludeContentFilterKeyAll,
		},
		{
			name:          "Override to exact mode",
			processorMode: IncludeContentFilterKeyAll,
			runtimeState: map[string]any{
				graph.CfgKeyIncludeFilterKeyMode: IncludeContentFilterKeyExact,
			},
			expectedResult: IncludeContentFilterKeyExact,
		},
		{
			name:          "Empty runtime value",
			processorMode: IncludeContentFilterKeyPrefix,
			runtimeState: map[string]any{
				graph.CfgKeyIncludeFilterKeyMode: "",
			},
			expectedResult: IncludeContentFilterKeyPrefix,
		},
		{
			name:          "Invalid runtime value",
			processorMode: IncludeContentFilterKeyPrefix,
			runtimeState: map[string]any{
				graph.CfgKeyIncludeFilterKeyMode: "invalid_mode",
			},
			expectedResult: IncludeContentFilterKeyPrefix,
		},
		{
			name:          "Non-string runtime value",
			processorMode: IncludeContentFilterKeyPrefix,
			runtimeState: map[string]any{
				graph.CfgKeyIncludeFilterKeyMode: 123,
			},
			expectedResult: IncludeContentFilterKeyPrefix,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			processor := &ContentRequestProcessor{
				IncludeContentFilterMode: tt.processorMode,
			}

			inv := &agent.Invocation{
				RunOptions: agent.RunOptions{
					RuntimeState: tt.runtimeState,
				},
			}

			result := processor.getIncludeContentFilterMode(inv)

			assert.Equal(t, tt.expectedResult, result)
		})
	}
}

func TestContentRequestProcessor_needAppendHistoryMessage(t *testing.T) {
	type fields struct {
		AppendHistoryMessage bool
	}
	type args struct {
		inv *agent.Invocation
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   bool
	}{
		{
			name: "use processor config when runtime state not set",
			fields: fields{
				AppendHistoryMessage: true,
			},
			args: args{
				inv: &agent.Invocation{
					RunOptions: agent.RunOptions{
						RuntimeState: map[string]any{},
					},
				},
			},
			want: true,
		},
		{
			name: "use processor config when runtime state is nil",
			fields: fields{
				AppendHistoryMessage: false,
			},
			args: args{
				inv: &agent.Invocation{
					RunOptions: agent.RunOptions{
						RuntimeState: nil,
					},
				},
			},
			want: false,
		},
		{
			name: "use runtime state when set to true",
			fields: fields{
				AppendHistoryMessage: false,
			},
			args: args{
				inv: &agent.Invocation{
					RunOptions: agent.RunOptions{
						RuntimeState: map[string]any{
							graph.CfgKeyAppendHistoryMessage: true,
						},
					},
				},
			},
			want: true,
		},
		{
			name: "use runtime state when set to false",
			fields: fields{
				AppendHistoryMessage: true,
			},
			args: args{
				inv: &agent.Invocation{
					RunOptions: agent.RunOptions{
						RuntimeState: map[string]any{
							graph.CfgKeyAppendHistoryMessage: false,
						},
					},
				},
			},
			want: false,
		},
		{
			name: "use processor config when runtime state value is not bool",
			fields: fields{
				AppendHistoryMessage: true,
			},
			args: args{
				inv: &agent.Invocation{
					RunOptions: agent.RunOptions{
						RuntimeState: map[string]any{
							graph.CfgKeyAppendHistoryMessage: "not a bool",
						},
					},
				},
			},
			want: true,
		},
		{
			name: "use processor config when runtime state value is int",
			fields: fields{
				AppendHistoryMessage: false,
			},
			args: args{
				inv: &agent.Invocation{
					RunOptions: agent.RunOptions{
						RuntimeState: map[string]any{
							graph.CfgKeyAppendHistoryMessage: 1,
						},
					},
				},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		p := &ContentRequestProcessor{
			AppendHistoryMessage: tt.fields.AppendHistoryMessage,
		}
		if got := p.needAppendHistoryMessage(tt.args.inv); got != tt.want {
			t.Errorf("%q. needAppendHistoryMessage() = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestWithIncludeContentFilterMode(t *testing.T) {
	tests := []struct {
		name     string
		mode     string
		wantMode string
	}{
		{
			name:     "empty mode",
			mode:     "",
			wantMode: "",
		},
		{
			name:     "valid mode prefix",
			mode:     "prefix",
			wantMode: "prefix",
		},
		{
			name:     "valid mode all",
			mode:     "all",
			wantMode: "all",
		},
		{
			name:     "special characters mode",
			mode:     "!@#$%^&*()",
			wantMode: "!@#$%^&*()",
		},
		{
			name:     "long mode string",
			mode:     "very_long_mode_string_with_more_than_50_characters_1234567890",
			wantMode: "very_long_mode_string_with_more_than_50_characters_1234567890",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			processor := &ContentRequestProcessor{}

			option := WithIncludeContentFilterMode(tt.mode)

			option(processor)

			assert.Equal(t, tt.wantMode, processor.IncludeContentFilterMode,
				"IncludeContentFilterMode should be set correctly")
		})
	}
}

func TestWithIncludeContentFilterMode_Type(t *testing.T) {
	option := WithIncludeContentFilterMode("test")

	assert.True(t, reflect.TypeOf(option).Kind() == reflect.Func,
		"Return value should be a function")

	expectedType := reflect.TypeOf(func(*ContentRequestProcessor) {})
	actualType := reflect.TypeOf(option)
	assert.True(t, actualType.AssignableTo(expectedType),
		"Return function should match ContentOption type")
}

func TestNewContentRequestProcessor(t *testing.T) {

	defaultWant := &ContentRequestProcessor{
		IncludeContentFilterMode: "prefix",
		AddContextPrefix:         true,
		PreserveSameBranch:       true,
		AppendHistoryMessage:     true,
		AddSessionSummary:        false,
		MaxHistoryRuns:           0,
	}

	tests := []struct {
		name string
		args []ContentOption
		want *ContentRequestProcessor
	}{

		{
			name: "no options",
			args: nil,
			want: defaultWant,
		},

		{
			name: "single option - AddContextPrefix false",
			args: []ContentOption{WithAddContextPrefix(false)},
			want: func() *ContentRequestProcessor {
				w := *defaultWant
				w.AddContextPrefix = false
				return &w
			}(),
		},

		{
			name: "multiple options",
			args: []ContentOption{
				WithAddContextPrefix(false),
				WithPreserveSameBranch(false),
				WithAppendHistoryMessage(false),
				WithIncludeContentFilterMode("all"),
			},
			want: &ContentRequestProcessor{
				IncludeContentFilterMode: "all",
				AddContextPrefix:         false,
				PreserveSameBranch:       false,
				AppendHistoryMessage:     false,
				AddSessionSummary:        false,
				MaxHistoryRuns:           0,
			},
		},

		{
			name: "option override",
			args: []ContentOption{
				WithAddContextPrefix(true),
				WithAddContextPrefix(false),
				WithPreserveSameBranch(true),
				WithPreserveSameBranch(false),
			},
			want: func() *ContentRequestProcessor {
				w := *defaultWant
				w.AddContextPrefix = false
				w.PreserveSameBranch = false
				return &w
			}(),
		},

		{
			name: "empty options slice",
			args: []ContentOption{},
			want: defaultWant,
		},

		{
			name: "unhandled fields remain default",
			args: []ContentOption{
				WithIncludeContentFilterMode("exact"),
			},
			want: func() *ContentRequestProcessor {
				w := *defaultWant
				w.IncludeContentFilterMode = "exact"
				return &w
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewContentRequestProcessor(tt.args...)

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NewContentRequestProcessor() = %v, want %v", got, tt.want)
			}

			assert.Equal(t, tt.want.IncludeContentFilterMode, got.IncludeContentFilterMode, "IncludeContentFilterMode mismatch")
			assert.Equal(t, tt.want.AddContextPrefix, got.AddContextPrefix, "AddContextPrefix mismatch")
			assert.Equal(t, tt.want.PreserveSameBranch, got.PreserveSameBranch, "PreserveSameBranch mismatch")
			assert.Equal(t, tt.want.AppendHistoryMessage, got.AppendHistoryMessage, "AppendHistoryMessage mismatch")
			assert.Equal(t, tt.want.AddSessionSummary, got.AddSessionSummary, "AddSessionSummary mismatch")
			assert.Equal(t, tt.want.MaxHistoryRuns, got.MaxHistoryRuns, "MaxHistoryRuns mismatch")
		})
	}
}

func TestContentRequestProcessor_mergeFunctionResponseEvents(t *testing.T) {
	type fields struct {
		IncludeContentFilterMode string
		AddContextPrefix         bool
		AddSessionSummary        bool
		MaxHistoryRuns           int
		PreserveSameBranch       bool
		AppendHistoryMessage     bool
	}
	type args struct {
		functionResponseEvents []event.Event
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   event.Event
	}{
		{
			name: "empty input",
			args: args{
				functionResponseEvents: []event.Event{},
			},
			want: event.Event{},
		},
		{
			name: "single event with valid choices",
			args: args{
				functionResponseEvents: []event.Event{
					{
						Author: "agent1",
						Response: &model.Response{
							Choices: []model.Choice{
								{
									Message: model.Message{
										ToolID:  "tool1",
										Content: "result1",
									},
								},
								{
									Message: model.Message{
										ToolID:  "tool2",
										Content: "result2",
									},
								},
							},
						},
					},
				},
			},
			want: event.Event{
				Author: "agent1",
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.Message{
								ToolID:  "tool1",
								Content: "result1",
							},
						},
						{
							Message: model.Message{
								ToolID:  "tool2",
								Content: "result2",
							},
						},
					},
				},
			},
		},
		{
			name: "multiple events with valid choices",
			args: args{
				functionResponseEvents: []event.Event{
					{
						Author: "agent1",
						Response: &model.Response{
							Choices: []model.Choice{
								{
									Message: model.Message{
										ToolID:  "tool1",
										Content: "result1",
									},
								},
							},
						},
					},
					{
						Author: "agent2",
						Response: &model.Response{
							Choices: []model.Choice{
								{
									Message: model.Message{
										ToolID:  "tool2",
										Content: "result2",
									},
								},
							},
						},
					},
				},
			},
			want: event.Event{
				Author: "agent1",
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.Message{
								ToolID:  "tool1",
								Content: "result1",
							},
						},
						{
							Message: model.Message{
								ToolID:  "tool2",
								Content: "result2",
							},
						},
					},
				},
			},
		},
		{
			name: "event with invalid choices",
			args: args{
				functionResponseEvents: []event.Event{
					{
						Author: "agent1",
						Response: &model.Response{
							Choices: []model.Choice{
								{
									Message: model.Message{
										ToolID: "tool1",
									},
								},
								{
									Message: model.Message{
										Content: "result2",
									},
								},
								{
									Message: model.Message{
										ToolID:  "tool3",
										Content: "result3",
									},
								},
							},
						},
					},
				},
			},
			want: event.Event{
				Author: "agent1",
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.Message{
								ToolID:  "tool3",
								Content: "result3",
							},
						},
					},
				},
			},
		},
		{
			name: "multiple events with mixed choices",
			args: args{
				functionResponseEvents: []event.Event{
					{
						Author: "agent1",
						Response: &model.Response{
							Choices: []model.Choice{
								{
									Message: model.Message{
										ToolID: "tool1",
									},
								},
								{
									Message: model.Message{
										ToolID:  "tool2",
										Content: "result2",
									},
								},
							},
						},
					},
					{
						Author: "agent2",
						Response: &model.Response{
							Choices: []model.Choice{
								{
									Message: model.Message{
										ToolID:  "tool3",
										Content: "result3",
									},
								},
								{
									Message: model.Message{
										Content: "result4",
									},
								},
							},
						},
					},
				},
			},
			want: event.Event{
				Author: "agent1",
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.Message{
								ToolID:  "tool2",
								Content: "result2",
							},
						},
						{
							Message: model.Message{
								ToolID:  "tool3",
								Content: "result3",
							},
						},
					},
				},
			},
		},
		{
			name: "first event has no choices",
			args: args{
				functionResponseEvents: []event.Event{
					{
						Author:   "agent1",
						Response: &model.Response{},
					},
					{
						Author: "agent2",
						Response: &model.Response{
							Choices: []model.Choice{
								{
									Message: model.Message{
										ToolID:  "tool1",
										Content: "result1",
									},
								},
							},
						},
					},
				},
			},
			want: event.Event{
				Author: "agent1",
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.Message{
								ToolID:  "tool1",
								Content: "result1",
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &ContentRequestProcessor{
				IncludeContentFilterMode: tt.fields.IncludeContentFilterMode,
				AddContextPrefix:         tt.fields.AddContextPrefix,
				AddSessionSummary:        tt.fields.AddSessionSummary,
				MaxHistoryRuns:           tt.fields.MaxHistoryRuns,
				PreserveSameBranch:       tt.fields.PreserveSameBranch,
				AppendHistoryMessage:     tt.fields.AppendHistoryMessage,
			}
			if got := p.mergeFunctionResponseEvents(tt.args.functionResponseEvents); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("%q. mergeFunctionResponseEvents() = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestContentRequestProcessor_isOtherAgentReply(t *testing.T) {
	type fields struct {
		PreserveSameBranch bool
	}
	type args struct {
		currentAgentName string
		currentBranch    string
		evt              *event.Event
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   bool
	}{

		{
			name: "nil event",
			fields: fields{
				PreserveSameBranch: true,
			},
			args: args{
				currentAgentName: "agent1",
				currentBranch:    "branch1",
				evt:              nil,
			},
			want: false,
		},

		{
			name: "empty agent name",
			fields: fields{
				PreserveSameBranch: true,
			},
			args: args{
				currentAgentName: "",
				currentBranch:    "branch1",
				evt: &event.Event{
					Author: "agent2",
					Branch: "branch2",
				},
			},
			want: false,
		},

		{
			name: "user event",
			fields: fields{
				PreserveSameBranch: true,
			},
			args: args{
				currentAgentName: "agent1",
				currentBranch:    "branch1",
				evt: &event.Event{
					Author: "user",
				},
			},
			want: false,
		},

		{
			name: "self agent event",
			fields: fields{
				PreserveSameBranch: true,
			},
			args: args{
				currentAgentName: "agent1",
				currentBranch:    "branch1",
				evt: &event.Event{
					Author: "agent1",
					Branch: "branch1",
				},
			},
			want: false,
		},

		{
			name: "same branch with preserve",
			fields: fields{
				PreserveSameBranch: true,
			},
			args: args{
				currentAgentName: "agent1",
				currentBranch:    "branch1",
				evt: &event.Event{
					Author: "agent2",
					Branch: "branch1",
				},
			},
			want: false,
		},

		{
			name: "child branch with preserve",
			fields: fields{
				PreserveSameBranch: true,
			},
			args: args{
				currentAgentName: "agent1",
				currentBranch:    "branch1",
				evt: &event.Event{
					Author: "agent2",
					Branch: "branch1/child",
				},
			},
			want: false,
		},

		{
			name: "parent branch with preserve",
			fields: fields{
				PreserveSameBranch: true,
			},
			args: args{
				currentAgentName: "agent1",
				currentBranch:    "branch1/child",
				evt: &event.Event{
					Author: "agent2",
					Branch: "branch1",
				},
			},
			want: false,
		},

		{
			name: "unrelated branch with preserve",
			fields: fields{
				PreserveSameBranch: true,
			},
			args: args{
				currentAgentName: "agent1",
				currentBranch:    "branch1",
				evt: &event.Event{
					Author: "agent2",
					Branch: "branch2",
				},
			},
			want: true,
		},

		{
			name: "same branch without preserve",
			fields: fields{
				PreserveSameBranch: false,
			},
			args: args{
				currentAgentName: "agent1",
				currentBranch:    "branch1",
				evt: &event.Event{
					Author: "agent2",
					Branch: "branch1",
				},
			},
			want: true,
		},

		{
			name: "empty branch",
			fields: fields{
				PreserveSameBranch: true,
			},
			args: args{
				currentAgentName: "agent1",
				currentBranch:    "",
				evt: &event.Event{
					Author: "agent2",
					Branch: "",
				},
			},
			want: true,
		},

		{
			name: "exact branch match",
			fields: fields{
				PreserveSameBranch: true,
			},
			args: args{
				currentAgentName: "agent1",
				currentBranch:    "branch1",
				evt: &event.Event{
					Author: "agent2",
					Branch: "branch1",
				},
			},
			want: false,
		},

		{
			name: "branch prefix match",
			fields: fields{
				PreserveSameBranch: true,
			},
			args: args{
				currentAgentName: "agent1",
				currentBranch:    "branch1",
				evt: &event.Event{
					Author: "agent2",
					Branch: "branch1/child",
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &ContentRequestProcessor{
				PreserveSameBranch: tt.fields.PreserveSameBranch,
			}
			if got := p.isOtherAgentReply(
				tt.args.currentAgentName,
				tt.args.currentBranch,
				tt.args.evt,
			); got != tt.want {
				t.Errorf("isOtherAgentReply() = %v, want %v", got, tt.want)
			}
		})
	}
}

func getSession() *session.Session {
	baseTime := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
	return &session.Session{
		Events: []event.Event{
			{
				Author:    "user",
				RequestID: "test-request-id-1",
				FilterKey: "test-filter",
				Timestamp: baseTime.Add(-1 * time.Hour),
				Version:   event.CurrentVersion,
			},
			{
				Author:    "user",
				RequestID: "test-request-id-1",
				FilterKey: "test-filter",
				Timestamp: baseTime.Add(-1 * time.Hour),
				Version:   event.CurrentVersion,
				Response: &model.Response{
					IsPartial: true,
					Choices: []model.Choice{
						{
							Delta: model.Message{
								Role:    model.RoleUser,
								Content: "test-request-id-1/test-filter/part message",
							},
						},
					},
				},
			},
			{
				Author:    "user",
				RequestID: "test-request-id",
				FilterKey: "test-filter",
				Timestamp: baseTime.Add(-1 * time.Hour),
				Version:   event.CurrentVersion,
				Response: &model.Response{
					IsPartial: true,
					Choices: []model.Choice{
						{
							Delta: model.Message{
								Role:    model.RoleUser,
								Content: "test-request-id/test-filter/part message",
							},
						},
					},
				},
			},
			{
				Author:    "user",
				RequestID: "test-request-id-1",
				FilterKey: "test-filter/a",
				Timestamp: baseTime.Add(-1 * time.Hour),
				Version:   event.CurrentVersion,
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.Message{
								Role:    model.RoleUser,
								Content: "test-request-id-1/test-filter/a/old message",
							},
						},
					},
				},
			},
			{
				Author:    "user",
				RequestID: "test-request-id",
				FilterKey: "test-filter/a",
				Timestamp: baseTime.Add(-1 * time.Hour),
				Version:   event.CurrentVersion,
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.Message{
								Role:    model.RoleUser,
								Content: "test-request-id/test-filter/a/old message",
							},
						},
					},
				},
			},
			{
				Author:    "assistant",
				RequestID: "test-request-id",
				FilterKey: "test-filter-a",
				Timestamp: baseTime.Add(-1 * time.Hour),
				Version:   event.CurrentVersion,
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.Message{
								Role:    model.RoleUser,
								Content: "test-request-id/test-filter-a",
							},
						},
					},
				},
			},
			{
				Author:    "assistant",
				RequestID: "test-request-id-1",
				FilterKey: "test-filter-a",
				Timestamp: baseTime.Add(-1 * time.Hour),
				Version:   event.CurrentVersion,
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.Message{
								Role:    model.RoleUser,
								Content: "test-request-id-1/test-filter-a",
							},
						},
					},
				},
			},
			{
				Author:    "assistant",
				RequestID: "test-request-id",
				FilterKey: "test-filter",
				Timestamp: baseTime.Add(-1 * time.Hour),
				Version:   event.CurrentVersion,
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.Message{
								Role:    model.RoleUser,
								Content: "test-request-id/test-filter",
							},
						},
					},
				},
			},
			{
				Author:    "assistant",
				RequestID: "test-request-id-1",
				FilterKey: "test-filter",
				Timestamp: baseTime.Add(-1 * time.Hour),
				Version:   event.CurrentVersion,
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.Message{
								Role:    model.RoleUser,
								Content: "test-request-id-1/test-filter",
							},
						},
					},
				},
			},
		},
	}
}

func TestContentRequestProcessor_getFilterIncrementMessages(t *testing.T) {
	baseTime := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)

	inv := agent.NewInvocation(
		agent.WithInvocationEventFilterKey("test-filter"),
		agent.WithInvocationSession(getSession()),
		agent.WithInvocationRunOptions(agent.RunOptions{
			RequestID: "test-request-id",
		}),
	)

	tests := []struct {
		name                     string
		summaryUpdatedAt         time.Time
		expectedCount            int
		expectedContent          []string
		appendHistoryMessage     bool
		includeContentFilterMode string
		invocation               *agent.Invocation
	}{
		{
			name:             "nil session",
			summaryUpdatedAt: baseTime,
			expectedCount:    0,
			expectedContent:  []string{},
			invocation: &agent.Invocation{
				RunOptions: agent.RunOptions{RequestID: "test-request-id"},
			},
			appendHistoryMessage:     true,
			includeContentFilterMode: IncludeContentFilterKeyPrefix,
		},
		{
			name:             "append history and all mode",
			summaryUpdatedAt: time.Time{},
			expectedCount:    6,
			expectedContent: []string{
				"test-request-id-1/test-filter/a/old message",
				"test-request-id/test-filter/a/old message",
				"test-request-id/test-filter-a",
				"test-request-id-1/test-filter-a",
				"test-request-id/test-filter",
				"test-request-id-1/test-filter",
			},
			invocation: &agent.Invocation{
				RunOptions: agent.RunOptions{RequestID: "test-request-id"},
			},
			appendHistoryMessage:     true,
			includeContentFilterMode: IncludeContentFilterKeyAll,
		},
		{
			name:             "append history and prefix mode",
			summaryUpdatedAt: time.Time{},
			expectedCount:    4,
			expectedContent: []string{
				"test-request-id-1/test-filter/a/old message",
				"test-request-id/test-filter/a/old message",
				"test-request-id/test-filter",
				"test-request-id-1/test-filter",
			},
			invocation: &agent.Invocation{
				RunOptions: agent.RunOptions{RequestID: "test-request-id"},
			},
			appendHistoryMessage:     true,
			includeContentFilterMode: IncludeContentFilterKeyPrefix,
		},
		{
			name:             "append history and exact mode",
			summaryUpdatedAt: time.Time{},
			expectedCount:    2,
			expectedContent: []string{
				"test-request-id/test-filter",
				"test-request-id-1/test-filter",
			},
			invocation: &agent.Invocation{
				RunOptions: agent.RunOptions{RequestID: "test-request-id"},
			},
			appendHistoryMessage:     true,
			includeContentFilterMode: IncludeContentFilterKeyExact,
		},
		{
			name:             "not append history and all mode",
			summaryUpdatedAt: time.Time{},
			expectedCount:    3,
			expectedContent: []string{
				"test-request-id/test-filter/a/old message",
				"test-request-id/test-filter-a",
				"test-request-id/test-filter",
			},
			invocation: &agent.Invocation{
				RunOptions: agent.RunOptions{RequestID: "test-request-id"},
			},
			appendHistoryMessage:     false,
			includeContentFilterMode: IncludeContentFilterKeyAll,
		},
		{
			name:             "not append history and prefix mode",
			summaryUpdatedAt: time.Time{},
			expectedCount:    2,
			expectedContent: []string{
				"test-request-id/test-filter/a/old message",
				"test-request-id/test-filter",
			},
			invocation: &agent.Invocation{
				RunOptions: agent.RunOptions{RequestID: "test-request-id"},
			},
			appendHistoryMessage:     false,
			includeContentFilterMode: IncludeContentFilterKeyPrefix,
		},
		{
			name:             "not append history and exact mode",
			summaryUpdatedAt: time.Time{},
			expectedCount:    1,
			expectedContent: []string{
				"test-request-id/test-filter",
			},
			invocation: &agent.Invocation{
				RunOptions: agent.RunOptions{RequestID: "test-request-id"},
			},
			appendHistoryMessage:     false,
			includeContentFilterMode: IncludeContentFilterKeyExact,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewContentRequestProcessor(
				WithIncludeContentFilterMode(tt.includeContentFilterMode),
				WithAppendHistoryMessage(tt.appendHistoryMessage),
			)

			messages := p.getIncrementMessages(inv, tt.summaryUpdatedAt)

			assert.Len(t, messages, tt.expectedCount)

			for i, expectedContent := range tt.expectedContent {
				assert.Equal(t, expectedContent, messages[i].Content)
			}
		})
	}
}
