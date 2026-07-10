//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/backwarder"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestFakePipelineRunsPromptIterEndToEnd(t *testing.T) {
	result := runTestPipeline(t, pipelineConfig{})
	report := result.Report
	require.NotNil(t, report)
	require.FileExists(t, result.ReportJSONPath)
	require.FileExists(t, result.ReportMarkdownPath)
	require.Len(t, report.Rounds, 2)
	require.True(t, report.Rounds[0].Accepted)
	require.True(t, report.Rounds[1].Accepted)
	require.True(t, report.Candidate.Accepted)
	require.Greater(t, scoreOf(report.Candidate.Validation), scoreOf(report.Baseline.Validation))
	require.NotNil(t, report.Candidate.Train)
	require.Greater(t, scoreOf(report.Candidate.Train), scoreOf(report.Baseline.Train))
	require.False(t, report.Gate.Publishable)
	require.Equal(t, "reject", report.Gate.Decision)
	require.NotContains(t, report.Phase1Pending, "final_gate")
	require.NotContains(t, report.Phase1Pending, "validation_delta")
	require.NotContains(t, report.Phase1Pending, "candidate_train_regression")
	require.NotContains(t, report.Phase1Pending, "failure_attribution")
	require.NotContains(t, report.Phase1Pending, "trace_smoke")
	require.NotContains(t, report.Phase1Pending, "design_doc")
	require.False(t, report.TraceSmoke.Enabled)
}

func TestBaselinePromptIsActuallyReadAndHashed(t *testing.T) {
	tmp := t.TempDir()
	promptPath := filepath.Join(tmp, "baseline_prompt.txt")
	promptText := "Unique fake regression prompt: use deterministic behavior only."
	require.NoError(t, os.WriteFile(promptPath, []byte(promptText), 0o644))
	result := runTestPipeline(t, pipelineConfig{PromptPath: promptPath})
	sum := sha256.Sum256([]byte(promptText))
	require.Equal(t, hex.EncodeToString(sum[:]), result.Report.PromptHash)
	require.Equal(t, filepath.ToSlash(filepath.Clean(promptPath)), result.Report.PromptSource)
	var sawPrompt bool
	for _, system := range result.Model.ObservedSystemMessages() {
		if strings.Contains(system, promptText) {
			sawPrompt = true
			break
		}
	}
	require.True(t, sawPrompt, "fake model should receive the prompt through llmagent instruction processing")
}

func TestPromptIterPatchReachesModelToolDeclaration(t *testing.T) {
	result := runTestPipeline(t, pipelineConfig{})
	descriptions := result.Model.ObservedToolDescriptions()
	require.Contains(t, descriptions, initialLookupDescription)
	require.Contains(t, descriptions, partialLookupDescription)
	require.Contains(t, descriptions, overfitLookupDescription)
	require.Equal(t, initialLookupDescription, result.Report.BaselineToolDescription)
	require.Equal(t, partialLookupDescription, result.Report.Rounds[0].Patches[0].ToolDescription)
	require.Equal(t, overfitLookupDescription, result.Report.Rounds[1].Patches[0].ToolDescription)
}

func TestBackwarderOnlyReturnsGradientForFailedCases(t *testing.T) {
	target := "candidate#tool.lookup_record"
	worker := &fakeBackwarder{targetSurfaceID: target}
	empty, err := worker.Backward(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, empty.Gradients)
	noAllowed, err := worker.Backward(context.Background(), &backwarder.Request{})
	require.NoError(t, err)
	require.Empty(t, noAllowed.Gradients)
	withAllowed, err := worker.Backward(context.Background(), &backwarder.Request{
		AllowedGradientSurfaceIDs: []string{target},
	})
	require.NoError(t, err)
	require.Len(t, withAllowed.Gradients, 1)
	require.Equal(t, target, withAllowed.Gradients[0].SurfaceID)
}

func TestDeterministicMetricsWithNilJudge(t *testing.T) {
	result := runTestPipeline(t, pipelineConfig{})
	require.True(t, result.Report.Candidate.Accepted)
	lookupCase := findCase(t, result.Report.Candidate.Validation, "flight_tr321_delay")
	require.True(t, lookupCase.Passed)
	require.ElementsMatch(t, []string{"final_response_avg_score", "tool_trajectory_avg_score"}, metricNames(lookupCase))
	require.True(t, result.Model.SawToolResult(), "candidate path should call the lookup tool and receive a tool result")
}

func TestCriticalNoToolCaseRegressesAndFinalGateRejects(t *testing.T) {
	result := runTestPipeline(t, pipelineConfig{})
	trainCase := findCase(t, result.Report.Baseline.Train, "flight_direct_no_tool")
	require.True(t, trainCase.Passed)
	require.Equal(t, 1.0, trainCase.Score)
	baselineValidationCase := findCase(t, result.Report.Baseline.Validation, "flight_tr789_cancelled_no_tool")
	candidateValidationCase := findCase(t, result.Report.Candidate.Validation, "flight_tr789_cancelled_no_tool")
	require.True(t, baselineValidationCase.Passed)
	require.False(t, candidateValidationCase.Passed)
	require.Equal(t, 1.0, baselineValidationCase.Score)
	require.Equal(t, 0.0, candidateValidationCase.Score)
	require.Equal(t, "reject", result.Report.Gate.Decision)
	reasons := strings.Join(result.Report.Gate.Reasons, "\n")
	require.Contains(t, reasons, "new hard fail")
	require.Contains(t, reasons, "critical regression")
	require.Equal(t, 1, result.Report.Delta.Summary.NewHardFail)
	require.Equal(t, 1, result.Report.Delta.Summary.CriticalRegression)
	require.Contains(t, attributionCaseIDs(result.Report.Attribution), "flight_tr789_cancelled_no_tool")
}

func TestValidationScoreImprovesAfterAcceptedPatch(t *testing.T) {
	result := runTestPipeline(t, pipelineConfig{})
	require.Less(t, scoreOf(result.Report.Baseline.Validation), scoreOf(result.Report.Candidate.Validation))
	require.Greater(t, result.Report.Rounds[0].ScoreDelta, 0.1)
	require.Greater(t, result.Report.Rounds[1].ScoreDelta, 0.1)
	require.InDelta(t, 0.25, scoreOf(result.Report.Baseline.Validation), 0.0001)
	require.InDelta(t, 0.50, scoreOf(result.Report.Rounds[0].Validation), 0.0001)
	require.InDelta(t, 0.75, scoreOf(result.Report.Rounds[1].Validation), 0.0001)
}

func TestReportSchemaMatchesFullPlanNames(t *testing.T) {
	result := runTestPipeline(t, pipelineConfig{})
	raw, err := os.ReadFile(result.ReportJSONPath)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.Contains(t, decoded, "baseline")
	require.Contains(t, decoded, "candidate")
	candidate := decoded["candidate"].(map[string]any)
	require.Contains(t, candidate, "validation")
	require.Contains(t, candidate, "acceptedProfile")
	require.Contains(t, candidate, "train")
	require.NotNil(t, candidate["train"])
	require.Contains(t, decoded, "delta")
	require.Contains(t, decoded, "attribution")
	require.Contains(t, decoded, "gate")
	require.Contains(t, decoded, "traceSmoke")
	require.NotContains(t, decoded, "decision")
	rounds := decoded["rounds"].([]any)
	firstRound := rounds[0].(map[string]any)
	require.Contains(t, firstRound, "train")
	require.Contains(t, firstRound, "validation")
	require.NotContains(t, firstRound, "trainScore")
	require.NotContains(t, firstRound, "validationScore")
}

func TestReportWritesToConfiguredOutputDir(t *testing.T) {
	outputDir := t.TempDir()
	result := runTestPipeline(t, pipelineConfig{OutputDir: outputDir})
	require.Equal(t, filepath.Join(outputDir, reportJSONName), result.ReportJSONPath)
	require.Equal(t, filepath.Join(outputDir, reportMarkdownName), result.ReportMarkdownPath)
	require.FileExists(t, filepath.Join(outputDir, reportJSONName))
	require.FileExists(t, filepath.Join(outputDir, reportMarkdownName))
}

func TestValidationDeltaClassifiesAllStates(t *testing.T) {
	delta := buildValidationDelta(
		validationSummary(
			summaryCase("new_pass", 0.0, false),
			summaryCase("new_fail", 1.0, true),
			summaryCase("improved", 0.2, false),
			summaryCase("regressed", 0.6, false),
			summaryCase("unchanged_pass", 1.0, true),
			summaryCase("unchanged_fail", 0.0, false),
		),
		validationSummary(
			summaryCase("new_pass", 1.0, true),
			summaryCase("new_fail", 0.0, false),
			summaryCase("improved", 0.6, false),
			summaryCase("regressed", 0.2, false),
			summaryCase("unchanged_pass", 1.0, true),
			summaryCase("unchanged_fail", 0.0, false),
		),
		nil,
	)
	require.Equal(t, 1, delta.Summary.NewPass)
	require.Equal(t, 1, delta.Summary.NewFail)
	require.Equal(t, 1, delta.Summary.Improved)
	require.Equal(t, 1, delta.Summary.Regressed)
	require.Equal(t, 1, delta.Summary.UnchangedPass)
	require.Equal(t, 1, delta.Summary.UnchangedFail)
	require.Equal(t, 1, delta.Summary.NewHardFail)
	classifications := map[string]string{}
	for _, caseDelta := range delta.PerCase {
		classifications[caseDelta.EvalCaseID] = caseDelta.Classification
	}
	require.Equal(t, deltaNewPass, classifications["new_pass"])
	require.Equal(t, deltaNewFail, classifications["new_fail"])
	require.Equal(t, deltaImproved, classifications["improved"])
	require.Equal(t, deltaRegressed, classifications["regressed"])
	require.Equal(t, deltaUnchangedPass, classifications["unchanged_pass"])
	require.Equal(t, deltaUnchangedFail, classifications["unchanged_fail"])
}

func TestFailureAttributionClassifiesAllCategories(t *testing.T) {
	report := buildFailureAttribution(&promptiterengine.EvaluationResult{
		EvalSets: []promptiterengine.EvalSetResult{
			{
				EvalSetID: "validation",
				Cases: []promptiterengine.CaseResult{
					attributionCase(
						"tool_not_called",
						invocationWithTools(nil, ""),
						invocationWithTools([]*evalset.Tool{lookupTool(map[string]string{"query": "TR321"})}, ""),
						"tool_trajectory_avg_score",
						"tool trajectory mismatch: number of tool calls mismatch: actual(0) < expected(1)",
						nil,
					),
					attributionCase(
						"wrong_tool_name",
						invocationWithTools([]*evalset.Tool{{Name: "search_record"}}, ""),
						invocationWithTools([]*evalset.Tool{lookupTool(map[string]string{"query": "TR321"})}, ""),
						"tool_trajectory_avg_score",
						"tool trajectory mismatch: name mismatch",
						nil,
					),
					attributionCase(
						"tool_arguments_mismatch",
						invocationWithTools([]*evalset.Tool{lookupTool(map[string]string{"query": "TR000"})}, ""),
						invocationWithTools([]*evalset.Tool{lookupTool(map[string]string{"query": "TR321"})}, ""),
						"tool_trajectory_avg_score",
						"tool trajectory mismatch: arguments mismatch",
						nil,
					),
					attributionCase(
						"final_response_mismatch",
						invocationWithTools(nil, "wrong answer"),
						invocationWithTools(nil, "right answer"),
						"final_response_avg_score",
						"final response mismatch: text mismatch",
						nil,
					),
					attributionCase(
						"route_error",
						nil,
						nil,
						"quality",
						"runner route error",
						&atrace.Trace{
							Status: atrace.TraceStatusFailed,
							Steps: []atrace.Step{
								{StepID: "route_step", Error: "route unavailable"},
							},
						},
					),
					attributionCase(
						"format_error",
						nil,
						nil,
						"quality",
						"json mismatch: invalid format",
						nil,
					),
					attributionCase(
						"knowledge_insufficient",
						nil,
						nil,
						"quality",
						"model says it does not have enough information",
						nil,
					),
					attributionCase(
						"metric_failure",
						nil,
						nil,
						"quality",
						"quality score below threshold",
						nil,
					),
				},
			},
		},
	})
	require.Len(t, report.PerFailedCase, len(attributionCategories))
	for _, category := range attributionCategories {
		require.Equal(t, 1, report.Summary[category], category)
	}
	byCase := indexAttributionCases(report)
	for _, category := range attributionCategories {
		require.Equal(t, category, byCase[category].Category)
	}
	require.Equal(t, "quality score below threshold", byCase["metric_failure"].FailedMetrics[0].Reason)
	require.Equal(t, "route_step", byCase["route_error"].TerminalStepID)
}

func TestFinalGateRules(t *testing.T) {
	cfg := finalGateConfig{
		MinValidationGain:          0.05,
		MaxDurationMs:              100,
		RejectOnNewHardFail:        true,
		RejectOnCriticalRegression: true,
	}
	baseDelta := ValidationDelta{
		Summary: DeltaSummary{
			ScoreDelta: 0.10,
		},
	}
	gate := decideFinalGate(defaultMode, cfg, baseDelta, 10*time.Millisecond, 0)
	require.True(t, gate.Publishable)
	require.Equal(t, "publish", gate.Decision)

	gate = decideFinalGate(defaultMode, cfg, ValidationDelta{Summary: DeltaSummary{ScoreDelta: 0.01}}, 10*time.Millisecond, 0)
	require.False(t, gate.Publishable)
	require.Contains(t, strings.Join(gate.Reasons, "\n"), "below minimum")

	withHardFail := baseDelta
	withHardFail.Summary.NewHardFail = 1
	gate = decideFinalGate(defaultMode, cfg, withHardFail, 10*time.Millisecond, 0)
	require.False(t, gate.Publishable)
	require.Contains(t, strings.Join(gate.Reasons, "\n"), "new hard fail")

	withCriticalRegression := baseDelta
	withCriticalRegression.Summary.CriticalRegression = 1
	gate = decideFinalGate(defaultMode, cfg, withCriticalRegression, 10*time.Millisecond, 0)
	require.False(t, gate.Publishable)
	require.Contains(t, strings.Join(gate.Reasons, "\n"), "critical regression")

	gate = decideFinalGate(defaultMode, cfg, baseDelta, 100*time.Millisecond, 0)
	require.False(t, gate.Publishable)
	require.Contains(t, strings.Join(gate.Reasons, "\n"), "exceeds max")
}

func TestCandidateTrainUsesAcceptedProfileNotLastRound(t *testing.T) {
	acceptedProfile := &promptiter.Profile{StructureID: "accepted"}
	rejectedProfile := &promptiter.Profile{StructureID: "rejected"}
	report, err := buildOptimizationReport(reportInput{
		mode:                    defaultMode,
		seed:                    defaultSeed,
		prompt:                  promptSource{Path: "config/baseline_prompt.txt", Hash: "hash", Summary: "summary"},
		targetSurfaceIDs:        []string{"candidate#tool.lookup_record"},
		baselineToolDescription: initialLookupDescription,
		runResult: &promptiterengine.RunResult{
			BaselineValidation: evaluationResult("validation", "flight_tr789_cancelled_no_tool", 0.25, false),
			AcceptedProfile:    acceptedProfile,
			Rounds: []promptiterengine.RoundResult{
				{
					Round:         1,
					Train:         evaluationResult("train", "round_1_train", 0.25, false),
					Validation:    evaluationResult("validation", "round_1_validation", 0.75, true),
					OutputProfile: acceptedProfile,
					Acceptance: &promptiterengine.AcceptanceDecision{
						Accepted:   true,
						ScoreDelta: 0.5,
						Reason:     "accepted",
					},
				},
				{
					Round:         2,
					Train:         evaluationResult("train", "round_2_train", 0.10, false),
					Validation:    evaluationResult("validation", "round_2_validation", 0.20, false),
					OutputProfile: rejectedProfile,
					Acceptance: &promptiterengine.AcceptanceDecision{
						Accepted:   false,
						ScoreDelta: -0.55,
						Reason:     "rejected",
					},
				},
			},
		},
		candidateTrain: evaluationResult("train", "candidate_train", 0.90, true),
		finalGate: finalGateConfig{
			MinValidationGain:          0.05,
			MaxDurationMs:              180000,
			CriticalCaseIDs:            []string{"TR789"},
			RejectOnNewHardFail:        true,
			RejectOnCriticalRegression: true,
		},
		latency: 10 * time.Millisecond,
	})
	require.NoError(t, err)
	require.Equal(t, 0.90, report.Candidate.Train.Score)
	require.Equal(t, 0.75, report.Candidate.Validation.Score)
	require.Same(t, acceptedProfile, report.Candidate.AcceptedProfile)
	require.NotSame(t, rejectedProfile, report.Candidate.AcceptedProfile)
}

func TestTraceSmokeModeEvaluatesTraceSetAndSkipsOptimization(t *testing.T) {
	result := runTestPipeline(t, pipelineConfig{Mode: traceSmokeMode})
	report := result.Report
	require.Equal(t, traceSmokeMode, report.Mode)
	require.True(t, report.TraceSmoke.Enabled)
	require.Equal(t, traceSmokeEvalSetID, report.TraceSmoke.EvalSetID)
	require.True(t, report.TraceSmoke.OptimizationSkipped)
	require.Equal(t, traceSmokeOptimizationSkippedReason, report.TraceSmoke.OptimizationSkippedReason)
	require.NotNil(t, report.TraceSmoke.Evaluation)
	require.InDelta(t, 0.5, report.TraceSmoke.Evaluation.Score, 0.0001)
	require.Empty(t, report.Rounds)
	require.False(t, report.Gate.Publishable)
	require.Equal(t, "skipped", report.Gate.Decision)
	require.Equal(t, 0, report.Cost.ModelCallCount)
	require.Equal(t, 0, report.Cost.WorkerCallCount)
	require.Equal(t, 0, result.Model.CallCount())
	require.NotNil(t, report.TraceSmoke.Attribution)
	require.Contains(t, attributionCaseIDs(*report.TraceSmoke.Attribution), "trace_flight_tr654_gate_tool_missing")
	failure := indexAttributionCases(*report.TraceSmoke.Attribution)["trace_flight_tr654_gate_tool_missing"]
	require.Equal(t, attributionToolNotCalled, failure.Category)
	require.Equal(t, "trace-tr654-step-1", failure.TerminalStepID)
	require.NotEmpty(t, failure.FailedMetrics[0].Reason)
}

func TestUnsupportedModeReturnsExplicitError(t *testing.T) {
	_, err := runFakePipeline(context.Background(), normalizeTestConfig(pipelineConfig{Mode: "unknown"}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "supported modes")
}

func runTestPipeline(t *testing.T, cfg pipelineConfig) *pipelineResult {
	t.Helper()
	if cfg.OutputDir == "" {
		cfg.OutputDir = t.TempDir()
	}
	result, err := runFakePipeline(context.Background(), normalizeTestConfig(cfg))
	require.NoError(t, err)
	return result
}

func normalizeTestConfig(cfg pipelineConfig) pipelineConfig {
	if cfg.Mode == "" {
		cfg.Mode = defaultMode
	}
	if cfg.DataDir == "" {
		cfg.DataDir = "./data"
	}
	if cfg.OutputDir == "" {
		cfg.OutputDir = filepath.Join(os.TempDir(), "promptiter-regression-loop-test-output")
	}
	if cfg.PromptPath == "" {
		cfg.PromptPath = "./config/baseline_prompt.txt"
	}
	if cfg.ConfigPath == "" {
		cfg.ConfigPath = "./config/promptiter.json"
	}
	if cfg.Seed == 0 {
		cfg.Seed = defaultSeed
	}
	return cfg
}

func findCase(t *testing.T, summary *EvaluationSummary, evalCaseID string) EvalCaseSummary {
	t.Helper()
	require.NotNil(t, summary)
	for _, evalSet := range summary.EvalSets {
		for _, evalCase := range evalSet.Cases {
			if evalCase.EvalCaseID == evalCaseID {
				return evalCase
			}
		}
	}
	t.Fatalf("case %s not found", evalCaseID)
	return EvalCaseSummary{}
}

func metricNames(evalCase EvalCaseSummary) []string {
	names := make([]string, 0, len(evalCase.Metrics))
	for _, metric := range evalCase.Metrics {
		names = append(names, metric.MetricName)
	}
	return names
}

func attributionCaseIDs(report AttributionReport) []string {
	ids := make([]string, 0, len(report.PerFailedCase))
	for _, failure := range report.PerFailedCase {
		ids = append(ids, failure.EvalCaseID)
	}
	return ids
}

func indexAttributionCases(report AttributionReport) map[string]FailureAttribution {
	out := make(map[string]FailureAttribution, len(report.PerFailedCase))
	for _, failure := range report.PerFailedCase {
		out[failure.EvalCaseID] = failure
	}
	return out
}

func attributionCase(
	evalCaseID string,
	actual *evalset.Invocation,
	expected *evalset.Invocation,
	metricName string,
	reason string,
	trace *atrace.Trace,
) promptiterengine.CaseResult {
	return promptiterengine.CaseResult{
		EvalSetID:          "validation",
		EvalCaseID:         evalCaseID,
		ActualInvocation:   actual,
		ExpectedInvocation: expected,
		Trace:              trace,
		Metrics: []promptiterengine.MetricResult{
			{
				MetricName: metricName,
				Score:      0,
				Status:     status.EvalStatusFailed,
				Reason:     reason,
			},
		},
	}
}

func invocationWithTools(tools []*evalset.Tool, finalResponse string) *evalset.Invocation {
	invocation := &evalset.Invocation{
		InvocationID: "invocation",
		Tools:        tools,
	}
	if finalResponse != "" {
		invocation.FinalResponse = &model.Message{
			Role:    model.RoleAssistant,
			Content: finalResponse,
		}
	}
	return invocation
}

func lookupTool(arguments any) *evalset.Tool {
	return &evalset.Tool{
		Name:      "lookup_record",
		Arguments: arguments,
	}
}

func validationSummary(cases ...EvalCaseSummary) *EvaluationSummary {
	total := 0.0
	for _, evalCase := range cases {
		total += evalCase.Score
	}
	score := 0.0
	if len(cases) > 0 {
		score = total / float64(len(cases))
	}
	return &EvaluationSummary{
		Score: score,
		EvalSets: []EvalSetSummary{
			{
				EvalSetID: "validation",
				Score:     score,
				Cases:     cases,
			},
		},
	}
}

func summaryCase(evalCaseID string, score float64, passed bool) EvalCaseSummary {
	return EvalCaseSummary{
		EvalCaseID: evalCaseID,
		Score:      score,
		Passed:     passed,
	}
}

func evaluationResult(
	evalSetID string,
	evalCaseID string,
	score float64,
	passed bool,
) *promptiterengine.EvaluationResult {
	evalStatus := status.EvalStatusPassed
	reason := ""
	if !passed {
		evalStatus = status.EvalStatusFailed
		reason = "synthetic failure"
	}
	return &promptiterengine.EvaluationResult{
		OverallScore: score,
		EvalSets: []promptiterengine.EvalSetResult{
			{
				EvalSetID:    evalSetID,
				OverallScore: score,
				Cases: []promptiterengine.CaseResult{
					{
						EvalSetID:  evalSetID,
						EvalCaseID: evalCaseID,
						Metrics: []promptiterengine.MetricResult{
							{
								MetricName: "quality",
								Score:      score,
								Status:     evalStatus,
								Reason:     reason,
							},
						},
					},
				},
			},
		},
	}
}
