//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAttributeFailuresUsesActualEvidence(t *testing.T) {
	snapshot := &evaluationSnapshot{
		Identity: snapshotIdentity{Split: "train", DatasetHash: "dataset", MetricsHash: "metrics", ProfileHash: "profile"},
		Cases: []caseEvidence{
			{
				CaseID:      "response",
				RunID:       1,
				Status:      "failed",
				Metrics:     []metricEvidence{{MetricName: "final", Status: "failed"}},
				Invocations: []invocationEvidence{{FinalResponse: "wrong", ExpectedFinalResponse: "right"}},
			},
			{
				CaseID:  "tool-selection",
				RunID:   1,
				Status:  "failed",
				Metrics: []metricEvidence{{MetricName: "tool", Status: "failed"}},
				Invocations: []invocationEvidence{{
					ActualTools:   []toolEvidence{{Name: "wrong"}},
					ExpectedTools: []toolEvidence{{Name: "right"}},
				}},
			},
			{
				CaseID:  "tool-argument",
				RunID:   1,
				Status:  "failed",
				Metrics: []metricEvidence{{MetricName: "tool", Status: "failed"}},
				Invocations: []invocationEvidence{{
					ActualTools:   []toolEvidence{{Name: "lookup", ArgumentsHash: "a"}},
					ExpectedTools: []toolEvidence{{Name: "lookup", ArgumentsHash: "b"}},
				}},
			},
			{
				CaseID:  "format",
				RunID:   1,
				Status:  "failed",
				Metrics: []metricEvidence{{MetricName: "format", Status: "failed", Reason: "json parse failed"}},
			},
			{
				CaseID:  "route",
				RunID:   1,
				Status:  "failed",
				Metrics: []metricEvidence{{MetricName: "route", Status: "failed"}},
				Trace:   traceEvidence{Steps: []traceStepEvidence{{Error: "wrong branch selected by router"}}},
			},
			{
				CaseID:  "retrieval",
				RunID:   1,
				Status:  "failed",
				Metrics: []metricEvidence{{MetricName: "rubric", Status: "failed", Reason: "knowledge retrieval recall was insufficient"}},
			},
		},
	}

	got, err := attributeFailures(snapshot)
	require.NoError(t, err)
	require.Len(t, got, 6)
	categories := map[string]string{}
	for _, item := range got {
		require.NotEmpty(t, item.MetricName)
		require.NotEmpty(t, item.Reason)
		require.NotEmpty(t, item.EvidenceRefs)
		categories[item.CaseID] = item.PrimaryCategory
	}
	require.Equal(t, "final_response_mismatch", categories["response"])
	require.Equal(t, "tool_selection_error", categories["tool-selection"])
	require.Equal(t, "tool_argument_error", categories["tool-argument"])
	require.Equal(t, "output_format_error", categories["format"])
	require.Equal(t, "route_error", categories["route"])
	require.Equal(t, "knowledge_retrieval_insufficient", categories["retrieval"])
}

func TestCompareSnapshotsStrictTransitions(t *testing.T) {
	baseline := testSnapshot("validation", 0.5, map[string]metricEvidence{
		"case-a": {MetricName: "metric", Score: 0, Status: "failed"},
		"case-b": {MetricName: "metric", Score: 1, Status: "passed"},
		"case-c": {MetricName: "metric", Score: 0.5, Status: "failed"},
	})
	candidate := testSnapshot("validation", 0.75, map[string]metricEvidence{
		"case-a": {MetricName: "metric", Score: 1, Status: "passed"},
		"case-b": {MetricName: "metric", Score: 0, Status: "failed"},
		"case-d": {MetricName: "metric", Score: 1, Status: "passed"},
	})

	delta, err := compareSnapshots(baseline, candidate)
	require.NoError(t, err)
	require.InDelta(t, 0.25, delta.OverallDelta, 1e-9)
	transitions := map[string]string{}
	for _, item := range delta.Metrics {
		transitions[item.CaseID] = item.Transition
	}
	require.Equal(t, "newly_passed", transitions["case-a"])
	require.Equal(t, "newly_failed", transitions["case-b"])
	require.Equal(t, "missing_in_candidate", transitions["case-c"])
	require.Equal(t, "unexpected_in_candidate", transitions["case-d"])
}

func TestGateRejectsOverfitAndIncompleteCandidate(t *testing.T) {
	baselineTrain := testSnapshot("train", 0.3, map[string]metricEvidence{"train": {MetricName: "metric", Score: 0.3, Status: "failed"}})
	candidateTrain := testSnapshot("train", 1.0, map[string]metricEvidence{"train": {MetricName: "metric", Score: 1, Status: "passed"}})
	candidateTrain.Identity.ProfileHash = "candidate-profile-v2"
	baselineValidation := testSnapshot("validation", 0.8, map[string]metricEvidence{"validation": {MetricName: "metric", Score: 0.8, Status: "passed"}})
	candidateValidation := testSnapshot("validation", 0.2, map[string]metricEvidence{})
	candidateValidation.Identity.ProfileHash = candidateTrain.Identity.ProfileHash
	trainDelta, err := compareSnapshots(baselineTrain, candidateTrain)
	require.NoError(t, err)
	validationDelta, err := compareSnapshots(baselineValidation, candidateValidation)
	require.NoError(t, err)
	decision := evaluateGate(
		gateConfig{MinValidationGain: 0.1, MaxGeneralizationGap: 0.2, RequireCompleteMatrix: true},
		budgetConfig{MaxModelCalls: 10, MaxTotalTokens: 100, MaxLatencyMS: 1000},
		baselineTrain,
		candidateTrain,
		baselineValidation,
		candidateValidation,
		trainDelta,
		validationDelta,
		accountingSummary{ModelCalls: 1, CompletionTokens: 1, TotalTokens: 1, WallLatencyMS: 1},
	)
	require.False(t, decision.Accepted)
	require.Contains(t, decision.ReasonCodes, "OVERFIT_VALIDATION_REGRESSION")
	require.Contains(t, decision.ReasonCodes, "CANDIDATE_RESULT_INCOMPLETE")
}

func TestGateAcceptsCompleteImprovement(t *testing.T) {
	baselineTrain := testSnapshot("train", 0, map[string]metricEvidence{"train": {MetricName: "metric", Score: 0, Status: "failed"}})
	candidateTrain := testSnapshot("train", 1, map[string]metricEvidence{"train": {MetricName: "metric", Score: 1, Status: "passed"}})
	candidateTrain.Identity.ProfileHash = "candidate-profile-v2"
	baselineValidation := testSnapshot("validation", 0, map[string]metricEvidence{"validation": {MetricName: "metric", Score: 0, Status: "failed"}})
	candidateValidation := testSnapshot("validation", 1, map[string]metricEvidence{"validation": {MetricName: "metric", Score: 1, Status: "passed"}})
	candidateValidation.Identity.ProfileHash = candidateTrain.Identity.ProfileHash
	trainDelta, err := compareSnapshots(baselineTrain, candidateTrain)
	require.NoError(t, err)
	validationDelta, err := compareSnapshots(baselineValidation, candidateValidation)
	require.NoError(t, err)
	decision := evaluateGate(
		gateConfig{MinValidationGain: 0.5, MaxGeneralizationGap: 0.1, RequireCompleteMatrix: true},
		budgetConfig{MaxModelCalls: 10, MaxTotalTokens: 100, MaxLatencyMS: 1000},
		baselineTrain,
		candidateTrain,
		baselineValidation,
		candidateValidation,
		trainDelta,
		validationDelta,
		accountingSummary{ModelCalls: 1, CompletionTokens: 1, TotalTokens: 1, WallLatencyMS: 1},
	)
	require.True(t, decision.Accepted)
	require.Equal(t, []string{"ALL_CHECKS_PASSED"}, decision.ReasonCodes)
}

func TestGateFailsClosedOnForgedEvidence(t *testing.T) {
	baselineTrain := testSnapshot("train", 0, map[string]metricEvidence{"train": {MetricName: "metric", Score: 0, Status: "failed"}})
	candidateTrain := testSnapshot("train", 1, map[string]metricEvidence{"train": {MetricName: "metric", Score: 1, Status: "passed"}})
	baselineValidation := testSnapshot("validation", 0, map[string]metricEvidence{"validation": {MetricName: "metric", Score: 0, Status: "failed"}})
	candidateValidation := testSnapshot("validation", 1, map[string]metricEvidence{"validation": {MetricName: "metric", Score: 1, Status: "passed"}})
	candidateTrain.Identity.ProfileHash = "candidate-profile-v2"
	candidateValidation.Identity.ProfileHash = "candidate-profile-v2"
	trainDelta, err := compareSnapshots(baselineTrain, candidateTrain)
	require.NoError(t, err)
	validationDelta, err := compareSnapshots(baselineValidation, candidateValidation)
	require.NoError(t, err)
	validationDelta.OverallDelta = 99
	decision := evaluateGate(
		gateConfig{MinValidationGain: 0.5, MaxGeneralizationGap: 1},
		budgetConfig{MaxModelCalls: 10, MaxTotalTokens: 100, MaxLatencyMS: 1000},
		baselineTrain, candidateTrain, baselineValidation, candidateValidation,
		trainDelta, validationDelta, accountingSummary{ModelCalls: 1, CompletionTokens: 1, TotalTokens: 1, WallLatencyMS: 1},
	)
	require.False(t, decision.Accepted)
	require.Contains(t, decision.ReasonCodes, "DELTA_SNAPSHOT_MISMATCH")

	candidateValidation.OverallScore = 99
	validationDelta, err = compareSnapshots(baselineValidation, candidateValidation)
	require.NoError(t, err)
	decision = evaluateGate(
		gateConfig{MinValidationGain: 0.5, MaxGeneralizationGap: 1},
		budgetConfig{MaxModelCalls: 10, MaxTotalTokens: 100, MaxLatencyMS: 1000},
		baselineTrain, candidateTrain, baselineValidation, candidateValidation,
		trainDelta, validationDelta, accountingSummary{ModelCalls: 1, CompletionTokens: 1, TotalTokens: 1, WallLatencyMS: 1},
	)
	require.False(t, decision.Accepted)
	require.Contains(t, decision.ReasonCodes, "INCONSISTENT_SNAPSHOT_SCORE")
}

func TestFailureAttributionHonorsRootCausePriorityAndChineseReasons(t *testing.T) {
	routeWithWrongTool := caseEvidence{
		CaseID:  "route-and-tool",
		RunID:   1,
		Status:  "failed",
		Metrics: []metricEvidence{{MetricName: "metric", Status: "failed", Reason: "工具调用失败"}},
		Invocations: []invocationEvidence{{
			ActualTools:   []toolEvidence{{Name: "billing"}},
			ExpectedTools: []toolEvidence{{Name: "weather"}},
		}},
		Trace: traceEvidence{Steps: []traceStepEvidence{{Error: "wrong branch selected by router"}}},
	}
	category, _, _, _ := classifyFailure(routeWithWrongTool, routeWithWrongTool.Metrics[0])
	require.Equal(t, "route_error", category)

	format := caseEvidence{CaseID: "format-zh", Status: "failed"}
	category, _, _, _ = classifyFailure(format, metricEvidence{MetricName: "metric", Status: "failed", Reason: "结构化输出格式错误"})
	require.Equal(t, "output_format_error", category)

	execution := caseEvidence{CaseID: "execution-zh", Status: "failed", Trace: traceEvidence{Steps: []traceStepEvidence{{Error: "工具执行失败"}}}}
	category, _, _, _ = classifyFailure(execution, metricEvidence{MetricName: "metric", Status: "failed"})
	require.Equal(t, "execution_error", category)
}

func TestDatasetGuardUsesInputsNotExpectedAnswers(t *testing.T) {
	dir := t.TempDir()
	trainPath := filepath.Join(dir, "train.json")
	validationPath := filepath.Join(dir, "validation.json")
	train := `{"evalSetId":"train","evalCases":[{"evalId":"train-1","conversation":[{"userContent":{"role":"user","content":"same question"},"finalResponse":{"role":"assistant","content":"answer A"}}]}]}`
	validation := `{"evalSetId":"validation","evalCases":[{"evalId":"validation-1","conversation":[{"userContent":{"role":"user","content":"same question"},"finalResponse":{"role":"assistant","content":"answer B"}}]}]}`
	require.NoError(t, os.WriteFile(trainPath, []byte(train), 0o600))
	require.NoError(t, os.WriteFile(validationPath, []byte(validation), 0o600))
	overlaps, err := validateDatasetIsolation(trainPath, validationPath, datasetGuardConfig{FailOnExactOverlap: true, NearThreshold: 0.9})
	require.Error(t, err)
	require.Contains(t, overlaps, "exact:train-1~validation-1")
}

func TestRedactReport(t *testing.T) {
	input := []byte(`{"x-api-key":"SECRET_CANARY_DO_NOT_PERSIST","note":"Bearer abc.def"}`)
	redacted := redactReport(input)
	require.NotContains(t, string(redacted), "SECRET_CANARY_DO_NOT_PERSIST")
	require.NotContains(t, string(redacted), "abc.def")
	require.Contains(t, string(redacted), "[REDACTED]")
	require.True(t, containsPromptSecret([]byte("api_key=secret-value")))
}

func TestNearDuplicateScoreAndDatasetGuardThreshold(t *testing.T) {
	near := textNearDuplicateScore(
		"case:train-weather Shanghai weather answer",
		"case:validation-weather Shanghai weather answer",
	)
	far := textNearDuplicateScore(
		"distance between Beijing and Tianjin",
		"emergency service hours for a city",
	)
	require.Greater(t, near, 0.75)
	require.Less(t, far, near)
}

func TestRenderMarkdownContainsDecisionAndPerMetricDelta(t *testing.T) {
	baseline := testSnapshot("validation", 0, map[string]metricEvidence{
		"case-a": {MetricName: "metric", Score: 0, Status: "failed"},
	})
	candidate := testSnapshot("validation", 1, map[string]metricEvidence{
		"case-a": {MetricName: "metric", Score: 1, Status: "passed"},
	})
	delta, err := compareSnapshots(baseline, candidate)
	require.NoError(t, err)
	report := &optimizationReport{
		Pipeline: pipelineMetadata{Scenario: scenarioImprovement},
		Baseline: baselineReport{Train: baseline, Validation: baseline, AttributionSummary: map[string]int{"final_response_mismatch": 1}},
		Rounds: []roundReport{{
			Round:               1,
			CandidateTrain:      candidate,
			CandidateValidation: candidate,
			TrainDelta:          delta,
			ValidationDelta:     delta,
			GateDecision:        gateDecision{Accepted: true},
		}},
		FinalDecision: gateDecision{Accepted: true, ReasonCodes: []string{"ALL_CHECKS_PASSED"}},
	}
	markdown := renderMarkdown(report)
	require.True(t, strings.Contains(markdown, "接受候选 Prompt"))
	require.True(t, strings.Contains(markdown, "case-a"))
	require.True(t, strings.Contains(markdown, "newly_passed"))
}

func TestPipelineDeterministicScenarios(t *testing.T) {
	for _, test := range []struct {
		name       string
		scenario   string
		accepted   bool
		reasonCode string
		maxRounds  int
		wantRounds int
	}{
		{name: "improvement", scenario: scenarioImprovement, accepted: true, reasonCode: "ALL_CHECKS_PASSED", maxRounds: 1, wantRounds: 1},
		{name: "noop", scenario: scenarioNoop, accepted: false, reasonCode: "VALIDATION_GAIN_BELOW_THRESHOLD", maxRounds: 1, wantRounds: 1},
		{name: "overfit", scenario: scenarioOverfit, accepted: false, reasonCode: "OVERFIT_VALIDATION_REGRESSION", maxRounds: 1, wantRounds: 1},
		{name: "multi round", scenario: scenarioMultiRound, accepted: true, reasonCode: "ALL_CHECKS_PASSED", maxRounds: 2, wantRounds: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("OPENAI_API_KEY", "")
			t.Setenv("ANTHROPIC_API_KEY", "")
			t.Setenv("GOOGLE_API_KEY", "")
			cfg, err := loadConfig(filepath.Join("data", "promptiter.json"))
			require.NoError(t, err)
			cfg.Scenario = test.scenario
			cfg.MaxRounds = test.maxRounds
			cfg.OutputDir = t.TempDir()
			report, err := runPipeline(t.Context(), cfg)
			require.NoError(t, err)
			require.Equal(t, test.accepted, report.FinalDecision.Accepted)
			require.True(t, slices.Contains(report.FinalDecision.ReasonCodes, test.reasonCode))
			require.Len(t, report.Rounds, test.wantRounds)
			if test.scenario == scenarioImprovement {
				splits := map[string]bool{}
				for _, item := range report.Baseline.Attributions {
					splits[item.Snapshot.Split] = true
				}
				require.True(t, splits["train"])
				require.True(t, splits["validation"])
			}
			if test.scenario == scenarioMultiRound {
				require.False(t, report.Rounds[0].GateDecision.Accepted)
				require.True(t, report.Rounds[1].GateDecision.Accepted)
				require.Equal(t, report.Rounds[0].ParentProfileHash, report.Rounds[1].ParentProfileHash,
					"a rejected candidate must not become the next round parent")
				require.NotEqual(t, report.Rounds[0].CandidateProfileHash, report.Rounds[1].CandidateProfileHash)
			}
			require.FileExists(t, cfg.reportJSONPath())
			require.FileExists(t, cfg.reportMarkdownPath())
			_, acceptedErr := os.Stat(filepath.Join(cfg.OutputDir, "accepted_prompt.txt"))
			if test.accepted {
				require.NoError(t, acceptedErr)
			} else {
				require.ErrorIs(t, acceptedErr, os.ErrNotExist)
			}
			require.FileExists(t, filepath.Join(cfg.OutputDir, "round-001-candidate_prompt.txt"))
			stages := make([]string, 0, len(report.Accounting.ByStage))
			for _, record := range report.Accounting.ByStage {
				stages = append(stages, record.Stage)
			}
			require.Contains(t, stages, "promptiter.backward")
			require.Contains(t, stages, "promptiter.aggregate")
			require.Contains(t, stages, "promptiter.optimize")
			require.Less(t, report.Pipeline.DurationMS, int64(180000))
		})
	}
}

func TestPipelineSemanticDeterminismAcrossRuns(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	run := func() *optimizationReport {
		cfg, err := loadConfig(filepath.Join("data", "promptiter.json"))
		require.NoError(t, err)
		cfg.OutputDir = t.TempDir()
		report, err := runPipeline(t.Context(), cfg)
		require.NoError(t, err)
		return report
	}
	first, second := run(), run()
	require.Equal(t, first.FinalDecision.Accepted, second.FinalDecision.Accepted)
	require.Equal(t, first.FinalDecision.ReasonCodes, second.FinalDecision.ReasonCodes)
	require.Equal(t, first.Baseline.Train.OverallScore, second.Baseline.Train.OverallScore)
	require.Equal(t, first.Baseline.Validation.OverallScore, second.Baseline.Validation.OverallScore)
	require.Equal(t, first.Rounds[0].CandidatePrompt, second.Rounds[0].CandidatePrompt)
	require.Equal(t, first.Rounds[0].ValidationDelta, second.Rounds[0].ValidationDelta)
	require.Equal(t, first.Accounting.ModelCalls, second.Accounting.ModelCalls)
	require.Equal(t, first.Accounting.TotalTokens, second.Accounting.TotalTokens)
}

func testSnapshot(split string, score float64, metrics map[string]metricEvidence) *evaluationSnapshot {
	snapshot := &evaluationSnapshot{
		Identity: snapshotIdentity{
			EvaluationRunID: fmt.Sprintf("run-%s-%.6f-%d", split, score, len(metrics)),
			Split:           split,
			EvalSetID:       "set-" + split,
			DatasetHash:     "dataset-" + split,
			MetricsHash:     "metrics",
			ProfileHash:     "candidate-profile",
		},
		OverallScore: score,
	}
	for caseID, metric := range metrics {
		snapshot.Cases = append(snapshot.Cases, caseEvidence{CaseID: caseID, Metrics: []metricEvidence{metric}})
	}
	return snapshot
}
