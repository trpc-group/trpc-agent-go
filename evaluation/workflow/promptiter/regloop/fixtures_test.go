//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regloop

import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

// metricR builds one metric result fixture.
func metricR(name string, score float64, st status.EvalStatus, reason string) engine.MetricResult {
	return engine.MetricResult{MetricName: name, Score: score, Status: st, Reason: reason}
}

// caseR builds one case result fixture.
func caseR(id string, metrics ...engine.MetricResult) engine.CaseResult {
	return engine.CaseResult{EvalSetID: "validation", EvalCaseID: id, Metrics: metrics}
}

// evalR builds one evaluation result fixture with a single eval set.
func evalR(overall float64, cases ...engine.CaseResult) *engine.EvaluationResult {
	return &engine.EvaluationResult{
		OverallScore: overall,
		EvalSets: []engine.EvalSetResult{
			{EvalSetID: "validation", OverallScore: overall, Cases: cases},
		},
	}
}

// lossRound builds a round with terminal losses of the given severities.
func lossRound(round int, validation *engine.EvaluationResult, accepted bool, delta float64, severities ...promptiter.LossSeverity) engine.RoundResult {
	terminal := make([]promptiter.TerminalLoss, 0, len(severities))
	for _, sev := range severities {
		terminal = append(terminal, promptiter.TerminalLoss{Severity: sev, MetricName: "final_response_avg_score"})
	}
	return engine.RoundResult{
		Round:      round,
		Validation: validation,
		Losses:     []promptiter.CaseLoss{{EvalCaseID: "c1", TerminalLosses: terminal}},
		Acceptance: &engine.AcceptanceDecision{Accepted: accepted, ScoreDelta: delta, Reason: "fixture"},
	}
}
