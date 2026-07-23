//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sessions

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReplayConsistency(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	reportPath := os.Getenv("REPLAY_REPORT_PATH")
	if reportPath == "" {
		reportPath = filepath.Join(
			t.TempDir(),
			"session_memory_summary_diff_report.json",
		)
	}
	result, err := RunReplayConsistency(ctx, RunnerConfig{
		CaseDir:    filepath.Join("testdata", "replay_cases"),
		ReportPath: reportPath,
		TempDir:    t.TempDir(),
		NormalizeOptions: NormalizeOptions{
			NormalizeGeneratedMemoryIDs: true,
			NilEqualsEmpty:              true,
		},
		RunMutations: true,
	})
	require.NoError(t, err)
	require.GreaterOrEqual(t, result.Report.Summary.CaseCount, 19)
	wantFactories, err := BackendFactoriesFromEnv()
	require.NoError(t, err)
	require.Equal(t, len(wantFactories), result.Report.Summary.BackendCount)
	if result.Report.Summary.UnexpectedDiffCount != 0 {
		for _, tc := range result.Report.Cases {
			for _, comparison := range tc.Comparisons {
				for _, diff := range comparison.Differences {
					if !diff.Allowed {
						t.Logf("case=%s backend=%s path=%s reference=%v actual=%v",
							tc.CaseID, comparison.ActualBackend, diff.Path,
							diff.Reference, diff.Actual)
					}
				}
			}
		}
	}
	require.Zero(t, result.Report.Summary.UnexpectedDiffCount,
		FormatReportSummary(result.Report))
	require.Equal(t, 1.0, result.Report.Summary.MutationDetectionRate,
		FormatReportSummary(result.Report))
	require.Less(t, result.Report.Summary.DurationMS, int64(30_000))

	wantSummaryMutations := map[string]string{
		"10_summary_missing":       MutationSummaryMissing,
		"11_summary_overwrite":     MutationSummaryOverwrite,
		"12_summary_wrong_session": MutationSummaryWrongSession,
		"15_event_postwrite_retry": MutationEventDuplicate,
		"16_state_summary_failure": MutationStateDirty,
		"18_track_observability":   MutationTrackPayload,
	}
	for _, tc := range result.Report.Cases {
		want, ok := wantSummaryMutations[tc.CaseID]
		if !ok {
			continue
		}
		require.Len(t, tc.Mutations, 1)
		require.Equal(t, want, tc.Mutations[0].Name)
		require.True(t, tc.Mutations[0].Detected)
		require.NotEmpty(t, tc.Mutations[0].Differences)
		for _, diff := range tc.Mutations[0].Differences {
			require.NotEmpty(t, diff.Path)
			require.NotEmpty(t, diff.SessionID)
			if want == MutationSummaryMissing ||
				want == MutationSummaryOverwrite ||
				want == MutationSummaryWrongSession {
				require.NotEmpty(t, diff.SummaryID)
			}
		}
	}
}

func TestCompareSnapshotsAllowedDiff(t *testing.T) {
	reference := comparisonFixture("reference")
	actual := comparisonFixture("actual")
	path := "$.sessions[0].events[0].content"

	withoutRule := CompareSnapshots(
		reference, actual, "inmemory", "sqlite", nil,
	)
	require.False(t, withoutRule.Equal)
	require.Len(t, withoutRule.Differences, 1)
	require.False(t, withoutRule.Differences[0].Allowed)

	withRule := CompareSnapshots(
		reference,
		actual,
		"inmemory",
		"sqlite",
		[]AllowedDiffRule{{
			Path: path, Backend: "sqlite",
			Reason: "explicit test-only backend representation difference",
		}},
	)
	require.True(t, withRule.Equal)
	require.Len(t, withRule.Differences, 1)
	require.True(t, withRule.Differences[0].Allowed)
	require.Equal(t, "session-allowed", withRule.Differences[0].SessionID)
	require.NotNil(t, withRule.Differences[0].EventIndex)
	require.Equal(t, 0, *withRule.Differences[0].EventIndex)
	require.Equal(t, "reference", withRule.Differences[0].Reference)
	require.Equal(t, "actual", withRule.Differences[0].Actual)

	wrongBackendRule := CompareSnapshots(
		reference,
		actual,
		"inmemory",
		"sqlite",
		[]AllowedDiffRule{{
			Path: path, Backend: "redis", Reason: "must not match sqlite",
		}},
	)
	require.False(t, wrongBackendRule.Equal)
	require.False(t, wrongBackendRule.Differences[0].Allowed)
}

func TestLoadReplayCaseAllowedDiff(t *testing.T) {
	fixturePath := filepath.Join(t.TempDir(), "allowed-diff.jsonl")
	fixture := []byte(
		"{\"action\":\"metadata\",\"version\":1,\"id\":\"allowed-diff\"}\n" +
			"{\"action\":\"allow_diff\",\"allowed_diff\":{\"path\":\"$.memories[*].id\"," +
			"\"backend\":\"redis\",\"reason\":\"generated id\"}}\n" +
			"{\"action\":\"create_session\",\"session_id\":\"session-allowed\"}\n",
	)
	require.NoError(t, os.WriteFile(fixturePath, fixture, 0o600))
	testCase, err := LoadReplayCase(fixturePath)
	require.NoError(t, err)
	require.Len(t, testCase.AllowedDiff, 1)
	require.Equal(t, "$.memories[*].id", testCase.AllowedDiff[0].Path)
	require.Equal(t, "redis", testCase.AllowedDiff[0].Backend)
	require.Equal(t, "generated id", testCase.AllowedDiff[0].Reason)
}

func comparisonFixture(content string) CanonicalSnapshot {
	return CanonicalSnapshot{Snapshot: Snapshot{
		CaseID: "allowed-diff",
		Sessions: []SessionSnapshot{{
			ID: "session-allowed",
			Events: []EventSnapshot{{
				ID: "event-allowed", Index: 0, Content: content,
			}},
		}},
	}}
}

func TestBackendFactoriesFromEnv(t *testing.T) {
	for _, name := range []string{
		"REPLAY_BACKENDS", "REPLAY_SKIP_INMEMORY", "REPLAY_SKIP_SQL",
		"REPLAY_SKIP_SQLITE", "REPLAY_SKIP_REDIS",
	} {
		t.Setenv(name, "")
	}
	t.Setenv("REPLAY_BACKENDS", "inmemory,sqlite,redis")
	t.Setenv("REPLAY_SKIP_REDIS", "true")
	factories, err := BackendFactoriesFromEnv()
	require.NoError(t, err)
	require.Equal(t, []string{"inmemory", "sqlite"}, factoryNames(factories))

	t.Setenv("REPLAY_SKIP_REDIS", "false")
	t.Setenv("REPLAY_SKIP_SQL", "true")
	factories, err = BackendFactoriesFromEnv()
	require.NoError(t, err)
	require.Equal(t, []string{"inmemory", "redis"}, factoryNames(factories))

	t.Setenv("REPLAY_SKIP_SQL", "false")
	t.Setenv("REPLAY_BACKENDS", "unknown")
	_, err = BackendFactoriesFromEnv()
	require.ErrorContains(t, err, "unknown REPLAY_BACKENDS entry")
}

func factoryNames(factories []BackendFactory) []string {
	names := make([]string, 0, len(factories))
	for _, factory := range factories {
		names = append(names, factory.Name())
	}
	return names
}

type failingBackendFactory struct{}

func (failingBackendFactory) Name() string { return "failing" }
func (failingBackendFactory) Create(context.Context, BackendConfig) (Backend, error) {
	return nil, errors.New("injected backend creation failure")
}

func TestRunReplayConsistencyWritesFailureReport(t *testing.T) {
	caseDir := t.TempDir()
	fixture := []byte(
		"{\"action\":\"metadata\",\"version\":1,\"id\":\"failure-report\"}\n" +
			"{\"action\":\"create_session\",\"session_id\":\"session-failure\"}\n",
	)
	require.NoError(t, os.WriteFile(
		filepath.Join(caseDir, "failure-report.jsonl"), fixture, 0o600,
	))
	reportPath := filepath.Join(t.TempDir(), "failure-report.json")
	result, err := RunReplayConsistency(context.Background(), RunnerConfig{
		CaseDir: caseDir, ReportPath: reportPath, TempDir: t.TempDir(),
		BackendFactories: []BackendFactory{failingBackendFactory{}},
	})
	require.ErrorContains(t, err, "injected backend creation failure")
	require.NotNil(t, result)
	require.Equal(t, "failed", result.Report.Status)
	require.Len(t, result.Report.Cases, 1)
	require.NotEmpty(t, result.Report.Cases[0].Error)

	raw, readErr := os.ReadFile(reportPath)
	require.NoError(t, readErr)
	var persisted ReplayReport
	require.NoError(t, json.Unmarshal(raw, &persisted))
	require.Equal(t, "failed", persisted.Status)
	require.Contains(t, persisted.Error, "injected backend creation failure")
	require.Contains(t, persisted.Cases[0].Error, "injected backend creation failure")
}

func TestRunReplayConsistencyWritesActionFailureReport(t *testing.T) {
	caseDir := t.TempDir()
	fixture := []byte(
		"{\"action\":\"metadata\",\"version\":1,\"id\":\"action-failure-report\"}\n" +
			"{\"action\":\"create_session\",\"session_id\":\"session-failure\"}\n" +
			"{\"action\":\"append_event\",\"session_id\":\"session-failure\"," +
			"\"failure\":{\"fail_before\":true},\"event\":{\"id\":\"event-failure\"," +
			"\"role\":\"user\",\"content\":\"fail\",\"timestamp\":\"2026-01-01T00:00:00Z\"}}\n",
	)
	require.NoError(t, os.WriteFile(
		filepath.Join(caseDir, "action-failure-report.jsonl"), fixture, 0o600,
	))
	reportPath := filepath.Join(t.TempDir(), "action-failure-report.json")
	result, err := RunReplayConsistency(context.Background(), RunnerConfig{
		CaseDir: caseDir, ReportPath: reportPath, TempDir: t.TempDir(),
		BackendFactories: []BackendFactory{InMemoryBackendFactory{}},
	})
	require.ErrorContains(t, err, "injected failure before write")
	require.NotNil(t, result)
	require.Equal(t, "failed", result.Report.Status)
	require.Len(t, result.Report.Cases, 1)
	require.Len(t, result.Report.Cases[0].Runs, 1)
	require.Contains(t, result.Report.Cases[0].Runs[0].Error, "injected failure before write")
	require.Len(t, result.Report.Cases[0].Runs[0].ActionResults, 2)
	require.False(t, result.Report.Cases[0].Runs[0].ActionResults[1].Success)

	raw, readErr := os.ReadFile(reportPath)
	require.NoError(t, readErr)
	var persisted ReplayReport
	require.NoError(t, json.Unmarshal(raw, &persisted))
	require.Equal(t, "failed", persisted.Status)
	require.Contains(t, persisted.Error, "injected failure before write")
}
