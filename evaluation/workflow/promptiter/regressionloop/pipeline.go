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
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

type Pipeline struct {
	config *Config
}

func NewPipeline(config *Config) *Pipeline {
	return &Pipeline{config: config}
}

func (p *Pipeline) Run(ctx context.Context) (*OptimizationReport, error) {
	startTime := time.Now()

	pctx := &PipelineContext{
		Config: p.config,
	}

	if err := p.S1BaselineEval(pctx); err != nil {
		return nil, fmt.Errorf("S1 baseline eval failed: %w", err)
	}

	if err := p.S2FailureAttribution(pctx); err != nil {
		return nil, fmt.Errorf("S2 failure attribution failed: %w", err)
	}

	if err := p.S3PromptiterOptimization(pctx); err != nil {
		return nil, fmt.Errorf("S3 promptiter optimization failed: %w", err)
	}

	if err := p.S4CandidateEval(pctx); err != nil {
		return nil, fmt.Errorf("S4 candidate eval failed: %w", err)
	}

	if err := p.S5DeltaAndGate(pctx); err != nil {
		return nil, fmt.Errorf("S5 delta and gate failed: %w", err)
	}

	endTime := time.Now()
	durationMS := endTime.Sub(startTime).Milliseconds()

	report := GenerateReport(pctx)
	report.RunMeta.EndTime = endTime
	report.RunMeta.DurationMS = durationMS

	if err := p.S6Reporting(pctx, report); err != nil {
		return nil, fmt.Errorf("S6 reporting failed: %w", err)
	}

	return report, nil
}

func (p *Pipeline) S1BaselineEval(ctx *PipelineContext) error {
	trainSet, err := loadEvalSet(p.config.TrainEvalSetPath)
	if err != nil {
		return fmt.Errorf("load train eval set: %w", err)
	}
	valSet, err := loadEvalSet(p.config.ValidationEvalSetPath)
	if err != nil {
		return fmt.Errorf("load validation eval set: %w", err)
	}

	ctx.BaselineTrain = p.generateFakeEvaluationResult(trainSet, ctx.Config.Seed)
	ctx.BaselineVal = p.generateFakeEvaluationResult(valSet, ctx.Config.Seed)
	return nil
}

type customEvalSet struct {
	EvalSetID   string           `json:"evalSetId"`
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Cases       []customEvalCase `json:"cases"`
}

type customEvalCase struct {
	EvalCaseID     string   `json:"evalCaseId"`
	Name           string   `json:"name"`
	Input          string   `json:"input"`
	ExpectedOutput string   `json:"expectedOutput"`
	Metrics        []string `json:"metrics"`
}

func loadEvalSet(path string) (*customEvalSet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file %s: %w", path, err)
	}
	var evalSet customEvalSet
	if err := json.Unmarshal(data, &evalSet); err != nil {
		return nil, fmt.Errorf("unmarshal file %s: %w", path, err)
	}
	if evalSet.Cases == nil {
		evalSet.Cases = []customEvalCase{}
	}
	return &evalSet, nil
}

func (p *Pipeline) generateFakeEvaluationResult(evalSet *customEvalSet, seed int64) *engine.EvaluationResult {
	rng := rand.New(rand.NewSource(seed))

	cases := make([]engine.CaseResult, 0, len(evalSet.Cases))
	totalScore := 0.0

	for _, evalCase := range evalSet.Cases {
		caseResult := engine.CaseResult{
			EvalSetID:  evalSet.EvalSetID,
			EvalCaseID: evalCase.EvalCaseID,
			Metrics:    make([]engine.MetricResult, 0, len(evalCase.Metrics)),
		}

		caseTotalScore := 0.0
		for _, metricID := range evalCase.Metrics {
			score := rng.Float64()
			var evalStatus status.EvalStatus
			var reason string

			if score >= 0.7 {
				evalStatus = status.EvalStatusPassed
			} else {
				evalStatus = status.EvalStatusFailed
				reason = getFakeFailureReason(metricID, rng)
			}

			caseResult.Metrics = append(caseResult.Metrics, engine.MetricResult{
				MetricName: metricID,
				Score:      score,
				Status:     evalStatus,
				Reason:     reason,
			})
			caseTotalScore += score
		}

		if len(caseResult.Metrics) > 0 {
			caseTotalScore /= float64(len(caseResult.Metrics))
		}
		totalScore += caseTotalScore
		cases = append(cases, caseResult)
	}

	overallScore := 0.0
	if len(cases) > 0 {
		overallScore = totalScore / float64(len(cases))
	}

	return &engine.EvaluationResult{
		OverallScore: overallScore,
		EvalSets: []engine.EvalSetResult{
			{
				EvalSetID:    evalSet.EvalSetID,
				OverallScore: overallScore,
				Cases:        cases,
			},
		},
	}
}

func getFakeFailureReason(metricID string, rng *rand.Rand) string {
	switch {
	case contains(metricID, "format"):
		reasons := []string{"JSON parse error", "format mismatch", "invalid structure"}
		return reasons[rng.Intn(len(reasons))]
	case contains(metricID, "tool"):
		reasons := []string{"tool call error", "unknown tool", "missing tool", "invalid arguments"}
		return reasons[rng.Intn(len(reasons))]
	case contains(metricID, "router"):
		reasons := []string{"route error", "routing failed", "no handler found"}
		return reasons[rng.Intn(len(reasons))]
	case contains(metricID, "knowledge"):
		reasons := []string{"missing information", "knowledge gap", "incorrect recall"}
		return reasons[rng.Intn(len(reasons))]
	default:
		reasons := []string{"response mismatch", "low similarity", "incorrect output"}
		return reasons[rng.Intn(len(reasons))]
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func (p *Pipeline) S2FailureAttribution(ctx *PipelineContext) error {
	ctx.Attributions = AttributeFailures(ctx.BaselineVal, p.config.AttributionRules)
	return nil
}

func (p *Pipeline) S3PromptiterOptimization(ctx *PipelineContext) error {
	switch p.config.Mode {
	case "fake":
		return p.runFakeOptimization(ctx)
	case "trace-smoke":
		return p.runFakeOptimization(ctx)
	default:
		return fmt.Errorf("unsupported mode: %s", p.config.Mode)
	}
}

func (p *Pipeline) runFakeOptimization(ctx *PipelineContext) error {
	lossHints := p.convertAttributionsToLossHints(ctx.Attributions)

	maxRounds := p.config.Optimization.MaxRounds
	minScoreGain := p.config.Optimization.MinScoreGain

	baselineValScore := ctx.BaselineVal.OverallScore

	var candidates []CandidateInfo
	currentScore := baselineValScore
	totalCost := 0.0
	totalCalls := 0
	totalLatency := 0

	for round := 1; round <= maxRounds; round++ {
		scoreImprovement := float64(round) * 0.05 * (1.0 - float64(len(lossHints))*0.02)
		if scoreImprovement < 0 {
			scoreImprovement = 0.02
		}

		candidateScore := currentScore + scoreImprovement
		if candidateScore > 1.0 {
			candidateScore = 1.0
		}

		scoreDelta := candidateScore - baselineValScore
		accepted := scoreDelta >= minScoreGain

		candidates = append(candidates, CandidateInfo{
			Round:           round,
			ValidationScore: candidateScore,
			Accepted:        accepted,
		})

		if accepted {
			currentScore = candidateScore
		}

		totalCost += 2.5 * float64(round)
		totalCalls += 5 + round*2
		totalLatency += 1000 + round*500

		if scoreDelta >= 0.15 {
			break
		}
	}

	ctx.Candidates = candidates
	ctx.TotalCost = totalCost
	ctx.TotalCalls = totalCalls
	ctx.TotalLatencyMS = int64(totalLatency)

	return nil
}

func (p *Pipeline) convertAttributionsToLossHints(attributions []AttributionResult) []engine.LossHint {
	var hints []engine.LossHint
	for _, attr := range attributions {
		hints = append(hints, engine.LossHint{
			EvalCaseID: attr.EvalCaseID,
			MetricName: attr.MetricName,
			Severity:   severityFromCategory(attr.Category),
			Reason:     attr.Reason,
		})
	}
	return hints
}

func (p *Pipeline) S4CandidateEval(ctx *PipelineContext) error {
	var acceptedCandidate *CandidateInfo
	for i := len(ctx.Candidates) - 1; i >= 0; i-- {
		if ctx.Candidates[i].Accepted {
			acceptedCandidate = &ctx.Candidates[i]
			break
		}
	}

	if acceptedCandidate == nil && len(ctx.Candidates) > 0 {
		acceptedCandidate = &ctx.Candidates[len(ctx.Candidates)-1]
	}

	var candidateValScore, candidateTrainScore float64
	if acceptedCandidate != nil {
		candidateValScore = acceptedCandidate.ValidationScore
		candidateTrainScore = candidateValScore * 1.05
	} else {
		candidateValScore = ctx.BaselineVal.OverallScore + 0.05
		candidateTrainScore = ctx.BaselineTrain.OverallScore + 0.05
	}

	if candidateTrainScore > 1.0 {
		candidateTrainScore = 1.0
	}

	ctx.CandidateTrain = p.generateImprovedEvaluationResult(ctx.BaselineTrain, candidateTrainScore)
	ctx.CandidateVal = p.generateImprovedEvaluationResult(ctx.BaselineVal, candidateValScore)

	return nil
}

func (p *Pipeline) generateImprovedEvaluationResult(baseline *engine.EvaluationResult, targetScore float64) *engine.EvaluationResult {
	if baseline == nil {
		return &engine.EvaluationResult{OverallScore: targetScore}
	}

	scoreRatio := targetScore / baseline.OverallScore
	if scoreRatio > 1.5 {
		scoreRatio = 1.5
	}

	evalSets := make([]engine.EvalSetResult, 0, len(baseline.EvalSets))
	totalScore := 0.0
	totalCases := 0

	for _, baselineSet := range baseline.EvalSets {
		cases := make([]engine.CaseResult, 0, len(baselineSet.Cases))

		for _, baselineCase := range baselineSet.Cases {
			metrics := make([]engine.MetricResult, 0, len(baselineCase.Metrics))
			caseTotalScore := 0.0

			for _, baselineMetric := range baselineCase.Metrics {
				newScore := baselineMetric.Score * scoreRatio
				if newScore > 1.0 {
					newScore = 1.0
				}

				var evalStatus status.EvalStatus
				var reason string
				if newScore >= 0.7 {
					evalStatus = status.EvalStatusPassed
				} else {
					evalStatus = status.EvalStatusFailed
					reason = baselineMetric.Reason
				}

				metrics = append(metrics, engine.MetricResult{
					MetricName: baselineMetric.MetricName,
					Score:      newScore,
					Status:     evalStatus,
					Reason:     reason,
				})
				caseTotalScore += newScore
			}

			if len(metrics) > 0 {
				caseTotalScore /= float64(len(metrics))
			}
			totalScore += caseTotalScore
			totalCases++

			cases = append(cases, engine.CaseResult{
				EvalSetID:  baselineCase.EvalSetID,
				EvalCaseID: baselineCase.EvalCaseID,
				Metrics:    metrics,
			})
		}

		setScore := 0.0
		if len(cases) > 0 {
			setScore = totalScore / float64(totalCases)
		}

		evalSets = append(evalSets, engine.EvalSetResult{
			EvalSetID:    baselineSet.EvalSetID,
			OverallScore: setScore,
			Cases:        cases,
		})
	}

	overallScore := 0.0
	if totalCases > 0 {
		overallScore = totalScore / float64(totalCases)
	}

	return &engine.EvaluationResult{
		OverallScore: overallScore,
		EvalSets:     evalSets,
	}
}

func (p *Pipeline) S5DeltaAndGate(ctx *PipelineContext) error {
	ctx.CaseDeltas = ComputeDeltas(ctx.BaselineVal, ctx.CandidateVal)
	decision := EvaluateGate(
		p.config.Gate,
		ctx.BaselineVal.OverallScore,
		ctx.CandidateVal.OverallScore,
		ctx.BaselineTrain.OverallScore,
		ctx.CandidateTrain.OverallScore,
		ctx.CaseDeltas,
		ctx.TotalCost,
		ctx.TotalCalls,
		ctx.TotalLatencyMS,
	)
	ctx.GateDecision = &decision
	return nil
}

func (p *Pipeline) S6Reporting(ctx *PipelineContext, report *OptimizationReport) error {
	return WriteReports(report, p.config.Output.OutputDir)
}
