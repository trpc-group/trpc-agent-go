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
	"slices"
	"strings"

	"golang.org/x/sync/errgroup"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/backwarder"
	iprofile "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/internal/profile"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/optimizer"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// Engine orchestrates a complete PromptIter lifecycle across evaluation, optimization, acceptance, and stop decisions.
type Engine interface {
	// Describe returns the current structure snapshot used to build traces and profiles.
	Describe(ctx context.Context) (*astructure.Snapshot, error)
	// Run executes multi-round optimization using training and validation feedback loops.
	Run(ctx context.Context, request *RunRequest, opts ...Option) (*RunResult, error)
}

// RunRequest carries the inputs required to start PromptIter optimization.
type RunRequest struct {
	// Train identifies evaluation data used to generate gradients.
	Train []EvalSetInput `json:"train"`
	// Validation identifies evaluation data used for patch acceptance checks.
	Validation []EvalSetInput `json:"validation"`
	// InitialProfile is the baseline profile for round one optimization.
	InitialProfile *promptiter.Profile
	// Teacher executes trace generation requests for evaluation.
	Teacher runner.Runner
	// Judge evaluates generated outputs and returns scoring details.
	Judge runner.Runner
	// EvaluationOptions configures how training and validation runs are executed.
	EvaluationOptions EvaluationOptions
	// BackwardOptions configures backward-stage execution.
	BackwardOptions BackwardOptions
	// AggregationOptions configures aggregation-stage execution.
	AggregationOptions AggregationOptions
	// OptimizerOptions configures optimizer-stage execution.
	OptimizerOptions OptimizerOptions
	// AcceptancePolicy controls minimum quality gain required to accept patching.
	AcceptancePolicy AcceptancePolicy
	// StopPolicy controls termination conditions between rounds.
	StopPolicy StopPolicy
	// MaxRounds is the hard cap for outer optimization iterations.
	MaxRounds int
	// TargetSurfaceIDs limits this run to optimizing only the listed surfaces.
	TargetSurfaceIDs []string
}

// EvalSetInput identifies one evaluation set and optional case filters for a PromptIter run.
type EvalSetInput struct {
	// EvalSetID identifies the eval set to execute.
	EvalSetID string `json:"evalSetId"`
	// EvalCaseIDs limits this eval set to specific eval cases when set.
	EvalCaseIDs []string `json:"evalCaseIds,omitempty"`
	// LossHints stores operator-provided loss reasons keyed by failed eval case and metric.
	LossHints []LossHint `json:"lossHints,omitempty"`
}

// LossHint carries operator-provided context for one failed metric on one eval case.
type LossHint struct {
	// EvalCaseID identifies the eval case to receive this hint.
	EvalCaseID string `json:"evalCaseId"`
	// MetricName identifies the failed metric to receive this hint.
	MetricName string `json:"metricName"`
	// Severity indicates how urgently this hint should influence optimization when set.
	Severity promptiter.LossSeverity `json:"severity,omitempty"`
	// Reason stores the manual loss text used by gradient computation.
	Reason string `json:"reason"`
}

// RunStatus identifies the lifecycle state of one PromptIter run view.
type RunStatus string

const (
	// RunStatusQueued indicates that the run has been created but has not started execution.
	RunStatusQueued RunStatus = "queued"
	// RunStatusRunning indicates that the run is actively executing.
	RunStatusRunning RunStatus = "running"
	// RunStatusSucceeded indicates that the run finished successfully.
	RunStatusSucceeded RunStatus = "succeeded"
	// RunStatusFailed indicates that the run finished with an error.
	RunStatusFailed RunStatus = "failed"
	// RunStatusCanceled indicates that the run was canceled before completion.
	RunStatusCanceled RunStatus = "canceled"
)

// RunResult stores the state and historical trace of one PromptIter execution.
type RunResult struct {
	// AppName identifies the PromptIter target app that owns this run.
	AppName string
	// ID uniquely identifies this run when the caller uses manager-backed execution.
	ID string
	// Status stores the lifecycle state of the run.
	Status RunStatus
	// CurrentRound stores the latest round started by the run.
	CurrentRound int
	// Structure is the snapshot used for all rounds in this request.
	Structure *astructure.Snapshot
	// BaselineValidation stores the accepted baseline validation result before optimization rounds.
	BaselineValidation *EvaluationResult
	// AcceptedProfile is the profile that passed acceptance and can be published.
	AcceptedProfile *promptiter.Profile
	// Rounds stores intermediate results of every optimization round.
	Rounds []RoundResult
	// ErrorMessage stores the terminal run error when the run failed or was canceled.
	ErrorMessage string
}

// RoundResult captures all observable state for one optimization round.
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
	default:
		return &engine{
			targetAgent:    targetAgent,
			backwarder:     backwarder,
			aggregator:     aggregator,
			optimizer:      optimizer,
			agentEvaluator: agentEvaluator,
		}, nil
	}
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
func (e *engine) Run(ctx context.Context, request *RunRequest, opts ...Option) (*RunResult, error) {
	options := newOptions(opts...)
	return e.run(ctx, request, options.observer)
}

func (e *engine) run(
	ctx context.Context,
	request *RunRequest,
	observer Observer,
) (*RunResult, error) {
	if err := e.validateRunRequest(request); err != nil {
		return nil, err
	}
	snapshot, err := e.describeStructure(ctx)
	if err != nil {
		return nil, fmt.Errorf("describe structure: %w", err)
	}
	if err := appendRunEvent(ctx, observer, EventKindStructureSnapshot, 0, snapshot); err != nil {
		return nil, err
	}
	structure, err := newStructureState(snapshot)
	if err != nil {
		return nil, fmt.Errorf("create structure state: %w", err)
	}
	targetSurfaceSet, err := compileTargetSurfaceIDs(structure, request.TargetSurfaceIDs)
	if err != nil {
		return nil, fmt.Errorf("compile target surface ids: %w", err)
	}
	initialProfile, err := normalizeProfile(structure, request.InitialProfile)
	if err != nil {
		return nil, fmt.Errorf("normalize initial profile: %w", err)
	}
	evaluationOptions := request.EvaluationOptions
	acceptedProfile := initialProfile
	acceptedValidationScore := 0.0
	baselineValidation, err := e.evaluate(ctx, structure, e.newEvaluationRequest(
		request.Validation,
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
	if err := appendRunEvent(ctx, observer, EventKindBaselineValidation, 0, baselineValidation); err != nil {
		return nil, err
	}
	result := &RunResult{
		Status:             RunStatusRunning,
		CurrentRound:       0,
		Structure:          structure.snapshot,
		BaselineValidation: baselineValidation,
		AcceptedProfile:    iprofile.Clone(acceptedProfile),
		Rounds:             make([]RoundResult, 0, request.MaxRounds),
	}
	roundsWithoutAcceptance := 0
	for roundNumber := 1; roundNumber <= request.MaxRounds; roundNumber++ {
		result.CurrentRound = roundNumber
		roundResult, effectiveScore, err := e.executeRound(
			ctx,
			request,
			structure,
			targetSurfaceSet,
			observer,
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
		accepted := roundResult.Acceptance != nil && roundResult.Acceptance.Accepted
		acceptanceReason := ""
		scoreDelta := 0.0
		if roundResult.Acceptance != nil {
			acceptanceReason = roundResult.Acceptance.Reason
			scoreDelta = roundResult.Acceptance.ScoreDelta
		}
		shouldStop := false
		stopReason := ""
		if roundResult.Stop != nil {
			shouldStop = roundResult.Stop.ShouldStop
			stopReason = roundResult.Stop.Reason
		}
		if err := appendRunEvent(ctx, observer, EventKindRoundCompleted, roundNumber, &RoundCompleted{
			Accepted:         accepted,
			AcceptanceReason: acceptanceReason,
			ScoreDelta:       scoreDelta,
			ShouldStop:       shouldStop,
			StopReason:       stopReason,
		}); err != nil {
			return nil, err
		}
		result.Rounds = append(result.Rounds, *roundResult)
		result.AcceptedProfile = iprofile.Clone(acceptedProfile)
		if roundResult.Stop.ShouldStop {
			break
		}
	}
	result.Status = RunStatusSucceeded
	return result, nil
}

func (e *engine) validateRunRequest(request *RunRequest) error {
	if request == nil {
		return errors.New("run request is nil")
	}
	if err := validateEvalSetInputs("train", request.Train); err != nil {
		return err
	}
	if err := validateEvalSetInputs("validation", request.Validation); err != nil {
		return err
	}
	switch {
	case request.MaxRounds <= 0:
		return errors.New("max rounds must be greater than 0")
	case request.TargetSurfaceIDs != nil && len(request.TargetSurfaceIDs) == 0:
		return errors.New("target surface ids must not be empty")
	case request.BackwardOptions.CaseParallelism < 0:
		return errors.New("backward case parallelism must be non-negative")
	case request.AggregationOptions.SurfaceParallelism < 0:
		return errors.New("aggregation surface parallelism must be non-negative")
	case request.OptimizerOptions.SurfaceParallelism < 0:
		return errors.New("optimizer surface parallelism must be non-negative")
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
	targetSurfaceSet targetSurfaceSet,
	observer Observer,
	evaluationOptions EvaluationOptions,
	acceptedProfile *promptiter.Profile,
	acceptedValidationScore float64,
	roundNumber int,
) (*RoundResult, float64, error) {
	if err := appendRunEvent(ctx, observer, EventKindRoundStarted, roundNumber, nil); err != nil {
		return nil, 0, err
	}
	roundResult := &RoundResult{
		Round:        roundNumber,
		InputProfile: iprofile.Clone(acceptedProfile),
	}
	trainResult, err := e.evaluate(ctx, structure, e.newEvaluationRequest(
		request.Train,
		acceptedProfile,
		request.Teacher,
		request.Judge,
		evaluationOptions,
	))
	if err != nil {
		return nil, 0, fmt.Errorf("evaluate train round %d: %w", roundNumber, err)
	}
	roundResult.Train = trainResult
	if err := appendRunEvent(ctx, observer, EventKindRoundTrainEvaluation, roundNumber, trainResult); err != nil {
		return nil, 0, err
	}
	losses, err := e.loss(trainResult)
	if err != nil {
		return nil, 0, fmt.Errorf("extract train losses round %d: %w", roundNumber, err)
	}
	losses, err = mergeLossHints(losses, trainResult, request.Train)
	if err != nil {
		return nil, 0, fmt.Errorf("merge train loss hints round %d: %w", roundNumber, err)
	}
	roundResult.Losses = losses
	if err := appendRunEvent(ctx, observer, EventKindRoundLosses, roundNumber, losses); err != nil {
		return nil, 0, err
	}
	backwardResult, err := e.backward(
		ctx,
		structure,
		acceptedProfile,
		trainResult,
		losses,
		targetSurfaceSet,
		request.BackwardOptions,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("backward round %d: %w", roundNumber, err)
	}
	roundResult.Backward = backwardResult
	if err := appendRunEvent(ctx, observer, EventKindRoundBackward, roundNumber, backwardResult); err != nil {
		return nil, 0, err
	}
	aggregationResult, err := e.aggregate(
		ctx,
		structure,
		backwardResult,
		targetSurfaceSet,
		request.AggregationOptions,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("aggregate round %d: %w", roundNumber, err)
	}
	roundResult.Aggregation = aggregationResult
	if err := appendRunEvent(ctx, observer, EventKindRoundAggregation, roundNumber, aggregationResult); err != nil {
		return nil, 0, err
	}
	patchSet, err := e.optimize(
		ctx,
		structure,
		acceptedProfile,
		aggregationResult,
		targetSurfaceSet,
		request.OptimizerOptions,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("optimize round %d: %w", roundNumber, err)
	}
	roundResult.Patches = patchSet
	if err := appendRunEvent(ctx, observer, EventKindRoundPatchSet, roundNumber, patchSet); err != nil {
		return nil, 0, err
	}
	outputProfile, err := applyPatchSet(structure, acceptedProfile, patchSet)
	if err != nil {
		return nil, 0, fmt.Errorf("apply patches round %d: %w", roundNumber, err)
	}
	roundResult.OutputProfile = outputProfile
	if err := appendRunEvent(ctx, observer, EventKindRoundOutputProfile, roundNumber, outputProfile); err != nil {
		return nil, 0, err
	}
	validationResult, err := e.evaluate(ctx, structure, e.newEvaluationRequest(
		request.Validation,
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
	if err := appendRunEvent(ctx, observer, EventKindRoundValidation, roundNumber, validationResult); err != nil {
		return nil, 0, err
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
	inputs []EvalSetInput,
	profile *promptiter.Profile,
	teacher runner.Runner,
	judge runner.Runner,
	options EvaluationOptions,
) *EvaluationRequest {
	return &EvaluationRequest{
		EvalSets: inputs,
		Profile:  profile,
		Teacher:  teacher,
		Judge:    judge,
		Options:  options,
	}
}

func validateEvalSetInputs(role string, inputs []EvalSetInput) error {
	prefix := ""
	if role != "" {
		prefix = role + " "
	}
	if len(inputs) == 0 {
		return fmt.Errorf("%sevaluation sets are empty", prefix)
	}
	for _, input := range inputs {
		if input.EvalSetID == "" {
			return fmt.Errorf("%sevaluation set id is empty", prefix)
		}
		if slices.Contains(input.EvalCaseIDs, "") {
			return fmt.Errorf("%seval case id for eval set %q is empty", prefix, input.EvalSetID)
		}
		selectedCaseIDs := make(map[string]struct{}, len(input.EvalCaseIDs))
		for _, evalCaseID := range input.EvalCaseIDs {
			selectedCaseIDs[evalCaseID] = struct{}{}
		}
		for _, hint := range input.LossHints {
			hintEvalCaseID := strings.TrimSpace(hint.EvalCaseID)
			switch {
			case hintEvalCaseID == "":
				return fmt.Errorf("%sloss hint eval case id for eval set %q is empty", prefix, input.EvalSetID)
			case strings.TrimSpace(hint.MetricName) == "":
				return fmt.Errorf(
					"%sloss hint metric name for eval set %q case %q is empty",
					prefix,
					input.EvalSetID,
					hint.EvalCaseID,
				)
			case strings.TrimSpace(hint.Reason) == "":
				return fmt.Errorf(
					"%sloss hint reason for eval set %q case %q metric %q is empty",
					prefix,
					input.EvalSetID,
					hint.EvalCaseID,
					hint.MetricName,
				)
			case !isValidLossHintSeverity(hint.Severity):
				return fmt.Errorf(
					"%sloss hint severity %q for eval set %q case %q metric %q is invalid",
					prefix,
					hint.Severity,
					input.EvalSetID,
					hint.EvalCaseID,
					hint.MetricName,
				)
			}
			if len(selectedCaseIDs) > 0 {
				if _, ok := selectedCaseIDs[hintEvalCaseID]; !ok {
					return fmt.Errorf(
						"%sloss hint eval case %q is not selected for eval set %q",
						prefix,
						hint.EvalCaseID,
						input.EvalSetID,
					)
				}
			}
		}
	}
	return nil
}

func isValidLossHintSeverity(severity promptiter.LossSeverity) bool {
	switch severity {
	case "",
		promptiter.LossSeverityP0,
		promptiter.LossSeverityP1,
		promptiter.LossSeverityP2,
		promptiter.LossSeverityP3:
		return true
	default:
		return false
	}
}

func runIndexedParallel(
	ctx context.Context,
	count int,
	parallelism int,
	fn func(context.Context, int) error,
) error {
	if parallelism <= 1 || count <= 1 {
		for index := range count {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := fn(ctx, index); err != nil {
				return err
			}
		}
		return nil
	}
	group, groupCtx := errgroup.WithContext(ctx)
	indexErrors := make([]error, count)
	group.SetLimit(parallelism)
	for index := range count {
		if err := groupCtx.Err(); err != nil {
			break
		}
		index := index
		group.Go(func() error {
			if err := groupCtx.Err(); err != nil {
				return err
			}
			if err := fn(groupCtx, index); err != nil {
				indexErrors[index] = err
				return err
			}
			return nil
		})
	}
	waitErr := group.Wait()
	indexErrors = append(indexErrors, waitErr, ctx.Err())
	return errors.Join(indexErrors...)
}
