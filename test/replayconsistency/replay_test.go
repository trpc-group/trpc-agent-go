//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replayconsistency

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
)

func TestLightweightReplayConsistency(t *testing.T) {
	cases := replaytest.StandardCases()
	runner := replaytest.Runner{
		BaselineBackend: "inmemory",
		Backends:        LightweightFactories(),
	}

	started := time.Now()
	result, err := runner.Run(context.Background(), cases)
	require.NoError(t, err)
	require.Less(t, time.Since(started), 30*time.Second)
	require.Len(t, result.Report.Cases, 10)
	require.Zero(t, result.Report.Summary.Failed)
	require.Zero(t, result.Report.Summary.DisallowedDiffs)
	require.Equal(t, 10, result.Report.Summary.Passed)
	for _, comparison := range result.Report.Cases {
		require.Equal(t, "sqlite", comparison.Backend)
		require.Equal(t, replaytest.ComparisonPassed, comparison.Status)
	}

	reportPath := filepath.Join(
		t.TempDir(),
		"session_memory_summary_track_diff_report.json",
	)
	require.NoError(t, replaytest.WriteReport(reportPath, result.Report))
}

func TestLightweightReplayDetectsInjectedInconsistency(t *testing.T) {
	cases := replaytest.StandardCases()
	runner := replaytest.Runner{
		BaselineBackend: "inmemory",
		Backends:        LightweightFactories(),
	}
	result, err := runner.Run(context.Background(), cases)
	require.NoError(t, err)

	detected := 0
	for _, replayCase := range cases {
		baseline := result.Snapshots[replayCase.Name]["inmemory"]
		target := cloneSnapshot(t, result.Snapshots[replayCase.Name]["sqlite"])
		injectCaseAnomaly(t, replayCase.Name, target)

		diffs := replaytest.CompareSnapshots(
			replayCase.Name,
			baseline,
			target,
			replayCase.AllowedDiffs,
		)
		require.Truef(
			t,
			hasDisallowed(diffs),
			"case %s did not detect its injected anomaly",
			replayCase.Name,
		)
		detected++
	}
	require.Equal(t, 10, detected)
	t.Logf(
		"detected %d/%d injected InMemory/SQLite inconsistencies",
		detected,
		len(cases),
	)
}

func TestExampleDiffReportCoversLocators(t *testing.T) {
	path := filepath.Join(
		"testdata",
		"session_memory_summary_track_diff_report.json",
	)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var report replaytest.DiffReport
	require.NoError(t, json.Unmarshal(data, &report))
	require.Equal(t, replaytest.ReportSchemaVersion, report.SchemaVersion)
	require.Len(t, report.Cases, 1)
	require.Len(t, report.Cases[0].Differences, 5)

	var (
		hasEvent   bool
		hasMemory  bool
		hasSummary bool
		hasTrack   bool
	)
	for _, difference := range report.Cases[0].Differences {
		require.Equal(
			t,
			replaytest.DifferenceSourceBackend,
			difference.Source,
		)
		hasEvent = hasEvent || difference.EventIndex != nil
		hasMemory = hasMemory || difference.MemoryID != ""
		hasSummary = hasSummary ||
			difference.SummaryFilterKey != ""
		hasTrack = hasTrack || difference.TrackName != ""
	}
	require.True(t, hasEvent)
	require.True(t, hasMemory)
	require.True(t, hasSummary)
	require.True(t, hasTrack)
}

func injectCaseAnomaly(
	t *testing.T,
	caseName string,
	snapshot *replaytest.Snapshot,
) {
	t.Helper()
	switch caseName {
	case "single_turn_dialogue":
		snapshot.Events[0].Author = "wrong-author"
	case "multi_turn_order":
		snapshot.Events[1], snapshot.Events[2] =
			snapshot.Events[2], snapshot.Events[1]
	case "tool_call_round_trip":
		snapshot.Events[1].ToolCalls[0].Arguments = map[string]any{
			"city": "wrong",
		}
	case "state_lifecycle":
		snapshot.State["counter"] = int64(999)
	case "memory_lifecycle":
		snapshot.Memories[0].Content = "polluted memory"
	case "summary_update":
		item := snapshot.Summaries["agent/research"]
		item.Text = "stale summary"
		snapshot.Summaries["agent/research"] = item
	case "summary_event_truncation":
		delete(snapshot.Summaries, "agent/long")
	case "track_observability":
		snapshot.Tracks["tools"][1].Error = "injected error"
	case "concurrent_interleaving":
		snapshot.Events[1].Content = "wrong concurrent result"
	case "failure_recovery":
		snapshot.Events = append(snapshot.Events, snapshot.Events[0])
	default:
		t.Fatalf("no anomaly injector for case %s", caseName)
	}
}

func cloneSnapshot(
	t *testing.T,
	input *replaytest.Snapshot,
) *replaytest.Snapshot {
	t.Helper()
	data, err := json.Marshal(input)
	require.NoError(t, err)
	var output replaytest.Snapshot
	require.NoError(t, json.Unmarshal(data, &output))
	return &output
}

func hasDisallowed(differences []replaytest.Difference) bool {
	for _, difference := range differences {
		if !difference.AllowedDiff {
			return true
		}
	}
	return false
}
