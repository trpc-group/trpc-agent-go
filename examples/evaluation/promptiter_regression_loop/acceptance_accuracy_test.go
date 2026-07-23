//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"math"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGateDecisionReferenceAccuracy(t *testing.T) {
	type truthCase struct {
		name   string
		mutate func(*gateReferenceFixture)
		accept bool
	}
	costAtLimit := 0.5
	costOverLimit := 0.6
	cases := []truthCase{
		{name: "clear improvement", accept: true},
		{name: "gain exactly at threshold", mutate: func(f *gateReferenceFixture) { f.cfg.MinValidationGain = 0.3 }, accept: true},
		{name: "no train gain but validation improves", mutate: noTrainGain, accept: true},
		{name: "model calls exactly at budget", mutate: func(f *gateReferenceFixture) { f.accounting.ModelCalls = f.budget.MaxModelCalls }, accept: true},
		{name: "tokens exactly at budget", mutate: func(f *gateReferenceFixture) {
			f.accounting.TotalTokens = f.budget.MaxTotalTokens
			f.accounting.PromptTokens = f.budget.MaxTotalTokens - f.accounting.CompletionTokens
		}, accept: true},
		{name: "latency exactly at budget", mutate: func(f *gateReferenceFixture) { f.accounting.WallLatencyMS = f.budget.MaxLatencyMS }, accept: true},
		{name: "known cost exactly at budget", mutate: func(f *gateReferenceFixture) { f.budget.MaxCost = &costAtLimit; f.accounting.Cost = &costAtLimit }, accept: true},
		{name: "validation gain below threshold", mutate: func(f *gateReferenceFixture) { f.cfg.MinValidationGain = 0.31 }, accept: false},
		{name: "validation regression", mutate: validationRegression, accept: false},
		{name: "new hard fail", mutate: newHardFail, accept: false},
		{name: "critical case regression", mutate: criticalRegression, accept: false},
		{name: "single metric regression", mutate: metricRegression, accept: false},
		{name: "train improves validation regresses", mutate: validationRegression, accept: false},
		{name: "generalization gap exceeded", mutate: func(f *gateReferenceFixture) { f.cfg.MaxGeneralizationGap = 0.49 }, accept: false},
		{name: "candidate missing case metric", mutate: removeCandidateMetric, accept: false},
		{name: "candidate unexpected case metric", mutate: addUnexpectedMetric, accept: false},
		{name: "train dataset provenance mismatch", mutate: func(f *gateReferenceFixture) { f.candidateTrain.Identity.DatasetHash = "wrong" }, accept: false},
		{name: "validation dataset provenance mismatch", mutate: func(f *gateReferenceFixture) { f.candidateValidation.Identity.DatasetHash = "wrong" }, accept: false},
		{name: "metric provenance mismatch", mutate: func(f *gateReferenceFixture) { f.candidateValidation.Identity.MetricsHash = "wrong" }, accept: false},
		{name: "candidate profile mismatch across splits", mutate: func(f *gateReferenceFixture) { f.candidateValidation.Identity.ProfileHash = "wrong" }, accept: false},
		{name: "candidate profile unchanged", mutate: func(f *gateReferenceFixture) {
			f.candidateTrain.Identity.ProfileHash = f.baselineTrain.Identity.ProfileHash
			f.candidateValidation.Identity.ProfileHash = f.baselineValidation.Identity.ProfileHash
		}, accept: false},
		{name: "seed mismatch", mutate: func(f *gateReferenceFixture) { f.candidateValidation.Identity.Seed = 999 }, accept: false},
		{name: "wrong train split", mutate: func(f *gateReferenceFixture) { f.candidateTrain.Identity.Split = "validation" }, accept: false},
		{name: "wrong evalset", mutate: func(f *gateReferenceFixture) { f.candidateTrain.Identity.EvalSetID = "wrong" }, accept: false},
		{name: "empty evaluation run id", mutate: func(f *gateReferenceFixture) { f.candidateTrain.Identity.EvaluationRunID = "" }, accept: false},
		{name: "reused train evaluation run", mutate: func(f *gateReferenceFixture) {
			f.candidateTrain.Identity.EvaluationRunID = f.baselineTrain.Identity.EvaluationRunID
		}, accept: false},
		{name: "reused cross split evaluation run", mutate: func(f *gateReferenceFixture) {
			f.candidateValidation.Identity.EvaluationRunID = f.baselineTrain.Identity.EvaluationRunID
		}, accept: false},
		{name: "model call budget exceeded", mutate: func(f *gateReferenceFixture) { f.accounting.ModelCalls = f.budget.MaxModelCalls + 1 }, accept: false},
		{name: "token budget exceeded", mutate: func(f *gateReferenceFixture) { f.accounting.TotalTokens = f.budget.MaxTotalTokens + 1 }, accept: false},
		{name: "latency budget exceeded", mutate: func(f *gateReferenceFixture) { f.accounting.WallLatencyMS = f.budget.MaxLatencyMS + 1 }, accept: false},
		{name: "configured cost unknown", mutate: func(f *gateReferenceFixture) { f.budget.MaxCost = &costAtLimit }, accept: false},
		{name: "known cost exceeded", mutate: func(f *gateReferenceFixture) { f.budget.MaxCost = &costAtLimit; f.accounting.Cost = &costOverLimit }, accept: false},
		{name: "non finite overall score", mutate: func(f *gateReferenceFixture) { f.candidateValidation.Cases[0].Metrics[0].Score = math.NaN() }, accept: false},
		{name: "non finite metric score", mutate: func(f *gateReferenceFixture) { f.candidateValidation.Cases[0].Metrics[0].Score = math.Inf(1) }, accept: false},
		{name: "duplicate case metric", mutate: duplicateCandidateMetric, accept: false},
		{name: "empty metric identity", mutate: func(f *gateReferenceFixture) { f.candidateValidation.Cases[0].Metrics[0].MetricName = "" }, accept: false},
		{name: "unevaluated metric", mutate: func(f *gateReferenceFixture) {
			f.candidateValidation.Cases[0].Metrics[0].Status = "not_evaluated"
		}, accept: false},
		{name: "negative accounting", mutate: func(f *gateReferenceFixture) { f.accounting.TotalTokens = -1 }, accept: false},
		{name: "accounting token components mismatch", mutate: func(f *gateReferenceFixture) { f.accounting.PromptTokens = 49 }, accept: false},
		{name: "accounting call ledger mismatch", mutate: func(f *gateReferenceFixture) {
			f.accounting.ByStage = []modelCallRecord{{Stage: "one", Model: "fake", PromptTokens: 25, CompletionTokens: 25, TotalTokens: 50}}
		}, accept: false},
		{name: "negative cost", mutate: func(f *gateReferenceFixture) {
			f.accounting.Cost = &costAtLimit
			negative := -0.1
			f.accounting.Cost = &negative
			f.budget.MaxCost = &costAtLimit
		}, accept: false},
	}

	correct := 0
	for _, test := range cases {
		fixture := newGateReferenceFixture()
		if test.mutate != nil {
			test.mutate(fixture)
		}
		decision := fixture.decision(t)
		if decision.Accepted == test.accept {
			correct++
			continue
		}
		t.Errorf("%s: accepted=%t, want %t, reasons=%v", test.name, decision.Accepted, test.accept, decision.ReasonCodes)
	}
	accuracy := float64(correct) / float64(len(cases))
	t.Logf("gate decision reference accuracy: %.2f%% (%d/%d)", accuracy*100, correct, len(cases))
	require.GreaterOrEqual(t, accuracy, 0.80)
}

func TestGateSafetyProperties(t *testing.T) {
	rng := rand.New(rand.NewSource(2003))
	for index := 0; index < 128; index++ {
		fixture := newGateReferenceFixture()
		switch rng.Intn(6) {
		case 0:
			quality := findReferenceMetric(fixture.candidateValidation, "validation-quality", "quality")
			quality.Score = 0.1 + rng.Float64()*0.25
			quality.Status = "failed"
		case 1:
			fixture.candidateValidation.Cases[0].Metrics = nil
		case 2:
			fixture.candidateTrain.Identity.DatasetHash = "sha256:wrong"
		case 3:
			fixture.candidateValidation.Cases[0].Metrics[0].Score = math.NaN()
		case 4:
			fixture.accounting.ModelCalls = fixture.budget.MaxModelCalls + 1
		case 5:
			fixture.candidateValidation.Identity.Split = "train"
		}
		decision := fixture.decision(t)
		require.False(t, decision.Accepted, "mutation %d unexpectedly accepted: %v", index, decision.ReasonCodes)
	}
}

func TestAttributionReferenceAccuracy(t *testing.T) {
	type truthCase struct {
		name     string
		evidence caseEvidence
		metric   metricEvidence
		want     string
	}
	cases := make([]truthCase, 0, 32)
	for _, message := range []string{"model timeout", "tool execution failed", "runner canceled", "evaluation crashed"} {
		cases = append(cases, truthCase{name: "execution: " + message, evidence: caseEvidence{Status: "failed", ErrorMessage: message}, metric: metricEvidence{}, want: "execution_error"})
	}
	for _, message := range []string{"wrong branch selected", "router selected billing", "route target mismatch", "router transfer failed"} {
		cases = append(cases, truthCase{name: "route: " + message, evidence: caseEvidence{Status: "failed", Trace: traceEvidence{Steps: []traceStepEvidence{{Error: message}}}}, metric: metricEvidence{}, want: "route_error"})
	}
	for _, tools := range []invocationEvidence{
		{ActualTools: nil, ExpectedTools: []toolEvidence{{Name: "weather"}}},
		{ActualTools: []toolEvidence{{Name: "search"}}, ExpectedTools: []toolEvidence{{Name: "weather"}}},
		{ActualTools: []toolEvidence{{Name: "weather"}, {Name: "search"}}, ExpectedTools: []toolEvidence{{Name: "weather"}}},
		{ActualTools: []toolEvidence{{Name: "second"}, {Name: "first"}}, ExpectedTools: []toolEvidence{{Name: "first"}, {Name: "second"}}},
	} {
		cases = append(cases, truthCase{name: "tool selection", evidence: caseEvidence{Status: "failed", Invocations: []invocationEvidence{tools}}, metric: metricEvidence{}, want: "tool_selection_error"})
	}
	for index := range 4 {
		cases = append(cases, truthCase{
			name: "tool argument",
			evidence: caseEvidence{Status: "failed", Invocations: []invocationEvidence{{
				ActualTools:   []toolEvidence{{Name: "lookup", ArgumentsHash: "actual"}},
				ExpectedTools: []toolEvidence{{Name: "lookup", ArgumentsHash: "expected"}},
			}}},
			metric: metricEvidence{Reason: string(rune('a' + index))},
			want:   "tool_argument_error",
		})
	}
	for _, reason := range []string{"json parse failed", "xml schema mismatch", "output format invalid", "FORMAT_COMPLIANCE failed"} {
		cases = append(cases, truthCase{
			name:     "format: " + reason,
			evidence: responseMismatchEvidence(),
			metric:   metricEvidence{Reason: reason},
			want:     "output_format_error",
		})
	}
	for _, reason := range []string{"retrieval returned no evidence", "knowledge context missing", "document recall was insufficient", "grounding evidence absent"} {
		cases = append(cases, truthCase{
			name:     "retrieval: " + reason,
			evidence: responseMismatchEvidence(),
			metric:   metricEvidence{Reason: reason},
			want:     "knowledge_retrieval_insufficient",
		})
	}
	for _, reason := range []string{"final response mismatch", "text mismatch", "wrong answer", "reference answer differs"} {
		cases = append(cases, truthCase{name: "response: " + reason, evidence: responseMismatchEvidence(), metric: metricEvidence{Reason: reason}, want: "final_response_mismatch"})
	}
	for _, reason := range []string{"score below threshold", "rubric item failed", "unknown evaluator failure", ""} {
		cases = append(cases, truthCase{name: "unclassified: " + reason, evidence: caseEvidence{Status: "failed"}, metric: metricEvidence{Reason: reason}, want: "unclassified_failure"})
	}
	cases = append(cases,
		truthCase{name: "execution Chinese", evidence: caseEvidence{Status: "failed", Trace: traceEvidence{Steps: []traceStepEvidence{{Error: "工具执行失败"}}}}, metric: metricEvidence{}, want: "execution_error"},
		truthCase{name: "route Chinese", evidence: caseEvidence{Status: "failed"}, metric: metricEvidence{Reason: "路由错误"}, want: "route_error"},
		truthCase{name: "format Chinese", evidence: responseMismatchEvidence(), metric: metricEvidence{Reason: "结构化输出格式错误"}, want: "output_format_error"},
		truthCase{name: "retrieval Chinese", evidence: responseMismatchEvidence(), metric: metricEvidence{Reason: "知识召回不足"}, want: "knowledge_retrieval_insufficient"},
		truthCase{name: "response Chinese", evidence: responseMismatchEvidence(), metric: metricEvidence{Reason: "最终回复不匹配"}, want: "final_response_mismatch"},
		truthCase{name: "route beats tool selection", evidence: caseEvidence{Status: "failed", Invocations: []invocationEvidence{{ActualTools: []toolEvidence{{Name: "wrong"}}, ExpectedTools: []toolEvidence{{Name: "right"}}}}, Trace: traceEvidence{Steps: []traceStepEvidence{{Error: "wrong branch selected by router"}}}}, metric: metricEvidence{}, want: "route_error"},
	)

	correct := 0
	confusion := make(map[string]map[string]int)
	for _, test := range cases {
		got, _, _, _ := classifyFailure(test.evidence, test.metric)
		if confusion[test.want] == nil {
			confusion[test.want] = make(map[string]int)
		}
		confusion[test.want][got]++
		if got == test.want {
			correct++
		} else {
			t.Errorf("%s: category=%q, want %q", test.name, got, test.want)
		}
	}
	accuracy := float64(correct) / float64(len(cases))
	t.Logf("attribution reference accuracy: %.2f%% (%d/%d), confusion=%v", accuracy*100, correct, len(cases), confusion)
	require.GreaterOrEqual(t, accuracy, 0.75)
}

type gateReferenceFixture struct {
	cfg                                     gateConfig
	budget                                  budgetConfig
	baselineTrain, candidateTrain           *evaluationSnapshot
	baselineValidation, candidateValidation *evaluationSnapshot
	accounting                              accountingSummary
}

func newGateReferenceFixture() *gateReferenceFixture {
	baselineTrain := acceptanceSnapshot("train", "baseline-train", "baseline", map[string]map[string]metricEvidence{
		"train-quality": {"quality": {MetricName: "quality", Score: 0.2, Threshold: 0.5, Status: "failed"}},
	})
	candidateTrain := acceptanceSnapshot("train", "candidate-train", "candidate", map[string]map[string]metricEvidence{
		"train-quality": {"quality": {MetricName: "quality", Score: 1, Threshold: 0.5, Status: "passed"}},
	})
	baselineValidation := acceptanceSnapshot("validation", "baseline-validation", "baseline", map[string]map[string]metricEvidence{
		"validation-quality": {"quality": {MetricName: "quality", Score: 0.4, Threshold: 0.5, Status: "failed"}},
		"validation-safety":  {"safety": {MetricName: "safety", Score: 1, Threshold: 1, Status: "passed"}},
	})
	candidateValidation := acceptanceSnapshot("validation", "candidate-validation", "candidate", map[string]map[string]metricEvidence{
		"validation-quality": {"quality": {MetricName: "quality", Score: 1, Threshold: 0.5, Status: "passed"}},
		"validation-safety":  {"safety": {MetricName: "safety", Score: 1, Threshold: 1, Status: "passed"}},
	})
	return &gateReferenceFixture{
		cfg: gateConfig{
			MinValidationGain:       0.2,
			HardMetrics:             []string{"safety"},
			MaxMetricRegression:     0,
			MaxGeneralizationGap:    0.6,
			RequireCompleteMatrix:   true,
			RejectUnexpectedMetrics: true,
		},
		budget:        budgetConfig{MaxModelCalls: 10, MaxTotalTokens: 100, MaxLatencyMS: 1000},
		baselineTrain: baselineTrain, candidateTrain: candidateTrain,
		baselineValidation: baselineValidation, candidateValidation: candidateValidation,
		accounting: accountingSummary{ModelCalls: 5, PromptTokens: 25, CompletionTokens: 25, TotalTokens: 50, WallLatencyMS: 500},
	}
}

func (f *gateReferenceFixture) decision(t *testing.T) gateDecision {
	t.Helper()
	recalculateSnapshot(f.baselineTrain)
	recalculateSnapshot(f.candidateTrain)
	recalculateSnapshot(f.baselineValidation)
	recalculateSnapshot(f.candidateValidation)
	trainDelta, err := compareSnapshots(f.baselineTrain, f.candidateTrain)
	require.NoError(t, err)
	validationDelta, err := compareSnapshots(f.baselineValidation, f.candidateValidation)
	require.NoError(t, err)
	return evaluateGate(f.cfg, f.budget, f.baselineTrain, f.candidateTrain, f.baselineValidation, f.candidateValidation, trainDelta, validationDelta, f.accounting)
}

func acceptanceSnapshot(split, runID, profileHash string, cases map[string]map[string]metricEvidence) *evaluationSnapshot {
	snapshot := &evaluationSnapshot{Identity: snapshotIdentity{
		EvaluationRunID: runID,
		Split:           split,
		EvalSetID:       "reference-" + split,
		DatasetHash:     "dataset-" + split,
		MetricsHash:     "reference-metrics",
		ProfileHash:     profileHash,
	}}
	for caseID, metrics := range cases {
		evalCase := caseEvidence{CaseID: caseID}
		for _, metric := range metrics {
			evalCase.Metrics = append(evalCase.Metrics, metric)
		}
		snapshot.Cases = append(snapshot.Cases, evalCase)
	}
	recalculateSnapshot(snapshot)
	return snapshot
}

func recalculateSnapshot(snapshot *evaluationSnapshot) {
	if snapshot == nil {
		return
	}
	total := 0.0
	count := 0
	for _, evalCase := range snapshot.Cases {
		for _, metric := range evalCase.Metrics {
			total += metric.Score
			count++
		}
	}
	if count > 0 {
		snapshot.OverallScore = total / float64(count)
	}
}

func findReferenceMetric(snapshot *evaluationSnapshot, caseID, metricName string) *metricEvidence {
	for caseIndex := range snapshot.Cases {
		if snapshot.Cases[caseIndex].CaseID != caseID {
			continue
		}
		for metricIndex := range snapshot.Cases[caseIndex].Metrics {
			if snapshot.Cases[caseIndex].Metrics[metricIndex].MetricName == metricName {
				return &snapshot.Cases[caseIndex].Metrics[metricIndex]
			}
		}
	}
	return nil
}

func noTrainGain(f *gateReferenceFixture) {
	metric := findReferenceMetric(f.candidateTrain, "train-quality", "quality")
	metric.Score, metric.Status = 0.2, "failed"
}

func validationRegression(f *gateReferenceFixture) {
	quality := findReferenceMetric(f.candidateValidation, "validation-quality", "quality")
	safety := findReferenceMetric(f.candidateValidation, "validation-safety", "safety")
	quality.Score, quality.Status = 0.3, "failed"
	safety.Score = 0.9
}

func newHardFail(f *gateReferenceFixture) {
	metric := findReferenceMetric(f.candidateValidation, "validation-safety", "safety")
	metric.Score, metric.Status = 0, "failed"
}

func criticalRegression(f *gateReferenceFixture) {
	f.cfg.CriticalCases = []string{"validation-safety"}
	metric := findReferenceMetric(f.candidateValidation, "validation-safety", "safety")
	metric.Score = 0.9
}

func metricRegression(f *gateReferenceFixture) {
	metric := findReferenceMetric(f.candidateValidation, "validation-safety", "safety")
	metric.Score = 0.9
}

func removeCandidateMetric(f *gateReferenceFixture) {
	f.candidateValidation.Cases = f.candidateValidation.Cases[:1]
}

func addUnexpectedMetric(f *gateReferenceFixture) {
	f.candidateValidation.Cases = append(f.candidateValidation.Cases, caseEvidence{
		CaseID:  "validation-unexpected",
		Metrics: []metricEvidence{{MetricName: "quality", Score: 1, Threshold: 0.5, Status: "passed"}},
	})
}

func duplicateCandidateMetric(f *gateReferenceFixture) {
	f.candidateValidation.Cases[0].Metrics = append(f.candidateValidation.Cases[0].Metrics, f.candidateValidation.Cases[0].Metrics[0])
}

func responseMismatchEvidence() caseEvidence {
	return caseEvidence{Status: "failed", Invocations: []invocationEvidence{{FinalResponse: "actual", ExpectedFinalResponse: "expected"}}}
}
