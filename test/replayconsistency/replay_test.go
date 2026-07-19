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

func TestLightweightReplayMatrix(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), lightweightReplayTimeout)
	defer cancel()

	runner := replaytest.Runner{
		Backends: []replaytest.Backend{
			newInMemoryBackend(),
			newSQLiteBackend(t.TempDir()),
		},
		NormalizeOptions: replaytest.DefaultNormalizeOptions(),
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
