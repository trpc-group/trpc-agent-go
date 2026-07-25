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
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/evaluation"

	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter_regression_loop/fakemodel"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter_regression_loop/pipeline"
)

// evalSnapshot bundles the full per-case evaluation results for the train and validation sets at a
// single point in the pipeline (baseline or candidate). Both are captured with run details enabled
// so downstream attribution and gating can see per-case metric outcomes.
type evalSnapshot struct {
	train      *evaluation.EvaluationResult
	validation *evaluation.EvaluationResult
}

// evaluateSnapshot evaluates both the train and validation sets with per-run details enabled.
func evaluateSnapshot(ctx context.Context, ev evaluation.AgentEvaluator) (*evalSnapshot, error) {
	train, err := ev.Evaluate(ctx, trainEvalSetID, evaluation.WithRunDetailsEnabled(true))
	if err != nil {
		return nil, fmt.Errorf("evaluate train set: %w", err)
	}
	validation, err := ev.Evaluate(ctx, validationEvalSetID, evaluation.WithRunDetailsEnabled(true))
	if err != nil {
		return nil, fmt.Errorf("evaluate validation set: %w", err)
	}
	return &evalSnapshot{train: train, validation: validation}, nil
}

// evaluateCandidate builds a fresh evaluator bound to the optimizer's accepted instruction and
// evaluates it on the train + validation sets. The returned closer releases the evaluator's
// runners; callers must invoke it.
func evaluateCandidate(ctx context.Context, cfg runConfig, fixture *fakemodel.Fixture, instruction string) (*evalSnapshot, func(), error) {
	built, err := buildCandidateEvaluator(cfg, instruction, fixture)
	if err != nil {
		return nil, nil, fmt.Errorf("build candidate evaluator: %w", err)
	}
	snapshot, err := evaluateSnapshot(ctx, built.evaluator)
	if err != nil {
		built.close()
		return nil, nil, err
	}
	return snapshot, built.close, nil
}

// printAttribution prints the failure attribution for a snapshot's train and validation sets.
func printAttribution(snapshot *evalSnapshot) {
	fmt.Println("── Baseline failure attribution ──")
	printSetAttribution("train", snapshot.train)
	printSetAttribution("validation", snapshot.validation)
}

func printSetAttribution(label string, result *evaluation.EvaluationResult) {
	attributions := pipeline.AttributeResult(result)
	fmt.Printf("[%s] overall=%s cases=%d\n", label, result.OverallStatus, len(attributions))
	for _, a := range attributions {
		if a.Passed {
			fmt.Printf("  ✅ %-28s score=%.3f\n", a.EvalCaseID, a.Score)
			continue
		}
		fmt.Printf("  ❌ %-28s score=%.3f category=%s reason=%q\n",
			a.EvalCaseID, a.Score, a.Category, a.Reason)
	}
}

// printGateDecision prints the acceptance-gate verdict: the per-case validation delta, each gate
// criterion, and the final accept/reject reasons.
func printGateDecision(d pipeline.GateDecision) {
	fmt.Println("── Acceptance gate (validation) ──")
	for _, delta := range d.ValidationDeltas {
		fmt.Printf("  %-12s %-28s %.3f → %.3f (%+.3f)\n",
			delta.Class, delta.EvalCaseID, delta.BaselineScore, delta.CandidateScore, delta.ScoreDelta)
	}
	for _, c := range d.Criteria {
		mark := "✅"
		if !c.Passed {
			mark = "❌"
		}
		fmt.Printf("  %s %-26s %s\n", mark, c.Name, c.Detail)
	}
	verdict := "REJECT"
	if d.Accepted {
		verdict = "ACCEPT"
	}
	fmt.Printf("Gate decision: %s\n", verdict)
	for _, r := range d.Reasons {
		fmt.Printf("  - %s\n", r)
	}
}
