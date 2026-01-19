//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package session

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// createUserEvent is a helper function to create user event for testing.
func createUserEvent(content string) *event.Event {
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
func createAssistantEvent(content string) *event.Event {
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

// TestOnConsecutiveUserMessage_InsertPlaceholder tests the
// InsertPlaceholderHandler.
func TestOnConsecutiveUserMessage_InsertPlaceholder(t *testing.T) {
	insertPlaceholderHandler := func(sess *Session, prev, curr *event.Event) bool {
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

	sess := NewSession(
		"test-app",
		"user1",
		"session1",
		WithOnConsecutiveUserMessage(insertPlaceholderHandler),
	)

	// Append first user message.
	userEvent1 := createUserEvent("hello")
	sess.UpdateUserSession(userEvent1)
	assert.Len(t, sess.Events, 1)

	// Append second user message (simulates broken connection).
	userEvent2 := createUserEvent("how are you")
	sess.UpdateUserSession(userEvent2)

	// Should have 3 events: user1, placeholder assistant, user2.
	require.Len(t, sess.Events, 3)
	assert.True(t, sess.Events[0].Response.IsUserMessage())
	assert.Equal(
		t,
		model.RoleAssistant,
		sess.Events[1].Response.Choices[0].Message.Role,
	)
	assert.True(t, sess.Events[2].Response.IsUserMessage())

	// Verify placeholder content.
	assert.Contains(
		t,
		sess.Events[1].Response.Choices[0].Message.Content,
		"interrupted",
	)
}

// TestOnConsecutiveUserMessage_RemovePrevious tests the RemovePreviousHandler.
func TestOnConsecutiveUserMessage_RemovePrevious(t *testing.T) {
	removePreviousHandler := func(sess *Session, prev, curr *event.Event) bool {
		if len(sess.Events) > 0 {
			sess.Events = sess.Events[:len(sess.Events)-1]
		}
		return true
	}

	sess := NewSession(
		"test-app",
		"user1",
		"session1",
		WithOnConsecutiveUserMessage(removePreviousHandler),
	)

	userEvent1 := createUserEvent("hello")
	sess.UpdateUserSession(userEvent1)
	assert.Len(t, sess.Events, 1)

	userEvent2 := createUserEvent("how are you")
	sess.UpdateUserSession(userEvent2)

	// Should have 1 event: only user2 (user1 removed).
	require.Len(t, sess.Events, 1)
	assert.Equal(
		t,
		"how are you",
		sess.Events[0].Response.Choices[0].Message.Content,
	)
}

// TestOnConsecutiveUserMessage_SkipCurrent tests the SkipCurrentHandler.
func TestOnConsecutiveUserMessage_SkipCurrent(t *testing.T) {
	skipCurrentHandler := func(sess *Session, prev, curr *event.Event) bool {
		return false
	}

	sess := NewSession(
		"test-app",
		"user1",
		"session1",
		WithOnConsecutiveUserMessage(skipCurrentHandler),
	)

	userEvent1 := createUserEvent("hello")
	sess.UpdateUserSession(userEvent1)
	assert.Len(t, sess.Events, 1)

	userEvent2 := createUserEvent("how are you")
	sess.UpdateUserSession(userEvent2)

	// Should have 1 event: only user1 (user2 skipped).
	require.Len(t, sess.Events, 1)
	assert.Equal(t, "hello", sess.Events[0].Response.Choices[0].Message.Content)
}

// TestOnConsecutiveUserMessage_Custom tests a custom handler that merges
// messages.
func TestOnConsecutiveUserMessage_Custom(t *testing.T) {
	customHandler := func(
		sess *Session, prev, curr *event.Event,
	) bool {
		// Merge two user messages.
		merged := fmt.Sprintf(
			"%s\n[After reconnection]\n%s",
			prev.Response.Choices[0].Message.Content,
			curr.Response.Choices[0].Message.Content,
		)
		curr.Response.Choices[0].Message.Content = merged

		// Remove previous.
		sess.Events = sess.Events[:len(sess.Events)-1]
		return true
	}

	sess := NewSession(
		"test-app",
		"user1",
		"session1",
		WithOnConsecutiveUserMessage(customHandler),
	)

	userEvent1 := createUserEvent("hello")
	sess.UpdateUserSession(userEvent1)

	userEvent2 := createUserEvent("how are you")
	sess.UpdateUserSession(userEvent2)

	require.Len(t, sess.Events, 1)
	assert.Contains(
		t,
		sess.Events[0].Response.Choices[0].Message.Content,
		"hello",
	)
	assert.Contains(
		t,
		sess.Events[0].Response.Choices[0].Message.Content,
		"how are you",
	)
	assert.Contains(
		t,
		sess.Events[0].Response.Choices[0].Message.Content,
		"reconnection",
	)
}

// TestOnConsecutiveUserMessage_NoHandler tests the behavior without a handler.
func TestOnConsecutiveUserMessage_NoHandler(t *testing.T) {
	// No handler configured (default behavior).
	sess := NewSession("test-app", "user1", "session1")

	userEvent1 := createUserEvent("hello")
	sess.UpdateUserSession(userEvent1)

	userEvent2 := createUserEvent("how are you")
	sess.UpdateUserSession(userEvent2)

	// Should have 2 consecutive user messages (not fixed).
	require.Len(t, sess.Events, 2)
	assert.True(t, sess.Events[0].Response.IsUserMessage())
	assert.True(t, sess.Events[1].Response.IsUserMessage())
	// This would cause API errors, but it's the default behavior without
	// handler configuration.
}

// TestOnConsecutiveUserMessage_WithNormalFlow tests that the handler is not
// triggered when messages alternate normally.
func TestOnConsecutiveUserMessage_WithNormalFlow(t *testing.T) {
	insertPlaceholderHandler := func(sess *Session, prev, curr *event.Event) bool {
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

	sess := NewSession(
		"test-app",
		"user1",
		"session1",
		WithOnConsecutiveUserMessage(insertPlaceholderHandler),
	)

	userEvent1 := createUserEvent("hello")
	sess.UpdateUserSession(userEvent1)
	require.Len(t, sess.Events, 1)

	assistantEvent := createAssistantEvent("Hi there!")
	sess.UpdateUserSession(assistantEvent)
	require.Len(t, sess.Events, 2)

	userEvent2 := createUserEvent("how are you")
	sess.UpdateUserSession(userEvent2)

	// Should have 3 events: user1, assistant, user2 (no placeholder inserted).
	require.Len(t, sess.Events, 3)
	assert.True(t, sess.Events[0].Response.IsUserMessage())
	assert.Equal(
		t,
		model.RoleAssistant,
		sess.Events[1].Response.Choices[0].Message.Role,
	)
	assert.True(t, sess.Events[2].Response.IsUserMessage())

	// Verify no placeholder was inserted.
	assert.Equal(
		t,
		"Hi there!",
		sess.Events[1].Response.Choices[0].Message.Content,
	)
}

// TestOnConsecutiveUserMessage_MultipleConsecutive tests handling of
// multiple consecutive user messages.
func TestOnConsecutiveUserMessage_MultipleConsecutive(t *testing.T) {
	insertPlaceholderHandler := func(sess *Session, prev, curr *event.Event) bool {
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

	sess := NewSession(
		"test-app",
		"user1",
		"session1",
		WithOnConsecutiveUserMessage(insertPlaceholderHandler),
	)

	userEvent1 := createUserEvent("first")
	sess.UpdateUserSession(userEvent1)
	require.Len(t, sess.Events, 1)

	userEvent2 := createUserEvent("second")
	sess.UpdateUserSession(userEvent2)
	require.Len(t, sess.Events, 3) // first, placeholder, second.

	userEvent3 := createUserEvent("third")
	sess.UpdateUserSession(userEvent3)
	require.Len(t, sess.Events, 5) // first, placeholder, second, placeholder, third.

	// Verify pattern: user, assistant, user, assistant, user.
	assert.True(t, sess.Events[0].Response.IsUserMessage())
	assert.Equal(
		t,
		model.RoleAssistant,
		sess.Events[1].Response.Choices[0].Message.Role,
	)
	assert.True(t, sess.Events[2].Response.IsUserMessage())
	assert.Equal(
		t,
		model.RoleAssistant,
		sess.Events[3].Response.Choices[0].Message.Role,
	)
	assert.True(t, sess.Events[4].Response.IsUserMessage())
}

// TestOnConsecutiveUserMessage_ThreadSafety tests concurrent access to the
// handler.
func TestOnConsecutiveUserMessage_ThreadSafety(t *testing.T) {
	insertPlaceholderHandler := func(sess *Session, prev, curr *event.Event) bool {
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

	sess := NewSession(
		"test-app",
		"user1",
		"session1",
		WithOnConsecutiveUserMessage(insertPlaceholderHandler),
	)

	// Append initial user message.
	userEvent1 := createUserEvent("initial")
	sess.UpdateUserSession(userEvent1)

	// Concurrently append user messages.
	const numGoroutines = 10
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			userEvent := createUserEvent(fmt.Sprintf("message-%d", idx))
			sess.UpdateUserSession(userEvent)
		}(i)
	}

	wg.Wait()

	// Verify we have events (exact count depends on timing, but should be > 1).
	assert.Greater(t, len(sess.Events), 1)

	// Verify no two consecutive user messages exist.
	for i := 1; i < len(sess.Events); i++ {
		if sess.Events[i-1].Response.IsUserMessage() &&
			sess.Events[i].Response.IsUserMessage() {
			t.Errorf(
				"found consecutive user messages at positions %d and %d",
				i-1,
				i,
			)
		}
	}
}
