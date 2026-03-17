//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package engine implements PromptIter orchestration and runtime flow for a generation round.
package engine

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/backwarder"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/optimizer"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// Engine orchestrates a complete PromptIter lifecycle across evaluation, optimization, acceptance, and stop decisions.
type Engine interface {
	// Describe returns the current structure snapshot used to build traces and profiles.
	Describe(ctx context.Context) (*promptiter.StructureSnapshot, error)
	// Run executes multi-round optimization using training and validation feedback loops.
	Run(ctx context.Context, request *RunRequest) (*RunResult, error)
}

// RunRequest carries the inputs required to start PromptIter optimization.
type RunRequest struct {
	// TrainEvalSetIDs identifies evaluation sets used to generate gradients.
	TrainEvalSetIDs []string
	// ValidationEvalSetIDs identifies evaluation sets used for patch acceptance checks.
	ValidationEvalSetIDs []string
	// InitialProfile is the baseline profile for round one optimization.
	InitialProfile *promptiter.Profile
	// Teacher executes trace generation requests for evaluation.
	Teacher runner.Runner
	// Judge evaluates generated outputs and returns scoring details.
	Judge runner.Runner
	// EvaluationOptions configures how training and validation runs are executed.
	EvaluationOptions EvaluationOptions
	// AcceptancePolicy controls minimum quality gain required to accept patching.
	AcceptancePolicy AcceptancePolicy
	// StopPolicy controls termination conditions between rounds.
	StopPolicy StopPolicy
	// MaxRounds is the hard cap for outer optimization iterations.
	MaxRounds int
}

// RunResult stores the end state and historical trace of a multi-round run.
type RunResult struct {
	// Structure is the snapshot used for all rounds in this request.
	Structure *promptiter.StructureSnapshot
	// AcceptedProfile is the profile that passed acceptance and can be published.
	AcceptedProfile *promptiter.Profile
	// Rounds stores intermediate results of every optimization round.
	Rounds []RoundResult
}

// RoundResult captures all artifacts for one optimization round.
type RoundResult struct {
	// Round is the one-based index of this optimization cycle.
	Round int
	// InputProfile is the profile evaluated at the start of this round.
	InputProfile *promptiter.Profile
	// Train is the train-set result used for gradient generation.
	Train *EvaluationResult
	// Losses stores terminal losses extracted from train traces.
	Losses []promptiter.CaseLoss
	// Backward stores backward outputs grouped by sample.
	Backward *BackwardResult
	// Aggregation stores gradient merges that remove duplicated surface signals.
	Aggregation *AggregationResult
	// Patches stores optimizer suggestions before acceptance and commit.
	Patches *promptiter.PatchSet
	// OutputProfile is the candidate profile created from generated patches.
	OutputProfile *promptiter.Profile
	// Validation is the validation result used for acceptance.
	Validation *EvaluationResult
	// Acceptance is the acceptance output for this round.
	Acceptance *AcceptanceDecision
	// Stop indicates whether the round triggered an early stop condition.
	Stop *StopDecision
}

// engine is the default Engine implementation.
type engine struct {
	// runner executes workflow-level actions triggered by the engine.
	runner runner.Runner
	// agentEvaluator evaluates prompt output quality and returns metric scores.
	agentEvaluator evaluation.AgentEvaluator
	// backwarder computes sample-level gradient packets from terminal losses.
	backwarder backwarder.Backwarder
	// aggregator merges sample gradients into per-surface aggregated gradient.
	aggregator aggregator.Aggregator
	// optimizer translates aggregated gradients into patch candidates.
	optimizer optimizer.Optimizer
}

// New creates an Engine implementation with injected collaborators.
func New(ctx context.Context,
	runner runner.Runner,
	agentEvaluator evaluation.AgentEvaluator,
	backwarder backwarder.Backwarder,
	aggregator aggregator.Aggregator,
	optimizer optimizer.Optimizer,
	opt ...option) (Engine, error) {
	return &engine{
		runner:         runner,
		agentEvaluator: agentEvaluator,
		backwarder:     backwarder,
		aggregator:     aggregator,
		optimizer:      optimizer,
	}, nil
}

// Describe returns the structure snapshot used for the current optimization session.
func (e *engine) Describe(ctx context.Context) (*promptiter.StructureSnapshot, error) {
	return nil, nil
}

// Run executes all optimization stages in sequence for each configured round.
func (e *engine) Run(ctx context.Context, request *RunRequest) (*RunResult, error) {
	if err := e.evaluate(ctx); err != nil {
		return nil, err
	}
	if err := e.loss(); err != nil {
		return nil, err
	}
	if err := e.backward(ctx); err != nil {
		return nil, err
	}
	if err := e.aggregate(ctx); err != nil {
		return nil, err
	}
	if err := e.optimize(ctx); err != nil {
		return nil, err
	}
	if err := e.accept(); err != nil {
		return nil, err
	}
	if err := e.stop(); err != nil {
		return nil, err
	}
	return &RunResult{}, nil
}
