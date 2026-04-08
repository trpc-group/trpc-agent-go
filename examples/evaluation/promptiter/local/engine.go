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
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
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
	promptIterWorkerApp = "promptiter-nba-commentary-worker"
	trainEvalSetID      = "nba-commentary-train"
	validationEvalSetID = "nba-commentary-validation"
	sharedMetricFileID  = "sports-commentary"
)

type runConfig struct {
	DataDir                   string
	OutputDir                 string
	CandidateModelName        string
	CandidateInstruction      string
	JudgeModelName            string
	WorkerModelName           string
	NumRuns                   int
	MaxRounds                 int
	EvalCaseParallelism       int
	ParallelInferenceEnabled  bool
	ParallelEvaluationEnabled bool
	DebugIO                   bool
	Logger                    *log.Logger
}

type sharedMetricLocator struct {
	metricFileID string
}

type promptIterRuntime struct {
	engine promptiterengine.Engine
	close  func()
}

func runPromptIterExample(ctx context.Context, cfg runConfig) error {
	runtime, err := buildPromptIterRuntime(ctx, cfg)
	if err != nil {
		return err
	}
	defer runtime.close()
	targetSurfaceID := astructure.SurfaceID(candidateAgentName, astructure.SurfaceTypeInstruction)
	result, err := runtime.engine.Run(ctx, buildRunRequest(cfg, targetSurfaceID))
	if err != nil {
		return fmt.Errorf("run promptiter: %w", err)
	}
	if err := printSummary(
		result,
		cfg.DataDir,
		cfg.OutputDir,
		cfg.CandidateInstruction,
		targetSurfaceID,
	); err != nil {
		return fmt.Errorf("print summary: %w", err)
	}
	return nil
}

func buildPromptIterRuntime(ctx context.Context, cfg runConfig) (*promptIterRuntime, error) {
	candidateModel, err := loadOpenAIModel(cfg.CandidateModelName)
	if err != nil {
		return nil, fmt.Errorf("load candidate model: %w", err)
	}
	judgeModel, err := loadOpenAIModel(cfg.JudgeModelName)
	if err != nil {
		return nil, fmt.Errorf("load judge model: %w", err)
	}
	workerModel, err := loadOpenAIModel(cfg.WorkerModelName)
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
	teacherAgent := newTeacherAgent(judgeModel)
	promptIterWorker := newPromptIterWorkerAgent(workerModel)
	candidateRunner := runner.NewRunner(candidateAppName, candidateAgent)
	judgeRunner := runner.NewRunner(judgeAppName, judgeAgent)
	teacherRunner := runner.NewRunner("promptiter-nba-commentary-teacher", teacherAgent)
	workerRunner := runner.NewRunner(promptIterWorkerApp, promptIterWorker)
	logger := cfg.Logger
	candidateLoggedRunner := newLoggingRunner("candidate", candidateRunner, logger, cfg.DebugIO)
	judgeLoggedRunner := newLoggingRunner("judge", judgeRunner, logger, cfg.DebugIO)
	teacherLoggedRunner := newLoggingRunner("teacher", teacherRunner, logger, cfg.DebugIO)
	backwarderRunner := newLoggingRunner("backwarder", workerRunner, logger, cfg.DebugIO)
	aggregatorRunner := newLoggingRunner("aggregator", workerRunner, logger, cfg.DebugIO)
	optimizerRunner := newLoggingRunner("optimizer", workerRunner, logger, cfg.DebugIO)
	closeAll := func() {
		candidateRunner.Close()
		judgeRunner.Close()
		teacherRunner.Close()
		workerRunner.Close()
	}
	evalSetManager := evalsetlocal.New(evalset.WithBaseDir(cfg.DataDir))
	metricManager := metriclocal.New(
		metric.WithBaseDir(cfg.DataDir),
		metric.WithLocator(&sharedMetricLocator{metricFileID: sharedMetricFileID}),
	)
	evalResultManager := evalresultlocal.New(evalresult.WithBaseDir(cfg.OutputDir))
	registry := registry.New()
	if err := registry.Register(commentaryLengthMetricName, newCommentaryLengthEvaluator()); err != nil {
		closeAll()
		return nil, fmt.Errorf("register commentary length evaluator: %w", err)
	}
	agentEvaluator, err := evaluation.New(
		appName,
		candidateLoggedRunner,
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithMetricManager(metricManager),
		evaluation.WithEvalResultManager(evalResultManager),
		evaluation.WithRegistry(registry),
		evaluation.WithExpectedRunner(teacherLoggedRunner),
		evaluation.WithJudgeRunner(judgeLoggedRunner),
		evaluation.WithNumRuns(cfg.NumRuns),
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
		candidateAgent,
		agentEvaluator,
		backwarderInstance,
		aggregatorInstance,
		optimizerInstance,
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

func buildRunRequest(cfg runConfig, targetSurfaceID string) *promptiterengine.RunRequest {
	targetScore := 1.0
	return &promptiterengine.RunRequest{
		TrainEvalSetIDs:      []string{trainEvalSetID},
		ValidationEvalSetIDs: []string{validationEvalSetID},
		EvaluationOptions: promptiterengine.EvaluationOptions{
			NumRuns:                           cfg.NumRuns,
			EvalCaseParallelism:               cfg.EvalCaseParallelism,
			EvalCaseParallelInferenceEnabled:  cfg.ParallelInferenceEnabled,
			EvalCaseParallelEvaluationEnabled: cfg.ParallelEvaluationEnabled,
		},
		AcceptancePolicy: promptiterengine.AcceptancePolicy{
			MinScoreGain: 0.005,
		},
		StopPolicy: promptiterengine.StopPolicy{
			MaxRoundsWithoutAcceptance: 5,
			TargetScore:                &targetScore,
		},
		MaxRounds:        cfg.MaxRounds,
		TargetSurfaceIDs: []string{targetSurfaceID},
	}
}

// Build maps every eval set to the shared metric file used by the example.
func (l *sharedMetricLocator) Build(baseDir, appName, _ string) string {
	return filepath.Join(baseDir, appName, l.metricFileID+".metrics.json")
}

func loadOpenAIModel(modelName string) (model.Model, error) {
	name := strings.TrimSpace(modelName)
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	baseURL := strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
	switch {
	case name == "":
		return nil, errors.New("model name is empty")
	case apiKey == "":
		return nil, errors.New("OPENAI_API_KEY is empty")
	}
	options := make([]openai.Option, 0, 2)
	options = append(options, openai.WithAPIKey(apiKey))
	if baseURL != "" {
		options = append(options, openai.WithBaseURL(baseURL))
	}
	return openai.New(name, options...), nil
}
