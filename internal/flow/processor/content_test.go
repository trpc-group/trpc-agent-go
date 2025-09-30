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

			messages := p.getHistoryMessages(inv, tt.summaryUpdatedAt)

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
	messages := p.getHistoryMessages(inv, time.Time{})
	assert.Len(t, messages, 1, "Should get one message")
	assert.Equal(t, "test message", messages[0].Content)

	// Test with minimal concurrency (2 goroutines only)
	var wg sync.WaitGroup
	results := make(chan int, 4)

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			messages := p.getHistoryMessages(inv, time.Time{})
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

			messages := tt.processor.getHistoryMessages(inv, time.Time{})

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
