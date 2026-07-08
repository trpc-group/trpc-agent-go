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
	"fmt"
	"os"
	"time"
)

// Evaluator evaluates one prompt on one eval set.
type Evaluator interface {
	Evaluate(ctx context.Context, req EvaluationRequest) (EvaluationSummary, error)
}

// Optimizer generates candidate prompts from baseline and train evidence.
type Optimizer interface {
	Candidates(ctx context.Context, req OptimizationRequest) ([]Candidate, error)
}

// Clock supplies time for reproducible tests.
type Clock interface {
	Now() time.Time
}

// SystemClock reads time from the host.
type SystemClock struct{}

// Now returns the current host time.
func (SystemClock) Now() time.Time { return time.Now() }

// EvaluationRequest describes one evaluator call.
type EvaluationRequest struct {
	Phase        Phase
	Round        int
	Prompt       string
	PromptSource PromptSource
	EvalSet      EvalSetRef
	Metrics      MetricsRef
	Config       Config
}

// OptimizationRequest describes candidate generation inputs.
type OptimizationRequest struct {
	Config             Config
	BaselinePrompt     string
	BaselineTrain      EvaluationSummary
	BaselineValidation EvaluationSummary
}

// Pipeline orchestrates the full regression loop.
type Pipeline struct {
	Evaluator Evaluator
	Optimizer Optimizer
	Clock     Clock
}

// Run executes baseline evaluation, optimization, validation gates, and reporting.
func (p *Pipeline) Run(ctx context.Context, cfg Config) (*RunResult, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if p == nil || p.Evaluator == nil {
		return nil, fmt.Errorf("evaluator is nil")
	}
	if p.Optimizer == nil {
		return nil, fmt.Errorf("optimizer is nil")
	}
	clock := p.Clock
	if clock == nil {
		clock = SystemClock{}
	}
	start := clock.Now()
	baselinePrompt, err := os.ReadFile(cfg.PromptSource.Path)
	if err != nil {
		return nil, fmt.Errorf("read baseline prompt: %w", err)
	}
	cfg.PromptSource.BaselineText = string(baselinePrompt)
	baseTrain, err := p.evaluate(ctx, cfg, PhaseBaselineTrain, 0, string(baselinePrompt), cfg.TrainEvalSet)
	if err != nil {
		return nil, err
	}
	baseValidation, err := p.evaluate(ctx, cfg, PhaseBaselineValidation, 0, string(baselinePrompt), cfg.ValidationEvalSet)
	if err != nil {
		return nil, err
	}
	baseline := EvaluationPair{Train: baseTrain, Validation: baseValidation}
	candidates, err := p.Optimizer.Candidates(ctx, OptimizationRequest{
		Config:             cfg,
		BaselinePrompt:     string(baselinePrompt),
		BaselineTrain:      baseTrain,
		BaselineValidation: baseValidation,
	})
	if err != nil {
		return nil, fmt.Errorf("generate candidates: %w", err)
	}
	if cfg.PromptIter.MaxRounds > 0 && len(candidates) > cfg.PromptIter.MaxRounds {
		candidates = candidates[:cfg.PromptIter.MaxRounds]
	}
	rounds := make([]CandidateRound, 0, len(candidates))
	var selected *CandidateRound
	for _, candidate := range candidates {
		train, err := p.evaluate(ctx, cfg, PhaseCandidateTrain, candidate.Round, candidate.Prompt, cfg.TrainEvalSet)
		if err != nil {
			return nil, err
		}
		validation, err := p.evaluate(ctx, cfg, PhaseCandidateValidation, candidate.Round, candidate.Prompt, cfg.ValidationEvalSet)
		if err != nil {
			return nil, err
		}
		deltas, _ := ComputeDeltas(baseValidation, validation, cfg.Gate.CriticalCaseIDs)
		roundCost := CostSummary{}
		addCost(&roundCost, train.Cost)
		addCost(&roundCost, validation.Cost)
		roundLatency := LatencySummary{TotalMS: train.Latency.TotalMS + validation.Latency.TotalMS}
		gate := EvaluateGate(cfg.Gate, baseValidation, validation, deltas, roundCost, roundLatency)
		round := CandidateRound{
			Round:        candidate.Round,
			Prompt:       candidate.Prompt,
			Reason:       candidate.Reason,
			Train:        train,
			Validation:   validation,
			Delta:        deltas,
			GateDecision: gate,
			Cost:         roundCost,
			Latency:      roundLatency,
		}
		rounds = append(rounds, round)
		if gate.Accepted && (selected == nil || round.Validation.Score > selected.Validation.Score) {
			copy := round
			selected = &copy
		}
	}
	finalRound := bestRound(rounds)
	finalDelta := DeltaReport{}
	finalGate := GateDecision{Accepted: false, Reasons: []string{"no candidate generated"}}
	if finalRound != nil {
		finalDelta.Cases, finalDelta.Summary = ComputeDeltas(baseValidation, finalRound.Validation, cfg.Gate.CriticalCaseIDs)
		finalGate = finalRound.GateDecision
	}
	report := &Report{
		Run: RunMetadata{
			AppName:    cfg.AppName,
			StartedAt:  start.Format(time.RFC3339),
			DurationMS: int64(clock.Now().Sub(start) / time.Millisecond),
			Seed:       cfg.Seed,
			Runner:     cfg.Runner,
		},
		Baseline:          baseline,
		Candidates:        rounds,
		SelectedCandidate: selected,
		Delta:             finalDelta,
		GateDecision:      finalGate,
		Artifacts:         []string{cfg.Output.JSONReport, cfg.Output.MarkdownReport},
	}
	report.FailureAttributionStats = BuildAttributionStats(report.Baseline, report.Candidates)
	report.CostSummary = SumCost(report.Baseline, report.Candidates)
	report.LatencySummary = SumLatency(report.Baseline, report.Candidates)
	if err := WriteReports(report, cfg.Output.JSONReport, cfg.Output.MarkdownReport); err != nil {
		return nil, err
	}
	return &RunResult{
		Report:       report,
		JSONPath:     cfg.Output.JSONReport,
		MarkdownPath: cfg.Output.MarkdownReport,
		StartedAt:    start,
	}, nil
}

func (p *Pipeline) evaluate(ctx context.Context, cfg Config, phase Phase, round int, prompt string, evalSet EvalSetRef) (EvaluationSummary, error) {
	result, err := p.Evaluator.Evaluate(ctx, EvaluationRequest{
		Phase:        phase,
		Round:        round,
		Prompt:       prompt,
		PromptSource: cfg.PromptSource,
		EvalSet:      evalSet,
		Metrics:      cfg.Metrics,
		Config:       cfg,
	})
	if err != nil {
		return EvaluationSummary{}, fmt.Errorf("evaluate %s %s: %w", phase, evalSet.ID, err)
	}
	return AttributeEvaluation(result), nil
}

func bestRound(rounds []CandidateRound) *CandidateRound {
	if len(rounds) == 0 {
		return nil
	}
	best := rounds[0]
	for _, round := range rounds[1:] {
		if round.Validation.Score > best.Validation.Score {
			best = round
		}
	}
	return &best
}
