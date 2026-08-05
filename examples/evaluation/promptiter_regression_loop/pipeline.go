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
	"os"
	"path/filepath"
	"time"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metriclocal "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/backwarder"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/optimizer"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const (
	candidateAppName = "headline-card-candidate"
	backwarderApp    = "headline-card-backwarder"
	aggregatorApp    = "headline-card-aggregator"
	optimizerApp     = "headline-card-optimizer"
)

// PipelineResult carries every observable result of one pipeline run. It feeds
// the audit report generator and keeps report.go free of engine dependencies.
type PipelineResult struct {
	Config          *Config
	RunID           string
	StartedAt       string
	DurationMs      int64
	BaselinePrompt  string
	BaselineTrain   *EvalResult
	BaselineValidation *EvalResult
	Rounds          []PipelineRound
	FinalAcceptedRound   int
	FinalCandidatePrompt string
	FinalGate        *GateDecision
	BaselineAttribution []CaseAttribution
	Recommendation   string
	ModelCalls       int
	LatencyMs        int64
}

// PipelineRound captures one optimization round after both engine and gate.
type PipelineRound struct {
	Round           int
	CandidatePrompt string
	Train           *EvalResult
	Validation      *EvalResult
	EngineAccepted  bool
	EngineReason    string
	GateAccepted    bool
	GateReason      string
	Deltas          []CaseDelta
	Attribution     []CaseAttribution
	ModelCalls      int
}

// sharedMetricLocator points every evalset at the single shared metrics file.
type sharedMetricLocator struct {
	metricFileID string
}

// Build implements metric.Locator.
func (l *sharedMetricLocator) Build(baseDir, appName, _ string) string {
	return filepath.Join(baseDir, appName, l.metricFileID+".metrics.json")
}

// RunPipeline executes the full Evaluation + Optimization closed loop.
func RunPipeline(ctx context.Context, cfg *Config) (*PipelineResult, error) {
	started := time.Now()
	runID := fmt.Sprintf("run-%d", started.UnixMilli())
	startedAt := started.Format(time.RFC3339)

	baselinePrompt, err := readPrompt(cfg.BaselinePromptFile)
	if err != nil {
		return nil, err
	}
	fake := newFakeModel(cfg.Model.Name)

	// Build the candidate agent with the baseline instruction.
	candidateAgent, err := newCandidateAgent(cfg.CandidateName, fake, baselinePrompt)
	if err != nil {
		return nil, fmt.Errorf("create candidate agent: %w", err)
	}
	candidateRunner := runner.NewRunner(candidateAppName, candidateAgent)
	defer candidateRunner.Close()

	// Build the PromptIter stage agents bound to the same deterministic model.
	backwarderAgent := newStageAgent(backwarderApp, fake)
	aggregatorAgent := newStageAgent(aggregatorApp, fake)
	optimizerAgent := newStageAgent(optimizerApp, fake)
	backwarderRunner := runner.NewRunner(backwarderApp, backwarderAgent)
	aggregatorRunner := runner.NewRunner(aggregatorApp, aggregatorAgent)
	optimizerRunner := runner.NewRunner(optimizerApp, optimizerAgent)
	defer backwarderRunner.Close()
	defer aggregatorRunner.Close()
	defer optimizerRunner.Close()

	// Local managers for evalsets, metrics and eval results.
	evalSetManager := evalsetlocal.New(evalset.WithBaseDir(cfg.DataDir))
	metricManager := metriclocal.New(
		metric.WithBaseDir(cfg.DataDir),
		metric.WithLocator(&sharedMetricLocator{metricFileID: cfg.MetricFileID}),
	)
	evalResultManager := evalresultlocal.New(evalresult.WithBaseDir(cfg.OutputDir))

	// High-level evaluator used both for the baseline and by the PromptIter engine.
	agentEvaluator, err := evaluation.New(
		cfg.AppName,
		candidateRunner,
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithMetricManager(metricManager),
		evaluation.WithEvalResultManager(evalResultManager),
		evaluation.WithNumRuns(1),
	)
	if err != nil {
		return nil, fmt.Errorf("create evaluator: %w", err)
	}
	defer agentEvaluator.Close()

	// PromptIter collaborators.
	backwarderInstance, err := backwarder.New(ctx, backwarderRunner)
	if err != nil {
		return nil, fmt.Errorf("create backwarder: %w", err)
	}
	aggregatorInstance, err := aggregator.New(ctx, aggregatorRunner)
	if err != nil {
		return nil, fmt.Errorf("create aggregator: %w", err)
	}
	optimizerInstance, err := optimizer.New(ctx, optimizerRunner)
	if err != nil {
		return nil, fmt.Errorf("create optimizer: %w", err)
	}
	engineInstance, err := promptiterengine.New(
		ctx,
		promptiterengine.WithAgent(candidateAgent),
		promptiterengine.WithAgentEvaluator(agentEvaluator),
		promptiterengine.WithBackwarder(backwarderInstance),
		promptiterengine.WithAggregator(aggregatorInstance),
		promptiterengine.WithOptimizer(optimizerInstance),
	)
	if err != nil {
		return nil, fmt.Errorf("create promptiter engine: %w", err)
	}

	// Stage 1: baseline evaluation on train and validation.
	baselineTrainRaw, err := agentEvaluator.Evaluate(ctx, cfg.TrainEvalSetID)
	if err != nil {
		return nil, fmt.Errorf("evaluate baseline train: %w", err)
	}
	baselineValidationRaw, err := agentEvaluator.Evaluate(ctx, cfg.ValidationEvalSetID)
	if err != nil {
		return nil, fmt.Errorf("evaluate baseline validation: %w", err)
	}
	baselineTrain := adaptDirectEval(baselineTrainRaw)
	baselineValidation := adaptDirectEval(baselineValidationRaw)
	baselineAttribution := AttributeAll(baselineTrain.Cases)

	// Stage 2: PromptIter optimization over the candidate instruction surface.
	runRequest := buildRunRequest(cfg)
	engineResult, err := engineInstance.Run(ctx, runRequest)
	if err != nil {
		return nil, fmt.Errorf("run promptiter engine: %w", err)
	}

	// Stage 3-5: per-round candidate prompt, delta, attribution and gate.
	comparisonBaseline := baselineValidation
	pipelineRounds := make([]PipelineRound, 0, len(engineResult.Rounds))
	finalAcceptedRound := 0
	finalCandidatePrompt := baselinePrompt
	finalCandidateValidation := baselineValidation
	for _, roundResult := range engineResult.Rounds {
		candidatePrompt := profileInstruction(roundResult.OutputProfile, cfg.TargetSurfaceID)
		if candidatePrompt == "" {
			candidatePrompt = baselinePrompt
		}
		candidateValidation := adaptEngineEval(roundResult.Validation)
		// Per-case deltas are computed against the current accepted baseline (the
		// comparison target used by both the engine acceptance and the gate), so
		// the report's delta table stays consistent with the gate decision.
		gateDeltas := ComputeDeltas(comparisonBaseline.Cases, candidateValidation.Cases)
		gateCalls := fake.CallCount()
		gateLatency := time.Since(started).Milliseconds()
		gateDecision := EvaluateGate(cfg.Gate, comparisonBaseline, candidateValidation, gateDeltas, gateCalls, gateLatency)
		attributions := AttributeAll(candidateValidation.Cases)

		engineAccepted := roundResult.Acceptance != nil && roundResult.Acceptance.Accepted
		engineReason := ""
		if roundResult.Acceptance != nil {
			engineReason = roundResult.Acceptance.Reason
		}
		pipelineRounds = append(pipelineRounds, PipelineRound{
			Round:           roundResult.Round,
			CandidatePrompt: candidatePrompt,
			Train:           adaptEngineEval(roundResult.Train),
			Validation:      candidateValidation,
			EngineAccepted:  engineAccepted,
			EngineReason:    engineReason,
			GateAccepted:    gateDecision.Accepted,
			GateReason:      gateDecision.Reason,
			Deltas:          gateDeltas,
			Attribution:     attributions,
			ModelCalls:      gateCalls,
		})
		if engineAccepted {
			comparisonBaseline = candidateValidation
			finalAcceptedRound = roundResult.Round
			finalCandidatePrompt = candidatePrompt
			finalCandidateValidation = candidateValidation
		}
	}

	// Stage 6: final gate over the last accepted candidate.
	finalGateDeltas := ComputeDeltas(baselineValidation.Cases, finalCandidateValidation.Cases)
	finalGate := EvaluateGate(
		cfg.Gate,
		baselineValidation,
		finalCandidateValidation,
		finalGateDeltas,
		fake.CallCount(),
		time.Since(started).Milliseconds(),
	)

	recommendation := buildRecommendation(cfg, finalGate, finalAcceptedRound, finalCandidatePrompt, baselineValidation, finalCandidateValidation)

	durationMs := time.Since(started).Milliseconds()
	return &PipelineResult{
		Config:              cfg,
		RunID:               runID,
		StartedAt:           startedAt,
		DurationMs:          durationMs,
		BaselinePrompt:      baselinePrompt,
		BaselineTrain:       baselineTrain,
		BaselineValidation:  baselineValidation,
		Rounds:              pipelineRounds,
		FinalAcceptedRound:  finalAcceptedRound,
		FinalCandidatePrompt: finalCandidatePrompt,
		FinalGate:           finalGate,
		BaselineAttribution: baselineAttribution,
		Recommendation:      recommendation,
		ModelCalls:          fake.CallCount(),
		LatencyMs:           durationMs,
	}, nil
}

// buildRunRequest assembles the PromptIter engine request.
func buildRunRequest(cfg *Config) *promptiterengine.RunRequest {
	options := promptiterengine.EvaluationOptions{
		EvalCaseParallelism:               cfg.EvalCaseParallelism,
		EvalCaseParallelInferenceEnabled:  cfg.ParallelInference,
		EvalCaseParallelEvaluationEnabled: cfg.ParallelEvaluation,
	}
	return &promptiterengine.RunRequest{
		Train: []promptiterengine.EvalSetInput{
			{EvalSetID: cfg.TrainEvalSetID},
		},
		Validation: []promptiterengine.EvalSetInput{
			{EvalSetID: cfg.ValidationEvalSetID},
		},
		EvaluationOptions: options,
		AcceptancePolicy: promptiterengine.AcceptancePolicy{
			MinScoreGain: cfg.AcceptancePolicy.MinScoreGain,
		},
		StopPolicy: promptiterengine.StopPolicy{
			MaxRoundsWithoutAcceptance: cfg.StopPolicy.MaxRoundsWithoutAcceptance,
		},
		MaxRounds:        cfg.MaxRounds,
		TargetSurfaceIDs: []string{cfg.TargetSurfaceID},
	}
}

// readPrompt reads the baseline prompt source file.
func readPrompt(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read baseline prompt %q: %w", path, err)
	}
	return string(raw), nil
}

// profileInstruction extracts the instruction text of a surface from a profile.
func profileInstruction(profile *promptiter.Profile, surfaceID string) string {
	if profile == nil {
		return ""
	}
	for _, override := range profile.Overrides {
		if override.SurfaceID == surfaceID && override.Value.Text != nil {
			return *override.Value.Text
		}
	}
	return ""
}

// adaptDirectEval converts an evaluation.EvaluationResult (baseline runs) into the
// normalized EvalResult used by the pipeline stages.
func adaptDirectEval(result *evaluation.EvaluationResult) *EvalResult {
	if result == nil {
		return &EvalResult{}
	}
	cases := make([]CaseScore, 0, len(result.EvalCases))
	totalScore := 0.0
	for _, evalCase := range result.EvalCases {
		if evalCase == nil {
			continue
		}
		metrics := make([]MetricScore, 0, len(evalCase.MetricResults))
		caseTotal := 0.0
		metricCount := 0
		casePassed := true
		for _, metricResult := range evalCase.MetricResults {
			if metricResult == nil || metricResult.EvalStatus == status.EvalStatusNotEvaluated {
				continue
			}
			passed := metricResult.EvalStatus == status.EvalStatusPassed
			reason := ""
			if metricResult.Details != nil {
				reason = metricResult.Details.Reason
			}
			metrics = append(metrics, MetricScore{
				MetricName: metricResult.MetricName,
				Score:      metricResult.Score,
				Passed:     passed,
				Status:     string(metricResult.EvalStatus),
				Reason:     reason,
			})
			caseTotal += metricResult.Score
			metricCount++
			if !passed {
				casePassed = false
			}
		}
		caseScore := 0.0
		if metricCount > 0 {
			caseScore = caseTotal / float64(metricCount)
		}
		cases = append(cases, CaseScore{
			EvalSetID:  result.EvalSetID,
			EvalCaseID: evalCase.EvalCaseID,
			Passed:     casePassed,
			Score:      caseScore,
			Metrics:    metrics,
		})
		totalScore += caseScore
	}
	overall := 0.0
	if len(cases) > 0 {
		overall = totalScore / float64(len(cases))
	}
	return &EvalResult{OverallScore: overall, Cases: cases}
}

// adaptEngineEval converts a promptiterengine.EvaluationResult (engine rounds) into
// the normalized EvalResult used by the pipeline stages.
func adaptEngineEval(result *promptiterengine.EvaluationResult) *EvalResult {
	if result == nil {
		return &EvalResult{}
	}
	cases := make([]CaseScore, 0)
	totalScore := 0.0
	for _, evalSet := range result.EvalSets {
		for _, evalCase := range evalSet.Cases {
			metrics := make([]MetricScore, 0, len(evalCase.Metrics))
			caseTotal := 0.0
			casePassed := true
			for _, metricResult := range evalCase.Metrics {
				passed := metricResult.Status == status.EvalStatusPassed
				metrics = append(metrics, MetricScore{
					MetricName: metricResult.MetricName,
					Score:      metricResult.Score,
					Passed:     passed,
					Status:     string(metricResult.Status),
					Reason:     metricResult.Reason,
				})
				caseTotal += metricResult.Score
				if !passed {
					casePassed = false
				}
			}
			caseScore := 0.0
			if len(evalCase.Metrics) > 0 {
				caseScore = caseTotal / float64(len(evalCase.Metrics))
			}
			cases = append(cases, CaseScore{
				EvalSetID:  evalSet.EvalSetID,
				EvalCaseID: evalCase.EvalCaseID,
				Passed:     casePassed,
				Score:      caseScore,
				Metrics:    metrics,
			})
			totalScore += caseScore
		}
	}
	overall := result.OverallScore
	if len(cases) == 0 {
		overall = 0
	}
	return &EvalResult{OverallScore: overall, Cases: cases}
}

// buildRecommendation summarizes the write-back decision in plain language.
func buildRecommendation(
	cfg *Config,
	finalGate *GateDecision,
	finalAcceptedRound int,
	finalCandidatePrompt string,
	baselineValidation *EvalResult,
	candidateValidation *EvalResult,
) string {
	baselineScore := 0.0
	candidateScore := 0.0
	if baselineValidation != nil {
		baselineScore = baselineValidation.OverallScore
	}
	if candidateValidation != nil {
		candidateScore = candidateValidation.OverallScore
	}
	if finalGate == nil {
		return "未产生门禁决策,建议保持现有 prompt 并复查配置。"
	}
	if !finalGate.Accepted {
		return fmt.Sprintf(
			"拒绝回写:候选 prompt 未通过接受门禁(%s)。验证集分数从 %.3f 到 %.3f,不值得回写源 prompt。",
			finalGate.Reason, baselineScore, candidateScore,
		)
	}
	if finalAcceptedRound == 0 {
		return fmt.Sprintf(
			"未发现比 baseline 更优的候选 prompt(验证集 %.3f),建议保持现状。",
			baselineScore,
		)
	}
	return fmt.Sprintf(
		"接受并回写第 %d 轮候选 prompt(验证集 %.3f → %.3f),已通过全部门禁检查。建议将 %q 更新到源 prompt 并归档本报告。",
		finalAcceptedRound, baselineScore, candidateScore, finalCandidatePrompt,
	)
}

// surfaceIDFor returns the surface id for the candidate instruction, mirroring
// astructure.SurfaceID used by the engine. Kept for documentation and tests.
func surfaceIDFor(nodeID string, surfaceType astructure.SurfaceType) string {
	return astructure.SurfaceID(nodeID, surfaceType)
}
