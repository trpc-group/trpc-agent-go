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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReporterBuildReport(t *testing.T) {
	report := BuildReport([]CaseResult{
		{CaseName: "pass", OverallStatus: StatusPassed},
		{CaseName: "fail", OverallStatus: StatusFailed, Comparisons: []ComparisonResult{{Diffs: []DiffResult{{Severity: SeverityError}}}}},
		{CaseName: "skip", OverallStatus: StatusSkipped},
	}, []string{"a", "b"}, "a")

	require.Equal(t, 3, report.TotalCases)
	require.Equal(t, 1, report.PassedCases)
	require.Equal(t, 1, report.FailedCases)
	require.Equal(t, 1, report.SkippedCases)
	require.Equal(t, 1, report.TotalDiffs)
	require.Equal(t, 1, report.ErrorDiffs)
}

func TestReporterJSON(t *testing.T) {
	var buf bytes.Buffer
	report := BuildReport([]CaseResult{{CaseName: "pass", OverallStatus: StatusPassed}}, []string{"a"}, "a")
	require.NoError(t, NewReporter(&buf).Write(report))

	var decoded Report
	require.NoError(t, json.Unmarshal(buf.Bytes(), &decoded))
	require.Equal(t, "a", decoded.Reference)
	require.Contains(t, buf.String(), `"total_cases"`)
}
