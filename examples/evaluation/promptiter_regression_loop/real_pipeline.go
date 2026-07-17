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

type realRuntime struct {
	engine         promptiterengine.Engine
	candidateModel model.Model
	judgeRunner    runner.Runner
	evalSetManager evalset.Manager
	metricManager  metric.Manager
	evalResultDir  string
	close          func()
}

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
	targetSurfaceID := astructure.SurfaceID(realCandidateAgentName, astructure.SurfaceTypeInstruction)
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
	critical := criticalCaseSet(input.Config.Gate.CriticalCaseIDs, input.ValidationEvalSet)
	firstRound := result.Rounds[0]
	candidatePrompt := promptFromProfile(input.BaselinePrompt, firstRound.OutputProfile)
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
	candidateTrain, err := evaluatePromptWithRealLLM(ctx, input, runtime, "candidate_train", input.TrainEvalSet, candidatePrompt)
	if err != nil {
		return nil, fmt.Errorf("evaluate candidate train: %w", err)
	}
	candidateValidation, err := evaluatePromptWithRealLLM(
		ctx, input, runtime, "candidate_validation", input.ValidationEvalSet, candidatePrompt,
	)
	if err != nil {
		return nil, fmt.Errorf("evaluate candidate validation: %w", err)
	}
	delta := ComputeDelta(baselineValidation, candidateValidation)
	totalCost := estimateRealCost(baselineTrain, baselineValidation, candidateTrain, candidateValidation)
	gate := DecideGate(input.Config.Gate, delta, totalCost)
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
		Candidate: CandidateSummary{
			ID:                   "promptiter-real-round-1",
			Description:          "Candidate generated by evaluation/workflow/promptiter engine using real LLM workers.",
			Prompt:               candidatePrompt,
			TrainEvaluation:      candidateTrain,
			ValidationEvaluation: candidateValidation,
		},
		Delta:              delta,
		Gate:               gate,
		FailureAttribution: summarizeFailures(candidateTrain, candidateValidation),
		Cost:               totalCost,
		Latency: LatencySummary{
			StartedAt:  startedAt,
			FinishedAt: finishedAt,
			DurationMs: finishedAt.Sub(startedAt).Milliseconds(),
		},
		Rounds: []RoundAudit{{
			Round:         firstRound.Round,
			CandidateID:   "promptiter-real-round-1",
			Losses:        firstRound.Losses,
			Patches:       firstRound.Patches,
			OutputProfile: firstRound.OutputProfile,
			Delta:         ComputeDelta(adaptPromptIterEvaluation("baseline_validation", result.BaselineValidation, critical), adaptPromptIterEvaluation("candidate_validation", firstRound.Validation, critical)),
			Gate:          gate,
			Cost:          totalCost,
		}},
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
	evalResultDir := outputDir(input)
	evalResultManager := evalresultlocal.New(evalresult.WithBaseDir(evalResultDir))
	agentEvaluator, err := evaluation.New(
		input.Config.AppName,
		candidateRunner,
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithMetricManager(metricManager),
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
		baseURL = "https://api.deepseek.com"
	}
	switch {
	case name == "":
		return nil, errors.New("model name is empty")
	case apiKey == "":
		return nil, errors.New("LLM_API_KEY, DEEPSEEK_API_KEY, DEEPSEEK_API_KEY1, or OPENAI_API_KEY is empty")
	}
	options := []openai.Option{openai.WithAPIKey(apiKey)}
	if baseURL != "" {
		options = append(options, openai.WithBaseURL(baseURL))
	}
	return openai.New(name, options...), nil
}
