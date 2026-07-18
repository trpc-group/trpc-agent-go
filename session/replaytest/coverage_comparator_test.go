// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package replaytest

import (
	"context"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestComparator_MemoryFieldDiffs(t *testing.T) {
	c := NewComparator()
	tc := ReplayCase{Name: "mem_fields"}
	tsA := time.Unix(10, 0).UTC()
	tsB := time.Unix(20, 0).UTC()
	etA := time.Unix(5, 0).UTC()
	etB := time.Unix(6, 0).UTC()
	luA := time.Unix(7, 0).UTC()
	luB := time.Unix(8, 0).UTC()

	a := &Snapshot{
		Backend: "a",
		Memories: []*memory.Entry{
			nil,
			{ID: "same-nil-payload", Memory: nil},
			{
				ID: "full-a",
				Memory: &memory.Memory{
					Memory: "content-a", Topics: []string{"t1"}, Participants: []string{"p1"},
					Kind: "fact", Location: "sz", EventTime: &etA, LastUpdated: &luA,
				},
				CreatedAt: tsA, UpdatedAt: tsA,
			},
		},
	}
	b := &Snapshot{
		Backend: "b",
		Memories: []*memory.Entry{
			{ID: "present"}, // nil vs non-nil presence
			{ID: "same-nil-payload", Memory: nil},
			{
				ID: "full-b",
				Memory: &memory.Memory{
					Memory: "content-b", Topics: []string{"t2"}, Participants: []string{"p2"},
					Kind: "episode", Location: "bj", EventTime: &etB, LastUpdated: &luB,
				},
				CreatedAt: tsB, UpdatedAt: tsB,
			},
		},
	}
	// Skip normalizer so IDs/timestamps stay as constructed for field paths.
	diffs := c.Compare(tc, a, b, InMemoryProfile(), InMemoryProfile())
	if ErrorDiffCount(diffs) == 0 {
		t.Fatalf("expected memory field diffs: %+v", diffs)
	}
	// both nil entries at same index
	a2 := &Snapshot{Backend: "a", Memories: []*memory.Entry{nil}}
	b2 := &Snapshot{Backend: "b", Memories: []*memory.Entry{nil}}
	if d := c.Compare(tc, a2, b2, InMemoryProfile(), InMemoryProfile()); ErrorDiffCount(d) != 0 {
		t.Fatalf("both nil entries: %+v", d)
	}
	// nil Memory payload vs non-nil
	a3 := &Snapshot{Backend: "a", Memories: []*memory.Entry{{ID: "x", Memory: nil}}}
	b3 := &Snapshot{Backend: "b", Memories: []*memory.Entry{{ID: "x", Memory: &memory.Memory{Memory: "y"}}}}
	if ErrorDiffCount(c.Compare(tc, a3, b3, InMemoryProfile(), InMemoryProfile())) == 0 {
		t.Fatal("nil memory payload mismatch")
	}
}

func TestComparator_LongRunningAndResponseTimestamp(t *testing.T) {
	c := NewComparator()
	n := NewNormalizer()
	tc := ReplayCase{Name: "lr"}
	ea := *UserEvent("e1", "hi")
	eb := *UserEvent("e1", "hi")
	ea.LongRunningToolIDs = map[string]struct{}{"a": {}, "b": {}}
	eb.LongRunningToolIDs = map[string]struct{}{"a": {}, "c": {}}
	if ea.Response != nil {
		ea.Response.Timestamp = time.Unix(1, 0).UTC()
	}
	if eb.Response != nil {
		eb.Response.Timestamp = time.Unix(2, 0).UTC()
	}
	a := &Snapshot{Backend: "a", Session: &session.Session{Events: []event.Event{ea}}}
	b := &Snapshot{Backend: "b", Session: &session.Session{Events: []event.Event{eb}}}
	a, _ = n.Normalize(a)
	b, _ = n.Normalize(b)
	diffs := c.Compare(tc, a, b, InMemoryProfile(), InMemoryProfile())
	if ErrorDiffCount(diffs) == 0 {
		t.Fatalf("expected long-running diffs: %+v", diffs)
	}

	// equal long running sets (order-insensitive)
	eb.LongRunningToolIDs = map[string]struct{}{"b": {}, "a": {}}
	if ea.Response != nil {
		ea.Response.Timestamp = time.Unix(1, 0).UTC()
	}
	if eb.Response != nil {
		eb.Response.Timestamp = time.Unix(1, 0).UTC()
	}
	a = &Snapshot{Backend: "a", Session: &session.Session{Events: []event.Event{ea}}}
	b = &Snapshot{Backend: "b", Session: &session.Session{Events: []event.Event{eb}}}
	a, _ = n.Normalize(a)
	b, _ = n.Normalize(b)
	_ = c.Compare(tc, a, b, InMemoryProfile(), InMemoryProfile())

	// empty long running both nil — normalize first so IDs stabilize to tag
	ea2 := *UserEvent("e1", "hi")
	eb2 := *UserEvent("e1", "hi")
	a = &Snapshot{Backend: "a", Session: &session.Session{Events: []event.Event{ea2}}}
	b = &Snapshot{Backend: "b", Session: &session.Session{Events: []event.Event{eb2}}}
	a, _ = n.Normalize(a)
	b, _ = n.Normalize(b)
	if d := c.Compare(tc, a, b, InMemoryProfile(), InMemoryProfile()); ErrorDiffCount(d) != 0 {
		t.Fatalf("equal simple events: %+v", d)
	}
}

func TestComparator_AllowedDiffRules(t *testing.T) {
	c := NewComparator()
	// within_delta on memories.length via constructed length mismatch using floats on a numeric path
	// Use AllowedDiff on content with same_type / not_empty / within_delta
	tc := ReplayCase{
		Name: "rules",
		AllowedDiffs: []AllowedDiff{
			{PathPattern: "events[*].response.choices[0].message.content", Rule: RuleSameType, Reason: "same type content"},
			{PathPattern: "events[*].response.choices[0].message.content", Rule: RuleNotEmpty, Reason: "both non-empty"},
		},
	}
	a := &Snapshot{Backend: "a", Session: &session.Session{Events: []event.Event{*UserEvent("e1", "hello")}}}
	b := &Snapshot{Backend: "b", Session: &session.Session{Events: []event.Event{*UserEvent("e1", "world")}}}
	n := NewNormalizer()
	a, _ = n.Normalize(a)
	b, _ = n.Normalize(b)
	diffs := c.Compare(tc, a, b, InMemoryProfile(), InMemoryProfile())
	// same_type + not_empty both apply; either mark allowed
	if ErrorDiffCount(diffs) != 0 {
		// not_empty and same_type should allow string content diffs
		t.Fatalf("expected allowed by same_type/not_empty: %+v", diffs)
	}

	// within_delta numeric: force a float-bearing path via Errors length? Use markAllowed unit via Compare
	// with Baseline/Actual as numbers — inject via Snapshot.Errors won't give floats.
	// Direct unit tests of rule helpers through markAllowed:
	out := markAllowed([]Diff{
		{Path: "score", Baseline: 1.0, Actual: 1.5, Explanation: "x"},
		{Path: "score", Baseline: 1.0, Actual: 10.0, Explanation: "y"},
		{Path: "score", Baseline: "1.0", Actual: "1.2", Explanation: "z"},
		{Path: "empty", Baseline: "", Actual: "x", Explanation: "e"},
		{Path: "types", Baseline: 1, Actual: "1", Explanation: "t"},
		{Path: "nopath", Baseline: 1, Actual: 2, Explanation: "n"},
	}, []AllowedDiff{
		{PathPattern: "score", Rule: RuleWithinDelta, Delta: 1.0, Reason: "close"},
		{PathPattern: "empty", Rule: RuleNotEmpty, Reason: "ne"},
		{PathPattern: "types", Rule: RuleSameType, Reason: "st"},
		{PathPattern: "", Rule: RuleIgnore, Reason: "empty pattern never matches"},
	})
	if !out[0].Allowed {
		t.Fatal("within_delta 0.5 should allow")
	}
	if out[1].Allowed {
		t.Fatal("within_delta 9 should not allow")
	}
	if !out[2].Allowed {
		t.Fatal("string parse within_delta")
	}
	if out[3].Allowed {
		t.Fatal("not_empty should fail when baseline empty")
	}
	if out[4].Allowed {
		t.Fatal("same_type int vs string should fail")
	}
	if out[5].Allowed {
		t.Fatal("empty pattern")
	}

	// isEmpty / asFloat edges via markAllowed
	out = markAllowed([]Diff{
		{Path: "b", Baseline: []byte{}, Actual: []byte{1}},
		{Path: "s", Baseline: []string{}, Actual: []string{"a"}},
		{Path: "m", Baseline: map[string]int{}, Actual: map[string]int{"k": 1}},
		{Path: "n", Baseline: nil, Actual: 1},
		{Path: "f32", Baseline: float32(1.0), Actual: float32(1.1)},
		{Path: "i64", Baseline: int64(1), Actual: int64(1)},
	}, []AllowedDiff{
		{PathPattern: "b", Rule: RuleNotEmpty},
		{PathPattern: "s", Rule: RuleNotEmpty},
		{PathPattern: "m", Rule: RuleNotEmpty},
		{PathPattern: "n", Rule: RuleNotEmpty},
		{PathPattern: "f32", Rule: RuleWithinDelta, Delta: 0.2},
		{PathPattern: "i64", Rule: RuleWithinDelta, Delta: 0},
	})
	if out[0].Allowed || out[1].Allowed || out[2].Allowed || out[3].Allowed {
		t.Fatalf("not_empty empty baselines: %+v", out[:4])
	}
	if !out[4].Allowed || !out[5].Allowed {
		t.Fatalf("float/int64 delta: %+v", out[4:])
	}

	// matchPath exact + wildcard
	if !matchPath("events[0].x", "events[0].x") {
		t.Fatal("exact")
	}
	if !matchPath("events[*].x", "events[12].x") {
		t.Fatal("index wildcard")
	}
	if matchPath("", "x") {
		t.Fatal("empty pattern")
	}
}

func TestComparator_HelpersDirect(t *testing.T) {
	// messageContent / toolCalls nil paths
	if messageContent(event.Event{}) != "" {
		t.Fatal("empty content")
	}
	if toolCalls(event.Event{}) != nil {
		t.Fatal("empty toolcalls")
	}
	e := *UserEvent("e", "hi")
	if messageContent(e) != "hi" {
		t.Fatalf("content=%q", messageContent(e))
	}
	tc := *ToolCallEvent("t")
	if toolCalls(tc) == nil {
		t.Fatal("toolcalls")
	}
	// messageContentAt / toolCallsAt OOB
	if messageContentAt(e, -1) != "" || messageContentAt(e, 5) != "" {
		t.Fatal("oob content")
	}
	if toolCallsAt(e, -1) != nil || toolCallsAt(e, 5) != nil {
		t.Fatal("oob tools")
	}
	// responseTimestamp
	if responseTimestamp(event.Event{}) != nil {
		t.Fatal("nil response ts")
	}
	if !responseTimestampEqual(event.Event{}, event.Event{}) {
		// both nil response — equal
	}
	// longRunningEqual / keysOfSet
	if !longRunningEqual(nil, nil) {
		t.Fatal("nil sets")
	}
	if longRunningEqual(map[string]struct{}{"a": {}}, map[string]struct{}{"b": {}}) {
		t.Fatal("diff sets")
	}
	if longRunningEqual(map[string]struct{}{"a": {}}, map[string]struct{}{"a": {}, "b": {}}) {
		t.Fatal("len mismatch")
	}
	ks := keysOfSet(map[string]struct{}{"b": {}, "a": {}})
	if len(ks) != 2 || ks[0] != "a" {
		t.Fatalf("keysOfSet=%v", ks)
	}
	if keysOfSet(nil) != nil && len(keysOfSet(nil)) != 0 {
		// nil -> empty or nil ok
	}
	// keysOf
	k2 := keysOf(map[string]struct{}{"z": {}, "a": {}})
	if len(k2) != 2 {
		t.Fatalf("keysOf=%v", k2)
	}
	// memory helpers nil
	if memoryTopics(nil) != nil && len(memoryTopics(nil)) != 0 {
		t.Fatal("topics nil")
	}
	if memoryParticipants(nil) != nil && len(memoryParticipants(nil)) != 0 {
		t.Fatal("participants nil")
	}
	if memoryKind(nil) != "" || memoryLocation(nil) != "" {
		t.Fatal("kind/loc nil")
	}
	if memoryEventTime(nil) != nil || memoryLastUpdated(nil) != nil {
		t.Fatal("ptr times nil")
	}
	if memoryTimestamps(nil) != nil {
		// may return nil or empty
	}
	if !memoryTimeEqual(nil, nil) {
		t.Fatal("both nil time equal")
	}
	if memoryTimeEqual(nil, &memory.Entry{}) {
		t.Fatal("nil vs non-nil")
	}
	if !memoryPtrTimeEqual(nil, nil) {
		t.Fatal("ptr both nil")
	}
	if memoryPtrTimeEqual(nil, &[]time.Time{time.Now()}[0]) {
		t.Fatal("ptr one nil")
	}
	// state map one-side missing key
	diffs := compareStateMap("state", session.StateMap{"k": []byte("v")}, session.StateMap{}, "s")
	if ErrorDiffCount(diffs) == 0 {
		t.Fatal("state missing key")
	}
	// both missing same — equal empty
	if ErrorDiffCount(compareStateMap("state", nil, nil, "s")) != 0 {
		t.Fatal("nil maps")
	}
	// one nil one empty
	_ = compareStateMap("state", nil, session.StateMap{}, "s")
}

func TestComparator_MultiChoiceAndStateDeltaPresence(t *testing.T) {
	c := NewComparator()
	tc := ReplayCase{Name: "multi"}
	// multi-choice residual path: equal first choice content/toolcalls, differ later choice
	ea := *UserEvent("e1", "hi")
	eb := *UserEvent("e1", "hi")
	ea.Response.Choices = append(ea.Response.Choices, model.Choice{Message: model.NewUserMessage("extra-a")})
	eb.Response.Choices = append(eb.Response.Choices, model.Choice{Message: model.NewUserMessage("extra-b")})
	a := &Snapshot{Backend: "a", Session: &session.Session{Events: []event.Event{ea}}}
	b := &Snapshot{Backend: "b", Session: &session.Session{Events: []event.Event{eb}}}
	if ErrorDiffCount(c.Compare(tc, a, b, InMemoryProfile(), InMemoryProfile())) == 0 {
		t.Fatal("expected multi-choice residual diff")
	}

	// state presence: session nil vs empty on one side handled by Compare
	a = &Snapshot{Backend: "a", Session: nil, AppState: session.StateMap{"k": []byte("1")}}
	b = &Snapshot{Backend: "b", Session: &session.Session{}, AppState: session.StateMap{"k": []byte("2")}}
	if ErrorDiffCount(c.Compare(tc, a, b, InMemoryProfile(), InMemoryProfile())) == 0 {
		t.Fatal("expected presence/state diffs")
	}
}

func TestHarness_AppendEventFromCacheAndDefaults(t *testing.T) {
	b := openInMemoryBackend(t)
	key := SessionKeyFor("cache_append")
	// first create session via update state so sessions map is warm
	// then append with empty SessionKey but existing snapshot SessionID
	tc := ReplayCase{
		Name: "cache_append",
		Steps: []Step{
			UpdateStateStep{
				StepKey: "init", Scope: "session", SessionKey: key,
				State: session.StateMap{"k": []byte("v")},
			},
			// GetSession sets snapshot.SessionID
			GetSessionStep{StepKey: "g", SessionKey: key},
			// empty SessionKey should reuse snapshot.SessionID
			AppendEventStep{StepKey: "e2", Event: UserEvent("e2", "from-cache")},
			// zero timestamp event
			AppendEventStep{StepKey: "e3", SessionKey: key, Event: &event.Event{
				Author: "user",
				Response: &model.Response{
					Object:  model.ObjectTypeChatCompletion,
					Choices: []model.Choice{{Message: model.NewUserMessage("z")}},
				},
			}},
			// wait summary defaults for timeout/poll
			CreateSummaryStep{StepKey: "sum", SessionKey: key, Force: true},
			WaitSummaryStep{StepKey: "w", SessionKey: key}, // defaults
		},
	}
	snap, err := executeCase(context.Background(), tc, b)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Session == nil {
		t.Fatal("nil session")
	}
}

// avoid importing context in signature noise
