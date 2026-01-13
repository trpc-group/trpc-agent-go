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
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// mockMemoryServiceForAutoMemory implements memory.Service for testing auto memory.
type mockMemoryServiceForAutoMemory struct {
	enqueueCalled bool
	enqueueErr    error
	sess          *session.Session
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

func (m *mockMemoryServiceForAutoMemory) EnqueueAutoMemoryJob(ctx context.Context, sess *session.Session) error {
	m.enqueueCalled = true
	m.sess = sess
	return m.enqueueErr
}

func (m *mockMemoryServiceForAutoMemory) Close() error {
	return nil
}

func TestEnqueueAutoMemoryJob(t *testing.T) {
	t.Run("nil memory service", func(t *testing.T) {
		r := &runner{memoryService: nil}
		sess := session.NewSession("app", "user", "sess")
		// Should not panic with nil memory service.
		r.enqueueAutoMemoryJob(context.Background(), sess)
	})

	t.Run("nil session", func(t *testing.T) {
		mockSvc := &mockMemoryServiceForAutoMemory{}
		r := &runner{memoryService: mockSvc}
		// Should not panic with nil session.
		r.enqueueAutoMemoryJob(context.Background(), nil)
		require.False(t, mockSvc.enqueueCalled)
	})

	t.Run("enqueues job with session", func(t *testing.T) {
		mockSvc := &mockMemoryServiceForAutoMemory{}
		r := &runner{memoryService: mockSvc}
		sess := session.NewSession("app", "user", "sess")
		r.enqueueAutoMemoryJob(context.Background(), sess)
		require.True(t, mockSvc.enqueueCalled)
		require.Same(t, sess, mockSvc.sess)
	})

	t.Run("handles enqueue error gracefully", func(t *testing.T) {
		mockSvc := &mockMemoryServiceForAutoMemory{enqueueErr: errors.New("queue full")}
		r := &runner{memoryService: mockSvc}
		sess := session.NewSession("app", "user", "sess")
		// Should not panic even if enqueue fails.
		r.enqueueAutoMemoryJob(context.Background(), sess)
		require.True(t, mockSvc.enqueueCalled)
	})
}

func TestRunner_WithMemoryService_AutoMemoryIntegration(t *testing.T) {
	mockMemSvc := &mockMemoryServiceForAutoMemory{}
	sessSvc := sessioninmemory.NewSessionService()
	mockAgent := &mockAgent{name: "test-agent"}

	r := NewRunner("test-app", mockAgent,
		WithSessionService(sessSvc),
		WithMemoryService(mockMemSvc),
	)

	ctx := context.Background()
	eventCh, err := r.Run(ctx, "user", "session", model.NewUserMessage("hello"))
	require.NoError(t, err)

	for range eventCh {
	}

	require.True(t, mockMemSvc.enqueueCalled)
	require.NotNil(t, mockMemSvc.sess)
	require.Equal(t, "test-app", mockMemSvc.sess.AppName)
	require.Equal(t, "user", mockMemSvc.sess.UserID)
}
