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
