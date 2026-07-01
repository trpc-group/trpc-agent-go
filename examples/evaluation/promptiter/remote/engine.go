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
	"errors"
	"fmt"
	"os"
	"path/filepath"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metriclocal "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/backwarder"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/optimizer"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	rootrunner "trpc.group/trpc-go/trpc-agent-go/runner"
	trpcagentrunner "trpc.group/trpc-go/trpc-agent-go/runner/trpcagent"
)

const (
	appName             = "promptiter-nba-commentary-app"
	candidateAppName    = "promptiter-nba-commentary-candidate"
	judgeAppName        = "promptiter-nba-commentary-judge"
	backwarderAppName   = "promptiter-nba-commentary-backwarder"
	aggregatorAppName   = "promptiter-nba-commentary-aggregator"
	optimizerAppName    = "promptiter-nba-commentary-optimizer"
	trainEvalSetID      = "nba-commentary-train"
	validationEvalSetID = "nba-commentary-validation"
	sharedMetricFileID  = "sports-commentary"
)

type remoteRunConfig struct {
	DataDir                    string
	OutputDir                  string
	CandidateTarget            string
	CandidateBasePath          string
	CandidateInstruction       string
	JudgeModelName             string
	WorkerModelName            string
	MaxRounds                  int
	MinScoreGain               float64
	MaxRoundsWithoutAcceptance int
	TargetScore                float64
	EvalCaseParallelism        int
	BackwardCaseParallelism    int
	AggregationParallelism     int
	OptimizerParallelism       int
	ParallelInferenceEnabled   bool
	ParallelEvaluationEnabled  bool
	ParallelBackwardEnabled    bool
	ParallelAggregationEnabled bool
	ParallelOptimizerEnabled   bool
}

type sharedMetricLocator struct {
	metricFileID string
}

type promptIterRuntime struct {
	engine promptiterengine.Engine
	close  func()
}

func runRemotePromptIterExample(ctx context.Context, cfg remoteRunConfig) error {
	result, targetSurfaceID, err := runRemotePromptIter(ctx, cfg)
	if err != nil {
		return err
	}
	if err := printSummary(result, cfg.DataDir, cfg.OutputDir, cfg.CandidateInstruction, targetSurfaceID); err != nil {
		return fmt.Errorf("print summary: %w", err)
	}
	return nil
}

func runRemotePromptIter(ctx context.Context, cfg remoteRunConfig) (*promptiterengine.RunResult, string, error) {
	runtime, err := buildRemotePromptIterRuntime(ctx, cfg)
	if err != nil {
		return nil, "", err
	}
	defer runtime.close()
	targetSurfaceID := astructure.SurfaceID(candidateAgentName, astructure.SurfaceTypeInstruction)
	result, err := runtime.engine.Run(ctx, buildRunRequest(cfg, targetSurfaceID))
	if err != nil {
		return nil, "", fmt.Errorf("run promptiter: %w", err)
	}
	return result, targetSurfaceID, nil
}

func buildRemotePromptIterRuntime(ctx context.Context, cfg remoteRunConfig) (*promptIterRuntime, error) {
	judgeModel, err := loadOpenAIModel(cfg.JudgeModelName)
	if err != nil {
		return nil, fmt.Errorf("load judge model: %w", err)
	}
	workerModel, err := loadOpenAIModel(cfg.WorkerModelName)
	if err != nil {
		return nil, fmt.Errorf("load worker model: %w", err)
	}
	if cfg.CandidateTarget == "" {
		return nil, errors.New("candidate target is empty")
	}
	if (cfg.ParallelInferenceEnabled || cfg.ParallelEvaluationEnabled) && cfg.EvalCaseParallelism <= 0 {
		return nil, errors.New("eval case parallelism must be greater than 0 when parallel inference or evaluation is enabled")
	}
	targetStructure, err := fetchRemoteStructure(ctx, candidateAppName, cfg.CandidateTarget, cfg.CandidateBasePath)
	if err != nil {
		return nil, fmt.Errorf("fetch remote candidate structure: %w", err)
	}
	candidateRunner, err := trpcagentrunner.New(candidateAppName, trpcagentrunner.WithTarget(cfg.CandidateTarget), trpcagentrunner.WithBasePath(cfg.CandidateBasePath))
	if err != nil {
		return nil, fmt.Errorf("create remote candidate runner: %w", err)
	}
	judgeAgent := newJudgeAgent(judgeModel)
	backwarderAgent := newBackwarderAgent(workerModel)
	aggregatorAgent := newAggregatorAgent(workerModel)
	optimizerAgent := newOptimizerAgent(workerModel)
	judgeRunner := rootrunner.NewRunner(judgeAppName, judgeAgent)
	backwarderRunner := rootrunner.NewRunner(backwarderAppName, backwarderAgent)
	aggregatorRunner := rootrunner.NewRunner(aggregatorAppName, aggregatorAgent)
	optimizerRunner := rootrunner.NewRunner(optimizerAppName, optimizerAgent)
	closeAll := func() {
		candidateRunner.Close()
		judgeRunner.Close()
		backwarderRunner.Close()
		aggregatorRunner.Close()
		optimizerRunner.Close()
	}
	evalSetManager := evalsetlocal.New(evalset.WithBaseDir(cfg.DataDir))
	metricManager := metriclocal.New(metric.WithBaseDir(cfg.DataDir), metric.WithLocator(&sharedMetricLocator{metricFileID: sharedMetricFileID}))
	evalResultManager := evalresultlocal.New(evalresult.WithBaseDir(cfg.OutputDir))
	agentEvaluator, err := evaluation.New(appName, candidateRunner, evaluation.WithEvalSetManager(evalSetManager), evaluation.WithMetricManager(metricManager), evaluation.WithEvalResultManager(evalResultManager), evaluation.WithJudgeRunner(judgeRunner), evaluation.WithNumRuns(1))
	if err != nil {
		closeAll()
		return nil, fmt.Errorf("create evaluator: %w", err)
	}
	backwarderInstance, err := backwarder.New(ctx, backwarderRunner)
	if err != nil {
		agentEvaluator.Close()
		closeAll()
		return nil, fmt.Errorf("create backwarder: %w", err)
	}
	aggregatorInstance, err := aggregator.New(ctx, aggregatorRunner)
	if err != nil {
		agentEvaluator.Close()
		closeAll()
		return nil, fmt.Errorf("create aggregator: %w", err)
	}
	optimizerInstance, err := optimizer.New(ctx, optimizerRunner)
	if err != nil {
		agentEvaluator.Close()
		closeAll()
		return nil, fmt.Errorf("create optimizer: %w", err)
	}
	engineInstance, err := promptiterengine.New(
		ctx,
		promptiterengine.WithAgentEvaluator(agentEvaluator),
		promptiterengine.WithBackwarder(backwarderInstance),
		promptiterengine.WithAggregator(aggregatorInstance),
		promptiterengine.WithOptimizer(optimizerInstance),
		promptiterengine.WithStructureSnapshot(targetStructure),
	)
	if err != nil {
		agentEvaluator.Close()
		closeAll()
		return nil, fmt.Errorf("create promptiter engine: %w", err)
	}
	return &promptIterRuntime{engine: engineInstance, close: func() {
		agentEvaluator.Close()
		closeAll()
	}}, nil
}

func buildRunRequest(cfg remoteRunConfig, targetSurfaceID string) *promptiterengine.RunRequest {
	targetScore := cfg.TargetScore
	return &promptiterengine.RunRequest{
		Train:      []promptiterengine.EvalSetInput{{EvalSetID: trainEvalSetID}},
		Validation: []promptiterengine.EvalSetInput{{EvalSetID: validationEvalSetID}},
		EvaluationOptions: promptiterengine.EvaluationOptions{
			EvalCaseParallelism:               cfg.EvalCaseParallelism,
			EvalCaseParallelInferenceEnabled:  cfg.ParallelInferenceEnabled,
			EvalCaseParallelEvaluationEnabled: cfg.ParallelEvaluationEnabled,
		},
		BackwardOptions: promptiterengine.BackwardOptions{
			CaseParallelismEnabled: cfg.ParallelBackwardEnabled,
			CaseParallelism:        cfg.BackwardCaseParallelism,
		},
		AggregationOptions: promptiterengine.AggregationOptions{
			SurfaceParallelismEnabled: cfg.ParallelAggregationEnabled,
			SurfaceParallelism:        cfg.AggregationParallelism,
		},
		OptimizerOptions: promptiterengine.OptimizerOptions{
			SurfaceParallelismEnabled: cfg.ParallelOptimizerEnabled,
			SurfaceParallelism:        cfg.OptimizerParallelism,
		},
		AcceptancePolicy: promptiterengine.AcceptancePolicy{MinScoreGain: cfg.MinScoreGain},
		StopPolicy:       promptiterengine.StopPolicy{MaxRoundsWithoutAcceptance: cfg.MaxRoundsWithoutAcceptance, TargetScore: &targetScore},
		MaxRounds:        cfg.MaxRounds,
		TargetSurfaceIDs: []string{
			targetSurfaceID,
		},
	}
}

func (l *sharedMetricLocator) Build(baseDir, appName, _ string) string {
	return filepath.Join(baseDir, appName, l.metricFileID+".metrics.json")
}

func loadOpenAIModel(modelName string) (model.Model, error) {
	name := modelName
	apiKey := os.Getenv("OPENAI_API_KEY")
	baseURL := os.Getenv("OPENAI_BASE_URL")
	switch {
	case name == "":
		return nil, errors.New("model name is empty")
	case apiKey == "":
		return nil, errors.New("OPENAI_API_KEY is empty")
	}
	options := []openai.Option{openai.WithAPIKey(apiKey)}
	if baseURL != "" {
		options = append(options, openai.WithBaseURL(baseURL))
	}
	return openai.New(name, options...), nil
}
