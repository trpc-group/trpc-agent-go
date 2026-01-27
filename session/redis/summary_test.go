//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

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
func (f *fakeSummarizer) SetPrompt(prompt string)  {}
func (f *fakeSummarizer) SetModel(m model.Model)   {}
func (f *fakeSummarizer) Metadata() map[string]any { return map[string]any{} }

func TestRedisService_GetSessionSummaryText_LocalPreferred(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	s, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer s.Close()

	sess := &session.Session{ID: "sid", AppName: "a", UserID: "u", Summaries: map[string]*session.Summary{
		"":   {Summary: "full", UpdatedAt: time.Now()},
		"b1": {Summary: "branch", UpdatedAt: time.Now()},
	}}
	text, ok := s.GetSessionSummaryText(context.Background(), sess)
	require.True(t, ok)
	require.Equal(t, "full", text)
}

func TestRedisService_CreateSessionSummary_NoSummarizer_NoOp(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	s, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer s.Close()

	sess := &session.Session{ID: "s1", AppName: "a", UserID: "u"}
	require.NoError(t, s.CreateSessionSummary(context.Background(), sess, "b1", false))
}

func TestRedisService_CreateSessionSummary_NoUpdate_SkipPersist(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	s, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer s.Close()

	s.opts.summarizer = &fakeSummarizer{allow: false, out: "sum"}
	sess := &session.Session{ID: "s1", AppName: "a", UserID: "u", Events: []event.Event{}}
	require.NoError(t, s.CreateSessionSummary(context.Background(), sess, "b1", false))
}

func TestRedisService_GetSessionSummaryText_RedisFallback(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	s, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer s.Close()

	// Prepare Redis summaries hash manually.
	key := session.Key{AppName: "appx", UserID: "ux", SessionID: "sid"}
	sumMap := map[string]*session.Summary{
		"":   {Summary: "full-from-redis", UpdatedAt: time.Now().UTC()},
		"k1": {Summary: "branch-from-redis", UpdatedAt: time.Now().UTC()},
	}
	payload, err := json.Marshal(sumMap)
	require.NoError(t, err)

	client := buildRedisClient(t, redisURL)
	err = client.HSet(context.Background(), getExpectedSessionSummaryKey(key), key.SessionID, string(payload)).Err()
	require.NoError(t, err)

	// Local session without summaries should fall back to Redis.
	sess := &session.Session{ID: key.SessionID, AppName: key.AppName, UserID: key.UserID}
	text, ok := s.GetSessionSummaryText(context.Background(), sess)
	require.True(t, ok)
	require.Equal(t, "full-from-redis", text)
}

func TestRedisService_CreateSessionSummary_PersistToRedis(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Enable a short TTL to also cover Expire on summary hash.
	s, err := NewService(WithRedisClientURL(redisURL), WithSessionTTL(5*time.Second))
	require.NoError(t, err)
	defer s.Close()

	// Create a session and append one valid event to make delta non-empty.
	key := session.Key{AppName: "app", UserID: "u", SessionID: "sid"}
	sess, err := s.CreateSession(context.Background(), key, session.StateMap{})
	require.NoError(t, err)

	e := event.New("inv", "author")
	e.Timestamp = time.Now()
	e.Response = &model.Response{Choices: []model.Choice{{
		Message: model.Message{Role: model.RoleUser, Content: "hello"},
	}}}
	require.NoError(t, s.AppendEvent(context.Background(), sess, e))

	// Enable summarizer to produce a summary and trigger persist via Lua.
	s.opts.summarizer = &fakeSummarizer{allow: true, out: "sum-text"}
	require.NoError(t, s.CreateSessionSummary(context.Background(), sess, "", false))

	// Verify Redis stored the map with key "".
	client := buildRedisClient(t, redisURL)
	raw, err := client.HGet(context.Background(), getExpectedSessionSummaryKey(key), key.SessionID).Bytes()
	require.NoError(t, err)
	var m map[string]*session.Summary
	require.NoError(t, json.Unmarshal(raw, &m))
	sum, ok := m[""]
	require.True(t, ok)
	require.Equal(t, "sum-text", sum.Summary)

	// Verify TTL is set on the summary hash.
	ttl := client.TTL(context.Background(), getExpectedSessionSummaryKey(key))
	require.NoError(t, ttl.Err())
	require.True(t, ttl.Val() > 0)
}

func TestRedisService_CreateSessionSummary_UpdateAndPersist_WithFetchedSession(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	s, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer s.Close()

	// Create a session and append a valid event.
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sid-thin"}
	sess, err := s.CreateSession(context.Background(), key, session.StateMap{})
	require.NoError(t, err)

	e := event.New("inv", "author")
	e.Timestamp = time.Now()
	e.Response = &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hello"}}}}
	require.NoError(t, s.AppendEvent(context.Background(), sess, e))

	// Enable summarizer.
	s.opts.summarizer = &fakeSummarizer{allow: true, out: "thin-sum"}

	// Call CreateSessionSummary with a session that includes events (fetch first),
	// aligning with the contract: summarization uses the passed-in session content.
	sessWithEvents, err := s.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, sessWithEvents)
	err = s.CreateSessionSummary(context.Background(), sessWithEvents, "", false)
	require.NoError(t, err)

	// Verify Redis has the summary under full-session key.
	client := buildRedisClient(t, redisURL)
	raw, err := client.HGet(context.Background(), getExpectedSessionSummaryKey(key), key.SessionID).Bytes()
	require.NoError(t, err)
	var m map[string]*session.Summary
	require.NoError(t, json.Unmarshal(raw, &m))
	sum, ok := m[""]
	require.True(t, ok)
	require.Equal(t, "thin-sum", sum.Summary)
}

func TestRedisService_CreateSessionSummary_SetIfNewer_NoOverride(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	s, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer s.Close()

	// Pre-populate Redis with a map whose UpdatedAt is in the future, so it is
	// newer than whatever we are about to write. The Lua script should keep it.
	key := session.Key{AppName: "app2", UserID: "u2", SessionID: "sid2"}
	future := time.Now().Add(1 * time.Hour).UTC()
	keep := map[string]*session.Summary{
		"": {Summary: "keep-me", UpdatedAt: future},
	}
	payload, err := json.Marshal(keep)
	require.NoError(t, err)

	client := buildRedisClient(t, redisURL)
	require.NoError(t, client.HSet(
		context.Background(), getExpectedSessionSummaryKey(key), key.SessionID, string(payload),
	).Err())

	// Create a session and append one event.
	sess, err := s.CreateSession(context.Background(), key, session.StateMap{})
	require.NoError(t, err)
	e := event.New("inv2", "author2")
	e.Timestamp = time.Now()
	e.Response = &model.Response{Choices: []model.Choice{{
		Message: model.Message{Role: model.RoleUser, Content: "hi"},
	}}}
	require.NoError(t, s.AppendEvent(context.Background(), sess, e))

	// Summarizer returns a different text with current time. Since stored is
	// newer, Lua should not override it.
	s.opts.summarizer = &fakeSummarizer{allow: true, out: "newer-candidate"}
	require.NoError(t, s.CreateSessionSummary(context.Background(), sess, "", false))

	// Read back and ensure value is unchanged.
	raw, err := client.HGet(context.Background(), getExpectedSessionSummaryKey(key), key.SessionID).Bytes()
	require.NoError(t, err)
	var got map[string]*session.Summary
	require.NoError(t, json.Unmarshal(raw, &got))
	sum, ok := got[""]
	require.True(t, ok)
	require.Equal(t, "keep-me", sum.Summary)
	require.True(t, sum.UpdatedAt.Equal(future))
}

func TestRedisService_EnqueueSummaryJob_AsyncEnabled(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Create service with async summary enabled
	s, err := NewService(
		WithRedisClientURL(redisURL),
		WithAsyncSummaryNum(2),
		WithSummaryQueueSize(10),
		WithSummarizer(&fakeSummarizer{allow: true, out: "async-summary"}),
	)
	require.NoError(t, err)
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

	// Verify summary was created in Redis
	client := buildRedisClient(t, redisURL)
	raw, err := client.HGet(context.Background(), getExpectedSessionSummaryKey(key), key.SessionID).Bytes()
	require.NoError(t, err)
	var m map[string]*session.Summary
	require.NoError(t, json.Unmarshal(raw, &m))
	sum, ok := m[""]
	require.True(t, ok)
	require.Equal(t, "async-summary", sum.Summary)
}

func TestRedisService_EnqueueSummaryJob_AsyncDisabled_FallbackToSync(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Create service with async summary disabled and async persist disabled.
	s, err := NewService(
		WithRedisClientURL(redisURL),
		WithEnableAsyncPersist(false),
		WithSummarizer(&fakeSummarizer{allow: true, out: "sync-summary"}),
	)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, s.Close())
	}()

	// Create a session first.
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sid"}
	sess, err := s.CreateSession(context.Background(), key, session.StateMap{})
	require.NoError(t, err)

	// Append an event to make delta non-empty.
	e := event.New("inv", "author")
	e.Timestamp = time.Now()
	e.Response = &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hello"}}}}
	require.NoError(t, s.AppendEvent(context.Background(), sess, e))

	// Enqueue summary job (should fall back to sync).
	err = s.EnqueueSummaryJob(context.Background(), sess, "", false)
	require.NoError(t, err)

	// Verify summary was created immediately in Redis (sync processing).
	client := buildRedisClient(t, redisURL)
	raw, err := client.HGet(context.Background(), getExpectedSessionSummaryKey(key), key.SessionID).Bytes()
	require.NoError(t, err)
	var m map[string]*session.Summary
	require.NoError(t, json.Unmarshal(raw, &m))
	sum, ok := m[""]
	require.True(t, ok)
	require.Equal(t, "sync-summary", sum.Summary)
}

func TestRedisService_EnqueueSummaryJob_NoSummarizer_NoOp(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Create service with async summary enabled but no summarizer
	s, err := NewService(
		WithRedisClientURL(redisURL),
		WithAsyncSummaryNum(2),
		WithSummaryQueueSize(10),
		// No summarizer set
	)
	require.NoError(t, err)
	defer s.Close()

	// Create a session first
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sid"}
	sess, err := s.CreateSession(context.Background(), key, session.StateMap{})
	require.NoError(t, err)

	// Enqueue summary job should return immediately
	err = s.EnqueueSummaryJob(context.Background(), sess, "", false)
	require.NoError(t, err)

	// Verify no summary was created in Redis
	client := buildRedisClient(t, redisURL)
	exists, err := client.HExists(context.Background(), getExpectedSessionSummaryKey(key), key.SessionID).Result()
	require.NoError(t, err)
	require.False(t, exists)
}

func TestRedisService_EnqueueSummaryJob_InvalidSession_Error(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	s, err := NewService(
		WithRedisClientURL(redisURL),
		WithSummarizer(&fakeSummarizer{allow: true, out: "test-summary"}),
	)
	require.NoError(t, err)
	defer s.Close()

	// Test with nil session
	err = s.EnqueueSummaryJob(context.Background(), nil, "", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "session is nil")

	// Test with invalid session key
	invalidSess := &session.Session{ID: "", AppName: "app", UserID: "user"}
	err = s.EnqueueSummaryJob(context.Background(), invalidSess, "", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "check session key failed")
}

func TestRedisService_EnqueueSummaryJob_QueueFull_FallbackToSync(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Create service with very small queue size.
	s, err := NewService(
		WithRedisClientURL(redisURL),
		WithAsyncSummaryNum(1),
		WithSummaryQueueSize(1), // Very small queue.
		WithSummarizer(&fakeSummarizer{allow: true, out: "fallback-summary"}),
	)
	require.NoError(t, err)
	defer s.Close()

	// Create a session first.
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sid"}
	sess, err := s.CreateSession(context.Background(), key, session.StateMap{})
	require.NoError(t, err)

	// Append events with different filterKeys to trigger cascade (not single filterKey
	// optimization). Version must be CurrentVersion for Filter() to use FilterKey.
	e1 := event.New("inv1", "author")
	e1.Timestamp = time.Now()
	e1.FilterKey = "user-messages"
	e1.Version = event.CurrentVersion
	e1.Response = &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hello"}}}}
	require.NoError(t, s.AppendEvent(context.Background(), sess, e1))

	e2 := event.New("inv2", "author")
	e2.Timestamp = time.Now()
	e2.FilterKey = "other-key"
	e2.Version = event.CurrentVersion
	e2.Response = &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "world"}}}}
	require.NoError(t, s.AppendEvent(context.Background(), sess, e2))

	// Fill up the queue by sending multiple jobs
	// Since queue size is 1, sending 2 jobs should fill it
	for i := 0; i < 2; i++ {
		err = s.EnqueueSummaryJob(context.Background(), sess, fmt.Sprintf("blocking-%d", i), false)
		require.NoError(t, err)
	}

	// Now try to enqueue another job - should fall back to sync
	err = s.EnqueueSummaryJob(context.Background(), sess, "user-messages", false)
	require.NoError(t, err)

	// Verify both branch summary and full summary were created immediately in Redis (sync fallback with cascade)
	client := buildRedisClient(t, redisURL)
	raw, err := client.HGet(context.Background(), getExpectedSessionSummaryKey(key), key.SessionID).Bytes()
	require.NoError(t, err)
	var m map[string]*session.Summary
	require.NoError(t, json.Unmarshal(raw, &m))

	// Check branch summary
	sum, ok := m["user-messages"]
	require.True(t, ok)
	require.Equal(t, "fallback-summary", sum.Summary)

	// Check full summary (should be created by cascade)
	sum, ok = m[""]
	require.True(t, ok)
	require.Equal(t, "fallback-summary", sum.Summary)
}

func TestRedisService_EnqueueSummaryJob_ConcurrentJobs(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Create service with async summary enabled
	s, err := NewService(
		WithRedisClientURL(redisURL),
		WithAsyncSummaryNum(3),
		WithSummaryQueueSize(100),
		WithSummarizer(&fakeSummarizer{allow: true, out: "concurrent-summary"}),
	)
	require.NoError(t, err)
	defer s.Close()

	// Create multiple sessions
	keys := []session.Key{
		{AppName: "app", UserID: "user1", SessionID: "sid1"},
		{AppName: "app", UserID: "user2", SessionID: "sid2"},
		{AppName: "app", UserID: "user3", SessionID: "sid3"},
	}

	// Create sessions and append events
	for i, key := range keys {
		sess, err := s.CreateSession(context.Background(), key, session.StateMap{})
		require.NoError(t, err)

		e := event.New("inv", "author")
		e.Timestamp = time.Now()
		e.Response = &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: fmt.Sprintf("hello%d", i)}}}}
		require.NoError(t, s.AppendEvent(context.Background(), sess, e))

		// Enqueue summary job
		err = s.EnqueueSummaryJob(context.Background(), sess, "", false)
		require.NoError(t, err)
	}

	// Wait for async processing
	time.Sleep(200 * time.Millisecond)

	// Verify all summaries were created
	client := buildRedisClient(t, redisURL)
	for _, key := range keys {
		raw, err := client.HGet(context.Background(), getExpectedSessionSummaryKey(key), key.SessionID).Bytes()
		require.NoError(t, err)
		var m map[string]*session.Summary
		require.NoError(t, json.Unmarshal(raw, &m))
		sum, ok := m[""]
		require.True(t, ok)
		require.Equal(t, "concurrent-summary", sum.Summary)
	}
}

// fakeBlockingSummarizer blocks until ctx is done, then returns an error.
type fakeBlockingSummarizer struct{}

func (f *fakeBlockingSummarizer) ShouldSummarize(sess *session.Session) bool { return true }
func (f *fakeBlockingSummarizer) Summarize(ctx context.Context, sess *session.Session) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}
func (f *fakeBlockingSummarizer) SetPrompt(prompt string)  {}
func (f *fakeBlockingSummarizer) SetModel(m model.Model)   {}
func (f *fakeBlockingSummarizer) Metadata() map[string]any { return map[string]any{} }

func TestRedisService_SummaryJobTimeout_CancelsSummarizer(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	s, err := NewService(
		WithRedisClientURL(redisURL),
		WithSummarizer(&fakeBlockingSummarizer{}),
		WithSummaryJobTimeout(50*time.Millisecond),
	)
	require.NoError(t, err)
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

	// Verify no summary was created in Redis.
	client := buildRedisClient(t, redisURL)
	exists, err := client.HExists(context.Background(), getExpectedSessionSummaryKey(key), key.SessionID).Result()
	require.NoError(t, err)
	require.False(t, exists)
}

func TestRedisService_EnqueueSummaryJob_ChannelClosed_PanicRecovery(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Create service with async summary enabled.
	s, err := NewService(
		WithRedisClientURL(redisURL),
		WithAsyncSummaryNum(1),
		WithSummaryQueueSize(1),
		WithSummarizer(&fakeSummarizer{allow: true, out: "panic-recovery-summary"}),
	)
	require.NoError(t, err)

	// Create a session first.
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sid"}
	sess, err := s.CreateSession(context.Background(), key, session.StateMap{})
	require.NoError(t, err)

	// Append an event to make delta non-empty.
	e := event.New("inv", "author")
	e.Timestamp = time.Now()
	e.Response = &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hello"}}}}
	require.NoError(t, s.AppendEvent(context.Background(), sess, e))

	// Get the latest session from storage to ensure we have the latest events.
	sessFromStorage, err := s.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, sessFromStorage)

	// Stop only the async worker (not the entire service) to simulate channel closure.
	// This allows sync fallback to still write to Redis.
	s.asyncWorker.Stop()

	// Enqueue summary job should fall back to sync processing since worker is stopped.
	err = s.EnqueueSummaryJob(context.Background(), sessFromStorage, "", false)
	require.NoError(t, err)

	// Verify summary was created through sync fallback.
	client := buildRedisClient(t, redisURL)
	exists, err := client.HExists(context.Background(), getExpectedSessionSummaryKey(key), key.SessionID).Result()
	require.NoError(t, err)
	require.True(t, exists)

	// Verify the summary content.
	bytes, err := client.HGet(context.Background(), getExpectedSessionSummaryKey(key), key.SessionID).Bytes()
	require.NoError(t, err)
	require.NotEmpty(t, bytes)

	var summaries map[string]*session.Summary
	err = json.Unmarshal(bytes, &summaries)
	require.NoError(t, err)
	require.NotNil(t, summaries)

	sum, ok := summaries[""]
	require.True(t, ok)
	require.Equal(t, "panic-recovery-summary", sum.Summary)

	// Now close the service properly.
	s.Close()
}

func TestRedisService_EnqueueSummaryJob_ChannelClosed_AllChannelsClosed(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Create service with multiple async workers.
	s, err := NewService(
		WithRedisClientURL(redisURL),
		WithAsyncSummaryNum(3),
		WithSummaryQueueSize(1),
		WithSummarizer(&fakeSummarizer{allow: true, out: "all-channels-closed-summary"}),
	)
	require.NoError(t, err)

	// Create a session first.
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sid"}
	sess, err := s.CreateSession(context.Background(), key, session.StateMap{})
	require.NoError(t, err)

	// Append an event to make delta non-empty.
	e := event.New("inv", "author")
	e.Timestamp = time.Now()
	e.Response = &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hello"}}}}
	require.NoError(t, s.AppendEvent(context.Background(), sess, e))

	// Get the latest session from storage to ensure we have the latest events.
	sessFromStorage, err := s.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, sessFromStorage)

	// Stop only the async worker (not the entire service) to simulate channel closure.
	// This allows sync fallback to still write to Redis.
	s.asyncWorker.Stop()

	// Enqueue summary job should fall back to sync processing since worker is stopped.
	err = s.EnqueueSummaryJob(context.Background(), sessFromStorage, "", false)
	require.NoError(t, err)

	// Verify summary was created through sync fallback.
	client := buildRedisClient(t, redisURL)
	exists, err := client.HExists(context.Background(), getExpectedSessionSummaryKey(key), key.SessionID).Result()
	require.NoError(t, err)
	require.True(t, exists)

	// Verify the summary content.
	bytes, err := client.HGet(context.Background(), getExpectedSessionSummaryKey(key), key.SessionID).Bytes()
	require.NoError(t, err)
	require.NotEmpty(t, bytes)

	var summaries map[string]*session.Summary
	err = json.Unmarshal(bytes, &summaries)
	require.NoError(t, err)
	require.NotNil(t, summaries)

	// Check full summary (should be created by sync fallback).
	sum, ok := summaries[""]
	require.True(t, ok)
	require.Equal(t, "all-channels-closed-summary", sum.Summary)

	// Now close the service properly.
	s.Close()
}

func TestRedisService_EnqueueSummaryJob_NoAsyncWorkers_FallbackToSyncWithCascade(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Create service with async workers.
	s, err := NewService(
		WithRedisClientURL(redisURL),
		WithAsyncSummaryNum(1),
		WithSummarizer(&fakeSummarizer{allow: true, out: "sync-summary"}),
	)
	require.NoError(t, err)
	defer s.Close()

	// Create a session first.
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sid"}
	sess, err := s.CreateSession(context.Background(), key, session.StateMap{})
	require.NoError(t, err)

	// Append events with different filterKeys to trigger cascade (not single filterKey
	// optimization). Version must be CurrentVersion for Filter() to use FilterKey.
	e1 := event.New("inv1", "author")
	e1.Timestamp = time.Now()
	e1.FilterKey = "tool-usage"
	e1.Version = event.CurrentVersion
	e1.Response = &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hello"}}}}
	require.NoError(t, s.AppendEvent(context.Background(), sess, e1))

	e2 := event.New("inv2", "author")
	e2.Timestamp = time.Now()
	e2.FilterKey = "other-key"
	e2.Version = event.CurrentVersion
	e2.Response = &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "world"}}}}
	require.NoError(t, s.AppendEvent(context.Background(), sess, e2))

	// Get the latest session from storage to ensure we have the latest events.
	sessFromStorage, err := s.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, sessFromStorage)

	// Stop only the async worker (not the entire service) to simulate no async workers scenario.
	// This allows sync fallback to still write to Redis.
	s.asyncWorker.Stop()

	// EnqueueSummaryJob should fall back to sync processing with cascade.
	err = s.EnqueueSummaryJob(context.Background(), sessFromStorage, "tool-usage", false)
	require.NoError(t, err)

	// Verify both branch summary and full summary were created.
	client := buildRedisClient(t, redisURL)
	raw, err := client.HGet(context.Background(), getExpectedSessionSummaryKey(key), key.SessionID).Bytes()
	require.NoError(t, err)
	var m map[string]*session.Summary
	require.NoError(t, json.Unmarshal(raw, &m))

	// Check branch summary.
	sum, ok := m["tool-usage"]
	require.True(t, ok)
	require.Equal(t, "sync-summary", sum.Summary)

	// Check full summary (should be created by cascade).
	sum, ok = m[""]
	require.True(t, ok)
	require.Equal(t, "sync-summary", sum.Summary)
}

func TestRedisService_EnqueueSummaryJob_SingleFilterKey_PersistsBothKeys(t *testing.T) {
	// This test verifies that when all events match a single filterKey,
	// the optimization path still persists BOTH the filterKey summary AND
	// the full-session summary (filter_key="") to Redis.
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Create service with async workers.
	s, err := NewService(
		WithRedisClientURL(redisURL),
		WithAsyncSummaryNum(1),
		WithSummarizer(&fakeSummarizer{allow: true, out: "single-key-summary"}),
	)
	require.NoError(t, err)
	defer s.Close()

	// Create a session first.
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sid-single"}
	sess, err := s.CreateSession(context.Background(), key, session.StateMap{})
	require.NoError(t, err)

	// Append events that ALL match the same filterKey (triggers single filterKey
	// optimization). Version must be CurrentVersion for Filter() to use FilterKey.
	e1 := event.New("inv1", "author")
	e1.Timestamp = time.Now()
	e1.FilterKey = "tool-usage"
	e1.Version = event.CurrentVersion
	e1.Response = &model.Response{Choices: []model.Choice{{
		Message: model.Message{Role: model.RoleUser, Content: "hello"},
	}}}
	require.NoError(t, s.AppendEvent(context.Background(), sess, e1))

	e2 := event.New("inv2", "author")
	e2.Timestamp = time.Now()
	e2.FilterKey = "tool-usage" // Same filterKey as e1.
	e2.Version = event.CurrentVersion
	e2.Response = &model.Response{Choices: []model.Choice{{
		Message: model.Message{Role: model.RoleUser, Content: "world"},
	}}}
	require.NoError(t, s.AppendEvent(context.Background(), sess, e2))

	// Get the latest session from storage to ensure we have the latest events.
	sessFromStorage, err := s.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, sessFromStorage)

	// Stop only the async worker to simulate sync fallback.
	s.asyncWorker.Stop()

	// EnqueueSummaryJob should trigger single filterKey optimization
	// and persist BOTH keys.
	err = s.EnqueueSummaryJob(context.Background(), sessFromStorage, "tool-usage", false)
	require.NoError(t, err)

	// Verify both filterKey summary and full-session summary were created.
	client := buildRedisClient(t, redisURL)
	raw, err := client.HGet(context.Background(), getExpectedSessionSummaryKey(key), key.SessionID).Bytes()
	require.NoError(t, err)
	var m map[string]*session.Summary
	require.NoError(t, json.Unmarshal(raw, &m))

	// Check filterKey summary.
	sum, ok := m["tool-usage"]
	require.True(t, ok, "filterKey summary should exist")
	require.Equal(t, "single-key-summary", sum.Summary)

	// Check full-session summary (filter_key=""). This is the critical part!
	sum, ok = m[""]
	require.True(t, ok, "full-session summary (filter_key='') should exist in Redis")
	require.Equal(t, "single-key-summary", sum.Summary)
}

func TestRedisService_GetSessionSummaryText_NilSession(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	s, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer s.Close()

	text, ok := s.GetSessionSummaryText(context.Background(), nil)
	require.False(t, ok)
	require.Empty(t, text)
}

func TestRedisService_CreateSessionSummary_NilSession(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	s, err := NewService(WithRedisClientURL(redisURL), WithSummarizer(&fakeSummarizer{allow: true, out: "sum"}))
	require.NoError(t, err)
	defer s.Close()

	err = s.CreateSessionSummary(context.Background(), nil, "", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "session is nil")
}

func TestRedisService_CreateSessionSummary_InvalidKey(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	s, err := NewService(WithRedisClientURL(redisURL), WithSummarizer(&fakeSummarizer{allow: true, out: "sum"}))
	require.NoError(t, err)
	defer s.Close()

	sess := &session.Session{ID: "", AppName: "app", UserID: "user"}
	err = s.CreateSessionSummary(context.Background(), sess, "", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "check session key failed")
}

type fakeErrorSummarizer struct{}

func (f *fakeErrorSummarizer) ShouldSummarize(sess *session.Session) bool { return true }
func (f *fakeErrorSummarizer) Summarize(ctx context.Context, sess *session.Session) (string, error) {
	return "", fmt.Errorf("summarizer error")
}
func (f *fakeErrorSummarizer) SetPrompt(prompt string)  {}
func (f *fakeErrorSummarizer) SetModel(m model.Model)   {}
func (f *fakeErrorSummarizer) Metadata() map[string]any { return map[string]any{} }

func TestCreateSessionSummary_WithSessionTTL(t *testing.T) {
	tests := []struct {
		name         string
		sessionTTL   time.Duration
		shouldSetTTL bool
	}{
		{
			name:         "with session TTL",
			sessionTTL:   30 * time.Second,
			shouldSetTTL: true,
		},
		{
			name:         "without session TTL",
			sessionTTL:   0,
			shouldSetTTL: false,
		},
		{
			name:         "with negative session TTL",
			sessionTTL:   -1 * time.Second,
			shouldSetTTL: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redisURL, cleanup := setupTestRedis(t)
			defer cleanup()

			s, err := NewService(
				WithRedisClientURL(redisURL),
				WithSessionTTL(tt.sessionTTL),
			)
			require.NoError(t, err)
			defer s.Close()

			// Create a session and append one valid event to make delta non-empty.
			key := session.Key{AppName: "app", UserID: "u", SessionID: "sid"}
			sess, err := s.CreateSession(context.Background(), key, session.StateMap{})
			require.NoError(t, err)

			e := event.New("inv", "author")
			e.Timestamp = time.Now()
			e.Response = &model.Response{Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleUser, Content: "hello"},
			}}}
			require.NoError(t, s.AppendEvent(context.Background(), sess, e))

			// Enable summarizer to produce a summary and trigger TTL logic.
			s.opts.summarizer = &fakeSummarizer{allow: true, out: "sum-text"}
			require.NoError(t, s.CreateSessionSummary(context.Background(), sess, "", false))

			// Verify TTL is set on the summary hash if sessionTTL > 0.
			client := buildRedisClient(t, redisURL)
			sumKey := getExpectedSessionSummaryKey(key)
			ttl := client.TTL(context.Background(), sumKey)

			if tt.shouldSetTTL {
				require.NoError(t, ttl.Err())
				assert.True(t, ttl.Val() > 0, "TTL should be set when sessionTTL > 0")
			} else {
				// When sessionTTL is 0 or negative, TTL should not be set
				// (miniredis returns -1 for no TTL)
				require.NoError(t, ttl.Err())
				assert.True(t, ttl.Val() <= 0, "TTL should not be set when sessionTTL <= 0")
			}
		})
	}
}

func TestRedisService_CreateAndGetSessionSummaryWithKeyPrefix(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	const testPrefix = "test-prefix"

	s, err := NewService(
		WithRedisClientURL(redisURL),
		WithKeyPrefix(testPrefix),
		WithSummarizer(&fakeSummarizer{allow: true, out: "prefixed-summary"}),
	)
	require.NoError(t, err)
	defer s.Close()

	key := session.Key{AppName: "app", UserID: "user", SessionID: "sid"}
	sess, err := s.CreateSession(context.Background(), key, session.StateMap{})
	require.NoError(t, err)

	// Append an event to make delta non-empty.
	e := event.New("inv", "author")
	e.Timestamp = time.Now()
	e.Response = &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hello"}}}}
	require.NoError(t, s.AppendEvent(context.Background(), sess, e))

	// Create summary via public API.
	err = s.CreateSessionSummary(context.Background(), sess, "", false)
	require.NoError(t, err)

	// Retrieve the summary via public API to ensure the prefix-aware Get path works.
	got, ok := s.GetSessionSummaryText(context.Background(), sess)
	require.True(t, ok)
	require.Equal(t, "prefixed-summary", got)

	// Verify Redis keys: only prefixed key should exist, unprefixed should not.
	client := buildRedisClient(t, redisURL)
	prefixedKey := getExpectedSessionSummaryKeyWithPrefix(testPrefix, key)
	unprefixedKey := getExpectedSessionSummaryKey(key)

	prefixedExists, err := client.HExists(context.Background(), prefixedKey, key.SessionID).Result()
	require.NoError(t, err)
	require.True(t, prefixedExists, "prefixed summary key should exist")

	unprefixedExists, err := client.HExists(context.Background(), unprefixedKey, key.SessionID).Result()
	require.NoError(t, err)
	require.False(t, unprefixedExists, "unprefixed summary key should not exist")
}
