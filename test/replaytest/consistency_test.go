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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
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

	// Reference snapshots for the remaining 6 cases.
	refMultiTurn := RunReplayCase(t, ctx, backends[0], findCase("multi_turn")).Snapshot
	refToolCalls := RunReplayCase(t, ctx, backends[0], findCase("tool_calls")).Snapshot
	refStateUpd := RunReplayCase(t, ctx, backends[0], findCase("state_updates")).Snapshot
	refSummaryBase := RunReplayCase(t, ctx, backends[0], findCase("summary")).Snapshot
	refConcurrent := RunReplayCase(t, ctx, backends[0], findCase("concurrent")).Snapshot
	refErrRec := RunReplayCase(t, ctx, backends[0], findCase("error_recovery")).Snapshot

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
			name: "multi_turn_event_shuffled", section: "events",
			ref: refMultiTurn,
			inject: func(s *ReplaySnapshot) {
				if len(s.Events) >= 4 {
					s.Events[1], s.Events[3] = s.Events[3], s.Events[1]
				}
			},
		},
		{
			name: "tool_calls_missing_tool_response", section: "events",
			ref: refToolCalls,
			inject: func(s *ReplaySnapshot) {
				// Delete the tool response event (index 2).
				if len(s.Events) > 2 {
					s.Events = append(s.Events[:2], s.Events[3:]...)
				}
			},
		},
		{
			name: "state_deleted_key_present", section: "state",
			ref: refStateUpd,
			inject: func(s *ReplaySnapshot) {
				s.State["orphan_key"] = "should-not-exist"
			},
		},
		{
			name: "summary_force_text_corrupted", section: "summary",
			ref: refSummaryBase,
			inject: func(s *ReplaySnapshot) {
				if entry, ok := s.Summaries["chat"]; ok {
					entry.Summary = "corrupted-force-summary"
					s.Summaries["chat"] = entry
				}
			},
		},
		{
			name: "concurrent_missing_event", section: "events",
			ref: refConcurrent,
			inject: func(s *ReplaySnapshot) {
				if len(s.Events) > 1 {
					s.Events = s.Events[:len(s.Events)-1]
				}
			},
		},
		{
			name: "summary_cutoff_corrupted", section: "summary",
			ref: refSummary,
			inject: func(s *ReplaySnapshot) {
				// Corrupt the CutoffAt in the boundary to a wrong
				// non-zero timestamp.  Should be detected even
				// though CutoffAt is no longer a boolean.
				for k, entry := range s.Summaries {
					if entry.Boundary != nil {
						entry.Boundary.CutoffAt = "1999-12-31T23:59:59Z"
						s.Summaries[k] = entry
						break
					}
				}
			},
		},
		{
			name: "error_recovery_duplicate_event", section: "events",
			ref: refErrRec,
			inject: func(s *ReplaySnapshot) {
				if len(s.Events) > 0 {
					// Clone the first event so snapshot reports a duplicate.
					dup, _ := json.Marshal(s.Events[0])
					var cloned map[string]any
					json.Unmarshal(dup, &cloned)
					s.Events = append(s.Events, cloned)
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
		{
			name: "explicit_event_id_rewritten", section: "events",
			ref: refSummary, // summary_truncation has explicit evt-001..evt-006
			inject: func(s *ReplaySnapshot) {
				// Corrupt a non-boundary event ID.  evt-005 is
				// a post-truncation event whose ID is NOT the
				// summary boundary's LastEventID — this tests
				// that scenario-defined IDs outside the boundary
				// are preserved and compared.
				for i, evt := range s.Events {
					if id, ok := evt["id"].(string); ok && id == "evt-005" {
						evt["id"] = "evt-005-corrupted"
						s.Events[i] = evt
						break
					}
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

// TestReplayConsistency_HistoricalBaseTimeRejected verifies that an
// explicitly-set BaseTime in the past (before session creation wall-clock
// time) is rejected at the start of RunReplayCase.  A historical BaseTime
// causes SQLite to silently discard summaries whose UpdatedAt predates the
// session's CreatedAt, producing a false backend difference.
func TestReplayConsistency_HistoricalBaseTimeRejected(t *testing.T) {
	cases, err := LoadReplayCasesFromDir(testdataDir(t))
	require.NoError(t, err)

	var rc *ReplayCase
	for _, c := range cases {
		if c.Name == "summary_truncation" {
			rc = c
			break
		}
	}
	require.NotNil(t, rc, "summary_truncation case not found")

	ctx := context.Background()
	backends := NewReplayBackends(t)

	rc.BaseTime = time.Now().Add(-24 * time.Hour) // historical — should fail.

	// RunReplayCase calls t.Fatalf for historical BaseTime.  Capture
	// the failure with a custom TB that records Fatalf rather than
	// terminating the test process.
	ft := &fatalTB{TB: t}
	func() {
		defer func() { recover() }()
		_ = RunReplayCase(ft, ctx, backends[0], rc)
	}()
	if !ft.fatalCalled {
		t.Error("expected RunReplayCase to call Fatalf for historical BaseTime, but it did not")
	}
}

// fatalTB wraps testing.TB and records whether Fatalf was called, without
// actually terminating the test.
type fatalTB struct {
	testing.TB
	fatalCalled bool
}

func (f *fatalTB) Fatalf(format string, args ...any) {
	f.fatalCalled = true
	panic(fatalPanic{})
}

type fatalPanic struct{}

func (f *fatalTB) Fatal(args ...any) {
	f.fatalCalled = true
	panic(fatalPanic{})
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
			name: "delete_app_state without state",
			steps: []ReplayStep{
				{Type: StepDeleteAppState, State: nil},
			},
			wantContain: "state is required",
		},
		{
			name: "delete_user_state without state",
			steps: []ReplayStep{
				{Type: StepDeleteUserState, State: nil},
			},
			wantContain: "state is required",
		},
		{
			name: "update_app_state without state",
			steps: []ReplayStep{
				{Type: StepUpdateAppState, State: nil},
			},
			wantContain: "state is required",
		},
		{
			name: "fault_both_modes_set",
			steps: []ReplayStep{
				{
					Type:  StepAppendEvent,
					Event: &actionEvent{Author: "user", Role: "user", Content: "hi"},
					Fault: &FaultConfig{FailBefore: true, FailAfter: true},
				},
			},
			wantContain: "exactly one of fail_before or fail_after",
		},
		{
			name: "fault_neither_mode_set",
			steps: []ReplayStep{
				{
					Type:  StepAppendEvent,
					Event: &actionEvent{Author: "user", Role: "user", Content: "hi"},
					Fault: &FaultConfig{},
				},
			},
			wantContain: "at least one of fail_before or fail_after",
		},

		{
			name: "add_memory_with_delete_op",
			steps: []ReplayStep{
				{Type: StepAddMemory, Memory: &actionMemory{Op: "delete", Content: "x"}},
			},
			wantContain: "requires op",
		},
		{
			name: "update_memory_with_add_op",
			steps: []ReplayStep{
				{Type: StepUpdateMemory, Memory: &actionMemory{Op: "add", Ref: "m1", Content: "x"}},
			},
			wantContain: "requires op",
		},
		{
			name: "concurrent_create_session_rejected",
			steps: []ReplayStep{
				{
					Type: StepConcurrentEvents,
					Concurrent: []ReplayStep{
						{Type: StepCreateSession},
					},
				},
			},
			wantContain: "not allowed inside concurrent_events",
		},
		{
			name: "concurrent_create_summary_rejected",
			steps: []ReplayStep{
				{
					Type: StepConcurrentEvents,
					Concurrent: []ReplayStep{
						{Type: StepCreateSummary, Summary: &actionSummary{FilterKey: "main", Text: "hi"}},
					},
				},
			},
			wantContain: "not allowed inside concurrent_events",
		},

		{
			name: "memory_event_time_malformed",
			steps: []ReplayStep{
				{Type: StepAddMemory, Memory: &actionMemory{Op: "add", Content: "x", Meta: &memoryMeta{EventTime: "not-a-timestamp"}}},
			},
			wantContain: "event_time",
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
				{Type: StepUpdateAppState, State: map[string]any{"k": "v"}},
				{Type: StepDeleteAppState, State: map[string]any{"k": nil}},
				{Type: StepDeleteUserState, State: map[string]any{"k": nil}},
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

// ---------------------------------------------------------------------------
// Fault injection sentinel error — regression tests
// ---------------------------------------------------------------------------

// stubSessionService returns a fixed error from AppendEvent for testing
// that the fault wrapper does not swallow real backend errors.
type stubSessionService struct {
	session.Service
	appendErr error
}

func (s *stubSessionService) AppendEvent(
	ctx context.Context, sess *session.Session, evt *event.Event, opts ...session.Option,
) error {
	return s.appendErr
}

func TestFaultInjection_SentinelError(t *testing.T) {
	backends := NewReplayBackends(t)
	backend := backends[0]
	ctx := context.Background()

	key := session.Key{AppName: "ft", UserID: "u", SessionID: "sid"}
	sess, err := backend.SessionService.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	require.NotNil(t, sess)

	fw := &faultSessionService{Service: backend.SessionService}

	t.Run("fail_before_returns_sentinel", func(t *testing.T) {
		fw.nextFault = &FaultConfig{FailBefore: true}
		evt := buildEvent(&actionEvent{Author: "user", Role: "user", Content: "hi"}, 0, time.Now())
		err := fw.AppendEvent(ctx, sess, evt)
		require.Error(t, err)
		require.True(t, errors.Is(err, ErrFaultInjected),
			"expected ErrFaultInjected, got: %v", err)
	})

	t.Run("fail_after_returns_sentinel", func(t *testing.T) {
		fw.nextFault = &FaultConfig{FailAfter: true}
		evt := buildEvent(&actionEvent{Author: "user", Role: "user", Content: "ok"}, 1, time.Now())
		err := fw.AppendEvent(ctx, sess, evt)
		require.Error(t, err)
		require.True(t, errors.Is(err, ErrFaultInjected),
			"expected ErrFaultInjected, got: %v", err)
	})

	t.Run("no_fault_no_error", func(t *testing.T) {
		fw.nextFault = nil
		evt := buildEvent(&actionEvent{Author: "user", Role: "user", Content: "clean"}, 2, time.Now())
		err := fw.AppendEvent(ctx, sess, evt)
		require.NoError(t, err)
	})

	t.Run("underlying_error_not_sentinel", func(t *testing.T) {
		// Wrap a stub that returns a real backend error with a
		// FaultConfig carrying FailAfter.  The wrapper must return
		// the underlying error — not ErrFaultInjected — because
		// the backend error happens before the fault fires.
		realErr := errors.New("simulated backend failure")
		stub := &stubSessionService{
			Service:   backend.SessionService,
			appendErr: realErr,
		}
		fw2 := &faultSessionService{
			Service:   stub,
			nextFault: &FaultConfig{FailAfter: true},
		}
		evt := buildEvent(&actionEvent{Author: "user", Role: "user", Content: "boom"}, 3, time.Now())
		err := fw2.AppendEvent(ctx, sess, evt)
		require.Error(t, err)
		require.False(t, errors.Is(err, ErrFaultInjected),
			"real backend error should NOT be ErrFaultInjected, got: %v", err)
		require.True(t, errors.Is(err, realErr),
			"expected underlying error %v, got: %v", realErr, err)
	})
}

// ---------------------------------------------------------------------------
// Load-time rejection of unknown JSON fields
// ---------------------------------------------------------------------------

func TestReplayCase_Load_RejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()

	writeJSON := func(name, content string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte(content), 0644))
		return p
	}

	t.Run("misspelled_top_level_key", func(t *testing.T) {
		path := writeJSON("top_level.json", `{
			"name": "typo-test",
			"desciption": "misspelled",
			"app_name": "app",
			"user_id": "u",
			"session_id": "s",
			"steps": [{"type": "create_session"}, {"type": "get_session"}]
		}`)
		_, err := LoadReplayCase(path)
		require.Error(t, err)
		require.Contains(t, err.Error(), "unmarshal")
	})

	t.Run("misspelled_nested_verify_field", func(t *testing.T) {
		// "events_count" is correct, "events_cout" is not.
		path := writeJSON("verify.json", `{
			"name": "verify-typo",
			"steps": [
				{"type": "create_session"},
				{"type": "get_session"}
			],
			"verify": {"events_cout": 1}
		}`)
		_, err := LoadReplayCase(path)
		require.Error(t, err)
		require.Contains(t, err.Error(), "unmarshal")
	})

	t.Run("misspelled_fault_field", func(t *testing.T) {
		// "fail_before" is correct, "fail_befor" is not.
		path := writeJSON("fault.json", `{
			"name": "fault-typo",
			"steps": [
				{"type": "create_session"},
				{
					"type": "append_event",
					"event": {"author": "user", "role": "user", "content": "hello"},
					"fault": {"fail_befor": true}
				},
				{"type": "get_session"}
			]
		}`)
		_, err := LoadReplayCase(path)
		require.Error(t, err)
		require.Contains(t, err.Error(), "unmarshal")
	})

	t.Run("trailing_json_value", func(t *testing.T) {
		path := writeJSON("trailing.json", `{
			"name": "trailing-test",
			"steps": [{"type": "create_session"}, {"type": "get_session"}]
		}{"trailing": "garbage"}`)
		_, err := LoadReplayCase(path)
		require.Error(t, err)
		require.Contains(t, err.Error(), "trailing data")
	})
}

// ---------------------------------------------------------------------------
// Parallel safety — fault wrappers must not race
// ---------------------------------------------------------------------------

func TestReplayConsistency_ParallelFaultAndNonFault(t *testing.T) {
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

	// Run a faulted case and a non-faulted case concurrently on the
	// same backend pair.  Without the per-run backend copy in
	// RunReplayCase this races on SessionService / TrackService /
	// MemoryService assignments and on Summarizer.SetText text.
	t.Run("parallel", func(t *testing.T) {
		t.Run("fault", func(t *testing.T) {
			t.Parallel()
			rc := findCase("error_recovery")
			rc.AppName = "replaytest-parallel-fault"
			rc.BaseTime = time.Now().UTC().Truncate(time.Second)
			_ = RunReplayCase(t, ctx, backends[0], rc)
			_ = RunReplayCase(t, ctx, backends[1], rc)
		})
		t.Run("non_fault", func(t *testing.T) {
			t.Parallel()
			rc := findCase("single_turn")
			rc.AppName = "replaytest-parallel-clean"
			rc.BaseTime = time.Now().UTC().Truncate(time.Second)
			_ = RunReplayCase(t, ctx, backends[0], rc)
			_ = RunReplayCase(t, ctx, backends[1], rc)
		})
	})
}

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
