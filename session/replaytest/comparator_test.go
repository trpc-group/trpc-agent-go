// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package replaytest

import (
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestComparator_DetectsEventAndSummaryDiffs(t *testing.T) {
	c := NewComparator()
	tc := ReplayCase{Name: "single_turn_text"}
	a := &Snapshot{
		Backend:   "inmemory",
		SessionID: "s1",
		Session: &session.Session{
			Events: []event.Event{*UserEvent("e1", "hello"), *AssistantEvent("e2", "hi")},
			Summaries: map[string]*session.Summary{
				"": {Summary: "full", Topics: []string{"a"}, UpdatedAt: time.Unix(1, 0).UTC()},
			},
		},
	}
	b := &Snapshot{
		Backend:   "sqlite",
		SessionID: "s1",
		Session: &session.Session{
			Events: []event.Event{*UserEvent("e1", "hello"), *AssistantEvent("e2", "bye")},
			Summaries: map[string]*session.Summary{
				"other": {Summary: "full", Topics: []string{"a"}, UpdatedAt: time.Unix(1, 0).UTC()},
			},
		},
	}
	n := NewNormalizer()
	a, _ = n.Normalize(a)
	b, _ = n.Normalize(b)
	diffs := c.Compare(tc, a, b, InMemoryProfile(), SQLiteProfile())
	if ErrorDiffCount(diffs) == 0 {
		t.Fatal("expected diffs")
	}
	var hasContent, hasSummary bool
	for _, d := range diffs {
		if !d.Allowed && d.Path == "events[1].response.choices[0].message.content" {
			hasContent = true
			if d.EventIndex == nil {
				t.Fatal("event_index missing")
			}
		}
		if !d.Allowed && (d.SummaryFilterKey == "" || d.SummaryFilterKey == "other") {
			if d.Path == `summaries[""]` || d.Path == `summaries["other"]` {
				hasSummary = true
			}
		}
	}
	if !hasContent {
		t.Fatalf("content diff missing: %+v", diffs)
	}
	if !hasSummary {
		t.Fatalf("summary filter-key diff missing: %+v", diffs)
	}
}

func TestComparator_AllowedDiffIgnore(t *testing.T) {
	c := NewComparator()
	tc := ReplayCase{
		Name: "x",
		AllowedDiffs: []AllowedDiff{
			{PathPattern: "events[*].response.choices[0].message.content", Rule: RuleIgnore, Reason: "ignore content"},
		},
	}
	a := &Snapshot{Backend: "a", Session: &session.Session{Events: []event.Event{*UserEvent("e1", "a")}}}
	b := &Snapshot{Backend: "b", Session: &session.Session{Events: []event.Event{*UserEvent("e1", "b")}}}
	n := NewNormalizer()
	a, _ = n.Normalize(a)
	b, _ = n.Normalize(b)
	diffs := c.Compare(tc, a, b, InMemoryProfile(), InMemoryProfile())
	if ErrorDiffCount(diffs) != 0 {
		t.Fatalf("expected allowed: %+v", diffs)
	}
}

func TestComparator_EmptyAllowedRuleNotIgnored(t *testing.T) {
	c := NewComparator()
	tc := ReplayCase{
		Name: "x",
		AllowedDiffs: []AllowedDiff{
			{PathPattern: "events[*].response.choices[0].message.content", Rule: "", Reason: "empty should not match"},
		},
	}
	a := &Snapshot{Backend: "a", Session: &session.Session{Events: []event.Event{*UserEvent("e1", "a")}}}
	b := &Snapshot{Backend: "b", Session: &session.Session{Events: []event.Event{*UserEvent("e1", "b")}}}
	n := NewNormalizer()
	a, _ = n.Normalize(a)
	b, _ = n.Normalize(b)
	diffs := c.Compare(tc, a, b, InMemoryProfile(), InMemoryProfile())
	if ErrorDiffCount(diffs) == 0 {
		t.Fatalf("empty rule must not allow diff: %+v", diffs)
	}
}

func TestComparator_StateDeltaBytes(t *testing.T) {
	c := NewComparator()
	tc := ReplayCase{Name: "state_delta_bytes"}
	mk := func(delta map[string][]byte) *Snapshot {
		return &Snapshot{
			Backend: "x",
			Session: &session.Session{Events: []event.Event{{
				ID:         "e1",
				StateDelta: delta,
			}}},
		}
	}
	n := NewNormalizer()

	// Different non-UTF-8 bytes must not collapse through JSON string encoding.
	a := mk(map[string][]byte{"bad": {0xff}})
	b := mk(map[string][]byte{"bad": {0xfe}})
	a, _ = n.Normalize(a)
	b, _ = n.Normalize(b)
	diffs := c.Compare(tc, a, b, InMemoryProfile(), InMemoryProfile())
	found := false
	for _, d := range diffs {
		if !d.Allowed && d.Path == `events[0].state_delta["bad"]` {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected state delta byte diff, got %+v", diffs)
	}

	// Identical non-UTF-8 bytes should not produce a state_delta diff.
	a2 := mk(map[string][]byte{"bad": {0xff}})
	b2 := mk(map[string][]byte{"bad": {0xff}})
	a2, _ = n.Normalize(a2)
	b2, _ = n.Normalize(b2)
	for _, d := range c.Compare(tc, a2, b2, InMemoryProfile(), InMemoryProfile()) {
		if !d.Allowed && d.Path == `events[0].state_delta["bad"]` {
			t.Fatalf("unexpected state delta diff for equal bytes: %+v", d)
		}
	}

	// Key presence remains explicit.
	a3 := mk(map[string][]byte{"bad": {0xff}})
	b3 := mk(nil)
	a3, _ = n.Normalize(a3)
	b3, _ = n.Normalize(b3)
	foundPresence := false
	for _, d := range c.Compare(tc, a3, b3, InMemoryProfile(), InMemoryProfile()) {
		if !d.Allowed && d.Path == `events[0].state_delta["bad"]` && d.Explanation == "state delta key presence mismatch" {
			foundPresence = true
		}
	}
	if !foundPresence {
		t.Fatalf("expected state delta presence diff")
	}
}

func TestComparator_AppUserStateCapturesDetectMissingUpdates(t *testing.T) {
	c := NewComparator()
	tc := ReplayCase{Name: "app_user_state_boundary"}
	a := &Snapshot{
		Backend:   "a",
		AppState:  session.StateMap{},
		UserState: session.StateMap{},
		AppStateCaptures: map[string]session.StateMap{
			"c12.app.list.initial": {"app_k": []byte("app-v1")},
		},
		UserStateCaptures: map[string]session.StateMap{
			"c12.user.list.initial": {"user_k": []byte("user-v1")},
		},
	}
	b := &Snapshot{
		Backend:   "b",
		AppState:  session.StateMap{},
		UserState: session.StateMap{},
	}
	n := NewNormalizer()
	a, _ = n.Normalize(a)
	b, _ = n.Normalize(b)
	diffs := c.Compare(tc, a, b, InMemoryProfile(), InMemoryProfile())
	want := map[string]bool{
		`app_state_captures["c12.app.list.initial"]`:   false,
		`user_state_captures["c12.user.list.initial"]`: false,
	}
	for _, d := range diffs {
		if _, ok := want[d.Path]; ok && !d.Allowed {
			want[d.Path] = true
		}
	}
	for path, found := range want {
		if !found {
			t.Fatalf("missing capture diff %s in %+v", path, diffs)
		}
	}
}

func TestComparator_EpisodicMemoryMetadataDiffs(t *testing.T) {
	t1 := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	mk := func(kind memory.Kind, eventTime *time.Time, participants []string, location string) *Snapshot {
		return &Snapshot{Backend: "x", Memories: []*memory.Entry{{
			ID: "raw", AppName: "app", UserID: "user",
			Memory: &memory.Memory{
				Memory: "visited Tokyo", Topics: []string{"travel"},
				Kind: kind, EventTime: eventTime, Participants: participants, Location: location,
			},
		}}}
	}
	a := mk(memory.KindEpisode, &t1, []string{"Alice", "User"}, "Tokyo")
	b := mk(memory.KindFact, &t2, []string{"Bob"}, "Kyoto")
	n := NewNormalizer()
	a, _ = n.Normalize(a)
	b, _ = n.Normalize(b)
	diffs := NewComparator().Compare(ReplayCase{Name: "episodic_metadata"}, a, b, InMemoryProfile(), InMemoryProfile())
	want := map[string]bool{
		"memories[0].kind":         false,
		"memories[0].event_time":   false,
		"memories[0].participants": false,
		"memories[0].location":     false,
	}
	for _, d := range diffs {
		if _, ok := want[d.Path]; ok && !d.Allowed {
			want[d.Path] = true
		}
	}
	for path, found := range want {
		if !found {
			t.Fatalf("missing episodic metadata diff %s in %+v", path, diffs)
		}
	}
}

func TestComparator_MemoryAndTrack(t *testing.T) {
	c := NewComparator()
	tc := ReplayCase{Name: "track_events"}
	ts := time.Unix(10, 0).UTC()
	a := &Snapshot{
		Backend: "a",
		Session: &session.Session{
			Tracks: map[session.Track]*session.TrackEvents{
				"tool": {Track: "tool", Events: []session.TrackEvent{
					{Track: "tool", Payload: []byte(`{"step":1}`), Timestamp: ts},
				}},
			},
		},
		Memories: []*memory.Entry{{ID: "1", Memory: &memory.Memory{Memory: "x", Topics: []string{"t"}, Participants: []string{"p"}}}},
	}
	b := &Snapshot{
		Backend: "b",
		Session: &session.Session{
			Tracks: map[session.Track]*session.TrackEvents{
				"tool": {Track: "tool", Events: []session.TrackEvent{
					{Track: "tool", Payload: []byte(`{"step":2}`), Timestamp: ts.Add(time.Second)},
				}},
			},
		},
		Memories: []*memory.Entry{{ID: "1", Memory: &memory.Memory{Memory: "y", Topics: []string{"t"}, Participants: []string{"q"}}}},
	}
	n := NewNormalizer()
	a, _ = n.Normalize(a)
	b, _ = n.Normalize(b)
	diffs := c.Compare(tc, a, b, InMemoryProfile(), InMemoryProfile())
	var hasTrackPayload, hasTrackTS, hasMemContent, hasMemPart bool
	for _, d := range diffs {
		if d.Allowed {
			continue
		}
		switch d.Path {
		case `tracks["tool"].events[0].payload`:
			hasTrackPayload = true
		case `tracks["tool"].events[0].timestamp`:
			hasTrackTS = true
		case "memories[0].content":
			hasMemContent = true
		case "memories[0].participants":
			hasMemPart = true
		}
	}
	if !hasTrackPayload || !hasTrackTS || !hasMemContent || !hasMemPart {
		t.Fatalf("expected track payload/ts + memory content/participants, got %+v", diffs)
	}
}

// TestComparator_TrackOwnershipFields ensures map-key agreement is not enough:
// TrackEvents.Track and each TrackEvent.Track must match independently.
func TestComparator_TrackOwnershipFields(t *testing.T) {
	c := NewComparator()
	tc := ReplayCase{Name: "track_ownership"}
	ts := time.Unix(10, 0).UTC()
	payload := []byte(`{"ok":true}`)

	base := func(container, eventTrack session.Track) *Snapshot {
		return &Snapshot{
			Backend: "x",
			Session: &session.Session{
				Tracks: map[session.Track]*session.TrackEvents{
					"tool": {
						Track: container,
						Events: []session.TrackEvent{
							{Track: eventTrack, Payload: payload, Timestamp: ts},
						},
					},
				},
			},
		}
	}

	// Same payload/time; only container Track differs.
	a := base("tool", "tool")
	b := base("other", "tool")
	n := NewNormalizer()
	a, _ = n.Normalize(a)
	b, _ = n.Normalize(b)
	diffs := c.Compare(tc, a, b, InMemoryProfile(), InMemoryProfile())
	foundContainer := false
	for _, d := range diffs {
		if !d.Allowed && d.Path == `tracks["tool"].track` {
			foundContainer = true
		}
	}
	if !foundContainer {
		t.Fatalf("expected container track ownership diff, got %+v", diffs)
	}

	// Only per-event Track differs.
	a2 := base("tool", "tool")
	b2 := base("tool", "other")
	a2, _ = n.Normalize(a2)
	b2, _ = n.Normalize(b2)
	diffs2 := c.Compare(tc, a2, b2, InMemoryProfile(), InMemoryProfile())
	foundEvent := false
	for _, d := range diffs2 {
		if !d.Allowed && d.Path == `tracks["tool"].events[0].track` {
			foundEvent = true
		}
	}
	if !foundEvent {
		t.Fatalf("expected event track ownership diff, got %+v", diffs2)
	}

	// Matching ownership: no track ownership paths.
	a3 := base("tool", "tool")
	b3 := base("tool", "tool")
	a3, _ = n.Normalize(a3)
	b3, _ = n.Normalize(b3)
	for _, d := range c.Compare(tc, a3, b3, InMemoryProfile(), InMemoryProfile()) {
		if !d.Allowed && (d.Path == `tracks["tool"].track` || d.Path == `tracks["tool"].events[0].track`) {
			t.Fatalf("unexpected ownership diff on equal fixtures: %+v", d)
		}
	}
}

// TestComparator_SecondarySessionSummaryAndTrack ensures Snapshot.Sessions
// entries compare summaries and tracks (not only identity/events/state).
func TestComparator_SecondarySessionSummaryAndTrack(t *testing.T) {
	c := NewComparator()
	tc := ReplayCase{Name: "secondary_session"}
	n := NewNormalizer()
	ts := time.Unix(20, 0).UTC()
	primaryID := "primary-sess"
	secondaryID := "secondary-sess"

	mkSnap := func(sumText string, trackPayload []byte) *Snapshot {
		return &Snapshot{
			Backend:   "x",
			SessionID: primaryID,
			Session: &session.Session{
				ID: primaryID,
				// Primary has matching summaries so only secondary differs.
				Summaries: map[string]*session.Summary{
					"": {Summary: "primary-ok", UpdatedAt: ts},
				},
			},
			Sessions: map[string]*session.Session{
				primaryID: {
					ID: primaryID,
					// Compared via Snapshot.Session; skipped in extra map.
				},
				secondaryID: {
					ID: secondaryID,
					Summaries: map[string]*session.Summary{
						"": {Summary: sumText, UpdatedAt: ts},
					},
					Tracks: map[session.Track]*session.TrackEvents{
						"tool": {
							Track: "tool",
							Events: []session.TrackEvent{
								{Track: "tool", Payload: trackPayload, Timestamp: ts},
							},
						},
					},
				},
			},
		}
	}

	// Only secondary summary text differs.
	a := mkSnap("sec-summary-a", []byte(`{"v":1}`))
	b := mkSnap("sec-summary-b", []byte(`{"v":1}`))
	a, _ = n.Normalize(a)
	b, _ = n.Normalize(b)
	diffs := c.Compare(tc, a, b, InMemoryProfile(), InMemoryProfile())
	foundSum := false
	wantSumPath := `sessions["secondary-sess"].summaries[""].summary`
	for _, d := range diffs {
		if !d.Allowed && d.Path == wantSumPath {
			foundSum = true
		}
	}
	if !foundSum {
		t.Fatalf("expected secondary summary diff path %s, got %+v", wantSumPath, diffs)
	}

	// Only secondary track payload differs.
	a2 := mkSnap("same", []byte(`{"v":1}`))
	b2 := mkSnap("same", []byte(`{"v":2}`))
	a2, _ = n.Normalize(a2)
	b2, _ = n.Normalize(b2)
	diffs2 := c.Compare(tc, a2, b2, InMemoryProfile(), InMemoryProfile())
	foundTrack := false
	wantTrackPath := `sessions["secondary-sess"].tracks["tool"].events[0].payload`
	for _, d := range diffs2 {
		if !d.Allowed && d.Path == wantTrackPath {
			foundTrack = true
		}
	}
	if !foundTrack {
		t.Fatalf("expected secondary track diff path %s, got %+v", wantTrackPath, diffs2)
	}

	// Equal secondary summary+track: no those paths.
	a3 := mkSnap("same", []byte(`{"v":1}`))
	b3 := mkSnap("same", []byte(`{"v":1}`))
	a3, _ = n.Normalize(a3)
	b3, _ = n.Normalize(b3)
	for _, d := range c.Compare(tc, a3, b3, InMemoryProfile(), InMemoryProfile()) {
		if d.Allowed {
			continue
		}
		if d.Path == wantSumPath || d.Path == wantTrackPath {
			t.Fatalf("unexpected secondary diff on equal fixtures: %+v", d)
		}
	}
}

func TestComparator_BranchLocalSemantic(t *testing.T) {
	c := NewComparator()
	tc := ReplayCase{Name: "concurrent_interleaved", EventCompareMode: EventCompareBranchLocal}
	ea := []event.Event{*UserEvent("b1.1", "x"), *UserEvent("b2.1", "y")}
	ea[0].Branch, ea[1].Branch = "b1", "b2"
	eb := []event.Event{*UserEvent("b2.1", "y"), *UserEvent("b1.1", "x")}
	eb[0].Branch, eb[1].Branch = "b2", "b1"
	a := &Snapshot{Backend: "a", Session: &session.Session{Events: ea}}
	b := &Snapshot{Backend: "b", Session: &session.Session{Events: eb}}
	n := NewNormalizer()
	a, _ = n.Normalize(a)
	b, _ = n.Normalize(b)
	diffs := c.Compare(tc, a, b, InMemoryProfile(), InMemoryProfile())
	if ErrorDiffCount(diffs) != 0 {
		t.Fatalf("branch_local should accept reordered global order: %+v", diffs)
	}

	// content mismatch on same logical id must still fail
	eb2 := []event.Event{*UserEvent("b2.1", "y"), *UserEvent("b1.1", "CHANGED")}
	eb2[0].Branch, eb2[1].Branch = "b2", "b1"
	b2 := &Snapshot{Backend: "b", Session: &session.Session{Events: eb2}}
	b2, _ = n.Normalize(b2)
	diffs = c.Compare(tc, a, b2, InMemoryProfile(), InMemoryProfile())
	if ErrorDiffCount(diffs) == 0 {
		t.Fatal("expected semantic content mismatch under branch_local")
	}
}

func TestComparator_BranchLocalDuplicateIDOccurrence(t *testing.T) {
	c := NewComparator()
	tc := ReplayCase{Name: "dup", EventCompareMode: EventCompareBranchLocal}
	// Same multiset of IDs and branch order, but first occurrence content differs.
	ea := []event.Event{*UserEvent("same", "first-a"), *UserEvent("same", "second")}
	ea[0].Branch, ea[1].Branch = "b1", "b1"
	eb := []event.Event{*UserEvent("same", "first-B"), *UserEvent("same", "second")}
	eb[0].Branch, eb[1].Branch = "b1", "b1"
	a := &Snapshot{Backend: "a", Session: &session.Session{Events: ea}}
	b := &Snapshot{Backend: "b", Session: &session.Session{Events: eb}}
	n := NewNormalizer()
	a, _ = n.Normalize(a)
	b, _ = n.Normalize(b)
	diffs := c.Compare(tc, a, b, InMemoryProfile(), InMemoryProfile())
	if ErrorDiffCount(diffs) == 0 {
		t.Fatalf("expected occurrence-aware semantic mismatch, got none: %+v", diffs)
	}
	var hasOcc bool
	for _, d := range diffs {
		if !d.Allowed && (contains(d.Path, "id=same#0") || contains(d.Path, "content")) {
			hasOcc = true
		}
	}
	if !hasOcc {
		t.Fatalf("expected first-occurrence content path, got %+v", diffs)
	}
}

func TestComparator_EventInvocationAndMemoryFields(t *testing.T) {
	c := NewComparator()
	tc := ReplayCase{Name: "fields"}
	ea := *UserEvent("e1", "hi")
	ea.Tag = "t1"
	ea.RequiresCompletion = true
	ea.FilterKey = "fk"
	ea.Version = 2
	ea.InvocationID = "inv-a"
	eb := *UserEvent("e1", "hi")
	eb.Tag = "t2"
	eb.RequiresCompletion = false
	eb.FilterKey = "fk2"
	eb.Version = 3
	eb.InvocationID = "inv-b"
	ts := time.Unix(100, 0).UTC()
	ts2 := time.Unix(200, 0).UTC()
	a := &Snapshot{
		Backend: "a",
		Session: &session.Session{Events: []event.Event{ea}},
		Memories: []*memory.Entry{{
			ID: "m",
			Memory: &memory.Memory{
				Memory: "x", Kind: memory.KindFact, Location: "home",
				EventTime: &ts, LastUpdated: &ts,
			},
		}},
	}
	b := &Snapshot{
		Backend: "b",
		Session: &session.Session{Events: []event.Event{eb}},
		Memories: []*memory.Entry{{
			ID: "m",
			Memory: &memory.Memory{
				Memory: "x", Kind: memory.KindEpisode, Location: "office",
				EventTime: &ts2, LastUpdated: &ts2,
			},
		}},
	}
	n := NewNormalizer()
	a, _ = n.Normalize(a)
	b, _ = n.Normalize(b)
	diffs := c.Compare(tc, a, b, InMemoryProfile(), InMemoryProfile())
	wantPaths := []string{
		"events[0].tag",
		"events[0].requires_completion",
		"events[0].filter_key",
		"events[0].version",
		"events[0].invocation_id",
		"memories[0].kind",
		"memories[0].location",
		"memories[0].event_time",
	}
	got := map[string]bool{}
	for _, d := range diffs {
		if !d.Allowed {
			got[d.Path] = true
		}
	}
	for _, pth := range wantPaths {
		if !got[pth] {
			t.Fatalf("missing path %s in %+v", pth, diffs)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || (len(s) > 0 && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()))
}

func TestComparator_ResponseResidual(t *testing.T) {
	c := NewComparator()
	tc := ReplayCase{Name: "resp"}
	ea := *UserEvent("e1", "hi")
	eb := *UserEvent("e1", "hi")
	if ea.Response == nil || eb.Response == nil {
		t.Fatal("fixture missing response")
	}
	ea.Response.Object = "chat.completion"
	ea.Response.Done = true
	eb.Response.Object = "chat.completion.chunk"
	eb.Response.Done = false
	eb.Response.Error = &model.ResponseError{Message: "boom"}
	a := &Snapshot{Backend: "a", Session: &session.Session{Events: []event.Event{ea}}}
	b := &Snapshot{Backend: "b", Session: &session.Session{Events: []event.Event{eb}}}
	n := NewNormalizer()
	a, _ = n.Normalize(a)
	b, _ = n.Normalize(b)
	diffs := c.Compare(tc, a, b, InMemoryProfile(), InMemoryProfile())
	var has bool
	for _, d := range diffs {
		if !d.Allowed && d.Path == "events[0].response" {
			has = true
		}
	}
	if !has {
		t.Fatalf("expected response residual diff, got %+v", diffs)
	}
}

func TestComparator_BranchLocalCrossBranchDuplicateID(t *testing.T) {
	cmp := NewComparator()
	tc := ReplayCase{Name: "cross_branch_dup", EventCompareMode: EventCompareBranchLocal}
	// Same ID appears once on each branch. Global interleaving differs; branch-local
	// occurrence counters must not cross-pair events across branches.
	a := &Snapshot{Session: &session.Session{Events: []event.Event{
		{ID: "same", Author: "user", Branch: "b1", Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "b1"}}}}},
		{ID: "same", Author: "user", Branch: "b2", Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "b2"}}}}},
	}}}
	b := &Snapshot{Session: &session.Session{Events: []event.Event{
		{ID: "same", Author: "user", Branch: "b2", Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "b2"}}}}},
		{ID: "same", Author: "user", Branch: "b1", Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "b1"}}}}},
	}}}
	diffs := cmp.Compare(tc, a, b, BackendProfile{Name: "A"}, BackendProfile{Name: "B"})
	if len(diffs) != 0 {
		t.Fatalf("branch-local cross-branch same ID with reordered interleaving should pass: %+v", diffs)
	}

	// Content mismatch on b1 must still be detected and not swallowed by global pairing.
	bBad := &Snapshot{Session: &session.Session{Events: []event.Event{
		{ID: "same", Author: "user", Branch: "b2", Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "b2"}}}}},
		{ID: "same", Author: "user", Branch: "b1", Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "B1-BAD"}}}}},
	}}}
	diffs = cmp.Compare(tc, a, bBad, BackendProfile{Name: "A"}, BackendProfile{Name: "B"})
	if len(diffs) == 0 {
		t.Fatal("expected content mismatch for branch b1 event")
	}
}

func TestComparator_SessionPresence(t *testing.T) {
	cmp := NewComparator()
	tc := ReplayCase{Name: "session_presence"}
	a := &Snapshot{Session: &session.Session{Events: nil}}
	b := &Snapshot{Session: nil}
	diffs := cmp.Compare(tc, a, b, BackendProfile{Name: "A"}, BackendProfile{Name: "B"})
	found := false
	for _, d := range diffs {
		if d.Path == "session" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected session presence mismatch, got %+v", diffs)
	}
}

func TestComparator_SessionIdentityFields(t *testing.T) {
	cmp := NewComparator()
	tc := ReplayCase{Name: "session_identity"}
	evt := *UserEvent("e1", "hello")
	baseSess := func() *session.Session {
		return &session.Session{
			ID:      "session-s1",
			AppName: DefaultApp,
			UserID:  DefaultUser,
			Events:  []event.Event{evt},
		}
	}
	baseSnap := func() *Snapshot {
		return &Snapshot{
			Backend:   "a",
			SessionID: "session-s1",
			Session:   baseSess(),
		}
	}

	// Snapshot.SessionID only.
	aID, bID := baseSnap(), baseSnap()
	bID.Backend = "b"
	bID.SessionID = "session-s2"
	assertSessionIdentityDiff(t, cmp, tc, aID, bID, "session_id", "session-s1", "session-s2")

	// Session.ID only (payload events unchanged).
	aSID, bSID := baseSnap(), baseSnap()
	bSID.Backend = "b"
	bSID.Session.ID = "session-other"
	assertSessionIdentityDiff(t, cmp, tc, aSID, bSID, "session.id", "session-s1", "session-other")

	// Session.AppName only.
	aApp, bApp := baseSnap(), baseSnap()
	bApp.Backend = "b"
	bApp.Session.AppName = "other-app"
	assertSessionIdentityDiff(t, cmp, tc, aApp, bApp, "session.app_name", DefaultApp, "other-app")

	// Session.UserID only.
	aUser, bUser := baseSnap(), baseSnap()
	bUser.Backend = "b"
	bUser.Session.UserID = "other-user"
	assertSessionIdentityDiff(t, cmp, tc, aUser, bUser, "session.user_id", DefaultUser, "other-user")

	// Matching identity should not invent identity diffs.
	aOK, bOK := baseSnap(), baseSnap()
	bOK.Backend = "b"
	for _, d := range cmp.Compare(tc, aOK, bOK, InMemoryProfile(), InMemoryProfile()) {
		if !d.Allowed && (d.Path == "session_id" || d.Path == "session.id" ||
			d.Path == "session.app_name" || d.Path == "session.user_id" ||
			d.Path == "session.created_at" || d.Path == "session.updated_at") {
			t.Fatalf("unexpected identity diff when equal: %+v", d)
		}
	}
}

// TestComparator_SessionAuditTimestamps: non-zero audit times collapse to
// FixedTimestamp (aligned across backends); zero vs non-zero still diffs.
func TestComparator_SessionAuditTimestamps(t *testing.T) {
	cmp := NewComparator()
	n := NewNormalizer()
	tc := ReplayCase{Name: "session_audit_time"}
	evt := *UserEvent("e1", "hello")

	mk := func(created, updated time.Time) *Snapshot {
		return &Snapshot{
			Backend:   "x",
			SessionID: "s1",
			Session: &session.Session{
				ID:        "s1",
				AppName:   DefaultApp,
				UserID:    DefaultUser,
				CreatedAt: created,
				UpdatedAt: updated,
				Events:    []event.Event{evt},
			},
		}
	}

	// Different non-zero clocks → both become FixedTimestamp → no audit diff.
	a1 := mk(time.Unix(1, 0).UTC(), time.Unix(2, 0).UTC())
	b1 := mk(time.Unix(100, 0).UTC(), time.Unix(200, 0).UTC())
	a1, _ = n.Normalize(a1)
	b1, _ = n.Normalize(b1)
	if !a1.Session.CreatedAt.Equal(FixedTimestamp) || !b1.Session.CreatedAt.Equal(FixedTimestamp) {
		t.Fatalf("expected FixedTimestamp, got a=%v b=%v", a1.Session.CreatedAt, b1.Session.CreatedAt)
	}
	for _, d := range cmp.Compare(tc, a1, b1, InMemoryProfile(), InMemoryProfile()) {
		if !d.Allowed && (d.Path == "session.created_at" || d.Path == "session.updated_at") {
			t.Fatalf("non-zero clocks should align after normalize: %+v", d)
		}
	}

	// Populated vs zero CreatedAt → must fail after normalize.
	a2 := mk(time.Unix(1, 0).UTC(), time.Time{})
	b2 := mk(time.Time{}, time.Time{})
	a2, _ = n.Normalize(a2)
	b2, _ = n.Normalize(b2)
	found := false
	for _, d := range cmp.Compare(tc, a2, b2, InMemoryProfile(), InMemoryProfile()) {
		if !d.Allowed && d.Path == "session.created_at" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected session.created_at diff for non-zero vs zero")
	}

	// Both zero → no created_at/updated_at diff.
	a3 := mk(time.Time{}, time.Time{})
	b3 := mk(time.Time{}, time.Time{})
	a3, _ = n.Normalize(a3)
	b3, _ = n.Normalize(b3)
	for _, d := range cmp.Compare(tc, a3, b3, InMemoryProfile(), InMemoryProfile()) {
		if !d.Allowed && (d.Path == "session.created_at" || d.Path == "session.updated_at") {
			t.Fatalf("unexpected audit diff when both zero: %+v", d)
		}
	}
}

func assertSessionIdentityDiff(t *testing.T, cmp *Comparator, tc ReplayCase, a, b *Snapshot, path string, wantBase, wantActual any) {
	t.Helper()
	diffs := cmp.Compare(tc, a, b, InMemoryProfile(), InMemoryProfile())
	for _, d := range diffs {
		if d.Path == path && !d.Allowed {
			if d.Baseline != wantBase || d.Actual != wantActual {
				t.Fatalf("path %s: baseline=%v actual=%v want %v / %v", path, d.Baseline, d.Actual, wantBase, wantActual)
			}
			return
		}
	}
	t.Fatalf("expected non-allowed diff at %s, got %+v", path, diffs)
}

func TestComparator_MemoryPresence(t *testing.T) {
	cmp := NewComparator()
	tc := ReplayCase{Name: "memory_presence"}
	a := &Snapshot{Memories: []*memory.Entry{nil}}
	b := &Snapshot{Memories: []*memory.Entry{{ID: "m1", Memory: &memory.Memory{Memory: "x"}}}}
	diffs := cmp.Compare(tc, a, b, BackendProfile{Name: "A"}, BackendProfile{Name: "B"})
	found := false
	for _, d := range diffs {
		if d.Path == "memories[0]" || d.Path == "memories[0].memory" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected memory presence mismatch, got %+v", diffs)
	}
}

func TestComparator_MemoryOwnershipFields(t *testing.T) {
	cmp := NewComparator()
	tc := ReplayCase{Name: "memory_ownership"}
	base := &memory.Entry{
		ID:      "m1",
		AppName: DefaultApp,
		UserID:  DefaultUser,
		Memory:  &memory.Memory{Memory: "likes tea", Topics: []string{"prefs"}},
	}

	// Same content, only AppName differs — must surface a non-allowed diff.
	aApp := &Snapshot{Backend: "a", Memories: []*memory.Entry{cloneMemoryForTest(base)}}
	bApp := &Snapshot{Backend: "b", Memories: []*memory.Entry{cloneMemoryForTest(base)}}
	bApp.Memories[0].AppName = "other-app"
	assertMemoryOwnershipDiff(t, cmp, tc, aApp, bApp, "memories[0].app_name", DefaultApp, "other-app")

	// Same content, only UserID differs.
	aUser := &Snapshot{Backend: "a", Memories: []*memory.Entry{cloneMemoryForTest(base)}}
	bUser := &Snapshot{Backend: "b", Memories: []*memory.Entry{cloneMemoryForTest(base)}}
	bUser.Memories[0].UserID = "other-user"
	assertMemoryOwnershipDiff(t, cmp, tc, aUser, bUser, "memories[0].user_id", DefaultUser, "other-user")

	// Matching ownership should not invent ownership diffs.
	aOK := &Snapshot{Backend: "a", Memories: []*memory.Entry{cloneMemoryForTest(base)}}
	bOK := &Snapshot{Backend: "b", Memories: []*memory.Entry{cloneMemoryForTest(base)}}
	for _, d := range cmp.Compare(tc, aOK, bOK, InMemoryProfile(), InMemoryProfile()) {
		if !d.Allowed && (d.Path == "memories[0].app_name" || d.Path == "memories[0].user_id") {
			t.Fatalf("unexpected ownership diff when equal: %+v", d)
		}
	}
}

func cloneMemoryForTest(in *memory.Entry) *memory.Entry {
	if in == nil {
		return nil
	}
	out := *in
	if in.Memory != nil {
		m := *in.Memory
		if in.Memory.Topics != nil {
			m.Topics = append([]string(nil), in.Memory.Topics...)
		}
		if in.Memory.Participants != nil {
			m.Participants = append([]string(nil), in.Memory.Participants...)
		}
		out.Memory = &m
	}
	return &out
}

func assertMemoryOwnershipDiff(t *testing.T, cmp *Comparator, tc ReplayCase, a, b *Snapshot, path string, wantBase, wantActual any) {
	t.Helper()
	diffs := cmp.Compare(tc, a, b, InMemoryProfile(), InMemoryProfile())
	for _, d := range diffs {
		if d.Path == path && !d.Allowed {
			if d.Baseline != wantBase || d.Actual != wantActual {
				t.Fatalf("path %s: baseline=%v actual=%v want %v / %v", path, d.Baseline, d.Actual, wantBase, wantActual)
			}
			return
		}
	}
	t.Fatalf("expected non-allowed diff at %s, got %+v", path, diffs)
}
