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
	"strings"
	"time"

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
	promptitermanager "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/manager"
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
	sharedMetricFileID  = "sports-commentary"
)

type asyncRunConfig struct {
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
	ParallelInferenceEnabled   bool
	ParallelEvaluationEnabled  bool
	PollInterval               time.Duration
}

type sharedMetricLocator struct {
	metricFileID string
}

type promptIterRuntime struct {
	manager promptitermanager.Manager
	close   func()
}

func runAsyncRunExample(ctx context.Context, cfg asyncRunConfig) error {
	runtime, err := buildPromptIterRuntime(ctx, cfg)
	if err != nil {
		return err
	}
	defer runtime.close()
	if cfg.PollInterval <= 0 {
		return errors.New("poll interval must be greater than 0")
	}
	targetSurfaceID := astructure.SurfaceID(candidateAgentName, astructure.SurfaceTypeInstruction)
	run, err := runtime.manager.Start(ctx, buildRunRequest(cfg, targetSurfaceID))
	if err != nil {
		return fmt.Errorf("start promptiter run: %w", err)
	}
	fmt.Printf("Started async run: %s\n", run.ID)
	result, err := waitForRun(ctx, runtime.manager, run.ID, cfg.PollInterval)
	if err != nil {
		return fmt.Errorf("wait for promptiter run: %w", err)
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

func buildPromptIterRuntime(ctx context.Context, cfg asyncRunConfig) (*promptIterRuntime, error) {
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
	backwarderAgent := newBackwarderAgent(workerModel)
	aggregatorAgent := newAggregatorAgent(workerModel)
	optimizerAgent := newOptimizerAgent(workerModel)
	candidateRunner := runner.NewRunner(candidateAppName, candidateAgent)
	judgeRunner := runner.NewRunner(judgeAppName, judgeAgent)
	backwarderRunner := runner.NewRunner(backwarderAppName, backwarderAgent)
	aggregatorRunner := runner.NewRunner(aggregatorAppName, aggregatorAgent)
	optimizerRunner := runner.NewRunner(optimizerAppName, optimizerAgent)
	closeAll := func() {
		candidateRunner.Close()
		judgeRunner.Close()
		backwarderRunner.Close()
		aggregatorRunner.Close()
		optimizerRunner.Close()
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
		candidateRunner,
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithMetricManager(metricManager),
		evaluation.WithEvalResultManager(evalResultManager),
		evaluation.WithRegistry(registry),
		evaluation.WithJudgeRunner(judgeRunner),
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
	managerInstance, err := promptitermanager.New(engineInstance)
	if err != nil {
		agentEvaluator.Close()
		closeAll()
		return nil, fmt.Errorf("create promptiter manager: %w", err)
	}
	return &promptIterRuntime{
		manager: managerInstance,
		close: func() {
			managerInstance.Close()
			agentEvaluator.Close()
			closeAll()
		},
	}, nil
}

func buildRunRequest(cfg asyncRunConfig, targetSurfaceID string) *promptiterengine.RunRequest {
	targetScore := cfg.TargetScore
	return &promptiterengine.RunRequest{
		TrainEvalSetIDs:      []string{trainEvalSetID},
		ValidationEvalSetIDs: []string{validationEvalSetID},
		EvaluationOptions: promptiterengine.EvaluationOptions{
			EvalCaseParallelism:               cfg.EvalCaseParallelism,
			EvalCaseParallelInferenceEnabled:  cfg.ParallelInferenceEnabled,
			EvalCaseParallelEvaluationEnabled: cfg.ParallelEvaluationEnabled,
		},
		AcceptancePolicy: promptiterengine.AcceptancePolicy{
			MinScoreGain: cfg.MinScoreGain,
		},
		StopPolicy: promptiterengine.StopPolicy{
			MaxRoundsWithoutAcceptance: cfg.MaxRoundsWithoutAcceptance,
			TargetScore:                &targetScore,
		},
		MaxRounds:        cfg.MaxRounds,
		TargetSurfaceIDs: []string{targetSurfaceID},
	}
}

func waitForRun(
	ctx context.Context,
	manager promptitermanager.Manager,
	runID string,
	pollInterval time.Duration,
) (*promptiterengine.RunResult, error) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	lastProgress := ""
	reportedBaseline := false
	reportedValidationRounds := make(map[int]struct{})
	for {
		run, err := manager.Get(ctx, runID)
		if err != nil {
			return nil, err
		}
		if run == nil {
			return nil, errors.New("manager returned nil run")
		}
		progress := describeRunProgress(run)
		if progress != "" && progress != lastProgress {
			fmt.Printf("Run %s progress: %s\n", runID, progress)
			lastProgress = progress
		}
		if run.BaselineValidation != nil && !reportedBaseline {
			fmt.Printf("Run %s baseline validation score: %.2f\n", runID, run.BaselineValidation.OverallScore)
			reportedBaseline = true
		}
		for i := range run.Rounds {
			round := &run.Rounds[i]
			if round.Validation == nil {
				continue
			}
			if _, ok := reportedValidationRounds[round.Round]; ok {
				continue
			}
			fmt.Printf("Run %s round %d validation score: %.2f\n", runID, round.Round, round.Validation.OverallScore)
			reportedValidationRounds[round.Round] = struct{}{}
		}
		if isTerminalRunStatus(run.Status) {
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

func describeRunProgress(run *promptiterengine.RunResult) string {
	if run == nil {
		return ""
	}
	switch run.Status {
	case promptiterengine.RunStatusQueued:
		return "queued"
	case promptiterengine.RunStatusRunning:
	default:
		return string(run.Status)
	}
	if run.BaselineValidation == nil {
		return "baseline validation"
	}
	if run.CurrentRound == 0 {
		return "waiting to start round 1"
	}
	round := currentRoundResult(run, run.CurrentRound)
	if round == nil {
		return fmt.Sprintf("round %d started", run.CurrentRound)
	}
	if round.Train == nil {
		return fmt.Sprintf("round %d train evaluation", round.Round)
	}
	if round.Losses == nil {
		return fmt.Sprintf("round %d terminal loss extraction", round.Round)
	}
	if round.Backward == nil {
		return fmt.Sprintf("round %d backward pass", round.Round)
	}
	if round.Aggregation == nil {
		return fmt.Sprintf("round %d gradient aggregation", round.Round)
	}
	if round.Patches == nil {
		return fmt.Sprintf("round %d optimizer", round.Round)
	}
	if round.OutputProfile == nil {
		return fmt.Sprintf("round %d applying patch set", round.Round)
	}
	if round.Validation == nil {
		return fmt.Sprintf("round %d validation evaluation", round.Round)
	}
	if round.Acceptance == nil || round.Stop == nil {
		return fmt.Sprintf("round %d acceptance and stop checks", round.Round)
	}
	if round.Acceptance.Accepted {
		return fmt.Sprintf("round %d completed and accepted", round.Round)
	}
	return fmt.Sprintf("round %d completed and rejected", round.Round)
}

func currentRoundResult(run *promptiterengine.RunResult, roundNumber int) *promptiterengine.RoundResult {
	for i := range run.Rounds {
		if run.Rounds[i].Round != roundNumber {
			continue
		}
		return &run.Rounds[i]
	}
	return nil
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
