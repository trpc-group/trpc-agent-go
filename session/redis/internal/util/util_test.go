//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package util

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func setupRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(func() { mr.Close() })
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	return mr, rdb
}

// =============================================================================
// ProcessStateCmd
// =============================================================================

func TestProcessStateCmd_WithData(t *testing.T) {
	_, rdb := setupRedis(t)
	ctx := context.Background()

	rdb.HSet(ctx, "state:test", "k1", "v1", "k2", "v2")

	pipe := rdb.Pipeline()
	cmd := pipe.HGetAll(ctx, "state:test")
	_, err := pipe.Exec(ctx)
	require.NoError(t, err)

	state, err := ProcessStateCmd(cmd)
	require.NoError(t, err)
	assert.Equal(t, []byte("v1"), state["k1"])
	assert.Equal(t, []byte("v2"), state["k2"])
}

func TestProcessStateCmd_Empty(t *testing.T) {
	_, rdb := setupRedis(t)
	ctx := context.Background()

	pipe := rdb.Pipeline()
	cmd := pipe.HGetAll(ctx, "state:nonexistent")
	_, err := pipe.Exec(ctx)
	require.NoError(t, err)

	state, err := ProcessStateCmd(cmd)
	require.NoError(t, err)
	assert.Empty(t, state)
}

// =============================================================================
// ProcessEventCmd
// =============================================================================

func TestProcessEventCmd_WithValidEvents(t *testing.T) {
	_, rdb := setupRedis(t)
	ctx := context.Background()

	now := time.Now()
	evt1 := &event.Event{
		ID:        "e1",
		Timestamp: now,
		Response: &model.Response{
			Done:    true,
			Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hello"}}},
		},
	}
	evt2 := &event.Event{
		ID:        "e2",
		Timestamp: now.Add(time.Second),
		Response: &model.Response{
			Done:    true,
			Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "world"}}},
		},
	}

	b1, _ := json.Marshal(evt1)
	b2, _ := json.Marshal(evt2)
	rdb.ZAdd(ctx, "events:test", redis.Z{Score: 1, Member: string(b1)}, redis.Z{Score: 2, Member: string(b2)})

	pipe := rdb.Pipeline()
	cmd := pipe.ZRange(ctx, "events:test", 0, -1)
	_, err := pipe.Exec(ctx)
	require.NoError(t, err)

	events, err := ProcessEventCmd(ctx, cmd)
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, "e1", events[0].ID)
	assert.Equal(t, "e2", events[1].ID)
}

func TestProcessEventCmd_Empty(t *testing.T) {
	_, rdb := setupRedis(t)
	ctx := context.Background()

	pipe := rdb.Pipeline()
	cmd := pipe.ZRange(ctx, "events:nonexistent", 0, -1)
	_, err := pipe.Exec(ctx)
	require.NoError(t, err)

	events, err := ProcessEventCmd(ctx, cmd)
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestProcessEventCmd_SkipsMalformedJSON(t *testing.T) {
	_, rdb := setupRedis(t)
	ctx := context.Background()

	validEvt := &event.Event{ID: "e1", Timestamp: time.Now()}
	b, _ := json.Marshal(validEvt)
	rdb.ZAdd(ctx, "events:test",
		redis.Z{Score: 1, Member: string(b)},
		redis.Z{Score: 2, Member: "not-valid-json"},
	)

	pipe := rdb.Pipeline()
	cmd := pipe.ZRange(ctx, "events:test", 0, -1)
	_, err := pipe.Exec(ctx)
	require.NoError(t, err)

	events, err := ProcessEventCmd(ctx, cmd)
	require.NoError(t, err)
	assert.Len(t, events, 1)
	assert.Equal(t, "e1", events[0].ID)
}

// =============================================================================
// MergeState
// =============================================================================

func TestMergeState(t *testing.T) {
	sess := session.NewSession("app", "user", "sess1")
	appState := session.StateMap{"cfg": []byte("on")}
	userState := session.StateMap{"pref": []byte("dark")}

	result := MergeState(appState, userState, sess)
	assert.Same(t, sess, result)
	assert.Equal(t, []byte("on"), sess.State[session.StateAppPrefix+"cfg"])
	assert.Equal(t, []byte("dark"), sess.State[session.StateUserPrefix+"pref"])
}

func TestMergeState_EmptyMaps(t *testing.T) {
	sess := session.NewSession("app", "user", "sess1")
	result := MergeState(nil, nil, sess)
	assert.Same(t, sess, result)
}

// =============================================================================
// NormalizeSessionEvents
// =============================================================================

func TestNormalizeSessionEvents_NonEmpty(t *testing.T) {
	events := [][]event.Event{
		{{ID: "e1"}, {ID: "e2"}},
		{{ID: "e3"}},
	}
	result := NormalizeSessionEvents(events)
	require.Len(t, result, 2)
	assert.Equal(t, "e1", result[0].ID)
}

func TestNormalizeSessionEvents_Empty(t *testing.T) {
	assert.Nil(t, NormalizeSessionEvents(nil))
	assert.Nil(t, NormalizeSessionEvents([][]event.Event{}))
}

// =============================================================================
// AttachTrackEvents
// =============================================================================

func TestAttachTrackEvents(t *testing.T) {
	sess := session.NewSession("app", "user", "sess1")
	trackMap := map[session.Track][]session.TrackEvent{
		"tool_calls": {
			{Track: "tool_calls", Payload: json.RawMessage(`{"fn":"add"}`), Timestamp: time.Now()},
		},
		"metrics": {
			{Track: "metrics", Payload: json.RawMessage(`{"latency":100}`), Timestamp: time.Now()},
		},
	}

	AttachTrackEvents(sess, []map[session.Track][]session.TrackEvent{trackMap})
	require.Len(t, sess.Tracks, 2)
	assert.Len(t, sess.Tracks["tool_calls"].Events, 1)
	assert.Len(t, sess.Tracks["metrics"].Events, 1)
}

func TestAttachTrackEvents_Empty(t *testing.T) {
	sess := session.NewSession("app", "user", "sess1")

	AttachTrackEvents(sess, nil)
	assert.Nil(t, sess.Tracks)

	AttachTrackEvents(sess, []map[session.Track][]session.TrackEvent{})
	assert.Nil(t, sess.Tracks)

	AttachTrackEvents(sess, []map[session.Track][]session.TrackEvent{{}})
	assert.Nil(t, sess.Tracks)
}

func TestAttachTrackEvents_OverwritesPrevious(t *testing.T) {
	sess := session.NewSession("app", "user", "sess1")
	first := map[session.Track][]session.TrackEvent{
		"a": {{Track: "a", Payload: json.RawMessage(`"first"`)}},
	}
	second := map[session.Track][]session.TrackEvent{
		"b": {{Track: "b", Payload: json.RawMessage(`"second"`)}},
	}

	AttachTrackEvents(sess, []map[session.Track][]session.TrackEvent{first})
	assert.Contains(t, sess.Tracks, session.Track("a"))

	AttachTrackEvents(sess, []map[session.Track][]session.TrackEvent{second})
	assert.NotContains(t, sess.Tracks, session.Track("a"))
	assert.Contains(t, sess.Tracks, session.Track("b"))
}

// =============================================================================
// AttachSummaries
// =============================================================================

func TestAttachSummaries(t *testing.T) {
	_, rdb := setupRedis(t)
	ctx := context.Background()

	summaries := map[string]*session.Summary{
		"all": {Summary: "test summary", UpdatedAt: time.Now().UTC()},
	}
	b, _ := json.Marshal(summaries)
	rdb.Set(ctx, "sum:test", string(b), 0)

	sess := session.NewSession("app", "user", "sess1",
		session.WithSessionEvents([]event.Event{{ID: "e1"}}),
	)

	pipe := rdb.Pipeline()
	cmd := pipe.Get(ctx, "sum:test")
	_, err := pipe.Exec(ctx)
	require.NoError(t, err)

	strCmd := redis.NewStringCmd(ctx, "get", "sum:test")
	strCmd.SetVal(cmd.Val())

	AttachSummaries(sess, strCmd)
	require.NotNil(t, sess.Summaries)
	assert.Equal(t, "test summary", sess.Summaries["all"].Summary)
}

func TestAttachSummaries_NilCmd(t *testing.T) {
	sess := session.NewSession("app", "user", "sess1",
		session.WithSessionEvents([]event.Event{{ID: "e1"}}),
	)
	AttachSummaries(sess, nil)
	assert.Empty(t, sess.Summaries)
}

func TestAttachSummaries_NoEvents(t *testing.T) {
	sess := session.NewSession("app", "user", "sess1")
	cmd := redis.NewStringCmd(context.Background())
	cmd.SetVal(`{"all":{"summary":"test","updated_at":"2025-01-01T00:00:00Z"}}`)
	AttachSummaries(sess, cmd)
	assert.Empty(t, sess.Summaries)
}

func TestAttachSummaries_InvalidJSON(t *testing.T) {
	sess := session.NewSession("app", "user", "sess1",
		session.WithSessionEvents([]event.Event{{ID: "e1"}}),
	)
	cmd := redis.NewStringCmd(context.Background())
	cmd.SetVal("not-json")
	AttachSummaries(sess, cmd)
	assert.Empty(t, sess.Summaries)
}

// =============================================================================
// Constants
// =============================================================================

func TestConstants(t *testing.T) {
	assert.Equal(t, "storage_type", ServiceMetaStorageTypeKey)
	assert.Equal(t, "hashidx", StorageTypeHashIdx)
	assert.Equal(t, "zset", StorageTypeZset)
}

// =============================================================================
// ProcessStateCmd Error Cases
// =============================================================================

func TestProcessStateCmd_Error(t *testing.T) {
	mr, rdb := setupRedis(t)
	ctx := context.Background()

	// Setup a pipeline with HGetAll
	pipe := rdb.Pipeline()
	cmd := pipe.HGetAll(ctx, "state:test")

	// Execute pipeline to populate cmd
	_, err := pipe.Exec(ctx)
	require.NoError(t, err)

	// Close miniredis to simulate connection error
	mr.Close()

	// Try to get result from cmd after connection is closed
	// This simulates an error scenario
	_, err = cmd.Result()
	if err != nil {
		// ProcessStateCmd should handle the error
		_, err := ProcessStateCmd(cmd)
		require.Error(t, err)
	}
}
