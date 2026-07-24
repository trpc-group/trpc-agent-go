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
	"path/filepath"
	"sort"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	evalresultinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metriclocal "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/backwarder"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/optimizer"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const (
	appName             = "promptiter-regression-loop-app"
	candidateAgentName  = "regression-candidate"
	trainEvalSetID      = "city-service-train"
	validationEvalSetID = "city-service-validation"
)

type pipelineRuntime struct {
	evaluator      evaluation.AgentEvaluator
	engine         promptiterengine.Engine
	candidateModel *deterministicModel
	runners        []runner.Runner
}

func buildRuntime(
	ctx context.Context,
	cfg *config,
	baselineInstruction string,
	recorder *accountingRecorder,
) (*pipelineRuntime, error) {
	targetSurfaceID := astructure.SurfaceID(candidateAgentName, astructure.SurfaceTypeInstruction)
	candidateModel := newCandidateModel(cfg.Scenario, recorder)
	workerModel := newWorkerModel(cfg.Scenario, targetSurfaceID, recorder)
	candidateAgent := llmagent.New(
		candidateAgentName,
		llmagent.WithModel(candidateModel),
		llmagent.WithInstruction(baselineInstruction),
		llmagent.WithTools(cityServiceTools()),
		llmagent.WithGenerationConfig(deterministicGenerationConfig()),
	)
	backwardAgent := newWorkerAgent("regression-backwarder", workerModel)
	aggregatorAgent := newWorkerAgent("regression-aggregator", workerModel)
	optimizerAgent := newWorkerAgent("regression-optimizer", workerModel)

	candidateRunner := runner.NewRunner(appName+"-candidate", candidateAgent)
	backwardRunner := runner.NewRunner(appName+"-backwarder", backwardAgent)
	aggregatorRunner := runner.NewRunner(appName+"-aggregator", aggregatorAgent)
	optimizerRunner := runner.NewRunner(appName+"-optimizer", optimizerAgent)
	runners := []runner.Runner{candidateRunner, backwardRunner, aggregatorRunner, optimizerRunner}
	closeRunners := func() {
		for _, current := range runners {
			_ = current.Close()
		}
	}

	evalSetManager := evalsetlocal.New(evalset.WithLocator(&fixedEvalSetLocator{paths: map[string]string{
		trainEvalSetID:      cfg.Inputs.TrainEvalset,
		validationEvalSetID: cfg.Inputs.ValidationEvalset,
	}}))
	metricManager := metriclocal.New(metric.WithLocator(&fixedMetricLocator{path: cfg.Inputs.Metrics}))
	evaluator, err := evaluation.New(
		appName,
		candidateRunner,
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithMetricManager(metricManager),
		evaluation.WithEvalResultManager(evalresultinmemory.New()),
		evaluation.WithNumRuns(1),
	)
	if err != nil {
		closeRunners()
		return nil, fmt.Errorf("create evaluator: %w", err)
	}
	backwardInstance, err := backwarder.New(ctx, backwardRunner)
	if err != nil {
		_ = evaluator.Close()
		closeRunners()
		return nil, fmt.Errorf("create backwarder: %w", err)
	}
	aggregatorInstance, err := aggregator.New(ctx, aggregatorRunner)
	if err != nil {
		_ = evaluator.Close()
		closeRunners()
		return nil, fmt.Errorf("create aggregator: %w", err)
	}
	optimizerInstance, err := optimizer.New(ctx, optimizerRunner)
	if err != nil {
		_ = evaluator.Close()
		closeRunners()
		return nil, fmt.Errorf("create optimizer: %w", err)
	}
	engine, err := promptiterengine.New(
		ctx,
		promptiterengine.WithAgent(candidateAgent),
		promptiterengine.WithAgentEvaluator(evaluator),
		promptiterengine.WithBackwarder(backwardInstance),
		promptiterengine.WithAggregator(aggregatorInstance),
		promptiterengine.WithOptimizer(optimizerInstance),
	)
	if err != nil {
		_ = evaluator.Close()
		closeRunners()
		return nil, fmt.Errorf("create promptiter engine: %w", err)
	}
	return &pipelineRuntime{
		evaluator:      evaluator,
		engine:         engine,
		candidateModel: candidateModel,
		runners:        runners,
	}, nil
}

func newWorkerAgent(name string, workerModel model.Model) agent.Agent {
	return llmagent.New(
		name,
		llmagent.WithModel(workerModel),
		llmagent.WithGenerationConfig(deterministicGenerationConfig()),
	)
}

func deterministicGenerationConfig() model.GenerationConfig {
	maxTokens := 4096
	temperature := 0.0
	stream := false
	return model.GenerationConfig{
		MaxTokens:   &maxTokens,
		Temperature: &temperature,
		Stream:      stream,
	}
}

func (r *pipelineRuntime) close() {
	if r == nil {
		return
	}
	if r.evaluator != nil {
		_ = r.evaluator.Close()
	}
	for _, current := range r.runners {
		_ = current.Close()
	}
}

func (r *pipelineRuntime) evaluate(
	ctx context.Context,
	evalSetID string,
	instruction string,
	stage string,
) (*evaluation.EvaluationResult, error) {
	if r == nil || r.evaluator == nil {
		return nil, errors.New("pipeline evaluator is nil")
	}
	r.candidateModel.setStage(stage)
	var patch agent.SurfacePatch
	patch.SetInstruction(instruction)
	result, err := r.evaluator.Evaluate(
		ctx,
		evalSetID,
		evaluation.WithNumRuns(1),
		evaluation.WithRunDetailsEnabled(true),
		evaluation.WithRunOptions(
			agent.WithExecutionTraceEnabled(true),
			agent.WithSurfacePatchForNode(candidateAgentName, patch),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("evaluate %s: %w", evalSetID, err)
	}
	return result, nil
}

func (r *pipelineRuntime) optimize(
	ctx context.Context,
	initialProfile *promptiter.Profile,
	lossHints []promptiterengine.LossHint,
	minScoreGain float64,
) (*promptiterengine.RunResult, error) {
	if r == nil || r.engine == nil {
		return nil, errors.New("promptiter engine is nil")
	}
	r.candidateModel.setStage("promptiter.evaluation")
	request := &promptiterengine.RunRequest{
		InitialProfile: initialProfile,
		Train: []promptiterengine.EvalSetInput{{
			EvalSetID: trainEvalSetID,
			LossHints: lossHints,
		}},
		Validation:         []promptiterengine.EvalSetInput{{EvalSetID: validationEvalSetID}},
		EvaluationOptions:  promptiterengine.EvaluationOptions{EvalCaseParallelism: 1},
		BackwardOptions:    promptiterengine.BackwardOptions{CaseParallelism: 1},
		AggregationOptions: promptiterengine.AggregationOptions{SurfaceParallelism: 1},
		OptimizerOptions:   promptiterengine.OptimizerOptions{SurfaceParallelism: 1},
		AcceptancePolicy:   promptiterengine.AcceptancePolicy{MinScoreGain: minScoreGain},
		StopPolicy:         promptiterengine.StopPolicy{MaxRoundsWithoutAcceptance: 1},
		MaxRounds:          1,
		TargetSurfaceIDs: []string{
			astructure.SurfaceID(candidateAgentName, astructure.SurfaceTypeInstruction),
		},
	}
	result, err := r.engine.Run(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("run promptiter: %w", err)
	}
	if len(result.Rounds) != 1 || result.Rounds[0].OutputProfile == nil {
		return nil, errors.New("promptiter did not produce exactly one output profile")
	}
	return result, nil
}

func instructionFromProfile(profile *promptiter.Profile, surfaceID string) (string, error) {
	if profile == nil {
		return "", errors.New("profile is nil")
	}
	for _, override := range profile.Overrides {
		if override.SurfaceID != surfaceID {
			continue
		}
		if override.Value.Text == nil {
			return "", fmt.Errorf("surface %q text is nil", surfaceID)
		}
		return *override.Value.Text, nil
	}
	return "", fmt.Errorf("surface %q is missing from profile", surfaceID)
}

type fixedEvalSetLocator struct {
	paths map[string]string
}

func (l *fixedEvalSetLocator) Build(_, _ string, evalSetID string) string {
	return l.paths[evalSetID]
}

func (l *fixedEvalSetLocator) List(_, _ string) ([]string, error) {
	ids := make([]string, 0, len(l.paths))
	for id := range l.paths {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

type fixedMetricLocator struct {
	path string
}

func (l *fixedMetricLocator) Build(_, _, _ string) string {
	return filepath.Clean(l.path)
}
