//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// ---------------------------------------------------------------------------
// normalizeEvents
// ---------------------------------------------------------------------------

func TestNormalizeEvents_StripsIDAndTimestamp(t *testing.T) {
	ts := time.Now().UTC()
	evt := event.Event{
		Response: &model.Response{
			ID:        "resp-1",
			Object:    model.ObjectTypeChatCompletion,
			Done:      true,
			Timestamp: ts,
			Choices:   []model.Choice{{Index: 0, Message: model.Message{Role: model.RoleUser, Content: "hello"}}},
		},
		Author:    "user",
		Branch:    "b",
		FilterKey: "b",
		ID:        "evt-id",
		Timestamp: ts,
		Version:   event.CurrentVersion,
	}

	out := normalizeEvents([]event.Event{evt})
	require.Len(t, out, 1)

	// ID, timestamp, created must be stripped — Response is embedded
	// inline (no json tag), so these sit at the top level.
	assert.NotContains(t, out[0], "id")
	assert.NotContains(t, out[0], "timestamp")
	assert.NotContains(t, out[0], "created")

	// Embedded Response fields (object, choices) must be preserved.
	assert.Contains(t, out[0], "object")
	assert.Contains(t, out[0], "choices")
}

func TestNormalizeEvents_PreservesContent(t *testing.T) {
	evt := event.Event{
		Response: &model.Response{
			Object:  model.ObjectTypeChatCompletion,
			Done:    true,
			Choices: []model.Choice{{Index: 0, Message: model.Message{Role: model.RoleAssistant, Content: "reply"}}},
		},
		Author:    "agent",
		Branch:    "chat",
		FilterKey: "chat",
		Tag:       "important",
		Version:   event.CurrentVersion,
	}

	out := normalizeEvents([]event.Event{evt})
	require.Len(t, out, 1)
	assert.Equal(t, "agent", out[0]["author"])
	assert.Equal(t, "chat", out[0]["branch"])
	assert.Equal(t, "chat", out[0]["filterKey"])
	assert.Equal(t, "important", out[0]["tag"])
}

func TestNormalizeEvents_NilSlice(t *testing.T) {
	out := normalizeEvents(nil)
	assert.Len(t, out, 0)
}

// ---------------------------------------------------------------------------
// normalizeState
// ---------------------------------------------------------------------------

func TestNormalizeState_JSONValue(t *testing.T) {
	state := session.StateMap{
		"config": []byte(`{"theme":"dark","enabled":true}`),
	}
	out := normalizeState(state)
	require.Contains(t, out, "config")

	parsed, ok := out["config"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "dark", parsed["theme"])
	assert.Equal(t, true, parsed["enabled"])
}

func TestNormalizeState_StringValue(t *testing.T) {
	state := session.StateMap{
		"name": []byte("hello world"),
	}
	out := normalizeState(state)
	assert.Equal(t, "hello world", out["name"])
}

func TestNormalizeState_NilValue(t *testing.T) {
	state := session.StateMap{
		"empty": nil,
	}
	out := normalizeState(state)
	assert.Nil(t, out["empty"])
}

func TestNormalizeState_EmptyMap(t *testing.T) {
	out := normalizeState(session.StateMap{})
	assert.Len(t, out, 0)
}

// ---------------------------------------------------------------------------
// normalizeMemories
// ---------------------------------------------------------------------------

func TestNormalizeMemories_SortedByKey(t *testing.T) {
	now := time.Now().UTC()
	entries := []*memory.Entry{
		{ID: "b", AppName: "app", UserID: "u", Memory: &memory.Memory{Memory: "beta", Topics: []string{"t2"}}},
		{ID: "a", AppName: "app", UserID: "u", Memory: &memory.Memory{Memory: "alpha", Topics: []string{"t1"}, EventTime: &now}},
	}

	out := normalizeMemories(entries)
	require.Len(t, out, 2)
	// Sorted by Key (JSON-marshalled content), "alpha" < "beta".
	assert.Equal(t, "alpha", out[0].Content)
	assert.Equal(t, "beta", out[1].Content)
	assert.Equal(t, "a", out[0].RawID)
	assert.Equal(t, "b", out[1].RawID)
}

func TestNormalizeMemories_NilEntry(t *testing.T) {
	entries := []*memory.Entry{nil}
	out := normalizeMemories(entries)
	assert.Len(t, out, 0)
}

func TestNormalizeMemories_EmptySlice(t *testing.T) {
	out := normalizeMemories(nil)
	assert.Len(t, out, 0)
}

// ---------------------------------------------------------------------------
// normalizeSummaries
// ---------------------------------------------------------------------------

func TestNormalizeSummaries_BoundaryFields(t *testing.T) {
	now := time.Now().UTC()
	boundary := &session.SummaryBoundary{
		Version:   2,
		FilterKey: "chat",
	}
	sess := &session.Session{
		Summaries: map[string]*session.Summary{
			"chat": {
				Summary:   "a summary",
				Topics:    []string{"t1", "t0"},
				UpdatedAt: now,
				Boundary:  boundary,
			},
		},
	}
	out := normalizeSummaries(sess)
	require.Contains(t, out, "chat")
	snap := out["chat"]
	assert.Equal(t, "a summary", snap.Summary)
	assert.Equal(t, []string{"t0", "t1"}, snap.Topics)
	require.NotNil(t, snap.Boundary)
	assert.Equal(t, 2, snap.Boundary.Version)
	assert.Equal(t, "chat", snap.Boundary.FilterKey)
}

func TestNormalizeSummaries_UpdatedAtNonZero(t *testing.T) {
	sess := &session.Session{
		Summaries: map[string]*session.Summary{
			"k": {Summary: "s", UpdatedAt: time.Now().UTC()},
		},
	}
	out := normalizeSummaries(sess)
	assert.True(t, out["k"].UpdatedAtNonZero)
}

func TestNormalizeSummaries_NilEntry(t *testing.T) {
	sess := &session.Session{
		Summaries: map[string]*session.Summary{
			"k": nil,
		},
	}
	out := normalizeSummaries(sess)
	assert.NotContains(t, out, "k")
}

// ---------------------------------------------------------------------------
// normalizeTracks
// ---------------------------------------------------------------------------

func TestNormalizeTracks_SortedByName(t *testing.T) {
	sess := &session.Session{
		Tracks: map[session.Track]*session.TrackEvents{
			"zzz": {Track: "zzz"},
			"aaa": {Track: "aaa"},
		},
	}
	out := normalizeTracks(sess)
	require.Len(t, out, 2)
	assert.Equal(t, "aaa", out[0].Name)
	assert.Equal(t, "zzz", out[1].Name)
}

func TestNormalizeTracks_TrackEventsPayload(t *testing.T) {
	payload := json.RawMessage(`{"status":"done","ms":123}`)
	sess := &session.Session{
		Tracks: map[session.Track]*session.TrackEvents{
			"exec": {
				Track: "exec",
				Events: []session.TrackEvent{
					{Payload: payload},
				},
			},
		},
	}
	out := normalizeTracks(sess)
	require.Len(t, out, 1)
	require.Len(t, out[0].Events, 1)
	// Payload should be a decoded map.
	parsed, ok := out[0].Events[0].Payload.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "done", parsed["status"])
}

// ---------------------------------------------------------------------------
// CaptureSnapshot
// ---------------------------------------------------------------------------

func TestCaptureSnapshot_NilSession(t *testing.T) {
	snap := CaptureSnapshot("test", nil, nil)
	assert.Equal(t, "test", snap.BackendName)
	assert.Empty(t, snap.State)
	assert.Empty(t, snap.Memories)
	assert.Empty(t, snap.Summaries)
}

func TestCaptureSnapshot_SessionMetadata(t *testing.T) {
	sess := &session.Session{
		ID:      "sid",
		AppName: "app",
		UserID:  "uid",
	}
	snap := CaptureSnapshot("b", sess, nil)
	assert.Equal(t, "sid", snap.Session.ID)
	assert.Equal(t, "app", snap.Session.App)
	assert.Equal(t, "uid", snap.Session.UserID)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func TestSortedStrings(t *testing.T) {
	assert.Nil(t, sortedStrings(nil))
	assert.Equal(t, []string{"a", "b", "c"}, sortedStrings([]string{"c", "a", "b"}))
}

func TestTimeToString(t *testing.T) {
	assert.Empty(t, timeToString(time.Time{}))
	tm := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	assert.Equal(t, "2026-07-18T10:00:00Z", timeToString(tm))
}
