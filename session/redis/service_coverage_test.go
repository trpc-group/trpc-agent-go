package redis

import (
	"context"
	"encoding/json"
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
