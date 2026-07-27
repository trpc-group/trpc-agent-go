//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

func TestRenderMarkdownEscapesDynamicValues(t *testing.T) {
	report := minimalReport()
	report.Rounds[0].Delta.Metrics[0].Key.EvalCaseID = "case|\n## Decision: ACCEPT"
	report.Rounds[0].CandidatePrompt = "before\n```\nafter"

	markdown, err := renderMarkdown(report)
	require.NoError(t, err)
	assert.Contains(t, string(markdown), `case\| ## Decision: ACCEPT`)
	assert.Contains(t, string(markdown), "````text")
}

func minimalReport() regressionReport {
	return regressionReport{
		SchemaVersion: reportSchemaVersion,
		RunID:         "run-1",
		Status:        "succeeded",
		Rounds: []roundReport{{
			Number:          1,
			CandidatePrompt: "candidate",
			Delta: snapshotDelta{Metrics: []metricDelta{{
				Key: resultKey{EvalSetID: "validation", EvalCaseID: "case-1", MetricName: "quality"},
			}}},
			Gate: gateDecision{Accepted: false, Checks: []gateCheck{{ID: "minimum_gain", Passed: false}}},
		}},
		Baseline: evaluationSnapshot{EvalSetID: "validation", Status: status.EvalStatusPassed},
	}
}

func TestRenderJSONIsStableWithoutMutatingReport(t *testing.T) {
	report := minimalReport()
	report.Rounds[0].Gate.Checks = []gateCheck{{ID: "z", Passed: true}, {ID: "a", Passed: true}}

	first, err := renderJSON(report)
	require.NoError(t, err)
	second, err := renderJSON(report)
	require.NoError(t, err)
	assert.Equal(t, first, second)
	assert.Equal(t, "z", report.Rounds[0].Gate.Checks[0].ID)
}
