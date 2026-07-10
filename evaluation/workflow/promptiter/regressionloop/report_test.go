//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regressionloop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

func TestRenderMarkdownContainsDecisionAndDelta(t *testing.T) {
	report := OptimizationReport{
		Metadata: RunMetadata{AppName: "app"},
		BaselineValidation: evaluationReportFromResult(evalResult("validation", []caseSpec{
			{id: "case", metric: "metric", score: 0, status: status.EvalStatusFailed},
		})),
		CandidateValidation: evaluationReportFromResult(evalResult("validation", []caseSpec{
			{id: "case", metric: "metric", score: 1, status: status.EvalStatusPassed},
		})),
		Delta: ComputeDelta(
			evalResult("validation", []caseSpec{{id: "case", metric: "metric", score: 0, status: status.EvalStatusFailed}}),
			evalResult("validation", []caseSpec{{id: "case", metric: "metric", score: 1, status: status.EvalStatusPassed}}),
			nil,
		),
		GateDecision: GateDecision{Accepted: true, Reasons: []string{"ok"}},
		Cost:         CostSummary{ModelCalls: 1, Amount: 0.25, Source: CostSourceProvider},
	}
	md := RenderMarkdown(report)
	assert.Contains(t, md, "Optimization Report")
	assert.Contains(t, md, "newly_passed")
	assert.Contains(t, md, "Gate Decision")
	assert.Contains(t, md, "Accepted validation")
	assert.Contains(t, md, "final audited candidate")
	assert.Contains(t, md, "`0.2500`")
}

func TestMarkdownCellEscapesAndTruncatesUnicode(t *testing.T) {
	got := markdownCell("字段|值 " + strings.Repeat("长", 200))
	assert.Contains(t, got, `字段\|值`)
	assert.LessOrEqual(t, len([]rune(got)), 180)
	assert.True(t, strings.HasSuffix(got, "..."))
	assert.Equal(t, "abc", truncateMarkdownCell("abc", 3))
}

func TestWriteReportsWritesJSONAndMarkdown(t *testing.T) {
	dir := t.TempDir()
	report := OptimizationReport{
		Metadata: RunMetadata{
			AppName:    "app",
			StartedAt:  time.Unix(1, 0),
			FinishedAt: time.Unix(2, 0),
			Duration:   Duration{Duration: time.Second},
		},
		BaselineValidation: evaluationReportFromResult(evalResult("validation", []caseSpec{
			{id: "case", metric: "metric", score: 1, status: status.EvalStatusPassed},
		})),
		GateDecision: GateDecision{Accepted: false, Reasons: []string{"reject"}},
	}
	jsonPath := filepath.Join(dir, "optimization_report.json")
	mdPath := filepath.Join(dir, "optimization_report.md")
	require.NoError(t, WriteReports(report, jsonPath, mdPath))
	jsonBytes, err := os.ReadFile(jsonPath)
	require.NoError(t, err)
	var decoded OptimizationReport
	require.NoError(t, json.Unmarshal(jsonBytes, &decoded))
	assert.Equal(t, "app", decoded.Metadata.AppName)
	assert.Contains(t, string(jsonBytes), `"overallScore"`)
	assert.Contains(t, string(jsonBytes), `"evalSets"`)
	assert.NotContains(t, string(jsonBytes), `"OverallScore"`)
	assert.NotContains(t, string(jsonBytes), `"EvalSets"`)
	mdBytes, err := os.ReadFile(mdPath)
	require.NoError(t, err)
	assert.Contains(t, string(mdBytes), "Decision")
}

func TestWriteReportsReturnsFilesystemErrors(t *testing.T) {
	dir := t.TempDir()
	report := OptimizationReport{Metadata: RunMetadata{AppName: "app"}}

	parentFile := filepath.Join(dir, "file")
	require.NoError(t, os.WriteFile(parentFile, []byte("x"), 0o644))
	err := WriteReports(report, filepath.Join(parentFile, "report.json"), filepath.Join(dir, "report.md"))
	assert.ErrorContains(t, err, "create JSON report dir")

	err = WriteReports(report, filepath.Join(dir, "report.json"), filepath.Join(parentFile, "report.md"))
	assert.ErrorContains(t, err, "create markdown report dir")

	badTimeReport := OptimizationReport{Metadata: RunMetadata{StartedAt: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}}
	err = WriteReports(badTimeReport, filepath.Join(dir, "bad-time.json"), filepath.Join(dir, "bad-time.md"))
	assert.ErrorContains(t, err, "marshal optimization report")

	jsonDir := filepath.Join(dir, "json-dir")
	require.NoError(t, os.Mkdir(jsonDir, 0o755))
	err = WriteReports(report, jsonDir, filepath.Join(dir, "report.md"))
	assert.ErrorContains(t, err, "write JSON report")

	markdownDir := filepath.Join(dir, "markdown-dir")
	require.NoError(t, os.Mkdir(markdownDir, 0o755))
	err = WriteReports(report, filepath.Join(dir, "report-ok.json"), markdownDir)
	assert.ErrorContains(t, err, "write markdown report")
}

func TestBuildReportIncludesAcceptedCandidateAttributionAndRoundAudit(t *testing.T) {
	prompt := "candidate prompt"
	report := BuildReport(ReportInput{
		Config: Config{
			AppName:             "app",
			PromptSource:        "prompt.txt",
			MetricsPath:         "metrics.json",
			TrainEvalSetID:      "train",
			ValidationEvalSetID: "validation",
			Scenario:            "overfit",
			TargetSurfaceIDs:    []string{"agent#instruction"},
			Gate:                GateConfig{RequireEngineAccepted: true},
			ModelConfig:         map[string]string{"model": "fake"},
			FakeConfig:          map[string]string{"mode": "fake-engine"},
			Attribution: AttributionConfig{
				MetricCategoryHints: map[string]FailureCategory{
					"final_response": FailureFinalResponseMismatch,
				},
			},
		},
		StartedAt:  time.Unix(1, 0),
		FinishedAt: time.Unix(2, 0),
		Metrics:    []MetricDefinition{{MetricName: "final_response"}},
		BaselineValidation: evalResult("validation", []caseSpec{
			{id: "case", metric: "final_response", score: 1, status: status.EvalStatusPassed},
		}),
		Attributions: []CaseAttribution{
			{
				EvalSetID:  "train",
				EvalCaseID: "train",
				MetricName: "router_decision",
				Category:   FailureRouteError,
				Reason:     "route error",
				Evidence:   []string{"router=general_support"},
			},
		},
		PromptIterRun: &promptiterengine.RunResult{
			AcceptedProfile: &promptiter.Profile{
				Overrides: []promptiter.SurfaceOverride{
					{SurfaceID: "agent#instruction", Value: astructure.SurfaceValue{Text: &prompt}},
				},
			},
			Rounds: []promptiterengine.RoundResult{
				{
					Round: 1,
					Train: evalResult("train", []caseSpec{
						{id: "train", metric: "tool_trajectory", score: 1, status: status.EvalStatusPassed},
					}),
					Validation: evalResult("validation", []caseSpec{
						{id: "case", metric: "final_response", score: 0, status: status.EvalStatusFailed, reason: "final response mismatch"},
					}),
					Patches: &promptiter.PatchSet{
						Patches: []promptiter.SurfacePatch{
							{SurfaceID: "agent#instruction", Value: astructure.SurfaceValue{Text: &prompt}, Reason: "test patch"},
						},
					},
					Acceptance: &promptiterengine.AcceptanceDecision{Accepted: true, Reason: "accepted"},
				},
			},
		},
	})
	assert.Equal(t, "metrics.json", report.Metadata.MetricsPath)
	assert.Equal(t, []string{"final_response"}, report.Metadata.MetricNames)
	assert.Equal(t, "overfit", report.Metadata.Scenario)
	assert.Equal(t, FailureFinalResponseMismatch, report.Metadata.AttributionHints["final_response"])
	assert.Equal(t, "candidate prompt", report.CandidatePrompt)
	require.Len(t, report.Rounds, 1)
	require.Len(t, report.Rounds[0].Patches, 1)
	assert.Equal(t, "test patch", report.Rounds[0].Patches[0].Reason)
	assert.Equal(t, 1, report.BaselineFailureAttributionSummary.ByCategory[FailureRouteError])
	assert.Equal(t, 1, report.CandidateFailureAttributionSummary.ByCategory[FailureFinalResponseMismatch])
	assert.Equal(t, 1, report.FailureAttributionSummary.ByCategory[FailureFinalResponseMismatch])
	assert.Equal(t, 2, report.FailureAttributionSummary.Total)
	md := RenderMarkdown(report)
	assert.Contains(t, md, "Baseline failures: `1`; candidate failures: `1`; combined: `2`")
	assert.Contains(t, md, "### Baseline")
	assert.Contains(t, md, "### Candidate")
	assert.Contains(t, md, "### Failure Details")
	assert.Contains(t, md, "| train | train | router_decision | route_error |  | route error | router=general_support |")
	assert.Contains(t, md, "final response mismatch")
}

func TestCandidatePromptCoversProfileValueVariants(t *testing.T) {
	assert.Empty(t, CandidatePrompt(nil))
	assert.Empty(t, profilePromptText(nil))
	assert.Empty(t, profilePromptText(&promptiter.Profile{}))

	skillProfile := &promptiter.Profile{Overrides: []promptiter.SurfaceOverride{
		{Value: astructure.SurfaceValue{Skills: []astructure.SkillRef{{Description: "skill prompt"}}}},
	}}
	assert.Equal(t, "skill prompt", profilePromptText(skillProfile))

	toolProfile := &promptiter.Profile{Overrides: []promptiter.SurfaceOverride{
		{Value: astructure.SurfaceValue{Tools: []astructure.ToolRef{{Description: "tool prompt"}}}},
	}}
	assert.Equal(t, "tool prompt", profilePromptText(toolProfile))

	fewShotProfile := &promptiter.Profile{Overrides: []promptiter.SurfaceOverride{
		{Value: astructure.SurfaceValue{FewShot: []astructure.FewShotExample{
			{Messages: []astructure.FewShotMessage{{Content: "few shot prompt"}}},
		}}},
	}}
	assert.Equal(t, "few shot prompt", profilePromptText(fewShotProfile))
	assert.Equal(t, "few shot prompt", CandidatePrompt(&promptiterengine.RunResult{
		AcceptedProfile: fewShotProfile,
	}))
}

func TestTraceReportAndSnapshotHelpers(t *testing.T) {
	assert.Nil(t, traceReportFromTrace(nil))
	assert.Nil(t, snapshotReportFromSnapshot(nil))
	assert.Equal(t, "snapshot", snapshotReportFromSnapshot(&atrace.Snapshot{Text: "snapshot"}).Text)

	trace := &atrace.Trace{
		RootAgentName:    "root",
		RootInvocationID: "root-invocation",
		SessionID:        "session",
		Status:           atrace.TraceStatusCompleted,
		Steps: []atrace.Step{
			{
				StepID:             "step",
				InvocationID:       "invocation",
				ParentInvocationID: "parent",
				AgentName:          "agent",
				Branch:             "branch",
				NodeID:             "node",
				PredecessorStepIDs: []string{"prev"},
				AppliedSurfaceIDs:  []string{"agent#instruction"},
				Input:              &atrace.Snapshot{Text: "input"},
				Output:             &atrace.Snapshot{Text: "output"},
				Error:              "err",
			},
		},
	}
	report := traceReportFromTrace(trace)
	require.NotNil(t, report)
	assert.Equal(t, "root", report.RootAgentName)
	require.Len(t, report.Steps, 1)
	assert.Equal(t, "input", report.Steps[0].Input.Text)
	assert.Equal(t, "output", report.Steps[0].Output.Text)
}

func TestBuildReportUsesRejectedFinalCandidateForAudit(t *testing.T) {
	prompt := "rejected candidate prompt"
	report := BuildReport(ReportInput{
		Config: Config{
			AppName:             "app",
			PromptSource:        "prompt.txt",
			MetricsPath:         "metrics.json",
			TrainEvalSetID:      "train",
			ValidationEvalSetID: "validation",
			TargetSurfaceIDs:    []string{"agent#instruction"},
			Gate:                GateConfig{RequireEngineAccepted: true},
		},
		StartedAt:  time.Unix(1, 0),
		FinishedAt: time.Unix(2, 0),
		BaselineValidation: evalResult("validation", []caseSpec{
			{id: "case", metric: "final_response", score: 1, status: status.EvalStatusPassed},
		}),
		PromptIterRun: &promptiterengine.RunResult{
			BaselineValidation: evalResult("validation", []caseSpec{
				{id: "case", metric: "final_response", score: 1, status: status.EvalStatusPassed},
			}),
			AcceptedProfile: &promptiter.Profile{},
			Rounds: []promptiterengine.RoundResult{
				{
					Round: 1,
					Validation: evalResult("validation", []caseSpec{
						{id: "case", metric: "final_response", score: 0, status: status.EvalStatusFailed, reason: "final response mismatch"},
					}),
					OutputProfile: &promptiter.Profile{
						Overrides: []promptiter.SurfaceOverride{
							{SurfaceID: "agent#instruction", Value: astructure.SurfaceValue{Text: &prompt}},
						},
					},
					Acceptance: &promptiterengine.AcceptanceDecision{Accepted: false, Reason: "rejected"},
				},
			},
		},
	})
	require.NotNil(t, report.CandidateValidation)
	assert.Equal(t, 0.0, report.CandidateValidation.OverallScore)
	assert.Equal(t, "rejected candidate prompt", report.CandidatePrompt)
	assert.Equal(t, 1, report.CandidateFailureAttributionSummary.Total)
	assert.False(t, report.GateDecision.Accepted)
}

func TestBuildReportUsesExplicitCandidateValidationOverride(t *testing.T) {
	report := BuildReport(ReportInput{
		Config: Config{
			Gate: GateConfig{RequireEngineAccepted: false},
		},
		BaselineValidation: evalResult("validation", []caseSpec{
			{id: "case", metric: "final_response", score: 0, status: status.EvalStatusFailed},
		}),
		CandidateValidation: evalResult("validation", []caseSpec{
			{id: "case", metric: "final_response", score: 1, status: status.EvalStatusPassed},
		}),
	})
	require.NotNil(t, report.CandidateValidation)
	assert.Equal(t, 1.0, report.CandidateValidation.OverallScore)
	assert.Equal(t, 1, report.Delta.Summary.NewlyPassed)
}
