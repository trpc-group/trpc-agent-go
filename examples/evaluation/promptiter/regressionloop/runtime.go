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
	"sync"
	"time"

	openaiopt "github.com/openai/openai-go/option"
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

type runtimeConfig struct {
	Config               config
	DataDir              string
	OutputDir            string
	CandidateInstruction string
}

type runtime struct {
	engine     promptiterengine.Engine
	evaluator  evaluation.AgentEvaluator
	backwarder backwarder.Backwarder
	aggregator aggregator.Aggregator
	optimizer  optimizer.Optimizer
	ledger     *ledger

	runners   []runner.Runner
	closeOnce sync.Once
	closeErr  error
}

type runtimeModels struct {
	candidate  model.Model
	judge      model.Model
	backwarder model.Model
	aggregator model.Model
	optimizer  model.Model
}

type sharedMetricLocator struct {
	metricFileID string
}

func (l *sharedMetricLocator) Build(baseDir, appName, _ string) string {
	return filepath.Join(baseDir, appName, l.metricFileID+".metrics.json")
}

func buildRuntime(ctx context.Context, cfg runtimeConfig) (*runtime, error) {
	if err := cfg.Config.validate(); err != nil {
		return nil, fmt.Errorf("validate runtime config: %w", err)
	}
	if strings.TrimSpace(cfg.DataDir) == "" {
		return nil, errors.New("runtime data directory is empty")
	}
	if strings.TrimSpace(cfg.OutputDir) == "" {
		return nil, errors.New("runtime output directory is empty")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	runLedger := newLedger()
	models, err := buildRuntimeModels(cfg.Config, runLedger)
	if err != nil {
		return nil, fmt.Errorf("build runtime models: %w", err)
	}
	candidateAgent := newCandidateAgent(models.candidate, cfg.CandidateInstruction)
	judgeAgent := newJudgeAgent(models.judge)
	backwarderAgent := newBackwarderAgent(models.backwarder)
	aggregatorAgent := newAggregatorAgent(models.aggregator)
	optimizerAgent := newOptimizerAgent(models.optimizer)

	candidateRunner := runner.NewRunner(regressionAppName, candidateAgent)
	judgeRunner := runner.NewRunner(judgeAgentName, judgeAgent)
	backwarderRunner := runner.NewRunner(backwarderAgentName, backwarderAgent)
	aggregatorRunner := runner.NewRunner(aggregatorAgentName, aggregatorAgent)
	optimizerRunner := runner.NewRunner(optimizerAgentName, optimizerAgent)
	runners := []runner.Runner{
		candidateRunner,
		judgeRunner,
		backwarderRunner,
		aggregatorRunner,
		optimizerRunner,
	}
	closeRunners := func() error {
		var closeErr error
		for _, ownedRunner := range runners {
			closeErr = errors.Join(closeErr, ownedRunner.Close())
		}
		return closeErr
	}

	evalSetManager := evalsetlocal.New(evalset.WithBaseDir(cfg.DataDir))
	metricManager := metriclocal.New(
		metric.WithBaseDir(cfg.DataDir),
		metric.WithLocator(&sharedMetricLocator{metricFileID: cfg.Config.MetricFileID}),
	)
	evalResultManager := evalresultlocal.New(evalresult.WithBaseDir(cfg.OutputDir))
	agentEvaluator, err := evaluation.New(
		regressionAppName,
		candidateRunner,
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithMetricManager(metricManager),
		evaluation.WithEvalResultManager(evalResultManager),
		evaluation.WithMetricRegistry(metricregistry.New()),
		evaluation.WithJudgeRunner(judgeRunner),
		evaluation.WithNumRuns(1),
		evaluation.WithRunDetailsEnabled(true),
		evaluation.WithEvalCaseParallelism(cfg.Config.EvalCaseParallelism),
		evaluation.WithEvalCaseParallelInferenceEnabled(cfg.Config.ParallelInference),
		evaluation.WithEvalCaseParallelEvaluationEnabled(cfg.Config.ParallelEvaluation),
	)
	if err != nil {
		_ = closeRunners()
		return nil, fmt.Errorf("create evaluator: %w", err)
	}
	closeEvaluatorAndRunners := func() {
		_ = agentEvaluator.Close()
		_ = closeRunners()
	}

	backwarderInstance, err := backwarder.New(ctx, backwarderRunner)
	if err != nil {
		closeEvaluatorAndRunners()
		return nil, fmt.Errorf("create backwarder: %w", err)
	}
	aggregatorInstance, err := aggregator.New(ctx, aggregatorRunner)
	if err != nil {
		closeEvaluatorAndRunners()
		return nil, fmt.Errorf("create aggregator: %w", err)
	}
	optimizerInstance, err := optimizer.New(ctx, optimizerRunner)
	if err != nil {
		closeEvaluatorAndRunners()
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
		closeEvaluatorAndRunners()
		return nil, fmt.Errorf("create promptiter engine: %w", err)
	}

	return &runtime{
		engine:     engineInstance,
		evaluator:  agentEvaluator,
		backwarder: backwarderInstance,
		aggregator: aggregatorInstance,
		optimizer:  optimizerInstance,
		ledger:     runLedger,
		runners:    runners,
	}, nil
}

func buildRuntimeModels(cfg config, runLedger *ledger) (runtimeModels, error) {
	if cfg.Mode == modeDeterministic {
		return runtimeModels{
			candidate: newDeterministicCountedModel(
				"candidate", "candidate", newScriptedModel("candidate"),
				runLedger, rolePricing(cfg.Candidate),
			),
			judge: newDeterministicCountedModel(
				"judge", "judge", newScriptedModel("judge"),
				runLedger, rolePricing(cfg.Judge),
			),
			backwarder: newDeterministicCountedModel(
				"worker", "backwarder", newScriptedModel("backwarder"),
				runLedger, rolePricing(cfg.Worker),
			),
			aggregator: newDeterministicCountedModel(
				"worker", "aggregator", newScriptedModel("aggregator"),
				runLedger, rolePricing(cfg.Worker),
			),
			optimizer: newDeterministicCountedModel(
				"worker", "optimizer", newScriptedModel("optimizer"),
				runLedger, rolePricing(cfg.Worker),
			),
		}, nil
	}

	candidate, err := newLiveStageModel("candidate", "candidate", cfg.Candidate, runLedger)
	if err != nil {
		return runtimeModels{}, err
	}
	judge, err := newLiveStageModel("judge", "judge", cfg.Judge, runLedger)
	if err != nil {
		return runtimeModels{}, err
	}
	backwarderModel, err := newLiveStageModel("worker", "backwarder", cfg.Worker, runLedger)
	if err != nil {
		return runtimeModels{}, err
	}
	aggregatorModel, err := newLiveStageModel("worker", "aggregator", cfg.Worker, runLedger)
	if err != nil {
		return runtimeModels{}, err
	}
	optimizerModel, err := newLiveStageModel("worker", "optimizer", cfg.Worker, runLedger)
	if err != nil {
		return runtimeModels{}, err
	}
	return runtimeModels{
		candidate:  candidate,
		judge:      judge,
		backwarder: backwarderModel,
		aggregator: aggregatorModel,
		optimizer:  optimizerModel,
	}, nil
}

func newDeterministicCountedModel(
	role, stage string,
	base model.Model,
	runLedger *ledger,
	prices pricing,
) *countedModel {
	counted := newCountedModel(role, stage, base, runLedger, prices)
	zero := time.Duration(0)
	counted.latencyOverride = &zero
	return counted
}

func newLiveModel(role string, cfg roleConfig, runLedger *ledger) (model.Model, error) {
	return newLiveStageModel(role, role, cfg, runLedger)
}

func newLiveStageModel(role, stage string, cfg roleConfig, runLedger *ledger) (model.Model, error) {
	if runLedger == nil {
		return nil, errors.New("ledger is nil")
	}
	if err := validateLiveRole(role, cfg); err != nil {
		return nil, err
	}
	apiKey := strings.TrimSpace(os.Getenv(cfg.APIKeyEnv))
	if apiKey == "" {
		return nil, fmt.Errorf("%s API key environment %q is empty", role, cfg.APIKeyEnv)
	}
	options := []openai.Option{
		openai.WithAPIKey(apiKey),
		openai.WithOpenAIOptions(openaiopt.WithMaxRetries(0)),
	}
	if cfg.BaseURL != "" {
		options = append(options, openai.WithBaseURL(cfg.BaseURL))
	}
	base := openai.New(cfg.Model, options...)
	return newCountedModel(role, stage, base, runLedger, rolePricing(cfg)), nil
}

func rolePricing(cfg roleConfig) pricing {
	return pricing{InputPerM: cfg.InputPerM, OutputPerM: cfg.OutputPerM}
}

func (r *runtime) Close() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		if r.evaluator != nil {
			r.closeErr = errors.Join(r.closeErr, r.evaluator.Close())
		}
		for _, ownedRunner := range r.runners {
			if ownedRunner != nil {
				r.closeErr = errors.Join(r.closeErr, ownedRunner.Close())
			}
		}
	})
	return r.closeErr
}
