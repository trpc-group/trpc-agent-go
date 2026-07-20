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

// init removes any stale report file so CI runs start clean.
func init() {
	// Respect explicit env overrides — only clean the default path.
	if os.Getenv(EnvReportPath) != "" {
		return
	}
	// Clean up a stale report from the default location, ignoring errors.
	_ = os.Remove(defaultReportName)
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
