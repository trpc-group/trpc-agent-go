//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"encoding/json"
	"strings"
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

func TestRenderReportRedactsAndBoundsDynamicEvidence(t *testing.T) {
	const secret = "sk-example-secret-value"
	report := minimalReport()
	report.Rounds[0].CandidatePrompt = "Authorization: Bearer " + secret + "\n" +
		strings.Repeat("candidate output ", maxReportTextBytes)
	report.Rounds[0].Error = "api_key=" + secret
	report.Rounds[0].Attributions = []caseAttribution{{
		EvalCaseID: "case-1",
		Primary: attribution{
			Category: attributionRuntimeError,
			Evidence: "token: " + secret,
		},
	}}
	report.Baseline.Cases = []caseResult{{
		EvalSetID: "validation", EvalCaseID: "case-1",
		ExecutionError: "password=" + secret,
		Metrics:        []metricResult{{Name: "quality", Reason: "secret=" + secret}},
	}}
	report.TerminalError = "credential: " + secret

	jsonReport, err := renderJSON(report)
	require.NoError(t, err)
	markdownReport, err := renderMarkdown(report)
	require.NoError(t, err)
	assert.NotContains(t, string(jsonReport), secret)
	assert.NotContains(t, string(markdownReport), secret)

	var rendered regressionReport
	require.NoError(t, json.Unmarshal(jsonReport, &rendered))
	assert.LessOrEqual(t, len(rendered.Rounds[0].CandidatePrompt), maxReportTextBytes)
	assert.Contains(t, rendered.Rounds[0].CandidatePrompt, reportTruncationMarker)
	assert.Equal(t, "api_key=[REDACTED]", rendered.Rounds[0].Error)
	assert.Equal(t, "token: [REDACTED]", rendered.Rounds[0].Attributions[0].Primary.Evidence)
	assert.Equal(t, "password=[REDACTED]", rendered.Baseline.Cases[0].ExecutionError)
	assert.Equal(t, "secret=[REDACTED]", rendered.Baseline.Cases[0].Metrics[0].Reason)
	assert.Equal(t, "credential: [REDACTED]", rendered.TerminalError)
}

func TestSanitizeReportTextRedactsQuotedAssignments(t *testing.T) {
	const input = `{"api_key":"generic-value-1234","password":"hunter-1234"}`
	got := sanitizeReportText(input)
	assert.NotContains(t, got, "generic-value-1234")
	assert.NotContains(t, got, "hunter-1234")
}
