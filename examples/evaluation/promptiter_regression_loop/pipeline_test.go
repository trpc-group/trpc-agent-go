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
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/backwarder"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/optimizer"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestRunFakePipelineEndToEnd(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	outputDir := t.TempDir()
	result, err := runFakePipeline(context.Background(), RunConfig{
		Mode:       fakeMode,
		DataDir:    "./data",
		OutputDir:  outputDir,
		PromptPath: "./config/baseline_prompt.txt",
		ConfigPath: "./config/promptiter.json",
	})
	require.NoError(t, err)
	require.NotNil(t, result.Run)
	require.Equal(t, phaseVersion, result.Report.Phase)
	require.Empty(t, result.Report.Pending)
	require.Equal(t, deterministicSeed, result.Report.Seed)
	require.NotEmpty(t, result.Report.ConfigPath)
	require.NotEmpty(t, result.Report.ConfigSHA256)
	require.Equal(t, fakeModelConfigSummary(), result.Report.ModelConfig)
	require.Equal(t, promptIterConfigSummary(promptIterFileConfig{
		MaxRounds: 2,
		AcceptancePolicy: &acceptancePolicyFileConfig{
			MinScoreGain: floatPtr(0.1),
		},
		StopPolicy: &stopPolicyFileConfig{
			TargetScore: floatPtr(1),
		},
	}), result.Report.PromptIterConfig)
	require.False(t, result.Report.TraceSmoke.Enabled)
	require.Len(t, result.Run.Rounds, 2)
	require.NotNil(t, result.Run.Rounds[0].Acceptance)
	require.True(t, result.Run.Rounds[0].Acceptance.Accepted)
	require.NotNil(t, result.Run.Rounds[1].Acceptance)
	require.True(t, result.Run.Rounds[1].Acceptance.Accepted)
	require.Greater(t, result.Report.Candidate.Validation.OverallScore, result.Report.Baseline.Validation.OverallScore)
	require.Equal(t, 0.25, result.Report.Baseline.Validation.OverallScore)
	require.Equal(t, 0.75, result.Report.Candidate.Validation.OverallScore)
	require.NotNil(t, result.Report.Candidate.Train)
	require.Greater(t, result.Report.Candidate.Train.OverallScore, result.Report.Baseline.Train.OverallScore)
	require.Equal(t, 1.0, result.Report.Candidate.Train.OverallScore)
	require.NotNil(t, result.Report.Candidate.AcceptedProfile)
	require.Len(t, result.Report.Candidate.AcceptedProfile.Overrides, 1)
	require.Equal(t, round2ToolDescription, result.Report.Candidate.AcceptedProfile.Overrides[0].Value.Tools[0].Description)
	require.NotNil(t, result.Report.Rounds[0].OutputProfile)
	require.Equal(t, round1ToolDescription, result.Report.Rounds[0].OutputProfile.Overrides[0].Value.Tools[0].Description)
	require.Equal(t, round1ToolDescription, result.Report.Rounds[0].Patches[0].Value.Tools[0].Description)
	require.NotNil(t, result.Report.Rounds[1].OutputProfile)
	require.Equal(t, round2ToolDescription, result.Report.Rounds[1].OutputProfile.Overrides[0].Value.Tools[0].Description)
	require.Equal(t, round2ToolDescription, result.Report.Rounds[1].Patches[0].Value.Tools[0].Description)
	require.Equal(t, gateDecisionReject, result.Report.Gate.Decision)
	require.Contains(t, result.Report.Gate.NewHardFails, "validation_status_tr789")
	require.Contains(t, result.Report.Gate.CriticalRegressions, "validation_status_tr789")
	require.Contains(t, result.Report.Gate.CriticalCaseIDs, "validation_status_tr789")
	require.Equal(t, 100, result.Report.Gate.MaxModelCalls)
	require.Equal(t, result.Report.ModelCallCount, result.Report.Gate.ModelCallCount)
	require.NotNil(t, result.Report.Attribution)
	require.NotNil(t, result.Report.Attribution.BaselineTrain)
	require.NotNil(t, result.Report.Attribution.BaselineValidation)
	require.NotNil(t, result.Report.Attribution.CandidateValidation)
	require.Positive(t, result.Report.Attribution.BaselineTrain.Summary.ToolNotCalled)
	require.Positive(t, result.Report.Attribution.BaselineValidation.Summary.ToolNotCalled)
	require.True(t, slices.ContainsFunc(result.Report.Attribution.CandidateValidation.PerFailedCase, func(item FailedCaseAttribution) bool {
		return item.EvalCaseID == "validation_status_tr789" && item.Category == attributionRouteError
	}))
	require.Equal(t, 0.0, result.Report.Cost.TotalUSD)
	require.Positive(t, result.Report.ModelCallCount)
	require.FileExists(t, result.ReportJSONPath)
	require.FileExists(t, result.ReportMarkdownPath)
	require.Contains(t, result.ModelObservations.ToolDescriptions, initialToolDescription)
	require.Contains(t, result.ModelObservations.ToolDescriptions, round1ToolDescription)
	require.Contains(t, result.ModelObservations.ToolDescriptions, round2ToolDescription)
}

func TestRunTraceSmokePipelineEndToEnd(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	result, err := runTraceSmokePipeline(context.Background(), RunConfig{
		Mode:         traceSmokeMode,
		DataDir:      "./data",
		OutputDir:    t.TempDir(),
		SampleReport: true,
	})
	require.NoError(t, err)
	require.Nil(t, result.Run)
	require.Equal(t, phaseVersion, result.Report.Phase)
	require.Equal(t, traceSmokeMode, result.Report.Mode)
	require.Equal(t, deterministicSeed, result.Report.Seed)
	require.Equal(t, fakeModelConfigSummary(), result.Report.ModelConfig)
	require.Empty(t, result.Report.ConfigPath)
	require.Empty(t, result.Report.ConfigSHA256)
	require.Nil(t, result.Report.PromptIterConfig)
	require.Empty(t, result.Report.Rounds)
	require.Nil(t, result.Report.Delta)
	require.Nil(t, result.Report.Gate)
	require.True(t, result.Report.TraceSmoke.Enabled)
	require.True(t, result.Report.TraceSmoke.OptimizationSkipped)
	require.Equal(t, traceSmokeSkipReason, result.Report.TraceSmoke.OptimizationSkippedReason)
	require.NotNil(t, result.Report.TraceSmoke.Evaluation)
	require.Equal(t, 0.75, result.Report.TraceSmoke.Evaluation.OverallScore)
	require.NotNil(t, result.Report.TraceSmoke.Attribution)
	require.True(t, slices.ContainsFunc(result.Report.TraceSmoke.Attribution.PerFailedCase, func(item FailedCaseAttribution) bool {
		return item.EvalCaseID == "trace_smoke_route_error_tr789" &&
			item.Category == attributionRouteError &&
			item.TerminalStep != nil &&
			slices.Contains(item.AppliedSurfaceIDs, defaultTargetSurfaceID())
	}))
	require.Zero(t, result.ModelObservations.RequestCount)
	require.Zero(t, result.Report.ModelCallCount)
	require.Zero(t, result.Report.LatencyMs)
	require.FileExists(t, result.ReportJSONPath)
	require.FileExists(t, result.ReportMarkdownPath)
}

func TestReportLatencyMs(t *testing.T) {
	require.Equal(t, int64(42), reportLatencyMs(42*time.Millisecond, false))
	require.Equal(t, sampleReportLatencyMs, reportLatencyMs(42*time.Millisecond, true))
}

func TestSampleReportSnapshotIsStableAndUpToDate(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	runSample := func(outputDir string) ([]byte, []byte) {
		result, err := runFakePipeline(context.Background(), RunConfig{
			Mode:         fakeMode,
			DataDir:      "./data",
			OutputDir:    outputDir,
			PromptPath:   "./config/baseline_prompt.txt",
			ConfigPath:   "./config/promptiter.json",
			SampleReport: true,
		})
		require.NoError(t, err)
		require.Zero(t, result.Report.LatencyMs)
		require.NotNil(t, result.Report.Gate)
		require.Zero(t, result.Report.Gate.LatencyMs)
		require.Contains(t, result.Report.Gate.Reasons, "optimization latency 0ms is within maximum 180000ms")
		jsonContent, err := os.ReadFile(result.ReportJSONPath)
		require.NoError(t, err)
		markdownContent, err := os.ReadFile(result.ReportMarkdownPath)
		require.NoError(t, err)
		return jsonContent, markdownContent
	}

	firstJSON, firstMarkdown := runSample(t.TempDir())
	secondJSON, secondMarkdown := runSample(t.TempDir())
	require.Equal(t, firstJSON, secondJSON)
	require.Equal(t, firstMarkdown, secondMarkdown)

	checkedInJSON, err := os.ReadFile("./sample/optimization_report.json")
	require.NoError(t, err)
	checkedInMarkdown, err := os.ReadFile("./sample/optimization_report.md")
	require.NoError(t, err)
	require.Equal(t, checkedInJSON, firstJSON)
	require.Equal(t, checkedInMarkdown, firstMarkdown)
}

func TestPromptReadHashAndNeutralDefaultPrompt(t *testing.T) {
	defaultPrompt, _, err := readPrompt("./config/baseline_prompt.txt")
	require.NoError(t, err)
	defaultLower := strings.ToLower(defaultPrompt)
	require.NotContains(t, defaultLower, "flight")
	require.NotContains(t, defaultLower, "record")
	require.NotContains(t, defaultLower, "lookup")
	customPrompt := filepath.Join(t.TempDir(), "prompt.txt")
	require.NoError(t, os.WriteFile(customPrompt, []byte("You are a helpful assistant. CUSTOM_TRACE_TOKEN\n"), 0o644))
	_, defaultHash, err := readPrompt("./config/baseline_prompt.txt")
	require.NoError(t, err)
	_, customHash, err := readPrompt(customPrompt)
	require.NoError(t, err)
	require.NotEqual(t, defaultHash, customHash)
	result, err := runFakePipeline(context.Background(), RunConfig{
		Mode:       fakeMode,
		DataDir:    "./data",
		OutputDir:  t.TempDir(),
		PromptPath: customPrompt,
		ConfigPath: "./config/promptiter.json",
	})
	require.NoError(t, err)
	require.True(t, slices.ContainsFunc(result.ModelObservations.Instructions, func(instruction string) bool {
		return strings.Contains(instruction, "CUSTOM_TRACE_TOKEN")
	}))
}

func TestFakeModelIntentUsesOnlyUserMessagesAndToolDescription(t *testing.T) {
	fake := newFakeModel()
	noRecord := fake.generate(&model.Request{
		Messages: []model.Message{model.NewUserMessage("Expected invocation would ask for status, but this user text has no record id.")},
		Tools:    toolMap(round2ToolDescription),
	})
	require.Empty(t, noRecord.Choices[0].Message.ToolCalls)
	initialDescription := fake.generate(&model.Request{
		Messages: []model.Message{model.NewUserMessage("How delayed is TR123?")},
		Tools:    toolMap(initialToolDescription),
	})
	require.Empty(t, initialDescription.Choices[0].Message.ToolCalls)
	round1Delay := fake.generate(&model.Request{
		Messages: []model.Message{model.NewUserMessage("How delayed is TR123?")},
		Tools:    toolMap(round1ToolDescription),
	})
	require.Len(t, round1Delay.Choices[0].Message.ToolCalls, 1)
	require.Equal(t, "lookup_record", round1Delay.Choices[0].Message.ToolCalls[0].Function.Name)
	require.JSONEq(t, `{"query":"TR123"}`, string(round1Delay.Choices[0].Message.ToolCalls[0].Function.Arguments))
	mixedRoles := fake.generate(&model.Request{
		Messages: []model.Message{
			model.NewAssistantMessage("What is the status of TR999?"),
			model.NewUserMessage("How delayed is TR123?"),
		},
		Tools: toolMap(round1ToolDescription),
	})
	require.Len(t, mixedRoles.Choices[0].Message.ToolCalls, 1)
	require.Equal(t, "lookup_record", mixedRoles.Choices[0].Message.ToolCalls[0].Function.Name)
	require.JSONEq(t, `{"query":"TR123"}`, string(mixedRoles.Choices[0].Message.ToolCalls[0].Function.Arguments))
	round1Gate := fake.generate(&model.Request{
		Messages: []model.Message{model.NewUserMessage("Which gate is TR654 using?")},
		Tools:    toolMap(round1ToolDescription),
	})
	require.Empty(t, round1Gate.Choices[0].Message.ToolCalls)
	round2Gate := fake.generate(&model.Request{
		Messages: []model.Message{model.NewUserMessage("Which gate is TR654 using?")},
		Tools:    toolMap(round2ToolDescription),
	})
	require.Len(t, round2Gate.Choices[0].Message.ToolCalls, 1)
	require.Equal(t, "lookup_record", round2Gate.Choices[0].Message.ToolCalls[0].Function.Name)
	require.JSONEq(t, `{"query":"TR654"}`, string(round2Gate.Choices[0].Message.ToolCalls[0].Function.Arguments))
}

func TestFakeModelDirectNoToolAndOverfitOrdering(t *testing.T) {
	response, ok := directNoToolResponse("Without looking anything up and without a record ID, just say ready.")
	require.True(t, ok)
	require.Equal(t, "ready", response)
	response, ok = directNoToolResponse("Without looking anything up, just say \"TR789 is cancelled\".")
	require.True(t, ok)
	require.Equal(t, "TR789 is cancelled.", response)
	_, ok = directNoToolResponse("Without looking anything up, explain TR789.")
	require.False(t, ok)

	fake := newFakeModel()
	round1Direct := fake.generate(&model.Request{
		Messages: []model.Message{model.NewUserMessage("Without looking anything up, just say 'TR789 is cancelled'.")},
		Tools:    toolMap(round1ToolDescription),
	})
	require.Empty(t, round1Direct.Choices[0].Message.ToolCalls)
	require.Equal(t, "TR789 is cancelled.", round1Direct.Choices[0].Message.Content)
	round2Overfit := fake.generate(&model.Request{
		Messages: []model.Message{model.NewUserMessage("Without looking anything up, just say 'TR789 is cancelled'.")},
		Tools:    toolMap(round2ToolDescription),
	})
	require.Len(t, round2Overfit.Choices[0].Message.ToolCalls, 1)
	require.Equal(t, "lookup_record", round2Overfit.Choices[0].Message.ToolCalls[0].Function.Name)
	require.JSONEq(t, `{"query":"TR789"}`, string(round2Overfit.Choices[0].Message.ToolCalls[0].Function.Arguments))
}

func TestFakeWorkersCoverCurrentPromptIterAPIs(t *testing.T) {
	target := defaultTargetSurfaceID()
	back := &fakeBackwarder{targetSurfaceID: target}
	empty, err := back.Backward(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, empty.Gradients)
	nonTarget, err := back.Backward(context.Background(), &backwarder.Request{
		EvalSetID:                 trainEvalSetID,
		EvalCaseID:                "case",
		StepID:                    "step",
		AllowedGradientSurfaceIDs: []string{"candidate#tool.other"},
	})
	require.NoError(t, err)
	require.Empty(t, nonTarget.Gradients)
	targeted, err := back.Backward(context.Background(), &backwarder.Request{
		EvalSetID:                 trainEvalSetID,
		EvalCaseID:                "case",
		StepID:                    "step",
		AllowedGradientSurfaceIDs: []string{target},
	})
	require.NoError(t, err)
	require.Len(t, targeted.Gradients, 1)
	require.Equal(t, promptiter.LossSeverityP1, targeted.Gradients[0].Severity)
	require.Equal(t, trainEvalSetID, targeted.Gradients[0].EvalSetID)
	require.Equal(t, "case", targeted.Gradients[0].EvalCaseID)
	require.Equal(t, "step", targeted.Gradients[0].StepID)
	agg := &fakeAggregator{}
	aggregated, err := agg.Aggregate(context.Background(), &aggregator.Request{
		SurfaceID: target,
		NodeID:    candidateAgentName,
		Type:      astructure.SurfaceTypeTool,
		Gradients: targeted.Gradients,
	})
	require.NoError(t, err)
	require.NotNil(t, aggregated.Gradient)
	require.Equal(t, target, aggregated.Gradient.SurfaceID)
	require.Equal(t, candidateAgentName, aggregated.Gradient.NodeID)
	require.Equal(t, astructure.SurfaceTypeTool, aggregated.Gradient.Type)
	require.Len(t, aggregated.Gradient.Gradients, 1)
	opt := &fakeOptimizer{}
	patch, err := opt.Optimize(context.Background(), &optimizer.Request{
		Surface: &astructure.Surface{
			SurfaceID: target,
			NodeID:    candidateAgentName,
			Type:      astructure.SurfaceTypeTool,
			Value: astructure.SurfaceValue{
				Tools: []astructure.ToolRef{{ID: "lookup_record", Description: initialToolDescription}},
			},
		},
		Gradient: aggregated.Gradient,
	})
	require.NoError(t, err)
	require.NotNil(t, patch.Patch)
	require.Equal(t, target, patch.Patch.SurfaceID)
	require.Equal(t, "lookup_record", patch.Patch.Value.Tools[0].ID)
	require.Equal(t, round1ToolDescription, patch.Patch.Value.Tools[0].Description)
	patch, err = opt.Optimize(context.Background(), &optimizer.Request{
		Surface: &astructure.Surface{
			SurfaceID: target,
			NodeID:    candidateAgentName,
			Type:      astructure.SurfaceTypeTool,
			Value: astructure.SurfaceValue{
				Tools: []astructure.ToolRef{{ID: "lookup_record", Description: round1ToolDescription}},
			},
		},
		Gradient: aggregated.Gradient,
	})
	require.NoError(t, err)
	require.NotNil(t, patch.Patch)
	require.Equal(t, round2ToolDescription, patch.Patch.Value.Tools[0].Description)
}

func TestReportSchema(t *testing.T) {
	result, err := runFakePipeline(context.Background(), RunConfig{
		Mode:       fakeMode,
		DataDir:    "./data",
		OutputDir:  t.TempDir(),
		PromptPath: "./config/baseline_prompt.txt",
		ConfigPath: "./config/promptiter.json",
	})
	require.NoError(t, err)
	report := result.Report
	require.NotNil(t, report.Baseline.Train)
	require.NotNil(t, report.Baseline.Validation)
	require.NotNil(t, report.Candidate.Train)
	require.NotNil(t, report.Candidate.Validation)
	require.NotNil(t, report.Candidate.AcceptedProfile)
	require.NotNil(t, report.Delta)
	require.NotNil(t, report.Gate)
	require.NotNil(t, report.Attribution)
	require.NotEmpty(t, report.Rounds)
	require.NotNil(t, report.Rounds[0].Train)
	require.NotNil(t, report.Rounds[0].Validation)
	require.NotNil(t, report.Rounds[0].OutputProfile)
	require.NotEmpty(t, report.Rounds[0].Patches)
	require.NotEmpty(t, report.Rounds[0].Patches[0].Value.Tools)
	require.Empty(t, report.Pending)
	require.False(t, report.TraceSmoke.Enabled)
	raw, err := json.Marshal(report)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "trainScore")
	require.NotContains(t, string(raw), "validationScore")
	require.NotContains(t, string(raw), "phase1Pending")
	require.NotContains(t, string(raw), "failure_attribution")
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.Equal(t, phaseVersion, decoded["phase"])
	require.Contains(t, decoded, "baseline")
	require.Equal(t, float64(deterministicSeed), decoded["seed"])
	require.Contains(t, decoded, "configPath")
	require.Contains(t, decoded, "configSha256")
	require.Contains(t, decoded, "modelConfig")
	require.Contains(t, decoded, "promptIterConfig")
	require.Contains(t, decoded, "candidate")
	require.Contains(t, decoded, "rounds")
	require.Contains(t, decoded, "delta")
	require.Contains(t, decoded, "gate")
	require.Contains(t, decoded, "attribution")
	require.Contains(t, decoded, "traceSmoke")
	require.Contains(t, decoded, "pending")
	require.Contains(t, decoded, "latencyMs")
	require.Contains(t, decoded, "modelCallCount")
	gate := decoded["gate"].(map[string]any)
	require.Contains(t, gate, "rejectOnNewHardFail")
	require.Contains(t, gate, "rejectOnCriticalRegression")
	candidate := decoded["candidate"].(map[string]any)
	require.Contains(t, candidate, "acceptedProfile")
	rounds := decoded["rounds"].([]any)
	require.Contains(t, rounds[0].(map[string]any), "outputProfile")
	patches := rounds[0].(map[string]any)["patches"].([]any)
	require.Contains(t, patches[0].(map[string]any), "value")
	markdown, err := os.ReadFile(result.ReportMarkdownPath)
	require.NoError(t, err)
	require.Contains(t, string(markdown), "- Baseline prompt: `./config/baseline_prompt.txt`")
	require.Contains(t, string(markdown), "- Baseline prompt SHA-256: `75357d685f238b6afd7738be9786fdafde641eb6ca9a3be7471939715a68a4de`")
	require.Contains(t, string(markdown), "- Final gate: rejectOnNewHardFail=`true`, rejectOnCriticalRegression=`true`, maxDurationMs=`180000`, maxModelCalls=`100`")
}

func TestTraceSmokeAdapterUsesRunDetailsExecutionTrace(t *testing.T) {
	executionTrace := traceForTest("trace_from_run_details")
	agentResult := agentEvaluationResultForAdapter(
		"trace_case",
		[]*evalresult.EvalMetricResult{
			evalMetricForTest("tool_trajectory_avg_score", 0, status.EvalStatusFailed, "tool trajectory mismatch: validate tool counts: number of tool calls mismatch: actual(1) != expected(0)"),
		},
		&evaluation.EvaluationInferenceDetails{
			SessionID:       "trace-session",
			ExecutionTraces: []*atrace.Trace{executionTrace},
		},
	)
	engineResult, err := adaptAgentEvaluationResultToEngine(agentResult)
	require.NoError(t, err)
	require.Equal(t, 0.0, engineResult.OverallScore)
	require.Len(t, engineResult.EvalSets, 1)
	require.Len(t, engineResult.EvalSets[0].Cases, 1)
	convertedCase := engineResult.EvalSets[0].Cases[0]
	require.Equal(t, "trace_case", convertedCase.EvalCaseID)
	require.Same(t, executionTrace, convertedCase.Trace)
	require.Equal(t, "trace-session", convertedCase.SessionID)
	require.Len(t, convertedCase.Metrics, 1)
	require.Equal(t, "tool_trajectory_avg_score", convertedCase.Metrics[0].MetricName)
	require.Equal(t, status.EvalStatusFailed, convertedCase.Metrics[0].Status)
	require.Contains(t, convertedCase.Metrics[0].Reason, "actual(1) != expected(0)")
}

func TestTraceSmokeAdapterFallsBackToInvocationExecutionTrace(t *testing.T) {
	executionTrace := traceForTest("trace_from_invocation")
	agentResult := agentEvaluationResultForAdapter(
		"trace_case",
		[]*evalresult.EvalMetricResult{
			evalMetricForTest("final_response_avg_score", 1, status.EvalStatusPassed, ""),
		},
		&evaluation.EvaluationInferenceDetails{
			SessionID: "trace-session",
			Inferences: []*evalset.Invocation{
				{
					InvocationID:   "trace_case_1",
					ExecutionTrace: executionTrace,
				},
			},
		},
	)
	engineResult, err := adaptAgentEvaluationResultToEngine(agentResult)
	require.NoError(t, err)
	require.Same(t, executionTrace, engineResult.EvalSets[0].Cases[0].Trace)
	require.Equal(t, 1.0, engineResult.OverallScore)
}

func TestTraceSmokeAdapterUsesFailedInvocationTrace(t *testing.T) {
	turn1Trace := traceForTest("turn-1")
	turn2Trace := traceForTest("turn-2")
	turn1Trace.Steps[0].AppliedSurfaceIDs = []string{"surface-turn-1"}
	turn2Trace.Steps[0].AppliedSurfaceIDs = []string{"surface-turn-2"}
	passedMetric := evalMetricForTest("final_response_avg_score", 1, status.EvalStatusPassed, "")
	failedMetric := evalMetricForTest("final_response_avg_score", 0, status.EvalStatusFailed, "final response mismatch")
	runResult := &evalresult.EvalCaseResult{
		EvalSetID:                traceSmokeEvalSetID,
		EvalID:                   "trace_case",
		SessionID:                "run-result-session",
		OverallEvalMetricResults: []*evalresult.EvalMetricResult{failedMetric},
		EvalMetricResultPerInvocation: []*evalresult.EvalMetricResultPerInvocation{
			{
				ActualInvocation: &evalset.Invocation{
					InvocationID:   "trace_case_turn_1",
					ExecutionTrace: turn1Trace,
				},
				EvalMetricResults: []*evalresult.EvalMetricResult{passedMetric},
			},
			{
				ActualInvocation: &evalset.Invocation{
					InvocationID:   "trace_case_turn_2",
					ExecutionTrace: turn2Trace,
				},
				EvalMetricResults: []*evalresult.EvalMetricResult{failedMetric},
			},
		},
	}
	agentResult := &evaluation.EvaluationResult{
		EvalSetID: traceSmokeEvalSetID,
		EvalCases: []*evaluation.EvaluationCaseResult{
			{
				EvalCaseID:      "trace_case",
				EvalCaseResults: []*evalresult.EvalCaseResult{runResult},
				RunDetails: []*evaluation.EvaluationCaseRunDetails{
					{
						RunID: 1,
						Inference: &evaluation.EvaluationInferenceDetails{
							SessionID:       "trace-session",
							ExecutionTraces: []*atrace.Trace{turn1Trace},
						},
					},
				},
			},
		},
	}
	engineResult, err := adaptAgentEvaluationResultToEngine(agentResult)
	require.NoError(t, err)
	convertedCase := engineResult.EvalSets[0].Cases[0]
	require.Same(t, turn2Trace, convertedCase.Trace)

	attribution, err := buildFailureAttribution(engineResult)
	require.NoError(t, err)
	require.Len(t, attribution.PerFailedCase, 1)
	failedCase := attribution.PerFailedCase[0]
	require.NotNil(t, failedCase.TerminalStep)
	require.Equal(t, "turn-2", failedCase.TerminalStep.StepID)
	require.Equal(t, []string{"surface-turn-2"}, failedCase.AppliedSurfaceIDs)
}

func TestTraceSmokeMarkdownOmitsOptimizationDecision(t *testing.T) {
	report := newTraceSmokeOptimizationReport(
		summaryFromCases(traceSmokeEvalSetID, reportCase("trace_case", 0, false)),
		&FailureAttribution{
			PerFailedCase: []FailedCaseAttribution{{EvalCaseID: "trace_case", Category: attributionRouteError}},
			Summary:       AttributionSummary{RouteError: 1},
		},
		ReportContext{Mode: traceSmokeMode},
	)
	markdown := string(renderMarkdownReport(report))
	require.Contains(t, markdown, "## Trace Smoke")
	require.Contains(t, markdown, traceSmokeSkipReason)
	require.NotContains(t, markdown, "Candidate validation overall score")
	require.NotContains(t, markdown, "Final release gate decision")
}

func TestMarkdownGateDecisionSummaryUsesActualGateReasons(t *testing.T) {
	tests := []struct {
		name                string
		decision            string
		reason              string
		criticalRegressions []string
		expectedSummary     string
	}{
		{
			name:            "accepted",
			decision:        gateDecisionAccept,
			reason:          "all configured checks passed",
			expectedSummary: "Final release outcome: approved by the final gate.",
		},
		{
			name:            "low gain rejected",
			decision:        gateDecisionReject,
			reason:          "validation gain 0.0100 is below minimum 0.0500",
			expectedSummary: "Final release outcome: rejected by the final gate; see the Final Gate reasons below.",
		},
		{
			name:                "critical regression rejected",
			decision:            gateDecisionReject,
			reason:              "critical regression cases: [critical]",
			criticalRegressions: []string{"critical"},
			expectedSummary:     "Final release outcome: rejected by the final gate because critical validation regression cases were detected: `critical`.",
		},
		{
			name:            "model call budget rejected",
			decision:        gateDecisionReject,
			reason:          "model calls 6 exceeds maximum 5",
			expectedSummary: "Final release outcome: rejected by the final gate; see the Final Gate reasons below.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := &OptimizationReport{
				Mode: fakeMode,
				Gate: &GateReport{
					Decision:            tt.decision,
					Reasons:             []string{tt.reason},
					CriticalRegressions: tt.criticalRegressions,
				},
			}
			markdown := string(renderMarkdownReport(report))
			require.Contains(t, markdown, tt.expectedSummary)
			require.Contains(t, markdown, tt.reason)
			require.NotContains(t, markdown, "Train and validation aggregate scores improved")
		})
	}
}

func TestMarkdownClarifiesPromptIterAcceptanceIsNotReleaseApproval(t *testing.T) {
	report := &OptimizationReport{
		Mode: fakeMode,
		Candidate: ReportCandidate{
			AcceptedProfile: &ProfileSummary{Overrides: []SurfaceOverrideSummary{}},
		},
		Rounds: []ReportRound{
			{
				Round:            1,
				Accepted:         true,
				AcceptanceReason: "candidate score gain satisfies acceptance policy",
			},
		},
		Gate: &GateReport{
			Decision:            gateDecisionReject,
			Reasons:             []string{"critical regression cases: [critical]"},
			CriticalRegressions: []string{"critical"},
		},
	}
	markdown := string(renderMarkdownReport(report))
	require.Contains(t, markdown, "### PromptIter Accepted Profile")
	require.Contains(t, markdown, "- Accepted by PromptIter: `true`")
	require.Contains(t, markdown, "- PromptIter acceptance reason: candidate score gain satisfies acceptance policy")
	require.Contains(t, markdown, "it is not release approval")
	require.Contains(t, markdown, "Final release gate decision: `reject`")
	require.Contains(t, markdown, "critical validation regression cases were detected: `critical`")
	require.NotContains(t, markdown, "\n### Accepted Profile\n")
	require.NotContains(t, markdown, "\n- Accepted: `true`\n")
}

func TestFinalGateConfigDefaultsAndOverrides(t *testing.T) {
	var nilConfig *finalGateFileConfig
	defaults := nilConfig.resolved()
	require.Equal(t, 0.05, defaults.MinValidationGain)
	require.Equal(t, int64(180000), defaults.MaxDurationMs)
	require.Zero(t, defaults.MaxModelCalls)
	require.ElementsMatch(t, []string{"validation_status_tr789"}, defaults.CriticalCaseIDs)
	require.True(t, defaults.RejectOnNewHardFail)
	require.True(t, defaults.RejectOnCriticalRegression)

	gain := 0.2
	maxDuration := int64(42)
	maxModelCalls := 7
	rejectHardFail := false
	rejectCritical := false
	overrides := (&finalGateFileConfig{
		MinValidationGain:          &gain,
		MaxDurationMs:              &maxDuration,
		MaxModelCalls:              &maxModelCalls,
		CriticalCaseIDs:            []string{"case_a"},
		RejectOnNewHardFail:        &rejectHardFail,
		RejectOnCriticalRegression: &rejectCritical,
	}).resolved()
	require.Equal(t, 0.2, overrides.MinValidationGain)
	require.Equal(t, int64(42), overrides.MaxDurationMs)
	require.Equal(t, 7, overrides.MaxModelCalls)
	require.ElementsMatch(t, []string{"case_a"}, overrides.CriticalCaseIDs)
	require.False(t, overrides.RejectOnNewHardFail)
	require.False(t, overrides.RejectOnCriticalRegression)

	cleared := (&finalGateFileConfig{CriticalCaseIDs: []string{}}).resolved()
	require.Empty(t, cleared.CriticalCaseIDs)
	require.True(t, cleared.RejectOnCriticalRegression)
}

func TestPromptIterConfigNewFieldsAndLegacyCompatibility(t *testing.T) {
	configDir := t.TempDir()
	newConfigPath := filepath.Join(configDir, "new.json")
	newConfigContent := []byte(`{
  "targetSurfaceIDs": ["candidate#tool.lookup_record"],
  "maxRounds": 3,
  "minScoreGain": 0.01,
  "targetScore": 0.25,
  "acceptancePolicy": {
    "minScoreGain": 0.3
  },
  "stopPolicy": {
    "targetScore": 0.8,
    "maxRoundsWithoutAcceptance": 2
  },
  "finalGate": {
    "maxModelCalls": 11
  }
}`)
	require.NoError(t, os.WriteFile(newConfigPath, newConfigContent, 0o644))
	cfg, configHash, err := readPromptIterConfigWithHash(newConfigPath)
	require.NoError(t, err)
	expectedHash := sha256.Sum256(newConfigContent)
	require.Equal(t, hex.EncodeToString(expectedHash[:]), configHash)
	targetSurfaceIDs, err := resolveTargetSurfaceIDs(cfg)
	require.NoError(t, err)
	require.Equal(t, []string{defaultTargetSurfaceID()}, targetSurfaceIDs)
	require.Equal(t, 0.3, cfg.minScoreGain())
	require.NotNil(t, cfg.targetScore())
	require.Equal(t, 0.8, *cfg.targetScore())
	require.Equal(t, 2, cfg.maxRoundsWithoutAcceptance())
	require.Equal(t, 11, cfg.FinalGate.resolved().MaxModelCalls)
	runRequest := buildRunRequest(cfg, targetSurfaceIDs[0])
	require.Equal(t, 0.3, runRequest.AcceptancePolicy.MinScoreGain)
	require.NotNil(t, runRequest.StopPolicy.TargetScore)
	require.Equal(t, 0.8, *runRequest.StopPolicy.TargetScore)
	require.Equal(t, 2, runRequest.StopPolicy.MaxRoundsWithoutAcceptance)

	legacyConfigPath := filepath.Join(configDir, "legacy.json")
	require.NoError(t, os.WriteFile(legacyConfigPath, []byte(`{
  "maxRounds": 2,
  "minScoreGain": 0.2,
  "targetScore": 0.7
}`), 0o644))
	legacy, err := readPromptIterConfig(legacyConfigPath)
	require.NoError(t, err)
	legacyTargets, err := resolveTargetSurfaceIDs(legacy)
	require.NoError(t, err)
	require.Equal(t, []string{defaultTargetSurfaceID()}, legacyTargets)
	require.Equal(t, 0.2, legacy.minScoreGain())
	require.NotNil(t, legacy.targetScore())
	require.Equal(t, 0.7, *legacy.targetScore())

	unsupportedConfigPath := filepath.Join(configDir, "unsupported.json")
	require.NoError(t, os.WriteFile(unsupportedConfigPath, []byte(`{
  "targetSurfaceIDs": ["candidate#instruction"]
}`), 0o644))
	unsupported, err := readPromptIterConfig(unsupportedConfigPath)
	require.NoError(t, err)
	_, err = resolveTargetSurfaceIDs(unsupported)
	require.ErrorContains(t, err, "unsupported targetSurfaceID")

	emptyCriticalPath := filepath.Join(configDir, "empty-critical.json")
	require.NoError(t, os.WriteFile(emptyCriticalPath, []byte(`{
  "finalGate": {
    "criticalCaseIDs": []
  }
}`), 0o644))
	emptyCritical, err := readPromptIterConfig(emptyCriticalPath)
	require.NoError(t, err)
	require.NotNil(t, emptyCritical.FinalGate.CriticalCaseIDs)
	require.Empty(t, emptyCritical.FinalGate.resolved().CriticalCaseIDs)

	blankConfigPath := filepath.Join(configDir, "blank.json")
	require.NoError(t, os.WriteFile(blankConfigPath, []byte(" \n\t"), 0o644))
	_, err = readPromptIterConfig(blankConfigPath)
	require.ErrorContains(t, err, "promptiter config is empty")

	unknownTopLevelPath := filepath.Join(configDir, "unknown-top-level.json")
	require.NoError(t, os.WriteFile(unknownTopLevelPath, []byte(`{
  "maxRounds": 2,
  "unknownReleasePolicy": true
}`), 0o644))
	_, err = readPromptIterConfig(unknownTopLevelPath)
	require.ErrorContains(t, err, `unknown field "unknownReleasePolicy"`)

	unknownMaxModelCallsPath := filepath.Join(configDir, "unknown-max-model-calls.json")
	require.NoError(t, os.WriteFile(unknownMaxModelCallsPath, []byte(`{
  "finalGate": {
    "maxModelCall": 11
  }
}`), 0o644))
	_, err = readPromptIterConfig(unknownMaxModelCallsPath)
	require.ErrorContains(t, err, `unknown field "maxModelCall"`)

	unknownRejectPolicyPath := filepath.Join(configDir, "unknown-reject-policy.json")
	require.NoError(t, os.WriteFile(unknownRejectPolicyPath, []byte(`{
  "finalGate": {
    "rejectOnNewHardFailure": false
  }
}`), 0o644))
	_, err = readPromptIterConfig(unknownRejectPolicyPath)
	require.ErrorContains(t, err, `unknown field "rejectOnNewHardFailure"`)
}

func TestRunOptionsForAcceptedProfileValidation(t *testing.T) {
	target := defaultTargetSurfaceID()
	runOptions, err := runOptionsForAcceptedProfile(&promptiter.Profile{
		Overrides: []promptiter.SurfaceOverride{
			{
				SurfaceID: target,
				Value: astructure.SurfaceValue{
					Tools: []astructure.ToolRef{{ID: "lookup_record", Description: round2ToolDescription}},
				},
			},
		},
	}, target)
	require.NoError(t, err)
	require.Len(t, runOptions, 1)

	_, err = runOptionsForAcceptedProfile(&promptiter.Profile{
		Overrides: []promptiter.SurfaceOverride{{SurfaceID: "candidate#tool.other"}},
	}, target)
	require.ErrorContains(t, err, "unsupported accepted profile surface")

	_, err = runOptionsForAcceptedProfile(&promptiter.Profile{
		Overrides: []promptiter.SurfaceOverride{
			{
				SurfaceID: target,
				Value: astructure.SurfaceValue{
					Tools: []astructure.ToolRef{
						{ID: "lookup_record", Description: round2ToolDescription},
						{ID: "other", Description: "other"},
					},
				},
			},
		},
	}, target)
	require.ErrorContains(t, err, "exactly one tool override")

	_, err = runOptionsForAcceptedProfile(&promptiter.Profile{
		Overrides: []promptiter.SurfaceOverride{
			{
				SurfaceID: target,
				Value: astructure.SurfaceValue{
					Tools: []astructure.ToolRef{{ID: "other", Description: round2ToolDescription}},
				},
			},
		},
	}, target)
	require.ErrorContains(t, err, "unsupported accepted profile tool")
}

func TestValidationDeltaCategories(t *testing.T) {
	baseline := summaryFromCases("validation",
		reportCase("new_pass", 0, false),
		reportCase("new_fail", 1, true),
		reportCase("improved", 0.25, false),
		reportCase("regressed", 0.50, false),
		reportCase("unchanged_pass", 1, true),
		reportCase("unchanged_fail", 0, false),
	)
	candidate := summaryFromCases("validation",
		reportCase("new_pass", 1, true),
		reportCase("new_fail", 0, false),
		reportCase("improved", 0.50, false),
		reportCase("regressed", 0.25, false),
		reportCase("unchanged_pass", 1, true),
		reportCase("unchanged_fail", 0, false),
	)
	delta, err := buildValidationDelta(baseline, candidate)
	require.NoError(t, err)
	require.Equal(t, DeltaSummary{
		NewPass:       1,
		NewFail:       1,
		Improved:      1,
		Regressed:     1,
		UnchangedPass: 1,
		UnchangedFail: 1,
	}, delta.Summary)
	require.True(t, delta.PerCase[1].NewHardFail)

	_, err = buildValidationDelta(baseline, summaryFromCases("validation", reportCase("new_pass", 1, true)))
	require.ErrorContains(t, err, "case count")
}

func TestCaseScorePassedAndHardFailHelpers(t *testing.T) {
	score, passed := caseScoreAndPassed(CaseSummary{})
	require.Equal(t, 0.0, score)
	require.False(t, passed)
	require.True(t, isHardFail(score, passed))

	score, passed = caseScoreAndPassed(CaseSummary{
		Metrics: []MetricSummary{
			{Score: 1, Status: "passed"},
			{Score: 0.5, Status: "failed"},
		},
	})
	require.Equal(t, 0.75, score)
	require.False(t, passed)
	require.False(t, isHardFail(score, passed))
}

func TestFinalGateDecisions(t *testing.T) {
	cfg := defaultFinalGateConfig()
	cfg.CriticalCaseIDs = []string{"critical"}
	baseline := summaryFromCases("validation",
		reportCase("critical", 0.5, false),
		reportCase("regular", 0.5, false),
	)
	candidate := summaryFromCases("validation",
		reportCase("critical", 1, true),
		reportCase("regular", 1, true),
	)
	delta, err := buildValidationDelta(baseline, candidate)
	require.NoError(t, err)
	gate, err := buildGateReport(baseline, candidate, delta, cfg, 10, 5, fakeMode)
	require.NoError(t, err)
	require.Equal(t, gateDecisionAccept, gate.Decision)
	require.Contains(t, gate.Reasons, "cost check skipped (fake mode)")
	require.Contains(t, gate.Reasons, "model call budget check skipped")

	lowGainCfg := cfg
	lowGainCfg.MinValidationGain = 0.75
	gate, err = buildGateReport(baseline, candidate, delta, lowGainCfg, 10, 5, fakeMode)
	require.NoError(t, err)
	require.Equal(t, gateDecisionReject, gate.Decision)

	hardFailCandidate := summaryFromCases("validation",
		reportCase("critical", 0.5, false),
		reportCase("regular", 0, false),
	)
	hardFailDelta, err := buildValidationDelta(
		summaryFromCases("validation", reportCase("critical", 0.5, false), reportCase("regular", 1, true)),
		hardFailCandidate,
	)
	require.NoError(t, err)
	gate, err = buildGateReport(baseline, hardFailCandidate, hardFailDelta, cfg, 10, 5, fakeMode)
	require.NoError(t, err)
	require.Equal(t, gateDecisionReject, gate.Decision)
	require.Contains(t, gate.NewHardFails, "regular")

	criticalRegressionCandidate := summaryFromCases("validation",
		reportCase("critical", 0.25, false),
		reportCase("regular", 1, true),
	)
	criticalDelta, err := buildValidationDelta(baseline, criticalRegressionCandidate)
	require.NoError(t, err)
	gate, err = buildGateReport(baseline, criticalRegressionCandidate, criticalDelta, cfg, 10, 5, fakeMode)
	require.NoError(t, err)
	require.Equal(t, gateDecisionReject, gate.Decision)
	require.Contains(t, gate.CriticalRegressions, "critical")

	statusRegressionCfg := cfg
	statusRegressionCfg.MinValidationGain = 0
	statusRegressionCfg.CriticalCaseIDs = []string{"critical_equal_score"}
	statusRegressionBaseline := summaryFromCases("validation",
		reportCaseWithMetrics("critical_equal_score",
			MetricSummary{MetricName: "metric_1", Score: 0.5, Status: "passed"},
			MetricSummary{MetricName: "metric_2", Score: 0.5, Status: "passed"},
		),
	)
	statusRegressionCandidate := summaryFromCases("validation",
		reportCaseWithMetrics("critical_equal_score",
			MetricSummary{MetricName: "metric_1", Score: 1, Status: "passed"},
			MetricSummary{MetricName: "metric_2", Score: 0, Status: "failed"},
		),
	)
	statusRegressionDelta, err := buildValidationDelta(statusRegressionBaseline, statusRegressionCandidate)
	require.NoError(t, err)
	require.Len(t, statusRegressionDelta.PerCase, 1)
	require.Equal(t, deltaNewFail, statusRegressionDelta.PerCase[0].Category)
	require.False(t, statusRegressionDelta.PerCase[0].NewHardFail)
	gate, err = buildGateReport(statusRegressionBaseline, statusRegressionCandidate, statusRegressionDelta, statusRegressionCfg, 10, 5, fakeMode)
	require.NoError(t, err)
	require.Equal(t, gateDecisionReject, gate.Decision)
	require.Contains(t, gate.CriticalRegressions, "critical_equal_score")
	require.True(t, statusRegressionDelta.PerCase[0].CriticalRegression)

	nonEnforcingCfg := cfg
	nonEnforcingCfg.RejectOnNewHardFail = false
	nonEnforcingCfg.RejectOnCriticalRegression = false
	nonEnforcingBaseline := summaryFromCases("validation",
		reportCase("critical", 0.5, false),
		reportCase("hard_fail", 1, true),
		reportCase("improved", 0, false),
		reportCase("steady", 0, false),
	)
	nonEnforcingCandidate := summaryFromCases("validation",
		reportCase("critical", 0, false),
		reportCase("hard_fail", 0, false),
		reportCase("improved", 1, true),
		reportCase("steady", 1, true),
	)
	nonEnforcingDelta, err := buildValidationDelta(nonEnforcingBaseline, nonEnforcingCandidate)
	require.NoError(t, err)
	gate, err = buildGateReport(nonEnforcingBaseline, nonEnforcingCandidate, nonEnforcingDelta, nonEnforcingCfg, 10, 5, fakeMode)
	require.NoError(t, err)
	require.Equal(t, gateDecisionAccept, gate.Decision)
	require.False(t, gate.RejectOnNewHardFail)
	require.False(t, gate.RejectOnCriticalRegression)
	require.NotEmpty(t, gate.NewHardFails)
	require.Contains(t, gate.NewHardFails, "hard_fail")
	require.Contains(t, gate.CriticalRegressions, "critical")
	require.Contains(t, gate.Reasons, "new hard fail cases detected but not enforced: [critical hard_fail]")
	require.Contains(t, gate.Reasons, "critical regression cases detected but not enforced: [critical]")
	rawGate, err := json.Marshal(gate)
	require.NoError(t, err)
	var serializedGate map[string]any
	require.NoError(t, json.Unmarshal(rawGate, &serializedGate))
	require.Equal(t, false, serializedGate["rejectOnNewHardFail"])
	require.Equal(t, false, serializedGate["rejectOnCriticalRegression"])
	require.NotEmpty(t, serializedGate["newHardFails"])
	require.NotEmpty(t, serializedGate["criticalRegressions"])

	latencyCfg := cfg
	latencyCfg.MaxDurationMs = 1
	gate, err = buildGateReport(baseline, candidate, delta, latencyCfg, 2, 5, fakeMode)
	require.NoError(t, err)
	require.Equal(t, gateDecisionReject, gate.Decision)

	latencyCfg.MaxDurationMs = 0
	gate, err = buildGateReport(baseline, candidate, delta, latencyCfg, 2, 5, fakeMode)
	require.NoError(t, err)
	require.Equal(t, gateDecisionAccept, gate.Decision)
	require.Contains(t, gate.Reasons, "latency budget check skipped")
	latencyCfg.MaxDurationMs = -1
	gate, err = buildGateReport(baseline, candidate, delta, latencyCfg, 2, 5, fakeMode)
	require.NoError(t, err)
	require.Equal(t, gateDecisionAccept, gate.Decision)
	require.Contains(t, gate.Reasons, "latency budget check skipped")

	callBudgetCfg := cfg
	callBudgetCfg.MaxModelCalls = 5
	gate, err = buildGateReport(baseline, candidate, delta, callBudgetCfg, 10, 5, fakeMode)
	require.NoError(t, err)
	require.Equal(t, gateDecisionAccept, gate.Decision)
	require.Contains(t, gate.Reasons, "model calls 5 is within maximum 5")
	gate, err = buildGateReport(baseline, candidate, delta, callBudgetCfg, 10, 6, fakeMode)
	require.NoError(t, err)
	require.Equal(t, gateDecisionReject, gate.Decision)
	require.Contains(t, gate.Reasons, "model calls 6 exceeds maximum 5")

	noCriticalCfg := cfg
	noCriticalCfg.CriticalCaseIDs = []string{}
	noCriticalBaseline := summaryFromCases("validation", reportCase("custom", 0, false))
	noCriticalCandidate := summaryFromCases("validation", reportCase("custom", 1, true))
	noCriticalDelta, err := buildValidationDelta(noCriticalBaseline, noCriticalCandidate)
	require.NoError(t, err)
	gate, err = buildGateReport(noCriticalBaseline, noCriticalCandidate, noCriticalDelta, noCriticalCfg, 10, 5, fakeMode)
	require.NoError(t, err)
	require.Equal(t, gateDecisionAccept, gate.Decision)
	require.Empty(t, gate.CriticalCaseIDs)
	require.Empty(t, gate.CriticalRegressions)
}

func TestFailureAttributionCategories(t *testing.T) {
	result := &promptiterengine.EvaluationResult{
		EvalSets: []promptiterengine.EvalSetResult{
			{
				EvalSetID: "validation",
				Cases: []promptiterengine.CaseResult{
					attributionCase("tool_not_called", "tool_trajectory_avg_score", "tool trajectory mismatch: validate tool counts: number of tool calls mismatch: actual(0) != expected(1)"),
					attributionCase("wrong_tool_name", "tool_trajectory_avg_score", "tool trajectory mismatch: name mismatch"),
					attributionCase("tool_arguments_mismatch", "tool_trajectory_avg_score", "tool trajectory mismatch: arguments mismatch"),
					attributionCase("route_error", "tool_trajectory_avg_score", "tool trajectory mismatch: validate tool counts: number of tool calls mismatch: actual(1) != expected(0)"),
					attributionCase("format_error", "final_response_avg_score", "json mismatch: invalid character"),
					attributionCase("knowledge_insufficient", "rubric_score", "knowledge insufficient: missing evidence"),
					attributionCase("final_response_mismatch", "final_response_avg_score", "final response mismatch"),
					attributionCase("metric_failure", "custom_metric", "unexpected scorer output"),
				},
			},
		},
	}
	attribution, err := buildFailureAttribution(result)
	require.NoError(t, err)
	require.Len(t, attribution.PerFailedCase, 8)
	categories := map[string]string{}
	for _, item := range attribution.PerFailedCase {
		categories[item.EvalCaseID] = item.Category
		require.NotEmpty(t, item.Evidence)
	}
	require.Equal(t, attributionToolNotCalled, categories["tool_not_called"])
	require.Equal(t, attributionWrongToolName, categories["wrong_tool_name"])
	require.Equal(t, attributionToolArgumentsMismatch, categories["tool_arguments_mismatch"])
	require.Equal(t, attributionRouteError, categories["route_error"])
	require.Equal(t, attributionFormatError, categories["format_error"])
	require.Equal(t, attributionKnowledgeInsufficient, categories["knowledge_insufficient"])
	require.Equal(t, attributionFinalResponseMismatch, categories["final_response_mismatch"])
	require.Equal(t, attributionMetricFailure, categories["metric_failure"])
	require.Equal(t, 1, attribution.Summary.ToolNotCalled)
	require.Equal(t, 1, attribution.Summary.WrongToolName)
	require.Equal(t, 1, attribution.Summary.ToolArgumentsMismatch)
	require.Equal(t, 1, attribution.Summary.RouteError)
	require.Equal(t, 1, attribution.Summary.FormatError)
	require.Equal(t, 1, attribution.Summary.KnowledgeInsufficient)
	require.Equal(t, 1, attribution.Summary.FinalResponseMismatch)
	require.Equal(t, 1, attribution.Summary.MetricFailure)
}

func TestFailureAttributionRejectsEmptyFailedReason(t *testing.T) {
	_, err := buildFailureAttribution(&promptiterengine.EvaluationResult{
		EvalSets: []promptiterengine.EvalSetResult{
			{
				EvalSetID: "validation",
				Cases: []promptiterengine.CaseResult{
					{
						EvalSetID:  "validation",
						EvalCaseID: "empty_reason",
						Metrics: []promptiterengine.MetricResult{
							{
								MetricName: "custom_metric",
								Status:     status.EvalStatusFailed,
								Score:      0,
								Reason:     " ",
							},
						},
					},
				},
			},
		},
	})
	require.ErrorContains(t, err, "missing reason")
}

func TestReportUsesLastAcceptedRoundAndHandlesNoAcceptedRound(t *testing.T) {
	target := defaultTargetSurfaceID()
	baselineValidation := engineEvalResult("validation", "validation_status_tr789", 0, false)
	acceptedValidation := engineEvalResult("validation", "validation_status_tr789", 1, true)
	rejectedValidation := engineEvalResult("validation", "validation_status_tr789", 0.25, false)
	train := engineEvalResult("train", "train_status_tr123", 0, false)
	result := &promptiterengine.RunResult{
		BaselineValidation: baselineValidation,
		Rounds: []promptiterengine.RoundResult{
			{
				Round:         1,
				Train:         train,
				OutputProfile: acceptedProfile(target),
				Validation:    acceptedValidation,
				Acceptance:    &promptiterengine.AcceptanceDecision{Accepted: true, ScoreDelta: 1},
			},
			{
				Round:         2,
				Train:         train,
				OutputProfile: acceptedProfile(target),
				Validation:    rejectedValidation,
				Acceptance:    &promptiterengine.AcceptanceDecision{Accepted: false, ScoreDelta: -0.75},
			},
		},
	}
	report, err := newOptimizationReport(result, summaryFromCases("train", reportCase("train_status_tr123", 1, true)), ReportContext{
		Mode:             fakeMode,
		TargetSurfaceIDs: []string{target},
		FinalGate:        defaultFinalGateConfig(),
	})
	require.NoError(t, err)
	require.Equal(t, 1.0, report.Candidate.Validation.OverallScore)

	noAccepted := &promptiterengine.RunResult{
		BaselineValidation: baselineValidation,
		Rounds: []promptiterengine.RoundResult{
			{
				Round:      1,
				Train:      train,
				Validation: rejectedValidation,
				Acceptance: &promptiterengine.AcceptanceDecision{Accepted: false, ScoreDelta: -0.75},
			},
		},
	}
	report, err = newOptimizationReport(noAccepted, nil, ReportContext{
		Mode:             fakeMode,
		TargetSurfaceIDs: []string{target},
		FinalGate:        defaultFinalGateConfig(),
	})
	require.NoError(t, err)
	require.NotNil(t, report.Candidate.Train)
	require.Equal(t, report.Baseline.Train, report.Candidate.Train)
	require.Equal(t, report.Baseline.Validation, report.Candidate.Validation)
}

func toolMap(description string) map[string]tool.Tool {
	return map[string]tool.Tool{
		"lookup_record": toolForTest{description: description},
	}
}

type toolForTest struct {
	description string
}

func (t toolForTest) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        "lookup_record",
		Description: t.description,
	}
}

func reportCase(id string, score float64, passed bool) CaseSummary {
	statusValue := "failed"
	if passed {
		statusValue = "passed"
	}
	return CaseSummary{
		EvalCaseID: id,
		Metrics: []MetricSummary{
			{MetricName: "metric", Score: score, Status: statusValue},
		},
	}
}

func reportCaseWithMetrics(id string, metrics ...MetricSummary) CaseSummary {
	return CaseSummary{
		EvalCaseID: id,
		Metrics:    metrics,
	}
}

func summaryFromCases(evalSetID string, cases ...CaseSummary) *EvaluationSummary {
	total := 0.0
	count := 0
	for _, evalCase := range cases {
		for _, metric := range evalCase.Metrics {
			total += metric.Score
			count++
		}
	}
	overall := 0.0
	if count > 0 {
		overall = total / float64(count)
	}
	return &EvaluationSummary{
		OverallScore: overall,
		EvalSets: []EvalSetSummary{
			{
				EvalSetID:    evalSetID,
				OverallScore: overall,
				Cases:        cases,
			},
		},
	}
}

func engineEvalResult(evalSetID, caseID string, score float64, passed bool) *promptiterengine.EvaluationResult {
	evalStatus := status.EvalStatusFailed
	reason := "failed"
	if passed {
		evalStatus = status.EvalStatusPassed
		reason = ""
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
						EvalCaseID: caseID,
						Metrics: []promptiterengine.MetricResult{
							{
								MetricName: "metric",
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

func attributionCase(caseID, metricName, reason string) promptiterengine.CaseResult {
	return promptiterengine.CaseResult{
		EvalSetID:  "validation",
		EvalCaseID: caseID,
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

func acceptedProfile(targetSurfaceID string) *promptiter.Profile {
	return &promptiter.Profile{
		Overrides: []promptiter.SurfaceOverride{
			{
				SurfaceID: targetSurfaceID,
				Value: astructure.SurfaceValue{
					Tools: []astructure.ToolRef{{ID: "lookup_record", Description: round2ToolDescription}},
				},
			},
		},
	}
}

func traceForTest(stepID string) *atrace.Trace {
	return &atrace.Trace{
		RootAgentName:    candidateAgentName,
		RootInvocationID: "root",
		SessionID:        "trace-session",
		Status:           atrace.TraceStatusCompleted,
		Steps: []atrace.Step{
			{
				StepID:            stepID,
				AgentName:         candidateAgentName,
				NodeID:            candidateAgentName,
				AppliedSurfaceIDs: []string{defaultTargetSurfaceID()},
			},
		},
	}
}

func agentEvaluationResultForAdapter(
	caseID string,
	metrics []*evalresult.EvalMetricResult,
	inference *evaluation.EvaluationInferenceDetails,
) *evaluation.EvaluationResult {
	runResult := &evalresult.EvalCaseResult{
		EvalSetID:                traceSmokeEvalSetID,
		EvalID:                   caseID,
		SessionID:                "run-result-session",
		OverallEvalMetricResults: metrics,
	}
	return &evaluation.EvaluationResult{
		EvalSetID: traceSmokeEvalSetID,
		EvalCases: []*evaluation.EvaluationCaseResult{
			{
				EvalCaseID:      caseID,
				EvalCaseResults: []*evalresult.EvalCaseResult{runResult},
				RunDetails: []*evaluation.EvaluationCaseRunDetails{
					{
						RunID:     1,
						Inference: inference,
					},
				},
			},
		},
	}
}

func evalMetricForTest(
	name string,
	score float64,
	evalStatus status.EvalStatus,
	reason string,
) *evalresult.EvalMetricResult {
	metric := &evalresult.EvalMetricResult{
		MetricName: name,
		Score:      score,
		EvalStatus: evalStatus,
	}
	if reason != "" {
		metric.Details = &evalresult.EvalMetricResultDetails{
			Reason: reason,
			Score:  score,
		}
	}
	return metric
}
