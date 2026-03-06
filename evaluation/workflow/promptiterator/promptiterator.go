//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package promptiterator implements an evaluation-driven prompt iteration workflow.
package promptiterator

import (
	"context"
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiterator/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiterator/issue"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiterator/optimizer"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// PromptIterator drives an evaluation-based prompt optimization loop.
type PromptIterator interface {
	// Run executes the prompt iteration starting from the initial prompt.
	Run(ctx context.Context, initialPrompt string, evalSetIDs []string, opt ...Option) (*Result, error)
	// Close releases resources held by the prompt iterator.
	Close() error
}

// Result contains the prompt iteration outcome and per-round details.
type Result struct {
	// InitialPrompt is the input prompt text.
	InitialPrompt string
	// FinalPrompt is the last prompt text produced by the workflow.
	FinalPrompt string
	// Passed indicates whether the final prompt passed all metrics.
	Passed bool
	// OptimizationRounds is the number of prompt optimization rounds performed.
	OptimizationRounds int
	// Rounds contains per-round evaluation and optimization details.
	Rounds []*Round
}

// Round captures a single evaluation step and its optional optimization output.
type Round struct {
	// Index is a 1-based round index.
	Index int
	// Prompt is the prompt used for Candidate inference in this round.
	Prompt string
	// EvalResults stores evaluation results keyed by evalSetID.
	EvalResults map[string]*evaluation.EvaluationResult
	// Passed indicates whether all evalsets passed in this round.
	Passed bool
	// Issues contains extracted issues for this round.
	Issues []issue.IssueRecord
	// Gradient is the aggregated gradient produced from Issues.
	Gradient *issue.AggregatedGradient
	// OptimizedPrompt is the next prompt produced by the optimizer.
	OptimizedPrompt string
}

// New creates a prompt iterator and builds the underlying evaluation.AgentEvaluator.
func New(appName string, candidateRunner runner.Runner, opt ...Option) (PromptIterator, error) {
	opts := newOptions(opt...)
	opts.appName = appName
	opts.candidateRunner = candidateRunner
	if err := opts.validate(true); err != nil {
		return nil, err
	}
	evalOpts := []evaluation.Option{
		evaluation.WithEvalSetManager(opts.evalSetManager),
		evaluation.WithMetricManager(opts.metricManager),
		evaluation.WithRegistry(opts.registry),
	}
	if opts.expectedRunner != nil {
		evalOpts = append(evalOpts, evaluation.WithExpectedRunner(opts.expectedRunner))
	}
	if opts.judgeRunner != nil {
		evalOpts = append(evalOpts, evaluation.WithJudgeRunner(opts.judgeRunner))
	}
	agentEvaluator, err := evaluation.New(opts.appName, opts.candidateRunner, evalOpts...)
	if err != nil {
		return nil, err
	}
	return &promptIterator{
		appName:               opts.appName,
		maxOptimizationRounds: opts.maxOptimizationRounds,
		agentEvaluator:        agentEvaluator,
		expectedRunner:        opts.expectedRunner,
		evalSetManager:        opts.evalSetManager,
		metricManager:         opts.metricManager,
		registry:              opts.registry,
		issueExtractor:        opts.issueExtractor,
		aggregator:            opts.aggregator,
		optimizer:             opts.optimizer,
		runOptions:            append([]agent.RunOption(nil), opts.runOptions...),
	}, nil
}

type promptIterator struct {
	appName               string
	maxOptimizationRounds int
	agentEvaluator        evaluation.AgentEvaluator
	expectedRunner        runner.Runner
	evalSetManager        evalset.Manager
	metricManager         metric.Manager
	registry              registry.Registry
	issueExtractor        issue.IssueExtractor
	aggregator            aggregator.Aggregator
	optimizer             optimizer.Optimizer
	runOptions            []agent.RunOption
}

// Run executes the prompt iteration starting from initialPrompt.
func (w *promptIterator) Run(ctx context.Context, initialPrompt string, evalSetIDs []string, opt ...Option) (*Result, error) {
	if w.agentEvaluator == nil {
		return nil, errors.New("prompt iterator is not initialized")
	}
	callOpts, err := w.mergeCallOptions(opt...)
	if err != nil {
		return nil, err
	}
	if len(evalSetIDs) == 0 {
		return nil, errors.New("eval set IDs are empty")
	}
	currentPrompt := initialPrompt
	out := &Result{
		InitialPrompt: initialPrompt,
		FinalPrompt:   initialPrompt,
		Passed:        false,
		Rounds:        make([]*Round, 0, callOpts.maxOptimizationRounds+1),
	}
	// Run optimization rounds until the prompt passes or the limit is reached.
	for round := 1; round <= callOpts.maxOptimizationRounds; round++ {
		roundResult, err := w.evaluateRound(ctx, round, currentPrompt, evalSetIDs, callOpts)
		if err != nil {
			return nil, err
		}
		out.Rounds = append(out.Rounds, roundResult)
		if roundResult.Passed {
			out.FinalPrompt = currentPrompt
			out.Passed = true
			return out, nil
		}
		if len(roundResult.Issues) == 0 {
			return nil, errors.New("no issues extracted while evaluation failed")
		}
		gradient, err := callOpts.aggregator.Aggregate(ctx, roundResult.Issues)
		if err != nil {
			return nil, fmt.Errorf("aggregate issues: %w", err)
		}
		if len(gradient.Issues) == 0 {
			return nil, errors.New("aggregated gradient issues are empty")
		}
		nextPrompt, err := callOpts.optimizer.Optimize(ctx, currentPrompt, gradient)
		if err != nil {
			return nil, fmt.Errorf("optimize prompt: %w", err)
		}
		roundResult.Gradient = gradient
		roundResult.OptimizedPrompt = nextPrompt
		currentPrompt = nextPrompt
		out.OptimizationRounds = round
		out.FinalPrompt = nextPrompt
	}
	// Evaluate the last prompt after completing all optimization rounds.
	finalRound, err := w.evaluateRound(ctx, callOpts.maxOptimizationRounds+1, currentPrompt, evalSetIDs, callOpts)
	if err != nil {
		return nil, err
	}
	out.Rounds = append(out.Rounds, finalRound)
	out.FinalPrompt = currentPrompt
	out.Passed = finalRound.Passed
	return out, nil
}

func (w *promptIterator) mergeCallOptions(opt ...Option) (*options, error) {
	callOpts := &options{
		appName:               w.appName,
		maxOptimizationRounds: w.maxOptimizationRounds,
		expectedRunner:        w.expectedRunner,
		evalSetManager:        w.evalSetManager,
		metricManager:         w.metricManager,
		registry:              w.registry,
		issueExtractor:        w.issueExtractor,
		aggregator:            w.aggregator,
		optimizer:             w.optimizer,
		runOptions:            append([]agent.RunOption(nil), w.runOptions...),
	}
	for _, o := range opt {
		o(callOpts)
	}
	if err := callOpts.validate(false); err != nil {
		return nil, err
	}
	return callOpts, nil
}

// Close releases resources held by the prompt iterator.
func (w *promptIterator) Close() error {
	if w.agentEvaluator != nil {
		return w.agentEvaluator.Close()
	}
	return nil
}
