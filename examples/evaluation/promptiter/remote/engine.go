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
	"time"

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
	promptitermanager "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/manager"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/optimizer"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	rootrunner "trpc.group/trpc-go/trpc-agent-go/runner"
	trpcagentrunner "trpc.group/trpc-go/trpc-agent-go/runner/trpcagent"
)

const (
	appName             = "promptiter-sports-recap-agent"
	candidateAppName    = appName
	judgeAppName        = "promptiter-sports-recap-judge"
	backwarderAppName   = "promptiter-sports-recap-backwarder"
	aggregatorAppName   = "promptiter-sports-recap-aggregator"
	optimizerAppName    = "promptiter-sports-recap-optimizer"
	trainEvalSetID      = "sports-recap-train"
	validationEvalSetID = "sports-recap-validation"
	sharedMetricFileID  = "sports-recap"
)

var targetSurfaceIDs = []string{
	"promptiter-sports-recap-agent/headline_agent#instruction",
	"promptiter-sports-recap-agent/highlights_agent#instruction",
	"promptiter-sports-recap-agent/stats_angle_agent#instruction",
	"promptiter-sports-recap-agent/recap_writer#instruction",
	"promptiter-sports-recap-agent/sports_editor#instruction",
}

type remoteRunConfig struct {
	DataDir                    string
	OutputDir                  string
	CandidateTarget            string
	CandidateBasePath          string
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
	PollInterval               time.Duration
}

type sharedMetricLocator struct {
	metricFileID string
}

type promptIterRuntime struct {
	manager promptitermanager.Manager
	close   func()
}

func runRemotePromptIterExample(ctx context.Context, cfg remoteRunConfig) error {
	result, targetSurfaceIDs, err := runRemotePromptIter(ctx, cfg)
	if err != nil {
		return err
	}
	if err := printSummary(
		result,
		cfg.DataDir,
		cfg.OutputDir,
		targetSurfaceIDs,
	); err != nil {
		return fmt.Errorf("print summary: %w", err)
	}
	return nil
}

func runRemotePromptIter(ctx context.Context, cfg remoteRunConfig) (*promptiterengine.RunResult, []string, error) {
	runtime, err := buildRemotePromptIterRuntime(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	defer runtime.close()
	if cfg.PollInterval <= 0 {
		return nil, nil, errors.New("poll interval must be greater than 0")
	}
	run, err := runtime.manager.Start(ctx, buildRunRequest(cfg, targetSurfaceIDs))
	if err != nil {
		return nil, nil, fmt.Errorf("start promptiter run: %w", err)
	}
	fmt.Printf("Started async remote run: %s\n", run.ID)
	result, err := waitForRun(ctx, runtime.manager, run.ID, cfg.PollInterval)
	if err != nil {
		return nil, nil, fmt.Errorf("wait for promptiter run: %w", err)
	}
	return result, targetSurfaceIDs, nil
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
	candidateRunner, err := trpcagentrunner.New(candidateAppName, trpcagentrunner.WithTarget(cfg.CandidateTarget), trpcagentrunner.WithBasePath(cfg.CandidateBasePath))
	if err != nil {
		return nil, fmt.Errorf("create remote candidate runner: %w", err)
	}
	targetStructure, err := candidateRunner.Describe(ctx)
	if err != nil {
		return nil, fmt.Errorf("describe remote candidate structure: %w", err)
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
		promptiterengine.WithStructure(targetStructure),
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
	managerInstance, err := promptitermanager.New(appName, engineInstance)
	if err != nil {
		agentEvaluator.Close()
		closeAll()
		return nil, fmt.Errorf("create promptiter manager: %w", err)
	}
	return &promptIterRuntime{manager: managerInstance, close: func() {
		managerInstance.Close()
		agentEvaluator.Close()
		closeAll()
	}}, nil
}

func buildRunRequest(
	cfg remoteRunConfig,
	targetSurfaceIDs []string,
) *promptiterengine.RunRequest {
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
		TargetSurfaceIDs: targetSurfaceIDs,
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

func waitForRun(
	ctx context.Context,
	manager promptitermanager.Manager,
	runID string,
	pollInterval time.Duration,
) (*promptiterengine.RunResult, error) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	reportedBaseline := false
	reportedTrainRounds := make(map[int]struct{})
	reportedValidationRounds := make(map[int]struct{})
	reportedCompletedRounds := make(map[int]struct{})
	for {
		run, err := manager.Get(ctx, runID)
		if err != nil {
			return nil, err
		}
		if run == nil {
			return nil, errors.New("manager returned nil run")
		}
		printProgress(runID, run)
		reportedBaseline = reportBaseline(runID, run, reportedBaseline)
		reportRoundMilestones(runID, run, reportedTrainRounds, reportedValidationRounds, reportedCompletedRounds)
		if isTerminalRunStatus(run.Status) {
			if err := terminalRunError(run); err != nil {
				return nil, err
			}
			return run, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func isTerminalRunStatus(status promptiterengine.RunStatus) bool {
	switch status {
	case promptiterengine.RunStatusSucceeded,
		promptiterengine.RunStatusFailed,
		promptiterengine.RunStatusCanceled:
		return true
	default:
		return false
	}
}

func terminalRunError(run *promptiterengine.RunResult) error {
	switch run.Status {
	case promptiterengine.RunStatusSucceeded:
		return nil
	case promptiterengine.RunStatusFailed:
		message := run.ErrorMessage
		if message == "" {
			message = "unknown error"
		}
		return fmt.Errorf("run %s failed: %s", run.ID, message)
	case promptiterengine.RunStatusCanceled:
		return fmt.Errorf("run %s canceled", run.ID)
	default:
		return nil
	}
}
