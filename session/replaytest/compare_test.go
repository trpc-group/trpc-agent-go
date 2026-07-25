//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompareSnapshotsLocatesDomainObjects(t *testing.T) {
	baseline := &Snapshot{
		Backend:   "inmemory",
		SessionID: "session-1",
		Events: []EventSnapshot{{
			ID:      "event-1",
			Author:  "assistant",
			Role:    "assistant",
			Content: "done",
		}},
		Memories: []MemorySnapshot{{
			ID:      "memory-1",
			Content: "prefers Go",
		}},
		Summaries: map[string]SummarySnapshot{
			"agent/tool": {
				ID:        "session-1:agent/tool",
				SessionID: "session-1",
				FilterKey: "agent/tool",
				Text:      "tool completed",
				Version:   1,
			},
		},
		Tracks: map[string][]TrackEventSnapshot{
			"tools": {{
				EventType:    "completed",
				InvocationID: "inv-1",
				DurationMS:   12,
			}},
		},
	}
	compared := cloneSnapshotForTest(t, baseline)
	compared.Backend = "sqlite"
	compared.Events[0].Content = "different"
	compared.Memories[0].Content = "prefers Rust"
	summary := compared.Summaries["agent/tool"]
	summary.Text = "wrong summary"
	compared.Summaries["agent/tool"] = summary
	compared.Tracks["tools"][0].InvocationID = "inv-wrong"

	diffs := CompareSnapshots(
		"domain-location",
		baseline,
		compared,
		nil,
	)
	require.Len(t, diffs, 4)

	byPath := make(map[string]Difference, len(diffs))
	for _, diff := range diffs {
		byPath[diff.FieldPath] = diff
		require.Equal(t, "session-1", diff.SessionID)
		require.Equal(t, "sqlite", diff.Backend)
		require.False(t, diff.AllowedDiff)
	}

	eventDiff := byPath[`events[0].content`]
	require.NotNil(t, eventDiff.EventIndex)
	require.Equal(t, 0, *eventDiff.EventIndex)

	memoryDiff := byPath[`memories["memory-1"].content`]
	require.Equal(t, "memory-1", memoryDiff.MemoryID)

	summaryDiff := byPath[`summaries["agent/tool"].text`]
	require.Equal(t, "agent/tool", summaryDiff.SummaryFilterKey)
	require.Equal(t, "session-1:agent/tool", summaryDiff.SummaryID)

	trackDiff := byPath[`tracks["tools"][0].invocation_id`]
	require.Equal(t, "tools", trackDiff.TrackName)
}

func TestCompareSnapshotsAppliesAllowedDiffRules(t *testing.T) {
	baseline := &Snapshot{
		Backend:   "inmemory",
		SessionID: "session-1",
		MemorySearches: []MemorySearchSnapshot{{
			Query: "go",
			Results: []MemorySnapshot{{
				ID:    "memory-1",
				Score: 0.8121,
			}},
		}},
		ObservedEventOrder: []string{"event-a", "event-b"},
	}
	compared := cloneSnapshotForTest(t, baseline)
	compared.Backend = "sqlite"
	compared.MemorySearches[0].Results[0].Score = 0.8128
	compared.ObservedEventOrder = []string{"event-b", "event-a"}

	diffs := CompareSnapshots(
		"allowed",
		baseline,
		compared,
		[]AllowedDiffRule{
			{
				PathPrefix:        "memory_searches",
				Backend:           "sqlite",
				AbsoluteTolerance: 0.001,
				Explanation:       "backend similarity precision",
			},
			{
				PathPrefix:  "observed_event_order",
				Backend:     "sqlite",
				Explanation: "concurrent commit order is nondeterministic",
			},
		},
	)
	require.Len(t, diffs, 3)
	for _, diff := range diffs {
		require.True(t, diff.AllowedDiff)
		require.NotEmpty(t, diff.Explanation)
	}
}

func TestCompareSnapshotsDetectsScalarTypeDrift(t *testing.T) {
	baseline := &Snapshot{
		Backend:   "inmemory",
		SessionID: "session-1",
		State: map[string]any{
			"count":   int64(1),
			"enabled": true,
		},
	}
	compared := cloneSnapshotForTest(t, baseline)
	compared.Backend = "sqlite"
	compared.State["count"] = "1"
	compared.State["enabled"] = "true"

	diffs := CompareSnapshots("type-drift", baseline, compared, nil)
	require.Len(t, diffs, 2)
	require.Equal(t, "state.count", diffs[0].FieldPath)
	require.Equal(t, "state.enabled", diffs[1].FieldPath)
	require.Equal(t, DifferenceSourceBackend, diffs[0].Source)
}

func TestCompareSnapshotsDetectsDuplicateMemories(t *testing.T) {
	baseline := &Snapshot{
		Backend:   "inmemory",
		SessionID: "session-1",
		Memories: []MemorySnapshot{{
			ID:      "memory-1",
			Content: "prefers Go",
		}},
	}
	compared := cloneSnapshotForTest(t, baseline)
	compared.Backend = "sqlite"
	compared.Memories = append(compared.Memories, compared.Memories[0])

	diffs := CompareSnapshots("duplicate-memory", baseline, compared, nil)
	require.Len(t, diffs, 1)
	require.Equal(t, "memories.length", diffs[0].FieldPath)
	require.Equal(t, 1, diffs[0].BaselineValue)
	require.Equal(t, 2, diffs[0].ComparedValue)
}

func TestCompareSnapshotsSkipsUnsupportedBaselineFeature(t *testing.T) {
	baseline := &Snapshot{
		Backend:   "inmemory",
		SessionID: "session-1",
		Unsupported: []UnsupportedFeature{{
			Feature:     FeatureMemory,
			Reason:      "inmemory backend does not implement memory",
			AllowedDiff: true,
		}},
	}
	compared := cloneSnapshotForTest(t, baseline)
	compared.Backend = "postgres"
	compared.Unsupported = nil
	compared.Memories = []MemorySnapshot{{
		ID:      "memory-1",
		Content: "prefers Go",
	}}

	diffs := CompareSnapshots("baseline-capability", baseline, compared, nil)
	require.Len(t, diffs, 1)
	require.True(t, diffs[0].AllowedDiff)
	require.Equal(
		t,
		`capabilities["memory"]`,
		diffs[0].FieldPath,
	)
	require.Equal(t, "unsupported", diffs[0].BaselineValue)
	require.Equal(t, "supported", diffs[0].ComparedValue)
}

func TestCompareSnapshotsHandlesMissingSnapshots(t *testing.T) {
	target := &Snapshot{
		Backend:   "sqlite",
		SessionID: "session-1",
	}
	diffs := CompareSnapshots("missing-baseline", nil, target, nil)
	require.Len(t, diffs, 1)
	require.Equal(t, DifferenceSourceBackend, diffs[0].Source)
	require.Empty(t, diffs[0].BaselineBackend)
	require.Equal(t, "sqlite", diffs[0].Backend)
	require.Equal(t, "session-1", diffs[0].SessionID)

	baseline := &Snapshot{
		Backend:   "inmemory",
		SessionID: "session-2",
	}
	diffs = CompareSnapshots("missing-target", baseline, nil, nil)
	require.Len(t, diffs, 1)
	require.Equal(t, "inmemory", diffs[0].BaselineBackend)
	require.Empty(t, diffs[0].Backend)
	require.Equal(t, "session-2", diffs[0].SessionID)

	diffs = CompareSnapshots("missing-both", nil, nil, nil)
	require.Len(t, diffs, 1)
	require.Empty(t, diffs[0].SessionID)
}

func TestAsFloat64SupportsPortableNumericTypes(t *testing.T) {
	tests := []struct {
		value any
		want  float64
		ok    bool
	}{
		{value: json.Number("1.5"), want: 1.5, ok: true},
		{value: float64(2.5), want: 2.5, ok: true},
		{value: float32(3.5), want: 3.5, ok: true},
		{value: int(4), want: 4, ok: true},
		{value: int64(5), want: 5, ok: true},
		{value: int32(6), want: 6, ok: true},
		{value: "7", ok: false},
	}
	for _, test := range tests {
		got, ok := asFloat64(test.value)
		require.Equal(t, test.ok, ok)
		require.Equal(t, test.want, got)
	}
}

func TestWriteReportProducesStableJSON(t *testing.T) {
	report := &DiffReport{
		SchemaVersion:   ReportSchemaVersion,
		BaselineBackend: "inmemory",
		Cases: []CaseComparison{{
			Case:      "single-turn",
			SessionID: "single-turn",
			Backend:   "sqlite",
			Status:    ComparisonPassed,
		}},
		Summary: ReportSummary{
			CaseComparisons: 1,
			Passed:          1,
		},
	}
	path := filepath.Join(t.TempDir(), "report.json")
	require.NoError(t, WriteReport(path, report))

	data := requireFile(t, path)
	var decoded DiffReport
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, ReportSchemaVersion, decoded.SchemaVersion)
	require.Equal(t, "single-turn", decoded.Cases[0].Case)
}

func TestWriteReportRejectsInvalidDestinations(t *testing.T) {
	require.Error(t, WriteReport(filepath.Join(t.TempDir(), "nil.json"), nil))

	root := t.TempDir()
	parentFile := filepath.Join(root, "parent")
	require.NoError(t, os.WriteFile(parentFile, []byte("file"), 0o644))
	require.Error(
		t,
		WriteReport(
			filepath.Join(parentFile, "report.json"),
			&DiffReport{},
		),
	)
	require.Error(t, WriteReport(root, &DiffReport{}))
}

func TestWriteReportKeepsPrivatePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose Unix permission bits")
	}

	parent := filepath.Join(t.TempDir(), "private", "nested")
	path := filepath.Join(parent, "report.json")
	report := &DiffReport{SchemaVersion: ReportSchemaVersion}

	require.NoError(t, WriteReport(path, report))
	parentInfo, err := os.Stat(parent)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), parentInfo.Mode().Perm())
	reportInfo, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), reportInfo.Mode().Perm())

	require.NoError(t, os.Chmod(path, 0o600))
	require.NoError(t, WriteReport(path, report))
	reportInfo, err = os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), reportInfo.Mode().Perm())
}

func cloneSnapshotForTest(t *testing.T, in *Snapshot) *Snapshot {
	t.Helper()
	data, err := json.Marshal(in)
	require.NoError(t, err)
	var out Snapshot
	require.NoError(t, json.Unmarshal(data, &out))
	return &out
}

func requireFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}
