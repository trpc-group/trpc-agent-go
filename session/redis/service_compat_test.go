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
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/redis/internal/hashidx"
	"trpc.group/trpc-go/trpc-agent-go/session/redis/internal/zset"
)

// --- Helper: seed old zset data directly (simulates pre-existing v1 data) ---

// seedZsetSession creates a session in zset storage via Transition mode,
// appends events, and optionally adds track events and summary.
// Returns the session key.
func seedZsetSession(
	t *testing.T, ctx context.Context, redisURL string,
	appName, userID, sessionID string,
	numEvents int,
	trackName string,
	summaryText string,
) session.Key {
	t.Helper()
	key := session.Key{AppName: appName, UserID: userID, SessionID: sessionID}

	svcT, err := NewService(
		WithRedisClientURL(redisURL),
		WithCompatMode(CompatModeTransition),
	)
	require.NoError(t, err)

	sess, err := svcT.CreateSession(ctx, key, session.StateMap{"seed": []byte("zset")})
	require.NoError(t, err)

	for i := 0; i < numEvents; i++ {
		now := time.Now()
		evt := createTestEvent(fmt.Sprintf("e%s%d", sessionID[:4], i), "", "msg", now, true)
		err = svcT.AppendEvent(ctx, sess, evt)
		require.NoError(t, err)
		time.Sleep(time.Millisecond)
	}

	if trackName != "" {
		trackEvt := &session.TrackEvent{
			Track:     session.Track(trackName),
			Payload:   json.RawMessage(`{"src":"zset"}`),
			Timestamp: time.Now(),
		}
		err = svcT.AppendTrackEvent(ctx, sess, trackEvt)
		require.NoError(t, err)
	}

	if summaryText != "" {
		client := buildRedisClient(t, redisURL)
		sumKey := zset.GetSessionSummaryKey(key)
		summaries := map[string]*session.Summary{
			session.SummaryFilterKeyAllContents: {
				Summary:   summaryText,
				UpdatedAt: time.Now().UTC(),
			},
		}
		payload, err := json.Marshal(summaries)
		require.NoError(t, err)
		err = client.HSet(ctx, sumKey, sessionID, string(payload)).Err()
		require.NoError(t, err)
	}

	svcT.Close()
	return key
}

// seedHashidxSession creates a session in hashidx storage via Legacy/None mode.
func seedHashidxSession(
	t *testing.T, ctx context.Context, redisURL string,
	appName, userID, sessionID string,
	numEvents int,
	trackName string,
	summaryText string,
) session.Key {
	t.Helper()
	key := session.Key{AppName: appName, UserID: userID, SessionID: sessionID}

	svcL, err := NewService(
		WithRedisClientURL(redisURL),
		WithEnableUserSessionIndex(true),
		WithCompatMode(CompatModeLegacy),
	)
	require.NoError(t, err)

	sess, err := svcL.CreateSession(ctx, key, session.StateMap{"seed": []byte("hashidx")})
	require.NoError(t, err)

	for i := 0; i < numEvents; i++ {
		now := time.Now()
		evt := createTestEvent(fmt.Sprintf("e%s%d", sessionID[:4], i), "", "msg", now, true)
		err = svcL.AppendEvent(ctx, sess, evt)
		require.NoError(t, err)
		time.Sleep(time.Millisecond)
	}

	if trackName != "" {
		trackEvt := &session.TrackEvent{
			Track:     session.Track(trackName),
			Payload:   json.RawMessage(`{"src":"hashidx"}`),
			Timestamp: time.Now(),
		}
		err = svcL.AppendTrackEvent(ctx, sess, trackEvt)
		require.NoError(t, err)
	}

	if summaryText != "" {
		client := buildRedisClient(t, redisURL)
		sumKey := hashidx.GetSessionSummaryKey("", key)
		summaries := map[string]*session.Summary{
			session.SummaryFilterKeyAllContents: {
				Summary:   summaryText,
				UpdatedAt: time.Now().UTC(),
			},
		}
		payload, err := json.Marshal(summaries)
		require.NoError(t, err)
		err = client.Set(ctx, sumKey, payload, 0).Err()
		require.NoError(t, err)
	}

	svcL.Close()
	return key
}

// ===================================================================================
// Mixed-Node Scenarios: Transition and Legacy nodes running simultaneously
// ===================================================================================

// TestMixedNodes_TransitionCreates_LegacyReads tests the core rolling-upgrade scenario:
// a Transition node creates sessions (zset), a Legacy node reads them and creates new ones (hashidx).
func TestMixedNodes_TransitionCreates_LegacyReads(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()
	ctx := context.Background()

	// Transition node creates a session with events + track + user state
	svcT, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeTransition))
	require.NoError(t, err)
	defer svcT.Close()

	keyOld := session.Key{AppName: "app1", UserID: "user1", SessionID: "zset-sid"}
	sessOld, err := svcT.CreateSession(ctx, keyOld, session.StateMap{"mode": []byte("transition")})
	require.NoError(t, err)

	evt1 := createTestEvent("e1", "", "hello from transition", time.Now(), true)
	require.NoError(t, svcT.AppendEvent(ctx, sessOld, evt1))

	trackEvt := &session.TrackEvent{
		Track:     "mytrack",
		Payload:   json.RawMessage(`{"from":"transition"}`),
		Timestamp: time.Now(),
	}
	require.NoError(t, svcT.AppendTrackEvent(ctx, sessOld, trackEvt))

	require.NoError(t, svcT.UpdateUserState(ctx,
		session.UserKey{AppName: "app1", UserID: "user1"},
		session.StateMap{"uk": []byte("transition-val")}))

	// Legacy node reads the zset session
	svcL, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)
	defer svcL.Close()

	gotSess, err := svcL.GetSession(ctx, keyOld)
	require.NoError(t, err)
	require.NotNil(t, gotSess, "Legacy should read Transition's zset session")
	assert.Equal(t, "zset-sid", gotSess.ID)
	assert.GreaterOrEqual(t, len(gotSess.Events), 1)
	assert.Contains(t, gotSess.Tracks, session.Track("mytrack"))

	// Legacy node appends events to the zset session
	evt2 := createTestEvent("e2", "", "appended by legacy", time.Now(), true)
	require.NoError(t, svcL.AppendEvent(ctx, gotSess, evt2))

	// Re-read: should see both events
	refreshed, err := svcL.GetSession(ctx, keyOld)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(refreshed.Events), 2)

	// Legacy node creates a new session (hashidx)
	keyNew := session.Key{AppName: "app1", UserID: "user1", SessionID: "hashidx-sid"}
	sessNew, err := svcL.CreateSession(ctx, keyNew, session.StateMap{"mode": []byte("legacy")})
	require.NoError(t, err)
	require.NotNil(t, sessNew)

	evt3 := createTestEvent("e3", "", "hello from legacy", time.Now(), true)
	require.NoError(t, svcL.AppendEvent(ctx, sessNew, evt3))

	// Verify storage: zset session in zset, hashidx session in hashidx
	client := buildRedisClient(t, redisURL)
	zsetKey := zset.GetSessionStateKey(keyOld)
	exists, err := client.HExists(ctx, zsetKey, keyOld.SessionID).Result()
	require.NoError(t, err)
	assert.True(t, exists, "old session should be in zset")

	hashidxKey := hashidx.GetSessionMetaKey("", keyNew)
	hExists, err := client.Exists(ctx, hashidxKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), hExists, "new session should be in hashidx")

	// Both nodes should see both sessions via ListSessions
	sessions, err := svcL.ListSessions(ctx, session.UserKey{AppName: "app1", UserID: "user1"})
	require.NoError(t, err)
	assert.Len(t, sessions, 2)

	sessionsT, err := svcT.ListSessions(ctx, session.UserKey{AppName: "app1", UserID: "user1"})
	require.NoError(t, err)
	assert.Len(t, sessionsT, 2)

	// User state: Transition wrote to both, Legacy reads hashidx first
	states, err := svcL.ListUserStates(ctx, session.UserKey{AppName: "app1", UserID: "user1"})
	require.NoError(t, err)
	assert.Equal(t, []byte("transition-val"), states["uk"])
}

// TestMixedNodes_TransitionCreates_LegacyAppendsTrack tests that a Legacy node
// can append track events to a session created by a Transition node.
func TestMixedNodes_TransitionCreates_LegacyAppendsTrack(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()
	ctx := context.Background()

	key := session.Key{AppName: "app1", UserID: "user1", SessionID: "track-compat-sid"}

	// Transition node creates session with an initial track event
	svcT, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeTransition))
	require.NoError(t, err)

	sess, err := svcT.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	evt := createTestEvent("e1", "", "hello", time.Now(), true)
	require.NoError(t, svcT.AppendEvent(ctx, sess, evt))

	trackEvt1 := &session.TrackEvent{
		Track:     "shared-track",
		Payload:   json.RawMessage(`{"step":1}`),
		Timestamp: time.Now(),
	}
	require.NoError(t, svcT.AppendTrackEvent(ctx, sess, trackEvt1))
	svcT.Close()

	// Legacy node reads and appends another track event to the same track
	svcL, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)
	defer svcL.Close()

	gotSess, err := svcL.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, gotSess)

	trackEvt2 := &session.TrackEvent{
		Track:     "shared-track",
		Payload:   json.RawMessage(`{"step":2}`),
		Timestamp: time.Now(),
	}
	require.NoError(t, svcL.AppendTrackEvent(ctx, gotSess, trackEvt2))

	// Verify: both track events readable with correct payload content
	refreshed, err := svcL.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, refreshed)
	require.Contains(t, refreshed.Tracks, session.Track("shared-track"))
	trackEvents := refreshed.Tracks[session.Track("shared-track")]
	require.GreaterOrEqual(t, len(trackEvents.Events), 2, "should have at least 2 track events")

	payloads := make([]string, 0, len(trackEvents.Events))
	for _, te := range trackEvents.Events {
		payloads = append(payloads, string(te.Payload))
	}
	assert.Contains(t, payloads, `{"step":1}`, "should contain Transition's track event payload")
	assert.Contains(t, payloads, `{"step":2}`, "should contain Legacy's track event payload")
}

// TestMixedNodes_LegacyCreates_NoneReads tests migration from Legacy to None:
// hashidx sessions survive, zset sessions become invisible.
func TestMixedNodes_LegacyCreates_NoneReads(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()
	ctx := context.Background()

	// Seed: old zset data (pre-existing v1 sessions)
	keyZset := seedZsetSession(t, ctx, redisURL, "app1", "user1", "old-zset-sid", 2, "old-track", "old summary")
	// Seed: Legacy mode hashidx session
	keyHashidx := seedHashidxSession(t, ctx, redisURL, "app1", "user1", "new-hashidx-sid", 2, "new-track", "new summary")

	// Legacy node should see both
	svcL, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)
	sessions, err := svcL.ListSessions(ctx, session.UserKey{AppName: "app1", UserID: "user1"})
	require.NoError(t, err)
	assert.Len(t, sessions, 2, "Legacy sees both zset and hashidx sessions")
	svcL.Close()

	// None node should only see hashidx session
	svcN, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeNone))
	require.NoError(t, err)
	defer svcN.Close()

	sessions, err = svcN.ListSessions(ctx, session.UserKey{AppName: "app1", UserID: "user1"})
	require.NoError(t, err)
	assert.Len(t, sessions, 1, "None mode only sees hashidx session")
	assert.Equal(t, "new-hashidx-sid", sessions[0].ID)

	// None cannot see zset session
	sessZset, err := svcN.GetSession(ctx, keyZset)
	require.NoError(t, err)
	assert.Nil(t, sessZset, "None should not see zset session")

	// None can see hashidx session with all data
	sessHashidx, err := svcN.GetSession(ctx, keyHashidx)
	require.NoError(t, err)
	require.NotNil(t, sessHashidx)
	assert.GreaterOrEqual(t, len(sessHashidx.Events), 1)
	assert.Contains(t, sessHashidx.Tracks, session.Track("new-track"))
	require.NotNil(t, sessHashidx.Summaries)
	require.Contains(t, sessHashidx.Summaries, session.SummaryFilterKeyAllContents)
	assert.Equal(t, "new summary", sessHashidx.Summaries[session.SummaryFilterKeyAllContents].Summary)
}

// ===================================================================================
// Old Data Compatibility: pre-existing zset data tested across all modes
// ===================================================================================

// TestOldData_ZsetSession_LegacyReadsSummary tests that summary data stored in zset
// is readable via Legacy mode fallback.
func TestOldData_ZsetSession_LegacyReadsSummary(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()
	ctx := context.Background()

	key := seedZsetSession(t, ctx, redisURL, "app1", "user1", "sum-sid", 2, "", "zset-summary-text")

	svcL, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)
	defer svcL.Close()

	sess, err := svcL.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, sess)

	// Summary should be loaded from zset storage via fallback
	require.NotNil(t, sess.Summaries, "summaries should be loaded from zset")
	require.Contains(t, sess.Summaries, session.SummaryFilterKeyAllContents)
	assert.Equal(t, "zset-summary-text", sess.Summaries[session.SummaryFilterKeyAllContents].Summary)

	// GetSessionSummaryText should also work
	text, found := svcL.GetSessionSummaryText(ctx, sess)
	assert.True(t, found)
	assert.Equal(t, "zset-summary-text", text)
}

// TestOldData_ZsetSession_LegacyReadsTrack tests that track events stored in zset
// are readable via Legacy mode.
func TestOldData_ZsetSession_LegacyReadsTrack(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()
	ctx := context.Background()

	key := seedZsetSession(t, ctx, redisURL, "app1", "user1", "trk-sid", 2, "v1-track", "")

	svcL, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)
	defer svcL.Close()

	sess, err := svcL.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, sess)

	// Track data should be loaded from zset
	require.NotNil(t, sess.Tracks)
	require.Contains(t, sess.Tracks, session.Track("v1-track"))
	trackEvents := sess.Tracks[session.Track("v1-track")]
	require.NotNil(t, trackEvents)
	assert.Len(t, trackEvents.Events, 1)
	assert.JSONEq(t, `{"src":"zset"}`, string(trackEvents.Events[0].Payload))
}

// TestOldData_ZsetUserState_LegacyFallback tests that user state stored in zset
// is readable via Legacy mode when hashidx has no data.
func TestOldData_ZsetUserState_LegacyFallback(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()
	ctx := context.Background()

	// Seed zset user state directly
	client := buildRedisClient(t, redisURL)
	zsetUserKey := zset.GetUserStateKey(session.Key{AppName: "app1", UserID: "user1"})
	require.NoError(t, client.HSet(ctx, zsetUserKey, "old_key", "old_val").Err())

	svcL, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)
	defer svcL.Close()

	// hashidx has no user state, should fall back to zset
	states, err := svcL.ListUserStates(ctx, session.UserKey{AppName: "app1", UserID: "user1"})
	require.NoError(t, err)
	assert.Equal(t, []byte("old_val"), states["old_key"])

	// After updating via Legacy (writes to hashidx), should read hashidx
	require.NoError(t, svcL.UpdateUserState(ctx,
		session.UserKey{AppName: "app1", UserID: "user1"},
		session.StateMap{"new_key": []byte("new_val")}))

	states, err = svcL.ListUserStates(ctx, session.UserKey{AppName: "app1", UserID: "user1"})
	require.NoError(t, err)
	assert.Equal(t, []byte("new_val"), states["new_key"])
}

// TestOldData_ZsetUserState_TransitionDualWrite tests that Transition mode
// writes user state to both zset and hashidx.
func TestOldData_ZsetUserState_TransitionDualWrite(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()
	ctx := context.Background()

	svcT, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeTransition))
	require.NoError(t, err)
	defer svcT.Close()

	userKey := session.UserKey{AppName: "app1", UserID: "user1"}
	require.NoError(t, svcT.UpdateUserState(ctx, userKey, session.StateMap{"k": []byte("v")}))

	client := buildRedisClient(t, redisURL)

	// Both storages should have the data
	zsetUserKey := zset.GetUserStateKey(session.Key{AppName: "app1", UserID: "user1"})
	val, err := client.HGet(ctx, zsetUserKey, "k").Result()
	require.NoError(t, err)
	assert.Equal(t, "v", val)

	hashidxUserKey := hashidx.GetUserStateKey("", "app1", "user1")
	val, err = client.HGet(ctx, hashidxUserKey, "k").Result()
	require.NoError(t, err)
	assert.Equal(t, "v", val)
}

// TestOldData_ZsetAppState_SharedKey tests that appstate key is shared between
// all compat modes (no routing needed).
func TestOldData_ZsetAppState_SharedKey(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()
	ctx := context.Background()

	// Write via Transition
	svcT, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeTransition))
	require.NoError(t, err)
	require.NoError(t, svcT.UpdateAppState(ctx, "app1", session.StateMap{"ak": []byte("av")}))
	svcT.Close()

	// Read via Legacy
	svcL, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)
	states, err := svcL.ListAppStates(ctx, "app1")
	require.NoError(t, err)
	assert.Equal(t, []byte("av"), states["ak"])
	svcL.Close()

	// Read via None
	svcN, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeNone))
	require.NoError(t, err)
	defer svcN.Close()
	states, err = svcN.ListAppStates(ctx, "app1")
	require.NoError(t, err)
	assert.Equal(t, []byte("av"), states["ak"])
}

// ===================================================================================
// UpdateSessionState routing across modes
// ===================================================================================

// TestOldData_UpdateSessionState_RoutesToCorrectStorage tests that UpdateSessionState
// routes to the storage where the session actually lives.
func TestOldData_UpdateSessionState_RoutesToCorrectStorage(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()
	ctx := context.Background()

	// Create a zset session
	keyZset := seedZsetSession(t, ctx, redisURL, "app1", "user1", "zset-state-sid", 1, "", "")
	// Create a hashidx session
	keyHashidx := seedHashidxSession(t, ctx, redisURL, "app1", "user1", "hashidx-state-sid", 1, "", "")

	svcL, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)
	defer svcL.Close()

	// Update zset session state
	require.NoError(t, svcL.UpdateSessionState(ctx, keyZset, session.StateMap{"zk": []byte("zv")}))

	// Update hashidx session state
	require.NoError(t, svcL.UpdateSessionState(ctx, keyHashidx, session.StateMap{"hk": []byte("hv")}))

	// Verify both sessions have correct state
	zsetSess, err := svcL.GetSession(ctx, keyZset)
	require.NoError(t, err)
	require.NotNil(t, zsetSess)
	assert.Equal(t, []byte("zv"), zsetSess.State["zk"])

	hashidxSess, err := svcL.GetSession(ctx, keyHashidx)
	require.NoError(t, err)
	require.NotNil(t, hashidxSess)
	assert.Equal(t, []byte("hv"), hashidxSess.State["hk"])
}

// ===================================================================================
// TrimConversations routing across modes
// ===================================================================================

// TestOldData_TrimConversations_RoutesToCorrectStorage tests that TrimConversations
// routes to the correct storage based on session location.
func TestOldData_TrimConversations_RoutesToCorrectStorage(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()
	ctx := context.Background()

	// Helper: create events with RequestID (required for TrimConversations grouping)
	appendEventsWithReqID := func(svc *Service, sess *session.Session, reqID string) {
		t.Helper()
		evt := createTestEvent("e-"+reqID, "", "msg-"+reqID, time.Now(), true)
		evt.RequestID = reqID
		require.NoError(t, svc.AppendEvent(ctx, sess, evt))
		time.Sleep(time.Millisecond)
	}

	// Create a zset session with events that have RequestIDs
	keyZset := session.Key{AppName: "app1", UserID: "user1", SessionID: "zset-trim-sid"}
	svcT, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeTransition))
	require.NoError(t, err)
	sessZ, err := svcT.CreateSession(ctx, keyZset, nil)
	require.NoError(t, err)
	for i := 0; i < 3; i++ {
		appendEventsWithReqID(svcT, sessZ, fmt.Sprintf("req-z-%d", i))
	}
	svcT.Close()

	svcL, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)
	defer svcL.Close()

	// Trim from zset session
	deleted, err := svcL.TrimConversations(ctx, keyZset, WithCount(1))
	require.NoError(t, err)
	assert.NotEmpty(t, deleted, "should have trimmed events from zset session")

	// Create a hashidx session with events that have RequestIDs
	keyHashidx := session.Key{AppName: "app1", UserID: "user1", SessionID: "hashidx-trim-sid"}
	sessH, err := svcL.CreateSession(ctx, keyHashidx, nil)
	require.NoError(t, err)
	for i := 0; i < 3; i++ {
		appendEventsWithReqID(svcL, sessH, fmt.Sprintf("req-h-%d", i))
	}

	// Trim from hashidx session
	deleted, err = svcL.TrimConversations(ctx, keyHashidx, WithCount(1))
	require.NoError(t, err)
	assert.NotEmpty(t, deleted, "should have trimmed events from hashidx session")
}

// ===================================================================================
// DeleteSession and DeleteUserState dual-delete behavior
// ===================================================================================

// TestOldData_DeleteSession_LegacyCleansUpBothStorages tests that deleting a session
// in Legacy mode cleans up both zset and hashidx data including track and summary keys.
func TestOldData_DeleteSession_LegacyCleansUpBothStorages(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()
	ctx := context.Background()

	key := seedZsetSession(t, ctx, redisURL, "app1", "user1", "del-sid", 2, "del-track", "del-summary")

	svcL, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)
	defer svcL.Close()

	// Confirm session exists before delete
	sess, err := svcL.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, sess)

	// Delete
	require.NoError(t, svcL.DeleteSession(ctx, key))

	client := buildRedisClient(t, redisURL)

	// zset session key
	zsetKey := zset.GetSessionStateKey(key)
	exists, err := client.HExists(ctx, zsetKey, key.SessionID).Result()
	require.NoError(t, err)
	assert.False(t, exists, "zset session should be deleted")

	// zset event key
	zsetEvtKey := zset.GetEventKey(key)
	evtExists, err := client.Exists(ctx, zsetEvtKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), evtExists, "zset event key should be deleted")

	// zset track key
	zsetTrackKey := zset.GetTrackKey(key, "del-track")
	trackExists, err := client.Exists(ctx, zsetTrackKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), trackExists, "zset track key should be deleted")

	// hashidx meta key (should not exist since session was in zset)
	hashidxKey := hashidx.GetSessionMetaKey("", key)
	hExists, err := client.Exists(ctx, hashidxKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), hExists, "hashidx meta should not exist")
}

// TestOldData_DeleteUserState_LegacyDeletesBoth tests that deleting user state
// in Legacy mode removes from both storages.
func TestOldData_DeleteUserState_LegacyDeletesBoth(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()
	ctx := context.Background()

	// Write to both storages via Transition mode
	svcT, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeTransition))
	require.NoError(t, err)
	require.NoError(t, svcT.UpdateUserState(ctx,
		session.UserKey{AppName: "app1", UserID: "user1"},
		session.StateMap{"uk": []byte("uv")}))
	svcT.Close()

	svcL, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)
	defer svcL.Close()

	require.NoError(t, svcL.DeleteUserState(ctx, session.UserKey{AppName: "app1", UserID: "user1"}, "uk"))

	client := buildRedisClient(t, redisURL)

	zsetUserKey := zset.GetUserStateKey(session.Key{AppName: "app1", UserID: "user1"})
	zExists, err := client.HExists(ctx, zsetUserKey, "uk").Result()
	require.NoError(t, err)
	assert.False(t, zExists, "deleted from zset")

	hashidxUserKey := hashidx.GetUserStateKey("", "app1", "user1")
	hExists, err := client.HExists(ctx, hashidxUserKey, "uk").Result()
	require.NoError(t, err)
	assert.False(t, hExists, "deleted from hashidx")
}

// ===================================================================================
// Summary compat across modes
// ===================================================================================

// TestOldData_Summary_TransitionWritesZset tests that summary created for
// a Transition-mode session goes to zset summary storage.
func TestOldData_Summary_TransitionWritesZset(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()
	ctx := context.Background()

	key := session.Key{AppName: "app1", UserID: "user1", SessionID: "sum-trans-sid"}

	svcT, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeTransition))
	require.NoError(t, err)
	defer svcT.Close()

	sess, err := svcT.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	evt := createTestEvent("e1", "", "hello", time.Now(), true)
	require.NoError(t, svcT.AppendEvent(ctx, sess, evt))

	// Write summary directly to zset format (simulating summarizer output)
	client := buildRedisClient(t, redisURL)
	sumKey := zset.GetSessionSummaryKey(key)
	summaries := map[string]*session.Summary{
		session.SummaryFilterKeyAllContents: {
			Summary:   "transition summary",
			UpdatedAt: time.Now().UTC(),
		},
	}
	payload, err := json.Marshal(summaries)
	require.NoError(t, err)
	require.NoError(t, client.HSet(ctx, sumKey, key.SessionID, string(payload)).Err())

	// Re-read session: summary should be present
	gotSess, err := svcT.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, gotSess)
	require.NotNil(t, gotSess.Summaries)
	assert.Equal(t, "transition summary", gotSess.Summaries[session.SummaryFilterKeyAllContents].Summary)

	// Verify: no hashidx summary key
	hashidxSumKey := hashidx.GetSessionSummaryKey("", key)
	hSumExists, err := client.Exists(ctx, hashidxSumKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), hSumExists, "should not have hashidx summary key")
}

// TestOldData_Summary_HashidxWriteReadable tests that summary created for
// a hashidx session is readable.
func TestOldData_Summary_HashidxWriteReadable(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()
	ctx := context.Background()

	key := seedHashidxSession(t, ctx, redisURL, "app1", "user1", "sum-hashidx-sid", 2, "", "hashidx summary text")

	svcN, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeNone))
	require.NoError(t, err)
	defer svcN.Close()

	sess, err := svcN.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.NotNil(t, sess.Summaries)
	assert.Equal(t, "hashidx summary text", sess.Summaries[session.SummaryFilterKeyAllContents].Summary)

	text, found := svcN.GetSessionSummaryText(ctx, sess)
	assert.True(t, found)
	assert.Equal(t, "hashidx summary text", text)
}

// TestOldData_Summary_ZsetFallbackViaLegacy tests the end-to-end flow:
// zset session with summary -> Legacy mode reads summary via fallback.
func TestOldData_Summary_ZsetFallbackViaLegacy(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()
	ctx := context.Background()

	key := seedZsetSession(t, ctx, redisURL, "app1", "user1", "sum-fb-sid", 2, "", "v1 summary")

	svcL, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)
	defer svcL.Close()

	sess, err := svcL.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, sess)

	require.NotNil(t, sess.Summaries)
	assert.Equal(t, "v1 summary", sess.Summaries[session.SummaryFilterKeyAllContents].Summary)

	text, found := svcL.GetSessionSummaryText(ctx, sess)
	assert.True(t, found)
	assert.Equal(t, "v1 summary", text)
}

// ===================================================================================
// Full migration path: Transition -> Legacy -> None with all data types
// ===================================================================================

// TestMigration_FullPath_TransitionLegacyNone tests the complete migration path
// with sessions, events, tracks, summary, and user/app state.
func TestMigration_FullPath_TransitionLegacyNone(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()
	ctx := context.Background()

	appName := "app1"
	userID := "user1"
	userKey := session.UserKey{AppName: appName, UserID: userID}

	// Phase 1: Transition mode - simulate old nodes
	svcT, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeTransition))
	require.NoError(t, err)

	keyOld := session.Key{AppName: appName, UserID: userID, SessionID: "phase1-sid"}
	sessOld, err := svcT.CreateSession(ctx, keyOld, session.StateMap{"phase": []byte("1")})
	require.NoError(t, err)

	evt1 := createTestEvent("e1", "", "phase1-msg", time.Now(), true)
	require.NoError(t, svcT.AppendEvent(ctx, sessOld, evt1))

	trackEvt1 := &session.TrackEvent{
		Track:     "audit",
		Payload:   json.RawMessage(`{"action":"create"}`),
		Timestamp: time.Now(),
	}
	require.NoError(t, svcT.AppendTrackEvent(ctx, sessOld, trackEvt1))

	require.NoError(t, svcT.UpdateUserState(ctx, userKey, session.StateMap{"ukey": []byte("phase1")}))
	require.NoError(t, svcT.UpdateAppState(ctx, appName, session.StateMap{"akey": []byte("phase1")}))

	// Write zset summary
	client := buildRedisClient(t, redisURL)
	sumKeyZset := zset.GetSessionSummaryKey(keyOld)
	sumPayload, _ := json.Marshal(map[string]*session.Summary{
		session.SummaryFilterKeyAllContents: {Summary: "phase1 summary", UpdatedAt: time.Now().UTC()},
	})
	require.NoError(t, client.HSet(ctx, sumKeyZset, keyOld.SessionID, string(sumPayload)).Err())
	svcT.Close()

	// Phase 2: Legacy mode - mixed environment
	svcL, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)

	// Old session readable with all data
	oldSess, err := svcL.GetSession(ctx, keyOld)
	require.NoError(t, err)
	require.NotNil(t, oldSess)
	assert.GreaterOrEqual(t, len(oldSess.Events), 1)
	assert.Contains(t, oldSess.Tracks, session.Track("audit"))
	require.NotNil(t, oldSess.Summaries)
	require.Contains(t, oldSess.Summaries, session.SummaryFilterKeyAllContents)
	assert.Equal(t, "phase1 summary", oldSess.Summaries[session.SummaryFilterKeyAllContents].Summary)

	// Create new session in hashidx
	keyNew := session.Key{AppName: appName, UserID: userID, SessionID: "phase2-sid"}
	sessNew, err := svcL.CreateSession(ctx, keyNew, session.StateMap{"phase": []byte("2")})
	require.NoError(t, err)

	evt2 := createTestEvent("e2", "", "phase2-msg", time.Now(), true)
	require.NoError(t, svcL.AppendEvent(ctx, sessNew, evt2))

	trackEvt2 := &session.TrackEvent{
		Track:     "audit",
		Payload:   json.RawMessage(`{"action":"update"}`),
		Timestamp: time.Now(),
	}
	require.NoError(t, svcL.AppendTrackEvent(ctx, sessNew, trackEvt2))

	// Write hashidx summary
	sumKeyHashidx := hashidx.GetSessionSummaryKey("", keyNew)
	sumPayload2, _ := json.Marshal(map[string]*session.Summary{
		session.SummaryFilterKeyAllContents: {Summary: "phase2 summary", UpdatedAt: time.Now().UTC()},
	})
	require.NoError(t, client.Set(ctx, sumKeyHashidx, sumPayload2, 0).Err())

	// ListSessions should see both
	sessions, err := svcL.ListSessions(ctx, userKey)
	require.NoError(t, err)
	assert.Len(t, sessions, 2)

	// User state: Legacy writes only to hashidx
	require.NoError(t, svcL.UpdateUserState(ctx, userKey, session.StateMap{"ukey": []byte("phase2")}))

	// App state is shared
	require.NoError(t, svcL.UpdateAppState(ctx, appName, session.StateMap{"akey": []byte("phase2")}))
	svcL.Close()

	// Phase 3: None mode - migration complete
	svcN, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeNone))
	require.NoError(t, err)
	defer svcN.Close()

	// Only hashidx session visible
	sessions, err = svcN.ListSessions(ctx, userKey)
	require.NoError(t, err)
	assert.Len(t, sessions, 1)
	assert.Equal(t, "phase2-sid", sessions[0].ID)

	// Hashidx session: full data integrity
	newSess, err := svcN.GetSession(ctx, keyNew)
	require.NoError(t, err)
	require.NotNil(t, newSess)
	assert.GreaterOrEqual(t, len(newSess.Events), 1)
	assert.Contains(t, newSess.Tracks, session.Track("audit"))
	require.NotNil(t, newSess.Summaries)
	require.Contains(t, newSess.Summaries, session.SummaryFilterKeyAllContents)
	assert.Equal(t, "phase2 summary", newSess.Summaries[session.SummaryFilterKeyAllContents].Summary)

	// Old zset session is gone
	oldSess2, err := svcN.GetSession(ctx, keyOld)
	require.NoError(t, err)
	assert.Nil(t, oldSess2)

	// User state from hashidx
	states, err := svcN.ListUserStates(ctx, userKey)
	require.NoError(t, err)
	assert.Equal(t, []byte("phase2"), states["ukey"])

	// App state is shared
	appStates, err := svcN.ListAppStates(ctx, appName)
	require.NoError(t, err)
	assert.Equal(t, []byte("phase2"), appStates["akey"])
}

// ===================================================================================
// CreateSession idempotency: same ID across modes
// ===================================================================================

// TestCreateSession_ExistingZset_LegacyReturnsExisting tests that Legacy mode
// returns the existing zset session if CreateSession is called with the same ID.
func TestCreateSession_ExistingZset_LegacyReturnsExisting(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()
	ctx := context.Background()

	key := session.Key{AppName: "app1", UserID: "user1", SessionID: "dup-sid"}

	// Create in Transition mode (zset)
	svcT, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeTransition))
	require.NoError(t, err)
	_, err = svcT.CreateSession(ctx, key, session.StateMap{"from": []byte("zset")})
	require.NoError(t, err)
	svcT.Close()

	// Try to create same session in Legacy mode
	svcL, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)
	defer svcL.Close()

	sess, err := svcL.CreateSession(ctx, key, session.StateMap{"from": []byte("hashidx")})
	require.NoError(t, err)
	require.NotNil(t, sess)
	// Should return existing zset session, not create a new hashidx one
	assert.Equal(t, []byte("zset"), sess.State["from"])
}

// TestCreateSession_ExistingHashidx_TransitionReturnsExisting tests that Transition mode
// returns the existing hashidx session if it already exists.
func TestCreateSession_ExistingHashidx_TransitionReturnsExisting(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()
	ctx := context.Background()

	key := session.Key{AppName: "app1", UserID: "user1", SessionID: "dup-sid"}

	// Create in Legacy mode (hashidx)
	svcL, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)
	_, err = svcL.CreateSession(ctx, key, session.StateMap{"from": []byte("hashidx")})
	require.NoError(t, err)
	svcL.Close()

	// Try to create same session in Transition mode
	svcT, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeTransition))
	require.NoError(t, err)
	defer svcT.Close()

	sess, err := svcT.CreateSession(ctx, key, session.StateMap{"from": []byte("zset")})
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.Equal(t, []byte("hashidx"), sess.State["from"])
}

// ===================================================================================
// None mode isolation: no zset interaction
// ===================================================================================

// TestNoneMode_NoZsetInteraction tests that None mode never reads from or writes to zset.
func TestNoneMode_NoZsetInteraction(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()
	ctx := context.Background()

	// Seed zset data
	client := buildRedisClient(t, redisURL)
	zsetUserKey := zset.GetUserStateKey(session.Key{AppName: "app1", UserID: "user1"})
	require.NoError(t, client.HSet(ctx, zsetUserKey, "zkey", "zval").Err())

	svcN, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeNone))
	require.NoError(t, err)
	defer svcN.Close()

	// Create session
	key := session.Key{AppName: "app1", UserID: "user1", SessionID: "none-sid"}
	sess, err := svcN.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	require.NotNil(t, sess)

	// AppendEvent
	evt := createTestEvent("e1", "", "hello", time.Now(), true)
	require.NoError(t, svcN.AppendEvent(ctx, sess, evt))

	// AppendTrackEvent
	trackEvt := &session.TrackEvent{
		Track:     "t1",
		Payload:   json.RawMessage(`{"a":1}`),
		Timestamp: time.Now(),
	}
	require.NoError(t, svcN.AppendTrackEvent(ctx, sess, trackEvt))

	// UpdateUserState (should not write to zset)
	require.NoError(t, svcN.UpdateUserState(ctx,
		session.UserKey{AppName: "app1", UserID: "user1"},
		session.StateMap{"nkey": []byte("nval")}))

	// ListUserStates should NOT fallback to zset
	states, err := svcN.ListUserStates(ctx, session.UserKey{AppName: "app1", UserID: "user1"})
	require.NoError(t, err)
	assert.Equal(t, []byte("nval"), states["nkey"])
	assert.Nil(t, states["zkey"], "should not see zset user state")

	// Delete session should not affect zset data
	require.NoError(t, svcN.DeleteSession(ctx, key))

	// zset user state should still exist
	val, err := client.HGet(ctx, zsetUserKey, "zkey").Result()
	require.NoError(t, err)
	assert.Equal(t, "zval", val)
}

// ===================================================================================
// AppendEvent to old zset session via Legacy mode (version-tag routing)
// ===================================================================================

// TestAppendEvent_VersionTagRouting_ZsetSession tests that when Legacy mode reads
// a zset session, the version tag routes subsequent AppendEvent to zset storage.
func TestAppendEvent_VersionTagRouting_ZsetSession(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()
	ctx := context.Background()

	key := seedZsetSession(t, ctx, redisURL, "app1", "user1", "ver-tag-sid", 1, "", "")

	svcL, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)
	defer svcL.Close()

	// GetSession sets the version tag in ServiceMeta
	sess, err := svcL.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, sess)

	// Append event - should go to zset via version tag
	evt := createTestEvent("evt-new", "", "new msg", time.Now(), true)
	require.NoError(t, svcL.AppendEvent(ctx, sess, evt))

	// Verify event is in zset
	client := buildRedisClient(t, redisURL)
	zsetEvtKey := zset.GetEventKey(key)
	count, err := client.ZCard(ctx, zsetEvtKey).Result()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, count, int64(2), "should have at least 2 events in zset")

	// Verify no hashidx event data
	hashidxEvtKey := hashidx.GetEventDataKey("", key)
	hExists, err := client.Exists(ctx, hashidxEvtKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), hExists, "no hashidx event data for zset session")
}

// TestAppendEvent_VersionTagRouting_HashidxSession tests that when Legacy mode reads
// a hashidx session, the version tag routes AppendEvent to hashidx storage.
func TestAppendEvent_VersionTagRouting_HashidxSession(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()
	ctx := context.Background()

	key := seedHashidxSession(t, ctx, redisURL, "app1", "user1", "ver-tag-h-sid", 1, "", "")

	svcL, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)
	defer svcL.Close()

	sess, err := svcL.GetSession(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, sess)

	evt := createTestEvent("evt-new", "", "new msg", time.Now(), true)
	require.NoError(t, svcL.AppendEvent(ctx, sess, evt))

	// Verify event is in hashidx
	client := buildRedisClient(t, redisURL)
	hashidxEvtKey := hashidx.GetEventDataKey("", key)
	hExists, err := client.Exists(ctx, hashidxEvtKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(1), hExists, "event should be in hashidx")
}

// ===================================================================================
// DeleteSession -> GetSession returns nil
// ===================================================================================

// TestDeleteSession_GetSessionReturnsNil verifies that after deleting a session,
// GetSession returns nil for both zset and hashidx sessions.
func TestDeleteSession_GetSessionReturnsNil(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()
	ctx := context.Background()

	keyZset := seedZsetSession(t, ctx, redisURL, "app1", "user1", "del-get-z", 2, "trk", "sum")
	keyHashidx := seedHashidxSession(t, ctx, redisURL, "app1", "user1", "del-get-h", 2, "trk", "sum")

	svcL, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)
	defer svcL.Close()

	// Confirm both exist
	sz, err := svcL.GetSession(ctx, keyZset)
	require.NoError(t, err)
	require.NotNil(t, sz)
	sh, err := svcL.GetSession(ctx, keyHashidx)
	require.NoError(t, err)
	require.NotNil(t, sh)

	// Delete both
	require.NoError(t, svcL.DeleteSession(ctx, keyZset))
	require.NoError(t, svcL.DeleteSession(ctx, keyHashidx))

	// GetSession should return nil
	sz2, err := svcL.GetSession(ctx, keyZset)
	require.NoError(t, err)
	assert.Nil(t, sz2, "deleted zset session should return nil from GetSession")

	sh2, err := svcL.GetSession(ctx, keyHashidx)
	require.NoError(t, err)
	assert.Nil(t, sh2, "deleted hashidx session should return nil from GetSession")

	// ListSessions should be empty
	sessions, err := svcL.ListSessions(ctx, session.UserKey{AppName: "app1", UserID: "user1"})
	require.NoError(t, err)
	assert.Empty(t, sessions)
}

// ===================================================================================
// UserState masking: hashidx write masks zset fallback
// ===================================================================================

// TestUserState_HashidxWriteMasksZsetFallback verifies that once Legacy writes
// any key to hashidx UserState, zset UserState keys become invisible
// (because ListUserStates returns hashidx data without merging zset).
func TestUserState_HashidxWriteMasksZsetFallback(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()
	ctx := context.Background()

	userKey := session.UserKey{AppName: "app1", UserID: "user1"}

	// Seed zset user state with key "old_key"
	client := buildRedisClient(t, redisURL)
	zsetUserKey := zset.GetUserStateKey(session.Key{AppName: "app1", UserID: "user1"})
	require.NoError(t, client.HSet(ctx, zsetUserKey, "old_key", "old_val").Err())

	svcL, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)
	defer svcL.Close()

	// Before any hashidx write: should fallback to zset
	states, err := svcL.ListUserStates(ctx, userKey)
	require.NoError(t, err)
	assert.Equal(t, []byte("old_val"), states["old_key"], "should see zset data via fallback")

	// Write a DIFFERENT key to hashidx
	require.NoError(t, svcL.UpdateUserState(ctx, userKey, session.StateMap{"new_key": []byte("new_val")}))

	// Now hashidx is non-empty, zset fallback is skipped
	states, err = svcL.ListUserStates(ctx, userKey)
	require.NoError(t, err)
	assert.Equal(t, []byte("new_val"), states["new_key"], "should see hashidx data")
	assert.Nil(t, states["old_key"], "zset old_key should be masked (not merged)")
}

// ===================================================================================
// GetSessionSummaryText in full migration path
// ===================================================================================

// TestMigration_GetSessionSummaryText_AcrossModes verifies GetSessionSummaryText
// works correctly at each phase of the migration path.
func TestMigration_GetSessionSummaryText_AcrossModes(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()
	ctx := context.Background()

	// Phase 1: Seed zset session with summary
	keyZset := seedZsetSession(t, ctx, redisURL, "app1", "user1", "sumtext-z", 2, "", "zset summary text")
	// Phase 1: Seed hashidx session with summary
	keyHashidx := seedHashidxSession(t, ctx, redisURL, "app1", "user1", "sumtext-h", 2, "", "hashidx summary text")

	// Legacy mode: both summaries readable via GetSessionSummaryText
	svcL, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)

	sessZ, err := svcL.GetSession(ctx, keyZset)
	require.NoError(t, err)
	require.NotNil(t, sessZ)
	textZ, foundZ := svcL.GetSessionSummaryText(ctx, sessZ)
	assert.True(t, foundZ, "Legacy should find zset summary")
	assert.Equal(t, "zset summary text", textZ)

	sessH, err := svcL.GetSession(ctx, keyHashidx)
	require.NoError(t, err)
	require.NotNil(t, sessH)
	textH, foundH := svcL.GetSessionSummaryText(ctx, sessH)
	assert.True(t, foundH, "Legacy should find hashidx summary")
	assert.Equal(t, "hashidx summary text", textH)
	svcL.Close()

	// None mode: only hashidx summary readable
	svcN, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeNone))
	require.NoError(t, err)
	defer svcN.Close()

	sessH2, err := svcN.GetSession(ctx, keyHashidx)
	require.NoError(t, err)
	require.NotNil(t, sessH2)
	textH2, foundH2 := svcN.GetSessionSummaryText(ctx, sessH2)
	assert.True(t, foundH2, "None should find hashidx summary")
	assert.Equal(t, "hashidx summary text", textH2)

	// zset session invisible in None mode
	sessZ2, err := svcN.GetSession(ctx, keyZset)
	require.NoError(t, err)
	assert.Nil(t, sessZ2, "None should not see zset session")
}

// ===================================================================================
// Pre-seeded zset data: full mixed-run with old data injected first
// ===================================================================================

// TestPreSeeded_ZsetData_LegacyFullWorkflow seeds complete zset data (session + events
// + track + summary + userstate + appstate), then runs a full workflow via Legacy mode.
func TestPreSeeded_ZsetData_LegacyFullWorkflow(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()
	ctx := context.Background()

	appName := "app1"
	userID := "user1"
	userKey := session.UserKey{AppName: appName, UserID: userID}

	// Seed: complete zset data
	keyOld := seedZsetSession(t, ctx, redisURL, appName, userID, "pre-zset-sid", 3, "old-track", "old zset summary")

	// Seed: zset user state
	client := buildRedisClient(t, redisURL)
	zsetUserKey := zset.GetUserStateKey(session.Key{AppName: appName, UserID: userID})
	require.NoError(t, client.HSet(ctx, zsetUserKey, "legacy_uk", "legacy_uv").Err())

	// Seed: app state (shared key)
	require.NoError(t, client.HSet(ctx, fmt.Sprintf("appstate:{%s}", appName), "legacy_ak", "legacy_av").Err())

	// Legacy mode: full workflow on pre-seeded data
	svcL, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)
	defer svcL.Close()

	// 1. Read old session with all data
	oldSess, err := svcL.GetSession(ctx, keyOld)
	require.NoError(t, err)
	require.NotNil(t, oldSess, "should read pre-seeded zset session")
	assert.GreaterOrEqual(t, len(oldSess.Events), 3, "should have 3 events")
	require.Contains(t, oldSess.Tracks, session.Track("old-track"))
	assert.Len(t, oldSess.Tracks[session.Track("old-track")].Events, 1)
	assert.JSONEq(t, `{"src":"zset"}`, string(oldSess.Tracks[session.Track("old-track")].Events[0].Payload))
	require.NotNil(t, oldSess.Summaries)
	require.Contains(t, oldSess.Summaries, session.SummaryFilterKeyAllContents)
	assert.Equal(t, "old zset summary", oldSess.Summaries[session.SummaryFilterKeyAllContents].Summary)

	// 2. GetSessionSummaryText on old session
	text, found := svcL.GetSessionSummaryText(ctx, oldSess)
	assert.True(t, found)
	assert.Equal(t, "old zset summary", text)

	// 3. Append event to old zset session
	evt := createTestEvent("evt-new", "", "legacy appended", time.Now(), true)
	require.NoError(t, svcL.AppendEvent(ctx, oldSess, evt))

	// 4. Append track event to old zset session
	trackEvt := &session.TrackEvent{
		Track:     "old-track",
		Payload:   json.RawMessage(`{"src":"legacy-append"}`),
		Timestamp: time.Now(),
	}
	require.NoError(t, svcL.AppendTrackEvent(ctx, oldSess, trackEvt))

	// 5. Re-read and verify
	refreshed, err := svcL.GetSession(ctx, keyOld)
	require.NoError(t, err)
	require.NotNil(t, refreshed)
	assert.GreaterOrEqual(t, len(refreshed.Events), 4, "should have 4 events after append")
	require.Contains(t, refreshed.Tracks, session.Track("old-track"))
	assert.GreaterOrEqual(t, len(refreshed.Tracks[session.Track("old-track")].Events), 2,
		"should have 2 track events after append")

	// 6. Create new hashidx session alongside old zset session
	keyNew := session.Key{AppName: appName, UserID: userID, SessionID: "pre-new-sid"}
	sessNew, err := svcL.CreateSession(ctx, keyNew, session.StateMap{"from": []byte("legacy")})
	require.NoError(t, err)
	require.NotNil(t, sessNew)

	evtNew := createTestEvent("evt-new-2", "", "new session msg", time.Now(), true)
	require.NoError(t, svcL.AppendEvent(ctx, sessNew, evtNew))

	// 7. ListSessions: both visible
	sessions, err := svcL.ListSessions(ctx, userKey)
	require.NoError(t, err)
	assert.Len(t, sessions, 2)

	// 8. User state: fallback to zset (hashidx empty for this user)
	states, err := svcL.ListUserStates(ctx, userKey)
	require.NoError(t, err)
	assert.Equal(t, []byte("legacy_uv"), states["legacy_uk"])

	// 9. App state: shared key
	appStates, err := svcL.ListAppStates(ctx, appName)
	require.NoError(t, err)
	assert.Equal(t, []byte("legacy_av"), appStates["legacy_ak"])

	// 10. UpdateSessionState on old zset session
	require.NoError(t, svcL.UpdateSessionState(ctx, keyOld, session.StateMap{"updated": []byte("yes")}))
	updatedSess, err := svcL.GetSession(ctx, keyOld)
	require.NoError(t, err)
	assert.Equal(t, []byte("yes"), updatedSess.State["updated"])

	// 11. TrimConversations on old zset session
	evtWithReq := createTestEvent("etrim", "", "trim-msg", time.Now(), true)
	evtWithReq.RequestID = "trim-req"
	require.NoError(t, svcL.AppendEvent(ctx, updatedSess, evtWithReq))
	deleted, err := svcL.TrimConversations(ctx, keyOld, WithCount(1))
	require.NoError(t, err)
	assert.NotEmpty(t, deleted)

	// 12. Delete old session, verify gone
	require.NoError(t, svcL.DeleteSession(ctx, keyOld))
	gone, err := svcL.GetSession(ctx, keyOld)
	require.NoError(t, err)
	assert.Nil(t, gone, "deleted session should be nil")
}

// TestPreSeeded_ZsetData_TransitionAndLegacyMixedRun seeds zset data, then runs
// Transition and Legacy services simultaneously against the same data.
func TestPreSeeded_ZsetData_TransitionAndLegacyMixedRun(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()
	ctx := context.Background()

	appName := "app1"
	userID := "user1"

	// Seed: zset session with events + track + summary + user state
	keyOld := seedZsetSession(t, ctx, redisURL, appName, userID, "mixed-zset-sid", 2, "shared-track", "old summary")

	client := buildRedisClient(t, redisURL)
	zsetUserKey := zset.GetUserStateKey(session.Key{AppName: appName, UserID: userID})
	require.NoError(t, client.HSet(ctx, zsetUserKey, "shared_uk", "zset_val").Err())

	// Both services running simultaneously
	svcT, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeTransition))
	require.NoError(t, err)
	defer svcT.Close()

	svcL, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)
	defer svcL.Close()

	// Both can read the old session
	sessT, err := svcT.GetSession(ctx, keyOld)
	require.NoError(t, err)
	require.NotNil(t, sessT, "Transition should read pre-seeded zset session")
	assert.GreaterOrEqual(t, len(sessT.Events), 2)
	require.Contains(t, sessT.Tracks, session.Track("shared-track"))
	require.NotNil(t, sessT.Summaries)
	require.Contains(t, sessT.Summaries, session.SummaryFilterKeyAllContents)
	assert.Equal(t, "old summary", sessT.Summaries[session.SummaryFilterKeyAllContents].Summary)

	sessL, err := svcL.GetSession(ctx, keyOld)
	require.NoError(t, err)
	require.NotNil(t, sessL, "Legacy should read pre-seeded zset session via fallback")

	// Transition appends event to old session
	evtT := createTestEvent("et1", "", "from transition", time.Now(), true)
	require.NoError(t, svcT.AppendEvent(ctx, sessT, evtT))

	// Legacy appends event to old session
	evtL := createTestEvent("el1", "", "from legacy", time.Now(), true)
	require.NoError(t, svcL.AppendEvent(ctx, sessL, evtL))

	// Both should see all events
	sessT2, err := svcT.GetSession(ctx, keyOld)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(sessT2.Events), 4, "Transition should see all events")

	sessL2, err := svcL.GetSession(ctx, keyOld)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(sessL2.Events), 4, "Legacy should see all events")

	// Transition creates new session (goes to zset)
	keyNewT := session.Key{AppName: appName, UserID: userID, SessionID: "mixed-new-t"}
	sessNewT, err := svcT.CreateSession(ctx, keyNewT, nil)
	require.NoError(t, err)
	require.NotNil(t, sessNewT)

	// Legacy creates new session (goes to hashidx)
	keyNewL := session.Key{AppName: appName, UserID: userID, SessionID: "mixed-new-l"}
	sessNewL, err := svcL.CreateSession(ctx, keyNewL, nil)
	require.NoError(t, err)
	require.NotNil(t, sessNewL)

	// Both see all 3 sessions
	sessionsT, err := svcT.ListSessions(ctx, session.UserKey{AppName: appName, UserID: userID})
	require.NoError(t, err)
	assert.Len(t, sessionsT, 3, "Transition should see 3 sessions")

	sessionsL, err := svcL.ListSessions(ctx, session.UserKey{AppName: appName, UserID: userID})
	require.NoError(t, err)
	assert.Len(t, sessionsL, 3, "Legacy should see 3 sessions")

	// Track: Legacy appends to old zset session's track
	trackEvtL := &session.TrackEvent{
		Track:     "shared-track",
		Payload:   json.RawMessage(`{"from":"legacy-mixed"}`),
		Timestamp: time.Now(),
	}
	require.NoError(t, svcL.AppendTrackEvent(ctx, sessL2, trackEvtL))

	// Verify track content
	finalSess, err := svcL.GetSession(ctx, keyOld)
	require.NoError(t, err)
	require.Contains(t, finalSess.Tracks, session.Track("shared-track"))
	trackEvts := finalSess.Tracks[session.Track("shared-track")]
	assert.GreaterOrEqual(t, len(trackEvts.Events), 2, "should have original + appended track events")
}

// TestPreSeeded_ZsetData_FullMigrationWithOldData tests the complete migration path
// starting from pre-seeded zset data through all three phases.
func TestPreSeeded_ZsetData_FullMigrationWithOldData(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()
	ctx := context.Background()

	appName := "app1"
	userID := "user1"
	userKey := session.UserKey{AppName: appName, UserID: userID}

	// Pre-seed: complete zset data (simulates v1 production data)
	keyV1 := seedZsetSession(t, ctx, redisURL, appName, userID, "v1-prod-sid", 5, "v1-track", "v1 production summary")

	client := buildRedisClient(t, redisURL)
	zsetUserKey := zset.GetUserStateKey(session.Key{AppName: appName, UserID: userID})
	require.NoError(t, client.HSet(ctx, zsetUserKey, "pref", "dark").Err())
	require.NoError(t, client.HSet(ctx, fmt.Sprintf("appstate:{%s}", appName), "version", "1.0").Err())

	// Phase 1: Transition mode reads old data, creates new session in zset
	svcT, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeTransition))
	require.NoError(t, err)

	sessV1, err := svcT.GetSession(ctx, keyV1)
	require.NoError(t, err)
	require.NotNil(t, sessV1)
	assert.GreaterOrEqual(t, len(sessV1.Events), 5)
	require.Contains(t, sessV1.Tracks, session.Track("v1-track"))
	require.NotNil(t, sessV1.Summaries)
	require.Contains(t, sessV1.Summaries, session.SummaryFilterKeyAllContents)

	keyT := session.Key{AppName: appName, UserID: userID, SessionID: "trans-new-sid"}
	sessT, err := svcT.CreateSession(ctx, keyT, nil)
	require.NoError(t, err)
	evtT := createTestEvent("et1", "", "transition msg", time.Now(), true)
	require.NoError(t, svcT.AppendEvent(ctx, sessT, evtT))
	svcT.Close()

	// Phase 2: Legacy mode reads both, creates new in hashidx
	svcL, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeLegacy))
	require.NoError(t, err)

	// Old v1 session readable
	sessV1L, err := svcL.GetSession(ctx, keyV1)
	require.NoError(t, err)
	require.NotNil(t, sessV1L)

	// Transition session readable
	sessTL, err := svcL.GetSession(ctx, keyT)
	require.NoError(t, err)
	require.NotNil(t, sessTL)

	// Create hashidx session
	keyL := session.Key{AppName: appName, UserID: userID, SessionID: "legacy-new-sid"}
	sessL, err := svcL.CreateSession(ctx, keyL, nil)
	require.NoError(t, err)
	evtL := createTestEvent("el1", "", "legacy msg", time.Now(), true)
	require.NoError(t, svcL.AppendEvent(ctx, sessL, evtL))

	// Write hashidx summary
	sumKeyH := hashidx.GetSessionSummaryKey("", keyL)
	sumPayload, _ := json.Marshal(map[string]*session.Summary{
		session.SummaryFilterKeyAllContents: {Summary: "legacy summary", UpdatedAt: time.Now().UTC()},
	})
	require.NoError(t, client.Set(ctx, sumKeyH, sumPayload, 0).Err())

	// All 3 sessions visible
	sessions, err := svcL.ListSessions(ctx, userKey)
	require.NoError(t, err)
	assert.Len(t, sessions, 3)

	// User state fallback
	states, err := svcL.ListUserStates(ctx, userKey)
	require.NoError(t, err)
	assert.Equal(t, []byte("dark"), states["pref"])
	svcL.Close()

	// Phase 3: None mode
	svcN, err := NewService(WithRedisClientURL(redisURL), WithEnableUserSessionIndex(true), WithCompatMode(CompatModeNone))
	require.NoError(t, err)
	defer svcN.Close()

	// Only hashidx session visible
	sessions, err = svcN.ListSessions(ctx, userKey)
	require.NoError(t, err)
	assert.Len(t, sessions, 1)
	assert.Equal(t, "legacy-new-sid", sessions[0].ID)

	// Hashidx session with summary
	sessNone, err := svcN.GetSession(ctx, keyL)
	require.NoError(t, err)
	require.NotNil(t, sessNone)
	require.NotNil(t, sessNone.Summaries)
	require.Contains(t, sessNone.Summaries, session.SummaryFilterKeyAllContents)
	assert.Equal(t, "legacy summary", sessNone.Summaries[session.SummaryFilterKeyAllContents].Summary)

	text, found := svcN.GetSessionSummaryText(ctx, sessNone)
	assert.True(t, found)
	assert.Equal(t, "legacy summary", text)

	// v1 and transition sessions invisible
	sessV1None, err := svcN.GetSession(ctx, keyV1)
	require.NoError(t, err)
	assert.Nil(t, sessV1None)

	sessTNone, err := svcN.GetSession(ctx, keyT)
	require.NoError(t, err)
	assert.Nil(t, sessTNone)

	// App state still accessible (shared key)
	appStates, err := svcN.ListAppStates(ctx, appName)
	require.NoError(t, err)
	assert.Equal(t, []byte("1.0"), appStates["version"])
}
