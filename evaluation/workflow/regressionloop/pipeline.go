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
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

// Pipeline orchestrates the evaluation-optimization regression loop.
// It wraps the PromptIter engine and adds failure attribution, enhanced
// acceptance gates, overfitting detection, and audit report generation.
type Pipeline struct {
	// Engine is the PromptIter engine that runs optimization rounds.
	Engine engine.Engine
	// Config configures the pipeline behavior.
	Config PipelineConfig
}

// NewPipeline creates a new regression loop pipeline.
func NewPipeline(eng engine.Engine, cfg PipelineConfig) *Pipeline {
	if cfg.GateConfig.MinScoreGain == 0 && !cfg.GateConfig.NoNewHardFailures &&
		cfg.GateConfig.OverfitThreshold == 0 {
		cfg.GateConfig = DefaultGateConfig()
	}
	return &Pipeline{
		Engine: eng,
		Config: cfg,
	}
}

// RunRequest carries the inputs for the regression loop.
type RunRequest struct {
	// TrainEvalSets identifies the training evaluation sets.
	TrainEvalSets []string
	// ValidationEvalSets identifies the validation evaluation sets.
	ValidationEvalSets []string
	// MaxRounds caps the optimization loop.
	MaxRounds int
	// BaselineResults are pre-computed baseline evaluation results (optional).
	BaselineResults []CaseEvalResult
	// TrainBaseline is the baseline training set summary.
	TrainBaseline *EvalRunSummary
	// ValBaseline is the baseline validation set summary.
	ValBaseline *EvalRunSummary
	// CostPerToken is the estimated cost per token for cost budgeting.
	CostPerToken float64
}

// RunResult is the output of the regression loop.
type RunResult struct {
	// Report is the full audit report.
	Report *OptimizationReport
	// Accepted indicates whether the candidate was accepted.
	Accepted bool
	// Elapsed is the total pipeline execution time.
	Elapsed time.Duration
}

// Run executes the full regression loop:
// 1. Run baseline evaluation (or use pre-computed results)
// 2. Run PromptIter optimization rounds
// 3. For each round: attribute failures, evaluate gate
// 4. Run candidate validation
// 5. Generate audit report (JSON + MD)
func (p *Pipeline) Run(ctx context.Context, req *RunRequest) (*RunResult, error) {
	start := time.Now()

	// Build the PromptIter run request.
	trainInputs := make([]engine.EvalSetInput, len(req.TrainEvalSets))
	for i, s := range req.TrainEvalSets {
		trainInputs[i] = engine.EvalSetInput{EvalSetID: s}
	}
	valInputs := make([]engine.EvalSetInput, len(req.ValidationEvalSets))
	for i, s := range req.ValidationEvalSets {
		valInputs[i] = engine.EvalSetInput{EvalSetID: s}
	}

	promptIterReq := &engine.RunRequest{
		Train:      trainInputs,
		Validation: valInputs,
		MaxRounds:  req.MaxRounds,
		AcceptancePolicy: engine.AcceptancePolicy{
			MinScoreGain: p.Config.GateConfig.MinScoreGain,
		},
	}

	// Run PromptIter engine.
	engineResult, err := p.Engine.Run(ctx, promptIterReq)
	if err != nil {
		return nil, fmt.Errorf("promptiter engine run: %w", err)
	}

	// Build baseline summary from engine result.
	baselineSummary := buildRoundSummary(engineResult.BaselineValidation)
	baselineRun := evalResultToRunSummary(engineResult.BaselineValidation)

	// Process each round.
	var roundAudits []RoundAudit
	var allAttributions []FailureAttribution
	var lastValResult *engine.EvaluationResult
	totalTokens := 0

	for _, rr := range engineResult.Rounds {
		audit := RoundAudit{
			Round:    rr.Round,
			Accepted: rr.Acceptance != nil && rr.Acceptance.Accepted,
			AcceptanceReason: func() string {
				if rr.Acceptance != nil {
					return rr.Acceptance.Reason
				}
				return ""
			}(),
		}

		// Extract train evaluation results for attribution.
		if rr.Train != nil {
			audit.TrainScore = rr.Train.OverallScore
			caseResults := evalResultToCaseResults(rr.Train)
			attributions := AttributeFailures(caseResults)
			audit.FailureAttributions = SummarizeAttributions(attributions)
			allAttributions = append(allAttributions, attributions...)
		}

		// Extract validation results.
		if rr.Validation != nil {
			audit.ValidationScore = rr.Validation.OverallScore
			lastValResult = rr.Validation
		}

		// Record patches.
		if rr.Patches != nil {
			for _, patch := range rr.Patches.Patches {
				newVal := ""
				if patch.Value.Text != nil {
					newVal = truncate(*patch.Value.Text, 200)
				}
				audit.PatchesApplied = append(audit.PatchesApplied, PatchAudit{
					SurfaceID: patch.SurfaceID,
					NewValue:  newVal,
					Reason:    patch.Reason,
				})
			}
		}

		// Overfitting detection per round.
		if rr.Train != nil && rr.Validation != nil && rr.InputProfile != nil {
			trainScore := rr.Train.OverallScore
			valScore := rr.Validation.OverallScore
			if baselineSummary != nil {
				trainDelta := trainScore - baselineSummary.OverallScore
				valDelta := valScore - baselineSummary.OverallScore
				if trainDelta > p.Config.GateConfig.OverfitThreshold && valDelta < 0 {
					audit.OverfittingDetected = true
				}
			}
		}

		roundAudits = append(roundAudits, audit)
	}

	// Build candidate summary.
	var candidateSummary *RoundSummary
	var candidateRun EvalRunSummary
	var trainCandidate *EvalRunSummary
	if lastValResult != nil {
		candidateSummary = buildRoundSummary(lastValResult)
		candidateRun = evalResultToRunSummary(lastValResult)
	}

	// Build train candidate summary from last round's train result.
	if len(engineResult.Rounds) > 0 {
		lastRound := engineResult.Rounds[len(engineResult.Rounds)-1]
		if lastRound.Train != nil {
			trainRun := evalResultToRunSummary(lastRound.Train)
			trainCandidate = &trainRun
		}
	}

	// Estimate cost.
	costSummary := CostSummary{
		TotalTokens:    totalTokens,
		EstimatedCost:  float64(totalTokens) * req.CostPerToken,
		TotalLatencyMs: time.Since(start).Milliseconds(),
		RoundsRun:      len(engineResult.Rounds),
	}

	// Build the optimization report.
	report := BuildOptimizationReport(
		p.Config,
		baselineSummary,
		candidateSummary,
		baselineRun,
		candidateRun,
		req.TrainBaseline,
		trainCandidate,
		allAttributions,
		roundAudits,
		costSummary,
	)

	accepted := report.GateDecision != nil && report.GateDecision.Accepted

	return &RunResult{
		Report:   report,
		Accepted: accepted,
		Elapsed:  time.Since(start),
	}, nil
}

// GenerateReports writes both JSON and Markdown reports to the given paths.
func GenerateReports(jsonPath, mdPath string, report *OptimizationReport) error {
	if err := WriteJSONReport(jsonPath, report); err != nil {
		return fmt.Errorf("write JSON report: %w", err)
	}
	if err := WriteMarkdownReport(mdPath, report); err != nil {
		return fmt.Errorf("write Markdown report: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func buildRoundSummary(result *engine.EvaluationResult) *RoundSummary {
	if result == nil {
		return nil
	}
	summary := &RoundSummary{
		OverallScore: result.OverallScore,
	}
	for _, es := range result.EvalSets {
		for _, c := range es.Cases {
			summary.TotalCases++
			passed := true
			for _, m := range c.Metrics {
				if m.Status == status.EvalStatusFailed {
					passed = false
					break
				}
			}
			if passed {
				summary.PassCount++
			} else {
				summary.FailCount++
			}
		}
	}
	return summary
}

func evalResultToRunSummary(result *engine.EvaluationResult) EvalRunSummary {
	if result == nil {
		return EvalRunSummary{}
	}
	summary := EvalRunSummary{
		OverallScore: result.OverallScore,
		CaseScores:   make(map[string]float64),
		CaseStatuses: make(map[string]string),
	}
	for _, es := range result.EvalSets {
		for _, c := range es.Cases {
			key := es.EvalSetID + "/" + c.EvalCaseID
			totalScore := 0.0
			count := 0
			passed := true
			for _, m := range c.Metrics {
				totalScore += m.Score
				count++
				if m.Status == status.EvalStatusFailed {
					passed = false
				}
			}
			if count > 0 {
				summary.CaseScores[key] = totalScore / float64(count)
			}
			if passed {
				summary.CaseStatuses[key] = "passed"
				summary.PassCount++
			} else {
				summary.CaseStatuses[key] = "failed"
				summary.FailCount++
			}
			summary.CaseCount++
		}
	}
	return summary
}

func evalResultToCaseResults(result *engine.EvaluationResult) []CaseEvalResult {
	if result == nil {
		return nil
	}
	var caseResults []CaseEvalResult
	for _, es := range result.EvalSets {
		for _, c := range es.Cases {
			cr := CaseEvalResult{
				EvalSetID:  es.EvalSetID,
				EvalCaseID: c.EvalCaseID,
			}
			for _, m := range c.Metrics {
				cr.Metrics = append(cr.Metrics, MetricInfo{
					MetricName: m.MetricName,
					Score:      m.Score,
					Status:     string(m.Status),
					Reason:     m.Reason,
				})
			}
			caseResults = append(caseResults, cr)
		}
	}
	return caseResults
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
