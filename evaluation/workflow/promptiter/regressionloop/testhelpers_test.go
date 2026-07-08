//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regressionloop

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fixedClock struct {
	ticks []time.Time
	i     int
}

func (c *fixedClock) Now() time.Time {
	if c.i >= len(c.ticks) {
		return c.ticks[len(c.ticks)-1]
	}
	tick := c.ticks[c.i]
	c.i++
	return tick
}

type fakeEvaluator struct {
	calls   []EvaluationRequest
	results map[string]EvaluationSummary
}

func (e *fakeEvaluator) Evaluate(ctx context.Context, req EvaluationRequest) (EvaluationSummary, error) {
	e.calls = append(e.calls, req)
	key := phaseKey(req.Phase, req.Round)
	result := e.results[key]
	result.EvalSetID = req.EvalSet.ID
	return result, nil
}

type fakeOptimizer struct {
	candidates []Candidate
}

func (o fakeOptimizer) Candidates(ctx context.Context, req OptimizationRequest) ([]Candidate, error) {
	return o.candidates, nil
}

func phaseKey(phase Phase, round int) string {
	return string(phase) + ":" + string(rune('0'+round))
}

func testConfig(t *testing.T) Config {
	t.Helper()
	dir := t.TempDir()
	prompt := filepath.Join(dir, "baseline_prompt.txt")
	require.NoError(t, os.WriteFile(prompt, []byte("baseline"), 0o644))
	return Config{
		AppName: "test-app",
		Seed:    42,
		PromptSource: PromptSource{
			ID:         "prompt",
			Path:       prompt,
			TargetType: PromptTargetAgentInstruction,
			SurfaceID:  "agent#instruction",
		},
		TrainEvalSet:      EvalSetRef{ID: "train", Path: filepath.Join(dir, "train.evalset.json")},
		ValidationEvalSet: EvalSetRef{ID: "validation", Path: filepath.Join(dir, "validation.evalset.json")},
		Metrics:           MetricsRef{Path: filepath.Join(dir, "metrics.json")},
		PromptIter:        PromptIterConfig{MaxRounds: 3, TargetSurfaceIDs: []string{"agent#instruction"}},
		Gate: GatePolicy{
			MinValidationScoreGain:  0.05,
			AllowNewHardFails:       false,
			BlockCriticalRegression: true,
			MaxCalls:                20,
			MaxLatencyMS:            1000,
		},
		Runner: RunnerConfig{Mode: RunnerModeFake, Deterministic: true},
		Output: OutputConfig{
			Dir:            dir,
			JSONReport:     filepath.Join(dir, "optimization_report.json"),
			MarkdownReport: filepath.Join(dir, "optimization_report.md"),
		},
	}
}

func evalSummary(score float64, cases ...CaseResult) EvaluationSummary {
	status := "passed"
	for _, c := range cases {
		if !c.Passed {
			status = "failed"
			break
		}
	}
	return EvaluationSummary{
		Score:   score,
		Status:  status,
		Cases:   cases,
		Cost:    CostSummary{Calls: len(cases), EstimatedCost: float64(len(cases)) * 0.001},
		Latency: LatencySummary{TotalMS: int64(len(cases) * 10)},
	}
}

func caseResult(id string, score float64, passed bool) CaseResult {
	return CaseResult{
		EvalID:   id,
		Score:    score,
		Passed:   passed,
		HardFail: !passed && score == 0,
		MetricResults: []MetricResult{{
			Name:     "quality",
			Score:    score,
			Passed:   passed,
			HardFail: !passed && score == 0,
			Reason:   "final response mismatch",
		}},
		FinalResponse:    "actual",
		ExpectedResponse: "expected",
	}
}
