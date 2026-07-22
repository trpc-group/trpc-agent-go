// Tencent is pleased to support the open source community by making trpc-agent-go available.
// Copyright (C) 2025 Tencent. All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	frameworkevent "trpc.group/trpc-go/trpc-agent-go/event"
	frameworksession "trpc.group/trpc-go/trpc-agent-go/session"
)

func TestNormalCasesHaveNoDiff(t *testing.T) {
	for _, tc := range Cases() {
		if d := Compare(tc.Name, "json", tc.Expected(), clone(tc.Expected())); len(d) != 0 {
			t.Fatalf("%s false positive: %+v", tc.Name, d)
		}
	}
}
func TestAllInjectedDifferencesDetected(t *testing.T) {
	for _, tc := range Cases() {
		a := tc.Expected()
		b := clone(a)
		before := Compare(tc.Name, "json", a, b)
		tc.Mutate(&b)
		after := Compare(tc.Name, "json", a, b)
		if !HasNewNonAllowedDiff(before, after, tc.FaultPath) {
			t.Fatalf("%s target mismatch %s not detected: %+v", tc.Name, tc.FaultPath, after)
		}
	}
}
func TestServiceBackendsReplayThroughRealAPIs(t *testing.T) {
	for _, tc := range Cases() {
		memoryBackend := NewInMemoryBackend()
		sqliteBackend, err := NewSQLiteBackend(t.TempDir() + "/" + tc.Name)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = memoryBackend.Close() })
		t.Cleanup(func() { _ = sqliteBackend.Close() })
		if err := tc.Run(memoryBackend); err != nil {
			t.Fatal(err)
		}
		if err := tc.Run(sqliteBackend); err != nil {
			t.Fatal(err)
		}
		left, err := memoryBackend.Load()
		if err != nil {
			t.Fatal(err)
		}
		right, err := sqliteBackend.Load()
		if err != nil {
			t.Fatal(err)
		}
		if diffs := Compare(tc.Name, sqliteBackend.Name(), left, right); len(diffs) != 0 {
			t.Fatalf("%s backend mismatch: %+v", tc.Name, diffs)
		}
		for _, result := range []struct {
			name     string
			snapshot Snapshot
		}{{memoryBackend.Name(), left}, {sqliteBackend.Name(), right}} {
			for _, diff := range Compare(tc.Name, result.name, tc.Expected(), result.snapshot) {
				if !diff.Allowed {
					t.Fatalf("%s lost modeled data in %s: %+v", tc.Name, result.name, diff)
				}
			}
		}
		if err := memoryBackend.Close(); err != nil {
			t.Fatal(err)
		}
		if err := sqliteBackend.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestServiceBackendPropagatesJSONConversionErrors(t *testing.T) {
	backend := NewInMemoryBackend()
	t.Cleanup(func() { _ = backend.Close() })
	fixture := base("invalid-json", Event{
		Seq: 1, Role: "assistant", Extensions: map[string]any{"bad": make(chan int)},
	})
	if err := ReplaySnapshot(backend, fixture); err == nil {
		t.Fatal("expected unsupported JSON value to fail replay")
	}
}

func TestCompareReportsNonJSONSnapshots(t *testing.T) {
	invalid := base("invalid-comparison", Event{
		Seq: 1, Role: "assistant", Extensions: map[string]any{"invalid": make(chan int)},
	})
	diffs := Compare("invalid-comparison", "json", invalid, invalid)
	if len(diffs) != 1 || diffs[0].Allowed || diffs[0].Path != "/" ||
		!strings.Contains(fmt.Sprint(diffs[0].Compared), "unsupported type") {
		t.Fatalf("invalid snapshot was silently compared: %+v", diffs)
	}
}

func TestSummaryBoundaryRejectsInvalidGeneratedSequence(t *testing.T) {
	_, err := snapshotSummaryBoundary(
		&frameworksession.SummaryBoundary{Version: 1, FilterKey: "all", LastEventID: "generated"},
		[]frameworkevent.Event{{
			ID: "generated",
			Extensions: map[string]json.RawMessage{
				generatedEventIDKey: json.RawMessage("true"),
				seqKey:              json.RawMessage("not-json"),
			},
		}},
	)
	if err == nil || !strings.Contains(err.Error(), "boundary event sequence") {
		t.Fatalf("invalid generated sequence was ignored: %v", err)
	}
}

func TestFrameworkEventSequenceMetadataIsRequired(t *testing.T) {
	tests := map[string]map[string]json.RawMessage{
		"missing": {},
		"null":    {seqKey: json.RawMessage("null")},
	}
	for name, extensions := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := fromFrameworkEvent(&frameworkevent.Event{Extensions: extensions})
			if err == nil {
				t.Fatalf("%s replay sequence metadata was silently replaced by storage order", name)
			}
		})
	}
}

type closeTrackingBackend struct {
	Backend
	closed   bool
	closeErr error
}

func (b *closeTrackingBackend) Close() error {
	b.closed = true
	return b.closeErr
}

func TestNewReplayBackendsClosesMemoryWhenDiskOpenFails(t *testing.T) {
	openErr := errors.New("open disk backend")
	closeErr := errors.New("close memory backend")
	memoryBackend := &closeTrackingBackend{closeErr: closeErr}
	left, right, err := newReplayBackendsWith(
		"unused",
		func() Backend { return memoryBackend },
		func(string) (Backend, error) { return nil, openErr },
	)
	if left != nil || right != nil || !memoryBackend.closed ||
		!errors.Is(err, openErr) || !errors.Is(err, closeErr) {
		t.Fatalf("disk failure did not close memory backend: left=%v right=%v closed=%t err=%v", left, right, memoryBackend.closed, err)
	}
}
func TestNormalization(t *testing.T) {
	a := Cases()[4].Expected()
	b := clone(a)
	a.Memories[0].Similarity = .91231
	b.Memories[0].Similarity = .91239
	a.Memories[0].ID = "generated-a"
	b.Memories[0].ID = "generated-b"
	if d := Compare("normalize", "json", a, b); len(d) > 0 {
		t.Fatal(d)
	}
}

func TestCompareDoesNotMutateInputs(t *testing.T) {
	input := base("compare-inputs",
		Event{Seq: 2, Role: "assistant", Content: "second", Timestamp: "2026-07-22T00:00:02Z"},
		Event{Seq: 1, Role: "user", Content: "first", Timestamp: "2026-07-22T00:00:01Z"},
	)
	input.Unsupported = nil
	input.Memories = []Memory{{ID: "generated-memory", Content: "memory", Similarity: 0.12345}}
	original := clone(input)

	_ = Compare("no-mutation", "json", input, clone(input))

	if !reflect.DeepEqual(input, original) {
		t.Fatalf("Compare mutated its input:\n got: %#v\nwant: %#v", input, original)
	}
}

func TestNormalizeDoesNotMutateInput(t *testing.T) {
	input := base("normalize-input",
		Event{Seq: 2, Role: "assistant", Content: "second", Timestamp: "2026-07-22T00:00:02Z"},
		Event{Seq: 1, Role: "user", Content: "first", Timestamp: "2026-07-22T00:00:01Z"},
	)
	input.Unsupported = nil
	input.Memories = []Memory{{ID: "generated-memory", Content: "memory", Similarity: 0.12345}}
	original := clone(input)

	normalized := Normalize(input)

	if !reflect.DeepEqual(input, original) {
		t.Fatalf("Normalize mutated its input:\n got: %#v\nwant: %#v", input, original)
	}
	if reflect.DeepEqual(normalized, original) {
		t.Fatal("Normalize did not normalize the detached snapshot")
	}
}

func TestZeroSequenceSummaryBoundaryIsRepresented(t *testing.T) {
	fixture := base("zero-summary-boundary", Event{Seq: 0, Role: "user", Content: "first", FilterKey: "all"})
	fixture.Summaries = []Summary{{
		ID: "summary:all", SessionID: fixture.SessionID, FilterKey: "all", Text: "summary", Version: 1,
	}}
	fixture.Summaries[0].Boundary = expectedSummaryBoundary(fixture.Events, "all")
	boundary := fixture.Summaries[0].Boundary
	data, err := json.Marshal(boundary)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"last_event_seq":0`) {
		t.Fatalf("zero sequence boundary lost its locator: %s", data)
	}
	for _, backend := range []Backend{NewInMemoryBackend(), mustSQLiteBackend(t)} {
		backend := backend
		t.Run(backend.Name(), func(t *testing.T) {
			t.Cleanup(func() { _ = backend.Close() })
			if err := ReplaySnapshot(backend, fixture); err != nil {
				t.Fatal(err)
			}
			got, err := backend.Load()
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Summaries) != 1 || got.Summaries[0].Boundary == nil ||
				got.Summaries[0].Boundary.LastEventSeq == nil || *got.Summaries[0].Boundary.LastEventSeq != 0 {
				t.Fatalf("zero sequence boundary did not round-trip: %+v", got.Summaries)
			}
			if diffs := Compare("zero-summary-boundary", backend.Name(), fixture, got); hasNonAllowed(diffs) {
				t.Fatalf("zero sequence boundary changed: %+v", diffs)
			}
		})
	}
}

type recordingBackend struct {
	Backend
	updates []struct {
		key   string
		value any
	}
}

func (b *recordingBackend) UpdateState(key string, value any) error {
	b.updates = append(b.updates, struct {
		key   string
		value any
	}{key: key, value: value})
	return b.Backend.UpdateState(key, value)
}

func TestStateCaseExercisesDeletion(t *testing.T) {
	backend := &recordingBackend{Backend: NewInMemoryBackend()}
	t.Cleanup(func() { _ = backend.Close() })
	if err := stateUpdateCase().Run(backend); err != nil {
		t.Fatal(err)
	}
	for _, update := range backend.updates {
		if update.key == "removed" && update.value == nil {
			return
		}
	}
	t.Fatalf("state replay case did not exercise deletion: %+v", backend.updates)
}

func TestUnsupportedCapabilityMarksMatchingDiffAllowed(t *testing.T) {
	a := Cases()[7].Expected()
	b := clone(a)
	b.Tracks[0].Error = "not persisted"
	b.Unsupported = map[string]string{"/tracks": "backend does not persist track details"}

	d := Compare("unsupported-track", "limited", a, b)
	if len(d) != 1 || !d[0].Allowed || d[0].Explanation != b.Unsupported["/tracks"] {
		t.Fatalf("expected documented track difference to be allowed: %+v", d)
	}
}

func TestUnsupportedCapabilityDoesNotAllowOtherDiffs(t *testing.T) {
	a := Cases()[5].Expected()
	b := clone(a)
	b.Summaries[0].Text = "lost"
	b.Unsupported = map[string]string{"/tracks": "backend does not persist tracks"}

	d := Compare("summary-loss", "limited", a, b)
	if len(d) != 1 || d[0].Allowed {
		t.Fatalf("unrelated data loss must remain disallowed: %+v", d)
	}
}

func TestBackendCapabilityPathsUseCanonicalOrder(t *testing.T) {
	backend := NewInMemoryBackend()
	t.Cleanup(func() { _ = backend.Close() })
	service := backend.(*serviceBackend)
	fixture := base("canonical-capabilities")
	fixture.Memories = []Memory{
		{ID: "z", Content: "z-memory", Scope: "session", Metadata: map[string]any{"private": true}},
		{ID: "a", Content: "a-memory", Scope: "user"},
	}
	fixture.Summaries = []Summary{
		{ID: "custom-z", FilterKey: "z", Text: "z"},
		{ID: "summary:a", FilterKey: "a", Text: "a"},
	}
	if err := service.Begin(fixture); err != nil {
		t.Fatal(err)
	}
	if _, ok := service.unsupported["/memories/1/scope"]; !ok {
		t.Fatalf("scope exception did not follow canonical memory order: %+v", service.unsupported)
	}
	if _, ok := service.unsupported["/memories/0/scope"]; ok {
		t.Fatalf("scope exception moved to a different memory: %+v", service.unsupported)
	}
	if _, ok := service.unsupported["/memories/1/metadata/private"]; !ok {
		t.Fatalf("metadata exception did not follow canonical memory order: %+v", service.unsupported)
	}
	if _, ok := service.unsupported["/summaries/1/id"]; !ok {
		t.Fatalf("summary ID exception did not follow canonical summary order: %+v", service.unsupported)
	}
}

func TestMetadataAllowanceDoesNotCoverPersistedTopics(t *testing.T) {
	backend := NewInMemoryBackend()
	t.Cleanup(func() { _ = backend.Close() })
	fixture := base("mixed-metadata")
	fixture.Memories = []Memory{{
		Content: "memory", Scope: "user",
		Metadata: map[string]any{"topics": []string{"kept"}, "private": true},
	}}
	if err := ReplaySnapshot(backend, fixture); err != nil {
		t.Fatal(err)
	}
	got, err := backend.Load()
	if err != nil {
		t.Fatal(err)
	}
	got.Memories[0].Metadata["topics"] = []string{"changed"}
	diffs := Compare("mixed-metadata", backend.Name(), fixture, got)
	foundTopic := false
	foundPrivate := false
	for _, diff := range diffs {
		if diff.Path == "/memories/0/metadata/topics/0" {
			foundTopic = true
			if diff.Allowed {
				t.Fatalf("persisted topics were covered by metadata allowance: %+v", diffs)
			}
		}
		if diff.Path == "/memories/0/metadata/private" {
			foundPrivate = true
			if !diff.Allowed {
				t.Fatalf("unsupported private metadata was not allowed: %+v", diffs)
			}
		}
	}
	if !foundTopic {
		t.Fatalf("topic mutation was not detected: %+v", diffs)
	}
	if !foundPrivate {
		t.Fatalf("private metadata loss was not reported: %+v", diffs)
	}
}

func TestUnexpectedStateIsNotErased(t *testing.T) {
	backend := NewInMemoryBackend()
	t.Cleanup(func() { _ = backend.Close() })
	fixture := base("unexpected-state")
	if err := backend.Begin(fixture); err != nil {
		t.Fatal(err)
	}
	if err := backend.UpdateState("unexpected", true); err != nil {
		t.Fatal(err)
	}
	got, err := backend.Load()
	if err != nil {
		t.Fatal(err)
	}
	diffs := Compare("unexpected-state", backend.Name(), fixture, got)
	if len(diffs) != 1 || diffs[0].Path != "/state/unexpected" || diffs[0].Allowed {
		t.Fatalf("unexpected state was hidden: %+v", diffs)
	}
}

func TestSummaryBoundaryMutationIsDetected(t *testing.T) {
	fixture := Cases()[6].Expected()
	backend := NewInMemoryBackend()
	t.Cleanup(func() { _ = backend.Close() })
	if err := ReplaySnapshot(backend, fixture); err != nil {
		t.Fatal(err)
	}
	got, err := backend.Load()
	if err != nil {
		t.Fatal(err)
	}
	if diffs := Compare("summary-boundary", backend.Name(), fixture, got); hasNonAllowed(diffs) {
		t.Fatalf("boundary did not round-trip: %+v", diffs)
	}
	(*got.Summaries[0].Boundary.LastEventSeq)--
	diffs := Compare("summary-boundary", backend.Name(), fixture, got)
	if len(diffs) == 0 || !hasNonAllowed(diffs) {
		t.Fatalf("boundary mutation was not detected: %+v", diffs)
	}
	got.Summaries[0].Boundary = nil
	diffs = Compare("summary-boundary", backend.Name(), fixture, got)
	if len(diffs) == 0 || !hasNonAllowed(diffs) {
		t.Fatalf("missing boundary was not detected: %+v", diffs)
	}
}

func TestStableSummaryBoundaryMutationIsDetected(t *testing.T) {
	fixture := base("stable-summary-boundary", Event{
		ID: "stable-event", Seq: 1, Role: "user", Content: "summarize", FilterKey: "all",
	})
	fixture.Summaries = []Summary{{
		ID: "summary:all", SessionID: fixture.SessionID, FilterKey: "all", Text: "summary", Version: 1,
	}}
	fixture.Summaries[0].Boundary = expectedSummaryBoundary(fixture.Events, "all")
	backend := NewInMemoryBackend()
	t.Cleanup(func() { _ = backend.Close() })
	if err := ReplaySnapshot(backend, fixture); err != nil {
		t.Fatal(err)
	}
	got, err := backend.Load()
	if err != nil {
		t.Fatal(err)
	}
	if diffs := Compare("stable-summary-boundary", backend.Name(), fixture, got); hasNonAllowed(diffs) {
		t.Fatalf("stable boundary did not round-trip: %+v", diffs)
	}
	got.Summaries[0].Boundary.LastEventID = "wrong-event"
	if diffs := Compare("stable-summary-boundary", backend.Name(), fixture, got); !hasNonAllowed(diffs) {
		t.Fatalf("stable boundary mutation was not detected: %+v", diffs)
	}
}

func TestBackendCapabilityComparisonIsSymmetric(t *testing.T) {
	withTrack := Cases()[7].Expected()
	withoutTrack := clone(withTrack)
	withoutTrack.Tracks = nil
	withoutTrack.Unsupported = map[string]string{"/tracks": "track capability unavailable"}
	for _, pair := range []struct{ left, right Snapshot }{
		{withoutTrack, withTrack}, {withTrack, withoutTrack},
	} {
		diffs := CompareBackends("symmetric", "limited-vs-full", pair.left, pair.right)
		if len(diffs) != 1 || !diffs[0].Allowed {
			t.Fatalf("capability handling depends on argument order: %+v", diffs)
		}
	}
}

func TestRunReplayCompletesDetectedInjectionCampaign(t *testing.T) {
	path := t.TempDir() + "/report.json"
	if err := runReplay(path, true); err != nil {
		t.Fatalf("complete injection campaign failed: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("report was not written: %v", err)
	}
}

func TestValidateReplayReportRejectsNormalNonAllowedDifferences(t *testing.T) {
	report := Report{Differences: []Difference{{Allowed: true}, {Allowed: false}, {Allowed: false}}}
	err := validateReplayReport(report, false)
	if err == nil || !strings.Contains(err.Error(), "2 non-allowed") {
		t.Fatalf("normal non-allowed differences were not rejected: %v", err)
	}
}

func TestValidateReplayReportAcceptsCompleteInjectionCampaign(t *testing.T) {
	report := Report{
		Cases: 2, DetectedInjected: 2,
		Differences: []Difference{{Allowed: false}, {Allowed: false}},
	}
	if err := validateReplayReport(report, true); err != nil {
		t.Fatalf("complete injection campaign was rejected: %v", err)
	}
}

func TestValidateReplayReportRejectsIncompleteInjectionCampaign(t *testing.T) {
	err := validateReplayReport(Report{Cases: 3, DetectedInjected: 2}, true)
	if err == nil || !strings.Contains(err.Error(), "detected 2 of 3") {
		t.Fatalf("incomplete injection campaign was not rejected: %v", err)
	}
}

func hasNonAllowed(diffs []Difference) bool {
	for _, diff := range diffs {
		if !diff.Allowed {
			return true
		}
	}
	return false
}

func TestStableEventIDRoundTrips(t *testing.T) {
	backend := NewInMemoryBackend()
	t.Cleanup(func() { _ = backend.Close() })
	fixture := base("stable-id", Event{ID: "caller-stable", Seq: 1, Role: "user", Content: "once"})
	if err := ReplaySnapshot(backend, fixture); err != nil {
		t.Fatal(err)
	}
	got, err := backend.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Events) != 1 || got.Events[0].ID != "caller-stable" {
		t.Fatalf("stable event ID was not preserved: %+v", got.Events)
	}
	mutated := clone(got)
	mutated.Events[0].ID = "rewritten"
	if diffs := Compare("stable-id", backend.Name(), fixture, mutated); len(diffs) == 0 || diffs[0].Allowed {
		t.Fatalf("stable ID rewrite was not detected: %+v", diffs)
	}
}

func TestToolCallIDsRoundTripAcrossInterleavedResults(t *testing.T) {
	fixture := base("interleaved-tools",
		Event{Seq: 0, Role: "user", Content: "run both tools"},
		Event{Seq: 1, Role: "assistant", Tool: "first", ToolCallID: "call-a", Args: map[string]any{"n": 1}},
		Event{Seq: 2, Role: "assistant", Tool: "second", ToolCallID: "call-b", Args: map[string]any{"n": 2}},
		Event{Seq: 3, Role: "tool", Tool: "second", ToolResultID: "call-b", Response: map[string]any{"ok": "b"}},
		Event{Seq: 4, Role: "tool", Tool: "first", ToolResultID: "call-a", Response: map[string]any{"ok": "a"}},
	)
	backend := NewInMemoryBackend()
	t.Cleanup(func() { _ = backend.Close() })
	if err := ReplaySnapshot(backend, fixture); err != nil {
		t.Fatal(err)
	}
	got, err := backend.Load()
	if err != nil {
		t.Fatal(err)
	}
	if diffs := Compare("interleaved-tools", backend.Name(), fixture, got); len(diffs) != 0 {
		t.Fatalf("tool IDs did not round-trip: %+v", diffs)
	}
}

func TestPlainTextToolResultPreservesContentAndCorrelationID(t *testing.T) {
	fixture := base("plain-tool-result",
		Event{Seq: 1, Role: "user", Content: "run plain tool"},
		Event{Seq: 2, Role: "assistant", Tool: "plain", ToolCallID: "call-text", Args: map[string]any{"value": 1}},
		Event{Seq: 3, Role: "tool", Tool: "plain", ToolResultID: "call-text", Content: "ok"},
	)
	for _, backend := range []Backend{NewInMemoryBackend(), mustSQLiteBackend(t)} {
		backend := backend
		t.Run(backend.Name(), func(t *testing.T) {
			t.Cleanup(func() { _ = backend.Close() })
			if err := ReplaySnapshot(backend, fixture); err != nil {
				t.Fatal(err)
			}
			got, err := backend.Load()
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Events) != 3 || got.Events[2].Content != "ok" || got.Events[2].ToolResultID != "call-text" || got.Events[2].Response != nil {
				t.Fatalf("plain tool result did not round-trip: %+v", got.Events)
			}
			if diffs := Compare("plain-tool-result", backend.Name(), fixture, got); len(diffs) != 0 {
				t.Fatalf("plain tool result changed: %+v", diffs)
			}
		})
	}
}

func TestServiceBackendLoadsMoreThanOneHundredMemories(t *testing.T) {
	fixture := base("memory-101")
	for i := 0; i < 101; i++ {
		fixture.Memories = append(fixture.Memories, Memory{ID: fmt.Sprintf("m-%03d", i), Content: fmt.Sprintf("memory-%03d", i), Scope: "user"})
	}
	for _, backend := range []Backend{NewInMemoryBackend(), mustSQLiteBackend(t)} {
		backend := backend
		t.Run(backend.Name(), func(t *testing.T) {
			t.Cleanup(func() { _ = backend.Close() })
			if err := ReplaySnapshot(backend, fixture); err != nil {
				t.Fatal(err)
			}
			got, err := backend.Load()
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Memories) != len(fixture.Memories) {
				t.Fatalf("memory load truncated: got %d want %d", len(got.Memories), len(fixture.Memories))
			}
		})
	}
}

func mustSQLiteBackend(t *testing.T) Backend {
	t.Helper()
	backend, err := NewSQLiteBackend(t.TempDir() + "/memory-101")
	if err != nil {
		t.Fatal(err)
	}
	return backend
}
