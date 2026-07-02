//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFullReport(t *testing.T) {
	backend, cleanup := newFullReplayBackend(t)
	defer cleanup()

	h := NewHarness(DefaultHarnessOpts())
	h.AddBackend(backend)
	report, err := h.Run(AllCases())
	require.NoError(t, err)
	require.Equal(t, 14, report.TotalCases)
	require.Equal(t, 14, report.PassedCases)
	require.Equal(t, 0, report.FailedCases)
	require.Equal(t, 0, report.SkippedCases)
	require.Len(t, report.Results, 14)
	requireAllCaseNames(t, report)

	var out bytes.Buffer
	require.NoError(t, NewReporter(&out).Write(report))
	var decoded Report
	require.NoError(t, json.Unmarshal(out.Bytes(), &decoded))
	require.Equal(t, report.TotalCases, decoded.TotalCases)
	require.Equal(t, report.PassedCases, decoded.PassedCases)
	requireAllCaseNames(t, &decoded)

	raw, err := os.ReadFile("testdata/session_memory_summary_track_diff_report.json")
	require.NoError(t, err)
	var sample Report
	require.NoError(t, json.Unmarshal(raw, &sample))
	require.False(t, sample.GeneratedAt.IsZero())
	require.Equal(t, 14, sample.TotalCases)
	require.Equal(t, 14, sample.PassedCases)
	require.Equal(t, 0, sample.FailedCases)
	require.Equal(t, 0, sample.SkippedCases)
	requireAllCaseNames(t, &sample)
}

func requireAllCaseNames(t *testing.T, report *Report) {
	t.Helper()
	got := map[string]bool{}
	for _, result := range report.Results {
		got[result.CaseName] = true
	}
	for _, tc := range AllCases() {
		require.True(t, got[tc.Name], tc.Name)
	}
}
