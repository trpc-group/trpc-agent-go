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
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
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
	Describe(ctx context.Context) (*astructure.Snapshot, error)
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
	Structure *astructure.Snapshot
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
	// targetAgent exports the current PromptIter structure for the optimization target.
	targetAgent agent.Agent
	// agentEvaluator executes train and validation evaluations through the shared evaluation framework.
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
	targetAgent agent.Agent,
	agentEvaluator evaluation.AgentEvaluator,
	backwarder backwarder.Backwarder,
	aggregator aggregator.Aggregator,
	optimizer optimizer.Optimizer) (Engine, error) {
	switch {
	case targetAgent == nil:
		return nil, errors.New("target agent is nil")
	case agentEvaluator == nil:
		return nil, errors.New("agent evaluator is nil")
	case backwarder == nil:
		return nil, errors.New("backwarder is nil")
	case aggregator == nil:
		return nil, errors.New("aggregator is nil")
	case optimizer == nil:
		return nil, errors.New("optimizer is nil")
	}
	return &engine{
		targetAgent:    targetAgent,
		backwarder:     backwarder,
		aggregator:     aggregator,
		optimizer:      optimizer,
		agentEvaluator: agentEvaluator,
	}, nil
}

// Describe returns the structure snapshot used for the current optimization session.
func (e *engine) Describe(ctx context.Context) (*astructure.Snapshot, error) {
	snapshot, err := e.describeStructure(ctx)
	if err != nil {
		return nil, err
	}
	return snapshot, nil
}

// Run executes all optimization stages in sequence for each configured round.
func (e *engine) Run(ctx context.Context, request *RunRequest) (*RunResult, error) {
	if err := e.validateRunRequest(request); err != nil {
		return nil, err
	}
	snapshot, err := e.describeStructure(ctx)
	if err != nil {
		return nil, fmt.Errorf("describe structure: %w", err)
	}
	structure, err := newStructureState(snapshot)
	if err != nil {
		return nil, fmt.Errorf("create structure state: %w", err)
	}
	initialProfile, err := normalizeProfile(structure, request.InitialProfile)
	if err != nil {
		return nil, fmt.Errorf("normalize initial profile: %w", err)
	}
	evaluationOptions := request.EvaluationOptions
	acceptedProfile := initialProfile
	acceptedValidationScore := 0.0
	baselineValidation, err := e.evaluate(ctx, structure, e.newEvaluationRequest(
		request.ValidationEvalSetIDs,
		acceptedProfile,
		request.Teacher,
		request.Judge,
		evaluationOptions,
	))
	if err != nil {
		return nil, fmt.Errorf("evaluate accepted baseline profile: %w", err)
	}
	acceptedValidationScore, err = evaluationScore(baselineValidation)
	if err != nil {
		return nil, fmt.Errorf("compute accepted baseline score: %w", err)
	}
	result := &RunResult{
		Structure:       structure.snapshot,
		AcceptedProfile: cloneProfile(acceptedProfile),
		Rounds:          make([]RoundResult, 0, request.MaxRounds),
	}
	roundsWithoutAcceptance := 0
	for roundNumber := 1; roundNumber <= request.MaxRounds; roundNumber++ {
		roundResult, effectiveScore, err := e.executeRound(
			ctx,
			request,
			structure,
			evaluationOptions,
			acceptedProfile,
			acceptedValidationScore,
			roundNumber,
		)
		if err != nil {
			return nil, err
		}
		if roundResult.Acceptance.Accepted {
			acceptedProfile = roundResult.OutputProfile
			acceptedValidationScore = effectiveScore
			roundsWithoutAcceptance = 0
		} else {
			roundsWithoutAcceptance++
		}
		roundResult.Stop = e.stop(
			roundNumber,
			request.MaxRounds,
			request.StopPolicy,
			roundsWithoutAcceptance,
			effectiveScore,
		)
		result.Rounds = append(result.Rounds, *roundResult)
		result.AcceptedProfile = cloneProfile(acceptedProfile)
		if roundResult.Stop.ShouldStop {
			break
		}
	}
	return result, nil
}

func (e *engine) validateRunRequest(request *RunRequest) error {
	switch {
	case request == nil:
		return errors.New("run request is nil")
	case len(request.TrainEvalSetIDs) == 0:
		return errors.New("train evaluation set ids are empty")
	case len(request.ValidationEvalSetIDs) == 0:
		return errors.New("validation evaluation set ids are empty")
	case request.MaxRounds <= 0:
		return errors.New("max rounds must be greater than 0")
	case e.targetAgent == nil:
		return errors.New("target agent is nil")
	case e.agentEvaluator == nil:
		return errors.New("agent evaluator is nil")
	default:
		return nil
	}
}

func (e *engine) describeStructure(ctx context.Context) (*astructure.Snapshot, error) {
	if e.targetAgent == nil {
		return nil, errors.New("target agent is nil")
	}
	snapshot, err := astructure.Export(ctx, e.targetAgent)
	if err != nil {
		return nil, fmt.Errorf("export target agent structure: %w", err)
	}
	return snapshot, nil
}

func (e *engine) executeRound(
	ctx context.Context,
	request *RunRequest,
	structure *structureState,
	evaluationOptions EvaluationOptions,
	acceptedProfile *promptiter.Profile,
	acceptedValidationScore float64,
	roundNumber int,
) (*RoundResult, float64, error) {
	roundResult := &RoundResult{
		Round:        roundNumber,
		InputProfile: cloneProfile(acceptedProfile),
	}
	trainResult, err := e.evaluate(ctx, structure, e.newEvaluationRequest(
		request.TrainEvalSetIDs,
		acceptedProfile,
		request.Teacher,
		request.Judge,
		evaluationOptions,
	))
	if err != nil {
		return nil, 0, fmt.Errorf("evaluate train round %d: %w", roundNumber, err)
	}
	roundResult.Train = trainResult
	losses, err := e.loss(trainResult)
	if err != nil {
		return nil, 0, fmt.Errorf("extract train losses round %d: %w", roundNumber, err)
	}
	roundResult.Losses = losses
	backwardResult, err := e.backward(ctx, structure, acceptedProfile, trainResult, losses)
	if err != nil {
		return nil, 0, fmt.Errorf("backward round %d: %w", roundNumber, err)
	}
	roundResult.Backward = backwardResult
	aggregationResult, err := e.aggregate(ctx, structure, backwardResult)
	if err != nil {
		return nil, 0, fmt.Errorf("aggregate round %d: %w", roundNumber, err)
	}
	roundResult.Aggregation = aggregationResult
	patchSet, err := e.optimize(ctx, structure, acceptedProfile, aggregationResult)
	if err != nil {
		return nil, 0, fmt.Errorf("optimize round %d: %w", roundNumber, err)
	}
	roundResult.Patches = patchSet
	outputProfile, err := applyPatchSet(structure, acceptedProfile, patchSet)
	if err != nil {
		return nil, 0, fmt.Errorf("apply patches round %d: %w", roundNumber, err)
	}
	roundResult.OutputProfile = outputProfile
	validationResult, err := e.evaluate(ctx, structure, e.newEvaluationRequest(
		request.ValidationEvalSetIDs,
		outputProfile,
		request.Teacher,
		request.Judge,
		evaluationOptions,
	))
	if err != nil {
		return nil, 0, fmt.Errorf("evaluate validation round %d: %w", roundNumber, err)
	}
	roundResult.Validation = validationResult
	baselineScore := acceptedValidationScore
	candidateScore, err := evaluationScore(validationResult)
	if err != nil {
		return nil, 0, fmt.Errorf("compute validation score round %d: %w", roundNumber, err)
	}
	acceptanceDecision := e.accept(request.AcceptancePolicy, baselineScore, candidateScore)
	roundResult.Acceptance = acceptanceDecision
	effectiveScore := baselineScore
	if acceptanceDecision.Accepted {
		effectiveScore = candidateScore
	}
	return roundResult, effectiveScore, nil
}

func (e *engine) newEvaluationRequest(
	evalSetIDs []string,
	profile *promptiter.Profile,
	teacher runner.Runner,
	judge runner.Runner,
	options EvaluationOptions,
) *EvaluationRequest {
	return &EvaluationRequest{
		EvalSetIDs: append(
			[]string(nil),
			evalSetIDs...,
		),
		Profile: cloneProfile(profile),
		Teacher: teacher,
		Judge:   judge,
		Options: options,
	}
}
