//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package inmemory

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// createUserEvent is a helper function to create user event for testing.
func createUserEventForTest(content string) *event.Event {
	return event.NewResponseEvent(
		"test-inv",
		"test-author",
		&model.Response{
			Done: true,
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role:    model.RoleUser,
						Content: content,
					},
				},
			},
		},
	)
}

// createAssistantEvent is a helper function to create assistant event for
// testing.
func createAssistantEventForTest(content string) *event.Event {
	return event.NewResponseEvent(
		"test-inv",
		"test-author",
		&model.Response{
			Done: true,
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: content,
					},
				},
			},
		},
	)
}

// TestOnConsecutiveUserMessage_InsertPlaceholder tests the placeholder handler.
func TestOnConsecutiveUserMessage_InsertPlaceholder(t *testing.T) {
	insertPlaceholderHandler := func(
		sess *session.Session,
		prev, curr *event.Event,
	) bool {
		finishReason := "error"
		placeholder := event.Event{
			Response: &model.Response{
				ID:        "",
				Object:    model.ObjectTypeChatCompletion,
				Created:   0,
				Done:      true,
				Timestamp: prev.Timestamp,
				Choices: []model.Choice{
					{
						Index: 0,
						Message: model.Message{
							Role:    model.RoleAssistant,
							Content: "[Connection interrupted]",
						},
						FinishReason: &finishReason,
					},
				},
			},
			RequestID:          prev.RequestID,
			InvocationID:       prev.InvocationID,
			ParentInvocationID: prev.ParentInvocationID,
			Author:             "system",
			ID:                 "",
			Timestamp:          prev.Timestamp,
			Branch:             prev.Branch,
			FilterKey:          prev.FilterKey,
			Version:            event.CurrentVersion,
		}
		sess.Events = append(sess.Events, placeholder)
		return true
	}

	svc := NewSessionService(
		WithOnConsecutiveUserMessage(insertPlaceholderHandler),
	)
	defer svc.Close()

	ctx := context.Background()
	key := session.Key{AppName: "test-app", UserID: "user1", SessionID: "session1"}

	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// First user message.
	err = svc.AppendEvent(ctx, sess, createUserEventForTest("hello"))
	require.NoError(t, err)

	// Second user message (consecutive) - handler should insert placeholder.
	err = svc.AppendEvent(ctx, sess, createUserEventForTest("how are you"))
	require.NoError(t, err)

	// Get session and verify.
	retrieved, err := svc.GetSession(ctx, key)
	require.NoError(t, err)

	// Should have 3 events: user, assistant placeholder, user.
	require.Len(t, retrieved.Events, 3)
	assert.Equal(t, model.RoleUser, retrieved.Events[0].Response.Choices[0].Message.Role)
	assert.Equal(t, model.RoleAssistant, retrieved.Events[1].Response.Choices[0].Message.Role)
	assert.Equal(t, "[Connection interrupted]", retrieved.Events[1].Response.Choices[0].Message.Content)
	assert.Equal(t, model.RoleUser, retrieved.Events[2].Response.Choices[0].Message.Role)
}

// TestOnConsecutiveUserMessage_RemovePrevious tests the remove previous handler.
func TestOnConsecutiveUserMessage_RemovePrevious(t *testing.T) {
	removePreviousHandler := func(
		sess *session.Session,
		prev, curr *event.Event,
	) bool {
		if len(sess.Events) > 0 {
			sess.Events = sess.Events[:len(sess.Events)-1]
		}
		return true
	}

	svc := NewSessionService(
		WithOnConsecutiveUserMessage(removePreviousHandler),
	)
	defer svc.Close()

	ctx := context.Background()
	key := session.Key{AppName: "test-app", UserID: "user1", SessionID: "session1"}

	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// First user message.
	err = svc.AppendEvent(ctx, sess, createUserEventForTest("hello"))
	require.NoError(t, err)

	// Second user message (consecutive) - handler should remove previous.
	err = svc.AppendEvent(ctx, sess, createUserEventForTest("how are you"))
	require.NoError(t, err)

	// Get session and verify.
	retrieved, err := svc.GetSession(ctx, key)
	require.NoError(t, err)

	// Should have 1 event: the second user message.
	require.Len(t, retrieved.Events, 1)
	assert.Equal(t, "how are you", retrieved.Events[0].Response.Choices[0].Message.Content)
}

// TestOnConsecutiveUserMessage_SkipCurrent tests the skip current handler.
func TestOnConsecutiveUserMessage_SkipCurrent(t *testing.T) {
	skipCurrentHandler := func(
		sess *session.Session,
		prev, curr *event.Event,
	) bool {
		return false
	}

	svc := NewSessionService(
		WithOnConsecutiveUserMessage(skipCurrentHandler),
	)
	defer svc.Close()

	ctx := context.Background()
	key := session.Key{AppName: "test-app", UserID: "user1", SessionID: "session1"}

	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// First user message.
	err = svc.AppendEvent(ctx, sess, createUserEventForTest("hello"))
	require.NoError(t, err)

	// Second user message (consecutive) - handler should skip it.
	err = svc.AppendEvent(ctx, sess, createUserEventForTest("how are you"))
	require.NoError(t, err)

	// Get session and verify.
	retrieved, err := svc.GetSession(ctx, key)
	require.NoError(t, err)

	// Should have 1 event: only the first user message.
	require.Len(t, retrieved.Events, 1)
	assert.Equal(t, "hello", retrieved.Events[0].Response.Choices[0].Message.Content)
}

// TestOnConsecutiveUserMessage_Custom tests a custom handler that modifies
// previous event.
func TestOnConsecutiveUserMessage_Custom(t *testing.T) {
	// Custom handler that modifies previous event content.
	customHandler := func(sess *session.Session, prev, curr *event.Event) bool {
		prev.Response.Choices[0].Message.Content = "[modified]"
		return true
	}

	svc := NewSessionService(
		WithOnConsecutiveUserMessage(customHandler),
	)
	defer svc.Close()

	ctx := context.Background()
	key := session.Key{AppName: "test-app", UserID: "user1", SessionID: "session1"}

	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// First user message.
	err = svc.AppendEvent(ctx, sess, createUserEventForTest("hello"))
	require.NoError(t, err)

	// Second user message (consecutive) - handler should modify previous.
	err = svc.AppendEvent(ctx, sess, createUserEventForTest("how are you"))
	require.NoError(t, err)

	// Get session and verify.
	retrieved, err := svc.GetSession(ctx, key)
	require.NoError(t, err)

	// Should have 2 events, with first one modified.
	require.Len(t, retrieved.Events, 2)
	assert.Equal(t, "[modified]", retrieved.Events[0].Response.Choices[0].Message.Content)
	assert.Equal(t, "how are you", retrieved.Events[1].Response.Choices[0].Message.Content)
}

// TestOnConsecutiveUserMessage_NoHandler tests behavior when no handler is set.
func TestOnConsecutiveUserMessage_NoHandler(t *testing.T) {
	svc := NewSessionService() // No handler configured.
	defer svc.Close()

	ctx := context.Background()
	key := session.Key{AppName: "test-app", UserID: "user1", SessionID: "session1"}

	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// First user message.
	err = svc.AppendEvent(ctx, sess, createUserEventForTest("hello"))
	require.NoError(t, err)

	// Second user message (consecutive) - no handler, should warn but still append.
	err = svc.AppendEvent(ctx, sess, createUserEventForTest("how are you"))
	require.NoError(t, err)

	// Get session and verify - both events should be present.
	retrieved, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	require.Len(t, retrieved.Events, 2)
}

// TestOnConsecutiveUserMessage_WithNormalFlow tests that the handler is not
// triggered when messages alternate normally.
func TestOnConsecutiveUserMessage_WithNormalFlow(t *testing.T) {
	handlerCalled := false
	handler := func(sess *session.Session, prev, curr *event.Event) bool {
		handlerCalled = true
		return true
	}

	svc := NewSessionService(
		WithOnConsecutiveUserMessage(handler),
	)
	defer svc.Close()

	ctx := context.Background()
	key := session.Key{AppName: "test-app", UserID: "user1", SessionID: "session1"}

	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// User message.
	err = svc.AppendEvent(ctx, sess, createUserEventForTest("hello"))
	require.NoError(t, err)

	// Assistant message.
	err = svc.AppendEvent(ctx, sess, createAssistantEventForTest("hi there"))
	require.NoError(t, err)

	// Another user message - not consecutive user messages.
	err = svc.AppendEvent(ctx, sess, createUserEventForTest("how are you"))
	require.NoError(t, err)

	// Handler should not have been called.
	assert.False(t, handlerCalled)

	// Get session and verify.
	retrieved, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	require.Len(t, retrieved.Events, 3)
}

// TestOnConsecutiveUserMessage_MultipleConsecutive tests handling of multiple
// consecutive user messages.
func TestOnConsecutiveUserMessage_MultipleConsecutive(t *testing.T) {
	insertPlaceholderHandler := func(
		sess *session.Session,
		prev, curr *event.Event,
	) bool {
		finishReason := "error"
		placeholder := event.Event{
			Response: &model.Response{
				ID:        "",
				Object:    model.ObjectTypeChatCompletion,
				Created:   0,
				Done:      true,
				Timestamp: prev.Timestamp,
				Choices: []model.Choice{
					{
						Index: 0,
						Message: model.Message{
							Role:    model.RoleAssistant,
							Content: "[Connection interrupted]",
						},
						FinishReason: &finishReason,
					},
				},
			},
			Author:  "system",
			Version: event.CurrentVersion,
		}
		sess.Events = append(sess.Events, placeholder)
		return true
	}

	svc := NewSessionService(
		WithOnConsecutiveUserMessage(insertPlaceholderHandler),
	)
	defer svc.Close()

	ctx := context.Background()
	key := session.Key{AppName: "test-app", UserID: "user1", SessionID: "session1"}

	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Three consecutive user messages.
	err = svc.AppendEvent(ctx, sess, createUserEventForTest("message 1"))
	require.NoError(t, err)
	err = svc.AppendEvent(ctx, sess, createUserEventForTest("message 2"))
	require.NoError(t, err)
	err = svc.AppendEvent(ctx, sess, createUserEventForTest("message 3"))
	require.NoError(t, err)

	// Get session and verify.
	retrieved, err := svc.GetSession(ctx, key)
	require.NoError(t, err)

	// Should have: user1, placeholder, user2, placeholder, user3.
	require.Len(t, retrieved.Events, 5)
	assert.Equal(t, model.RoleUser, retrieved.Events[0].Response.Choices[0].Message.Role)
	assert.Equal(t, model.RoleAssistant, retrieved.Events[1].Response.Choices[0].Message.Role)
	assert.Equal(t, model.RoleUser, retrieved.Events[2].Response.Choices[0].Message.Role)
	assert.Equal(t, model.RoleAssistant, retrieved.Events[3].Response.Choices[0].Message.Role)
	assert.Equal(t, model.RoleUser, retrieved.Events[4].Response.Choices[0].Message.Role)
}

// TestOnConsecutiveUserMessage_ThreadSafety tests concurrent access.
func TestOnConsecutiveUserMessage_ThreadSafety(t *testing.T) {
	var mu sync.Mutex
	callCount := 0

	handler := func(sess *session.Session, prev, curr *event.Event) bool {
		mu.Lock()
		callCount++
		mu.Unlock()
		return true
	}

	svc := NewSessionService(
		WithOnConsecutiveUserMessage(handler),
	)
	defer svc.Close()

	ctx := context.Background()
	key := session.Key{AppName: "test-app", UserID: "user1", SessionID: "session1"}

	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Initial user message.
	err = svc.AppendEvent(ctx, sess, createUserEventForTest("initial"))
	require.NoError(t, err)

	// Concurrent user messages.
	var wg sync.WaitGroup
	const numGoroutines = 10

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			evt := createUserEventForTest(fmt.Sprintf("message %d", idx))
			_ = svc.AppendEvent(ctx, sess, evt)
		}(i)
	}

	wg.Wait()

	// Verify no panics occurred and handler was called.
	mu.Lock()
	assert.Greater(t, callCount, 0, "Handler should have been called at least once")
	mu.Unlock()
}
