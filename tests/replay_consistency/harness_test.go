package replayconsistency

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestDefaultReplayCases(t *testing.T) {
	cases := DefaultReplayCases()
	require.Len(t, cases, 10)
	assert.Equal(t, "single_turn_text", cases[0].Name)
	assert.Equal(t, "failure_retry_dedup", cases[9].Name)
}

func TestNormalizeSnapshot_SortsAndCanonicalizes(t *testing.T) {
	snapshot := NormalizedSnapshot{
		State:  []NormalizedState{{Key: "b", Value: "2"}, {Key: "a", Value: "1"}},
		Events: []NormalizedEvent{{Index: 2, ID: "b"}, {Index: 1, ID: "a"}},
	}
	normalized := NormalizeSnapshot(snapshot)
	require.Len(t, normalized.State, 2)
	require.Len(t, normalized.Events, 2)
	assert.Equal(t, "a", normalized.State[0].Key)
	assert.Equal(t, 1, normalized.Events[0].Index)
}

func TestNormalizeStateAndMemory(t *testing.T) {
	state := session.StateMap{"json": []byte(`{"b":2,"a":1}`), "plain": []byte("hello")}
	normalizedState := NormalizeState(state)
	require.Len(t, normalizedState, 2)
	assert.Equal(t, "json", normalizedState[0].Key)
	assert.Equal(t, `{"a":1,"b":2}`, normalizedState[0].Value)

	entry := &memory.Entry{
		ID:      "m1",
		AppName: "app",
		UserID:  "user",
		Memory: &memory.Memory{
			Memory:      "prefers concise replies",
			Topics:      []string{"style", "preference"},
			LastUpdated: ptrTime(time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)),
		},
		UpdatedAt: time.Date(2026, 1, 1, 10, 1, 0, 0, time.UTC),
	}
	normalizedMemory := NormalizeMemoryEntry(entry)
	assert.Equal(t, "m1", normalizedMemory.ID)
	assert.Equal(t, "prefers concise replies", normalizedMemory.Content)
}

func TestCompareSnapshotsAndBuildReport(t *testing.T) {
	baseline := NormalizedSnapshot{Events: []NormalizedEvent{{Index: 0, ID: "a", Content: "hello"}}}
	actual := NormalizedSnapshot{Events: []NormalizedEvent{{Index: 0, ID: "b", Content: "hello world"}}}
	diffs := CompareSnapshots("case-1", "sqlite", baseline, actual)
	require.NotEmpty(t, diffs)
	assert.Equal(t, "case-1", diffs[0].CaseName)
	assert.Equal(t, "events[0].id", diffs[0].Path)

	report := BuildReport([]CaseResult{{CaseName: "case-1", Backend: "sqlite", Diffs: diffs}})
	assert.Equal(t, 1, report.Summary.CasesRun)
	assert.Equal(t, 1, report.Summary.BackendsRun)
	assert.GreaterOrEqual(t, report.Summary.DiffCount, 1)

	encoded, err := MarshalReport(report)
	require.NoError(t, err)
	assert.True(t, json.Valid(encoded))
}

func TestReplayHarnessRunReturnsSkeletonReport(t *testing.T) {
	backend, err := newInMemoryReplayBackend()
	require.NoError(t, err)
	defer backend.Close()

	harness := ReplayHarness{
		Backends: []Backend{backend},
		Options:  HarnessOptions{MaxCases: 1},
	}
	report, err := harness.Run(contextBackground())
	require.NoError(t, err)
	assert.NotEmpty(t, report.Results)
	assert.Equal(t, 1, report.Summary.CasesRun)
}

func TestFullReplayLightMode(t *testing.T) {
	backend, err := newInMemoryReplayBackend()
	require.NoError(t, err)
	defer backend.Close()

	harness := ReplayHarness{
		Backends: []Backend{backend},
		Options:  HarnessOptions{LightMode: true},
	}
	report, err := harness.Run(contextBackground())
	require.NoError(t, err)
	require.NotEmpty(t, report.Results)
	assert.Equal(t, 10, report.Summary.CasesRun)
	t.Logf("Light mode report: %d cases, %d diffs (allowed: %d)",
		report.Summary.CasesRun, report.Summary.DiffCount, report.Summary.AllowedDiffCount)
}

func ptrTime(t time.Time) *time.Time { return &t }

func contextBackground() context.Context { return context.Background() }

// TestCrossBackendReplay runs all scenarios against InMemory baseline and SQLite test backend.
// This is the primary integration test for the replay consistency framework.
func TestCrossBackendReplay(t *testing.T) {
	baseline, err := newInMemoryReplayBackend()
	require.NoError(t, err)
	defer baseline.Close()

	sqliteBackend, err := newSQLiteReplayBackend()
	require.NoError(t, err)
	defer sqliteBackend.Close()

	harness := ReplayHarness{
		Backends: []Backend{baseline, sqliteBackend},
	}
	report, err := harness.Run(contextBackground())
	require.NoError(t, err)
	require.NotEmpty(t, report.Results)

	t.Logf("Cross-backend report: %d cases, %d diffs (allowed: %d)",
		report.Summary.CasesRun, report.Summary.DiffCount, report.Summary.AllowedDiffCount)

	// Check that unexpected diffs are zero.
	for _, result := range report.Results {
		for _, diff := range result.Diffs {
			if !diff.AllowedDiff {
				t.Errorf("Unexpected diff in case %q on backend %q: path=%s baseline=%q actual=%q",
					result.CaseName, result.Backend, diff.Path, diff.Baseline, diff.Actual)
			}
		}
	}

	// Write the report to the example JSON file.
	reportData, err := MarshalReport(report)
	require.NoError(t, err)
	err = os.WriteFile("session_memory_summary_track_diff_report.json", reportData, 0644)
	require.NoError(t, err)
	t.Logf("Report written to session_memory_summary_track_diff_report.json")

	// Assert all cases passed (only allowed diffs).
	for _, result := range report.Results {
		for _, diff := range result.Diffs {
			if !diff.AllowedDiff {
				assert.True(t, diff.AllowedDiff,
					"Unexpected diff: case=%q backend=%q path=%q",
					result.CaseName, result.Backend, diff.Path)
			}
		}
	}
}
