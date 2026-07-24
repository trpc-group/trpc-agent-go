//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestComparator_EqualResults(t *testing.T) {
	c := NewComparator()
	now := time.Now().UTC()

	a := &BackendResult{
		BackendName: "A",
		Session: &session.Session{
			AppName:   "test",
			UserID:    "u1",
			Events:    nil,
			State:     session.StateMap{"k": []byte("v")},
			CreatedAt: now,
		},
	}
	b := &BackendResult{
		BackendName: "B",
		Session: &session.Session{
			AppName:   "test",
			UserID:    "u1",
			Events:    nil,
			State:     session.StateMap{"k": []byte("v")},
			CreatedAt: now,
		},
	}

	diffs := c.Compare("case1", a, b)
	if len(diffs) != 0 {
		t.Errorf("expected 0 diffs for equal results, got %d: %v", len(diffs), diffs)
	}
}

func TestComparator_DifferentStrings(t *testing.T) {
	c := NewComparator()
	a := &BackendResult{BackendName: "A"}
	b := &BackendResult{BackendName: "B"}

	diffs := c.compareString("case", a, b, "field", "hello", "world")
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d", len(diffs))
	}
	if diffs[0].Baseline != "hello" || diffs[0].Actual != "world" {
		t.Errorf("expected baseline=hello actual=world, got baseline=%v actual=%v", diffs[0].Baseline, diffs[0].Actual)
	}
}

func TestComparator_TimeTolerance(t *testing.T) {
	c := NewComparator()
	now := time.Now().UTC()

	// Within 1s: no diff.
	diffs := c.compareTime("case", &BackendResult{BackendName: "A"}, &BackendResult{BackendName: "B"},
		"ts", now, now.Add(500*time.Millisecond))
	if len(diffs) != 0 {
		t.Errorf("expected 0 diffs for 500ms delta, got %d", len(diffs))
	}

	// Beyond 1s: diff.
	diffs = c.compareTime("case", &BackendResult{BackendName: "A"}, &BackendResult{BackendName: "B"},
		"ts", now, now.Add(2*time.Second))
	if len(diffs) != 1 {
		t.Errorf("expected 1 diff for 2s delta, got %d", len(diffs))
	}
}

func TestComparator_FloatTolerance(t *testing.T) {
	c := NewComparator()

	diffs := c.compareFloat("case", &BackendResult{BackendName: "A"}, &BackendResult{BackendName: "B"},
		"score", 0.5, 0.505)
	if len(diffs) != 0 {
		t.Errorf("expected 0 diffs for 0.005 delta, got %d", len(diffs))
	}

	diffs = c.compareFloat("case", &BackendResult{BackendName: "A"}, &BackendResult{BackendName: "B"},
		"score", 0.5, 0.6)
	if len(diffs) != 1 {
		t.Errorf("expected 1 diff for 0.1 delta, got %d", len(diffs))
	}
}

func TestComparator_NilResults(t *testing.T) {
	c := NewComparator()
	diffs := c.Compare("case", nil, nil)
	if len(diffs) != 1 {
		t.Errorf("expected 1 diff for nil results, got %d", len(diffs))
	}
}

func TestComparator_StateMismatch(t *testing.T) {
	c := NewComparator()
	a := &BackendResult{
		BackendName: "A",
		Session: &session.Session{
			State: session.StateMap{"k": []byte("v1")},
		},
	}
	b := &BackendResult{
		BackendName: "B",
		Session: &session.Session{
			State: session.StateMap{"k": []byte("v2")},
		},
	}
	diffs := c.Compare("case", a, b)
	found := false
	for _, d := range diffs {
		if d.FieldPath == "session.state.k" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diff at session.state.k")
	}
}

func TestComparator_TrackMismatch(t *testing.T) {
	c := NewComparator()
	a := &BackendResult{
		BackendName: "InMemory",
		Session: &session.Session{
			Tracks: map[session.Track]*session.TrackEvents{
				"tool_exec": {Track: "tool_exec", Events: []session.TrackEvent{
					{Track: "tool_exec", Timestamp: time.Now().UTC()},
				}},
			},
		},
	}
	b := &BackendResult{
		BackendName: "SQLite",
		Session: &session.Session{
			Tracks: map[session.Track]*session.TrackEvents{
				"tool_exec": {Track: "tool_exec", Events: []session.TrackEvent{
					{Track: "tool_exec", Timestamp: time.Now().UTC()},
				}},
			},
		},
	}
	diffs := c.Compare("case", a, b)
	for _, d := range diffs {
		if !d.AllowedDiff {
			t.Errorf("unexpected unallowed diff: %s (%s)", d.FieldPath, d.DiffReason)
		}
	}
}
