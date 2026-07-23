//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
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

	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metriclocal "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
	metricregistry "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/backwarder"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/optimizer"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

type realRuntime struct {
	engine         promptiterengine.Engine
	candidateModel model.Model
	judgeRunner    runner.Runner
	evalSetManager evalset.Manager
	metricManager  metric.Manager
	metricRegistry metricregistry.Registry
	evalResultDir  string
	close          func()
}

const (
	defaultLLMBaseURL = "https://api.deepseek.com"
	defaultLLMModel   = "deepseek-chat"
)

// RunRealLLMPipeline executes the same audit loop with real PromptIter and LLM runners.
func RunRealLLMPipeline(ctx context.Context, input *LoadedInput) (*OptimizationReport, error) {
	if input == nil {
		return nil, errors.New("input is nil")
	}
	startedAt := time.Now()
	runtime, err := buildRealRuntime(ctx, input)
	if err != nil {
		return nil, err
	}
	defer runtime.close()
	targetSurfaceID := resolveTargetSurfaceID(input.Config.TargetSurfaceID)
	result, err := runtime.engine.Run(ctx, &promptiterengine.RunRequest{
		Train: []promptiterengine.EvalSetInput{{
			EvalSetID: input.TrainEvalSet.EvalSetID,
		}},
		Validation: []promptiterengine.EvalSetInput{{
			EvalSetID: input.ValidationEvalSet.EvalSetID,
		}},
		EvaluationOptions: promptiterengine.EvaluationOptions{
			EvalCaseParallelism:               1,
			EvalCaseParallelInferenceEnabled:  false,
			EvalCaseParallelEvaluationEnabled: false,
		},
		AcceptancePolicy: promptiterengine.AcceptancePolicy{
			MinScoreGain: input.Config.Gate.MinValidationGain,
		},
		StopPolicy: promptiterengine.StopPolicy{
			MaxRoundsWithoutAcceptance: input.Config.MaxRounds,
		},
		MaxRounds:        input.Config.MaxRounds,
		TargetSurfaceIDs: []string{targetSurfaceID},
	})
	if err != nil {
		return nil, fmt.Errorf("run real promptiter engine: %w", err)
	}
	if len(result.Rounds) == 0 {
		return nil, errors.New("real promptiter produced no rounds")
	}
	baselineTrain, err := evaluatePromptWithRealLLM(ctx, input, runtime, "baseline_train", input.TrainEvalSet, input.BaselinePrompt)
	if err != nil {
		return nil, fmt.Errorf("evaluate baseline train: %w", err)
	}
	baselineValidation, err := evaluatePromptWithRealLLM(
		ctx, input, runtime, "baseline_validation", input.ValidationEvalSet, input.BaselinePrompt,
	)
	if err != nil {
		return nil, fmt.Errorf("evaluate baseline validation: %w", err)
	}
	totalCost := estimateRealCost(baselineTrain, baselineValidation)
	var selection candidateSelection
	roundAudits := make([]RoundAudit, 0, len(result.Rounds))
	for _, roundResult := range result.Rounds {
		roundStart := time.Now()
		candidateID := fmt.Sprintf("promptiter-real-round-%d", roundResult.Round)
		candidatePrompt := promptFromProfile(input.BaselinePrompt, roundResult.OutputProfile, targetSurfaceID)
		candidateTrain, err := evaluatePromptWithRealLLM(ctx, input, runtime, candidateID+"_train", input.TrainEvalSet, candidatePrompt)
		if err != nil {
			return nil, fmt.Errorf("evaluate %s train: %w", candidateID, err)
		}
		candidateValidation, err := evaluatePromptWithRealLLM(
			ctx, input, runtime, candidateID+"_validation", input.ValidationEvalSet, candidatePrompt,
		)
		if err != nil {
			return nil, fmt.Errorf("evaluate %s validation: %w", candidateID, err)
		}
		roundCost := estimateRealCost(candidateTrain, candidateValidation)
		totalCost = addCost(totalCost, roundCost)
		delta := ComputeDelta(baselineValidation, candidateValidation)
		gate := DecideGate(input.Config.Gate, delta, totalCost)
		roundAudits = append(roundAudits, RoundAudit{
			Round:         roundResult.Round,
			CandidateID:   candidateID,
			Losses:        roundResult.Losses,
			Patches:       roundResult.Patches,
			OutputProfile: roundResult.OutputProfile,
			Delta:         delta,
			Gate:          gate,
			Cost:          roundCost,
			LatencyMs:     time.Since(roundStart).Milliseconds(),
		})
		summary := CandidateSummary{
			ID:                   candidateID,
			Description:          "Candidate generated by evaluation/workflow/promptiter engine using real LLM workers.",
			Prompt:               candidatePrompt,
			TrainEvaluation:      candidateTrain,
			ValidationEvaluation: candidateValidation,
		}
		if selection.consider(summary, delta, gate) {
			break
		}
	}
	if !selection.ok {
		return nil, errors.New("real promptiter produced no candidate profiles")
	}
	finishedAt := time.Now()
	report := &OptimizationReport{
		RunID:              fmt.Sprintf("%s-%d-real", input.Config.AppName, input.Config.Seed),
		AppName:            input.Config.AppName,
		Mode:               "real_llm",
		DataSource:         "real LLM via OpenAI-compatible endpoint; evalsets remain local reproducible fixtures",
		Seed:               input.Config.Seed,
		TargetSurfaceID:    targetSurfaceID,
		PromptSource:       filepath.ToSlash(input.Config.PromptSource),
		FakeEngine:         FakeEngineConfig{Name: "real-llm", Model: input.Config.LLM.CandidateModel, TraceMode: true, Determinism: "temperature-controlled"},
		BaselinePrompt:     input.BaselinePrompt,
		BaselineTrain:      baselineTrain,
		BaselineValidation: baselineValidation,
		Candidate:          selection.summary,
		Delta:              selection.delta,
		Gate:               selection.gate,
		FailureAttribution: summarizeFailures(selection.summary.TrainEvaluation, selection.summary.ValidationEvaluation),
		Cost:               totalCost,
		Latency: LatencySummary{
			StartedAt:  startedAt,
			FinishedAt: finishedAt,
			DurationMs: finishedAt.Sub(startedAt).Milliseconds(),
		},
		Rounds: roundAudits,
	}
	return report, nil
}

func buildRealRuntime(ctx context.Context, input *LoadedInput) (*realRuntime, error) {
	cfg := input.Config.LLM
	candidateModel, err := loadOpenAIModel(cfg.CandidateModel)
	if err != nil {
		return nil, fmt.Errorf("load candidate model: %w", err)
	}
	judgeModel, err := loadOpenAIModel(cfg.JudgeModel)
	if err != nil {
		return nil, fmt.Errorf("load judge model: %w", err)
	}
	workerModel, err := loadOpenAIModel(cfg.WorkerModel)
	if err != nil {
		return nil, fmt.Errorf("load worker model: %w", err)
	}
	candidateAgent, err := newRealCandidateAgent(candidateModel, input.BaselinePrompt, cfg)
	if err != nil {
		return nil, fmt.Errorf("create candidate agent: %w", err)
	}
	judgeAgent := newRealJudgeAgent(judgeModel, cfg)
	backwarderAgent := newRealBackwarderAgent(workerModel, cfg)
	aggregatorAgent := newRealAggregatorAgent(workerModel, cfg)
	optimizerAgent := newRealOptimizerAgent(workerModel, cfg)
	candidateRunner := runner.NewRunner(input.Config.AppName+"-candidate", candidateAgent)
	judgeRunner := runner.NewRunner(input.Config.AppName+"-judge", judgeAgent)
	backwarderRunner := runner.NewRunner(input.Config.AppName+"-backwarder", backwarderAgent)
	aggregatorRunner := runner.NewRunner(input.Config.AppName+"-aggregator", aggregatorAgent)
	optimizerRunner := runner.NewRunner(input.Config.AppName+"-optimizer", optimizerAgent)
	closeAll := func() {
		candidateRunner.Close()
		judgeRunner.Close()
		backwarderRunner.Close()
		aggregatorRunner.Close()
		optimizerRunner.Close()
	}
	evalSetManager := evalsetlocal.New(evalset.WithBaseDir(input.ConfigDir), evalset.WithLocator(newConfigEvalSetLocator(input)))
	metricManager := metriclocal.New(metric.WithBaseDir(input.ConfigDir), metric.WithLocator(newConfigMetricLocator(input)))
	metricRegistry, err := newRegressionMetricRegistry()
	if err != nil {
		closeAll()
		return nil, fmt.Errorf("create metric registry: %w", err)
	}
	evalResultDir := outputDir(input)
	evalResultManager := evalresultlocal.New(evalresult.WithBaseDir(evalResultDir))
	agentEvaluator, err := evaluation.New(
		input.Config.AppName,
		candidateRunner,
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithMetricManager(metricManager),
		evaluation.WithMetricRegistry(metricRegistry),
		evaluation.WithEvalResultManager(evalResultManager),
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
	return &realRuntime{
		engine:         engineInstance,
		candidateModel: candidateModel,
		judgeRunner:    judgeRunner,
		evalSetManager: evalSetManager,
		metricManager:  metricManager,
		metricRegistry: metricRegistry,
		evalResultDir:  evalResultDir,
		close: func() {
			agentEvaluator.Close()
			closeAll()
		},
	}, nil
}

func evaluatePromptWithRealLLM(
	ctx context.Context,
	input *LoadedInput,
	runtime *realRuntime,
	name string,
	set EvalSetInput,
	prompt string,
) (EvaluationRun, error) {
	agentInstance, err := newRealCandidateAgent(runtime.candidateModel, prompt, input.Config.LLM)
	if err != nil {
		return EvaluationRun{}, err
	}
	candidateRunner := runner.NewRunner(input.Config.AppName+"-"+name, agentInstance)
	defer candidateRunner.Close()
	evalResultManager := evalresultlocal.New(evalresult.WithBaseDir(runtime.evalResultDir))
	defer evalResultManager.Close()
	evaluator, err := evaluation.New(
		input.Config.AppName,
		candidateRunner,
		evaluation.WithEvalSetManager(runtime.evalSetManager),
		evaluation.WithMetricManager(runtime.metricManager),
		evaluation.WithMetricRegistry(runtime.metricRegistry),
		evaluation.WithEvalResultManager(evalResultManager),
		evaluation.WithJudgeRunner(runtime.judgeRunner),
		evaluation.WithNumRuns(1),
	)
	if err != nil {
		return EvaluationRun{}, err
	}
	defer evaluator.Close()
	result, err := evaluator.Evaluate(ctx, set.EvalSetID, evaluation.WithRunDetailsEnabled(true))
	if err != nil {
		return EvaluationRun{}, err
	}
	return adaptEvaluationResult(name, result, criticalCaseSet(input.Config.Gate.CriticalCaseIDs, input.ValidationEvalSet)), nil
}

func loadOpenAIModel(modelName string) (model.Model, error) {
	name := strings.TrimSpace(modelName)
	if envModel := strings.TrimSpace(os.Getenv("LLM_MODEL")); envModel != "" {
		name = envModel
	}
	if name == "" {
		name = defaultLLMModel
	}
	apiKey := strings.TrimSpace(os.Getenv("LLM_API_KEY"))
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
	}
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY1"))
	}
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	}
	baseURL := strings.TrimSpace(os.Getenv("LLM_BASE_URL"))
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv("DEEPSEEK_BASE_URL"))
	}
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
	}
	if baseURL == "" {
		baseURL = defaultLLMBaseURL
	}
	switch {
	case apiKey == "":
		return nil, errors.New("LLM_API_KEY, DEEPSEEK_API_KEY, DEEPSEEK_API_KEY1, or OPENAI_API_KEY is empty")
	}
	options := []openai.Option{openai.WithAPIKey(apiKey)}
	if baseURL != "" {
		options = append(options, openai.WithBaseURL(baseURL))
	}
	return openai.New(name, options...), nil
}
