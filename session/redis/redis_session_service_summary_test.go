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

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// --- Summary (CreateSessionSummary / GetSessionSummaryText) tests ---

type fakeSummarizer struct {
	allow bool
	out   string
}

func (f *fakeSummarizer) ShouldSummarize(sess *session.Session) bool { return f.allow }
func (f *fakeSummarizer) Summarize(ctx context.Context, sess *session.Session) (string, error) {
	return f.out, nil
}
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
	err = client.HSet(context.Background(), getSessionSummaryKey(key), key.SessionID, string(payload)).Err()
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
	raw, err := client.HGet(context.Background(), getSessionSummaryKey(key), key.SessionID).Bytes()
	require.NoError(t, err)
	var m map[string]*session.Summary
	require.NoError(t, json.Unmarshal(raw, &m))
	sum, ok := m[""]
	require.True(t, ok)
	require.Equal(t, "sum-text", sum.Summary)

	// Verify TTL is set on the summary hash.
	ttl := client.TTL(context.Background(), getSessionSummaryKey(key))
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
	raw, err := client.HGet(context.Background(), getSessionSummaryKey(key), key.SessionID).Bytes()
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
		context.Background(), getSessionSummaryKey(key), key.SessionID, string(payload),
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
	raw, err := client.HGet(context.Background(), getSessionSummaryKey(key), key.SessionID).Bytes()
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
	raw, err := client.HGet(context.Background(), getSessionSummaryKey(key), key.SessionID).Bytes()
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

	// Create service with async summary disabled
	s, err := NewService(
		WithRedisClientURL(redisURL),
		WithSummarizer(&fakeSummarizer{allow: true, out: "sync-summary"}),
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

	// Enqueue summary job (should fall back to sync)
	err = s.EnqueueSummaryJob(context.Background(), sess, "", false)
	require.NoError(t, err)

	// Verify summary was created immediately in Redis (sync processing)
	client := buildRedisClient(t, redisURL)
	raw, err := client.HGet(context.Background(), getSessionSummaryKey(key), key.SessionID).Bytes()
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
	exists, err := client.HExists(context.Background(), getSessionSummaryKey(key), key.SessionID).Result()
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
	require.Contains(t, err.Error(), "nil session")

	// Test with invalid session key
	invalidSess := &session.Session{ID: "", AppName: "app", UserID: "user"}
	err = s.EnqueueSummaryJob(context.Background(), invalidSess, "", false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "check session key failed")
}

func TestRedisService_EnqueueSummaryJob_QueueFull_FallbackToSync(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Create service with very small queue size
	s, err := NewService(
		WithRedisClientURL(redisURL),
		WithAsyncSummaryNum(1),
		WithSummaryQueueSize(1), // Very small queue
		WithSummarizer(&fakeSummarizer{allow: true, out: "fallback-summary"}),
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

	// Fill up the queue by sending multiple jobs
	// Since queue size is 1, sending 2 jobs should fill it
	for i := 0; i < 2; i++ {
		err = s.EnqueueSummaryJob(context.Background(), sess, fmt.Sprintf("blocking-%d", i), false)
		require.NoError(t, err)
	}

	// Now try to enqueue another job - should fall back to sync
	err = s.EnqueueSummaryJob(context.Background(), sess, "", false)
	require.NoError(t, err)

	// Verify summary was created immediately in Redis (sync fallback)
	client := buildRedisClient(t, redisURL)
	raw, err := client.HGet(context.Background(), getSessionSummaryKey(key), key.SessionID).Bytes()
	require.NoError(t, err)
	var m map[string]*session.Summary
	require.NoError(t, json.Unmarshal(raw, &m))
	sum, ok := m[""]
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
		raw, err := client.HGet(context.Background(), getSessionSummaryKey(key), key.SessionID).Bytes()
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
	exists, err := client.HExists(context.Background(), getSessionSummaryKey(key), key.SessionID).Result()
	require.NoError(t, err)
	require.False(t, exists)
}

func TestRedisService_EnqueueSummaryJob_ChannelClosed_PanicRecovery(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Create service with async summary enabled
	s, err := NewService(
		WithRedisClientURL(redisURL),
		WithAsyncSummaryNum(1),
		WithSummaryQueueSize(1),
		WithSummarizer(&fakeSummarizer{allow: true, out: "panic-recovery-summary"}),
	)
	require.NoError(t, err)

	// Create a session first
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sid"}
	sess, err := s.CreateSession(context.Background(), key, session.StateMap{})
	require.NoError(t, err)

	// Append an event to make delta non-empty
	e := event.New("inv", "author")
	e.Timestamp = time.Now()
	e.Response = &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hello"}}}}
	require.NoError(t, s.AppendEvent(context.Background(), sess, e))

	// Manually close the channel to simulate channel closure
	// This will cause a panic when trying to send to the closed channel
	close(s.summaryJobChans[0])

	// Enqueue summary job should handle the panic and fall back to sync processing
	err = s.EnqueueSummaryJob(context.Background(), sess, "panic-test", false)
	require.NoError(t, err)

	// Verify summary was created through sync fallback
	client := buildRedisClient(t, redisURL)
	exists, err := client.HExists(context.Background(), getSessionSummaryKey(key), key.SessionID).Result()
	require.NoError(t, err)
	require.True(t, exists)

	// Verify the summary content
	bytes, err := client.HGet(context.Background(), getSessionSummaryKey(key), key.SessionID).Bytes()
	require.NoError(t, err)
	require.NotEmpty(t, bytes)

	var summaries map[string]*session.Summary
	err = json.Unmarshal(bytes, &summaries)
	require.NoError(t, err)
	require.NotNil(t, summaries)

	sum, ok := summaries["panic-test"]
	require.True(t, ok)
	require.Equal(t, "panic-recovery-summary", sum.Summary)

	// Don't call s.Close() since we manually closed the channel
	// This simulates a scenario where the service is being shut down
}

func TestRedisService_EnqueueSummaryJob_ChannelClosed_AllChannelsClosed(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Create service with multiple async workers
	s, err := NewService(
		WithRedisClientURL(redisURL),
		WithAsyncSummaryNum(3),
		WithSummaryQueueSize(1),
		WithSummarizer(&fakeSummarizer{allow: true, out: "all-channels-closed-summary"}),
	)
	require.NoError(t, err)

	// Create a session first
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sid"}
	sess, err := s.CreateSession(context.Background(), key, session.StateMap{})
	require.NoError(t, err)

	// Append an event to make delta non-empty
	e := event.New("inv", "author")
	e.Timestamp = time.Now()
	e.Response = &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hello"}}}}
	require.NoError(t, s.AppendEvent(context.Background(), sess, e))

	// Close all channels to simulate service shutdown scenario
	for _, ch := range s.summaryJobChans {
		close(ch)
	}

	// Enqueue summary job should handle the panic and fall back to sync processing
	err = s.EnqueueSummaryJob(context.Background(), sess, "all-closed-test", false)
	require.NoError(t, err)

	// Verify summary was created through sync fallback
	client := buildRedisClient(t, redisURL)
	exists, err := client.HExists(context.Background(), getSessionSummaryKey(key), key.SessionID).Result()
	require.NoError(t, err)
	require.True(t, exists)

	// Verify the summary content
	bytes, err := client.HGet(context.Background(), getSessionSummaryKey(key), key.SessionID).Bytes()
	require.NoError(t, err)
	require.NotEmpty(t, bytes)

	var summaries map[string]*session.Summary
	err = json.Unmarshal(bytes, &summaries)
	require.NoError(t, err)
	require.NotNil(t, summaries)

	sum, ok := summaries["all-closed-test"]
	require.True(t, ok)
	require.Equal(t, "all-channels-closed-summary", sum.Summary)

	// Don't call s.Close() since we manually closed the channels
	// This simulates a scenario where the service is being shut down
}
