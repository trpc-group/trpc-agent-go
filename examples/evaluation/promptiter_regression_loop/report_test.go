//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func samplePipelineResult() *PipelineResult {
	baselineValidation := &EvalResult{OverallScore: 0.0, Cases: []CaseScore{
		{EvalCaseID: "validation_01_baseball", Passed: false, Score: 0.0},
		{EvalCaseID: "validation_02_ice_hockey", Passed: false, Score: 0.0},
		{EvalCaseID: "validation_03_badminton", Passed: false, Score: 0.0},
	}}
	candidateValidation := &EvalResult{OverallScore: 1.0, Cases: []CaseScore{
		{EvalCaseID: "validation_01_baseball", Passed: true, Score: 1.0},
		{EvalCaseID: "validation_02_ice_hockey", Passed: true, Score: 1.0},
		{EvalCaseID: "validation_03_badminton", Passed: true, Score: 1.0},
	}}
	deltas := ComputeDeltas(baselineValidation.Cases, candidateValidation.Cases)
	gate := EvaluateGate(
		GateConfig{MinScoreGain: 0.05, MaxNewHardFails: 0, MaxModelCalls: 200, MaxLatencyMs: 180000},
		baselineValidation, candidateValidation, deltas, 42, 1500,
	)
	return &PipelineResult{
		Config: &Config{
			TrainEvalSetID:      "train",
			ValidationEvalSetID: "validation",
			MetricFileID:        "headline-card",
			BaselinePromptFile:  "prompt.txt",
			TargetSurfaceID:     "candidate#instruction",
			MaxRounds:           3,
			Gate:                GateConfig{MinScoreGain: 0.05, MaxNewHardFails: 0, MaxModelCalls: 200, MaxLatencyMs: 180000},
			Model:               ModelConfig{Provider: "fake", Name: "scripted", Seed: 42},
		},
		RunID:              "run-test",
		StartedAt:          "2026-08-05T00:00:00Z",
		DurationMs:         1500,
		BaselinePrompt:     "baseline prompt",
		BaselineTrain:      &EvalResult{OverallScore: 0.0},
		BaselineValidation: baselineValidation,
		Rounds: []PipelineRound{
			{
				Round:           1,
				CandidatePrompt: "[STAGE_GOOD] candidate",
				Train:           &EvalResult{OverallScore: 0.0},
				Validation:      candidateValidation,
				EngineAccepted:  true,
				EngineReason:    "candidate score gain satisfies acceptance policy",
				GateAccepted:    true,
				GateReason:      "all gate checks passed",
				Deltas:          deltas,
			},
		},
		FinalAcceptedRound:   1,
		FinalCandidatePrompt: "[STAGE_GOOD] candidate",
		FinalGate:            gate,
		Recommendation:       "接受并回写第 1 轮候选 prompt",
		ModelCalls:           42,
		LatencyMs:            1500,
	}
}

func TestBuildReport_ContainsRequiredFields(t *testing.T) {
	report := BuildReport(samplePipelineResult())

	require.Equal(t, "promptiter_regression_loop", report.Pipeline.Name)
	require.Equal(t, int64(42), report.Pipeline.RandomSeed)
	require.InDelta(t, 0.0, report.Baseline.ValidationScore, 1e-9)
	require.Equal(t, 1, report.Optimization.FinalAcceptedRound)
	require.True(t, report.Gate.Accepted)
	require.NotEmpty(t, report.Gate.Reason)
	require.Len(t, report.Gate.Checks, 4)

	// The final candidate validation score must be present.
	require.InDelta(t, 1.0, report.Optimization.Rounds[0].ValidationScore, 1e-9)
	// Per-case delta must be recorded.
	require.Len(t, report.Optimization.Rounds[0].Deltas, 3)
	require.Equal(t, DeltaNewlyPassed, report.Optimization.Rounds[0].Deltas[0].Outcome)
	// Baseline per-case records must be present.
	require.Len(t, report.Baseline.ValidationCases, 3)
	require.NotEmpty(t, report.Recommendation)
}

func TestWriteJSONReport(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "optimization_report.json")
	report := BuildReport(samplePipelineResult())
	require.NoError(t, WriteJSONReport(report, path))

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	content := string(raw)
	for _, required := range []string{
		"\"pipeline\"", "\"baseline\"", "\"optimization\"", "\"gate\"",
		"\"validationScore\"", "\"finalAcceptedRound\"", "\"recommendation\"",
		"\"validation_score_gain\"", "\"checks\"",
	} {
		require.Contains(t, content, required, "JSON report missing %s", required)
	}
}

func TestWriteMarkdownReport(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "optimization_report.md")
	report := BuildReport(samplePipelineResult())
	require.NoError(t, WriteMarkdownReport(report, path))

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	content := string(raw)
	for _, required := range []string{
		"基线评测", "优化轮次", "接受门禁", "失败归因", "成本与预算", "优化建议",
		"validation_score_gain", "逐 case delta", "接受候选",
	} {
		require.True(t, strings.Contains(content, required), "Markdown report missing %q", required)
	}
}
