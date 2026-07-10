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
	"errors"
	"fmt"
	"os"
	"time"

	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

// Evaluator runs one prompt against one eval set.
type Evaluator interface {
	Evaluate(ctx context.Context, request EvaluationRequest) (*promptiterengine.EvaluationResult, error)
}

// PromptIterator runs PromptIter using the prepared RunRequest.
type PromptIterator interface {
	Run(ctx context.Context, request *promptiterengine.RunRequest) (*promptiterengine.RunResult, error)
}

// CostProvider returns cumulative cost after a run.
type CostProvider interface {
	CostSummary() CostSummary
}

// Clock supplies time for deterministic tests.
type Clock interface {
	Now() time.Time
}

// SystemClock uses the host clock.
type SystemClock struct{}

// Now returns the current host time.
func (SystemClock) Now() time.Time { return time.Now() }

// EvaluationRequest describes one baseline evaluator call.
type EvaluationRequest struct {
	Phase     Phase
	EvalSetID string
	Prompt    string
	Config    Config
	Metrics   []MetricDefinition
}

// Pipeline orchestrates baseline evaluation, attribution, PromptIter, gating, and reports.
type Pipeline struct {
	Evaluator        Evaluator
	PromptIterator   PromptIterator
	CostProvider     CostProvider
	AttributionJudge AttributionJudge
	Clock            Clock
}

// Result stores the generated report and artifact paths.
type Result struct {
	Report       OptimizationReport
	JSONPath     string
	MarkdownPath string
}

// Run executes the full regression loop.
func (p Pipeline) Run(ctx context.Context, cfg Config) (*Result, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if p.Evaluator == nil {
		return nil, errors.New("evaluator is nil")
	}
	if p.PromptIterator == nil {
		return nil, errors.New("prompt iterator is nil")
	}
	clock := p.Clock
	if clock == nil {
		clock = SystemClock{}
	}
	startedAt := clock.Now()
	promptBytes, err := os.ReadFile(cfg.PromptSource)
	if err != nil {
		return nil, fmt.Errorf("read prompt source: %w", err)
	}
	prompt := string(promptBytes)
	metrics, err := LoadMetricDefinitions(cfg.MetricsPath)
	if err != nil {
		return nil, err
	}
	baselineTrain, err := p.Evaluator.Evaluate(ctx, EvaluationRequest{
		Phase:     PhaseBaselineTrain,
		EvalSetID: cfg.TrainEvalSetID,
		Prompt:    prompt,
		Config:    cfg,
		Metrics:   metrics,
	})
	if err != nil {
		return nil, fmt.Errorf("evaluate baseline train: %w", err)
	}
	baselineValidation, err := p.Evaluator.Evaluate(ctx, EvaluationRequest{
		Phase:     PhaseBaselineValidation,
		EvalSetID: cfg.ValidationEvalSetID,
		Prompt:    prompt,
		Config:    cfg,
		Metrics:   metrics,
	})
	if err != nil {
		return nil, fmt.Errorf("evaluate baseline validation: %w", err)
	}
	attributionHints := AttributionHints(cfg, metrics)
	attributionOptions := AttributionOptions{
		Hints:   attributionHints,
		Metrics: metrics,
		Judge:   p.AttributionJudge,
	}
	attributions := append(
		AttributeFailuresWithOptions(ctx, baselineTrain, attributionOptions),
		AttributeFailuresWithOptions(ctx, baselineValidation, attributionOptions)...,
	)
	trainAttributions := AttributeFailuresWithOptions(ctx, baselineTrain, attributionOptions)
	request := cfg.BuildRunRequest(BuildLossHints(trainAttributions))
	initialProfile, err := BuildPromptProfile(cfg.TargetSurfaceIDs, prompt)
	if err != nil {
		return nil, fmt.Errorf("build initial prompt profile: %w", err)
	}
	request.InitialProfile = initialProfile
	promptIterRun, err := p.PromptIterator.Run(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("run promptiter: %w", err)
	}
	candidateValidation, reranCandidateValidation, err := p.evaluateFinalCandidate(ctx, cfg, promptIterRun, metrics)
	if err != nil {
		return nil, err
	}
	finishedAt := clock.Now()
	latency := Duration{Duration: finishedAt.Sub(startedAt)}
	cost := estimateCost(promptIterRun, reranCandidateValidation)
	if p.CostProvider != nil {
		cost = normalizeProviderCost(p.CostProvider.CostSummary(), cost)
	}
	report := BuildReport(ReportInput{
		Config:              cfg,
		StartedAt:           startedAt,
		FinishedAt:          finishedAt,
		BaselineTrain:       baselineTrain,
		BaselineValidation:  baselineValidation,
		CandidateValidation: candidateValidation,
		PromptIterRun:       promptIterRun,
		Attributions:        attributions,
		AttributionJudge:    p.AttributionJudge,
		Metrics:             metrics,
		Cost:                cost,
		Latency:             latency,
	})
	if err := WriteReports(report, cfg.OutputJSON, cfg.OutputMarkdown); err != nil {
		return nil, err
	}
	return &Result{
		Report:       report,
		JSONPath:     cfg.OutputJSON,
		MarkdownPath: cfg.OutputMarkdown,
	}, nil
}

func (p Pipeline) evaluateFinalCandidate(
	ctx context.Context,
	cfg Config,
	run *promptiterengine.RunResult,
	metrics []MetricDefinition,
) (*promptiterengine.EvaluationResult, bool, error) {
	candidatePrompt := CandidatePrompt(run)
	if candidatePrompt == "" {
		return nil, false, nil
	}
	result, err := p.Evaluator.Evaluate(ctx, EvaluationRequest{
		Phase:     PhaseCandidateValidation,
		EvalSetID: cfg.ValidationEvalSetID,
		Prompt:    candidatePrompt,
		Config:    cfg,
		Metrics:   metrics,
	})
	if err != nil {
		return nil, false, fmt.Errorf("evaluate candidate validation: %w", err)
	}
	return result, true, nil
}

func estimateCost(run *promptiterengine.RunResult, reranCandidateValidation ...bool) CostSummary {
	extraEvaluations := 0
	if len(reranCandidateValidation) > 0 && reranCandidateValidation[0] {
		extraEvaluations = 1
	}
	if run == nil {
		return CostSummary{
			ModelCalls: 2 + extraEvaluations,
			Estimated:  true,
			Source:     CostSourceModelCallEstimate,
		}
	}
	// Two explicit baseline calls are done by this pipeline. PromptIter also
	// evaluates baseline validation once, then train and validation per round.
	// When a candidate prompt is present, the pipeline reruns validation once
	// after PromptIter so the final report is based on an explicit candidate
	// regression pass.
	return CostSummary{
		ModelCalls: 3 + len(run.Rounds)*2 + extraEvaluations,
		Estimated:  true,
		Source:     CostSourceModelCallEstimate,
	}
}

func normalizeProviderCost(cost, fallback CostSummary) CostSummary {
	if cost.ModelCalls == 0 {
		cost.ModelCalls = fallback.ModelCalls
	}
	cost.Estimated = false
	if cost.Source == "" {
		cost.Source = CostSourceProvider
	}
	return cost
}
