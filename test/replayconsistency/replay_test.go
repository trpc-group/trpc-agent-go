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
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
)

const (
	lightweightReplayTimeout      = 30 * time.Second
	minimumExampleDifferenceCount = 7
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
	report, err := runner.Run(ctx, replaytest.StandardReplayCases())
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
	_, err := runner.Run(context.Background(), []replaytest.ReplayCase{replayCase})
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
	eventTime := time.Date(2026, time.July, 21, 0, 0, 0, 0, time.UTC)
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
	report, err := runner.Run(context.Background(), []replaytest.ReplayCase{replayCase})
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
	var report replaytest.Report
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("unmarshal example report: %v", err)
	}
	if report.Baseline == "" || len(report.Differences) < minimumExampleDifferenceCount {
		t.Fatalf("example report is incomplete: %#v", report)
	}
	if _, err := replaytest.MarshalReport(report); err != nil {
		t.Fatalf("MarshalReport() error = %v", err)
	}
}
