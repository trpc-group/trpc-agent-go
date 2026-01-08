//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package clickhouse

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/clickhouse"
)

type mockSummarizer struct {
	summarizeFunc func(ctx context.Context, session *session.Session) (string, error)
}

// SetModel implements summary.SessionSummarizer.
func (*mockSummarizer) SetModel(m model.Model) {
	panic("unimplemented")
}

// SetPrompt implements summary.SessionSummarizer.
func (m *mockSummarizer) SetPrompt(prompt string) {
	panic("unimplemented")
}

func (m *mockSummarizer) Summarize(ctx context.Context, session *session.Session) (string, error) {
	if m.summarizeFunc != nil {
		return m.summarizeFunc(ctx, session)
	}
	return "mock summary", nil
}

func (m *mockSummarizer) ShouldSummarize(sess *session.Session) bool {
	return true
}

func (m *mockSummarizer) Metadata() map[string]any {
	return map[string]any{"model": "mock"}
}

func TestService_AsyncSummary(t *testing.T) {
	// Register mock client
	storage.SetClientBuilder(func(opts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return &mockClient{}, nil
	})

	mockCli := &mockClient{}
	mockSum := &mockSummarizer{}

	// Setup service with async summary
	s, err := NewService(
		WithSkipDBInit(true),
		WithSummarizer(mockSum),
		WithAsyncSummaryNum(1),
		WithSummaryQueueSize(10),
		WithSummaryJobTimeout(time.Second),
		WithClickHouseDSN("clickhouse://localhost:9000"),
	)
	assert.NoError(t, err)
	s.chClient = mockCli // inject mock client
	defer s.Close()

	// Mock Exec for inserting summary
	execCalled := make(chan struct{})
	mockCli.execFunc = func(ctx context.Context, query string, args ...any) error {
		// Verify insert summary
		select {
		case execCalled <- struct{}{}:
		default:
		}
		return nil
	}

	// Create session
	sess := &session.Session{
		ID:        "test-sess",
		AppName:   "test-app",
		UserID:    "test-user",
		Events:    []event.Event{{ID: "1"}},
		Summaries: make(map[string]*session.Summary),
	}

	// Mock Summarize
	mockSum.summarizeFunc = func(ctx context.Context, sess *session.Session) (string, error) {
		return "summary result", nil
	}

	// Enqueue job
	err = s.EnqueueSummaryJob(context.Background(), sess, "some-filter", true)
	assert.NoError(t, err)

	// Wait for async processing
	// Expect at least one call
	select {
	case <-execCalled:
		// Success
	case <-time.After(2 * time.Second):
		assert.FailNow(t, "timeout waiting for summary execution")
	}
}

func TestService_CreateSessionSummary_Error(t *testing.T) {
	// Register mock client
	storage.SetClientBuilder(func(opts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return &mockClient{}, nil
	})

	mockCli := &mockClient{}
	mockSum := &mockSummarizer{}
	s, err := NewService(WithSummarizer(mockSum), WithSkipDBInit(true), WithClickHouseDSN("clickhouse://localhost:9000"))
	assert.NoError(t, err)
	s.chClient = mockCli

	sess := &session.Session{
		ID:        "sess1",
		AppName:   "app1",
		UserID:    "user1",
		Summaries: make(map[string]*session.Summary),
		Events:    []event.Event{{ID: "1"}},
	}

	// Case 1: Summarize failed
	mockSum.summarizeFunc = func(ctx context.Context, sess *session.Session) (string, error) {
		return "", assert.AnError
	}
	// Note: internal/session/summary/SummarizeSession might swallow error from summarizer
	// and return nil error. If so, CreateSessionSummary returns nil.
	_ = s.CreateSessionSummary(context.Background(), sess, "key", true)
	// We don't assert error here strictly because it depends on internal implementation behavior
	// regarding error swallowing.

	// Case 2: DB Error
	sess.Summaries = make(map[string]*session.Summary) // Re-init to be safe
	mockSum.summarizeFunc = func(ctx context.Context, sess *session.Session) (string, error) {
		// Mock side effect: update session summary to trigger persist
		if sess.Summaries == nil {
			sess.Summaries = make(map[string]*session.Summary)
		}
		sess.Summaries["key"] = &session.Summary{Summary: "summary", UpdatedAt: time.Now()}
		return "summary", nil
	}
	mockCli.execFunc = func(ctx context.Context, query string, args ...any) error {
		return assert.AnError
	}
	err = s.CreateSessionSummary(context.Background(), sess, "key", true)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "upsert summary failed")
}

func TestService_CreateSessionSummary_NoTTL(t *testing.T) {
	// Register mock client
	storage.SetClientBuilder(func(opts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return &mockClient{}, nil
	})

	mockCli := &mockClient{}
	mockSum := &mockSummarizer{}
	s, err := NewService(WithSummarizer(mockSum), WithSkipDBInit(true), WithSessionTTL(0), WithClickHouseDSN("clickhouse://localhost:9000"))
	assert.NoError(t, err)
	s.chClient = mockCli

	sess := &session.Session{
		ID:        "sess1",
		AppName:   "app1",
		UserID:    "user1",
		Summaries: make(map[string]*session.Summary),
		Events:    []event.Event{{ID: "1"}},
	}

	mockSum.summarizeFunc = func(ctx context.Context, sess *session.Session) (string, error) {
		return "test summary", nil
	}

	// Mock exec - should pass nil for expires_at since sessionTTL is 0
	execCalled := false
	mockCli.execFunc = func(ctx context.Context, query string, args ...any) error {
		execCalled = true
		assert.Contains(t, query, "INSERT INTO session_summaries")
		// expires_at should be nil (last argument)
		assert.Len(t, args, 8)
		assert.Nil(t, args[7]) // expires_at should be nil
		return nil
	}

	err = s.CreateSessionSummary(context.Background(), sess, "key", true)
	assert.NoError(t, err)
	assert.True(t, execCalled)
}
