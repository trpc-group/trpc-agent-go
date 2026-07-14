//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package fakeenv

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter/regressionloop/internal/config"
)

func TestFileInputsDriveActualMetricResults(t *testing.T) {
	baseDir, cfg := copyFixture(t)
	baseline, err := os.ReadFile(filepath.Join(baseDir, cfg.Prompt.SourceFile))
	if err != nil {
		t.Fatal(err)
	}
	environment, err := New(context.Background(), baseDir, string(baseline), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer environment.Close()
	before, err := environment.Evaluator.EvaluateProfile(context.Background(), cfg.Evaluation.ValidationEvalSetID, environment.InitialProfile)
	if err != nil {
		t.Fatal(err)
	}
	if before.OverallScore != 7.0/9.0 {
		t.Fatalf("baseline score = %v, want %v", before.OverallScore, 7.0/9.0)
	}

	path := filepath.Join(baseDir, cfg.Evaluation.ValidationFile)
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatal(err)
	}
	cases := document["evalCases"].([]any)
	conversation := cases[1].(map[string]any)["conversation"].([]any)
	conversation[0].(map[string]any)["finalResponse"].(map[string]any)["content"] = `{"game_id":"202","winner":"Changed","decisive_moment":"fixture edit"}`
	updated, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, updated, 0o600); err != nil {
		t.Fatal(err)
	}

	environment, err = New(context.Background(), baseDir, string(baseline), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer environment.Close()
	after, err := environment.Evaluator.EvaluateProfile(context.Background(), cfg.Evaluation.ValidationEvalSetID, environment.InitialProfile)
	if err != nil {
		t.Fatal(err)
	}
	if after.OverallScore >= before.OverallScore {
		t.Fatalf("edited evalset did not change metric result: before=%v after=%v", before.OverallScore, after.OverallScore)
	}
}

func TestMetricsFileControlsExecutedMetricSet(t *testing.T) {
	baseDir, cfg := copyFixture(t)
	baseline, err := os.ReadFile(filepath.Join(baseDir, cfg.Prompt.SourceFile))
	if err != nil {
		t.Fatal(err)
	}
	environment, err := New(context.Background(), baseDir, string(baseline), cfg)
	if err != nil {
		t.Fatal(err)
	}
	before, err := environment.Evaluator.EvaluateProfile(context.Background(), cfg.Evaluation.ValidationEvalSetID, environment.InitialProfile)
	if err != nil {
		t.Fatal(err)
	}
	if err := environment.Close(); err != nil {
		t.Fatal(err)
	}

	metricsPath := filepath.Join(baseDir, cfg.Evaluation.MetricsFile)
	payload, err := os.ReadFile(metricsPath)
	if err != nil {
		t.Fatal(err)
	}
	var metrics []map[string]any
	if err := json.Unmarshal(payload, &metrics); err != nil {
		t.Fatal(err)
	}
	metrics = metrics[1:]
	updated, err := json.Marshal(metrics)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(metricsPath, updated, 0o600); err != nil {
		t.Fatal(err)
	}

	environment, err = New(context.Background(), baseDir, string(baseline), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer environment.Close()
	after, err := environment.Evaluator.EvaluateProfile(context.Background(), cfg.Evaluation.ValidationEvalSetID, environment.InitialProfile)
	if err != nil {
		t.Fatal(err)
	}
	if after.OverallScore == before.OverallScore {
		t.Fatalf("edited metrics did not change executed score: before=%v after=%v", before.OverallScore, after.OverallScore)
	}
	if len(after.EvalSets[0].Cases[0].Metrics) != 2 {
		t.Fatalf("metric file was not applied: got %d metrics", len(after.EvalSets[0].Cases[0].Metrics))
	}
}

func TestMetricsThresholdChangesPassFail(t *testing.T) {
	baseDir, cfg := copyFixture(t)
	baseline, err := os.ReadFile(filepath.Join(baseDir, cfg.Prompt.SourceFile))
	if err != nil {
		t.Fatal(err)
	}
	metricsPath := filepath.Join(baseDir, cfg.Evaluation.MetricsFile)
	payload, err := os.ReadFile(metricsPath)
	if err != nil {
		t.Fatal(err)
	}
	var metrics []map[string]any
	if err := json.Unmarshal(payload, &metrics); err != nil {
		t.Fatal(err)
	}
	metrics[0]["threshold"] = float64(0)
	updated, err := json.Marshal(metrics)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(metricsPath, updated, 0o600); err != nil {
		t.Fatal(err)
	}
	environment, err := New(context.Background(), baseDir, string(baseline), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer environment.Close()
	result, err := environment.Evaluator.EvaluateProfile(context.Background(), cfg.Evaluation.ValidationEvalSetID, environment.InitialProfile)
	if err != nil {
		t.Fatal(err)
	}
	for _, evalCase := range result.EvalSets[0].Cases {
		for _, metric := range evalCase.Metrics {
			if metric.MetricName == "final_response_exact" && metric.Status != status.EvalStatusPassed {
				t.Fatalf("threshold edit was not used for %s: %#v", evalCase.EvalCaseID, metric)
			}
		}
	}
}

func TestAddingEvalCaseChangesEvaluationAndReportInput(t *testing.T) {
	baseDir, cfg := copyFixture(t)
	baseline, err := os.ReadFile(filepath.Join(baseDir, cfg.Prompt.SourceFile))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(baseDir, cfg.Evaluation.ValidationFile)
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatal(err)
	}
	cases := document["evalCases"].([]any)
	clone := cases[0].(map[string]any)
	encoded, err := json.Marshal(clone)
	if err != nil {
		t.Fatal(err)
	}
	var added map[string]any
	if err := json.Unmarshal(encoded, &added); err != nil {
		t.Fatal(err)
	}
	added["evalId"] = "val-04"
	document["evalCases"] = append(cases, added)
	updated, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, updated, 0o600); err != nil {
		t.Fatal(err)
	}
	environment, err := New(context.Background(), baseDir, string(baseline), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer environment.Close()
	result, err := environment.Evaluator.EvaluateProfile(context.Background(), cfg.Evaluation.ValidationEvalSetID, environment.InitialProfile)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.EvalSets[0].Cases) != 4 {
		t.Fatalf("evalset edit produced %d cases, want 4", len(result.EvalSets[0].Cases))
	}
}

func TestResourceMeterMeasuresPerProfileUsage(t *testing.T) {
	baseDir, cfg := copyFixture(t)
	baseline, err := os.ReadFile(filepath.Join(baseDir, cfg.Prompt.SourceFile))
	if err != nil {
		t.Fatal(err)
	}
	environment, err := New(context.Background(), baseDir, string(baseline), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer environment.Close()
	baselineProfile := environment.InitialProfile
	v1 := "version: v1\nProduce a grounded game recap."
	v1Profile := &promptiter.Profile{Overrides: []promptiter.SurfaceOverride{{SurfaceID: SurfaceID, Value: astructure.SurfaceValue{Text: &v1}}}}

	if _, err := environment.Evaluator.EvaluateProfile(context.Background(), cfg.Evaluation.ValidationEvalSetID, baselineProfile); err != nil {
		t.Fatal(err)
	}
	baselineMeasurement := environment.Evaluator.Measure(cfg.Evaluation.ValidationEvalSetID, baselineProfile)
	if got := environment.Evaluator.Measure(cfg.Evaluation.ValidationEvalSetID, v1Profile); got.Usage.ModelCalls != 0 {
		t.Fatalf("unevaluated v1 profile has measurement %#v", got)
	}
	if _, err := environment.Evaluator.EvaluateProfile(context.Background(), cfg.Evaluation.ValidationEvalSetID, v1Profile); err != nil {
		t.Fatal(err)
	}
	v1Measurement := environment.Evaluator.Measure(cfg.Evaluation.ValidationEvalSetID, v1Profile)
	baselineAfterV1 := environment.Evaluator.Measure(cfg.Evaluation.ValidationEvalSetID, baselineProfile)

	for name, measurement := range map[string]struct {
		caseRuns int
		model    int
		tools    int
		latency  float64
		cost     float64
	}{
		"baseline": {caseRuns: baselineMeasurement.Usage.EvaluationCaseRuns, model: baselineMeasurement.Usage.ModelCalls, tools: baselineMeasurement.Usage.ToolCalls, latency: baselineMeasurement.LatencySeconds, cost: baselineMeasurement.Cost},
		"v1":       {caseRuns: v1Measurement.Usage.EvaluationCaseRuns, model: v1Measurement.Usage.ModelCalls, tools: v1Measurement.Usage.ToolCalls, latency: v1Measurement.LatencySeconds, cost: v1Measurement.Cost},
	} {
		if measurement.caseRuns != 3 || measurement.model != 4 || measurement.tools != 1 {
			t.Errorf("%s counts = cases %d, model %d, tools %d; want 3, 4, 1", name, measurement.caseRuns, measurement.model, measurement.tools)
		}
		if measurement.latency != 0.057 || measurement.cost != 0.0042 {
			t.Errorf("%s deterministic resource values = latency %v, cost %v; want 0.057, 0.0042", name, measurement.latency, measurement.cost)
		}
	}
	if baselineAfterV1 != baselineMeasurement {
		t.Fatalf("v1 evaluation changed baseline measurement: before=%#v after=%#v", baselineMeasurement, baselineAfterV1)
	}
	if baselineMeasurement.Usage.TokenUsageAvailable || v1Measurement.Usage.TokenUsageAvailable {
		t.Fatal("fake evaluator unexpectedly reports token usage")
	}
}

func copyFixture(t *testing.T) (string, *config.Config) {
	t.Helper()
	source := filepath.Join("..", "..", "data", "promptiter-recap-app")
	target := filepath.Join(t.TempDir(), "promptiter-recap-app")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"baseline.prompt.txt", "promptiter.json", "recap.metrics.json", "train.evalset.json", "validation.evalset.json"} {
		payload, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(target, name), payload, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.Load(filepath.Join(target, "promptiter.json"))
	if err != nil {
		t.Fatal(err)
	}
	return target, cfg
}
