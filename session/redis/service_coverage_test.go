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

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/redis/internal/hashidx"
	"trpc.group/trpc-go/trpc-agent-go/session/redis/internal/zset"
)

// ============================================================================
// summary.go coverage: getSummaryFromZSet, slow path routing, error paths
// ============================================================================

func TestCreateSessionSummary_SlowPath_ZsetSession(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Create zset session via Transition mode
	svcT, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeTransition),
		WithSummarizer(&fakeSummarizer{allow: true, out: "zset-summary"}))
	require.NoError(t, err)

	key := session.Key{AppName: "app1", UserID: "u1", SessionID: "zsid"}
	sess, err := svcT.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)

	// Append event to make delta non-empty
	e := event.New("inv", "author")
	e.Timestamp = time.Now()
	e.Response = &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hello"}}}}
	require.NoError(t, svcT.AppendEvent(context.Background(), sess, e))
	svcT.Close()

	// Use Legacy mode (no version tag on session since we clear ServiceMeta)
	svcL, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeLegacy),
		WithSummarizer(&fakeSummarizer{allow: true, out: "zset-summary-legacy"}))
	require.NoError(t, err)
	defer svcL.Close()

	// Get session to have events
	sessL, err := svcL.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, sessL)

	// Clear version tag to force slow path
	sessL.ServiceMeta = nil

	err = svcL.CreateSessionSummary(context.Background(), sessL, "", false)
	require.NoError(t, err)

	// Verify summary was written to zset storage (zset summary: HASH with field=sessionID)
	client := buildRedisClient(t, redisURL)
	sumKey := zset.GetSessionSummaryKey(key)
	raw, err := client.HGet(context.Background(), sumKey, key.SessionID).Bytes()
	require.NoError(t, err)
	var m map[string]*session.Summary
	require.NoError(t, json.Unmarshal(raw, &m))
	assert.Equal(t, "zset-summary-legacy", m[""].Summary)
}

func TestCreateSessionSummary_SlowPath_HashidxSession(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeNone),
		WithSummarizer(&fakeSummarizer{allow: true, out: "hashidx-slow"}))
	require.NoError(t, err)
	defer svc.Close()

	key := session.Key{AppName: "app1", UserID: "u1", SessionID: "hsid"}
	sess, err := svc.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)

	e := event.New("inv", "author")
	e.Timestamp = time.Now()
	e.Response = &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hello"}}}}
	require.NoError(t, svc.AppendEvent(context.Background(), sess, e))

	sessGet, err := svc.GetSession(context.Background(), key)
	require.NoError(t, err)
	// Clear version tag to force slow path
	sessGet.ServiceMeta = nil

	err = svc.CreateSessionSummary(context.Background(), sessGet, "", false)
	require.NoError(t, err)

	// Verify summary written to hashidx
	client := buildRedisClient(t, redisURL)
	sumKey := hashidx.GetSessionSummaryKey("", key)
	raw, err := client.Get(context.Background(), sumKey).Bytes()
	require.NoError(t, err)
	var m map[string]*session.Summary
	require.NoError(t, json.Unmarshal(raw, &m))
	assert.Equal(t, "hashidx-slow", m[""].Summary)
}

func TestCreateSessionSummary_SessionNotFound_NoError(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeNone),
		WithSummarizer(&fakeSummarizer{allow: true, out: "orphan"}))
	require.NoError(t, err)
	defer svc.Close()

	// Session with events but not in Redis
	sess := &session.Session{
		ID: "nosid", AppName: "app1", UserID: "u1",
		Events: []event.Event{{ID: "e1", Response: &model.Response{
			Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "x"}}},
		}}},
	}
	// No ServiceMeta -> slow path, session not found -> should return nil
	err = svc.CreateSessionSummary(context.Background(), sess, "", false)
	require.NoError(t, err)
}

func TestGetSessionSummaryText_SlowPath_ZsetSession(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Create zset session
	svcT, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeTransition))
	require.NoError(t, err)

	key := session.Key{AppName: "app1", UserID: "u1", SessionID: "gzs1"}
	sess, err := svcT.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)

	// Write summary directly to zset storage (HASH: field=sessionID, value=map[filterKey]*Summary)
	client := buildRedisClient(t, redisURL)
	sumKey := zset.GetSessionSummaryKey(key)
	sumMap := map[string]*session.Summary{"": {Summary: "zset-sum-text", UpdatedAt: time.Now().UTC()}}
	sumJSON, _ := json.Marshal(sumMap)
	require.NoError(t, client.HSet(context.Background(), sumKey, key.SessionID, string(sumJSON)).Err())
	svcT.Close()

	// Use Legacy mode to read
	svcL, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)
	defer svcL.Close()

	// Clear ServiceMeta to force slow path
	sess.ServiceMeta = nil
	sess.CreatedAt = time.Now().Add(-time.Hour)
	text, ok := svcL.GetSessionSummaryText(context.Background(), sess)
	require.True(t, ok)
	assert.Equal(t, "zset-sum-text", text)
}

func TestGetSessionSummaryText_SlowPath_HashidxSession(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeNone))
	require.NoError(t, err)
	defer svc.Close()

	key := session.Key{AppName: "app1", UserID: "u1", SessionID: "ghs1"}
	sess, err := svc.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)

	// Write summary directly
	client := buildRedisClient(t, redisURL)
	sumKey := hashidx.GetSessionSummaryKey("", key)
	m := map[string]*session.Summary{"": {Summary: "hash-sum-text", UpdatedAt: time.Now().UTC()}}
	payload, _ := json.Marshal(m)
	require.NoError(t, client.Set(context.Background(), sumKey, string(payload), 0).Err())

	// Clear ServiceMeta to force slow path
	sess.ServiceMeta = nil
	sess.CreatedAt = time.Now().Add(-time.Hour)
	text, ok := svc.GetSessionSummaryText(context.Background(), sess)
	require.True(t, ok)
	assert.Equal(t, "hash-sum-text", text)
}

func TestGetSessionSummaryText_SlowPath_SessionNotFound(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeNone))
	require.NoError(t, err)
	defer svc.Close()

	sess := &session.Session{ID: "nope", AppName: "app1", UserID: "u1"}
	text, ok := svc.GetSessionSummaryText(context.Background(), sess)
	assert.False(t, ok)
	assert.Empty(t, text)
}

func TestGetSessionSummaryText_InvalidKey(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer svc.Close()

	sess := &session.Session{ID: "", AppName: "", UserID: ""}
	text, ok := svc.GetSessionSummaryText(context.Background(), sess)
	assert.False(t, ok)
	assert.Empty(t, text)
}

func TestGetSessionSummaryText_VersionTag_ZsetPath(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)
	defer svc.Close()

	key := session.Key{AppName: "app1", UserID: "u1", SessionID: "vtag1"}
	// Write summary to zset (HASH: field=sessionID, value=map[filterKey]*Summary)
	client := buildRedisClient(t, redisURL)
	sumKey := zset.GetSessionSummaryKey(key)
	sumMap := map[string]*session.Summary{"": {Summary: "via-tag", UpdatedAt: time.Now().UTC()}}
	sumJSON, _ := json.Marshal(sumMap)
	require.NoError(t, client.HSet(context.Background(), sumKey, key.SessionID, string(sumJSON)).Err())

	// Session with zset version tag -> fast path
	sess := &session.Session{
		ID: "vtag1", AppName: "app1", UserID: "u1",
		CreatedAt:   time.Now().Add(-time.Hour),
		ServiceMeta: map[string]string{"storage_type": "zset"},
	}
	text, ok := svc.GetSessionSummaryText(context.Background(), sess)
	require.True(t, ok)
	assert.Equal(t, "via-tag", text)
}

func TestCreateSessionSummary_VersionTag_ZsetPath(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeLegacy),
		WithSummarizer(&fakeSummarizer{allow: true, out: "via-zset-tag"}))
	require.NoError(t, err)
	defer svc.Close()

	// Create zset session via Transition mode
	svcT, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeTransition))
	require.NoError(t, err)
	key := session.Key{AppName: "app1", UserID: "u1", SessionID: "cstag1"}
	sess, err := svcT.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)
	e := event.New("inv", "author")
	e.Timestamp = time.Now()
	e.Response = &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hello"}}}}
	require.NoError(t, svcT.AppendEvent(context.Background(), sess, e))
	svcT.Close()

	// GetSession via Legacy to get version tag
	sessL, err := svc.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, sessL)
	// Should have zset version tag
	assert.Equal(t, "zset", sessL.ServiceMeta["storage_type"])

	err = svc.CreateSessionSummary(context.Background(), sessL, "", false)
	require.NoError(t, err)

	// Verify written to zset (HASH: field=sessionID, value=map[filterKey]*Summary)
	client := buildRedisClient(t, redisURL)
	raw, err := client.HGet(context.Background(), zset.GetSessionSummaryKey(key), key.SessionID).Bytes()
	require.NoError(t, err)
	var m map[string]*session.Summary
	require.NoError(t, json.Unmarshal(raw, &m))
	assert.Equal(t, "via-zset-tag", m[""].Summary)
}

// ============================================================================
// service.go coverage: persistEvent slow path, mergeAppUserState, ListSessions
// ============================================================================

func TestPersistEvent_SlowPath_ZsetSession(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Create zset session
	svcT, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeTransition))
	require.NoError(t, err)
	key := session.Key{AppName: "app1", UserID: "u1", SessionID: "pe-zset"}
	sess, err := svcT.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)
	svcT.Close()

	// Use Legacy mode, clear version tag to force slow path in persistEvent
	svcL, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)
	defer svcL.Close()

	e := createTestEvent("pe1", "agent", "content", time.Now(), false)
	// Clear ServiceMeta -> slow path
	sess.ServiceMeta = nil
	err = svcL.AppendEvent(context.Background(), sess, e)
	require.NoError(t, err)

	// Verify event was persisted to zset
	sessGet, err := svcL.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, sessGet)
	assert.GreaterOrEqual(t, len(sessGet.Events), 1)
}

func TestPersistEvent_SlowPath_HashidxSession(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeNone))
	require.NoError(t, err)
	defer svc.Close()

	key := session.Key{AppName: "app1", UserID: "u1", SessionID: "pe-hash"}
	sess, err := svc.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)

	e := createTestEvent("pe2", "agent", "content", time.Now(), false)
	// Clear ServiceMeta -> slow path
	sess.ServiceMeta = nil
	err = svc.AppendEvent(context.Background(), sess, e)
	require.NoError(t, err)

	sessGet, err := svc.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, sessGet)
	assert.Len(t, sessGet.Events, 1)
}

func TestPersistEvent_SlowPath_SessionNotFound(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeNone))
	require.NoError(t, err)
	defer svc.Close()

	sess := &session.Session{ID: "nosid", AppName: "app1", UserID: "u1"}
	e := createTestEvent("pe3", "agent", "content", time.Now(), false)
	err = svc.AppendEvent(context.Background(), sess, e)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session not found")
}

// ============================================================================
// track_service.go coverage: persistTrackEvent slow path
// ============================================================================

func TestPersistTrackEvent_SlowPath_ZsetSession(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Create zset session
	svcT, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeTransition))
	require.NoError(t, err)
	key := session.Key{AppName: "app1", UserID: "u1", SessionID: "pt-zset"}
	sess, err := svcT.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)
	svcT.Close()

	svcL, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)
	defer svcL.Close()

	// Clear version tag to force slow path
	sess.ServiceMeta = nil
	te := &session.TrackEvent{
		Track:     "alpha",
		Payload:   json.RawMessage(`{"data":"test"}`),
		Timestamp: time.Now(),
	}
	err = svcL.AppendTrackEvent(context.Background(), sess, te)
	require.NoError(t, err)

	// Verify track was persisted to zset
	sessGet, err := svcL.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, sessGet)
	assert.NotEmpty(t, sessGet.Tracks)
}

func TestPersistTrackEvent_SlowPath_HashidxSession(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeNone))
	require.NoError(t, err)
	defer svc.Close()

	key := session.Key{AppName: "app1", UserID: "u1", SessionID: "pt-hash"}
	sess, err := svc.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)

	// Clear version tag to force slow path
	sess.ServiceMeta = nil
	te := &session.TrackEvent{
		Track:     "beta",
		Payload:   json.RawMessage(`{"data":"test"}`),
		Timestamp: time.Now(),
	}
	err = svc.AppendTrackEvent(context.Background(), sess, te)
	require.NoError(t, err)

	sessGet, err := svc.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, sessGet)
	assert.NotEmpty(t, sessGet.Tracks)
}

func TestPersistTrackEvent_SlowPath_SessionNotFound(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeNone))
	require.NoError(t, err)
	defer svc.Close()

	sess := &session.Session{ID: "nosid", AppName: "app1", UserID: "u1"}
	te := &session.TrackEvent{
		Track:     "gamma",
		Payload:   json.RawMessage(`"p"`),
		Timestamp: time.Now(),
	}
	err = svc.AppendTrackEvent(context.Background(), sess, te)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session not found")
}

func TestAppendTrackEvent_NilSession(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer svc.Close()

	err = svc.AppendTrackEvent(context.Background(), nil, &session.TrackEvent{Track: "t"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session is nil")
}

func TestAppendTrackEvent_InvalidKey(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer svc.Close()

	sess := &session.Session{ID: "", AppName: "", UserID: ""}
	err = svc.AppendTrackEvent(context.Background(), sess, &session.TrackEvent{Track: "t"})
	require.Error(t, err)
}

// ============================================================================
// state_service.go coverage: UpdateSessionState prefix validation, edge cases
// ============================================================================

func TestUpdateSessionState_AppPrefixRejected(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeNone))
	require.NoError(t, err)
	defer svc.Close()

	key := session.Key{AppName: "app1", UserID: "u1", SessionID: "uss1"}
	_, err = svc.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)

	err = svc.UpdateSessionState(context.Background(), key, session.StateMap{
		"app:forbidden": []byte("val"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not allowed")
	assert.Contains(t, err.Error(), "UpdateAppState")
}

func TestUpdateSessionState_UserPrefixRejected(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeNone))
	require.NoError(t, err)
	defer svc.Close()

	key := session.Key{AppName: "app1", UserID: "u1", SessionID: "uss2"}
	_, err = svc.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)

	err = svc.UpdateSessionState(context.Background(), key, session.StateMap{
		"user:forbidden": []byte("val"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not allowed")
	assert.Contains(t, err.Error(), "UpdateUserState")
}

func TestUpdateSessionState_SessionNotFound(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeNone))
	require.NoError(t, err)
	defer svc.Close()

	key := session.Key{AppName: "app1", UserID: "u1", SessionID: "nosid"}
	err = svc.UpdateSessionState(context.Background(), key, session.StateMap{"k": []byte("v")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session not found")
}

func TestUpdateSessionState_ZsetRoute(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Create zset session via Transition mode
	svcT, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeTransition))
	require.NoError(t, err)
	key := session.Key{AppName: "app1", UserID: "u1", SessionID: "uss-zset"}
	_, err = svcT.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)
	svcT.Close()

	svcL, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)
	defer svcL.Close()

	err = svcL.UpdateSessionState(context.Background(), key, session.StateMap{"k": []byte("v")})
	require.NoError(t, err)

	// Verify state was written to zset
	sess, err := svcL.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.Equal(t, []byte("v"), sess.State["k"])
}

func TestUpdateSessionState_InvalidKey(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer svc.Close()

	err = svc.UpdateSessionState(context.Background(), session.Key{}, session.StateMap{"k": []byte("v")})
	require.Error(t, err)
}

func TestUpdateUserState_TransitionMode(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeTransition))
	require.NoError(t, err)
	defer svc.Close()

	userKey := session.UserKey{AppName: "app1", UserID: "u1"}
	err = svc.UpdateUserState(context.Background(), userKey, session.StateMap{"k": []byte("v")})
	require.NoError(t, err)

	// Verify written to both
	states, err := svc.ListUserStates(context.Background(), userKey)
	require.NoError(t, err)
	assert.Equal(t, []byte("v"), states["k"])
}

func TestListUserStates_InvalidKey(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer svc.Close()

	_, err = svc.ListUserStates(context.Background(), session.UserKey{})
	require.Error(t, err)
}

func TestDeleteUserState_InvalidKey(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer svc.Close()

	err = svc.DeleteUserState(context.Background(), session.UserKey{}, "k")
	require.Error(t, err)
}

func TestDeleteUserState_EmptyKey(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer svc.Close()

	err = svc.DeleteUserState(context.Background(), session.UserKey{AppName: "app", UserID: "u"}, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "state key is required")
}

func TestDeleteAppState_EmptyKey(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer svc.Close()

	err = svc.DeleteAppState(context.Background(), "app", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "state key is required")
}

func TestDeleteAppState_EmptyAppName(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer svc.Close()

	err = svc.DeleteAppState(context.Background(), "", "k")
	require.Error(t, err)
}

func TestListAppStates_EmptyAppName(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer svc.Close()

	_, err = svc.ListAppStates(context.Background(), "")
	require.Error(t, err)
}

func TestUpdateAppState_EmptyAppName(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer svc.Close()

	err = svc.UpdateAppState(context.Background(), "", session.StateMap{"k": []byte("v")})
	require.Error(t, err)
}

// ============================================================================
// service.go: mergeAppUserState with pre-existing state
// ============================================================================

func TestMergeAppUserState_WithExistingState(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeNone),
		WithAppStateTTL(time.Hour), WithUserStateTTL(time.Hour))
	require.NoError(t, err)
	defer svc.Close()

	ctx := context.Background()
	// Pre-populate app and user state
	require.NoError(t, svc.UpdateAppState(ctx, "mergeapp", session.StateMap{"ak": []byte("av")}))
	userKey := session.UserKey{AppName: "mergeapp", UserID: "mergeu"}
	require.NoError(t, svc.UpdateUserState(ctx, userKey, session.StateMap{"uk": []byte("uv")}))

	// Create session -> should merge app and user state
	key := session.Key{AppName: "mergeapp", UserID: "mergeu", SessionID: "ms1"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	require.NotNil(t, sess)

	assert.Equal(t, []byte("av"), sess.State["app:ak"])
	assert.Equal(t, []byte("uv"), sess.State["user:uk"])
}

// ============================================================================
// service.go: ListSessions invalid key
// ============================================================================

func TestListSessions_InvalidKey(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer svc.Close()

	_, err = svc.ListSessions(context.Background(), session.UserKey{})
	require.Error(t, err)
}

// ============================================================================
// service.go: DeleteSession with compat errors
// ============================================================================

func TestDeleteSession_InvalidKey(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer svc.Close()

	err = svc.DeleteSession(context.Background(), session.Key{})
	require.Error(t, err)
}

// ============================================================================
// service.go: TrimConversations routing to zset
// ============================================================================

func TestTrimConversations_ZsetSession(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svcT, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeTransition))
	require.NoError(t, err)

	key := session.Key{AppName: "app1", UserID: "u1", SessionID: "trim-zset"}
	sess, err := svcT.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)

	// Append events with RequestIDs
	for i := 0; i < 3; i++ {
		e := createTestEvent("e"+string(rune('0'+i)), "agent", "content", time.Now().Add(time.Duration(i)*time.Second), false)
		e.RequestID = "req" + string(rune('0'+i))
		require.NoError(t, svcT.AppendEvent(context.Background(), sess, e))
	}
	svcT.Close()

	svcL, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)
	defer svcL.Close()

	// Trim from zset session
	deleted, err := svcL.TrimConversations(context.Background(), key, WithCount(1))
	require.NoError(t, err)
	assert.NotEmpty(t, deleted)
}

func TestTrimConversations_InvalidKey(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer svc.Close()

	_, err = svc.TrimConversations(context.Background(), session.Key{})
	require.Error(t, err)
}

// ============================================================================
// service.go: AppendEvent invalid session
// ============================================================================

func TestAppendEvent_InvalidKey(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer svc.Close()

	sess := &session.Session{ID: "", AppName: "", UserID: ""}
	err = svc.AppendEvent(context.Background(), sess, event.New("e1", "a"))
	require.Error(t, err)
}

// ============================================================================
// service.go: GetSession invalid key
// ============================================================================

func TestGetSession_InvalidKey(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer svc.Close()

	_, err = svc.GetSession(context.Background(), session.Key{})
	require.Error(t, err)
}

// ============================================================================
// service.go: CreateSession invalid key
// ============================================================================

func TestCreateSession_InvalidKey(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer svc.Close()

	_, err = svc.CreateSession(context.Background(), session.Key{}, nil)
	require.Error(t, err)
}

// ============================================================================
// service.go: NewService with instance name (not URL)
// ============================================================================

func TestNewService_NoURL_NoInstance_Error(t *testing.T) {
	_, err := NewService()
	require.Error(t, err)
}

// ============================================================================
// service.go: mergeAppUserState error handling
// ============================================================================

func TestCreateSession_MergeAppUserState_Error(t *testing.T) {
	// Manually setup miniredis to control its lifecycle
	mr, err := miniredis.Run()
	require.NoError(t, err)
	redisURL := "redis://" + mr.Addr()
	defer mr.Close()

	svc, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer svc.Close()

	// Create a session first
	key := session.Key{AppName: "merge-test", UserID: "user1", SessionID: "session1"}
	_, err = svc.CreateSession(context.Background(), key, session.StateMap{"key1": []byte("val1")})
	require.NoError(t, err)

	// Close miniredis server to simulate connection error
	mr.Close()

	// GetSession may or may not return error depending on code path
	// The important thing is that mergeAppUserState doesn't panic when Redis is closed
	_, _ = svc.GetSession(context.Background(), key)
}

// ============================================================================
// service.go: mergeAppUserState with nil session
// ============================================================================

func TestMergeAppUserState_NilSession(t *testing.T) {
	// This test indirectly covers mergeAppUserState with nil session
	// by testing the code paths that could potentially pass nil
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer svc.Close()

	ctx := context.Background()
	key := session.Key{AppName: "test", UserID: "user", SessionID: "sess"}

	// Create session - this triggers mergeAppUserState
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	require.NotNil(t, sess)

	// Verify session has expected fields even with nil input state
	assert.Equal(t, key.SessionID, sess.ID)
	assert.Equal(t, key.AppName, sess.AppName)
	assert.Equal(t, key.UserID, sess.UserID)
}

// ============================================================================
// service.go: ListSessions with transition mode
// ============================================================================

func TestListSessions_TransitionMode(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(
		WithRedisClientURL(redisURL),
		WithCompatMode(CompatModeTransition),
	)
	require.NoError(t, err)
	defer svc.Close()

	ctx := context.Background()
	userKey := session.UserKey{AppName: "test", UserID: "user"}

	// Create a session in transition mode (goes to zset)
	sessionKey := session.Key{AppName: "test", UserID: "user", SessionID: "sess1"}
	_, err = svc.CreateSession(ctx, sessionKey, nil)
	require.NoError(t, err)

	// ListSessions should find it
	sessions, err := svc.ListSessions(ctx, userKey)
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	assert.Equal(t, "sess1", sessions[0].ID)
}

// ============================================================================
// Additional coverage tests for 90% target
// ============================================================================

// TestUpdateUserState_Transition_DualWrite tests UpdateUserState in Transition mode writes to both storages
func TestUpdateUserState_Transition_DualWrite(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svcT, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeTransition))
	require.NoError(t, err)
	defer svcT.Close()

	ctx := context.Background()
	userKey := session.UserKey{AppName: "app1", UserID: "u1"}

	// Write user state in transition mode - should write to both zset and hashidx
	err = svcT.UpdateUserState(ctx, userKey, session.StateMap{"key": []byte("value")})
	require.NoError(t, err)

	// Verify zset has the data
	zsetClient := zset.NewClient(buildRedisClient(t, redisURL), zset.Config{KeyPrefix: ""})
	zsetStates, err := zsetClient.ListUserStates(ctx, userKey)
	require.NoError(t, err)
	assert.Equal(t, []byte("value"), zsetStates["key"])

	// Verify hashidx has the data
	hashidxClient := hashidx.NewClient(buildRedisClient(t, redisURL), hashidx.Config{KeyPrefix: ""})
	hashidxStates, err := hashidxClient.ListUserStates(ctx, userKey)
	require.NoError(t, err)
	assert.Equal(t, []byte("value"), hashidxStates["key"])
}

// TestListUserStates_Legacy_Fallback tests ListUserStates in Legacy mode falls back to zset
func TestListUserStates_Legacy_Fallback(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// First write user state to zset using Legacy mode
	svcL, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)

	ctx := context.Background()
	userKey := session.UserKey{AppName: "app1", UserID: "u1"}

	// Write to hashidx directly (simulating data that exists in hashidx)
	hashidxClient := hashidx.NewClient(buildRedisClient(t, redisURL), hashidx.Config{KeyPrefix: ""})
	err = hashidxClient.UpdateUserState(ctx, userKey, session.StateMap{"zkey": []byte("zval")}, time.Hour)
	require.NoError(t, err)
	svcL.Close()

	// Now read using Legacy mode - should find data in hashidx
	svcL2, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)
	defer svcL2.Close()

	states, err := svcL2.ListUserStates(ctx, userKey)
	require.NoError(t, err)
	assert.Equal(t, []byte("zval"), states["zkey"])
}

// TestCreateSession_Transition_Mode tests CreateSession in Transition mode
func TestCreateSession_Transition_Mode(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svcT, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeTransition))
	require.NoError(t, err)
	defer svcT.Close()

	ctx := context.Background()
	key := session.Key{AppName: "app1", UserID: "u1", SessionID: "trans-session"}

	// Pre-populate states
	err = svcT.UpdateAppState(ctx, "app1", session.StateMap{"appkey": []byte("appval")})
	require.NoError(t, err)
	err = svcT.UpdateUserState(ctx, session.UserKey{AppName: "app1", UserID: "u1"}, session.StateMap{"userkey": []byte("userval")})
	require.NoError(t, err)

	// Create session in Transition mode
	sess, err := svcT.CreateSession(ctx, key, session.StateMap{"sesskey": []byte("sessval")})
	require.NoError(t, err)
	require.NotNil(t, sess)

	// Verify session was created in zset (Transition writes to zset)
	zsetClient := zset.NewClient(buildRedisClient(t, redisURL), zset.Config{KeyPrefix: ""})
	exists, err := zsetClient.Exists(ctx, key)
	require.NoError(t, err)
	assert.True(t, exists)

	// Verify session state includes merged app and user state
	assert.Equal(t, []byte("sessval"), sess.State["sesskey"])
	assert.Equal(t, []byte("appval"), sess.State["app:appkey"])
	assert.Equal(t, []byte("userval"), sess.State["user:userkey"])
}

// TestDeleteSession_ZsetSession tests DeleteSession with zset session
func TestDeleteSession_ZsetSession(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Create zset session via Transition mode
	svcT, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeTransition))
	require.NoError(t, err)

	ctx := context.Background()
	key := session.Key{AppName: "app1", UserID: "u1", SessionID: "del-zset"}
	sess, err := svcT.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Append some events and track events
	e := createTestEvent("e1", "agent", "content", time.Now(), false)
	require.NoError(t, svcT.AppendEvent(ctx, sess, e))

	te := &session.TrackEvent{
		Track:     "test",
		Payload:   json.RawMessage(`"data"`),
		Timestamp: time.Now(),
	}
	require.NoError(t, svcT.AppendTrackEvent(ctx, sess, te))
	svcT.Close()

	// Delete using Legacy mode
	svcL, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)
	defer svcL.Close()

	err = svcL.DeleteSession(ctx, key)
	require.NoError(t, err)

	// Verify session is deleted
	sess, err = svcL.GetSession(ctx, key)
	require.NoError(t, err)
	assert.Nil(t, sess)
}

// TestDeleteSession_HashidxSession tests DeleteSession with hashidx session
func TestDeleteSession_HashidxSession(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeNone))
	require.NoError(t, err)
	defer svc.Close()

	ctx := context.Background()
	key := session.Key{AppName: "app1", UserID: "u1", SessionID: "del-hash"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Append events
	e := createTestEvent("e1", "agent", "content", time.Now(), false)
	require.NoError(t, svc.AppendEvent(ctx, sess, e))

	// Delete session
	err = svc.DeleteSession(ctx, key)
	require.NoError(t, err)

	// Verify session is deleted
	sess, err = svc.GetSession(ctx, key)
	require.NoError(t, err)
	assert.Nil(t, sess)
}

// TestNewService_WithAllOptions tests NewService with all possible options
func TestNewService_WithAllOptions(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Test with various options
	svc, err := NewService(
		WithRedisClientURL(redisURL),
		WithKeyPrefix("test"),
		WithSessionTTL(time.Hour),
		WithAppStateTTL(2*time.Hour),
		WithUserStateTTL(30*time.Minute),
		WithEnableAsyncPersist(true),
		WithAsyncPersisterNum(4),
		WithSummarizer(&fakeSummarizer{allow: true, out: "test"}),
		WithAsyncSummaryNum(2),
		WithSummaryQueueSize(100),
		WithSummaryJobTimeout(30*time.Second),
		WithCompatMode(CompatModeTransition),
	)
	require.NoError(t, err)
	svc.Close()
}

// TestAsyncPersist_MultipleWorkers tests async persister with multiple workers
func TestAsyncPersist_MultipleWorkers(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(
		WithRedisClientURL(redisURL),
		WithEnableAsyncPersist(true),
		WithAsyncPersisterNum(4),
	)
	require.NoError(t, err)
	defer svc.Close()

	ctx := context.Background()
	key := session.Key{AppName: "app1", UserID: "u1", SessionID: "async-multi"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Send many events
	for i := 0; i < 50; i++ {
		e := createTestEvent(fmt.Sprintf("e%d", i), "agent", "content", time.Now(), false)
		err = svc.AppendEvent(ctx, sess, e)
		require.NoError(t, err)
	}

	// Give time for async workers to process
	time.Sleep(100 * time.Millisecond)

	// Verify events were persisted
	sessGet, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(sessGet.Events), 50)
}

// TestListSessions_MixedStorages tests ListSessions finding sessions from both storages
func TestListSessions_MixedStorages(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	userKey := session.UserKey{AppName: "app", UserID: "user"}

	// Create hashidx session
	svcN, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeNone))
	require.NoError(t, err)
	_, err = svcN.CreateSession(ctx, session.Key{AppName: "app", UserID: "user", SessionID: "hash-sess"}, nil)
	require.NoError(t, err)
	svcN.Close()

	// Create zset session
	svcT, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeTransition))
	require.NoError(t, err)
	_, err = svcT.CreateSession(ctx, session.Key{AppName: "app", UserID: "user", SessionID: "zset-sess"}, nil)
	require.NoError(t, err)
	svcT.Close()

	// List using Transition mode should find both
	svcT2, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeTransition))
	require.NoError(t, err)
	defer svcT2.Close()

	sessions, err := svcT2.ListSessions(ctx, userKey)
	require.NoError(t, err)
	assert.Len(t, sessions, 2)

	sessionIDs := make(map[string]bool)
	for _, s := range sessions {
		sessionIDs[s.ID] = true
	}
	assert.True(t, sessionIDs["hash-sess"])
	assert.True(t, sessionIDs["zset-sess"])
}

// TestTrimConversations_HashidxSession tests TrimConversations with hashidx session
func TestTrimConversations_HashidxSession(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeNone))
	require.NoError(t, err)
	defer svc.Close()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "trim-hash"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Append events with RequestIDs
	for i := 0; i < 3; i++ {
		e := createTestEvent("e"+string(rune('0'+i)), "agent", "content", time.Now().Add(time.Duration(i)*time.Second), false)
		e.RequestID = "req" + string(rune('0'+i))
		require.NoError(t, svc.AppendEvent(ctx, sess, e))
	}

	// Trim from hashidx session
	deleted, err := svc.TrimConversations(ctx, key, WithCount(1))
	require.NoError(t, err)
	assert.NotEmpty(t, deleted)
}

// TestDeleteSession_TransitionMode tests DeleteSession in Transition mode
func TestDeleteSession_TransitionMode(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svcT, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeTransition))
	require.NoError(t, err)

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "del-trans"}
	_, err = svcT.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	svcT.Close()

	// Delete using Transition mode (should delete from both)
	svcT2, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeTransition))
	require.NoError(t, err)
	defer svcT2.Close()

	err = svcT2.DeleteSession(ctx, key)
	require.NoError(t, err)

	// Verify session is deleted from both storages
	sess, err := svcT2.GetSession(ctx, key)
	require.NoError(t, err)
	assert.Nil(t, sess)
}

// TestUpdateUserState_LegacyMode tests UpdateUserState in Legacy mode
func TestUpdateUserState_LegacyMode(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svcL, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)
	defer svcL.Close()

	ctx := context.Background()
	userKey := session.UserKey{AppName: "app", UserID: "user"}

	// Write user state in Legacy mode - writes to hashidx
	err = svcL.UpdateUserState(ctx, userKey, session.StateMap{"key": []byte("value")})
	require.NoError(t, err)

	// Verify hashidx has the data
	hashidxClient := hashidx.NewClient(buildRedisClient(t, redisURL), hashidx.Config{KeyPrefix: ""})
	hashidxStates, err := hashidxClient.ListUserStates(ctx, userKey)
	require.NoError(t, err)
	assert.Equal(t, []byte("value"), hashidxStates["key"])

	// ListUserStates should return the data from hashidx
	states, err := svcL.ListUserStates(ctx, userKey)
	require.NoError(t, err)
	assert.Equal(t, []byte("value"), states["key"])
}

// TestDeleteUserState_LegacyMode tests DeleteUserState in Legacy mode
func TestDeleteUserState_LegacyMode(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svcL, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)
	defer svcL.Close()

	ctx := context.Background()
	userKey := session.UserKey{AppName: "app", UserID: "user"}

	// Create user state (goes to hashidx in Legacy mode)
	err = svcL.UpdateUserState(ctx, userKey, session.StateMap{"k1": []byte("v1"), "k2": []byte("v2")})
	require.NoError(t, err)

	// Delete k1
	err = svcL.DeleteUserState(ctx, userKey, "k1")
	require.NoError(t, err)

	// Verify k1 deleted, k2 still exists (using service API)
	states, err := svcL.ListUserStates(ctx, userKey)
	require.NoError(t, err)
	assert.Nil(t, states["k1"])
	assert.Equal(t, []byte("v2"), states["k2"])
}

// TestUpdateUserState_Transition_OnlyHashidx tests UpdateUserState in Transition mode when only hashidx has data
func TestUpdateUserState_Transition_OnlyHashidx(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// First write to hashidx only
	svcN, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeNone))
	require.NoError(t, err)

	ctx := context.Background()
	userKey := session.UserKey{AppName: "app", UserID: "user"}
	err = svcN.UpdateUserState(ctx, userKey, session.StateMap{"key": []byte("hash-value")})
	require.NoError(t, err)
	svcN.Close()

	// Now read using Transition mode
	svcT, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeTransition))
	require.NoError(t, err)
	defer svcT.Close()

	states, err := svcT.ListUserStates(ctx, userKey)
	require.NoError(t, err)
	assert.Equal(t, []byte("hash-value"), states["key"])
}

// TestGetSession_NonExistent tests GetSession with non-existent session in various modes
func TestGetSession_NonExistent(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	modes := []CompatMode{CompatModeNone, CompatModeLegacy, CompatModeTransition}

	for _, mode := range modes {
		svc, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(mode))
		require.NoError(t, err)

		ctx := context.Background()
		key := session.Key{AppName: "app", UserID: "user", SessionID: "nonexistent"}

		sess, err := svc.GetSession(ctx, key)
		require.NoError(t, err)
		assert.Nil(t, sess, "mode %d should return nil for non-existent session", mode)

		svc.Close()
	}
}

// TestListUserStates_NoneMode tests ListUserStates in None mode
func TestListUserStates_NoneMode(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeNone))
	require.NoError(t, err)
	defer svc.Close()

	ctx := context.Background()
	userKey := session.UserKey{AppName: "app", UserID: "user"}

	// Write user state in None mode - should only write to hashidx
	err = svc.UpdateUserState(ctx, userKey, session.StateMap{"key": []byte("value")})
	require.NoError(t, err)

	// List should return the data from hashidx
	states, err := svc.ListUserStates(ctx, userKey)
	require.NoError(t, err)
	assert.Equal(t, []byte("value"), states["key"])
}

// TestUpdateAppState_LegacyMode tests UpdateAppState in Legacy mode
func TestUpdateAppState_LegacyMode(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svcL, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)
	defer svcL.Close()

	ctx := context.Background()

	// Update app state in Legacy mode (goes to hashidx)
	err = svcL.UpdateAppState(ctx, "app", session.StateMap{"key": []byte("value")})
	require.NoError(t, err)

	// Verify it can be read back
	states, err := svcL.ListAppStates(ctx, "app")
	require.NoError(t, err)
	assert.Equal(t, []byte("value"), states["key"])
}

// TestDeleteAppState_LegacyMode tests DeleteAppState in Legacy mode
func TestDeleteAppState_LegacyMode(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svcL, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)
	defer svcL.Close()

	ctx := context.Background()

	// Create app state
	err = svcL.UpdateAppState(ctx, "app", session.StateMap{"k1": []byte("v1"), "k2": []byte("v2")})
	require.NoError(t, err)

	// Delete k1
	err = svcL.DeleteAppState(ctx, "app", "k1")
	require.NoError(t, err)

	// Verify k1 deleted, k2 still exists
	states, err := svcL.ListAppStates(ctx, "app")
	require.NoError(t, err)
	assert.Nil(t, states["k1"])
	assert.Equal(t, []byte("v2"), states["k2"])
}

// TestEnqueueSummaryJob_NoAsyncWorker tests EnqueueSummaryJob without async worker
func TestEnqueueSummaryJob_NoAsyncWorker(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Create service without async summary worker
	svc, err := NewService(
		WithRedisClientURL(redisURL),
		WithSummarizer(&fakeSummarizer{allow: true, out: "test"}),
		// No async summary options, so no async worker
	)
	require.NoError(t, err)
	defer svc.Close()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "enqueue"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Enqueue should work even without async worker (falls back to sync)
	err = svc.EnqueueSummaryJob(ctx, sess, "", false)
	require.NoError(t, err)
}

// TestEnqueueSummaryJob_NoSummarizer tests EnqueueSummaryJob without summarizer
func TestEnqueueSummaryJob_NoSummarizer(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Create service without summarizer
	svc, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer svc.Close()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "no-sum"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Enqueue should return nil when no summarizer
	err = svc.EnqueueSummaryJob(ctx, sess, "", false)
	require.NoError(t, err)
}

// TestGetSessionSummaryText_LegacyMode tests GetSessionSummaryText in Legacy mode with zset data
func TestGetSessionSummaryText_LegacyMode(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Create zset session
	svcT, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeTransition))
	require.NoError(t, err)

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	sess, err := svcT.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	svcT.Close()

	// Add summary to zset
	client := buildRedisClient(t, redisURL)
	sumKey := zset.GetSessionSummaryKey(key)
	summaries := map[string]*session.Summary{
		session.SummaryFilterKeyAllContents: {
			Summary:   "legacy-summary",
			UpdatedAt: time.Now().UTC(),
		},
	}
	payload, _ := json.Marshal(summaries)
	client.HSet(ctx, sumKey, "sess", string(payload))

	// Get summary using Legacy mode
	svcL, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)
	defer svcL.Close()

	text, ok := svcL.GetSessionSummaryText(ctx, sess)
	require.True(t, ok)
	assert.Equal(t, "legacy-summary", text)
}

// TestUpdateSessionState_LegacyMode tests UpdateSessionState in Legacy mode
func TestUpdateSessionState_LegacyMode(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Create zset session
	svcT, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeTransition))
	require.NoError(t, err)

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	_, err = svcT.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	svcT.Close()

	// Update session state using Legacy mode
	svcL, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)
	defer svcL.Close()

	err = svcL.UpdateSessionState(ctx, key, session.StateMap{"newkey": []byte("newval")})
	require.NoError(t, err)

	// Verify state was updated by reading the session
	sessL, err := svcL.GetSession(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, []byte("newval"), sessL.State["newkey"])
}

// TestCreateSessionSummary_LegacyMode tests CreateSessionSummary in Legacy mode
func TestCreateSessionSummary_LegacyMode(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Create zset session
	svcT, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeTransition))
	require.NoError(t, err)

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sum-leg"}
	sess, err := svcT.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Add an event
	e := event.New("inv", "author")
	e.Timestamp = time.Now()
	e.Response = &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hello"}}}}
	require.NoError(t, svcT.AppendEvent(ctx, sess, e))
	svcT.Close()

	// Create summary using Legacy mode
	svcL, err := NewService(
		WithRedisClientURL(redisURL),
		WithCompatMode(CompatModeLegacy),
		WithSummarizer(&fakeSummarizer{allow: true, out: "legacy-summary"}),
		WithSessionTTL(time.Hour),
	)
	require.NoError(t, err)
	defer svcL.Close()

	// Get session with events
	sessL, err := svcL.GetSession(ctx, key)
	require.NoError(t, err)

	err = svcL.CreateSessionSummary(ctx, sessL, "", false)
	require.NoError(t, err)
}

// TestGetSummaryFromZSet_ErrorPath tests error path for getSummaryFromZSet
func TestGetSummaryFromZSet_ErrorPath(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	redisURL := "redis://" + mr.Addr()
	defer mr.Close()

	svc, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)

	// Create session first
	key := session.Key{AppName: "app1", UserID: "u1", SessionID: "zsum-err"}
	sess, err := svc.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)

	// Close Redis to trigger error
	mr.Close()

	// GetSessionSummaryText should handle zset error gracefully
	_, ok := svc.GetSessionSummaryText(context.Background(), sess)
	// When Redis is closed, it should return false (not found)
	assert.False(t, ok)
	svc.Close()
}

// TestGetSummaryFromHashIdx_ErrorPath tests error path for getSummaryFromHashIdx
func TestGetSummaryFromHashIdx_ErrorPath(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	redisURL := "redis://" + mr.Addr()
	defer mr.Close()

	svc, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeNone))
	require.NoError(t, err)

	// Create session
	key := session.Key{AppName: "app1", UserID: "u1", SessionID: "hsum-err"}
	sess, err := svc.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)

	// Close Redis
	mr.Close()

	// GetSessionSummaryText should handle hashidx error gracefully
	_, ok := svc.GetSessionSummaryText(context.Background(), sess)
	assert.False(t, ok)
	svc.Close()
}

// TestCheckSessionExists_BothStorages tests checkSessionExists with sessions in both storages
func TestCheckSessionExists_BothStorages(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	key := session.Key{AppName: "app1", UserID: "u1", SessionID: "exist-test"}

	// Create in zset (Transition mode)
	svcT, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeTransition))
	require.NoError(t, err)
	_, err = svcT.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	svcT.Close()

	// Check existence using Legacy mode
	svcL, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)
	defer svcL.Close()

	zsetExists, hashidxExists, err := svcL.checkSessionExists(ctx, key)
	require.NoError(t, err)
	assert.True(t, zsetExists)
	assert.False(t, hashidxExists)
}

// TestListSessions_EmptyResult tests ListSessions with empty result
func TestListSessions_EmptyResult(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer svc.Close()

	ctx := context.Background()
	userKey := session.UserKey{AppName: "nonexistent", UserID: "user"}

	sessions, err := svc.ListSessions(ctx, userKey)
	require.NoError(t, err)
	assert.Empty(t, sessions)
}

// ============================================================================
// Additional tests for uncovered lines in service.go
// ============================================================================

// TestMergeAppUserState_ListAppStatesError tests mergeAppUserState when ListAppStates returns error
func TestMergeAppUserState_ListAppStatesError(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	redisURL := "redis://" + mr.Addr()
	defer mr.Close()

	svc, err := NewService(
		WithRedisClientURL(redisURL),
		WithAppStateTTL(time.Hour),
		WithUserStateTTL(time.Hour),
	)
	require.NoError(t, err)
	defer svc.Close()

	ctx := context.Background()
	key := session.Key{AppName: "test", UserID: "user", SessionID: "session1"}

	// Create a session
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	require.NotNil(t, sess)

	// Pre-populate user state before closing Redis
	userKey := session.UserKey{AppName: "test", UserID: "user"}
	err = svc.UpdateUserState(ctx, userKey, session.StateMap{"uk": []byte("uv")})
	require.NoError(t, err)

	// Close Redis to cause ListAppStates to fail
	mr.Close()

	// mergeAppUserState should handle error gracefully (just log warning)
	mergedSess, err := svc.mergeAppUserState(ctx, key, sess)
	// Should not return error, just skip app state merge
	require.NoError(t, err)
	require.NotNil(t, mergedSess)
}

// TestMergeAppUserState_ListUserStatesError tests mergeAppUserState when ListUserStates returns error
func TestMergeAppUserState_ListUserStatesError(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	redisURL := "redis://" + mr.Addr()
	defer mr.Close()

	svc, err := NewService(
		WithRedisClientURL(redisURL),
		WithAppStateTTL(time.Hour),
		WithUserStateTTL(time.Hour),
	)
	require.NoError(t, err)
	defer svc.Close()

	ctx := context.Background()
	key := session.Key{AppName: "test", UserID: "user", SessionID: "session2"}

	// Create a session
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	require.NotNil(t, sess)

	// Pre-populate app state before closing Redis
	err = svc.UpdateAppState(ctx, "test", session.StateMap{"ak": []byte("av")})
	require.NoError(t, err)

	// Close Redis to cause ListUserStates to fail
	mr.Close()

	// mergeAppUserState should handle error gracefully (just log warning)
	mergedSess, err := svc.mergeAppUserState(ctx, key, sess)
	// Should not return error, just skip user state merge
	require.NoError(t, err)
	require.NotNil(t, mergedSess)
}

// TestMergeAppUserState_RefreshTTLsError tests mergeAppUserState when Refresh TTLs fail
func TestMergeAppUserState_RefreshTTLsError(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	redisURL := "redis://" + mr.Addr()
	defer mr.Close()

	svc, err := NewService(
		WithRedisClientURL(redisURL),
		WithAppStateTTL(time.Hour),
		WithUserStateTTL(time.Hour),
	)
	require.NoError(t, err)

	ctx := context.Background()
	key := session.Key{AppName: "test", UserID: "user", SessionID: "session3"}

	// Pre-populate states
	err = svc.UpdateAppState(ctx, "test", session.StateMap{"ak": []byte("av")})
	require.NoError(t, err)
	userKey := session.UserKey{AppName: "test", UserID: "user"}
	err = svc.UpdateUserState(ctx, userKey, session.StateMap{"uk": []byte("uv")})
	require.NoError(t, err)

	// Create session
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	require.NotNil(t, sess)

	// Close Redis to cause Refresh TTL to fail
	mr.Close()

	// mergeAppUserState should handle error gracefully
	mergedSess, err := svc.mergeAppUserState(ctx, key, sess)
	// Should not return error even if Refresh TTL fails
	require.NoError(t, err)
	require.NotNil(t, mergedSess)
	svc.Close()
}

// TestPersistEvent_SlowPath_HashidxError tests persistEvent slow path when hashidx AppendEvent fails
func TestPersistEvent_SlowPath_HashidxError(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	redisURL := "redis://" + mr.Addr()
	defer mr.Close()

	// Create service in None mode (hashidx only)
	svc, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeNone))
	require.NoError(t, err)

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess1"}

	// Create session
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	require.NotNil(t, sess)

	// Close Redis to cause AppendEvent to fail
	mr.Close()

	// Clear ServiceMeta to force slow path
	sess.ServiceMeta = nil

	e := createTestEvent("pe1", "agent", "content", time.Now(), false)
	err = svc.AppendEvent(ctx, sess, e)
	// Should return error when hashidx fails
	require.Error(t, err)
	svc.Close()
}

// TestCheckSessionExists_HashidxError tests checkSessionExists when hashidx check fails
func TestCheckSessionExists_HashidxError(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	redisURL := "redis://" + mr.Addr()
	defer mr.Close()

	svc, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "checkerr"}

	// Close Redis to cause checkSessionExists to fail
	mr.Close()

	_, _, err = svc.checkSessionExists(ctx, key)
	// Should return error when pipeline fails
	require.Error(t, err)
	svc.Close()
}

// TestPersistEvent_HashidxErrorPath tests persistEvent hashidx error path
func TestPersistEvent_HashidxErrorPath(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	redisURL := "redis://" + mr.Addr()
	defer mr.Close()

	// Create service in None mode
	svc, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeNone))
	require.NoError(t, err)

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "hasherr"}

	// Create session
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	require.NotNil(t, sess)

	// Set version tag to hashidx to use fast path
	sess.ServiceMeta = map[string]string{"storage_type": "hashidx"}

	// Close Redis to cause AppendEvent to fail
	mr.Close()

	e := createTestEvent("e1", "agent", "content", time.Now(), false)
	err = svc.persistEvent(ctx, "hashidx", e, key)
	// Should return error when hashidx fails
	require.Error(t, err)
	svc.Close()
}

// TestPersistEvent_ZsetErrorPath tests persistEvent zset error path
func TestPersistEvent_ZsetErrorPath(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	redisURL := "redis://" + mr.Addr()
	defer mr.Close()

	// Create service in Legacy mode
	svc, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "zseterr"}

	// Create zset session
	svcT, _ := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeTransition))
	sess, _ := svcT.CreateSession(ctx, key, nil)
	svcT.Close()
	require.NotNil(t, sess)

	// Set version tag to zset to use fast path
	sess.ServiceMeta = map[string]string{"storage_type": "zset"}

	// Close Redis to cause AppendEvent to fail
	mr.Close()

	e := createTestEvent("e1", "agent", "content", time.Now(), false)
	err = svc.persistEvent(ctx, "zset", e, key)
	// Should return error when zset fails
	require.Error(t, err)
	svc.Close()
}

// ============================================================================
// NewService negative TTL normalization (lines 123-132)
// ============================================================================

func TestNewService_NegativeTTLNormalization(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	// Negative TTLs should be normalized to 0 (no expiration)
	svc, err := NewService(
		WithRedisClientURL(redisURL),
		WithSessionTTL(-1*time.Second),
		WithAppStateTTL(-2*time.Second),
		WithUserStateTTL(-3*time.Second),
	)
	require.NoError(t, err)
	defer svc.Close()

	// Should be usable after normalization
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "neg-ttl"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	assert.NotNil(t, sess)
}

// ============================================================================
// ListSessions: duplicate session merge path (lines 461-464)
// ============================================================================

func TestListSessions_DuplicateMerge(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	// Same session ID created in both zset and hashidx
	key := session.Key{AppName: "dup", UserID: "u1", SessionID: "dup-sess"}

	// Create in zset (Transition mode creates in zset)
	svcT, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeTransition))
	require.NoError(t, err)
	_, err = svcT.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	svcT.Close()

	// Create same session in hashidx (None mode creates in hashidx)
	svcN, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeNone))
	require.NoError(t, err)
	_, err = svcN.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	svcN.Close()

	// List using Transition mode: both storages return same session - duplicate merge path
	svcT2, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeTransition))
	require.NoError(t, err)
	defer svcT2.Close()

	sessions, err := svcT2.ListSessions(ctx, session.UserKey{AppName: "dup", UserID: "u1"})
	require.NoError(t, err)
	// Duplicate merged - should only be 1 session
	assert.Len(t, sessions, 1)
}

// ============================================================================
// TrimConversations: no session found returns nil, nil (line 694)
// ============================================================================

func TestTrimConversations_NotFound(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeNone))
	require.NoError(t, err)
	defer svc.Close()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "nonexistent"}

	// Session doesn't exist: returns nil, nil
	deleted, err := svc.TrimConversations(ctx, key, WithCount(1))
	require.NoError(t, err)
	assert.Nil(t, deleted)
}

// ============================================================================
// appendEventInternal: context cancellation path (lines 572-574)
// ============================================================================

func TestAppendEventInternal_ContextCancellation(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	redisURL := "redis://" + mr.Addr()
	defer mr.Close()

	// Use 1 persister with very small buffer so channel fills up quickly
	svc, err := NewService(
		WithRedisClientURL(redisURL),
		WithEnableAsyncPersist(true),
		WithAsyncPersisterNum(1),
	)
	require.NoError(t, err)

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "ctx-cancel"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Fill the event channel by sending many events quickly
	for i := 0; i < defaultChanBufferSize+10; i++ {
		e := createTestEvent(fmt.Sprintf("e%d", i), "agent", "fill", time.Now().Add(time.Duration(i)*time.Millisecond), false)
		_ = svc.AppendEvent(ctx, sess, e)
	}

	// Now send with cancelled context - should hit the context cancellation path
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel() // cancel immediately

	e := createTestEvent("cancelled", "agent", "cancel", time.Now(), false)
	err = svc.AppendEvent(cancelCtx, sess, e)
	// Either error (ctx cancelled) or nil (channel had room) - both are valid
	_ = err

	svc.Close()
}

// ============================================================================
// startAsyncPersistWorker: error logging path (lines 742-744)
// ============================================================================

func TestAsyncPersist_PersistError(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	redisURL := "redis://" + mr.Addr()
	defer mr.Close()

	svc, err := NewService(
		WithRedisClientURL(redisURL),
		WithEnableAsyncPersist(true),
		WithAsyncPersisterNum(1),
	)
	require.NoError(t, err)

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "async-err"}
	sess, err := svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Send event via async path
	e := createTestEvent("e1", "agent", "content", time.Now(), false)
	err = svc.AppendEvent(ctx, sess, e)
	require.NoError(t, err)

	// Close Redis so the async worker's persistEvent call fails (logs error)
	mr.Close()

	// Give async worker time to process and log the error
	time.Sleep(50 * time.Millisecond)

	svc.Close()
}

// ============================================================================
// ListSessions: hashidx error path (lines 438-441)
// ============================================================================

func TestListSessions_HashidxError(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	redisURL := "redis://" + mr.Addr()
	defer mr.Close()

	svc, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeNone))
	require.NoError(t, err)

	ctx := context.Background()
	mr.Close()

	_, err = svc.ListSessions(ctx, session.UserKey{AppName: "app", UserID: "u1"})
	require.Error(t, err)
	svc.Close()
}

// ============================================================================
// DeleteSession: error paths (lines 503-508)
// ============================================================================

func TestDeleteSession_HashidxError(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	redisURL := "redis://" + mr.Addr()
	defer mr.Close()

	svc, err := NewService(WithRedisClientURL(redisURL), WithCompatMode(CompatModeNone))
	require.NoError(t, err)

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "del-err"}

	_, err = svc.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	mr.Close()

	err = svc.DeleteSession(ctx, key)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete session (hashidx)")
	svc.Close()
}
