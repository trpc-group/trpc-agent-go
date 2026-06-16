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

	"trpc.group/trpc-go/trpc-agent-go/evolution"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

type mockEvolutionService struct {
	enqueueCalled bool
	enqueueErr    error
	closeCalled   int
	closeErr      error
	job           evolution.LearningJob
}

func (m *mockEvolutionService) EnqueueLearningJob(_ context.Context, job evolution.LearningJob) error {
	m.enqueueCalled = true
	m.job = job
	return m.enqueueErr
}

func (m *mockEvolutionService) Close() error {
	m.closeCalled++
	return m.closeErr
}

func TestWithEvolutionService(t *testing.T) {
	t.Run("sets evolution service in options", func(t *testing.T) {
		evolutionService := &mockEvolutionService{}
		opts := &Options{}

		option := WithEvolutionService(evolutionService)
		option(opts)

		require.Equal(t, evolutionService, opts.evolutionService)
	})

	t.Run("sets nil evolution service", func(t *testing.T) {
		opts := &Options{}

		option := WithEvolutionService(nil)
		option(opts)

		require.Nil(t, opts.evolutionService)
	})
}

func TestEnqueueEvolutionLearningJob(t *testing.T) {
	t.Run("nil evolution service", func(t *testing.T) {
		r := &runner{evolutionService: nil}
		sess := session.NewSession("app", "user", "sess")
		r.enqueueEvolutionLearningJob(context.Background(), sess)
	})

	t.Run("nil session", func(t *testing.T) {
		mockSvc := &mockEvolutionService{}
		r := &runner{evolutionService: mockSvc}
		r.enqueueEvolutionLearningJob(context.Background(), nil)
		require.False(t, mockSvc.enqueueCalled)
	})

	t.Run("enqueues job with session and nil outcome", func(t *testing.T) {
		mockSvc := &mockEvolutionService{}
		r := &runner{evolutionService: mockSvc}
		sess := session.NewSession("app", "user", "sess")
		r.enqueueEvolutionLearningJob(context.Background(), sess)
		require.True(t, mockSvc.enqueueCalled)
		require.Same(t, sess, mockSvc.job.Session)
		require.Nil(t, mockSvc.job.Outcome,
			"runner hook must not invent an outcome; only the caller knows the verdict")
	})

	t.Run("handles enqueue error gracefully", func(t *testing.T) {
		mockSvc := &mockEvolutionService{enqueueErr: errors.New("queue full")}
		r := &runner{evolutionService: mockSvc}
		sess := session.NewSession("app", "user", "sess")
		r.enqueueEvolutionLearningJob(context.Background(), sess)
		require.True(t, mockSvc.enqueueCalled)
	})
}

func TestRunner_WithEvolutionService_Integration(t *testing.T) {
	mockEvolutionSvc := &mockEvolutionService{}
	sessSvc := sessioninmemory.NewSessionService()
	mockAgent := &mockAgent{name: "test-agent"}

	r := NewRunner("test-app", mockAgent,
		WithSessionService(sessSvc),
		WithEvolutionService(mockEvolutionSvc),
	)

	ctx := context.Background()
	eventCh, err := r.Run(ctx, "user", "session", model.NewUserMessage("hello"))
	require.NoError(t, err)

	for range eventCh {
	}

	require.True(t, mockEvolutionSvc.enqueueCalled)
	require.NotNil(t, mockEvolutionSvc.job.Session)
	require.Equal(t, "test-app", mockEvolutionSvc.job.Session.AppName)
	require.Equal(t, "user", mockEvolutionSvc.job.Session.UserID)
	require.Nil(t, mockEvolutionSvc.job.Outcome,
		"runner-driven hook must not synthesize an outcome")
}

func TestRunnerClose_DoesNotCloseEvolutionService(t *testing.T) {
	mockEvolutionSvc := &mockEvolutionService{}
	mockAgent := &mockAgent{name: "test-agent"}

	r := NewRunner("test-app", mockAgent, WithEvolutionService(mockEvolutionSvc))
	require.NoError(t, r.Close())
	require.Equal(t, 0, mockEvolutionSvc.closeCalled)

	// Close should be idempotent.
	require.NoError(t, r.Close())
	require.Equal(t, 0, mockEvolutionSvc.closeCalled)

	require.NoError(t, mockEvolutionSvc.Close())
	require.Equal(t, 1, mockEvolutionSvc.closeCalled)
}

var _ evolution.Service = (*mockEvolutionService)(nil)
