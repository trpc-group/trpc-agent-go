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
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestAttributeFailure(t *testing.T) {
	retrievalRequired := true
	expectedCalls := []ToolCall{{Name: "weather", Arguments: map[string]any{"city": "Shenzhen"}}}
	c := EvalCase{
		ExpectedResponse:  "sunny",
		ExpectedToolCalls: expectedCalls,
		ExpectedRoute:     "weather",
		RetrievalRequired: &retrievalRequired,
	}
	tests := []struct {
		name  string
		trace RunTrace
		want  Attribution
	}{
		{"runtime", RunTrace{Error: "boom"}, AttributionRuntime},
		{"route", RunTrace{Route: "chat"}, AttributionRoute},
		{"tool", RunTrace{Route: "weather", ToolCalls: []ToolCall{{Name: "search"}}}, AttributionToolCall},
		{"args", RunTrace{Route: "weather", ToolCalls: []ToolCall{{Name: "weather", Arguments: map[string]any{"city": "Beijing"}}}}, AttributionToolArgs},
		{"format", RunTrace{Route: "weather", ToolCalls: expectedCalls, RetrievalHit: true}, AttributionFormat},
		{"knowledge", RunTrace{Route: "weather", ToolCalls: expectedCalls, FormatValid: true}, AttributionKnowledge},
		{"response", RunTrace{Route: "weather", ToolCalls: expectedCalls, FormatValid: true, RetrievalHit: true}, AttributionFinalResponse},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := AttributeFailure(c, tt.trace, 0)
			if got != tt.want {
				t.Fatalf("got %s want %s", got, tt.want)
			}
		})
	}
}

func TestScoreCaseRuntimeErrorCannotPass(t *testing.T) {
	c := EvalCase{ID: "runtime", ExpectedResponse: "ok"}
	trace := RunTrace{FinalResponse: "ok", FormatValid: true, Error: "runner failed"}
	metrics := MetricsConfig{ResponseWeight: 1, FormatWeight: 1, PassThreshold: 0.5}

	got := ScoreCase(c, trace, metrics)
	if got.Passed || got.Attribution != AttributionRuntime {
		t.Fatalf("runtime failure must not pass: %+v", got)
	}
}

func TestScoreCaseRejectsUnexpectedToolArgs(t *testing.T) {
	c := EvalCase{ID: "args", ExpectedToolCalls: []ToolCall{{Name: "weather", Arguments: map[string]any{"city": "Shenzhen"}}}}
	trace := RunTrace{ToolCalls: []ToolCall{{Name: "weather", Arguments: map[string]any{"city": "Shenzhen", "units": "metric"}}}, FormatValid: true}
	metrics := MetricsConfig{ToolWeight: 1, PassThreshold: 0.75}

	got := ScoreCase(c, trace, metrics)
	if got.Passed || got.Attribution != AttributionToolArgs {
		t.Fatalf("unexpected tool arguments must be rejected: %+v", got)
	}
}

func TestScoreCasePreservesOrderedNestedToolTrajectory(t *testing.T) {
	expected := []ToolCall{
		{Name: "search", Arguments: map[string]any{"limit": float64(2), "filters": map[string]any{"region": "cn"}}},
		{Name: "summarize", Arguments: map[string]any{"ids": []any{"a", "b"}}},
	}
	c := EvalCase{ID: "trajectory", ExpectedResponse: "done", ExpectedToolCalls: expected}
	metrics := MetricsConfig{ResponseWeight: 1, ToolWeight: 1, FormatWeight: 1, PassThreshold: 0.8}

	got := ScoreCase(c, RunTrace{FinalResponse: "done", ToolCalls: expected, FormatValid: true}, metrics)
	if !got.Passed {
		t.Fatalf("complete ordered trajectory should pass: %+v", got)
	}
	reversed := []ToolCall{expected[1], expected[0]}
	got = ScoreCase(c, RunTrace{FinalResponse: "done", ToolCalls: reversed, FormatValid: true}, metrics)
	if got.Passed || got.Attribution != AttributionToolCall {
		t.Fatalf("reordered trajectory must fail: %+v", got)
	}
}

func TestScoreCaseTreatsOmittedAndEmptyToolArgumentsEqually(t *testing.T) {
	c := EvalCase{ID: "no-args", ExpectedToolCalls: []ToolCall{{Name: "ping"}}}
	trace := RunTrace{ToolCalls: []ToolCall{{Name: "ping", Arguments: map[string]any{}}}, FormatValid: true}
	got := ScoreCase(c, trace, MetricsConfig{ToolWeight: 1, PassThreshold: 1})
	if !got.Passed {
		t.Fatalf("semantically empty tool arguments should match: %+v", got)
	}
}

func TestExecutionContractsAreHardRequirements(t *testing.T) {
	retrievalRequired := true
	expectedCalls := []ToolCall{{Name: "weather", Arguments: map[string]any{"city": "Shenzhen"}}}
	c := EvalCase{
		ID:                "critical",
		ExpectedResponse:  "sunny",
		ExpectedToolCalls: expectedCalls,
		ExpectedRoute:     "weather",
		RetrievalRequired: &retrievalRequired,
		Critical:          true,
	}
	metrics := MetricsConfig{ResponseWeight: 0.7, ToolWeight: 0.2, FormatWeight: 0.1, PassThreshold: 0.75}
	baseline := ScoreCase(c, RunTrace{FinalResponse: "sunny", ToolCalls: expectedCalls, Route: "weather", FormatValid: true, RetrievalHit: true}, metrics)
	if !baseline.Passed {
		t.Fatalf("baseline should pass: %+v", baseline)
	}

	tests := []struct {
		name  string
		trace RunTrace
	}{
		{"route", RunTrace{FinalResponse: "sunny", ToolCalls: expectedCalls, Route: "chat", FormatValid: true, RetrievalHit: true}},
		{"retrieval", RunTrace{FinalResponse: "sunny", ToolCalls: expectedCalls, Route: "weather", FormatValid: true}},
		{"format", RunTrace{FinalResponse: "sunny", ToolCalls: expectedCalls, Route: "weather", RetrievalHit: true}},
		{"arguments", RunTrace{FinalResponse: "sunny", ToolCalls: []ToolCall{{Name: "weather", Arguments: map[string]any{"city": "Beijing"}}}, Route: "weather", FormatValid: true, RetrievalHit: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := ScoreCase(c, tt.trace, metrics)
			if candidate.Passed {
				t.Fatalf("execution regression passed weighted scoring: %+v", candidate)
			}
			delta := CompareEvaluations(
				EvaluationResult{OverallScore: baseline.Score, Cases: []CaseResult{baseline}},
				EvaluationResult{OverallScore: candidate.Score, Cases: []CaseResult{candidate}},
			)
			gate := ApplyGate(GateConfig{NoNewHardFails: true}, EvaluationResult{}, EvaluationResult{}, delta)
			if gate.Accepted {
				t.Fatalf("gate accepted execution regression: %+v", gate)
			}
			if !strings.Contains(strings.Join(gate.Reasons, " "), "new hard fail: critical") {
				t.Fatalf("gate did not record the execution contract as a hard fail: %+v", gate)
			}
		})
	}
}

func TestCompareEvaluations(t *testing.T) {
	base := EvaluationResult{OverallScore: .5, Cases: []CaseResult{{CaseID: "a", Score: .4, Passed: false}, {CaseID: "b", Score: .8, Passed: true}}}
	candidate := EvaluationResult{OverallScore: .6, Cases: []CaseResult{{CaseID: "a", Score: .8, Passed: true}, {CaseID: "b", Score: .4, Passed: false}}}
	d := CompareEvaluations(base, candidate)
	if d.NewlyPassed != 1 || d.NewlyFailed != 1 || d.ScoreDelta != .1 {
		t.Fatalf("unexpected delta: %+v", d)
	}
}

func TestGateRejectsOverfitHardRegression(t *testing.T) {
	base := EvaluationResult{OverallScore: .7, TotalCost: 1, ToolCalls: 1, Cases: []CaseResult{{CaseID: "critical", Critical: true, Score: 1, Passed: true}}}
	candidate := EvaluationResult{OverallScore: .8, TotalCost: 1, ToolCalls: 1, Cases: []CaseResult{{CaseID: "critical", Critical: true, Score: .2, Passed: false}}}
	delta := CompareEvaluations(base, candidate)
	maxCostIncrease := 1.0
	maxToolCalls := 2
	gate := ApplyGate(GateConfig{MinValidationGain: .01, NoNewHardFails: true, CriticalCaseIDs: []string{"critical"}, MaxCostIncrease: &maxCostIncrease, MaxToolCalls: &maxToolCalls}, base, candidate, delta)
	if gate.Accepted || !strings.Contains(strings.Join(gate.Reasons, " "), "hard fail") {
		t.Fatalf("expected hard-fail rejection: %+v", gate)
	}
}

func TestConfiguredCriticalCaseRejectsNewFailureWithHigherScore(t *testing.T) {
	base := EvaluationResult{OverallScore: .5, Cases: []CaseResult{{CaseID: "critical", Score: .6, Passed: true}}}
	candidate := EvaluationResult{OverallScore: .7, Cases: []CaseResult{{CaseID: "critical", Score: .8, Passed: false}}}
	delta := CompareEvaluations(base, candidate)
	gate := ApplyGate(GateConfig{MinValidationGain: .1, CriticalCaseIDs: []string{"critical"}}, base, candidate, delta)
	if gate.Accepted || !strings.Contains(strings.Join(gate.Reasons, " "), "critical case newly failed") {
		t.Fatalf("configured critical hard failure was accepted: %+v", gate)
	}
}

func TestGateRejectsNonBijectiveEvaluationCases(t *testing.T) {
	validBase := []CaseResult{{CaseID: "a", Passed: true}, {CaseID: "b", Passed: true}}
	tests := []struct {
		name      string
		baseline  []CaseResult
		candidate []CaseResult
		want      string
	}{
		{"missing", validBase, []CaseResult{{CaseID: "a", Passed: true}}, "missing candidate case: b"},
		{"extra", validBase, append(append([]CaseResult{}, validBase...), CaseResult{CaseID: "c"}), "extra candidate case: c"},
		{"duplicate-candidate", validBase, []CaseResult{{CaseID: "a"}, {CaseID: "a"}, {CaseID: "b"}}, "duplicate candidate case: a"},
		{"duplicate-baseline", []CaseResult{{CaseID: "a"}, {CaseID: "a"}}, []CaseResult{{CaseID: "a"}}, "duplicate baseline case: a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := EvaluationResult{OverallScore: .5, Cases: tt.baseline}
			candidate := EvaluationResult{OverallScore: .8, Cases: tt.candidate}
			delta := CompareEvaluations(base, candidate)
			gate := ApplyGate(GateConfig{MinValidationGain: .1}, base, candidate, delta)
			if gate.Accepted || !strings.Contains(strings.Join(gate.Reasons, " "), tt.want) {
				t.Fatalf("invalid case set was not rejected: delta=%+v gate=%+v", delta, gate)
			}
		})
	}
}

func TestGateRejectsMissingProtectedCaseEvenWithHigherScore(t *testing.T) {
	base := EvaluationResult{
		OverallScore: .7,
		Cases: []CaseResult{
			{CaseID: "critical", Critical: true, Score: .7, Passed: true},
			{CaseID: "normal", Score: .7, Passed: true},
		},
	}
	candidate := EvaluationResult{
		OverallScore: .95,
		Cases:        []CaseResult{{CaseID: "normal", Score: .95, Passed: true}},
	}
	delta := CompareEvaluations(base, candidate)
	gate := ApplyGate(
		GateConfig{
			MinValidationGain: 0.05,
			CriticalCaseIDs:   []string{"critical"},
		},
		base,
		candidate,
		delta,
	)
	if gate.Accepted {
		t.Fatalf("candidate missing critical case should be rejected: %+v", gate)
	}
	if !strings.Contains(strings.Join(gate.Reasons, " "), "invalid evaluation case set: missing candidate case: critical") {
		t.Fatalf("missing critical case was not surfaced as a case-set violation: %+v", gate)
	}
}

func TestGateRejectsCriticalCaseThatFailsEvenWithHigherScore(t *testing.T) {
	base := EvaluationResult{
		OverallScore: .5,
		Cases: []CaseResult{
			{CaseID: "critical", Critical: true, Score: .9, Passed: true},
			{CaseID: "normal", Score: .4, Passed: true},
		},
	}
	candidate := EvaluationResult{
		OverallScore: .8,
		Cases: []CaseResult{
			{CaseID: "critical", Critical: true, Score: .9, Passed: false},
			{CaseID: "normal", Score: .95, Passed: true},
		},
	}
	delta := CompareEvaluations(base, candidate)
	gate := ApplyGate(
		GateConfig{
			MinValidationGain: 0.05,
			CriticalCaseIDs:   []string{"critical"},
		},
		base,
		candidate,
		delta,
	)
	if gate.Accepted {
		t.Fatalf("critical failed case should be rejected: %+v", gate)
	}
	if !strings.Contains(strings.Join(gate.Reasons, " "), "critical case did not pass: critical") {
		t.Fatalf("critical fail reason missing: %+v", gate)
	}
}

func TestGateOmittedBudgetsAreDisabled(t *testing.T) {
	base := EvaluationResult{OverallScore: .5, TotalCost: 1, ToolCalls: 0}
	candidate := EvaluationResult{OverallScore: .7, TotalCost: 3, ToolCalls: 4}
	delta := CompareEvaluations(base, candidate)
	gate := ApplyGate(GateConfig{MinValidationGain: .1}, base, candidate, delta)
	if !gate.Accepted {
		t.Fatalf("omitted budgets must not reject a candidate: %+v", gate)
	}
}

func TestGateRejectsZeroGainConsistently(t *testing.T) {
	base := EvaluationResult{OverallScore: .8}
	candidate := EvaluationResult{OverallScore: .8}
	delta := CompareEvaluations(base, candidate)
	gate := ApplyGate(GateConfig{MinValidationGain: 0}, base, candidate, delta)
	if gate.Accepted {
		t.Fatalf("zero-gain candidate must not be accepted: %+v", gate)
	}
}

func TestDecodeJSONRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "promptiter.json")
	if err := os.WriteFile(path, []byte(`{"seed":1,"engine":{"type":"fake_trace","model":"fixture"},"gate":{"min_validation_gain":0,"misspelled_guard":true},"candidates":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var cfg LoopConfig
	if err := decodeJSONFile(path, &cfg); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown safeguard field was not rejected: %v", err)
	}
}

func TestPipelineAndReport(t *testing.T) {
	p, err := LoadPipeline("data")
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !r.Accepted || r.SelectedCandidate != "focused" || len(r.Rounds) != 3 {
		t.Fatalf("unexpected result: %+v", r)
	}
	if r.Rounds[2].Gate.Accepted {
		t.Fatal("overfit candidate must be rejected")
	}
	encoded, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var restored OptimizationReport
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.Metrics, restored.Metrics) || !reflect.DeepEqual(r.Gate, restored.Gate) {
		t.Fatalf("report did not preserve reproducibility config: before=%+v/%+v after=%+v/%+v", r.Metrics, r.Gate, restored.Metrics, restored.Gate)
	}
	md := MarkdownReport(r)
	if !strings.Contains(md, "new fail") || !strings.Contains(md, "Failure attribution") || !strings.Contains(md, "Reproducibility configuration") {
		t.Fatal("report is incomplete")
	}
}

func TestCheckedInReportExamplesMatchCurrentSchema(t *testing.T) {
	data, err := os.ReadFile("optimization_report.json.example")
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var report OptimizationReport
	if err := decoder.Decode(&report); err != nil {
		t.Fatalf("JSON report example does not match the current schema: %v", err)
	}
	pipeline, err := LoadPipeline("data")
	if err != nil {
		t.Fatal(err)
	}
	actual, err := pipeline.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	actual.StartedAt = report.StartedAt
	actual.DurationMS = report.DurationMS
	for i := range actual.Rounds {
		actual.Rounds[i].DurationMS = report.Rounds[i].DurationMS
	}
	if !reflect.DeepEqual(*actual, report) {
		t.Fatal("JSON report example does not match current deterministic pipeline output")
	}
	markdown, err := os.ReadFile("optimization_report.md.example")
	if err != nil {
		t.Fatal(err)
	}
	if got := MarkdownReport(&report); got != string(markdown) {
		t.Fatal("Markdown report example was not generated from the JSON example")
	}
}

func TestPipelineValidationRejectsAmbiguousIDs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Pipeline)
		want   string
	}{
		{"reserved candidate", func(p *Pipeline) { p.Config.Candidates[0].ID = "baseline" }, "reserved candidate ID"},
		{"duplicate candidate", func(p *Pipeline) { p.Config.Candidates = append(p.Config.Candidates, p.Config.Candidates[0]) }, "duplicate or reserved candidate ID"},
		{"duplicate validation case", func(p *Pipeline) { p.Valid.Cases[1].ID = p.Valid.Cases[0].ID }, "duplicate case ID"},
		{"unknown critical case", func(p *Pipeline) { p.Config.Gate.CriticalCaseIDs = []string{"typo"} }, "is not in the validation set"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pipeline, _ := validPipelineForValidation(t)
			tt.mutate(pipeline)
			if err := pipeline.validate(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("validate() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestPipelineValidationRejectsInvalidNumericConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Pipeline)
		want   string
	}{
		{"negative weight", func(p *Pipeline) { p.Metrics.ToolWeight = -1 }, "weights cannot be negative"},
		{"nonfinite threshold", func(p *Pipeline) { p.Metrics.PassThreshold = math.Inf(1) }, "must be finite"},
		{"overflowing total weight", func(p *Pipeline) {
			p.Metrics.ResponseWeight = 1e308
			p.Metrics.ToolWeight = 1e308
		}, "total metric weight must be finite"},
		{"zero weights", func(p *Pipeline) { p.Metrics.ResponseWeight = 0 }, "at least one metric weight"},
		{"negative gain", func(p *Pipeline) { p.Config.Gate.MinValidationGain = -0.1 }, "min_validation_gain"},
		{"negative cost budget", func(p *Pipeline) { value := -1.0; p.Config.Gate.MaxCostIncrease = &value }, "max_cost_increase"},
		{"negative tool budget", func(p *Pipeline) { value := -1; p.Config.Gate.MaxToolCalls = &value }, "max_tool_calls"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pipeline, _ := validPipelineForValidation(t)
			tt.mutate(pipeline)
			if err := pipeline.validate(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("validate() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestEvaluateRejectsAggregateOverflow(t *testing.T) {
	pipeline := &Pipeline{Metrics: MetricsConfig{ResponseWeight: 1}}
	t.Run("large finite cost", func(t *testing.T) {
		set := EvalSet{ID: "large", Cases: []EvalCase{
			{ID: "one", Runs: map[string]RunTrace{"baseline": {Cost: 1e308}}},
		}}
		result, err := pipeline.Evaluate(context.Background(), set, "baseline")
		if err != nil {
			t.Fatal(err)
		}
		if !finite(result.TotalCost) || result.TotalCost != 1e308 {
			t.Fatalf("large finite cost became %v", result.TotalCost)
		}
	})
	t.Run("cost", func(t *testing.T) {
		set := EvalSet{ID: "cost", Cases: []EvalCase{
			{ID: "one", Runs: map[string]RunTrace{"baseline": {Cost: 1e308}}},
			{ID: "two", Runs: map[string]RunTrace{"baseline": {Cost: 1e308}}},
		}}
		if _, err := pipeline.Evaluate(context.Background(), set, "baseline"); err == nil || !strings.Contains(err.Error(), "overflows total cost") {
			t.Fatalf("Evaluate() cost overflow error = %v", err)
		}
	})
	t.Run("latency", func(t *testing.T) {
		set := EvalSet{ID: "latency", Cases: []EvalCase{
			{ID: "one", Runs: map[string]RunTrace{"baseline": {LatencyMS: math.MaxInt64}}},
			{ID: "two", Runs: map[string]RunTrace{"baseline": {LatencyMS: 1}}},
		}}
		if _, err := pipeline.Evaluate(context.Background(), set, "baseline"); err == nil || !strings.Contains(err.Error(), "overflows total latency") {
			t.Fatalf("Evaluate() latency overflow error = %v", err)
		}
	})
}

func TestPipelineValidationRejectsPromptPathEscape(t *testing.T) {
	pipeline, root := validPipelineForValidation(t)
	outside := filepath.Join(root, "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, escaped := range []string{
		"../outside.txt", `..\outside.txt`, `C:\outside.txt`,
		"/outside.txt", `\\server\share\outside.txt`,
	} {
		t.Run(escaped, func(t *testing.T) {
			copy := *pipeline
			copy.Config = pipeline.Config
			copy.Config.Candidates = append([]CandidateConfig(nil), pipeline.Config.Candidates...)
			copy.Config.Candidates[0].PromptFile = escaped
			if err := copy.validate(); err == nil || !strings.Contains(err.Error(), "prompt file") {
				t.Fatalf("validate() accepted escaped prompt %q: %v", escaped, err)
			}
		})
	}
}

func TestPipelineValidationRejectsPromptSymlinkEscape(t *testing.T) {
	pipeline, root := validPipelineForValidation(t)
	outside := filepath.Join(root, "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(pipeline.Dir, "linked.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	pipeline.Config.Candidates[0].PromptFile = "linked.txt"
	if err := pipeline.validate(); err == nil || !strings.Contains(err.Error(), "escapes the data directory") {
		t.Fatalf("validate() accepted escaped symlink: %v", err)
	}
}

func validPipelineForValidation(t *testing.T) (*Pipeline, string) {
	t.Helper()
	root := t.TempDir()
	data := filepath.Join(root, "data")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"baseline_prompt.txt": "baseline", "candidate.txt": "candidate",
	} {
		if err := os.WriteFile(filepath.Join(data, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	makeSet := func(id, prefix string) EvalSet {
		set := EvalSet{ID: id}
		for i := 1; i <= 3; i++ {
			set.Cases = append(set.Cases, EvalCase{
				ID: fmt.Sprintf("%s-%d", prefix, i),
				Runs: map[string]RunTrace{
					"baseline": {FormatValid: true},
					"focused":  {FormatValid: true},
				},
			})
		}
		return set
	}
	pipeline := &Pipeline{
		Dir:     data,
		Metrics: MetricsConfig{PassThreshold: 0.5, ResponseWeight: 1},
		Config: LoopConfig{
			Engine:     EngineConfig{Type: "fake_trace", Model: "fixture"},
			Gate:       GateConfig{CriticalCaseIDs: []string{"validation-1"}},
			Candidates: []CandidateConfig{{ID: "focused", PromptFile: "candidate.txt"}},
		},
		Train: makeSet("train", "train"),
		Valid: makeSet("validation", "validation"),
	}
	return pipeline, root
}
