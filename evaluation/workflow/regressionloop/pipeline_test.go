//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package regressionloop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

// fakeEngine is a deterministic engine.Engine implementation for tests.
type fakeEngine struct {
	result *engine.RunResult
	err    error
	// captured request for assertions
	lastReq *engine.RunRequest
}

func (f *fakeEngine) Describe(ctx context.Context) (*astructure.Snapshot, error) {
	return nil, nil
}

func (f *fakeEngine) Run(
	ctx context.Context,
	request *engine.RunRequest,
	opts ...engine.Option,
) (*engine.RunResult, error) {
	f.lastReq = request
	return f.result, f.err
}

func newFakeEvaluationResult(score float64, setID string, cases ...engine.CaseResult) *engine.EvaluationResult {
	return &engine.EvaluationResult{
		OverallScore: score,
		EvalSets: []engine.EvalSetResult{{
			EvalSetID:    setID,
			OverallScore: score,
			Cases:        cases,
		}},
	}
}

func fakeEngineResult() *engine.RunResult {
	patchText := "improved instruction text"
	return &engine.RunResult{
		Status: engine.RunStatusSucceeded,
		BaselineValidation: newFakeEvaluationResult(0.70, "validation",
			engine.CaseResult{
				EvalCaseID: "v1",
				Metrics:    []engine.MetricResult{{MetricName: "final_response_avg_score", Score: 0.9, Status: status.EvalStatusPassed}},
			},
			engine.CaseResult{
				EvalCaseID: "v2",
				Metrics:    []engine.MetricResult{{MetricName: "final_response_avg_score", Score: 0.4, Status: status.EvalStatusFailed, Reason: "response did not match"}},
			},
		),
		Rounds: []engine.RoundResult{{
			Round:        1,
			InputProfile: &promptiter.Profile{StructureID: "s1"},
			Train: newFakeEvaluationResult(0.80, "train",
				engine.CaseResult{
					EvalCaseID: "t1",
					Metrics:    []engine.MetricResult{{MetricName: "tool_trajectory_avg_score", Score: 0.3, Status: status.EvalStatusFailed, Reason: "agent called wrong tool"}},
				},
				engine.CaseResult{
					EvalCaseID: "t2",
					Metrics:    []engine.MetricResult{{MetricName: "final_response_avg_score", Score: 0.95, Status: status.EvalStatusPassed}},
				},
			),
			Validation: newFakeEvaluationResult(0.78, "validation",
				engine.CaseResult{
					EvalCaseID: "v1",
					Metrics:    []engine.MetricResult{{MetricName: "final_response_avg_score", Score: 0.92, Status: status.EvalStatusPassed}},
				},
			),
			Patches: &promptiter.PatchSet{
				Patches: []promptiter.SurfacePatch{{
					SurfaceID: "agent/instruction",
					Value:     astructure.SurfaceValue{Text: &patchText},
					Reason:    "fix tool selection",
				}},
			},
			Acceptance: &engine.AcceptanceDecision{Accepted: true, ScoreDelta: 0.08, Reason: "gain ok"},
		}},
	}
}

func TestPipelineRun(t *testing.T) {
	eng := &fakeEngine{result: fakeEngineResult()}
	p := NewPipeline(eng, PipelineConfig{
		MaxRounds:     2,
		TrainEvalSets: []string{"train"},
		ValEvalSets:   []string{"validation"},
	})

	res, err := p.Run(context.Background(), &RunRequest{
		TrainEvalSets:      []string{"train"},
		ValidationEvalSets: []string{"validation"},
		MaxRounds:          2,
		CostPerToken:       0.00001,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Report == nil {
		t.Fatal("expected report")
	}

	// Engine request propagation.
	if eng.lastReq == nil {
		t.Fatal("engine was not called")
	}
	if len(eng.lastReq.Train) != 1 || eng.lastReq.Train[0].EvalSetID != "train" {
		t.Errorf("train inputs not propagated: %+v", eng.lastReq.Train)
	}
	if eng.lastReq.MaxRounds != 2 {
		t.Errorf("max rounds not propagated: %d", eng.lastReq.MaxRounds)
	}

	// Report content.
	report := res.Report
	if report.Baseline == nil || report.Baseline.TotalCases != 2 {
		t.Errorf("baseline summary wrong: %+v", report.Baseline)
	}
	if report.Candidate == nil || report.Candidate.TotalCases != 1 {
		t.Errorf("candidate summary wrong: %+v", report.Candidate)
	}
	if len(report.Rounds) != 1 {
		t.Fatalf("expected 1 round audit, got %d", len(report.Rounds))
	}
	round := report.Rounds[0]
	if !round.Accepted {
		t.Error("round should be accepted")
	}
	if round.TrainScore != 0.80 || round.ValidationScore != 0.78 {
		t.Errorf("round scores wrong: %v / %v", round.TrainScore, round.ValidationScore)
	}
	if len(round.PatchesApplied) != 1 || round.PatchesApplied[0].SurfaceID != "agent/instruction" {
		t.Errorf("patches not audited: %+v", round.PatchesApplied)
	}
	if len(round.FailureAttributions) == 0 {
		t.Error("expected failure attributions for failed train case")
	}

	// Gate decision must exist and be accepted (validation improved).
	if report.GateDecision == nil {
		t.Fatal("expected gate decision")
	}
	if !report.GateDecision.Accepted {
		t.Errorf("expected accepted, got: %s", report.GateDecision.Summary)
	}
	if !res.Accepted {
		t.Error("run result accepted should be true")
	}
}

func TestPipelineRunEngineError(t *testing.T) {
	eng := &fakeEngine{err: errors.New("engine exploded")}
	p := NewPipeline(eng, PipelineConfig{})

	_, err := p.Run(context.Background(), &RunRequest{})
	if err == nil {
		t.Fatal("expected error from engine failure")
	}
	if !strings.Contains(err.Error(), "promptiter engine run") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPipelineRunOverfittingRound(t *testing.T) {
	result := fakeEngineResult()
	// Train improves a lot but validation degrades below baseline (0.70).
	result.Rounds[0].Train = newFakeEvaluationResult(0.95, "train",
		engine.CaseResult{
			EvalCaseID: "t1",
			Metrics:    []engine.MetricResult{{MetricName: "m", Score: 0.95, Status: status.EvalStatusPassed}},
		},
	)
	result.Rounds[0].Validation = newFakeEvaluationResult(0.60, "validation",
		engine.CaseResult{
			EvalCaseID: "v1",
			Metrics:    []engine.MetricResult{{MetricName: "m", Score: 0.6, Status: status.EvalStatusFailed, Reason: "response did not match"}},
		},
	)

	eng := &fakeEngine{result: result}
	p := NewPipeline(eng, PipelineConfig{})

	res, err := p.Run(context.Background(), &RunRequest{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(res.Report.Rounds) != 1 {
		t.Fatalf("expected 1 round, got %d", len(res.Report.Rounds))
	}
	if !res.Report.Rounds[0].OverfittingDetected {
		t.Error("expected overfitting detection on train-up/val-down round")
	}
	if res.Accepted {
		t.Error("overfit candidate must not be accepted")
	}
}

func TestPipelineRunEmptyRounds(t *testing.T) {
	result := fakeEngineResult()
	result.Rounds = nil
	eng := &fakeEngine{result: result}
	p := NewPipeline(eng, PipelineConfig{})

	res, err := p.Run(context.Background(), &RunRequest{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(res.Report.Rounds) != 0 {
		t.Errorf("expected no rounds, got %d", len(res.Report.Rounds))
	}
	if res.Report.Candidate != nil {
		t.Errorf("expected nil candidate without rounds, got %+v", res.Report.Candidate)
	}
}

func TestNewPipelineDefaultGate(t *testing.T) {
	p := NewPipeline(&fakeEngine{}, PipelineConfig{})
	if p.Config.GateConfig.MinScoreGain <= 0 {
		t.Error("expected default gate config to be applied")
	}
	if !p.Config.GateConfig.NoNewHardFailures {
		t.Error("expected default no-new-hard-failures rule")
	}
}

func TestGenerateReports(t *testing.T) {
	report := BuildOptimizationReport(
		PipelineConfig{},
		&RoundSummary{OverallScore: 0.7, TotalCases: 2, PassCount: 1, FailCount: 1},
		&RoundSummary{OverallScore: 0.8, TotalCases: 2, PassCount: 2, FailCount: 0},
		EvalRunSummary{}, EvalRunSummary{}, nil, nil, nil, nil,
		CostSummary{RoundsRun: 1},
	)

	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "report.json")
	mdPath := filepath.Join(dir, "report.md")
	if err := GenerateReports(jsonPath, mdPath, report); err != nil {
		t.Fatalf("generate reports: %v", err)
	}
	for _, path := range []string{jsonPath, mdPath} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("missing %s: %v", path, err)
		}
		if info.Size() == 0 {
			t.Errorf("empty report file %s", path)
		}
	}
}

func TestGenerateReportsJSONError(t *testing.T) {
	report := &OptimizationReport{}
	dir := t.TempDir()
	// A directory path cannot be written as a file.
	err := GenerateReports(dir, filepath.Join(dir, "r.md"), report)
	if err == nil || !strings.Contains(err.Error(), "write JSON report") {
		t.Errorf("expected JSON write error, got: %v", err)
	}
}

func TestWriteMarkdownReportError(t *testing.T) {
	report := &OptimizationReport{}
	err := WriteMarkdownReport(t.TempDir(), report)
	if err == nil {
		t.Error("expected markdown write error for directory path")
	}
}

func TestGenerateReportsMarkdownError(t *testing.T) {
	report := &OptimizationReport{}
	dir := t.TempDir()
	// JSON path is valid, Markdown path points at a directory so the
	// second write fails after the first succeeds.
	err := GenerateReports(filepath.Join(dir, "r.json"), dir, report)
	if err == nil || !strings.Contains(err.Error(), "write Markdown report") {
		t.Errorf("expected Markdown write error, got: %v", err)
	}
}

func TestBuildRoundSummaryNil(t *testing.T) {
	if buildRoundSummary(nil) != nil {
		t.Error("expected nil summary for nil result")
	}
}

func TestEvalResultToRunSummaryNil(t *testing.T) {
	s := evalResultToRunSummary(nil)
	if s.CaseScores != nil || s.CaseCount != 0 {
		t.Errorf("expected empty summary, got %+v", s)
	}
}

func TestEvalResultToCaseResultsNil(t *testing.T) {
	if evalResultToCaseResults(nil) != nil {
		t.Error("expected nil case results for nil input")
	}
}

func TestEvalHelpersEmptyMetrics(t *testing.T) {
	// Cases without metrics must be counted as passed with no score entry.
	result := newFakeEvaluationResult(0.5, "set", engine.CaseResult{EvalCaseID: "c1"})
	summary := evalResultToRunSummary(result)
	if summary.CaseStatuses["set/c1"] != "passed" {
		t.Errorf("empty-metric case should count as passed: %+v", summary.CaseStatuses)
	}
	if _, ok := summary.CaseScores["set/c1"]; ok {
		t.Error("empty-metric case should have no score entry")
	}

	crs := evalResultToCaseResults(result)
	if len(crs) != 1 || crs[0].EvalCaseID != "c1" || len(crs[0].Metrics) != 0 {
		t.Errorf("unexpected case results: %+v", crs)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Errorf("short string changed: %q", got)
	}
	long := strings.Repeat("a", 300)
	got := truncate(long, 200)
	if len(got) != 203 || !strings.HasSuffix(got, "...") {
		t.Errorf("truncate wrong: len=%d", len(got))
	}
}

func TestBuildOptimizationReportDefaults(t *testing.T) {
	report := BuildOptimizationReport(
		PipelineConfig{},
		&RoundSummary{OverallScore: 0.7},
		&RoundSummary{OverallScore: 0.75},
		EvalRunSummary{OverallScore: 0.7, CaseScores: map[string]float64{}, CaseStatuses: map[string]string{}},
		EvalRunSummary{OverallScore: 0.75, CaseScores: map[string]float64{}, CaseStatuses: map[string]string{}},
		nil, nil, nil, nil, CostSummary{},
	)
	if report.GateDecision == nil {
		t.Fatal("expected gate decision")
	}
	if !report.GateDecision.Accepted {
		t.Errorf("expected accepted with default gate: %s", report.GateDecision.Summary)
	}
	if report.Timestamp.IsZero() {
		t.Error("expected timestamp to be set")
	}
}
