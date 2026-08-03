//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type stubEngine struct {
	result *promptiterengine.RunResult
	err    error
}

func (s stubEngine) Run(context.Context, *promptiterengine.RunRequest, ...promptiterengine.Option) (*promptiterengine.RunResult, error) {
	return s.result, s.err
}

func TestRunExecutesPromptIterAndAnalyzesResult(t *testing.T) {
	report, err := Run(context.Background(), stubEngine{result: runResult(0.5, 0.8, true)},
		&promptiterengine.RunRequest{MaxRounds: 1}, GateConfig{MinScoreGain: 0.1})
	require.NoError(t, err)
	assert.True(t, report.Accepted)
}

func TestRunPropagatesCancellationAndEngineErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Run(ctx, stubEngine{}, &promptiterengine.RunRequest{MaxRounds: 1}, GateConfig{})
	require.ErrorIs(t, err, context.Canceled)
	_, err = Run(context.Background(), stubEngine{err: errors.New("engine failed")},
		&promptiterengine.RunRequest{MaxRounds: 1}, GateConfig{})
	require.ErrorContains(t, err, "run PromptIter")
}

type recordingEngine struct {
	results  []*promptiterengine.RunResult
	profiles []*promptiter.Profile
}

func (e *recordingEngine) Run(_ context.Context, request *promptiterengine.RunRequest, _ ...promptiterengine.Option) (*promptiterengine.RunResult, error) {
	e.profiles = append(e.profiles, request.InitialProfile)
	result := e.results[0]
	e.results = e.results[1:]
	return result, nil
}

func TestRunDoesNotPropagateGateRejectedProfile(t *testing.T) {
	initial := profile("initial")
	engine := &recordingEngine{results: []*promptiterengine.RunResult{
		runResult(0.5, 0.4, true),
		runResult(0.5, 0.8, true),
	}}
	report, err := Run(context.Background(), engine,
		&promptiterengine.RunRequest{InitialProfile: initial, MaxRounds: 2}, GateConfig{MinScoreGain: 0.1})
	require.NoError(t, err)
	require.Len(t, engine.profiles, 2)
	assert.Same(t, initial, engine.profiles[0])
	assert.Same(t, initial, engine.profiles[1])
	require.Len(t, report.Rounds, 2)
	assert.False(t, report.Rounds[0].Decision.Accepted)
	assert.True(t, report.Rounds[1].Decision.Accepted)
	assert.Equal(t, 2, report.AcceptedRound)
}

func TestRunPropagatesGateAcceptedProfile(t *testing.T) {
	initial := profile("initial")
	first := runResult(0.5, 0.8, true)
	engine := &recordingEngine{results: []*promptiterengine.RunResult{
		first,
		runResult(0.8, 0.95, true),
	}}
	report, err := Run(context.Background(), engine,
		&promptiterengine.RunRequest{InitialProfile: initial, MaxRounds: 2}, GateConfig{MinScoreGain: 0.1})
	require.NoError(t, err)
	require.Len(t, engine.profiles, 2)
	assert.Same(t, initial, engine.profiles[0])
	assert.Same(t, first.Rounds[0].OutputProfile, engine.profiles[1])
	assert.Equal(t, 2, report.AcceptedRound)
}

func TestAnalyzeRunAcceptsSafeCandidate(t *testing.T) {
	result := runResult(0.5, 0.8, true)
	report, err := AnalyzeRun(result, GateConfig{MinScoreGain: 0.1})
	require.NoError(t, err)
	assert.True(t, report.Accepted)
	assert.Equal(t, 1, report.AcceptedRound)
}

func TestAnalyzeRunRejectsEngineRejection(t *testing.T) {
	result := runResult(0.5, 0.8, false)
	report, err := AnalyzeRun(result, GateConfig{MinScoreGain: 0.1})
	require.NoError(t, err)
	assert.False(t, report.Accepted)
	assert.Contains(t, report.Rounds[0].Decision.Reasons, "PromptIter engine rejected the candidate")
}

func TestAnalyzeRunRejectsIncompleteValidation(t *testing.T) {
	result := runResult(0.5, 0.8, true)
	result.Rounds[0].Validation = nil
	report, err := AnalyzeRun(result, GateConfig{})
	require.NoError(t, err)
	assert.False(t, report.Rounds[0].Decision.Accepted)
}

func TestAnalyzeRunDoesNotPromoteRejectedBaseline(t *testing.T) {
	result := runResult(0.5, 0.4, true)
	result.Rounds = append(result.Rounds, promptiterengine.RoundResult{
		Round: 2, Validation: testEvaluation(0.65), Acceptance: &promptiterengine.AcceptanceDecision{Accepted: true},
		OutputProfile: profile("second"),
	})
	report, err := AnalyzeRun(result, GateConfig{MinScoreGain: 0.1, MaxScoreRegressions: 1})
	require.NoError(t, err)
	assert.InDelta(t, 0.15, report.Rounds[1].DeltaFromAccepted.ScoreDelta, 0.0001)
}

func TestWriteReportsAndAcceptedPrompt(t *testing.T) {
	result := runResult(0.5, 0.8, true)
	report, err := AnalyzeRun(result, GateConfig{MinScoreGain: 0.1})
	require.NoError(t, err)
	dir := t.TempDir()
	require.NoError(t, WriteReports(dir, report))
	assert.FileExists(t, filepath.Join(dir, "optimization_report.json"))
	assert.FileExists(t, filepath.Join(dir, "optimization_report.md"))
	promptPath := filepath.Join(dir, "accepted_prompt.txt")
	require.NoError(t, WriteAcceptedPrompt(promptPath, "agent#instruction", report))
	payload, err := os.ReadFile(promptPath)
	require.NoError(t, err)
	assert.Equal(t, "candidate", string(payload))
}

func TestWriteAcceptedPromptRejectsRejectedCandidate(t *testing.T) {
	err := WriteAcceptedPrompt(filepath.Join(t.TempDir(), "prompt.txt"), "surface", &Report{})
	require.ErrorContains(t, err, "no accepted profile")
}

func TestSummarizeUsageCountsTraceSteps(t *testing.T) {
	result := testEvaluation(1)
	result.EvalSets[0].Cases[0].Trace = &atrace.Trace{Steps: []atrace.Step{
		{NodeType: "llm", Usage: &model.Usage{TotalTokens: 12}},
		{NodeType: "tool"},
	}}
	assert.Equal(t, UsageSummary{ModelCalls: 1, ToolCalls: 1, Tokens: 12}, SummarizeUsage(result))
}

func runResult(baseline, candidate float64, engineAccepted bool) *promptiterengine.RunResult {
	return &promptiterengine.RunResult{
		Status: promptiterengine.RunStatusSucceeded, BaselineValidation: testEvaluation(baseline),
		Rounds: []promptiterengine.RoundResult{{
			Round: 1, Validation: testEvaluation(candidate), OutputProfile: profile("candidate"),
			Acceptance: &promptiterengine.AcceptanceDecision{Accepted: engineAccepted},
		}},
	}
}

func testEvaluation(score float64) *promptiterengine.EvaluationResult {
	evalStatus := status.EvalStatusFailed
	if score >= 0.6 {
		evalStatus = status.EvalStatusPassed
	}
	return &promptiterengine.EvaluationResult{OverallScore: score, EvalSets: []promptiterengine.EvalSetResult{{
		EvalSetID: "validation", OverallScore: score,
		Cases: []promptiterengine.CaseResult{{EvalSetID: "validation", EvalCaseID: "case-1", Metrics: []promptiterengine.MetricResult{{MetricName: "quality", Score: score, Status: evalStatus}}}},
	}}}
}

func profile(text string) *promptiter.Profile {
	return &promptiter.Profile{StructureID: "structure", Overrides: []promptiter.SurfaceOverride{{
		SurfaceID: "agent#instruction", Value: astructure.SurfaceValue{Text: &text},
	}}}
}
