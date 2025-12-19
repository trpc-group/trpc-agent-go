//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestExtractMessagesFromSession(t *testing.T) {
	t.Run("nil session", func(t *testing.T) {
		messages := extractMessagesFromSession(nil)
		require.Nil(t, messages)
	})

	t.Run("empty events", func(t *testing.T) {
		sess := session.NewSession("app", "user", "sess")
		messages := extractMessagesFromSession(sess)
		require.Nil(t, messages)
	})

	t.Run("no user message found", func(t *testing.T) {
		sess := session.NewSession("app", "user", "sess")
		sess.Events = []event.Event{
			{
				Response: &model.Response{
					Choices: []model.Choice{
						{Message: model.NewAssistantMessage("assistant reply")},
					},
				},
			},
		}
		messages := extractMessagesFromSession(sess)
		require.Nil(t, messages)
	})

	t.Run("event with nil response", func(t *testing.T) {
		sess := session.NewSession("app", "user", "sess")
		sess.Events = []event.Event{
			{Response: nil},
			{
				Response: &model.Response{
					Choices: []model.Choice{
						{Message: model.NewUserMessage("hello")},
					},
				},
			},
		}
		messages := extractMessagesFromSession(sess)
		require.Len(t, messages, 1)
		require.Equal(t, "hello", messages[0].Content)
	})

	t.Run("extracts user and assistant messages", func(t *testing.T) {
		sess := session.NewSession("app", "user", "sess")
		sess.Events = []event.Event{
			{
				Response: &model.Response{
					Choices: []model.Choice{
						{Message: model.NewUserMessage("hello")},
					},
				},
			},
			{
				Response: &model.Response{
					Choices: []model.Choice{
						{Message: model.NewAssistantMessage("hi there")},
					},
				},
			},
		}
		messages := extractMessagesFromSession(sess)
		require.Len(t, messages, 2)
		require.Equal(t, model.RoleUser, messages[0].Role)
		require.Equal(t, "hello", messages[0].Content)
		require.Equal(t, model.RoleAssistant, messages[1].Role)
		require.Equal(t, "hi there", messages[1].Content)
	})

	t.Run("skips messages with tool calls", func(t *testing.T) {
		sess := session.NewSession("app", "user", "sess")
		sess.Events = []event.Event{
			{
				Response: &model.Response{
					Choices: []model.Choice{
						{Message: model.NewUserMessage("call a tool")},
					},
				},
			},
			{
				Response: &model.Response{
					Choices: []model.Choice{
						{Message: model.Message{
							Role:    model.RoleAssistant,
							Content: "calling tool",
							ToolCalls: []model.ToolCall{
								{ID: "tool1", Function: model.FunctionDefinitionParam{Name: "test"}},
							},
						}},
					},
				},
			},
			{
				Response: &model.Response{
					Choices: []model.Choice{
						{Message: model.NewAssistantMessage("tool result processed")},
					},
				},
			},
		}
		messages := extractMessagesFromSession(sess)
		require.Len(t, messages, 2)
		require.Equal(t, "call a tool", messages[0].Content)
		require.Equal(t, "tool result processed", messages[1].Content)
	})

	t.Run("skips messages with empty content", func(t *testing.T) {
		sess := session.NewSession("app", "user", "sess")
		sess.Events = []event.Event{
			{
				Response: &model.Response{
					Choices: []model.Choice{
						{Message: model.NewUserMessage("hello")},
					},
				},
			},
			{
				Response: &model.Response{
					Choices: []model.Choice{
						{Message: model.NewAssistantMessage("")},
					},
				},
			},
			{
				Response: &model.Response{
					Choices: []model.Choice{
						{Message: model.NewAssistantMessage("valid reply")},
					},
				},
			},
		}
		messages := extractMessagesFromSession(sess)
		require.Len(t, messages, 2)
		require.Equal(t, "hello", messages[0].Content)
		require.Equal(t, "valid reply", messages[1].Content)
	})

	t.Run("extracts from last user message only", func(t *testing.T) {
		sess := session.NewSession("app", "user", "sess")
		sess.Events = []event.Event{
			{
				Response: &model.Response{
					Choices: []model.Choice{
						{Message: model.NewUserMessage("first question")},
					},
				},
			},
			{
				Response: &model.Response{
					Choices: []model.Choice{
						{Message: model.NewAssistantMessage("first answer")},
					},
				},
			},
			{
				Response: &model.Response{
					Choices: []model.Choice{
						{Message: model.NewUserMessage("second question")},
					},
				},
			},
			{
				Response: &model.Response{
					Choices: []model.Choice{
						{Message: model.NewAssistantMessage("second answer")},
					},
				},
			},
		}
		messages := extractMessagesFromSession(sess)
		// Should only extract from the last user message onwards.
		require.Len(t, messages, 2)
		require.Equal(t, "second question", messages[0].Content)
		require.Equal(t, "second answer", messages[1].Content)
	})

	t.Run("user message with empty content not considered", func(t *testing.T) {
		sess := session.NewSession("app", "user", "sess")
		sess.Events = []event.Event{
			{
				Response: &model.Response{
					Choices: []model.Choice{
						{Message: model.NewUserMessage("valid user message")},
					},
				},
			},
			{
				Response: &model.Response{
					Choices: []model.Choice{
						{Message: model.NewAssistantMessage("reply")},
					},
				},
			},
			{
				Response: &model.Response{
					Choices: []model.Choice{
						{Message: model.Message{Role: model.RoleUser, Content: ""}},
					},
				},
			},
		}
		messages := extractMessagesFromSession(sess)
		// The empty user message should not be the "last user message".
		require.Len(t, messages, 2)
		require.Equal(t, "valid user message", messages[0].Content)
		require.Equal(t, "reply", messages[1].Content)
	})

	t.Run("handles nil response in extraction loop", func(t *testing.T) {
		sess := session.NewSession("app", "user", "sess")
		sess.Events = []event.Event{
			{
				Response: &model.Response{
					Choices: []model.Choice{
						{Message: model.NewUserMessage("hello")},
					},
				},
			},
			{Response: nil},
			{
				Response: &model.Response{
					Choices: []model.Choice{
						{Message: model.NewAssistantMessage("world")},
					},
				},
			},
		}
		messages := extractMessagesFromSession(sess)
		require.Len(t, messages, 2)
		require.Equal(t, "hello", messages[0].Content)
		require.Equal(t, "world", messages[1].Content)
	})
}

// mockMemoryServiceForAutoMemory implements memory.Service for testing auto memory.
type mockMemoryServiceForAutoMemory struct {
	enqueueCalled bool
	enqueueErr    error
	userKey       memory.UserKey
	messages      []model.Message
}

func (m *mockMemoryServiceForAutoMemory) AddMemory(ctx context.Context, userKey memory.UserKey, memoryStr string, topics []string) error {
	return nil
}

func (m *mockMemoryServiceForAutoMemory) UpdateMemory(ctx context.Context, memoryKey memory.Key, memoryStr string, topics []string) error {
	return nil
}

func (m *mockMemoryServiceForAutoMemory) DeleteMemory(ctx context.Context, memoryKey memory.Key) error {
	return nil
}

func (m *mockMemoryServiceForAutoMemory) ClearMemories(ctx context.Context, userKey memory.UserKey) error {
	return nil
}

func (m *mockMemoryServiceForAutoMemory) ReadMemories(ctx context.Context, userKey memory.UserKey, limit int) ([]*memory.Entry, error) {
	return nil, nil
}

func (m *mockMemoryServiceForAutoMemory) SearchMemories(ctx context.Context, userKey memory.UserKey, query string) ([]*memory.Entry, error) {
	return nil, nil
}

func (m *mockMemoryServiceForAutoMemory) Tools() []tool.Tool {
	return nil
}

func (m *mockMemoryServiceForAutoMemory) EnqueueAutoMemoryJob(ctx context.Context, userKey memory.UserKey, messages []model.Message) error {
	m.enqueueCalled = true
	m.userKey = userKey
	m.messages = messages
	return m.enqueueErr
}

func (m *mockMemoryServiceForAutoMemory) Close() error {
	return nil
}

func TestEnqueueAutoMemoryJob(t *testing.T) {
	t.Run("nil memory service", func(t *testing.T) {
		r := &runner{
			memoryService: nil,
		}
		sess := session.NewSession("app", "user", "sess")
		// Should not panic with nil memory service.
		r.enqueueAutoMemoryJob(context.Background(), sess)
	})

	t.Run("empty messages", func(t *testing.T) {
		mockSvc := &mockMemoryServiceForAutoMemory{}
		r := &runner{
			memoryService: mockSvc,
		}
		sess := session.NewSession("app", "user", "sess")
		// No events, so no messages to extract.
		r.enqueueAutoMemoryJob(context.Background(), sess)
		require.False(t, mockSvc.enqueueCalled)
	})

	t.Run("enqueues job with messages", func(t *testing.T) {
		mockSvc := &mockMemoryServiceForAutoMemory{}
		r := &runner{
			memoryService: mockSvc,
		}
		sess := session.NewSession("app", "user", "sess")
		sess.Events = []event.Event{
			{
				Response: &model.Response{
					Choices: []model.Choice{
						{Message: model.NewUserMessage("hello")},
					},
				},
			},
			{
				Response: &model.Response{
					Choices: []model.Choice{
						{Message: model.NewAssistantMessage("hi")},
					},
				},
			},
		}
		r.enqueueAutoMemoryJob(context.Background(), sess)
		require.True(t, mockSvc.enqueueCalled)
		require.Equal(t, "app", mockSvc.userKey.AppName)
		require.Equal(t, "user", mockSvc.userKey.UserID)
		require.Len(t, mockSvc.messages, 2)
	})

	t.Run("handles enqueue error gracefully", func(t *testing.T) {
		mockSvc := &mockMemoryServiceForAutoMemory{
			enqueueErr: errors.New("queue full"),
		}
		r := &runner{
			memoryService: mockSvc,
		}
		sess := session.NewSession("app", "user", "sess")
		sess.Events = []event.Event{
			{
				Response: &model.Response{
					Choices: []model.Choice{
						{Message: model.NewUserMessage("hello")},
					},
				},
			},
		}
		// Should not panic even if enqueue fails.
		r.enqueueAutoMemoryJob(context.Background(), sess)
		require.True(t, mockSvc.enqueueCalled)
	})
}

func TestRunner_WithMemoryService_AutoMemoryIntegration(t *testing.T) {
	// Test that runner calls enqueueAutoMemoryJob on completion.
	mockMemSvc := &mockMemoryServiceForAutoMemory{}
	sessionService := sessioninmemory.NewSessionService()
	mockAgent := &mockAgent{name: "test-agent"}

	r := NewRunner("test-app", mockAgent,
		WithSessionService(sessionService),
		WithMemoryService(mockMemSvc),
	)

	ctx := context.Background()
	eventCh, err := r.Run(ctx, "user", "session", model.NewUserMessage("hello"))
	require.NoError(t, err)

	// Consume all events.
	for range eventCh {
	}

	// Verify that auto memory job was enqueued.
	require.True(t, mockMemSvc.enqueueCalled)
	require.Equal(t, "test-app", mockMemSvc.userKey.AppName)
	require.Equal(t, "user", mockMemSvc.userKey.UserID)
	require.NotEmpty(t, mockMemSvc.messages)
}
