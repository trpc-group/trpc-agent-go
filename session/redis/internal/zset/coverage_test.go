//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package zset

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// ============================================================================
// buildTrackLists error path: invalid _tracks JSON triggers error in
// GetSession -> getTrackEvents -> buildTrackLists
// ============================================================================

func TestCov_BuildTrackLists_InvalidTracks(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "badtracks"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Read current state via go-redis
	stateKey := c.sessionStateKey(key)
	stateJSON, err := rdb.HGet(ctx, stateKey, key.SessionID).Result()
	require.NoError(t, err)

	var state SessionState
	err = json.Unmarshal([]byte(stateJSON), &state)
	require.NoError(t, err)

	// "tracks" is the actual key used by session.TracksFromState (not "_tracks")
	state.State = session.StateMap{"tracks": []byte("invalid json")}
	state.UpdatedAt = time.Now()

	newStateJSON, _ := json.Marshal(state)
	err = rdb.HSet(ctx, stateKey, key.SessionID, string(newStateJSON)).Err()
	require.NoError(t, err)

	// GetSession -> getTrackEvents -> buildTrackLists returns error
	_, err = c.GetSession(ctx, key, 0, time.Time{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "track")
}

// ============================================================================
// DeleteSession error paths
// ============================================================================

func TestCov_DeleteSession_ListTracksError(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "delerr"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	stateKey := c.sessionStateKey(key)
	err = rdb.HSet(ctx, stateKey, key.SessionID, "invalid json").Err()
	require.NoError(t, err)

	err = c.DeleteSession(ctx, key)
	require.Error(t, err)
}

func TestCov_DeleteSession_PipelineError(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "delpipe"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	mr.Close()

	err = c.DeleteSession(ctx, key)
	require.Error(t, err)
}

// ============================================================================
// getTrackEvents error paths via GetSession
// ============================================================================

func TestCov_GetTrackEvents_PipelineError(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "pipeerr"}

	_, err := c.CreateSession(ctx, key, session.StateMap{"_tracks": []byte(`["track1"]`)})
	require.NoError(t, err)

	mr.Close()

	_, err = c.GetSession(ctx, key, 0, time.Time{})
	require.Error(t, err)
}

// ============================================================================
// AppendEvent error paths
// ============================================================================

func TestCov_AppendEvent_PipelineError(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "appenderr"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	mr.Close()

	evt := &event.Event{
		ID:        "e1",
		Timestamp: time.Now(),
		Author:    "test",
	}
	err = c.AppendEvent(ctx, key, evt)
	require.Error(t, err)
}

func TestCov_AppendEvent_NilState(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "nilstate"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	stateKey := c.sessionStateKey(key)
	stateJSON, err := rdb.HGet(ctx, stateKey, key.SessionID).Result()
	require.NoError(t, err)

	var state SessionState
	err = json.Unmarshal([]byte(stateJSON), &state)
	require.NoError(t, err)

	state.State = nil
	state.UpdatedAt = time.Now()

	newStateJSON, _ := json.Marshal(state)
	err = rdb.HSet(ctx, stateKey, key.SessionID, string(newStateJSON)).Err()
	require.NoError(t, err)

	evt := &event.Event{
		ID:         "e1",
		Timestamp:  time.Now(),
		Author:     "test",
		StateDelta: session.StateMap{"key": []byte("value")},
	}
	err = c.AppendEvent(ctx, key, evt)
	require.NoError(t, err)
}

func TestCov_AppendEvent_InvalidStateJSON(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "invappend"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	stateKey := c.sessionStateKey(key)
	err = rdb.HSet(ctx, stateKey, key.SessionID, "invalid json").Err()
	require.NoError(t, err)

	evt := &event.Event{
		ID:        "e1",
		Timestamp: time.Now(),
		Author:    "test",
	}
	err = c.AppendEvent(ctx, key, evt)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}

// ============================================================================
// CreateSummary error path
// ============================================================================

func TestCov_CreateSummary_LuaError(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "sumluaerr"}

	mr.Close()

	sum := &session.Summary{
		Summary:   "test",
		UpdatedAt: time.Now(),
	}
	err := c.CreateSummary(ctx, key, "", sum, time.Hour)
	require.Error(t, err)
}

// ============================================================================
// ListSessions error paths
// ============================================================================

func TestCov_ListSessions_PipelineError(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()

	mr.Close()

	_, err := c.ListSessions(ctx, session.UserKey{AppName: "app", UserID: "u1"}, 0, time.Time{}, false)
	require.Error(t, err)
}

// processSessStateCmdList error path: corrupt session state JSON
// Note: ListSessions swallows this error because len(sessStates)==0 when error occurs.
// So we test via GetSession which uses processSessionStateCmd instead.
func TestCov_GetSession_InvalidStateJSON(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "invstate"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	stateKey := c.sessionStateKey(key)
	err = rdb.HSet(ctx, stateKey, key.SessionID, "invalid json").Err()
	require.NoError(t, err)

	_, err = c.GetSession(ctx, key, 0, time.Time{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}

// getEventsList error: ProcessEventCmd skips invalid JSON (warn+skip),
// so we test the pipeline error path instead.
func TestCov_GetSession_EventsPipelineError(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "evpipe"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Corrupt the event key type to cause pipeline error
	eventKey := c.eventKey(key)
	err = rdb.Set(ctx, eventKey, "not-a-zset", 0).Err()
	require.NoError(t, err)

	_, err = c.GetSession(ctx, key, 0, time.Time{})
	require.Error(t, err)
}

// ProcessEventCmd skip path: invalid event JSON is warn+skipped
func TestCov_GetSession_MalformedEventSkipped(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "skipev"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Add invalid JSON as event member via go-redis
	eventKey := c.eventKey(key)
	err = rdb.ZAdd(ctx, eventKey, redis.Z{
		Score:  float64(time.Now().UnixNano()),
		Member: "not valid json",
	}).Err()
	require.NoError(t, err)

	// GetSession should succeed but skip the malformed event
	sess, err := c.GetSession(ctx, key, 0, time.Time{})
	require.NoError(t, err)
	assert.NotNil(t, sess)
}

// ============================================================================
// UpdateSessionState error paths
// ============================================================================

func TestCov_UpdateSessionState_RedisError(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "updstateerr"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	mr.Close()

	err = c.UpdateSessionState(ctx, key, session.StateMap{"key": []byte("value")})
	require.Error(t, err)
}

func TestCov_UpdateSessionState_InvalidStateJSON(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "invstate2"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	stateKey := c.sessionStateKey(key)
	err = rdb.HSet(ctx, stateKey, key.SessionID, "invalid json").Err()
	require.NoError(t, err)

	err = c.UpdateSessionState(ctx, key, session.StateMap{"key": []byte("value")})
	require.Error(t, err)
}

func TestCov_UpdateSessionState_NilState(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "statenil"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	stateKey := c.sessionStateKey(key)
	rawState, err := rdb.HGet(ctx, stateKey, key.SessionID).Result()
	require.NoError(t, err)

	var state SessionState
	err = json.Unmarshal([]byte(rawState), &state)
	require.NoError(t, err)

	state.State = nil
	state.UpdatedAt = time.Now()

	newState, _ := json.Marshal(state)
	err = rdb.HSet(ctx, stateKey, key.SessionID, string(newState)).Err()
	require.NoError(t, err)

	err = c.UpdateSessionState(ctx, key, session.StateMap{"key": []byte("val")})
	require.NoError(t, err)
}

func TestCov_UpdateSessionState_PipelineError(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "updpipe"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Read valid state first (HGet succeeds), then close Redis before pipeline exec
	stateKey := c.sessionStateKey(key)
	stateJSON, err := rdb.HGet(ctx, stateKey, key.SessionID).Result()
	require.NoError(t, err)
	require.NotEmpty(t, stateJSON)

	// Close Redis after HGet but before the pipeline exec in UpdateSessionState
	// Since we can't intercept between HGet and pipeline, close Redis entirely
	mr.Close()

	err = c.UpdateSessionState(ctx, key, session.StateMap{"key": []byte("val")})
	require.Error(t, err)
}

// ============================================================================
// AppendTrackEvent error paths
// ============================================================================

func TestCov_AppendTrackEvent_PipelineError(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "trackerr2"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	mr.Close()

	te := &session.TrackEvent{
		Track:     "mytrack",
		Payload:   json.RawMessage(`"data"`),
		Timestamp: time.Now(),
	}
	err = c.AppendTrackEvent(ctx, key, te)
	require.Error(t, err)
}

func TestCov_AppendTrackEvent_InvalidStateJSON(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "trackjson"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	stateKey := c.sessionStateKey(key)
	err = rdb.HSet(ctx, stateKey, key.SessionID, "invalid json").Err()
	require.NoError(t, err)

	te := &session.TrackEvent{
		Track:     "mytrack",
		Payload:   json.RawMessage(`"data"`),
		Timestamp: time.Now(),
	}
	err = c.AppendTrackEvent(ctx, key, te)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}

// ============================================================================
// collectTrackQueryResults error path: invalid track event JSON
// ============================================================================

func TestCov_CollectTrackQueryResults_InvalidTrackJSON(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "badtrackjson"}

	// Create session and add a valid track event first
	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	te := &session.TrackEvent{
		Track:     "mytrack",
		Payload:   json.RawMessage(`"data"`),
		Timestamp: time.Now(),
	}
	err = c.AppendTrackEvent(ctx, key, te)
	require.NoError(t, err)

	// Replace the valid track event with invalid JSON using same score range.
	// Use a score that is within the query range (past, not future).
	trackKey := c.trackKey(key, "mytrack")

	// Delete all existing members and add only invalid JSON
	err = rdb.Del(ctx, trackKey).Err()
	require.NoError(t, err)
	err = rdb.ZAdd(ctx, trackKey, redis.Z{
		Score:  float64(time.Now().UnixNano()),
		Member: "invalid track event json",
	}).Err()
	require.NoError(t, err)

	// GetSession -> getTrackEvents -> collectTrackQueryResults should return error
	_, err = c.GetSession(ctx, key, 0, time.Time{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "track")
}

// ============================================================================
// Exists error path
// ============================================================================

func TestCov_Exists_RedisError(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "existserr"}

	mr.Close()

	_, err := c.Exists(ctx, key)
	require.Error(t, err)
}

// ============================================================================
// fetchSessionMeta error path
// ============================================================================

func TestCov_FetchSessionMeta_PipelineError(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "fetcherr"}

	mr.Close()

	_, err := c.GetSession(ctx, key, 0, time.Time{})
	require.Error(t, err)
}

// ============================================================================
// ListSessions with track events count mismatch
// This covers the len(trackEvents) != len(sessStates) check
// ============================================================================

func TestCov_ListSessions_WithValidTracks(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()

	key1 := session.Key{AppName: "app", UserID: "u1", SessionID: "s1"}
	key2 := session.Key{AppName: "app", UserID: "u1", SessionID: "s2"}

	_, err := c.CreateSession(ctx, key1, nil)
	require.NoError(t, err)
	_, err = c.CreateSession(ctx, key2, nil)
	require.NoError(t, err)

	// Add track event to key1
	te := &session.TrackEvent{
		Track:     "t1",
		Payload:   json.RawMessage(`"data"`),
		Timestamp: time.Now(),
	}
	err = c.AppendTrackEvent(ctx, key1, te)
	require.NoError(t, err)

	// ListSessions should work with tracks
	sessions, err := c.ListSessions(ctx, session.UserKey{AppName: "app", UserID: "u1"}, 0, time.Time{}, false)
	require.NoError(t, err)
	assert.Len(t, sessions, 2)
}

// ============================================================================
// ListSessions with corrupt session state (processSessStateCmdList error)
// Note: ListSessions swallows error when len(sessStates)==0, but
// if there are MULTIPLE sessions and only ONE is corrupt, the error
// propagates because len(sessStates) > 0 is not reached.
// Actually, processSessStateCmdList iterates ALL entries and returns
// error on first invalid one, so sessStates will be nil.
// This means the error is always swallowed by ListSessions.
// We test that ListSessions returns empty list gracefully.
// ============================================================================

func TestCov_ListSessions_CorruptStateReturnsEmpty(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "corrupt"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	stateKey := c.sessionStateKey(key)
	err = rdb.HSet(ctx, stateKey, key.SessionID, "invalid json").Err()
	require.NoError(t, err)

	// ListSessions returns empty list (error swallowed by len check)
	sessions, err := c.ListSessions(ctx, session.UserKey{AppName: "app", UserID: "u1"}, 0, time.Time{}, false)
	require.NoError(t, err)
	assert.Empty(t, sessions)
}

// ============================================================================
// getEventsList error paths via ListSessions
// ============================================================================

func TestCov_ListSessions_EventsPipelineError(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "evpipels"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Corrupt the event key type to cause pipeline error
	eventKey := c.eventKey(key)
	err = rdb.Set(ctx, eventKey, "not-a-zset", 0).Err()
	require.NoError(t, err)

	_, err = c.ListSessions(ctx, session.UserKey{AppName: "app", UserID: "u1"}, 0, time.Time{}, false)
	require.Error(t, err)
}

// ============================================================================
// ListSessions with malformed events (warn+skip)
// ============================================================================

func TestCov_ListSessions_MalformedEventsSkipped(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "skipevls"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	eventKey := c.eventKey(key)
	err = rdb.ZAdd(ctx, eventKey, redis.Z{
		Score:  float64(time.Now().UnixNano()),
		Member: "not valid json",
	}).Err()
	require.NoError(t, err)

	sessions, err := c.ListSessions(ctx, session.UserKey{AppName: "app", UserID: "u1"}, 0, time.Time{}, false)
	require.NoError(t, err)
	assert.Len(t, sessions, 1)
}

// ============================================================================
// CreateSession error path
// ============================================================================

func TestCov_CreateSession_PipelineError(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "createerr"}

	mr.Close()

	_, err := c.CreateSession(ctx, key, nil)
	require.Error(t, err)
}

func TestCov_CreateSession_EmptySessionID(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: ""}

	_, err := c.CreateSession(ctx, key, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sessionID is required")
}

// ============================================================================
// listTracksForSession paths
// ============================================================================

func TestCov_ListTracksForSession_NotFound(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "notrack"}

	tracks, err := c.listTracksForSession(ctx, key)
	require.NoError(t, err)
	assert.Nil(t, tracks)
}

func TestCov_ListTracksForSession_InvalidJSON(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "invtrack"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	stateKey := c.sessionStateKey(key)
	err = rdb.HSet(ctx, stateKey, key.SessionID, "invalid json").Err()
	require.NoError(t, err)

	_, err = c.listTracksForSession(ctx, key)
	require.Error(t, err)
}

func TestCov_ListTracksForSession_WithTracks(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "withtracks"}

	// Create session, then add a track event to populate _tracks
	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	te := &session.TrackEvent{
		Track:     "t1",
		Payload:   json.RawMessage(`"data"`),
		Timestamp: time.Now(),
	}
	err = c.AppendTrackEvent(ctx, key, te)
	require.NoError(t, err)

	tracks, err := c.listTracksForSession(ctx, key)
	require.NoError(t, err)
	assert.Len(t, tracks, 1)
	assert.Equal(t, session.Track("t1"), tracks[0])
}

// ============================================================================
// collectTrackQueryResults error path: non-redis.Nil error
// ============================================================================

func TestCov_CollectTrackQueryResults_RedisError(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "trackrediserr"}

	// Create session with track
	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	te := &session.TrackEvent{
		Track:     "mytrack",
		Payload:   json.RawMessage(`"data"`),
		Timestamp: time.Now(),
	}
	err = c.AppendTrackEvent(ctx, key, te)
	require.NoError(t, err)

	// Close Redis to cause error in getTrackEvents -> collectTrackQueryResults
	mr.Close()

	// GetSession -> getTrackEvents -> collectTrackQueryResults should return error
	_, err = c.GetSession(ctx, key, 0, time.Time{})
	require.Error(t, err)
}

// ============================================================================
// GetSummary error paths
// ============================================================================

func TestCov_GetSummary_UnmarshalError(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "sumunmarshal"}

	// Create session first
	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Write invalid JSON to summary key
	sumKey := c.sessionSummaryKey(key)
	err = rdb.HSet(ctx, sumKey, key.SessionID, "invalid json").Err()
	require.NoError(t, err)

	// GetSummary should return error for invalid JSON
	_, err = c.GetSummary(ctx, key)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}

// ============================================================================
// TrimConversations edge cases
// ============================================================================

func TestCov_TrimConversations_NoEvents(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "trimnoev"}

	// Create session without events
	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Trim should return nil when no events exist
	deleted, err := c.TrimConversations(ctx, key, 1)
	require.NoError(t, err)
	assert.Nil(t, deleted)
}

func TestCov_TrimConversations_EventsWithoutRequestID(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "trimnoreq"}

	// Create session
	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Add event without RequestID
	evt := &event.Event{
		ID:        "e1",
		Timestamp: time.Now(),
		Response: &model.Response{
			Done: true,
			Choices: []model.Choice{
				{Message: model.Message{Role: model.RoleUser, Content: "test"}},
			},
		},
		// No RequestID set
	}
	err = c.AppendEvent(ctx, key, evt)
	require.NoError(t, err)

	// Trim should skip events without RequestID
	deleted, err := c.TrimConversations(ctx, key, 1)
	require.NoError(t, err)
	assert.Empty(t, deleted)
}

// ============================================================================
// getEventsList with session event limit from config
// ============================================================================

func TestCov_GetEventsList_ConfigEventLimit(t *testing.T) {
	cfg := Config{
		SessionTTL:        time.Hour,
		SessionEventLimit: 2, // limit to 2 events
	}
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, cfg)
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "evlimit"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Add 5 events
	baseTime := time.Now()
	for i := 0; i < 5; i++ {
		evt := &event.Event{
			ID:        fmt.Sprintf("e%d", i),
			Timestamp: baseTime.Add(time.Duration(i) * time.Second),
			Response: &model.Response{
				Done: true,
				Choices: []model.Choice{
					{Message: model.Message{Role: model.RoleUser, Content: fmt.Sprintf("msg%d", i)}},
				},
			},
		}
		err = c.AppendEvent(ctx, key, evt)
		require.NoError(t, err)
	}

	// GetSession with limit=0 should use config limit (2)
	sess, err := c.GetSession(ctx, key, 0, time.Time{})
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.Len(t, sess.Events, 2)
}

// ============================================================================
// CreateSession with nil state value
// ============================================================================

func TestCov_CreateSession_NilStateValue(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "nilstateval"}

	// Create session with nil state value
	state := session.StateMap{
		"normal": []byte("value"),
		"nilkey": nil,
	}
	sess, err := c.CreateSession(ctx, key, state)
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.Equal(t, []byte("value"), sess.State["normal"])
	assert.Nil(t, sess.State["nilkey"])
}

// ============================================================================
// TrimConversations: corrupt event JSON in ZSet triggers unmarshal error
// ============================================================================

func TestCov_TrimConversations_CorruptEventJSON(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "trim-corrupt"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Inject malformed JSON directly into the event ZSet
	eventKey := c.eventKey(key)
	err = rdb.ZAdd(ctx, eventKey, redis.Z{
		Score:  float64(time.Now().UnixNano()),
		Member: "not valid json at all",
	}).Err()
	require.NoError(t, err)

	// TrimConversations should return error when event JSON can't be parsed
	_, err = c.TrimConversations(ctx, key, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "trim events: unmarshal event")
}

// ============================================================================
// ListSessions: getTrackEvents pipeline error path
// ============================================================================

func TestCov_ListSessions_TrackEventsPipelineError(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "tkpipeerr"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Append a track event so session state has tracks populated
	te := &session.TrackEvent{
		Track:     "mytrack",
		Payload:   json.RawMessage(`"data"`),
		Timestamp: time.Now(),
	}
	err = c.AppendTrackEvent(ctx, key, te)
	require.NoError(t, err)

	// Corrupt the track ZSet key type so ZRevRangeByScore fails in pipeline
	trackKey := c.trackKey(key, "mytrack")
	err = rdb.Del(ctx, trackKey).Err()
	require.NoError(t, err)
	// Set as string type instead of ZSet to cause WRONGTYPE error
	err = rdb.Set(ctx, trackKey, "not-a-zset", 0).Err()
	require.NoError(t, err)

	_, err = c.ListSessions(ctx, session.UserKey{AppName: "app", UserID: "u1"}, 0, time.Time{}, false)
	require.Error(t, err)
}

// ============================================================================
// ListSessions: with limit > 0 covers the getTrackEvents limit logic
// ============================================================================

func TestCov_ListSessions_WithTrackLimit(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "tracklimit"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// Append two track events
	for i := 0; i < 3; i++ {
		te := &session.TrackEvent{
			Track:     "mytrack",
			Payload:   json.RawMessage(`"data"`),
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
		}
		err = c.AppendTrackEvent(ctx, key, te)
		require.NoError(t, err)
	}

	// ListSessions with limit=2 exercises the limit > 0 branch in getTrackEvents
	sessions, err := c.ListSessions(ctx, session.UserKey{AppName: "app", UserID: "u1"}, 2, time.Time{}, false)
	require.NoError(t, err)
	require.Len(t, sessions, 1)
}

// ============================================================================
// ListAppStates and ListUserStates: connection error paths
// ============================================================================

func TestCov_ListAppStates_ConnectionError(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()

	mr.Close()

	_, err := c.ListAppStates(ctx, "myapp")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list app states")
}

func TestCov_ListUserStates_ConnectionError(t *testing.T) {
	mr, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()

	mr.Close()

	_, err := c.ListUserStates(ctx, session.UserKey{AppName: "myapp", UserID: "u1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list user states")
}

// ============================================================================
// CreateSummary: lua error when Redis is unavailable
// ============================================================================

func TestCov_CreateSummary_ExpireTTLWithConnection(t *testing.T) {
	_, rdb := setupMiniredis(t)
	c := NewClient(rdb, defaultConfig())
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "sum-expire"}

	_, err := c.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	// CreateSummary with TTL > 0 exercises the Expire branch
	err = c.CreateSummary(ctx, key, "fk1", &session.Summary{Summary: "hello", UpdatedAt: time.Now()}, time.Hour)
	require.NoError(t, err)
}
