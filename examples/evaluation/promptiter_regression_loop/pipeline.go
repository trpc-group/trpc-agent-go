//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"fmt"
	"path/filepath"
	"time"
)

// RunPipeline executes baseline evaluation, PromptIter-style optimization,
// validation regression, gate decision, and audit report assembly.
func RunPipeline(ctx context.Context, input *LoadedInput) (*OptimizationReport, error) {
	if input == nil {
		return nil, fmt.Errorf("input is nil")
	}
	startedAt := time.Now()
	evaluator := newLocalEvaluator(input.Metrics, input.Config.FakeEngine)
	baselineTrain, err := evaluator.Evaluate(ctx, "baseline_train", input.TrainEvalSet, input.BaselinePrompt)
	if err != nil {
		return nil, fmt.Errorf("evaluate baseline train: %w", err)
	}
	baselineValidation, err := evaluator.Evaluate(ctx, "baseline_validation", input.ValidationEvalSet, input.BaselinePrompt)
	if err != nil {
		return nil, fmt.Errorf("evaluate baseline validation: %w", err)
	}
	report := &OptimizationReport{
		RunID:              fmt.Sprintf("%s-%d", input.Config.AppName, input.Config.Seed),
		AppName:            input.Config.AppName,
		Mode:               "deterministic",
		DataSource:         "fake model with deterministic evalset responses",
		Seed:               input.Config.Seed,
		TargetSurfaceID:    input.Config.TargetSurfaceID,
		PromptSource:       filepath.ToSlash(input.Config.PromptSource),
		FakeEngine:         input.Config.FakeEngine,
		BaselinePrompt:     input.BaselinePrompt,
		BaselineTrain:      baselineTrain,
		BaselineValidation: baselineValidation,
		Cost:               addCost(baselineTrain.Cost, baselineValidation.Cost),
		FailureAttribution: summarizeFailures(baselineTrain, baselineValidation),
	}
	candidates := input.Config.Candidates
	if input.Config.MaxRounds < len(candidates) {
		candidates = candidates[:input.Config.MaxRounds]
	}
	var selected *CandidateSummary
	var selectedDelta DeltaSummary
	var selectedGate GateDecision
	for idx, candidate := range candidates {
		roundStart := time.Now()
		prompt := candidatePrompt(input.BaselinePrompt, candidate)
		patches, profile := buildPromptIterArtifacts(input.Config, candidate, prompt)
		trainResult, err := evaluator.Evaluate(ctx, "candidate_train", input.TrainEvalSet, prompt)
		if err != nil {
			return nil, fmt.Errorf("evaluate candidate train: %w", err)
		}
		validationResult, err := evaluator.Evaluate(ctx, "candidate_validation", input.ValidationEvalSet, prompt)
		if err != nil {
			return nil, fmt.Errorf("evaluate candidate validation: %w", err)
		}
		roundCost := addCost(trainResult.Cost, validationResult.Cost)
		report.Cost = addCost(report.Cost, roundCost)
		delta := ComputeDelta(baselineValidation, validationResult)
		gate := DecideGate(input.Config.Gate, delta, report.Cost)
		round := RoundAudit{
			Round:         idx + 1,
			CandidateID:   candidate.ID,
			Losses:        buildLosses(trainResult),
			Patches:       patches,
			OutputProfile: profile,
			Delta:         delta,
			Gate:          gate,
			Cost:          roundCost,
			LatencyMs:     time.Since(roundStart).Milliseconds(),
		}
		report.Rounds = append(report.Rounds, round)
		summary := CandidateSummary{
			ID:                   candidate.ID,
			Description:          candidate.Description,
			Prompt:               prompt,
			TrainEvaluation:      trainResult,
			ValidationEvaluation: validationResult,
		}
		if selected == nil || delta.CandidateScore > selectedDelta.CandidateScore {
			selected = &summary
			selectedDelta = delta
			selectedGate = gate
		}
		if gate.Accepted {
			break
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("no candidates configured")
	}
	report.Candidate = *selected
	report.Delta = selectedDelta
	report.Gate = selectedGate
	report.FailureAttribution = FailureSummary{
		Train:      countFailures(report.Candidate.TrainEvaluation),
		Validation: countFailures(report.Candidate.ValidationEvaluation),
	}
	finishedAt := time.Now()
	report.Latency = LatencySummary{
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
		DurationMs: finishedAt.Sub(startedAt).Milliseconds(),
	}
	return report, nil
}
