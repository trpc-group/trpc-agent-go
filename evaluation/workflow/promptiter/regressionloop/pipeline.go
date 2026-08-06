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
	"math"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
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

// PromptProfileBuilder builds the initial PromptIter profile from the prompt source.
type PromptProfileBuilder interface {
	BuildPromptProfile(surfaceIDs []string, prompt string) (*promptiter.Profile, error)
}

// PromptProfileBuilderFunc adapts a function to PromptProfileBuilder.
type PromptProfileBuilderFunc func(surfaceIDs []string, prompt string) (*promptiter.Profile, error)

// BuildPromptProfile implements PromptProfileBuilder.
func (f PromptProfileBuilderFunc) BuildPromptProfile(
	surfaceIDs []string,
	prompt string,
) (*promptiter.Profile, error) {
	return f(surfaceIDs, prompt)
}

// CandidateProfileValidator validates the final candidate profile before rerunning validation.
type CandidateProfileValidator interface {
	ValidateCandidateProfile(profile *promptiter.Profile, targetSurfaceIDs []string) error
}

// CandidateProfileValidatorFunc adapts a function to CandidateProfileValidator.
type CandidateProfileValidatorFunc func(profile *promptiter.Profile, targetSurfaceIDs []string) error

// ValidateCandidateProfile implements CandidateProfileValidator.
func (f CandidateProfileValidatorFunc) ValidateCandidateProfile(
	profile *promptiter.Profile,
	targetSurfaceIDs []string,
) error {
	return f(profile, targetSurfaceIDs)
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
	Profile   *promptiter.Profile
	Config    Config
	Metrics   []MetricDefinition
}

// Pipeline orchestrates baseline evaluation, attribution, PromptIter, gating, and reports.
type Pipeline struct {
	Evaluator                 Evaluator
	PromptIterator            PromptIterator
	ProfileBuilder            PromptProfileBuilder
	CandidateProfileValidator CandidateProfileValidator
	CostProvider              CostProvider
	AttributionJudge          AttributionJudge
	Clock                     Clock
}

// Result stores the generated report and artifact paths.
type Result struct {
	Report       OptimizationReport
	JSONPath     string
	MarkdownPath string
}

// Run executes the full regression loop.
func (p Pipeline) Run(ctx context.Context, cfg Config) (*Result, error) {
	if err := cfg.validate(configValidationOptions{
		allowCustomTargetSurfaces: p.ProfileBuilder != nil,
	}); err != nil {
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
	if strings.TrimSpace(prompt) == "" {
		return nil, errors.New("prompt source is empty")
	}
	metrics, err := LoadMetricDefinitions(cfg.MetricsPath)
	if err != nil {
		return nil, err
	}
	profileBuilder := p.ProfileBuilder
	if profileBuilder == nil {
		profileBuilder = PromptProfileBuilderFunc(BuildPromptProfile)
	}
	initialProfile, err := profileBuilder.BuildPromptProfile(cfg.TargetSurfaceIDs, prompt)
	if err != nil {
		return nil, fmt.Errorf("build initial prompt profile: %w", err)
	}
	baselineTrain, err := p.Evaluator.Evaluate(ctx, EvaluationRequest{
		Phase:     PhaseBaselineTrain,
		EvalSetID: cfg.TrainEvalSetID,
		Prompt:    prompt,
		Profile:   initialProfile,
		Config:    cfg,
		Metrics:   metrics,
	})
	if err != nil {
		return nil, fmt.Errorf("evaluate baseline train: %w", err)
	}
	if err := validateBaselineEvaluationResult(PhaseBaselineTrain, baselineTrain); err != nil {
		return nil, err
	}
	baselineValidation, err := p.Evaluator.Evaluate(ctx, EvaluationRequest{
		Phase:     PhaseBaselineValidation,
		EvalSetID: cfg.ValidationEvalSetID,
		Prompt:    prompt,
		Profile:   initialProfile,
		Config:    cfg,
		Metrics:   metrics,
	})
	if err != nil {
		return nil, fmt.Errorf("evaluate baseline validation: %w", err)
	}
	if err := validateBaselineEvaluationResult(PhaseBaselineValidation, baselineValidation); err != nil {
		return nil, err
	}
	if err := validateGateSelectors(cfg.Gate, metrics, baselineValidation); err != nil {
		return nil, err
	}
	attributionHints := AttributionHints(cfg, metrics)
	attributionOptions := AttributionOptions{
		Hints:   attributionHints,
		Metrics: metrics,
		Judge:   p.AttributionJudge,
	}
	trainAttributions := AttributeFailuresWithOptions(ctx, baselineTrain, attributionOptions)
	attributions := append(
		trainAttributions,
		AttributeFailuresWithOptions(ctx, baselineValidation, attributionOptions)...,
	)
	request := cfg.BuildRunRequest(BuildLossHints(trainAttributions))
	request.InitialProfile = initialProfile
	promptIterRun, err := p.PromptIterator.Run(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("run promptiter: %w", err)
	}
	candidateValidation, reranCandidateValidation, err := p.evaluateFinalCandidate(ctx, cfg, promptIterRun, metrics)
	if err != nil {
		return nil, err
	}
	candidateAttributions := AttributeFailuresWithOptions(ctx, candidateValidation, AttributionOptions{
		Hints:   attributionHints,
		Metrics: metrics,
		Judge:   p.AttributionJudge,
	})
	finishedAt := clock.Now()
	latency := Duration{Duration: finishedAt.Sub(startedAt)}
	cost := estimateCost(promptIterRun, reranCandidateValidation)
	if p.CostProvider != nil {
		cost = normalizeProviderCost(p.CostProvider.CostSummary(), cost)
	}
	report := BuildReport(ReportInput{
		Ctx:                   ctx,
		Config:                cfg,
		StartedAt:             startedAt,
		FinishedAt:            finishedAt,
		BaselineTrain:         baselineTrain,
		BaselineValidation:    baselineValidation,
		CandidateValidation:   candidateValidation,
		PromptIterRun:         promptIterRun,
		Attributions:          attributions,
		CandidateAttributions: candidateAttributions,
		Metrics:               metrics,
		Cost:                  cost,
		Latency:               latency,
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
	candidateProfile := finalCandidateProfile(run)
	if candidateProfile == nil || len(candidateProfile.Overrides) == 0 {
		return nil, false, nil
	}
	validator := p.CandidateProfileValidator
	if validator == nil {
		validator = CandidateProfileValidatorFunc(validateCandidateProfileTargets)
	}
	if err := validator.ValidateCandidateProfile(candidateProfile, cfg.TargetSurfaceIDs); err != nil {
		return nil, false, err
	}
	candidatePrompt, _ := profilePromptText(candidateProfile)
	result, err := p.Evaluator.Evaluate(ctx, EvaluationRequest{
		Phase:     PhaseCandidateValidation,
		EvalSetID: cfg.ValidationEvalSetID,
		Prompt:    candidatePrompt,
		Profile:   candidateProfile,
		Config:    cfg,
		Metrics:   metrics,
	})
	if err != nil {
		return nil, false, fmt.Errorf("evaluate candidate validation: %w", err)
	}
	if result == nil {
		return nil, false, errors.New("candidate evaluator returned nil result without error")
	}
	return result, true, nil
}

func finalCandidateProfile(run *promptiterengine.RunResult) *promptiter.Profile {
	if run == nil {
		return nil
	}
	for i := len(run.Rounds) - 1; i >= 0; i-- {
		if run.Rounds[i].OutputProfile != nil && len(run.Rounds[i].OutputProfile.Overrides) > 0 {
			return run.Rounds[i].OutputProfile
		}
	}
	return run.AcceptedProfile
}

func validateCandidateProfileTargets(profile *promptiter.Profile, targetSurfaceIDs []string) error {
	if profile == nil || len(profile.Overrides) == 0 {
		return nil
	}
	if len(targetSurfaceIDs) != 1 {
		return fmt.Errorf(
			"candidate profile requires exactly one matching target surface id; got %v",
			targetSurfaceIDs,
		)
	}
	targetSurfaceID := strings.TrimSpace(targetSurfaceIDs[0])
	if len(profile.Overrides) != 1 {
		return fmt.Errorf("candidate profile has multiple overrides; got %d", len(profile.Overrides))
	}
	candidateSurfaceID := strings.TrimSpace(profile.Overrides[0].SurfaceID)
	if candidateSurfaceID != targetSurfaceID {
		return fmt.Errorf(
			"candidate profile surface %q does not match configured target surface %q",
			candidateSurfaceID,
			targetSurfaceID,
		)
	}
	return nil
}

func validateBaselineEvaluationResult(phase Phase, result *promptiterengine.EvaluationResult) error {
	if result == nil {
		return fmt.Errorf("%s evaluator returned nil result without error", phase)
	}
	if len(result.EvalSets) == 0 {
		return fmt.Errorf("%s evaluator returned result without metric coverage", phase)
	}
	sawCase := false
	for _, evalSet := range result.EvalSets {
		if len(evalSet.Cases) == 0 {
			continue
		}
		for _, evalCase := range evalSet.Cases {
			sawCase = true
			if len(evalCase.Metrics) == 0 {
				return fmt.Errorf("%s evaluator returned result without metric coverage", phase)
			}
			for _, metric := range evalCase.Metrics {
				if strings.TrimSpace(metric.MetricName) == "" ||
					!isCompleteMetricStatus(metric.Status) ||
					math.IsNaN(metric.Score) || math.IsInf(metric.Score, 0) {
					return fmt.Errorf("%s evaluator returned result without metric coverage", phase)
				}
			}
		}
	}
	if !sawCase {
		return fmt.Errorf("%s evaluator returned result without metric coverage", phase)
	}
	return nil
}

func isCompleteMetricStatus(metricStatus status.EvalStatus) bool {
	return metricStatus == status.EvalStatusPassed || metricStatus == status.EvalStatusFailed
}

func validateGateSelectors(
	gate GateConfig,
	metrics []MetricDefinition,
	baselineValidation *promptiterengine.EvaluationResult,
) error {
	var errs []error
	if missing := unresolvedHardFailMetricNames(gate.HardFailMetricNames, metrics); len(missing) > 0 {
		errs = append(errs, fmt.Errorf("gate hard fail metric names not found in metrics: %v", missing))
	}
	if missing := unresolvedCriticalCaseIDs(gate.CriticalCaseIDs, baselineValidation); len(missing) > 0 {
		errs = append(errs, fmt.Errorf("gate critical case ids not found in baseline validation: %v", missing))
	}
	return errors.Join(errs...)
}

func unresolvedHardFailMetricNames(selectors []string, metrics []MetricDefinition) []string {
	if len(selectors) == 0 {
		return nil
	}
	available := make(map[string]struct{}, len(metrics))
	for _, metric := range metrics {
		if name := strings.TrimSpace(metric.MetricName); name != "" {
			available[name] = struct{}{}
		}
	}
	var missing []string
	for _, selector := range selectors {
		selector = strings.TrimSpace(selector)
		if selector == "" {
			continue
		}
		if _, ok := available[selector]; !ok {
			missing = append(missing, selector)
		}
	}
	return missing
}

func unresolvedCriticalCaseIDs(selectors []string, baselineValidation *promptiterengine.EvaluationResult) []string {
	if len(selectors) == 0 {
		return nil
	}
	available := make(map[string]struct{})
	if baselineValidation != nil {
		for _, evalSet := range baselineValidation.EvalSets {
			for _, evalCase := range evalSet.Cases {
				if id := strings.TrimSpace(evalCase.EvalCaseID); id != "" {
					available[id] = struct{}{}
				}
			}
		}
	}
	var missing []string
	for _, selector := range selectors {
		selector = strings.TrimSpace(selector)
		if selector == "" {
			continue
		}
		if _, ok := available[selector]; !ok {
			missing = append(missing, selector)
		}
	}
	return missing
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
	measuredModelCalls := cost.ModelCallsMeasured
	if !measuredModelCalls && cost.ModelCalls == 0 {
		cost.ModelCalls = fallback.ModelCalls
	}
	if cost.Amount != 0 {
		cost.AmountMeasured = true
	}
	cost.Estimated = !measuredModelCalls
	if cost.Source == "" {
		cost.Source = CostSourceProvider
	}
	return cost
}
