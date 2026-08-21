//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replayconsistency

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
)

const (
	lightweightReplayTimeout = 30 * time.Second
)

func standardNormalizeOptions() replaytest.NormalizeOptions {
	options := replaytest.DefaultNormalizeOptions()
	options.PreserveEventIDs = true
	return options
}

func TestStandardNormalizeOptionsPreserveExplicitEventIDs(t *testing.T) {
	options := standardNormalizeOptions()
	if !options.PreserveEventIDs {
		t.Fatal("standard replay normalization must preserve explicit event IDs")
	}
}

func TestLightweightReplayMatrix(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), lightweightReplayTimeout)
	defer cancel()

	runner := replaytest.Runner{
		Backends: []replaytest.Backend{
			newInMemoryBackend(),
			newSQLiteBackend(t.TempDir()),
		},
		NormalizeOptions: standardNormalizeOptions(),
		CompareOptions:   replaytest.DefaultCompareOptions(),
	}
	started := time.Now()
	report, err := runReplayCases(ctx, runner, replaytest.StandardReplayCases())
	if err != nil {
		t.Fatalf("Runner.Run() error = %v", err)
	}
	if report.HasUnexpectedDifferences() {
		t.Fatalf("lightweight replay mismatch: %#v", report.Differences)
	}
	if elapsed := time.Since(started); elapsed > lightweightReplayTimeout {
		t.Fatalf("lightweight replay took %s, want <= %s", elapsed, lightweightReplayTimeout)
	}
}

func TestAfterWriteRetryDetectsDuplicateEvent(t *testing.T) {
	const sessionID = "after-write-session"
	event := &replaytest.EventSnapshot{ID: "event-1", Author: "user", Role: "user", Done: true}
	fault := replaytest.Operation{
		Kind: replaytest.OperationAppendEvent, SessionID: sessionID, Event: event,
		InjectedFailure: "commit succeeded but response failed",
		FailurePoint:    replaytest.FailureAfterWrite, ExpectFailure: true,
	}
	replayCase := replaytest.ReplayCase{
		Name: "after-write-retry",
		Operations: []replaytest.Operation{
			{Kind: replaytest.OperationCreateSession, SessionID: sessionID},
			fault,
			{Kind: replaytest.OperationAppendEvent, SessionID: sessionID, Event: event},
		},
		Invariants: []replaytest.SnapshotInvariant{{
			Name: "retry must not duplicate event",
			Check: func(snapshot replaytest.Snapshot) error {
				if len(snapshot.Sessions) != 1 || len(snapshot.Sessions[0].Events) != 1 {
					return fmt.Errorf("unexpected replay events: %#v", snapshot.Sessions)
				}
				return nil
			},
		}},
	}
	runner := replaytest.Runner{Backends: []replaytest.Backend{newInMemoryBackend()}}
	_, err := runReplayCases(context.Background(), runner, []replaytest.ReplayCase{replayCase})
	if err == nil || !strings.Contains(err.Error(), "retry must not duplicate event") {
		t.Fatalf("Runner.Run() error = %v", err)
	}
}

func TestAfterWriteRetryLeavesStateClean(t *testing.T) {
	const sessionID = "after-write-state-session"
	retry := replaytest.Operation{
		Kind:         replaytest.OperationUpdateState,
		SessionID:    sessionID,
		StateUpdates: map[string]any{"status": "recovered"},
		StateDeletes: []string{"transient"},
	}
	fault := retry
	fault.InjectedFailure = "state commit succeeded but response failed"
	fault.FailurePoint = replaytest.FailureAfterWrite
	fault.ExpectFailure = true
	replayCase := replaytest.ReplayCase{
		Name:         "after-write-state-retry",
		Capabilities: []replaytest.Capability{replaytest.CapabilitySession},
		Operations: []replaytest.Operation{
			{Kind: replaytest.OperationCreateSession, SessionID: sessionID},
			{
				Kind: replaytest.OperationUpdateState, SessionID: sessionID,
				StateUpdates: map[string]any{"status": "pending", "transient": true},
			},
			fault,
			retry,
		},
		Invariants: []replaytest.SnapshotInvariant{{
			Name: "after-write state retry leaves only the final state",
			Check: func(snapshot replaytest.Snapshot) error {
				if len(snapshot.Sessions) != 1 {
					return fmt.Errorf("session count = %d, want 1", len(snapshot.Sessions))
				}
				state := snapshot.Sessions[0].State
				if len(state) != 1 || state["status"] != replaytest.JSONStateValue("recovered") {
					return fmt.Errorf("dirty state after retry: %#v", state)
				}
				return nil
			},
		}},
	}
	runLightweightRecoveryCase(t, replayCase)
}

func TestAfterWriteRetryLeavesSummaryConsistent(t *testing.T) {
	const sessionID = "after-write-summary-session"
	eventTime := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	retry := replaytest.Operation{
		Kind:      replaytest.OperationUpdateSummary,
		SessionID: sessionID,
		Summary: &replaytest.SummarySnapshot{
			SessionID: sessionID,
			FilterKey: "branch/main",
			Text:      "recovered summary",
		},
	}
	fault := retry
	fault.InjectedFailure = "summary commit succeeded but response failed"
	fault.FailurePoint = replaytest.FailureAfterWrite
	fault.ExpectFailure = true
	replayCase := replaytest.ReplayCase{
		Name:         "after-write-summary-retry",
		Capabilities: []replaytest.Capability{replaytest.CapabilitySession, replaytest.CapabilitySummary},
		Operations: []replaytest.Operation{
			{Kind: replaytest.OperationCreateSession, SessionID: sessionID},
			{
				Kind: replaytest.OperationAppendEvent, SessionID: sessionID,
				Event: &replaytest.EventSnapshot{
					ID: "event-1", Author: "user", Role: "user", Content: "context",
					Done: true, Timestamp: eventTime,
				},
			},
			{
				Kind: replaytest.OperationUpdateSummary, SessionID: sessionID,
				Summary: &replaytest.SummarySnapshot{
					SessionID: sessionID, FilterKey: "branch/main", Text: "initial summary",
				},
			},
			fault,
			retry,
		},
		Invariants: []replaytest.SnapshotInvariant{{
			Name: "after-write summary retry preserves ownership and overwrite metadata",
			Check: func(snapshot replaytest.Snapshot) error {
				if len(snapshot.Sessions) != 1 || len(snapshot.Sessions[0].Summaries) != 1 {
					return fmt.Errorf("unexpected summaries after retry: %#v", snapshot.Sessions)
				}
				summary := snapshot.Sessions[0].Summaries[0]
				if summary.SessionID != sessionID || summary.FilterKey != "branch/main" ||
					summary.Text != "recovered summary" || summary.Version != 1 ||
					summary.UpdatedAt.IsZero() {
					return fmt.Errorf("wrong summary after retry: %#v", summary)
				}
				if summary.Boundary["filter_key"] != "branch/main" ||
					summary.Boundary["last_event_id"] != "event-1" ||
					summary.Boundary["cutoff_at"] == nil {
					return fmt.Errorf("wrong summary boundary after retry: %#v", summary.Boundary)
				}
				return nil
			},
		}},
	}
	runLightweightRecoveryCase(t, replayCase)
}

func runLightweightRecoveryCase(t *testing.T, replayCase replaytest.ReplayCase) {
	t.Helper()
	runner := replaytest.Runner{
		Backends: []replaytest.Backend{
			newInMemoryBackend(),
			newSQLiteBackend(t.TempDir()),
		},
		NormalizeOptions: standardNormalizeOptions(),
		CompareOptions:   replaytest.DefaultCompareOptions(),
	}
	report, err := runReplayCases(context.Background(), runner, []replaytest.ReplayCase{replayCase})
	if err != nil {
		t.Fatalf("Runner.Run() error = %v", err)
	}
	if report.HasUnexpectedDifferences() {
		t.Fatalf("recovery replay mismatch: %#v", report.Differences)
	}
}

func TestExampleReportIsValid(t *testing.T) {
	data, err := os.ReadFile("testdata/session_memory_summary_track_diff_report.json")
	if err != nil {
		t.Fatalf("read example report: %v", err)
	}
	var decoded replaytest.Report
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal example report: %v", err)
	}
	report := exampleReport(t)
	encoded, err := replaytest.MarshalReport(report)
	if err != nil {
		t.Fatalf("MarshalReport() error = %v", err)
	}
	// The committed example report may carry CRLF line endings depending on
	// the platform that generated it; compare line-ending agnostically.
	if !bytes.Equal(bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n")), encoded) {
		t.Fatalf("example report is stale:\ngot:\n%s\nwant:\n%s", data, encoded)
	}
	state := exampleDifferenceAt(t, report.Differences, "$.sessions[0].state.theme.value")
	if state.Locator.StateKey != "theme" {
		t.Fatalf("state locator = %#v", state.Locator)
	}
}

func exampleReport(t *testing.T) replaytest.Report {
	t.Helper()
	differences := make([]replaytest.Difference, 0, 6)

	singleBaseline := replaytest.Snapshot{Sessions: []replaytest.SessionSnapshot{{
		ID:     "session-1",
		Events: []replaytest.EventSnapshot{{Content: "same"}, {Content: "hi"}},
	}}}
	singleActual := singleBaseline
	singleActual.Sessions = append([]replaytest.SessionSnapshot(nil), singleBaseline.Sessions...)
	singleActual.Sessions[0].Events = append(
		[]replaytest.EventSnapshot(nil), singleBaseline.Sessions[0].Events...,
	)
	singleActual.Sessions[0].Events[1].Content = "hello"
	differences = append(differences, exampleComparedDifference(
		t, "single-turn", "sqlite", singleBaseline, singleActual,
		"$.sessions[0].events[1].content", "assistant content differs after replay", nil,
	))

	stateBaseline := replaytest.Snapshot{Sessions: []replaytest.SessionSnapshot{{
		ID: "session-1",
		State: map[string]replaytest.StateValueSnapshot{
			"theme": replaytest.TextStateValue("dark"),
		},
	}}}
	stateActual := replaytest.Snapshot{Sessions: []replaytest.SessionSnapshot{{
		ID: "session-1",
		State: map[string]replaytest.StateValueSnapshot{
			"theme": replaytest.TextStateValue("light"),
		},
	}}}
	differences = append(differences, exampleComparedDifference(
		t, "state-update", "sqlite", stateBaseline, stateActual,
		"$.sessions[0].state.theme.value", "final state value differs after overwrite", nil,
	))

	memoryBaseline := exampleMemorySearchSnapshot("prefers concise answers", 0.91)
	memoryContentActual := exampleMemorySearchSnapshot("verify tests before delivery", 0.91)
	differences = append(differences, exampleComparedDifference(
		t, "memory-read-write", "postgres", memoryBaseline, memoryContentActual,
		"$.memory_searches[0].results[0].content",
		"memory retrieval returned different content at rank 0", nil,
	))
	memoryScoreActual := exampleMemorySearchSnapshot("prefers concise answers", 0.9)
	scoreRule := replaytest.AllowedDiffRule{
		Case: "memory-read-write", Backend: "vector-memory",
		Path:        "$.memory_searches[0].results[0].score",
		Explanation: "vector backend uses a documented score tolerance",
	}
	differences = append(differences, exampleComparedDifference(
		t, "memory-read-write", "vector-memory", memoryBaseline, memoryScoreActual,
		scoreRule.Path, scoreRule.Explanation, []replaytest.AllowedDiffRule{scoreRule},
	))

	summaryBaseline := replaytest.Snapshot{Sessions: []replaytest.SessionSnapshot{{
		ID: "session-1", Summaries: []replaytest.SummarySnapshot{{
			SessionID: "session-1", FilterKey: "branch/main", Text: "updated summary",
		}},
	}}}
	summaryActual := replaytest.Snapshot{Sessions: []replaytest.SessionSnapshot{{
		ID: "session-1", Summaries: []replaytest.SummarySnapshot{{
			SessionID: "session-1", FilterKey: "branch/main", Text: "initial summary",
		}},
	}}}
	differences = append(differences, exampleComparedDifference(
		t, "summary-update", "mysql", summaryBaseline, summaryActual,
		"$.sessions[0].summaries[0].text", "summary overwrite kept the stale text", nil,
	))

	trackBaseline := exampleTrackSnapshot("timeout")
	trackActual := exampleTrackSnapshot("")
	differences = append(differences, exampleComparedDifference(
		t, "track-events", "sqlite", trackBaseline, trackActual,
		"$.sessions[0].tracks[0].events[2].error", "track error payload was lost", nil,
	))
	report := replaytest.NewMatrixReport("inmemory", []replaytest.CaseResult{
		{
			Case: "track-events", Status: replaytest.ResultPass,
			Backends: []replaytest.CaseBackendResult{
				{Backend: "postgres", Status: replaytest.ResultPass},
				{Backend: "clickhouse", Status: replaytest.ResultUnsupported,
					Unsupported: []replaytest.Capability{replaytest.CapabilityTrack}},
			},
		},
		{
			Case: "summary-update", Status: replaytest.ResultFail,
			Backends: []replaytest.CaseBackendResult{
				{Backend: "mysql", Status: replaytest.ResultFail},
				{Backend: "clickhouse", Status: replaytest.ResultUnsupported,
					Unsupported: []replaytest.Capability{replaytest.CapabilitySummary}},
			},
		},
	}, differences)
	report.GeneratedAt = time.Date(2026, time.August, 12, 0, 0, 0, 0, time.UTC)
	report.Probes = []replaytest.CapabilityProbeResult{{
		Probe: "session-ttl-expiry", Backend: "clickhouse", Capability: replaytest.CapabilityTTL,
		Status: replaytest.ResultUnsupported, AllowedDiff: true,
		Explanation: "ClickHouse 25.3 session did not expire within the deterministic probe deadline",
	}}
	return report
}

func exampleComparedDifference(
	t *testing.T,
	caseName string,
	backend string,
	baseline replaytest.Snapshot,
	actual replaytest.Snapshot,
	path string,
	explanation string,
	rules []replaytest.AllowedDiffRule,
) replaytest.Difference {
	t.Helper()
	differences, err := replaytest.CompareSnapshots(replaytest.CompareInput{
		Case: caseName, Backend: backend, Baseline: baseline, Actual: actual,
		Options: replaytest.CompareOptions{AllowedDiffRules: rules},
	})
	if err != nil {
		t.Fatalf("CompareSnapshots(%s/%s) error = %v", caseName, backend, err)
	}
	if len(differences) != 1 || differences[0].Path != path {
		t.Fatalf("CompareSnapshots(%s/%s) = %#v, want path %s", caseName, backend, differences, path)
	}
	difference := differences[0]
	difference.Explanation = explanation
	return difference
}

func exampleMemorySearchSnapshot(content string, score float64) replaytest.Snapshot {
	return replaytest.Snapshot{MemorySearches: []replaytest.MemorySearchSnapshot{{
		AppName: "replaytest", UserID: "user-1", Query: "preferences",
		Results: []replaytest.MemorySnapshot{{
			ID: "memory-0001", AppName: "replaytest", UserID: "user-1",
			Scope:   replaytest.MemoryScope{AppName: "replaytest", UserID: "user-1"},
			Content: content, Score: score,
		}},
	}}}
}

func exampleTrackSnapshot(eventError string) replaytest.Snapshot {
	return replaytest.Snapshot{Sessions: []replaytest.SessionSnapshot{{
		ID: "session-1", Tracks: []replaytest.TrackSnapshot{{
			Name: "tools", Events: []replaytest.TrackEventSnapshot{{}, {}, {Error: eventError}},
		}},
	}}}
}

func exampleDifferenceAt(
	t *testing.T,
	differences []replaytest.Difference,
	path string,
) replaytest.Difference {
	t.Helper()
	for _, difference := range differences {
		if difference.Path == path {
			return difference
		}
	}
	t.Fatalf("difference %q not found in %#v", path, differences)
	return replaytest.Difference{}
}

// TestPreflightRejectsInternalSessionStateKeys guards against drift between
// the keys the bundled fixture filters from observable snapshots and the keys
// the replay framework rejects during operation preflight. Both sides now
// delegate to session.IsInternalStateKey, so any future internal key added
// there must also be rejected by Operation.Validate.
func TestPreflightRejectsInternalSessionStateKeys(t *testing.T) {
	internalKeys := []string{
		"tracks",
		session.SummaryLastIncludedTimestampStateKey,
		session.SummaryLastIncludedEventIDStateKey,
	}
	for _, key := range internalKeys {
		if !session.IsInternalStateKey(key) {
			t.Fatalf("session.IsInternalStateKey(%q) = false", key)
		}
		operation := replaytest.Operation{
			Kind:         replaytest.OperationUpdateState,
			SessionID:    "session-1",
			StateUpdates: map[string]any{key: "value"},
		}
		if err := operation.Validate(); err == nil {
			t.Errorf("update state key %q passed replay preflight", key)
		}
	}
	for _, key := range []string{"profile", "temp:scratch", "summary:custom"} {
		operation := replaytest.Operation{
			Kind:         replaytest.OperationUpdateState,
			SessionID:    "session-1",
			StateUpdates: map[string]any{key: "value"},
		}
		if err := operation.Validate(); err != nil {
			t.Errorf("update state key %q rejected by replay preflight: %v", key, err)
		}
	}
}

func TestAdapterPreflightRejectsPayloadsBeforeCreatingFixtures(t *testing.T) {
	invalidRawEvent := replaytest.Operation{
		Kind: replaytest.OperationAppendEvent, SessionID: "session-1",
		Event: &replaytest.EventSnapshot{
			Extensions: map[string]any{"raw": []byte(`{"broken"`)},
		},
	}
	tests := []struct {
		name      string
		operation replaytest.Operation
	}{
		{
			name: "invalid raw bytes in parallel child",
			operation: replaytest.Operation{
				Kind:     replaytest.OperationParallel,
				Parallel: []replaytest.Operation{invalidRawEvent},
			},
		},
		{
			name: "reserved event extension",
			operation: replaytest.Operation{
				Kind: replaytest.OperationAppendEvent, SessionID: "session-1",
				Event: &replaytest.EventSnapshot{Extensions: map[string]any{
					toolResponseExtraExtensionKey: map[string]any{"provider": "caller"},
				}},
			},
		},
		{
			name: "unsupported memory metadata",
			operation: replaytest.Operation{
				Kind: replaytest.OperationWriteMemory,
				Memory: &replaytest.MemorySnapshot{
					AppName: "app", UserID: "user", Content: "memory",
					Metadata: map[string]any{"unsupported": true},
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			created := 0
			runner := replaytest.Runner{Backends: []replaytest.Backend{{
				Name: "backend",
				New: func(context.Context, string) (replaytest.Fixture, error) {
					created++
					return nil, fmt.Errorf("unexpected fixture creation")
				},
			}}}
			_, err := runReplayCases(context.Background(), runner, []replaytest.ReplayCase{{
				Name: "invalid", Operations: []replaytest.Operation{test.operation},
			}})
			if err == nil {
				t.Fatal("runReplayCases() accepted invalid adapter payload")
			}
			if created != 0 {
				t.Fatalf("Backend.New called %d times before adapter preflight rejection", created)
			}
		})
	}
}
