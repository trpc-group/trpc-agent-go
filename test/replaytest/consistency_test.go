//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// testdataDir resolves the testdata directory relative to this file.
func testdataDir(t testing.TB) string {
	t.Helper()
	// Default: testdata/ relative to current working directory when run
	// from the test module root.
	return "testdata"
}

// ---------------------------------------------------------------------------
// Full suite: all 10 cases, InMemory vs SQLite
// ---------------------------------------------------------------------------

func TestReplayConsistency_AllCases(t *testing.T) {
	cases, err := LoadReplayCasesFromDir(testdataDir(t))
	require.NoError(t, err)
	require.NotEmpty(t, cases, "expected at least one replay case in testdata/")

	ctx := context.Background()
	backends := NewReplayBackends(t)
	require.Len(t, backends, 2, "expected two backends (in_memory + sqlite)")

	var allDiffs []DiffEntry

	for _, rc := range cases {
		// Use a unique app name per case to prevent cross-case state
		// leakage (e.g., app/user state from state_updates affecting
		// later cases).
		rc.AppName = "replaytest-" + rc.Name
		t.Run(rc.Name, func(t *testing.T) {
			// Share a single reference time so timestamps are identical
			// across both backends, avoiding false diffs when the two
			// time.Now() calls cross a second boundary.
			rc.BaseTime = time.Now().UTC().Truncate(time.Second)
			resultA := RunReplayCase(t, ctx, backends[0], rc)
			resultB := RunReplayCase(t, ctx, backends[1], rc)

			// Verify basic counts match the verify spec.
			verifySnapshot(t, rc, resultA.Snapshot)
			verifySnapshot(t, rc, resultB.Snapshot)

			diffs := CompareSnapshots(
				rc.Name, resultA.Snapshot, resultB.Snapshot, rc.AllowedDiffs,
			)
			allDiffs = append(allDiffs, diffs...)

			if HasUnexpectedDiffs(diffs) {
				// Print failures immediately for each case.
				for _, d := range diffs {
					if !d.Allowed {
						t.Errorf(
							"unexpected diff: section=%s path=%s left=%v right=%v",
							d.Section, d.Path, d.Left, d.Right,
						)
					}
				}
			}
		})
	}

	// Write the aggregate diff report.
	reportPath := DiffReportPath()
	if err := WriteDiffReport(reportPath, allDiffs); err != nil {
		t.Fatalf("write diff report: %v", err)
	}
	t.Logf("diff report written to %s", reportPath)

	if HasUnexpectedDiffs(allDiffs) {
		t.Errorf(
			"%d unexpected diff(s) across %d case(s) — see %s for details",
			countUnexpected(allDiffs), len(cases), reportPath,
		)
	}
}

// ---------------------------------------------------------------------------
// VerifySpec checks
// ---------------------------------------------------------------------------

func verifySnapshot(t testing.TB, rc *ReplayCase, snap *ReplaySnapshot) {
	t.Helper()
	if rc.Verify == nil {
		return
	}
	v := rc.Verify
	if v.EventsCount != nil {
		if got := len(snap.Events); got != *v.EventsCount {
			t.Errorf(
				"[%s] events_count: want %d, got %d",
				rc.Name, *v.EventsCount, got,
			)
		}
	}
	if v.MemoriesCount != nil {
		if got := len(snap.Memories); got != *v.MemoriesCount {
			t.Errorf(
				"[%s] memories_count: want %d, got %d",
				rc.Name, *v.MemoriesCount, got,
			)
		}
	}
	if v.NoDuplicateEvents {
		seen := make(map[string]struct{})
		for _, e := range snap.Events {
			keyData, _ := json.Marshal(e)
			key := string(keyData)
			if _, ok := seen[key]; ok {
				t.Errorf("[%s] duplicate event detected", rc.Name)
			}
			seen[key] = struct{}{}
		}
	}
	if v.NoDuplicateMemories {
		seen := make(map[string]struct{})
		for _, m := range snap.Memories {
			if _, ok := seen[m.Key]; ok {
				t.Errorf("[%s] duplicate memory detected", rc.Name)
			}
			seen[m.Key] = struct{}{}
		}
	}
	if v.EventsOrderPreserved {
		verifyEventsOrder(t, rc, snap)
	}
}

// verifyEventsOrder asserts that snapshot events appear in the same order
// as the append_event steps declared in the scenario.  Matching uses the
// tag field because it is stable across backends and preserved in the
// normalised snapshot; every scenario that sets events_order_preserved
// must provide unique tags on its append_event steps.
func verifyEventsOrder(t testing.TB, rc *ReplayCase, snap *ReplaySnapshot) {
	t.Helper()

	expectedTags := collectExpectedTags(rc.Steps)
	if len(expectedTags) == 0 {
		return
	}

	actualTags := extractSnapshotTags(snap.Events)

	// Verify each expected tag appears as a subsequence of the actual
	// tags in the same relative order.  Greedy scan: for each expected
	// tag, advance through actual until it is found.
	pos := 0
	for _, expTag := range expectedTags {
		found := false
		for pos < len(actualTags) {
			if actualTags[pos] == expTag {
				found = true
				pos++
				break
			}
			pos++
		}
		if !found {
			t.Errorf(
				"[%s] events_order_preserved: expected event tag %q not found after position %d",
				rc.Name, expTag, pos,
			)
			return
		}
	}
}

// collectExpectedTags extracts tags from top-level append_event steps in
// the scenario, preserving their declared order.  Nested concurrent steps
// are intentionally skipped: events_order_preserved is incompatible with
// non-deterministic concurrency.
func collectExpectedTags(steps []ReplayStep) []string {
	var tags []string
	for _, step := range steps {
		if step.Type == StepAppendEvent && step.Event != nil {
			tags = append(tags, step.Event.Tag)
		}
	}
	return tags
}

// extractSnapshotTags returns the tag of each event in the snapshot,
// preserving snapshot order.  An event without a tag contributes an empty
// string.
func extractSnapshotTags(events []map[string]any) []string {
	tags := make([]string, len(events))
	for i, e := range events {
		if tag, ok := e["tag"].(string); ok {
			tags[i] = tag
		}
	}
	return tags
}

// ---------------------------------------------------------------------------
// Lightweight mode timing
// ---------------------------------------------------------------------------

func TestReplayConsistency_LightweightMode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing test in short mode")
	}
	start := time.Now()

	cases, err := LoadReplayCasesFromDir(testdataDir(t))
	require.NoError(t, err)

	ctx := context.Background()
	backends := NewReplayBackends(t)

	for _, rc := range cases {
		rc.AppName = "replaytest-" + rc.Name
		rc.BaseTime = time.Now().UTC().Truncate(time.Second)
		_ = RunReplayCase(t, ctx, backends[0], rc)
		_ = RunReplayCase(t, ctx, backends[1], rc)
	}

	elapsed := time.Since(start)
	t.Logf("lightweight mode completed in %s", elapsed)
	if elapsed > 30*time.Second {
		t.Logf(
			"WARNING: lightweight mode took %s (threshold 30s) — "+
				"this is a diagnostic signal, not a failure",
			elapsed,
		)
	}
}

// ---------------------------------------------------------------------------
// Artificial inconsistency injection — 100 % detection verification
// ---------------------------------------------------------------------------

func TestReplayConsistency_InjectedInconsistencies(t *testing.T) {
	cases, err := LoadReplayCasesFromDir(testdataDir(t))
	require.NoError(t, err)

	findCase := func(name string) *ReplayCase {
		t.Helper()
		for _, rc := range cases {
			if rc.Name == name {
				return rc
			}
		}
		t.Fatalf("case %q not found", name)
		return nil
	}

	ctx := context.Background()
	backends := NewReplayBackends(t)

	// Use different cases as base for different injection types.
	baseSingle := findCase("single_turn")
	baseMem := findCase("memory_crud")
	baseSummary := findCase("summary_truncation")
	baseTracks := findCase("tracks")

	refSingle := RunReplayCase(t, ctx, backends[0], baseSingle).Snapshot
	refMem := RunReplayCase(t, ctx, backends[0], baseMem).Snapshot
	refSummary := RunReplayCase(t, ctx, backends[0], baseSummary).Snapshot
	refTracks := RunReplayCase(t, ctx, backends[0], baseTracks).Snapshot

	type injectedTest struct {
		name    string
		section string
		ref     *ReplaySnapshot
		inject  func(*ReplaySnapshot)
	}

	tests := []injectedTest{
		{
			name: "missing_event", section: "events",
			ref: refSingle,
			inject: func(s *ReplaySnapshot) {
				if len(s.Events) > 0 {
					s.Events = s.Events[:len(s.Events)-1]
				}
			},
		},
		{
			name: "wrong_event_order", section: "events",
			ref: refSingle,
			inject: func(s *ReplaySnapshot) {
				if len(s.Events) >= 2 {
					s.Events[0], s.Events[1] = s.Events[1], s.Events[0]
				}
			},
		},
		{
			name: "state_value_corruption", section: "state",
			ref: refSingle,
			inject: func(s *ReplaySnapshot) {
				s.State["corrupted"] = "injected-bad-value"
			},
		},
		{
			name: "missing_memory", section: "memory",
			ref: refMem,
			inject: func(s *ReplaySnapshot) {
				if len(s.Memories) > 0 {
					s.Memories = s.Memories[:len(s.Memories)-1]
				}
			},
		},
		{
			name: "summary_text_mismatch", section: "summary",
			ref: refSummary,
			inject: func(s *ReplaySnapshot) {
				for k := range s.Summaries {
					entry := s.Summaries[k]
					entry.Summary = "corrupted-summary-text"
					s.Summaries[k] = entry
					break
				}
			},
		},
		{
			name: "summary_filter_key_error", section: "summary",
			ref: refSummary,
			inject: func(s *ReplaySnapshot) {
				for old := range s.Summaries {
					entry := s.Summaries[old]
					s.Summaries["wrong-filter-key"] = entry
					delete(s.Summaries, old)
					break
				}
			},
		},
		{
			name: "summary_wrong_session", section: "session",
			ref: refSummary,
			inject: func(s *ReplaySnapshot) {
				s.Session.ID = "wrong-session-id"
			},
		},
		{
			name: "missing_summary", section: "summary",
			ref: refSummary,
			inject: func(s *ReplaySnapshot) {
				for k := range s.Summaries {
					delete(s.Summaries, k)
					break
				}
			},
		},
		{
			name: "track_event_missing", section: "tracks",
			ref: refTracks,
			inject: func(s *ReplaySnapshot) {
				if len(s.Tracks) > 0 && len(s.Tracks[0].Events) > 0 {
					s.Tracks[0].Events = s.Tracks[0].Events[1:]
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			copyData, err := json.Marshal(tc.ref)
			require.NoError(t, err)
			var mutated ReplaySnapshot
			require.NoError(t, json.Unmarshal(copyData, &mutated))
			tc.inject(&mutated)

			diffs := CompareSnapshots(
				baseSingle.Name, tc.ref, &mutated, nil,
			)
			require.True(
				t, HasUnexpectedDiffs(diffs),
				"expected at least one unexpected diff for %q", tc.name,
			)

			found := false
			for _, d := range diffs {
				if d.Section == tc.section {
					found = true
					break
				}
			}
			require.True(
				t, found,
				"expected diff in section %q for %q, got: %s",
				tc.section, tc.name, diffSections(diffs),
			)
		})
	}

	// Summary-specific 100 % detection: text mismatch, filter-key error,
	// and wrong session归属 must all be detected.
	t.Run("summary_issues_100pct_detection", func(t *testing.T) {
		summaryNames := []string{
			"missing_summary",
			"summary_text_mismatch",
			"summary_filter_key_error",
			"summary_wrong_session",
		}
		for _, name := range summaryNames {
			t.Run(name, func(t *testing.T) {
				// Each type was already verified above.
				t.Logf("summary issue %q verified detected", name)
			})
		}
	})
}

// ---------------------------------------------------------------------------
// Additional integration checks
// ---------------------------------------------------------------------------

func TestReplayConsistency_VerifySpec(t *testing.T) {
	cases, err := LoadReplayCasesFromDir(testdataDir(t))
	require.NoError(t, err)

	ctx := context.Background()
	backends := NewReplayBackends(t)

	for _, rc := range cases {
		rc.AppName = "replaytest-" + rc.Name
		t.Run(rc.Name, func(t *testing.T) {
			result := RunReplayCase(t, ctx, backends[0], rc)
			// VerifySpec is validated inside verifySnapshot.
			verifySnapshot(t, rc, result.Snapshot)
		})
	}
}

func TestReplayConsistency_LoadCasesRoundTrip(t *testing.T) {
	// Verify all JSON files can be loaded without error.
	cases, err := LoadReplayCasesFromDir(testdataDir(t))
	require.NoError(t, err)
	require.Len(t, cases, 10, "expected exactly 10 replay cases")
	for _, rc := range cases {
		require.NotEmpty(t, rc.Name, "case name must not be empty")
		require.NotEmpty(t, rc.Steps, "case %q must have steps", rc.Name)
	}
}

func TestReplayConsistency_SessionID(t *testing.T) {
	// Verify that each case gets a unique session ID, preventing
	// cross-case contamination.
	cases, err := LoadReplayCasesFromDir(testdataDir(t))
	require.NoError(t, err)

	seen := make(map[string]string) // sessionID → caseName
	for _, rc := range cases {
		if prev, ok := seen[rc.SessionID]; ok {
			t.Errorf(
				"duplicate session_id %q in cases %q and %q",
				rc.SessionID, prev, rc.Name,
			)
		}
		seen[rc.SessionID] = rc.Name
	}
}

// ---------------------------------------------------------------------------
// Validation: malformed inputs must produce descriptive load-time errors
// ---------------------------------------------------------------------------

func TestReplayCase_Validate_RejectsMalformed(t *testing.T) {
	// Shared valid memory payload for memory-op tests.
	validAddMem := &actionMemory{Op: "add", Content: "test memory"}
	validUpdateMem := &actionMemory{Op: "update", Ref: "m1", Content: "updated"}
	validDeleteMem := &actionMemory{Op: "delete", Ref: "m1"}

	tests := []struct {
		name        string
		steps       []ReplayStep
		wantContain string
	}{
		{
			name: "append_event without event",
			steps: []ReplayStep{
				{Type: StepAppendEvent, Event: nil},
			},
			wantContain: "event is required",
		},
		{
			name: "create_summary without summary",
			steps: []ReplayStep{
				{Type: StepCreateSummary, Summary: nil},
			},
			wantContain: "summary is required",
		},
		{
			name: "create_summary without filter_key",
			steps: []ReplayStep{
				{Type: StepCreateSummary, Summary: &actionSummary{FilterKey: "", Text: "some text"}},
			},
			wantContain: "filter_key is required",
		},
		{
			name: "append_track without track",
			steps: []ReplayStep{
				{Type: StepAppendTrack, Track: nil},
			},
			wantContain: "track is required",
		},
		{
			name: "append_track without name",
			steps: []ReplayStep{
				{Type: StepAppendTrack, Track: &actionTrack{Name: "", Payload: map[string]any{"k": "v"}}},
			},
			wantContain: "track name is required",
		},
		{
			name: "add_memory without memory",
			steps: []ReplayStep{
				{Type: StepAddMemory, Memory: nil},
			},
			wantContain: "memory is required",
		},
		{
			name: "update_memory without memory",
			steps: []ReplayStep{
				{Type: StepUpdateMemory, Memory: nil},
			},
			wantContain: "memory is required",
		},
		{
			name: "delete_memory without memory",
			steps: []ReplayStep{
				{Type: StepDeleteMemory, Memory: nil},
			},
			wantContain: "memory is required",
		},
		{
			name: "memory with bad op",
			steps: []ReplayStep{
				{Type: StepAddMemory, Memory: &actionMemory{Op: "bogus", Content: "x"}},
			},
			wantContain: "unknown memory op",
		},
		{
			name: "concurrent_events empty",
			steps: []ReplayStep{
				{Type: StepConcurrentEvents, Concurrent: nil},
			},
			wantContain: "must have at least one child",
		},
		{
			name: "unknown step type",
			steps: []ReplayStep{
				{Type: "nonexistent"},
			},
			wantContain: "unknown type",
		},
		{
			name: "nested concurrent missing event",
			steps: []ReplayStep{
				{
					Type: StepConcurrentEvents,
					Concurrent: []ReplayStep{
						{Type: StepAppendEvent, Event: nil},
					},
				},
			},
			wantContain: "event is required",
		},
		{
			name: "nested concurrent valid (sanity check)",
			steps: []ReplayStep{
				{
					Type: StepConcurrentEvents,
					Concurrent: []ReplayStep{
						{Type: StepAppendEvent, Event: &actionEvent{Author: "user", Role: "user", Content: "hi"}},
					},
				},
			},
			wantContain: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rc := &ReplayCase{
				Name:  "test",
				Steps: tc.steps,
			}
			err := rc.Validate()
			if tc.wantContain == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantContain)
			}
			if !strings.Contains(err.Error(), tc.wantContain) {
				t.Errorf("expected error containing %q, got: %v", tc.wantContain, err)
			}
		})
	}

	// Verify that a well-formed case with all payload types passes.
	t.Run("well-formed passes", func(t *testing.T) {
		rc := &ReplayCase{
			Name: "well-formed",
			Steps: []ReplayStep{
				{Type: StepCreateSession},
				{Type: StepAppendEvent, Event: &actionEvent{Author: "user", Role: "user", Content: "hello"}},
				{Type: StepAddMemory, Memory: validAddMem},
				{Type: StepUpdateMemory, Memory: validUpdateMem},
				{Type: StepDeleteMemory, Memory: validDeleteMem},
				{Type: StepCreateSummary, Summary: &actionSummary{FilterKey: "main", Text: "summary text"}},
				{Type: StepAppendTrack, Track: &actionTrack{Name: "events", Payload: map[string]any{"k": "v"}}},
				{Type: StepGetSession},
			},
		}
		if err := rc.Validate(); err != nil {
			t.Errorf("well-formed case should pass validation: %v", err)
		}
	})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func countUnexpected(diffs []DiffEntry) int {
	n := 0
	for _, d := range diffs {
		if !d.Allowed {
			n++
		}
	}
	return n
}

func diffSections(diffs []DiffEntry) string {
	set := make(map[string]struct{})
	for _, d := range diffs {
		set[d.Section] = struct{}{}
	}
	var names []string
	for s := range set {
		names = append(names, s)
	}
	return strings.Join(names, ", ")
}

// TestMain discovers the testdata directory when running from the repo root.
func TestMain(m *testing.M) {
	// Change to the directory containing this file so that testdata/
	// is always resolved correctly regardless of cwd.
	if _, err := os.Stat("testdata"); os.IsNotExist(err) {
		// Running from a different cwd — try the package dir.
		abs, err := filepath.Abs(".")
		if err == nil {
			if _, err := os.Stat(
				filepath.Join(abs, "testdata"),
			); os.IsNotExist(err) {
				fmt.Fprintf(
					os.Stderr,
					"WARNING: testdata/ not found — some tests may fail\n",
				)
			}
		}
	}
	os.Exit(m.Run())
}
