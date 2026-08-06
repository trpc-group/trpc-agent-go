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
	"log"
	"os"
	"path/filepath"
	"strings"

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
	"trpc.group/trpc-go/trpc-agent-go/runner"
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
)

// sharedMetricFileID is the metric file used by every eval set in this example.
// It is a var (not const) so a pipeline config can override it.
var sharedMetricFileID = "sports-commentary"

type regressionConfig struct {
	DataDir                    string
	OutputDir                  string
	CandidateModelName         string
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
	Fake                       bool
	FakeScenario               fakeScenario
	KeyCaseIDs                 []string
	PromptType                 string
	TargetSurfaces             []string
	TrainEvalSetID             string
	ValidationEvalSetID        string
	MetricFileID               string
	CostPerEval                float64
	CostPerWorker              float64
	CostBudget                 float64
	Seed                       int
	Attribution                string
	AttributionModelName       string
	DebugIO                    bool
	Logger                     *log.Logger
}

type sharedMetricLocator struct {
	metricFileID string
}

type promptIterRuntime struct {
	engine promptiterengine.Engine
	close  func()
}

// runEngine builds the PromptIter runtime and runs a single optimization pass,
// returning the full run result (baseline validation + per-round train/validation
// evals + accepted profile) for the regression loop to consume.
func runEngine(
	ctx context.Context,
	cfg regressionConfig,
) (*promptiterengine.RunResult, string, error) {
	runtime, err := buildPromptIterRuntime(ctx, cfg)
	if err != nil {
		return nil, "", err
	}
	defer runtime.close()
	targetSurfaceIDs := resolveTargetSurfaceIDs(cfg)
	result, err := runtime.engine.Run(ctx, buildRunRequest(cfg, targetSurfaceIDs))
	if err != nil {
		return nil, "", fmt.Errorf("run promptiter: %w", err)
	}
	targetSurfaceID := ""
	if len(targetSurfaceIDs) > 0 {
		targetSurfaceID = targetSurfaceIDs[0]
	}
	return result, targetSurfaceID, nil
}

// resolveTargetSurfaceIDs maps the configured prompt type (or explicit surface
// ids) to the PromptIter target surfaces. This is what lets the pipeline optimize
// system / agent / skill / router prompts: the engine treats each as a surface
// identified by "<node>#<surfaceType>". When TargetSurfaces is set it is used
// verbatim (so any app-specific surface, e.g. a router prompt, can be targeted).
func resolveTargetSurfaceIDs(cfg regressionConfig) []string {
	if len(cfg.TargetSurfaces) > 0 {
		return cfg.TargetSurfaces
	}
	var st astructure.SurfaceType
	switch strings.ToLower(strings.TrimSpace(cfg.PromptType)) {
	case "system", "global", "global_instruction":
		st = astructure.SurfaceTypeGlobalInstruction
	case "skill":
		st = astructure.SurfaceTypeSkill
	case "router":
		st = astructure.SurfaceType("router")
	default: // "agent" / "instruction" / unset
		st = astructure.SurfaceTypeInstruction
	}
	return []string{astructure.SurfaceID(candidateAgentName, st)}
}

func buildPromptIterRuntime(ctx context.Context, cfg regressionConfig) (*promptIterRuntime, error) {
	candidateModel, err := loadOpenAIModel(cfg.CandidateModelName, fakeRoleCandidate, cfg.Fake, cfg.FakeScenario)
	if err != nil {
		return nil, fmt.Errorf("load candidate model: %w", err)
	}
	judgeModel, err := loadOpenAIModel(cfg.JudgeModelName, fakeRoleJudge, cfg.Fake, cfg.FakeScenario)
	if err != nil {
		return nil, fmt.Errorf("load judge model: %w", err)
	}
	workerModel, err := loadOpenAIModel(cfg.WorkerModelName, fakeRoleWorker, cfg.Fake, cfg.FakeScenario)
	if err != nil {
		return nil, fmt.Errorf("load worker model: %w", err)
	}
	if (cfg.ParallelInferenceEnabled || cfg.ParallelEvaluationEnabled) && cfg.EvalCaseParallelism <= 0 {
		return nil, errors.New("eval case parallelism must be greater than 0 when parallel inference or evaluation is enabled")
	}
	candidateAgent, err := newCandidateAgent(candidateModel, cfg.CandidateInstruction)
	if err != nil {
		return nil, fmt.Errorf("create candidate agent: %w", err)
	}
	judgeAgent := newJudgeAgent(judgeModel)
	backwarderAgent := newBackwarderAgent(workerModel)
	aggregatorAgent := newAggregatorAgent(workerModel)
	optimizerAgent := newOptimizerAgent(workerModel)
	candidateRunner := runner.NewRunner(candidateAppName, candidateAgent)
	judgeRunner := runner.NewRunner(judgeAppName, judgeAgent)
	backwarderBaseRunner := runner.NewRunner(backwarderAppName, backwarderAgent)
	aggregatorBaseRunner := runner.NewRunner(aggregatorAppName, aggregatorAgent)
	optimizerBaseRunner := runner.NewRunner(optimizerAppName, optimizerAgent)
	logger := cfg.Logger
	candidateLoggedRunner := newLoggingRunner("candidate", candidateRunner, logger, cfg.DebugIO)
	judgeLoggedRunner := newLoggingRunner("judge", judgeRunner, logger, cfg.DebugIO)
	backwarderRunner := newLoggingRunner("backwarder", backwarderBaseRunner, logger, cfg.DebugIO)
	aggregatorRunner := newLoggingRunner("aggregator", aggregatorBaseRunner, logger, cfg.DebugIO)
	optimizerRunner := newLoggingRunner("optimizer", optimizerBaseRunner, logger, cfg.DebugIO)
	closeAll := func() {
		candidateRunner.Close()
		judgeRunner.Close()
		backwarderBaseRunner.Close()
		aggregatorBaseRunner.Close()
		optimizerBaseRunner.Close()
	}
	evalSetManager := evalsetlocal.New(evalset.WithBaseDir(cfg.DataDir))
	metricManager := metriclocal.New(
		metric.WithBaseDir(cfg.DataDir),
		metric.WithLocator(&sharedMetricLocator{metricFileID: sharedMetricFileID}),
	)
	evalResultManager := evalresultlocal.New(evalresult.WithBaseDir(cfg.OutputDir))
	agentEvaluator, err := evaluation.New(
		appName,
		candidateLoggedRunner,
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithMetricManager(metricManager),
		evaluation.WithEvalResultManager(evalResultManager),
		evaluation.WithJudgeRunner(judgeLoggedRunner),
		evaluation.WithNumRuns(1),
	)
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
		promptiterengine.WithAgent(candidateAgent),
		promptiterengine.WithAgentEvaluator(agentEvaluator),
		promptiterengine.WithBackwarder(backwarderInstance),
		promptiterengine.WithAggregator(aggregatorInstance),
		promptiterengine.WithOptimizer(optimizerInstance),
	)
	if err != nil {
		agentEvaluator.Close()
		closeAll()
		return nil, fmt.Errorf("create promptiter engine: %w", err)
	}
	return &promptIterRuntime{
		engine: engineInstance,
		close: func() {
			agentEvaluator.Close()
			closeAll()
		},
	}, nil
}

func buildRunRequest(
	cfg regressionConfig,
	targetSurfaceIDs []string,
) *promptiterengine.RunRequest {
	targetScore := cfg.TargetScore
	return &promptiterengine.RunRequest{
		Train: []promptiterengine.EvalSetInput{
			{
				EvalSetID: cfg.TrainEvalSetID,
			},
		},
		Validation: []promptiterengine.EvalSetInput{
			{
				EvalSetID: cfg.ValidationEvalSetID,
			},
		},
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
		AcceptancePolicy: promptiterengine.AcceptancePolicy{
			MinScoreGain: cfg.MinScoreGain,
		},
		StopPolicy: promptiterengine.StopPolicy{
			MaxRoundsWithoutAcceptance: cfg.MaxRoundsWithoutAcceptance,
			TargetScore:                &targetScore,
		},
		MaxRounds:        cfg.MaxRounds,
		TargetSurfaceIDs: targetSurfaceIDs,
	}
}

// Build maps every eval set to the shared metric file used by the example.
func (l *sharedMetricLocator) Build(baseDir, appName, _ string) string {
	return filepath.Join(baseDir, appName, l.metricFileID+".metrics.json")
}

func loadOpenAIModel(modelName string, role fakeRole, fake bool, scenario fakeScenario) (model.Model, error) {
	name := strings.TrimSpace(modelName)
	if name == "" {
		return nil, errors.New("model name is empty")
	}
	if fake {
		return newFakeModel(name, role, scenario), nil
	}
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	baseURL := strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
	if apiKey == "" {
		return nil, errors.New("OPENAI_API_KEY is empty")
	}
	options := make([]openai.Option, 0, 2)
	options = append(options, openai.WithAPIKey(apiKey))
	if baseURL != "" {
		options = append(options, openai.WithBaseURL(baseURL))
	}
	return openai.New(name, options...), nil
}
