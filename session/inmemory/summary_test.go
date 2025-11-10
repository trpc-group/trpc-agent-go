//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
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
	"testing"
	"time"

	"github.com/spaolacci/murmur3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type fakeSummarizer struct {
	allow bool
	out   string
}

func (f *fakeSummarizer) ShouldSummarize(sess *session.Session) bool { return f.allow }
func (f *fakeSummarizer) Summarize(ctx context.Context, sess *session.Session) (string, error) {
	return f.out, nil
}
func (f *fakeSummarizer) Metadata() map[string]any { return map[string]any{} }

func TestMemoryService_GetSessionSummaryText_LocalPreferred(t *testing.T) {
	s := NewSessionService()
	sess := &session.Session{Summaries: map[string]*session.Summary{
		"":   {Summary: "full", UpdatedAt: time.Now()},
		"b1": {Summary: "branch", UpdatedAt: time.Now()},
	}}
	text, ok := s.GetSessionSummaryText(context.Background(), sess)
	require.True(t, ok)
	require.Equal(t, "full", text)
}

func TestMemoryService_CreateSessionSummary_NoSummarizer_NoOp(t *testing.T) {
	s := NewSessionService()
	sess := &session.Session{ID: "s1", AppName: "a", UserID: "u"}
	require.NoError(t, s.CreateSessionSummary(context.Background(), sess, "b1", false))
}

func TestMemoryService_CreateSessionSummary_NoUpdate_SkipPersist(t *testing.T) {
	s := NewSessionService()
	// Create stored session first because CreateSessionSummary looks it up under lock.
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sid"}
	stored, err := s.CreateSession(context.Background(), key, session.StateMap{})
	require.NoError(t, err)
	require.NotNil(t, stored)

	// summarizer allow=false leads to updated=false; should return without persisting.
	s.opts.summarizer = &fakeSummarizer{allow: false, out: "sum"}
	require.NoError(t, s.CreateSessionSummary(context.Background(), &session.Session{
		ID: key.SessionID, AppName: key.AppName, UserID: key.UserID,
	}, "b1", false))

	got, err := s.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, got)
	_, exists := got.Summaries["b1"]
	require.False(t, exists)
}

func TestMemoryService_CreateSessionSummary_UpdateAndPersist(t *testing.T) {
	s := NewSessionService()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sid2"}
	sess, err := s.CreateSession(context.Background(), key, session.StateMap{})
	require.NoError(t, err)

	// Append one valid event so delta is non-empty.
	e := event.New("inv", "author")
	e.Timestamp = time.Now()
	e.Response = &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hello"}}}}
	require.NoError(t, s.AppendEvent(context.Background(), sess, e))

	// Enable summarizer and create summary under filterKey "" (full-session).
	s.opts.summarizer = &fakeSummarizer{allow: true, out: "summary-text"}
	// Fetch authoritative session with events and pass it explicitly.
	sessWithEvents, err := s.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, sessWithEvents)
	require.NoError(t, s.CreateSessionSummary(context.Background(), sessWithEvents, "", false))

	// Verify stored summary exists and GetSessionSummaryText returns it.
	got, err := s.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, got)
	sum, ok := got.Summaries[""]
	require.True(t, ok)
	require.Equal(t, "summary-text", sum.Summary)

	text, ok := s.GetSessionSummaryText(context.Background(), got)
	require.True(t, ok)
	require.Equal(t, "summary-text", text)
}

func TestMemoryService_EnqueueSummaryJob_AsyncEnabled(t *testing.T) {
	// Create service with async summary enabled
	s := NewSessionService(
		WithAsyncSummaryNum(2),
		WithSummaryQueueSize(10),
		WithSummarizer(&fakeSummarizer{allow: true, out: "async-summary"}),
	)
	defer s.Close()

	// Create a session first
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sid"}
	sess, err := s.CreateSession(context.Background(), key, session.StateMap{})
	require.NoError(t, err)

	// Append an event to make delta non-empty
	e := event.New("inv", "author")
	e.Timestamp = time.Now()
	e.Response = &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hello"}}}}
	require.NoError(t, s.AppendEvent(context.Background(), sess, e))

	// Enqueue summary job
	err = s.EnqueueSummaryJob(context.Background(), sess, "", false)
	require.NoError(t, err)

	// Wait a bit for async processing
	time.Sleep(100 * time.Millisecond)

	// Verify summary was created
	got, err := s.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, got)
	sum, ok := got.Summaries[""]
	require.True(t, ok)
	require.Equal(t, "async-summary", sum.Summary)
}

func TestMemoryService_EnqueueSummaryJob_AsyncEnabled_Default(t *testing.T) {
	// Create service with async summary enabled by default
	s := NewSessionService(
		WithSummarizer(&fakeSummarizer{allow: true, out: "async-summary"}),
	)

	// Create a session first
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sid"}
	sess, err := s.CreateSession(context.Background(), key, session.StateMap{})
	require.NoError(t, err)

	// Append an event to make delta non-empty
	e := event.New("inv", "author")
	e.Timestamp = time.Now()
	e.Response = &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hello"}}}}
	require.NoError(t, s.AppendEvent(context.Background(), sess, e))

	// Enqueue summary job (should use async processing)
	err = s.EnqueueSummaryJob(context.Background(), sess, "", false)
	require.NoError(t, err)

	// Wait a bit for async processing
	time.Sleep(100 * time.Millisecond)

	// Verify summary was created (async processing)
	got, err := s.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, got)
	sum, ok := got.Summaries[""]
	require.True(t, ok)
	require.Equal(t, "async-summary", sum.Summary)
}

func TestMemoryService_EnqueueSummaryJob_NoSummarizer_NoOp(t *testing.T) {
	// Create service with async summary enabled but no summarizer
	s := NewSessionService(
		WithAsyncSummaryNum(2),
		WithSummaryQueueSize(10),
		// No summarizer set
	)
	defer s.Close()

	// Create a session first
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sid"}
	sess, err := s.CreateSession(context.Background(), key, session.StateMap{})
	require.NoError(t, err)

	// Enqueue summary job should return immediately
	err = s.EnqueueSummaryJob(context.Background(), sess, "", false)
	require.NoError(t, err)

	// Verify no summary was created
	got, err := s.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, got)
	_, ok := got.Summaries[""]
	require.False(t, ok)
}

func TestMemoryService_EnqueueSummaryJob_InvalidSession_Error(t *testing.T) {
	s := NewSessionService(
		WithSummarizer(&fakeSummarizer{allow: true, out: "test-summary"}),
	)
	defer s.Close()

	// Test with nil session
	err := s.EnqueueSummaryJob(context.Background(), nil, "", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil session")

	// Test with invalid session key
	invalidSess := &session.Session{ID: "", AppName: "app", UserID: "user"}
	err = s.EnqueueSummaryJob(context.Background(), invalidSess, "", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "check session key failed")
}

func TestMemoryService_EnqueueSummaryJob_QueueFull_FallbackToSync(t *testing.T) {
	// Create service with very small queue size
	s := NewSessionService(
		WithAsyncSummaryNum(1),
		WithSummaryQueueSize(1), // Very small queue
		WithSummarizer(&fakeSummarizer{allow: true, out: "fallback-summary"}),
	)
	defer s.Close()

	// Create a session first
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sid"}
	sess, err := s.CreateSession(context.Background(), key, session.StateMap{})
	require.NoError(t, err)

	// Append an event to make delta non-empty
	e := event.New("inv", "author")
	e.Timestamp = time.Now()
	e.Response = &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hello"}}}}
	require.NoError(t, s.AppendEvent(context.Background(), sess, e))

	// Fill up the target worker queue with a blocking job
	blockingJob := &summaryJob{
		sessionKey: key,
		filterKey:  "blocking",
		force:      true,
		session:    sess,
	}

	// Choose the same worker index as tryEnqueueJob would select.
	keyStr := key.AppName + ":" + key.UserID + ":" + key.SessionID
	idx := int(murmur3.Sum32([]byte(keyStr))) % len(s.summaryJobChans)

	// Send a job to fill that queue (this will block the worker)
	select {
	case s.summaryJobChans[idx] <- blockingJob:
		// Queue is now full
	default:
		// Queue was already full
	}

	// Now try to enqueue another job - should fall back to sync
	err = s.EnqueueSummaryJob(context.Background(), sess, "branch", false)
	require.NoError(t, err)
	time.Sleep(time.Millisecond * 100)

	// Verify summary was created immediately (sync fallback)
	got, err := s.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, got)
	sum, ok := got.Summaries["branch"]
	require.True(t, ok)
	require.Equal(t, "fallback-summary", sum.Summary)
}

// fakeBlockingSummarizer blocks until ctx is done, then returns an error.
type fakeBlockingSummarizer struct{}

func (f *fakeBlockingSummarizer) ShouldSummarize(sess *session.Session) bool { return true }
func (f *fakeBlockingSummarizer) Summarize(ctx context.Context, sess *session.Session) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}
func (f *fakeBlockingSummarizer) Metadata() map[string]any { return map[string]any{} }

func TestMemoryService_SummaryJobTimeout_CancelsSummarizer(t *testing.T) {
	s := NewSessionService(
		WithSummarizer(&fakeBlockingSummarizer{}),
		WithSummaryJobTimeout(50*time.Millisecond),
	)
	defer s.Close()

	// Create a session and append one event so delta is non-empty.
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sid-timeout"}
	sess, err := s.CreateSession(context.Background(), key, session.StateMap{})
	require.NoError(t, err)

	e := event.New("inv", "author")
	e.Timestamp = time.Now()
	e.Response = &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hello"}}}}
	require.NoError(t, s.AppendEvent(context.Background(), sess, e))

	// Enqueue job; summarizer will block until timeout; worker should cancel and not persist.
	err = s.EnqueueSummaryJob(context.Background(), sess, "", false)
	require.NoError(t, err)

	// Wait longer than timeout to ensure worker had time to cancel.
	time.Sleep(150 * time.Millisecond)

	// Verify no summary was created.
	got, err := s.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, got)
	_, ok := got.Summaries[""]
	require.False(t, ok)
}

func TestMemoryService_EnqueueSummaryJob_ChannelClosed_PanicRecovery(t *testing.T) {
	// Create service with async summary enabled
	s := NewSessionService(
		WithAsyncSummaryNum(1),
		WithSummaryQueueSize(1),
		WithSummarizer(&fakeSummarizer{allow: true, out: "panic-recovery-summary"}),
	)
	defer s.Close()

	// Create a session first
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sid"}
	sess, err := s.CreateSession(context.Background(), key, session.StateMap{})
	require.NoError(t, err)

	// Append an event to make delta non-empty
	e := event.New("inv", "author")
	e.Timestamp = time.Now()
	e.Response = &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hello"}}}}
	require.NoError(t, s.AppendEvent(context.Background(), sess, e))

	// Close the service to simulate channel closure
	// This will cause a panic when trying to send to the closed channel
	s.Close()

	// Enqueue summary job should handle the panic and fall back to sync processing
	err = s.EnqueueSummaryJob(context.Background(), sess, "panic-test", false)
	require.NoError(t, err)

	// Verify summary was created through sync fallback
	got, err := s.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, got)
	sum, ok := got.Summaries["panic-test"]
	require.True(t, ok)
	require.Equal(t, "panic-recovery-summary", sum.Summary)
}

func TestMemoryService_EnqueueSummaryJob_ChannelClosed_AllChannelsClosed(t *testing.T) {
	// Create service with multiple async workers
	s := NewSessionService(
		WithAsyncSummaryNum(3),
		WithSummaryQueueSize(1),
		WithSummarizer(&fakeSummarizer{allow: true, out: "all-channels-closed-summary"}),
	)
	defer s.Close()

	// Create a session first
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sid"}
	sess, err := s.CreateSession(context.Background(), key, session.StateMap{})
	require.NoError(t, err)

	// Append an event to make delta non-empty
	e := event.New("inv", "author")
	e.Timestamp = time.Now()
	e.Response = &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hello"}}}}
	require.NoError(t, s.AppendEvent(context.Background(), sess, e))

	// Close the service to simulate service shutdown scenario
	s.Close()

	// Enqueue summary job should handle the panic and fall back to sync processing
	err = s.EnqueueSummaryJob(context.Background(), sess, "all-closed-test", false)
	require.NoError(t, err)

	// Verify summary was created through sync fallback
	got, err := s.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, got)
	sum, ok := got.Summaries["all-closed-test"]
	require.True(t, ok)
	require.Equal(t, "all-channels-closed-summary", sum.Summary)
}

func TestMemoryService_GetSessionSummaryText_NilSession(t *testing.T) {
	s := NewSessionService()
	text, ok := s.GetSessionSummaryText(context.Background(), nil)
	require.False(t, ok)
	require.Empty(t, text)
}

func TestMemoryService_GetSessionSummaryText_EmptySummaries(t *testing.T) {
	s := NewSessionService()
	sess := &session.Session{ID: "s1", Summaries: map[string]*session.Summary{}}
	text, ok := s.GetSessionSummaryText(context.Background(), sess)
	require.False(t, ok)
	require.Empty(t, text)
}

func TestMemoryService_GetSessionSummaryText_NilSummaries(t *testing.T) {
	s := NewSessionService()
	sess := &session.Session{ID: "s1", Summaries: nil}
	text, ok := s.GetSessionSummaryText(context.Background(), sess)
	require.False(t, ok)
	require.Empty(t, text)
}

func TestMemoryService_GetSessionSummaryText_EmptySummaryText(t *testing.T) {
	s := NewSessionService()
	sess := &session.Session{
		ID: "s1",
		Summaries: map[string]*session.Summary{
			session.SummaryFilterKeyAllContents: {Summary: "", UpdatedAt: time.Now()},
		},
	}
	text, ok := s.GetSessionSummaryText(context.Background(), sess)
	require.False(t, ok)
	require.Empty(t, text)
}

func TestMemoryService_GetSessionSummaryText_NilSummaryEntry(t *testing.T) {
	s := NewSessionService()
	sess := &session.Session{
		ID: "s1",
		Summaries: map[string]*session.Summary{
			session.SummaryFilterKeyAllContents: nil,
		},
	}
	text, ok := s.GetSessionSummaryText(context.Background(), sess)
	require.False(t, ok)
	require.Empty(t, text)
}

func TestMemoryService_GetSessionSummaryText_BranchSummaryFallback(t *testing.T) {
	s := NewSessionService()
	sess := &session.Session{
		ID: "s1",
		Summaries: map[string]*session.Summary{
			"branch1": {Summary: "branch-summary", UpdatedAt: time.Now()},
		},
	}
	text, ok := s.GetSessionSummaryText(context.Background(), sess)
	require.True(t, ok)
	require.Equal(t, "branch-summary", text)
}

func TestMemoryService_CreateSessionSummary_NilSession(t *testing.T) {
	s := NewSessionService(WithSummarizer(&fakeSummarizer{allow: true, out: "sum"}))
	err := s.CreateSessionSummary(context.Background(), nil, "", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil session")
}

func TestMemoryService_CreateSessionSummary_InvalidKey(t *testing.T) {
	s := NewSessionService(WithSummarizer(&fakeSummarizer{allow: true, out: "sum"}))
	sess := &session.Session{ID: "", AppName: "app", UserID: "user"}
	err := s.CreateSessionSummary(context.Background(), sess, "", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "check session key failed")
}

func TestMemoryService_CreateSessionSummary_SessionNotFound(t *testing.T) {
	s := NewSessionService(WithSummarizer(&fakeSummarizer{allow: true, out: "sum"}))

	// Create a session in memory to trigger the branch summary logic.
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sid"}
	_, err := s.CreateSession(context.Background(), key, session.StateMap{})
	require.NoError(t, err)

	// Add an event.
	e := event.New("inv", "author")
	e.Timestamp = time.Now()
	e.Response = &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hello"}}}}
	sess := &session.Session{ID: key.SessionID, AppName: key.AppName, UserID: key.UserID}
	require.NoError(t, s.AppendEvent(context.Background(), sess, e))

	// Try to create summary for non-existent session.
	nonExistentSess := &session.Session{ID: "non-existent", AppName: "app", UserID: "user"}
	err = s.CreateSessionSummary(context.Background(), nonExistentSess, "b1", true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "session not found")
}

func TestMemoryService_ProcessSummaryJob_Panic(t *testing.T) {
	s := NewSessionService(
		WithSummarizer(&fakeSummarizer{allow: true, out: "test"}),
	)
	defer s.Close()

	key := session.Key{AppName: "app", UserID: "user", SessionID: "sid"}

	// Process a job with no stored session - should trigger error but not panic.
	job := &summaryJob{
		sessionKey: key,
		filterKey:  "",
		force:      false,
		session:    &session.Session{ID: key.SessionID, AppName: key.AppName, UserID: key.UserID},
	}

	// This should not panic, just log error.
	require.NotPanics(t, func() {
		s.processSummaryJob(job)
	})
}

func TestMemoryService_TryEnqueueJob_ContextCancelled(t *testing.T) {
	service := NewSessionService(
		WithSummarizer(&fakeSummarizer{allow: true, out: "test"}),
		WithAsyncSummaryNum(1),
		WithSummaryQueueSize(1),
	)
	defer service.Close()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u", SessionID: "s"}
	sess, err := service.CreateSession(ctx, key, session.StateMap{})
	require.NoError(t, err)

	// Fill the queue first
	job1 := &summaryJob{
		sessionKey: key,
		filterKey:  "",
		force:      false,
		session:    sess,
	}

	// First job should succeed
	assert.True(t, service.tryEnqueueJob(ctx, job1))

	// Create a cancelled context
	cancelledCtx, cancel := context.WithCancel(ctx)
	cancel()

	// Second job with cancelled context should fail
	job2 := &summaryJob{
		sessionKey: session.Key{AppName: "app", UserID: "u2", SessionID: "s2"},
		filterKey:  "",
		force:      false,
		session:    sess,
	}
	assert.False(t, service.tryEnqueueJob(cancelledCtx, job2))
}

func TestMemoryService_AppendEvent_Errors(t *testing.T) {
	service := NewSessionService()
	defer service.Close()

	ctx := context.Background()

	// Test with invalid session key
	sess := &session.Session{
		ID:      "",
		AppName: "",
		UserID:  "",
	}
	err := service.AppendEvent(ctx, sess, &event.Event{})
	require.Error(t, err)

	// Test with non-existent app
	sess = &session.Session{
		ID:      "s1",
		AppName: "non-existent",
		UserID:  "u1",
	}
	err = service.AppendEvent(ctx, sess, &event.Event{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "app not found")

	// Create a session first
	key := session.Key{AppName: "app", UserID: "u", SessionID: "s"}
	sess, err = service.CreateSession(ctx, key, session.StateMap{})
	require.NoError(t, err)

	// Test with non-existent user
	sess2 := &session.Session{
		ID:      "s2",
		AppName: "app",
		UserID:  "non-existent",
	}
	err = service.AppendEvent(ctx, sess2, &event.Event{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "user not found")

	// Test with non-existent session
	sess3 := &session.Session{
		ID:      "non-existent",
		AppName: "app",
		UserID:  "u",
	}
	err = service.AppendEvent(ctx, sess3, &event.Event{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session not found")
}

func TestMemoryService_ListSessions_Filtering(t *testing.T) {
	service := NewSessionService()
	defer service.Close()

	ctx := context.Background()
	userKey := session.UserKey{AppName: "app", UserID: "u"}

	// Create multiple sessions with events
	key1 := session.Key{AppName: "app", UserID: "u", SessionID: "s1"}
	sess1, err := service.CreateSession(ctx, key1, session.StateMap{})
	require.NoError(t, err)

	// Add some events
	e1 := &event.Event{
		ID:        "e1",
		Response:  &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hello"}}}},
		Timestamp: time.Now(),
	}
	require.NoError(t, service.AppendEvent(ctx, sess1, e1))

	// List sessions
	sessions, err := service.ListSessions(ctx, userKey)
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	assert.NotEmpty(t, sessions[0].Events)
}

func TestMemoryService_DeleteSession_Errors(t *testing.T) {
	service := NewSessionService()
	defer service.Close()

	ctx := context.Background()

	// Test with invalid key
	err := service.DeleteSession(ctx, session.Key{AppName: "", UserID: "u", SessionID: "s"})
	require.Error(t, err)

	// Test with non-existent app
	err = service.DeleteSession(ctx, session.Key{AppName: "non-existent", UserID: "u", SessionID: "s"})
	require.NoError(t, err) // Should not error

	// Test with non-existent user
	key := session.Key{AppName: "app", UserID: "u", SessionID: "s"}
	_, err = service.CreateSession(ctx, key, session.StateMap{})
	require.NoError(t, err)

	err = service.DeleteSession(ctx, session.Key{AppName: "app", UserID: "non-existent", SessionID: "s"})
	require.NoError(t, err) // Should not error

	// Test with non-existent session
	err = service.DeleteSession(ctx, session.Key{AppName: "app", UserID: "u", SessionID: "non-existent"})
	require.NoError(t, err) // Should not error
}

func TestMemoryService_GetOrCreateAppSessions_Concurrent(t *testing.T) {
	service := NewSessionService()
	defer service.Close()

	// Test concurrent access to getOrCreateAppSessions
	const numGoroutines = 10
	done := make(chan bool, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			app := service.getOrCreateAppSessions("concurrent-app")
			assert.NotNil(t, app)
			done <- true
		}()
	}

	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	// Verify only one app was created
	app, ok := service.getAppSessions("concurrent-app")
	assert.True(t, ok)
	assert.NotNil(t, app)
}

func TestProcessSummaryJob(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(t *testing.T, service *SessionService) *summaryJob
		expectError bool
	}{
		{
			name: "successful summary processing",
			setup: func(t *testing.T, service *SessionService) *summaryJob {
				// Create a session with events
				key := session.Key{AppName: "app", UserID: "user", SessionID: "sid"}
				sess, err := service.CreateSession(context.Background(), key, session.StateMap{})
				require.NoError(t, err)

				// Add an event to make delta non-empty
				e := event.New("inv", "author")
				e.Timestamp = time.Now()
				e.Response = &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hello"}}}}
				err = service.AppendEvent(context.Background(), sess, e)
				require.NoError(t, err)

				// Enable summarizer
				service.opts.summarizer = &fakeSummarizer{allow: true, out: "test summary"}

				return &summaryJob{
					sessionKey: key,
					filterKey:  "",
					force:      false,
					session:    sess,
				}
			},
			expectError: false,
		},
		{
			name: "summary job with branch filter",
			setup: func(t *testing.T, service *SessionService) *summaryJob {
				// Create a session with events
				key := session.Key{AppName: "app", UserID: "user", SessionID: "sid2"}
				sess, err := service.CreateSession(context.Background(), key, session.StateMap{})
				require.NoError(t, err)

				// Add an event
				e := event.New("inv", "author")
				e.Timestamp = time.Now()
				e.Response = &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hello"}}}}
				err = service.AppendEvent(context.Background(), sess, e)
				require.NoError(t, err)

				// Enable summarizer
				service.opts.summarizer = &fakeSummarizer{allow: true, out: "branch summary"}

				return &summaryJob{
					sessionKey: key,
					filterKey:  "branch1",
					force:      false,
					session:    sess,
				}
			},
			expectError: false,
		},
		{
			name: "summarizer returns false",
			setup: func(t *testing.T, service *SessionService) *summaryJob {
				// Create a session
				key := session.Key{AppName: "app", UserID: "user", SessionID: "sid3"}
				sess, err := service.CreateSession(context.Background(), key, session.StateMap{})
				require.NoError(t, err)

				// Enable summarizer that returns false
				service.opts.summarizer = &fakeSummarizer{allow: false, out: "no update"}

				return &summaryJob{
					sessionKey: key,
					filterKey:  "",
					force:      false,
					session:    sess,
				}
			},
			expectError: false,
		},
		{
			name: "summarizer returns error",
			setup: func(t *testing.T, service *SessionService) *summaryJob {
				// Create a session
				key := session.Key{AppName: "app", UserID: "user", SessionID: "sid4"}
				sess, err := service.CreateSession(context.Background(), key, session.StateMap{})
				require.NoError(t, err)

				// Enable summarizer that returns error
				service.opts.summarizer = &fakeErrorSummarizer{}

				return &summaryJob{
					sessionKey: key,
					filterKey:  "",
					force:      false,
					session:    sess,
				}
			},
			expectError: false, // Should not panic or error, just log
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			service := NewSessionService()
			defer service.Close()

			job := tt.setup(t, service)

			// This should not panic
			require.NotPanics(t, func() {
				service.processSummaryJob(job)
			})
		})
	}
}

type fakeErrorSummarizer struct{}

func (f *fakeErrorSummarizer) ShouldSummarize(sess *session.Session) bool { return true }
func (f *fakeErrorSummarizer) Summarize(ctx context.Context, sess *session.Session) (string, error) {
	return "", fmt.Errorf("summarizer error")
}
func (f *fakeErrorSummarizer) Metadata() map[string]any { return map[string]any{} }
