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
			includeContents: BranchFilterModePrefix,

			expectedMsg:  nil,
			expectedTime: time.Time{},
		},
		{
			name:            "nil summaries",
			session:         &session.Session{},
			includeContents: BranchFilterModePrefix,
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
			includeContents: BranchFilterModePrefix,
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
			includeContents: BranchFilterModePrefix,
			expectedMsg: &model.Message{
				Role:    model.RoleSystem,
				Content: formatSummaryContent("Test summary content"),
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
			includeContents: BranchFilterModeAll,
			expectedMsg: &model.Message{
				Role:    model.RoleSystem,
				Content: formatSummaryContent("Full session summary"),
			},
			expectedTime: time.Date(2023, 1, 1, 13, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewContentRequestProcessor(WithBranchFilterMode(tt.includeContents))

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
	assert.Equal(t, formatSummaryContent("Initial summary"), msg.Content)
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
	assert.Equal(t, formatSummaryContent("Updated summary"), msg.Content)
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

func TestNewContentRequestProcessor(t *testing.T) {

	defaultWant := &ContentRequestProcessor{
		BranchFilterMode:   "prefix",
		AddContextPrefix:   true,
		PreserveSameBranch: false,
		TimelineFilterMode: "all",
		AddSessionSummary:  false,
		MaxHistoryRuns:     0,
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
				WithTimelineFilterMode(TimelineFilterCurrentRequest),
				WithBranchFilterMode("all"),
			},
			want: &ContentRequestProcessor{
				BranchFilterMode:   "all",
				AddContextPrefix:   false,
				PreserveSameBranch: false,
				TimelineFilterMode: TimelineFilterCurrentRequest,
				AddSessionSummary:  false,
				MaxHistoryRuns:     0,
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
				WithBranchFilterMode("exact"),
			},
			want: func() *ContentRequestProcessor {
				w := *defaultWant
				w.BranchFilterMode = "exact"
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

			assert.Equal(t, tt.want.BranchFilterMode, got.BranchFilterMode, "IncludeContentFilterMode mismatch")
			assert.Equal(t, tt.want.AddContextPrefix, got.AddContextPrefix,
				"AddContextPrefix mismatch")
			assert.Equal(t, tt.want.PreserveSameBranch,
				got.PreserveSameBranch,
				"PreserveSameBranch mismatch")
			assert.Equal(t, tt.want.TimelineFilterMode,
				got.TimelineFilterMode, "TimelineFilterMode mismatch")
			assert.Equal(t, tt.want.AddSessionSummary,
				got.AddSessionSummary, "AddSessionSummary mismatch")
			assert.Equal(t, tt.want.MaxHistoryRuns, got.MaxHistoryRuns,
				"MaxHistoryRuns mismatch")
		})
	}
}

func TestContentRequestProcessor_mergeUserMessages(t *testing.T) {
	tests := []struct {
		name     string
		messages []model.Message
		want     []model.Message
		desc     string
	}{
		{
			name:     "empty slice",
			messages: nil,
			want:     nil,
			desc:     "nil input should return nil",
		},
		{
			name: "single user message",
			messages: []model.Message{
				model.NewUserMessage("hello"),
			},
			want: []model.Message{
				model.NewUserMessage("hello"),
			},
			desc: "single message should be unchanged",
		},
		{
			name: "mixed roles unchanged",
			messages: []model.Message{
				model.NewUserMessage("hello"),
				model.NewAssistantMessage("hi"),
				model.NewUserMessage("there"),
			},
			want: []model.Message{
				model.NewUserMessage("hello"),
				model.NewAssistantMessage("hi"),
				model.NewUserMessage("there"),
			},
			desc: "non-context user and assistant messages stay as-is",
		},
		{
			name: "merge consecutive context users",
			messages: []model.Message{
				model.NewUserMessage(contextPrefix + " A"),
				model.NewUserMessage(contextPrefix + " B"),
				model.NewAssistantMessage("keep"),
			},
			want: []model.Message{
				model.NewUserMessage(
					contextPrefix + " A" + mergedUserSeparator +
						contextPrefix + " B",
				),
				model.NewAssistantMessage("keep"),
			},
			desc: "context user messages are merged into one",
		},
		{
			name: "merge context users with content parts",
			messages: []model.Message{
				{
					Role:    model.RoleUser,
					Content: contextPrefix + " A",
					ContentParts: []model.ContentPart{
						{
							Type: model.ContentTypeText,
							Text: func() *string {
								text := "part A"
								return &text
							}(),
						},
					},
				},
				{
					Role:    model.RoleUser,
					Content: contextPrefix + " B",
					ContentParts: []model.ContentPart{
						{
							Type: model.ContentTypeText,
							Text: func() *string {
								text := "part B"
								return &text
							}(),
						},
					},
				},
			},
			want: []model.Message{
				{
					Role: model.RoleUser,
					Content: contextPrefix + " A" +
						mergedUserSeparator +
						contextPrefix + " B",
					ContentParts: []model.ContentPart{
						{
							Type: model.ContentTypeText,
							Text: func() *string {
								text := "part A"
								return &text
							}(),
						},
						{
							Type: model.ContentTypeText,
							Text: func() *string {
								text := "part B"
								return &text
							}(),
						},
					},
				},
			},
			desc: "content parts from context users are merged",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &ContentRequestProcessor{
				AddContextPrefix: true,
			}
			got := p.mergeUserMessages(tt.messages)
			assert.Equal(t, tt.want, got, tt.desc)
		})
	}
}

func TestContentRequestProcessor_mergeUserMessages_NoPrefix(t *testing.T) {
	p := &ContentRequestProcessor{
		AddContextPrefix: false,
	}
	messages := []model.Message{
		model.NewUserMessage(contextPrefix + " one"),
		model.NewUserMessage(contextPrefix + " two"),
	}

	got := p.mergeUserMessages(messages)

	assert.Equal(t, messages, got,
		"when AddContextPrefix is false messages should be unchanged")
}

func TestContentRequestProcessor_mergeFunctionResponseEvents(t *testing.T) {
	type fields struct {
		IncludeContentFilterMode string
		AddContextPrefix         bool
		AddSessionSummary        bool
		MaxHistoryRuns           int
		PreserveSameBranch       bool
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
				BranchFilterMode:   tt.fields.IncludeContentFilterMode,
				AddContextPrefix:   tt.fields.AddContextPrefix,
				AddSessionSummary:  tt.fields.AddSessionSummary,
				MaxHistoryRuns:     tt.fields.MaxHistoryRuns,
				PreserveSameBranch: tt.fields.PreserveSameBranch,
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

func getSession(evts ...event.Event) *session.Session {
	return &session.Session{
		Events: evts,
	}
}

func createEvent(requestID, invocationID, filterKey, content string, timestamp time.Time, isPartial bool) event.Event {
	evt := event.Event{
		Author:       "assistant",
		RequestID:    requestID,
		InvocationID: invocationID,
		FilterKey:    filterKey,
		Timestamp:    timestamp,
		Version:      event.CurrentVersion,
		Response: &model.Response{
			IsPartial: false,
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: content,
					},
				},
			},
		},
	}
	if isPartial {
		evt.Response = &model.Response{
			IsPartial: true,
			Choices: []model.Choice{
				{
					Delta: model.Message{
						Role:    model.RoleAssistant,
						Content: content,
					},
				},
			},
		}
	}
	return evt
}

func TestContentRequestProcessor_IncludeContentsNoneSkipsHistory(t *testing.T) {
	p := NewContentRequestProcessor()

	sess := &session.Session{
		Events: []event.Event{
			{
				Author: "user",
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
		},
	}

	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
		agent.WithInvocationMessage(model.NewUserMessage("current")),
	)
	inv.RunOptions = agent.RunOptions{
		RuntimeState: map[string]any{
			"include_contents": "none",
		},
	}

	req := &model.Request{}
	p.ProcessRequest(context.Background(), inv, req, nil)

	if len(req.Messages) != 1 {
		t.Fatalf("expected only invocation message, got %d messages", len(req.Messages))
	}
	msg := req.Messages[0]
	if msg.Role != model.RoleUser || msg.Content != "current" {
		t.Fatalf("unexpected message: %+v", msg)
	}
}

func TestContentRequestProcessor_getFilterIncrementMessages(t *testing.T) {
	baseTime := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	sess := getSession(
		// invalid content
		createEvent("test-request-id", "test-invocation-id", "test-filter", "message1", baseTime.Add(time.Second), true),
		// different requestID and invocationID
		createEvent("test-request-id-2", "test-invocation-id-4", "test-filter", "message2", baseTime.Add(time.Second), false),
		createEvent("test-request-id-3", "test-invocation-id-5", "test-filter/a", "message3", baseTime.Add(time.Second), false),
		createEvent("test-request-id-4", "test-invocation-id-6", "test-filter-a", "message4", baseTime.Add(time.Second), false),
		createEvent("test-request-id-2", "test-invocation-id-4", "test-filter", "message5", baseTime.Add(-10*time.Second), false),
		createEvent("test-request-id-3", "test-invocation-id-5", "test-filter/a", "message6", baseTime.Add(-10*time.Second), false),
		createEvent("test-request-id-4", "test-invocation-id-6", "test-filter-a", "message7", baseTime.Add(-10*time.Second), false),

		// same requestID and different invocationID
		createEvent("test-request-id", "test-invocation-id-1", "test-filter", "message8", baseTime.Add(time.Second), false),
		createEvent("test-request-id", "test-invocation-id-2", "test-filter/a", "message9", baseTime.Add(time.Second), false),
		createEvent("test-request-id", "test-invocation-id-3", "test-filter-a", "message10", baseTime.Add(time.Second), false),
		createEvent("test-request-id", "test-invocation-id-1", "test-filter", "message11", baseTime.Add(-10*time.Second), false),
		createEvent("test-request-id", "test-invocation-id-2", "test-filter/a", "message12", baseTime.Add(-10*time.Second), false),
		createEvent("test-request-id", "test-invocation-id-3", "test-filter-a", "message13", baseTime.Add(-10*time.Second), false),

		// same requestID and invocationID
		createEvent("test-request-id", "test-invocation-id", "test-filter", "message14", baseTime.Add(time.Second), false),
		createEvent("test-request-id", "test-invocation-id", "test-filter/a", "message15", baseTime.Add(time.Second), false),
		createEvent("test-request-id", "test-invocation-id", "test-filter-a", "message16", baseTime.Add(time.Second), false),
		createEvent("test-request-id", "test-invocation-id", "test-filter", "message17", baseTime.Add(-10*time.Second), false),
		createEvent("test-request-id", "test-invocation-id", "test-filter/a", "message18", baseTime.Add(-10*time.Second), false),
		createEvent("test-request-id", "test-invocation-id", "test-filter-a", "message19", baseTime.Add(-10*time.Second), false),
	)
	inv := agent.NewInvocation(
		agent.WithInvocationEventFilterKey("test-filter"),
		agent.WithInvocationSession(sess),
		agent.WithInvocationRunOptions(agent.RunOptions{
			RequestID: "test-request-id",
		}),
	)
	inv.InvocationID = "test-invocation-id"

	tests := []struct {
		name               string
		summaryUpdatedAt   time.Time
		expectedCount      int
		expectedContent    []string
		timelineFilterMode string
		branchFilterMode   string
	}{
		{
			name:             "BranchFilterModeAll and TimelineFilterAll and zero time",
			expectedCount:    18,
			summaryUpdatedAt: time.Time{},
			expectedContent: []string{
				"message2", "message3", "message4", "message5", "message6", "message7",
				"message8", "message9", "message10", "message11", "message12", "message13",
				"message14", "message15", "message16", "message17", "message18", "message19",
			},
			branchFilterMode:   BranchFilterModeAll,
			timelineFilterMode: TimelineFilterAll,
		},
		{
			name:             "BranchFilterPrefix and TimelineFilterAll and zero time",
			expectedCount:    12,
			summaryUpdatedAt: time.Time{},
			expectedContent: []string{
				"message2", "message3", "message5", "message6",
				"message8", "message9", "message11", "message12",
				"message14", "message15", "message17", "message18",
			},
			branchFilterMode:   BranchFilterModePrefix,
			timelineFilterMode: TimelineFilterAll,
		},
		{
			name:             "BranchFilterModeExact and TimelineFilterAll and zero time",
			expectedCount:    6,
			summaryUpdatedAt: time.Time{},
			expectedContent: []string{
				"message2", "message5",
				"message8", "message11",
				"message14", "message17",
			},
			branchFilterMode:   BranchFilterModeExact,
			timelineFilterMode: TimelineFilterAll,
		},
		{
			name:             "BranchFilterModeAll and TimelineFilterCurrentRequest and zero time",
			expectedCount:    12,
			summaryUpdatedAt: time.Time{},
			expectedContent: []string{
				"message8", "message9", "message10", "message11", "message12", "message13",
				"message14", "message15", "message16", "message17", "message18", "message19",
			},
			branchFilterMode:   BranchFilterModeAll,
			timelineFilterMode: TimelineFilterCurrentRequest,
		},
		{
			name:             "BranchFilterModeAll and TimelineFilterCurrentInvocation and zero time",
			expectedCount:    6,
			summaryUpdatedAt: time.Time{},
			expectedContent: []string{
				"message14", "message15", "message16", "message17", "message18", "message19",
			},
			branchFilterMode:   BranchFilterModeAll,
			timelineFilterMode: TimelineFilterCurrentInvocation,
		},
		{
			name:             "BranchFilterModeAll and TimelineFilterAll and has time",
			summaryUpdatedAt: baseTime,
			expectedCount:    9,
			expectedContent: []string{
				"message2", "message3", "message4",
				"message8", "message9", "message10",
				"message14", "message15", "message16",
			},
			branchFilterMode:   BranchFilterModeAll,
			timelineFilterMode: TimelineFilterAll,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewContentRequestProcessor(
				WithBranchFilterMode(tt.branchFilterMode),
				WithTimelineFilterMode(tt.timelineFilterMode),
			)

			messages := p.getIncrementMessages(inv, tt.summaryUpdatedAt)

			assert.Len(t, messages, tt.expectedCount)

			for i, expectedContent := range tt.expectedContent {
				assert.Equal(t, expectedContent, messages[i].Content)
			}
		})
	}
}

func TestContentRequestProcessor_shouldIncludeEvent(t *testing.T) {
	baseTime := time.Now()
	sinceTime := baseTime.Add(-time.Hour)

	tests := []struct {
		name                string
		setup               func() (*ContentRequestProcessor, event.Event, *agent.Invocation, string, bool, time.Time)
		expected            bool
		isInvocationMessage bool
	}{
		{
			name: "nil response",
			setup: func() (*ContentRequestProcessor, event.Event, *agent.Invocation, string, bool, time.Time) {
				p := &ContentRequestProcessor{}
				evt := event.Event{}
				inv := &agent.Invocation{}
				return p, evt, inv, "", true, baseTime
			},
			expected: false,
		},
		{
			name: "is partial event",
			setup: func() (*ContentRequestProcessor, event.Event, *agent.Invocation, string, bool, time.Time) {
				p := &ContentRequestProcessor{}
				evt := event.Event{
					RequestID:    "123",
					InvocationID: "123",
					Version:      event.CurrentVersion,
					Response: &model.Response{
						IsPartial: true,
						Choices: []model.Choice{
							{
								Delta: model.Message{
									Content: "test-content",
								},
							},
						},
					},
				}
				inv := &agent.Invocation{InvocationID: "123", RunOptions: agent.RunOptions{RequestID: "123"}}
				return p, evt, inv, "", true, baseTime
			},
			expected: false,
		},
		{
			name: "invalid content",
			setup: func() (*ContentRequestProcessor, event.Event, *agent.Invocation, string, bool, time.Time) {
				p := &ContentRequestProcessor{}
				evt := event.Event{
					RequestID:    "123",
					InvocationID: "123",
					Version:      event.CurrentVersion,
					Response: &model.Response{
						Choices: []model.Choice{
							{
								Message: model.Message{
									Content: "",
								},
							},
						},
					},
				}
				inv := &agent.Invocation{InvocationID: "123", RunOptions: agent.RunOptions{RequestID: "123"}}
				return p, evt, inv, "", true, baseTime
			},
			expected: false,
		},
		{
			name: "timestamp before since when not zero time",
			setup: func() (*ContentRequestProcessor, event.Event, *agent.Invocation, string, bool, time.Time) {
				p := &ContentRequestProcessor{}
				evt := event.Event{
					RequestID:    "123",
					InvocationID: "123",
					Version:      event.CurrentVersion,
					Response: &model.Response{
						Choices: []model.Choice{
							{
								Message: model.Message{
									Content: "content",
								},
							},
						},
					},
					Timestamp: sinceTime.Add(-time.Hour),
				}
				inv := &agent.Invocation{InvocationID: "123", RunOptions: agent.RunOptions{RequestID: "123"}}
				return p, evt, inv, "", false, sinceTime
			},
			expected: false,
		},
		{
			name: "timestamp equal since when not zero time",
			setup: func() (*ContentRequestProcessor, event.Event, *agent.Invocation, string, bool, time.Time) {
				p := &ContentRequestProcessor{}
				evt := event.Event{
					RequestID:    "123",
					InvocationID: "123",
					Version:      event.CurrentVersion,
					Response: &model.Response{
						Choices: []model.Choice{
							{
								Message: model.Message{
									Content: "content",
								},
							},
						},
					},
					Timestamp: sinceTime,
				}
				inv := &agent.Invocation{InvocationID: "123", RunOptions: agent.RunOptions{RequestID: "123"}}
				return p, evt, inv, "", false, sinceTime
			},
			expected: false,
		},
		{
			name: "TimelineFilterCurrentRequest with different request ID",
			setup: func() (*ContentRequestProcessor, event.Event, *agent.Invocation, string, bool, time.Time) {
				p := &ContentRequestProcessor{
					TimelineFilterMode: TimelineFilterCurrentRequest,
				}
				evt := event.Event{
					RequestID: "req1",
					Version:   event.CurrentVersion,
					Response: &model.Response{
						Choices: []model.Choice{
							{
								Message: model.Message{
									Content: "content",
								},
							},
						},
					},
					Timestamp: baseTime,
				}
				inv := &agent.Invocation{
					RunOptions: agent.RunOptions{RequestID: "req2"},
				}
				return p, evt, inv, "", true, baseTime
			},
			expected: false,
		},
		{
			name: "TimelineFilterCurrentRequest with same request ID",
			setup: func() (*ContentRequestProcessor, event.Event, *agent.Invocation, string, bool, time.Time) {
				p := &ContentRequestProcessor{
					TimelineFilterMode: TimelineFilterCurrentRequest,
				}
				evt := event.Event{
					Version: event.CurrentVersion,
					Response: &model.Response{
						Choices: []model.Choice{
							{
								Message: model.Message{
									Content: "content",
								},
							},
						},
					},
					Timestamp: baseTime,
					RequestID: "req1",
				}
				inv := &agent.Invocation{
					RunOptions: agent.RunOptions{RequestID: "req1"},
				}
				return p, evt, inv, "", true, baseTime
			},
			expected: true,
		},
		{
			name: "TimelineFilterCurrentRequest with same request ID",
			setup: func() (*ContentRequestProcessor, event.Event, *agent.Invocation, string, bool, time.Time) {
				p := &ContentRequestProcessor{
					TimelineFilterMode: TimelineFilterCurrentRequest,
				}
				evt := event.Event{
					Version: event.CurrentVersion,
					Response: &model.Response{
						Choices: []model.Choice{
							{
								Message: model.Message{
									Role:    model.RoleUser,
									Content: "content",
								},
							},
						},
					},
					Timestamp:    baseTime,
					RequestID:    "req1",
					InvocationID: "inv1",
				}
				inv := &agent.Invocation{
					InvocationID: "inv1",
					RunOptions:   agent.RunOptions{RequestID: "req1"},
					Message: model.Message{
						Role:    model.RoleUser,
						Content: "content",
					},
				}
				return p, evt, inv, "", true, baseTime
			},
			expected:            true,
			isInvocationMessage: true,
		},
		{
			name: "TimelineFilterCurrentInvocation with same invocation ID",
			setup: func() (*ContentRequestProcessor, event.Event, *agent.Invocation, string, bool, time.Time) {
				p := &ContentRequestProcessor{
					TimelineFilterMode: TimelineFilterCurrentInvocation,
				}
				evt := event.Event{
					Version: event.CurrentVersion,
					Response: &model.Response{
						Choices: []model.Choice{
							{
								Message: model.Message{
									Content: "content",
								},
							},
						},
					},
					Timestamp:    baseTime,
					InvocationID: "inv1",
				}
				inv := &agent.Invocation{
					InvocationID: "inv1",
				}
				return p, evt, inv, "", true, baseTime
			},
			expected: true,
		},
		{
			name: "TimelineFilterCurrentInvocation with different invocation ID and non-user message",
			setup: func() (*ContentRequestProcessor, event.Event, *agent.Invocation, string, bool, time.Time) {
				p := &ContentRequestProcessor{
					TimelineFilterMode: TimelineFilterCurrentInvocation,
				}
				evt := event.Event{
					Version: event.CurrentVersion,
					Response: &model.Response{
						Choices: []model.Choice{
							{
								Message: model.Message{
									Content: "content",
								},
							},
						},
					},
					Timestamp:    baseTime,
					InvocationID: "inv1",
				}
				inv := &agent.Invocation{
					InvocationID: "inv2",
				}
				return p, evt, inv, "", true, baseTime
			},
			expected: false,
		},
		{
			name: "TimelineFilterCurrentInvocation with different invocation ID, user message, but different request ID",
			setup: func() (*ContentRequestProcessor, event.Event, *agent.Invocation, string, bool, time.Time) {
				p := &ContentRequestProcessor{
					TimelineFilterMode: TimelineFilterCurrentInvocation,
				}
				evt := event.Event{
					Version: event.CurrentVersion,
					Response: &model.Response{
						Choices: []model.Choice{
							{Message: model.Message{Content: "test content", Role: model.RoleUser}},
						},
					},
					Timestamp:    baseTime,
					InvocationID: "inv1",
					RequestID:    "req1",
				}
				inv := &agent.Invocation{
					InvocationID: "inv2",
					Message:      model.Message{Content: "test content"},
					RunOptions:   agent.RunOptions{RequestID: "req2"},
				}
				return p, evt, inv, "", true, baseTime
			},
			expected: false,
		},
		{
			name: "TimelineFilterCurrentInvocation with different invocation ID, user message, but different content",
			setup: func() (*ContentRequestProcessor, event.Event, *agent.Invocation, string, bool, time.Time) {
				p := &ContentRequestProcessor{
					TimelineFilterMode: TimelineFilterCurrentInvocation,
				}
				evt := event.Event{
					Version: event.CurrentVersion,
					Response: &model.Response{
						Choices: []model.Choice{
							{Message: model.Message{Content: "content1", Role: model.RoleUser}},
						},
					},
					Timestamp:    baseTime,
					InvocationID: "inv1",
					RequestID:    "req1",
				}
				inv := &agent.Invocation{
					InvocationID: "inv2",
					Message:      model.Message{Content: "content2"},
					RunOptions:   agent.RunOptions{RequestID: "req1"},
				}
				return p, evt, inv, "", true, baseTime
			},
			expected: false,
		},
		{
			name: "TimelineFilterCurrentInvocation with different invocation ID, user message, matching request ID and content",
			setup: func() (*ContentRequestProcessor, event.Event, *agent.Invocation, string, bool, time.Time) {
				p := &ContentRequestProcessor{
					TimelineFilterMode: TimelineFilterCurrentInvocation,
				}
				evt := event.Event{
					Version: event.CurrentVersion,
					Response: &model.Response{
						Choices: []model.Choice{
							{Message: model.Message{Content: "test content", Role: model.RoleUser}},
						},
					},
					Timestamp:    baseTime,
					InvocationID: "inv1",
					RequestID:    "req1",
				}
				inv := &agent.Invocation{
					InvocationID: "inv2",
					Message:      model.Message{Content: "test content"},
					RunOptions:   agent.RunOptions{RequestID: "req1"},
				}
				return p, evt, inv, "", true, baseTime
			},
			expected:            true,
			isInvocationMessage: true,
		},
		{
			name: "BranchFilterModeExact with different filter key",
			setup: func() (*ContentRequestProcessor, event.Event, *agent.Invocation, string, bool, time.Time) {
				p := &ContentRequestProcessor{
					BranchFilterMode: BranchFilterModeExact,
				}
				evt := event.Event{
					Version: event.CurrentVersion,
					Response: &model.Response{
						Choices: []model.Choice{
							{Message: model.Message{Content: "test content"}},
						},
					},
					Timestamp: baseTime,
					FilterKey: "filter1",
				}
				inv := &agent.Invocation{}
				return p, evt, inv, "filter2", true, baseTime
			},
			expected: false,
		},
		{
			name: "BranchFilterModeExact with same filter key",
			setup: func() (*ContentRequestProcessor, event.Event, *agent.Invocation, string, bool, time.Time) {
				p := &ContentRequestProcessor{
					BranchFilterMode: BranchFilterModeExact,
				}
				evt := event.Event{
					Version: event.CurrentVersion,
					Response: &model.Response{
						Choices: []model.Choice{
							{Message: model.Message{Content: "test content"}},
						},
					},
					Timestamp: baseTime,
					FilterKey: "filter1",
				}
				inv := &agent.Invocation{}
				return p, evt, inv, "filter1", true, baseTime
			},
			expected: true,
		},
		{
			name: "BranchFilterModePrefix with non-matching filter",
			setup: func() (*ContentRequestProcessor, event.Event, *agent.Invocation, string, bool, time.Time) {
				p := &ContentRequestProcessor{
					BranchFilterMode: BranchFilterModePrefix,
				}
				evt := event.Event{
					Version:   event.CurrentVersion,
					FilterKey: "filter1",
					Response: &model.Response{
						Choices: []model.Choice{
							{Message: model.Message{Content: "test content"}},
						},
					},
					Timestamp: baseTime,
				}
				inv := &agent.Invocation{}
				return p, evt, inv, "filter", true, baseTime
			},
			expected: false,
		},
		{
			name: "BranchFilterModePrefix with matching filter",
			setup: func() (*ContentRequestProcessor, event.Event, *agent.Invocation, string, bool, time.Time) {
				p := &ContentRequestProcessor{
					BranchFilterMode: BranchFilterModePrefix,
				}
				evt := event.Event{
					Version:   event.CurrentVersion,
					FilterKey: "filter/a",
					Response: &model.Response{
						Choices: []model.Choice{
							{Message: model.Message{Content: "test content"}},
						},
					},
					Timestamp: baseTime,
				}
				inv := &agent.Invocation{}
				return p, evt, inv, "filter", true, baseTime
			},
			expected: true,
		},
		{
			name: "all conditions satisfied",
			setup: func() (*ContentRequestProcessor, event.Event, *agent.Invocation, string, bool, time.Time) {
				p := &ContentRequestProcessor{
					TimelineFilterMode: TimelineFilterCurrentRequest,
					BranchFilterMode:   BranchFilterModeExact,
				}
				evt := event.Event{
					Version: event.CurrentVersion,
					Response: &model.Response{
						Choices: []model.Choice{
							{Message: model.Message{Content: "test content"}},
						},
					},
					Timestamp: baseTime,
					RequestID: "req1",
					FilterKey: "filter1",
				}
				inv := &agent.Invocation{
					RunOptions: agent.RunOptions{RequestID: "req1"},
				}
				return p, evt, inv, "filter1", true, baseTime
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, evt, inv, filter, isZeroTime, since := tt.setup()
			result, isInvocationMessage := p.shouldIncludeEvent(evt, inv, filter, isZeroTime, since)
			if result != tt.expected {
				t.Errorf("shouldIncludeEvent() = %v, want %v", result, tt.expected)
			}
			if isInvocationMessage != tt.isInvocationMessage {
				t.Errorf("shouldIncludeEvent() = %v, want %v", isInvocationMessage, tt.isInvocationMessage)
			}
		})
	}
}

func TestInsertInvocationMessage(t *testing.T) {
	createInvocation := func(id, requestID, content string) *agent.Invocation {
		return &agent.Invocation{
			InvocationID: id,
			RunOptions: agent.RunOptions{
				RequestID: requestID,
			},
			Message: model.Message{
				Role:    model.RoleUser,
				Content: content,
			},
		}
	}

	createEvent := func(requestID, invocationID string, message *model.Message) event.Event {
		evt := event.Event{
			InvocationID: invocationID,
			RequestID:    requestID,
			Response:     &model.Response{},
		}
		if message != nil {
			evt.Response = &model.Response{
				Choices: []model.Choice{
					{Message: *message},
				},
			}
		}
		return evt
	}

	tests := []struct {
		name       string
		events     []event.Event
		invocation *agent.Invocation
		wantEvents []event.Event
		wantLength int
	}{
		{
			name:       "empty content should return original events unchanged",
			events:     []event.Event{createEvent("req1", "inv1", nil)},
			invocation: createInvocation("inv1", "req1", ""),
			wantEvents: []event.Event{createEvent("req1", "inv1", nil)},
			wantLength: 1,
		},
		{
			name:       "empty events slice with non-empty content should insert new event",
			events:     []event.Event{},
			invocation: createInvocation("inv1", "req1", "Hello"),
			wantEvents: []event.Event{createEvent("req1", "inv1", &model.Message{
				Role:    model.RoleUser,
				Content: "Hello",
			})},
			wantLength: 1,
		},
		{
			name: "should append to end when no matching requestID and invocationID found",
			events: []event.Event{
				createEvent("req1", "inv1", nil),
				createEvent("req1", "inv2", nil),
			},
			invocation: createInvocation("inv3", "req1", "New message"),
			wantLength: 3,
		},
		{
			name: "should insert at correct position when match found",
			events: []event.Event{
				createEvent("req1", "inv1", nil),
				createEvent("req1", "inv2", nil),
				createEvent("req1", "inv3", nil),
			},
			invocation: createInvocation("inv2", "req1", "Inserted message"),
			wantEvents: []event.Event{
				createEvent("req1", "inv1", nil),
				createEvent("req1", "inv2", &model.Message{
					Role:    model.RoleUser,
					Content: "Inserted message",
				}),
				createEvent("req1", "inv2", nil),
				createEvent("req1", "inv3", nil),
			},
			wantLength: 4,
		},
		{
			name: "should handle multiple requestIDs correctly",
			events: []event.Event{
				createEvent("req1", "inv1", nil),
				createEvent("req2", "inv3", nil),
				createEvent("req1", "inv2", nil),
			},
			invocation: createInvocation("inv2", "req1", "Message for inv2"),
			wantEvents: []event.Event{
				createEvent("req1", "inv1", nil),
				createEvent("req2", "inv3", nil),
				createEvent("req1", "inv2", &model.Message{
					Role:    model.RoleUser,
					Content: "Message for inv2",
				}),
				createEvent("req1", "inv2", nil),
			},
			wantLength: 4,
		},
		{
			name:       "nil events slice should be handled",
			events:     nil,
			invocation: createInvocation("inv1", "req1", "Test message"),
			wantLength: 1,
			wantEvents: []event.Event{
				createEvent("req1", "inv1", &model.Message{
					Role:    model.RoleUser,
					Content: "Test message",
				}),
			},
		},
	}

	processor := &ContentRequestProcessor{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var inputEvents []event.Event
			if tt.events != nil {
				inputEvents = make([]event.Event, len(tt.events))
				copy(inputEvents, tt.events)
			}

			got := processor.insertInvocationMessage(inputEvents, tt.invocation)

			if len(got) != tt.wantLength {
				t.Errorf("insertInvocationMessage() length = %d, want %d", len(got), tt.wantLength)
			}

			if tt.wantEvents != nil {
				if len(got) != len(tt.wantEvents) {
					t.Errorf("insertInvocationMessage() got %d events, want %d", len(got), len(tt.wantEvents))
				} else {
					for i, evt := range got {
						wantEvt := tt.wantEvents[i]
						if evt.InvocationID != wantEvt.InvocationID || evt.RequestID != wantEvt.RequestID {
							t.Errorf("event at index %d mismatch", i)
						}
						if !reflect.DeepEqual(evt.Response, wantEvt.Response) {
							t.Errorf("event at index %d mismatch", i)
						}
					}
				}
			}
		})
	}
}

func TestContentRequestProcessor_getCurrentInvocationMessages(t *testing.T) {
	baseTime := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	// Helper to create tool call event
	createToolCallEvent := func(invocationID, author, toolCallID string, ts time.Time) event.Event {
		return event.Event{
			Author:       author,
			InvocationID: invocationID,
			Timestamp:    ts,
			Version:      event.CurrentVersion,
			Response: &model.Response{
				Choices: []model.Choice{
					{
						Message: model.Message{
							Role: model.RoleAssistant,
							ToolCalls: []model.ToolCall{
								{
									ID: toolCallID,
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
	}

	// Helper to create tool result event
	createToolResultEvent := func(invocationID, author, toolID, content string, ts time.Time) event.Event {
		return event.Event{
			Author:       author,
			InvocationID: invocationID,
			Timestamp:    ts,
			Version:      event.CurrentVersion,
			Response: &model.Response{
				Object: model.ObjectTypeToolResponse,
				Choices: []model.Choice{
					{
						Message: model.Message{
							Role:    model.RoleTool,
							ToolID:  toolID,
							Content: content,
						},
					},
				},
			},
		}
	}

	// Helper to create assistant event
	createAssistantEvent := func(invocationID, author, content string, ts time.Time) event.Event {
		return event.Event{
			Author:       author,
			InvocationID: invocationID,
			Timestamp:    ts,
			Version:      event.CurrentVersion,
			Response: &model.Response{
				Choices: []model.Choice{
					{
						Message: model.Message{
							Role:    model.RoleAssistant,
							Content: content,
						},
					},
				},
			},
		}
	}

	tests := []struct {
		name            string
		sessionEvents   []event.Event
		invocationID    string
		agentName       string
		invMessage      string
		expectedCount   int
		expectedContent []string
	}{
		{
			name:          "nil session returns nil",
			sessionEvents: nil,
			invocationID:  "inv1",
			expectedCount: 0,
		},
		{
			name: "filters events by invocation ID",
			sessionEvents: []event.Event{
				createAssistantEvent("inv1", "agent1", "message from inv1", baseTime),
				createAssistantEvent("inv2", "agent1", "message from inv2", baseTime.Add(time.Second)),
				createAssistantEvent("inv1", "agent1", "another from inv1", baseTime.Add(2*time.Second)),
			},
			invocationID:    "inv1",
			agentName:       "agent1",
			expectedCount:   2,
			expectedContent: []string{"message from inv1", "another from inv1"},
		},
		{
			name: "includes tool call and tool result from current invocation",
			sessionEvents: []event.Event{
				createToolCallEvent("inv1", "agent1", "call1", baseTime),
				createToolResultEvent("inv1", "agent1", "call1", "tool result", baseTime.Add(time.Second)),
			},
			invocationID:  "inv1",
			agentName:     "agent1",
			expectedCount: 2,
		},
		{
			name: "excludes partial events",
			sessionEvents: []event.Event{
				createAssistantEvent("inv1", "agent1", "complete message", baseTime),
				{
					Author:       "agent1",
					InvocationID: "inv1",
					Timestamp:    baseTime.Add(time.Second),
					Version:      event.CurrentVersion,
					Response: &model.Response{
						IsPartial: true,
						Choices: []model.Choice{
							{
								Delta: model.Message{
									Role:    model.RoleAssistant,
									Content: "partial",
								},
							},
						},
					},
				},
			},
			invocationID:    "inv1",
			agentName:       "agent1",
			expectedCount:   1,
			expectedContent: []string{"complete message"},
		},
		{
			name: "excludes events with nil response",
			sessionEvents: []event.Event{
				createAssistantEvent("inv1", "agent1", "valid message", baseTime),
				{
					Author:       "agent1",
					InvocationID: "inv1",
					Timestamp:    baseTime.Add(time.Second),
					Version:      event.CurrentVersion,
					Response:     nil,
				},
			},
			invocationID:    "inv1",
			agentName:       "agent1",
			expectedCount:   1,
			expectedContent: []string{"valid message"},
		},
		{
			name: "inserts invocation message when not present",
			sessionEvents: []event.Event{
				createAssistantEvent("inv1", "agent1", "assistant reply", baseTime),
			},
			invocationID:    "inv1",
			agentName:       "agent1",
			invMessage:      "user query",
			expectedCount:   2,
			expectedContent: []string{"user query", "assistant reply"},
		},
		{
			name: "does not duplicate invocation message if already present",
			sessionEvents: []event.Event{
				{
					Author:       "user",
					InvocationID: "inv1",
					Timestamp:    baseTime,
					Version:      event.CurrentVersion,
					Response: &model.Response{
						Choices: []model.Choice{
							{
								Message: model.Message{
									Role:    model.RoleUser,
									Content: "user query",
								},
							},
						},
					},
				},
				createAssistantEvent("inv1", "agent1", "assistant reply", baseTime.Add(time.Second)),
			},
			invocationID:    "inv1",
			agentName:       "agent1",
			invMessage:      "user query",
			expectedCount:   2,
			expectedContent: []string{"user query", "assistant reply"},
		},
		{
			name: "full ReAct loop scenario - tool calls visible",
			sessionEvents: []event.Event{
				createToolCallEvent("inv1", "subagent", "tc1", baseTime),
				createToolResultEvent("inv1", "subagent", "tc1", "result1", baseTime.Add(time.Second)),
				createAssistantEvent("inv1", "subagent", "thinking...", baseTime.Add(2*time.Second)),
				createToolCallEvent("inv1", "subagent", "tc2", baseTime.Add(3*time.Second)),
				createToolResultEvent("inv1", "subagent", "tc2", "result2", baseTime.Add(4*time.Second)),
			},
			invocationID:  "inv1",
			agentName:     "subagent",
			expectedCount: 5,
		},
		{
			name: "converts foreign agent events",
			sessionEvents: []event.Event{
				createAssistantEvent("inv1", "other_agent", "foreign message", baseTime),
				createAssistantEvent("inv1", "my_agent", "my message", baseTime.Add(time.Second)),
			},
			invocationID:  "inv1",
			agentName:     "my_agent",
			expectedCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewContentRequestProcessor()

			var sess *session.Session
			if tt.sessionEvents != nil {
				sess = &session.Session{
					Events:  tt.sessionEvents,
					EventMu: sync.RWMutex{},
				}
			}

			inv := &agent.Invocation{
				InvocationID: tt.invocationID,
				AgentName:    tt.agentName,
				Session:      sess,
				Message: model.Message{
					Role:    model.RoleUser,
					Content: tt.invMessage,
				},
			}

			messages := p.getCurrentInvocationMessages(inv)

			assert.Len(t, messages, tt.expectedCount, "unexpected message count")

			if len(tt.expectedContent) > 0 {
				for i, expected := range tt.expectedContent {
					if i < len(messages) {
						assert.Equal(t, expected, messages[i].Content,
							"message %d content mismatch", i)
					}
				}
			}
		})
	}
}

func TestContentRequestProcessor_getCurrentInvocationMessages_IsolatedSubagent(t *testing.T) {
	// This test specifically validates the fix for isolated subagent tool history.
	// When a subagent runs with include_contents=none (isolated mode), it should
	// still see its own tool calls and results within the current invocation.
	baseTime := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	// Simulate a session with events from both parent and subagent invocations
	sess := &session.Session{
		EventMu: sync.RWMutex{},
		Events: []event.Event{
			// Parent invocation events (should be excluded)
			{
				Author:       "parent_agent",
				InvocationID: "parent_inv",
				Timestamp:    baseTime,
				Version:      event.CurrentVersion,
				Response: &model.Response{
					Choices: []model.Choice{
						{Message: model.Message{Role: model.RoleAssistant, Content: "parent message"}},
					},
				},
			},
			// Subagent's first tool call
			{
				Author:       "subagent",
				InvocationID: "subagent_inv",
				Timestamp:    baseTime.Add(time.Second),
				Version:      event.CurrentVersion,
				Response: &model.Response{
					Choices: []model.Choice{
						{
							Message: model.Message{
								Role: model.RoleAssistant,
								ToolCalls: []model.ToolCall{
									{
										ID: "tool_call_1",
										Function: model.FunctionDefinitionParam{
											Name:      "search",
											Arguments: []byte(`{"query":"test"}`),
										},
									},
								},
							},
						},
					},
				},
			},
			// Subagent's first tool result
			{
				Author:       "subagent",
				InvocationID: "subagent_inv",
				Timestamp:    baseTime.Add(2 * time.Second),
				Version:      event.CurrentVersion,
				Response: &model.Response{
					Object: model.ObjectTypeToolResponse,
					Choices: []model.Choice{
						{
							Message: model.Message{
								Role:    model.RoleTool,
								ToolID:  "tool_call_1",
								Content: "search result",
							},
						},
					},
				},
			},
		},
	}

	p := NewContentRequestProcessor()
	inv := &agent.Invocation{
		InvocationID: "subagent_inv",
		AgentName:    "subagent",
		Session:      sess,
		Message: model.Message{
			Role:    model.RoleUser,
			Content: "do something",
		},
	}

	messages := p.getCurrentInvocationMessages(inv)

	// Should have: user message + tool call + tool result = 3 messages
	// (or 2 if user message is inserted differently)
	assert.GreaterOrEqual(t, len(messages), 2, "should include tool call and result")

	// Verify tool call is present
	hasToolCall := false
	hasToolResult := false
	for _, msg := range messages {
		if len(msg.ToolCalls) > 0 {
			hasToolCall = true
		}
		if msg.Role == model.RoleTool && msg.ToolID != "" {
			hasToolResult = true
		}
	}

	assert.True(t, hasToolCall, "should include tool call message")
	assert.True(t, hasToolResult, "should include tool result message")
}

func TestContentRequestProcessor_ProcessReasoningContent(t *testing.T) {
	tests := []struct {
		name             string
		mode             string
		msg              model.Message
		messageRequestID string
		currentRequestID string
		wantReasoning    string
	}{
		{
			name: "keep_all mode preserves reasoning content",
			mode: ReasoningContentModeKeepAll,
			msg: model.Message{
				Role:             model.RoleAssistant,
				Content:          "final answer",
				ReasoningContent: "thinking process",
			},
			messageRequestID: "req-1",
			currentRequestID: "req-2",
			wantReasoning:    "thinking process",
		},
		{
			name: "default mode (empty) uses discard_previous_turns behavior",
			mode: "",
			msg: model.Message{
				Role:             model.RoleAssistant,
				Content:          "final answer",
				ReasoningContent: "thinking process",
			},
			messageRequestID: "req-1",
			currentRequestID: "req-2",
			wantReasoning:    "", // Previous request's reasoning is discarded.
		},
		{
			name: "default mode (empty) keeps current request reasoning",
			mode: "",
			msg: model.Message{
				Role:             model.RoleAssistant,
				Content:          "final answer",
				ReasoningContent: "thinking process",
			},
			messageRequestID: "req-1",
			currentRequestID: "req-1",
			wantReasoning:    "thinking process", // Current request's reasoning is kept.
		},
		{
			name: "discard_all mode removes all reasoning content",
			mode: ReasoningContentModeDiscardAll,
			msg: model.Message{
				Role:             model.RoleAssistant,
				Content:          "final answer",
				ReasoningContent: "thinking process",
			},
			messageRequestID: "req-1",
			currentRequestID: "req-1",
			wantReasoning:    "",
		},
		{
			name: "discard_previous_turns keeps current request reasoning",
			mode: ReasoningContentModeDiscardPreviousTurns,
			msg: model.Message{
				Role:             model.RoleAssistant,
				Content:          "final answer",
				ReasoningContent: "current thinking",
			},
			messageRequestID: "req-1",
			currentRequestID: "req-1",
			wantReasoning:    "current thinking",
		},
		{
			name: "discard_previous_turns removes previous request reasoning",
			mode: ReasoningContentModeDiscardPreviousTurns,
			msg: model.Message{
				Role:             model.RoleAssistant,
				Content:          "final answer",
				ReasoningContent: "old thinking",
			},
			messageRequestID: "req-1",
			currentRequestID: "req-2",
			wantReasoning:    "",
		},
		{
			name: "user message is not processed",
			mode: ReasoningContentModeDiscardAll,
			msg: model.Message{
				Role:             model.RoleUser,
				Content:          "user message",
				ReasoningContent: "should not be touched",
			},
			messageRequestID: "req-1",
			currentRequestID: "req-2",
			wantReasoning:    "should not be touched",
		},
		{
			name: "empty reasoning content is unchanged",
			mode: ReasoningContentModeDiscardAll,
			msg: model.Message{
				Role:             model.RoleAssistant,
				Content:          "final answer",
				ReasoningContent: "",
			},
			messageRequestID: "req-1",
			currentRequestID: "req-2",
			wantReasoning:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &ContentRequestProcessor{
				ReasoningContentMode: tt.mode,
			}
			result := p.processReasoningContent(tt.msg, tt.messageRequestID, tt.currentRequestID)
			assert.Equal(t, tt.wantReasoning, result.ReasoningContent,
				"processReasoningContent() reasoning = %v, want %v",
				result.ReasoningContent, tt.wantReasoning)
		})
	}
}

func TestContentRequestProcessor_WithReasoningContentMode(t *testing.T) {
	tests := []struct {
		name         string
		mode         string
		expectedMode string
	}{
		{
			name:         "set keep_all mode",
			mode:         ReasoningContentModeKeepAll,
			expectedMode: ReasoningContentModeKeepAll,
		},
		{
			name:         "set discard_previous_turns mode",
			mode:         ReasoningContentModeDiscardPreviousTurns,
			expectedMode: ReasoningContentModeDiscardPreviousTurns,
		},
		{
			name:         "set discard_all mode",
			mode:         ReasoningContentModeDiscardAll,
			expectedMode: ReasoningContentModeDiscardAll,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewContentRequestProcessor(
				WithReasoningContentMode(tt.mode),
			)
			assert.Equal(t, tt.expectedMode, p.ReasoningContentMode,
				"WithReasoningContentMode() mode = %v, want %v",
				p.ReasoningContentMode, tt.expectedMode)
		})
	}
}

func TestContentRequestProcessor_GetIncrementMessagesWithReasoningContent(t *testing.T) {
	baseTime := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	// Helper to create an assistant event with reasoning content.
	createAssistantEvent := func(
		requestID, content, reasoning string,
		timestamp time.Time,
	) event.Event {
		return event.Event{
			Author:    "assistant",
			RequestID: requestID,
			FilterKey: "test-filter",
			Timestamp: timestamp,
			Version:   event.CurrentVersion,
			Response: &model.Response{
				Choices: []model.Choice{
					{
						Message: model.Message{
							Role:             model.RoleAssistant,
							Content:          content,
							ReasoningContent: reasoning,
						},
					},
				},
			},
		}
	}

	tests := []struct {
		name                 string
		mode                 string
		events               []event.Event
		currentRequestID     string
		expectedReasonings   []string
		expectedMessageCount int
	}{
		{
			name: "keep_all preserves all reasoning",
			mode: ReasoningContentModeKeepAll,
			events: []event.Event{
				createAssistantEvent("req-1", "answer1", "thinking1", baseTime.Add(time.Second)),
				createAssistantEvent("req-2", "answer2", "thinking2", baseTime.Add(2*time.Second)),
			},
			currentRequestID:     "req-2",
			expectedReasonings:   []string{"thinking1", "thinking2"},
			expectedMessageCount: 2,
		},
		{
			name: "discard_previous_turns keeps only current request reasoning",
			mode: ReasoningContentModeDiscardPreviousTurns,
			events: []event.Event{
				createAssistantEvent("req-1", "answer1", "thinking1", baseTime.Add(time.Second)),
				createAssistantEvent("req-2", "answer2", "thinking2", baseTime.Add(2*time.Second)),
			},
			currentRequestID:     "req-2",
			expectedReasonings:   []string{"", "thinking2"},
			expectedMessageCount: 2,
		},
		{
			name: "discard_all removes all reasoning",
			mode: ReasoningContentModeDiscardAll,
			events: []event.Event{
				createAssistantEvent("req-1", "answer1", "thinking1", baseTime.Add(time.Second)),
				createAssistantEvent("req-2", "answer2", "thinking2", baseTime.Add(2*time.Second)),
			},
			currentRequestID:     "req-2",
			expectedReasonings:   []string{"", ""},
			expectedMessageCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sess := &session.Session{
				Events: tt.events,
			}

			inv := agent.NewInvocation(
				agent.WithInvocationEventFilterKey("test-filter"),
				agent.WithInvocationSession(sess),
				agent.WithInvocationRunOptions(agent.RunOptions{
					RequestID: tt.currentRequestID,
				}),
			)

			p := NewContentRequestProcessor(
				WithReasoningContentMode(tt.mode),
			)

			messages := p.getIncrementMessages(inv, time.Time{})

			assert.Equal(t, tt.expectedMessageCount, len(messages),
				"expected %d messages, got %d", tt.expectedMessageCount, len(messages))

			for i, msg := range messages {
				if i < len(tt.expectedReasonings) {
					assert.Equal(t, tt.expectedReasonings[i], msg.ReasoningContent,
						"message %d: expected reasoning %q, got %q",
						i, tt.expectedReasonings[i], msg.ReasoningContent)
				}
			}
		})
	}
}
