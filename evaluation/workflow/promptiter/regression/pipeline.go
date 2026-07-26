//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/internal/profilecompiler"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

var errPromptIterBudgetExhausted = errors.New("promptiter model-call budget exhausted")

// PipelineOption configures native PromptIter collaborators and observability.
type PipelineOption func(*pipelineOptions)

type pipelineOptions struct {
	teacher            runner.Runner
	judge              runner.Runner
	evaluationOptions  promptiterengine.EvaluationOptions
	backwardOptions    promptiterengine.BackwardOptions
	aggregationOptions promptiterengine.AggregationOptions
	optimizerOptions   promptiterengine.OptimizerOptions
	engineObserver     promptiterengine.Observer
	resourceMeter      ResourceMeter
	resourceObserver   ResourceObserver
}

// WithTeacher supplies the optional expected-output runner to native
// PromptIter evaluations.
func WithTeacher(teacher runner.Runner) PipelineOption {
	return func(options *pipelineOptions) {
		options.teacher = teacher
	}
}

// WithJudge supplies the optional judge runner to native PromptIter
// evaluations.
func WithJudge(judge runner.Runner) PipelineOption {
	return func(options *pipelineOptions) {
		options.judge = judge
	}
}

// WithEngineEvaluationOptions configures native PromptIter evaluation
// parallelism.
func WithEngineEvaluationOptions(options promptiterengine.EvaluationOptions) PipelineOption {
	return func(target *pipelineOptions) {
		target.evaluationOptions = options
	}
}

// WithEngineBackwardOptions configures native PromptIter backward execution.
func WithEngineBackwardOptions(options promptiterengine.BackwardOptions) PipelineOption {
	return func(target *pipelineOptions) {
		target.backwardOptions = options
	}
}

// WithEngineAggregationOptions configures native PromptIter aggregation.
func WithEngineAggregationOptions(options promptiterengine.AggregationOptions) PipelineOption {
	return func(target *pipelineOptions) {
		target.aggregationOptions = options
	}
}

// WithEngineOptimizerOptions configures native PromptIter optimization.
func WithEngineOptimizerOptions(options promptiterengine.OptimizerOptions) PipelineOption {
	return func(target *pipelineOptions) {
		target.optimizerOptions = options
	}
}

// WithEngineObserver forwards native PromptIter stage events.
func WithEngineObserver(observer promptiterengine.Observer) PipelineOption {
	return func(options *pipelineOptions) {
		options.engineObserver = observer
	}
}

// WithResourceMeter supplies one cumulative meter shared by outer evaluation
// and PromptIter collaborators.
func WithResourceMeter(meter ResourceMeter) PipelineOption {
	return func(options *pipelineOptions) {
		options.resourceMeter = meter
	}
}

// WithResourceObserver receives every global resource-ledger entry.
func WithResourceObserver(observer ResourceObserver) PipelineOption {
	return func(options *pipelineOptions) {
		options.resourceObserver = observer
	}
}

// Pipeline runs native PromptIter one generation round at a time and applies
// independent search and held-out release decisions.
type Pipeline struct {
	engine    promptiterengine.Engine
	evaluator SnapshotEvaluator
	options   pipelineOptions
}

// New creates a native regression pipeline.
func New(
	nativeEngine promptiterengine.Engine,
	evaluator SnapshotEvaluator,
	opts ...PipelineOption,
) (*Pipeline, error) {
	if nativeEngine == nil {
		return nil, errors.New("promptiter engine is nil")
	}
	if evaluator == nil {
		return nil, errors.New("snapshot evaluator is nil")
	}
	options := pipelineOptions{}
	for _, option := range opts {
		if option != nil {
			option(&options)
		}
	}
	return &Pipeline{
		engine:    nativeEngine,
		evaluator: evaluator,
		options:   options,
	}, nil
}

// Run executes the auditable Evaluation + PromptIter + held-out release loop.
// Operational failures are retained in the returned report. Context
// cancellation and deadline errors are returned with the finalized report;
// other returned errors prevent a report identity from being established.
//
//nolint:gocyclo // Keep the outer state-machine transitions visible in execution order.
func (p *Pipeline) Run(ctx context.Context, config *RunConfig) (*Report, error) {
	if config == nil {
		return nil, errors.New("run config is nil")
	}
	cfg := *config
	if err := validateRunConfig(&cfg); err != nil {
		return nil, err
	}
	structure, err := p.engine.Describe(ctx)
	if err != nil {
		return nil, fmt.Errorf("describe promptiter structure: %w", err)
	}
	if err := validateProfileAndTargets(structure, cfg.InitialProfile, cfg.PromptIter.TargetSurfaceIDs); err != nil {
		return nil, err
	}
	cfg.InitialProfile, err = bindProfileToStructure(cfg.InitialProfile, structure)
	if err != nil {
		return nil, fmt.Errorf("bind initial profile to structure: %w", err)
	}
	initial, err := buildProfileRecord(
		ProfileInitial,
		cfg.InitialProfile,
		cfg.PromptIter.TargetSurfaceIDs[0],
		structure,
	)
	if err != nil {
		return nil, fmt.Errorf("build initial profile record: %w", err)
	}
	report := newReport(cfg, initial)
	state := ProfileState{
		Initial:  initial,
		Search:   withProfileRole(initial, ProfileSearch),
		Released: withProfileRole(initial, ProfileReleased),
	}
	seenProfiles := map[string]struct{}{initial.Hash: {}}

	baselineTrain, trainErr := p.evaluateSnapshot(
		ctx,
		&cfg,
		cfg.InitialProfile,
		initial.Hash,
		cfg.Train,
		"train",
		"baseline_train",
		0,
		&report.Resources,
		nil,
	)
	report.BaselineTrain = baselineTrain
	if baselineTrain != nil {
		state.SearchTrain = baselineTrain
	}
	trainTerminationErr := contextTerminationError(ctx, trainErr)
	if trainTerminationErr != nil || trainErr != nil || !snapshotCompleted(baselineTrain) {
		auditErr := trainErr
		if trainTerminationErr != nil {
			auditErr = trainTerminationErr
		}
		report.Status = PipelineRunFailed
		report.StopReason = StopNecessaryRunFailed
		report.Errors = appendErrors(report.Errors, auditErr)
		finalizeReport(report, state)
		return report, trainTerminationErr
	}
	if stopped, reason := budgetStop(cfg.Gate, report.Resources.Cumulative); stopped {
		report.Status = PipelineBudgetStopped
		report.StopReason = StopBudgetExhausted
		report.FinalDecision = Decision{
			Status:  DecisionNotEvaluable,
			Reasons: []string{reason},
		}
		finalizeReport(report, state)
		return report, nil
	}

	baselineValidation, validationErr := p.evaluateSnapshot(
		ctx,
		&cfg,
		cfg.InitialProfile,
		initial.Hash,
		cfg.Validation,
		"heldout_validation",
		"baseline_validation",
		0,
		&report.Resources,
		nil,
	)
	report.BaselineValidation = baselineValidation
	if baselineValidation != nil {
		state.InitialValidation = baselineValidation
		state.SearchValidation = baselineValidation
		state.ReleasedValidation = baselineValidation
		initial.EvaluationRunID = baselineValidation.Provenance.RunID
		state.Initial.EvaluationRunID = initial.EvaluationRunID
		state.Search.EvaluationRunID = initial.EvaluationRunID
		state.Released.EvaluationRunID = initial.EvaluationRunID
		report.InitialProfile.EvaluationRunID = initial.EvaluationRunID
	}
	validationTerminationErr := contextTerminationError(ctx, validationErr)
	if validationTerminationErr != nil ||
		validationErr != nil ||
		!snapshotCompleted(baselineValidation) {
		auditErr := validationErr
		if validationTerminationErr != nil {
			auditErr = validationTerminationErr
		}
		report.Status = PipelineRunFailed
		report.StopReason = StopNecessaryRunFailed
		report.Errors = appendErrors(report.Errors, auditErr)
		finalizeReport(report, state)
		return report, validationTerminationErr
	}
	if stopped, reason := budgetStop(cfg.Gate, report.Resources.Cumulative); stopped {
		report.Status = PipelineBudgetStopped
		report.StopReason = StopBudgetExhausted
		report.FinalDecision = Decision{
			Status:  DecisionNotEvaluable,
			Reasons: []string{reason},
		}
		finalizeReport(report, state)
		return report, nil
	}

	internalValidationIDs, err := resolveInternalValidation(cfg.Train, cfg.PromptIter)
	if err != nil {
		return nil, err
	}
	for round := 1; round <= cfg.PromptIter.MaxOuterRounds; round++ {
		if snapshotFailedCount(state.SearchTrain) == 0 {
			report.StopReason = StopTrainingFailuresFixed
			break
		}
		hints, err := buildLossHints(state.SearchTrain)
		if err != nil {
			report.Status = PipelineRunFailed
			report.StopReason = StopNecessaryRunFailed
			report.Errors = append(report.Errors, fmt.Sprintf("build round %d loss hints: %v", round, err))
			break
		}
		searchParent := state.Search
		releasedParent := state.Released
		searchTrainParent := state.SearchTrain
		searchValidationParent := state.SearchValidation
		releasedValidationParent := state.ReleasedValidation
		candidateReport := CandidateReport{
			Round:              round,
			ID:                 fmt.Sprintf("candidate-%02d", round),
			Status:             EvaluationNotEvaluable,
			SearchParentHash:   searchParent.Hash,
			ReleasedParentHash: releasedParent.Hash,
			PromptIterRunID:    fmt.Sprintf("%s/promptiter/%d", cfg.RunID, round),
			SearchDecision:     notEvaluableDecision("candidate has not completed outer evaluation"),
			ReleaseDecision:    notEvaluableDecision("candidate has not completed outer evaluation"),
			Resources:          ResourceLedger{Entries: []ResourceEntry{}},
		}
		runRequest := &promptiterengine.RunRequest{
			Train: []promptiterengine.EvalSetInput{{
				EvalSetID:   cfg.Train.EvalSetID,
				EvalCaseIDs: append([]string(nil), cfg.Train.CaseIDs...),
				LossHints:   hints,
			}},
			Validation: []promptiterengine.EvalSetInput{{
				EvalSetID:   cfg.Train.EvalSetID,
				EvalCaseIDs: append([]string(nil), internalValidationIDs...),
			}},
			InitialProfile:     state.Search.Profile,
			Teacher:            p.options.teacher,
			Judge:              p.options.judge,
			EvaluationOptions:  p.options.evaluationOptions,
			BackwardOptions:    p.options.backwardOptions,
			AggregationOptions: p.options.aggregationOptions,
			OptimizerOptions:   p.options.optimizerOptions,
			AcceptancePolicy: promptiterengine.AcceptancePolicy{
				MinScoreGain: cfg.PromptIter.SearchMinScoreGain,
			},
			MaxRounds:        1,
			TargetSurfaceIDs: append([]string(nil), cfg.PromptIter.TargetSurfaceIDs...),
		}
		runResult, runErr := p.runPromptIter(
			ctx,
			runRequest,
			round,
			searchParent.Hash,
			report,
			&candidateReport,
		)
		promptIterTerminationErr := contextTerminationError(ctx, runErr)
		if promptIterTerminationErr != nil || runErr != nil {
			auditErr := runErr
			if promptIterTerminationErr != nil {
				auditErr = promptIterTerminationErr
			}
			candidateReport.Errors = append(candidateReport.Errors, auditErr.Error())
			if promptIterTerminationErr != nil {
				candidateReport.PromptIterStatus = string(promptiterengine.RunStatusCanceled)
				candidateReport.SearchDecision = notEvaluableDecision(auditErr.Error())
				candidateReport.ReleaseDecision = notEvaluableDecision(auditErr.Error())
				candidateReport.Transition = unchangedTransition(
					searchParent.Hash,
					releasedParent.Hash,
					"native PromptIter run was canceled",
				)
				report.Candidates = append(report.Candidates, candidateReport)
				report.Status = PipelineRunFailed
				report.StopReason = StopNecessaryRunFailed
				report.Errors = append(
					report.Errors,
					fmt.Sprintf("round %d promptiter canceled: %v", round, auditErr),
				)
				finalizeReport(report, state)
				return report, promptIterTerminationErr
			}
			if errors.Is(runErr, errPromptIterBudgetExhausted) {
				candidateReport.PromptIterStatus = "budget_stopped"
				candidateReport.SearchDecision = notEvaluableDecision(runErr.Error())
				candidateReport.ReleaseDecision = notEvaluableDecision(runErr.Error())
				candidateReport.Transition = unchangedTransition(
					searchParent.Hash,
					releasedParent.Hash,
					"native PromptIter stopped at the model-call budget",
				)
				report.Candidates = append(report.Candidates, candidateReport)
				report.Status = PipelineBudgetStopped
				report.StopReason = StopBudgetExhausted
				report.Errors = append(
					report.Errors,
					fmt.Sprintf("round %d promptiter budget: %v", round, runErr),
				)
				break
			}
			candidateReport.PromptIterStatus = string(promptiterengine.RunStatusFailed)
			candidateReport.Transition = unchangedTransition(
				searchParent.Hash,
				releasedParent.Hash,
				"native PromptIter run failed",
			)
			report.Candidates = append(report.Candidates, candidateReport)
			report.Status = PipelineRunFailed
			report.StopReason = StopNecessaryRunFailed
			report.Errors = append(report.Errors, fmt.Sprintf("round %d promptiter: %v", round, runErr))
			break
		}
		if runResult != nil {
			candidateReport.PromptIterStatus = string(runResult.Status)
		}
		if err := validatePromptIterResult(runResult, runRequest, structure); err != nil {
			candidateReport.Errors = append(candidateReport.Errors, err.Error())
			candidateReport.Transition = unchangedTransition(
				searchParent.Hash,
				releasedParent.Hash,
				"native PromptIter result is not evaluable",
			)
			report.Candidates = append(report.Candidates, candidateReport)
			report.Status = PipelineRunFailed
			report.StopReason = StopNecessaryRunFailed
			report.Errors = append(report.Errors, fmt.Sprintf("round %d promptiter result: %v", round, err))
			break
		}
		engineRound := runResult.Rounds[0]
		if engineRound.OutputProfile == nil || engineRound.Patches == nil ||
			len(engineRound.Patches.Patches) == 0 {
			candidateReport.Errors = append(candidateReport.Errors, "native PromptIter produced no candidate patches")
			candidateReport.Transition = unchangedTransition(
				searchParent.Hash,
				releasedParent.Hash,
				"no candidate was generated",
			)
			report.Candidates = append(report.Candidates, candidateReport)
			report.StopReason = StopNoCandidate
			break
		}
		candidateProfile, err := bindProfileToStructure(engineRound.OutputProfile, structure)
		if err != nil {
			candidateReport.Errors = append(candidateReport.Errors, err.Error())
			candidateReport.Transition = unchangedTransition(
				searchParent.Hash,
				releasedParent.Hash,
				"candidate profile is invalid",
			)
			report.Candidates = append(report.Candidates, candidateReport)
			report.Status = PipelineRunFailed
			report.StopReason = StopNecessaryRunFailed
			report.Errors = append(report.Errors, fmt.Sprintf("round %d profile: %v", round, err))
			break
		}
		candidate, err := buildProfileRecord(
			ProfileCandidate,
			candidateProfile,
			cfg.PromptIter.TargetSurfaceIDs[0],
			structure,
		)
		if err != nil {
			candidateReport.Errors = append(candidateReport.Errors, err.Error())
			candidateReport.Transition = unchangedTransition(
				searchParent.Hash,
				releasedParent.Hash,
				"candidate profile is invalid",
			)
			report.Candidates = append(report.Candidates, candidateReport)
			report.Status = PipelineRunFailed
			report.StopReason = StopNecessaryRunFailed
			report.Errors = append(report.Errors, fmt.Sprintf("round %d profile: %v", round, err))
			break
		}
		candidateReport.ID = fmt.Sprintf("candidate-%02d-%s", round, shortHash(candidate.Hash))
		candidateReport.Profile = &candidate
		candidateReport.Patches = adaptPatches(engineRound.Patches)
		candidateReport.OptimizationReason = patchReasons(engineRound.Patches)
		if stopped, reason := budgetStop(cfg.Gate, report.Resources.Cumulative); stopped {
			candidateReport.Errors = append(candidateReport.Errors, reason)
			candidateReport.SearchDecision = notEvaluableDecision(reason)
			candidateReport.ReleaseDecision = notEvaluableDecision(reason)
			candidateReport.Transition = unchangedTransition(
				searchParent.Hash,
				releasedParent.Hash,
				"budget stopped candidate before outer evaluation",
			)
			report.Candidates = append(report.Candidates, candidateReport)
			report.Status = PipelineBudgetStopped
			report.StopReason = StopBudgetExhausted
			break
		}
		if _, repeated := seenProfiles[candidate.Hash]; repeated {
			reason := "candidate repeats an evaluated profile fingerprint"
			candidateReport.Errors = append(
				candidateReport.Errors,
				"candidate profile fingerprint was already evaluated",
			)
			candidateReport.SearchDecision = notEvaluableDecision(reason)
			candidateReport.ReleaseDecision = notEvaluableDecision(reason)
			candidateReport.Transition = unchangedTransition(
				searchParent.Hash,
				releasedParent.Hash,
				"repeated candidate fingerprint",
			)
			report.Candidates = append(report.Candidates, candidateReport)
			report.StopReason = StopRepeatedFingerprint
			break
		}
		seenProfiles[candidate.Hash] = struct{}{}

		candidateTrain, candidateTrainErr := p.evaluateSnapshot(
			ctx,
			&cfg,
			candidate.Profile,
			candidate.Hash,
			cfg.Train,
			"train",
			"candidate_train",
			round,
			&report.Resources,
			&candidateReport.Resources,
		)
		candidateReport.Train = candidateTrain
		candidateTrainTerminationErr := contextTerminationError(ctx, candidateTrainErr)
		if candidateTrainTerminationErr != nil ||
			candidateTrainErr != nil ||
			!snapshotCompleted(candidateTrain) {
			auditErr := candidateTrainErr
			if candidateTrainTerminationErr != nil {
				auditErr = candidateTrainTerminationErr
			}
			candidateReport.Status = EvaluationNotEvaluable
			candidateReport.Errors = appendErrors(candidateReport.Errors, auditErr)
			candidateReport.SearchDecision = notEvaluableDecision(
				"candidate train evaluation is not complete",
			)
			candidateReport.ReleaseDecision = notEvaluableDecision(
				"candidate train evaluation is not complete",
			)
			candidateReport.Transition = unchangedTransition(
				searchParent.Hash,
				releasedParent.Hash,
				"candidate train evaluation failed",
			)
			report.Candidates = append(report.Candidates, candidateReport)
			report.Status = PipelineRunFailed
			report.StopReason = StopNecessaryRunFailed
			report.Errors = append(
				report.Errors,
				fmt.Sprintf("round %d candidate train evaluation is not complete", round),
			)
			if candidateTrainTerminationErr != nil {
				finalizeReport(report, state)
				return report, candidateTrainTerminationErr
			}
			break
		}
		if stopped, reason := budgetStop(cfg.Gate, report.Resources.Cumulative); stopped {
			candidateReport.Errors = append(candidateReport.Errors, reason)
			candidateReport.SearchDecision = notEvaluableDecision(reason)
			candidateReport.ReleaseDecision = notEvaluableDecision(reason)
			candidateReport.Transition = unchangedTransition(
				searchParent.Hash,
				releasedParent.Hash,
				"budget stopped candidate before held-out evaluation",
			)
			report.Candidates = append(report.Candidates, candidateReport)
			report.Status = PipelineBudgetStopped
			report.StopReason = StopBudgetExhausted
			break
		}
		candidateValidation, candidateValidationErr := p.evaluateSnapshot(
			ctx,
			&cfg,
			candidate.Profile,
			candidate.Hash,
			cfg.Validation,
			"heldout_validation",
			"candidate_validation",
			round,
			&report.Resources,
			&candidateReport.Resources,
		)
		candidateReport.Validation = candidateValidation
		candidateValidationTerminationErr := contextTerminationError(ctx, candidateValidationErr)
		if candidateValidationTerminationErr != nil ||
			candidateValidationErr != nil ||
			!snapshotCompleted(candidateValidation) {
			auditErr := candidateValidationErr
			if candidateValidationTerminationErr != nil {
				auditErr = candidateValidationTerminationErr
			}
			candidateReport.Status = EvaluationNotEvaluable
			candidateReport.Errors = appendErrors(candidateReport.Errors, auditErr)
			candidateReport.SearchDecision = notEvaluableDecision(
				"candidate held-out evaluation is not complete",
			)
			candidateReport.ReleaseDecision = notEvaluableDecision("candidate held-out evaluation is not complete")
			transition, transitionErr := ApplyStateTransition(
				&state,
				candidate,
				candidateTrain,
				candidateValidation,
				candidateReport.SearchDecision,
				candidateReport.ReleaseDecision,
			)
			if transitionErr != nil {
				candidateReport.Errors = append(candidateReport.Errors, transitionErr.Error())
				candidateReport.Transition = unchangedTransition(
					searchParent.Hash,
					releasedParent.Hash,
					"candidate state transition failed",
				)
			} else {
				candidateReport.Transition = transition
			}
			report.Candidates = append(report.Candidates, candidateReport)
			report.Status = PipelineRunFailed
			report.StopReason = StopNecessaryRunFailed
			report.Errors = append(
				report.Errors,
				fmt.Sprintf("round %d candidate evaluation is not complete", round),
			)
			if candidateValidationTerminationErr != nil {
				finalizeReport(report, state)
				return report, candidateValidationTerminationErr
			}
			break
		}
		candidateReport.Status = EvaluationCompleted
		candidate.EvaluationRunID = candidateValidation.Provenance.RunID
		candidateReport.Profile = &candidate
		vsInitial, initialErr := CalculateDelta(
			"vs_initial",
			state.InitialValidation,
			candidateValidation,
			cfg.Gate,
		)
		vsSearchParent, searchValidationErr := CalculateDelta(
			"vs_search_parent",
			searchValidationParent,
			candidateValidation,
			cfg.Gate,
		)
		vsReleased, releasedValidationErr := CalculateDelta(
			"vs_released",
			releasedValidationParent,
			candidateValidation,
			cfg.Gate,
		)
		searchTrainDelta, searchTrainErr := CalculateDelta(
			"search_train",
			searchTrainParent,
			candidateTrain,
			cfg.Gate,
		)
		if deltaErr := errors.Join(
			initialErr,
			searchValidationErr,
			releasedValidationErr,
			searchTrainErr,
		); deltaErr != nil {
			candidateReport.Status = EvaluationNotEvaluable
			candidateReport.Errors = append(candidateReport.Errors, deltaErr.Error())
			candidateReport.SearchDecision = notEvaluableDecision("candidate train delta is not evaluable")
			candidateReport.ReleaseDecision = notEvaluableDecision("candidate held-out delta is not evaluable")
			candidateReport.Transition = unchangedTransition(
				searchParent.Hash,
				releasedParent.Hash,
				"candidate deltas are not evaluable",
			)
			report.Candidates = append(report.Candidates, candidateReport)
			report.Status = PipelineRunFailed
			report.StopReason = StopNecessaryRunFailed
			report.Errors = append(report.Errors, fmt.Sprintf("round %d deltas: %v", round, deltaErr))
			break
		}
		candidateReport.Deltas = &DeltaSet{
			VsInitial:      vsInitial,
			VsSearchParent: vsSearchParent,
			VsReleased:     vsReleased,
		}
		candidateReport.SearchDecision = decideSearchFromNative(
			engineRound.Acceptance,
			searchTrainDelta,
		)
		candidateReport.ReleaseDecision = DecideRelease(
			cfg.Gate,
			vsReleased,
			report.Resources.Cumulative,
		)
		transition, err := ApplyStateTransition(
			&state,
			candidate,
			candidateTrain,
			candidateValidation,
			candidateReport.SearchDecision,
			candidateReport.ReleaseDecision,
		)
		if err != nil {
			candidateReport.Status = EvaluationNotEvaluable
			candidateReport.Errors = append(candidateReport.Errors, err.Error())
			candidateReport.SearchDecision = notEvaluableDecision("state transition is not evaluable")
			candidateReport.ReleaseDecision = notEvaluableDecision("state transition is not evaluable")
			candidateReport.Transition = unchangedTransition(
				searchParent.Hash,
				releasedParent.Hash,
				"candidate state transition failed",
			)
			report.Candidates = append(report.Candidates, candidateReport)
			report.Status = PipelineRunFailed
			report.StopReason = StopNecessaryRunFailed
			report.Errors = append(report.Errors, fmt.Sprintf("round %d transition: %v", round, err))
			break
		}
		candidateReport.Transition = transition
		report.Candidates = append(report.Candidates, candidateReport)
		if stopped, reason := budgetStop(cfg.Gate, report.Resources.Cumulative); stopped {
			report.Status = PipelineBudgetStopped
			report.StopReason = StopBudgetExhausted
			if candidateReport.ReleaseDecision.Status == DecisionNotEvaluable {
				report.Errors = append(report.Errors, reason)
			}
			break
		}
		if candidateReport.SearchDecision.Status == DecisionNotEvaluable ||
			candidateReport.ReleaseDecision.Status == DecisionNotEvaluable {
			report.Status = PipelineRunFailed
			report.StopReason = StopNecessaryRunFailed
			report.Errors = append(
				report.Errors,
				fmt.Sprintf("round %d candidate decision is not evaluable", round),
			)
			break
		}
		if round == cfg.PromptIter.MaxOuterRounds {
			report.StopReason = StopMaxRounds
		}
	}
	finalizeReport(report, state)
	return report, nil
}

func contextTerminationError(ctx context.Context, operationErr error) error {
	ctxErr := ctx.Err()
	if operationErr == nil {
		return ctxErr
	}
	if errors.Is(operationErr, context.Canceled) ||
		errors.Is(operationErr, context.DeadlineExceeded) {
		if ctxErr != nil && !errors.Is(operationErr, ctxErr) {
			return errors.Join(operationErr, ctxErr)
		}
		return operationErr
	}
	if ctxErr != nil {
		return errors.Join(operationErr, ctxErr)
	}
	return nil
}

func (p *Pipeline) runPromptIter(
	ctx context.Context,
	request *promptiterengine.RunRequest,
	round int,
	profileHash string,
	report *Report,
	candidate *CandidateReport,
) (*promptiterengine.RunResult, error) {
	lastMeter := snapshotMeter(p.options.resourceMeter)
	lastEventAt := time.Now()
	eventsSeen := 0
	observer := func(ctx context.Context, event *promptiterengine.Event) error {
		now := time.Now()
		usage := measuredUsage(
			p.options.resourceMeter,
			lastMeter,
			now.Sub(lastEventAt).Milliseconds(),
		)
		lastMeter = snapshotMeter(p.options.resourceMeter)
		lastEventAt = now
		stage := "promptiter_unknown"
		if event != nil {
			stage = "promptiter_" + string(event.Kind)
		}
		p.appendEntry(report, candidate, ResourceEntry{
			Stage:       stage,
			Round:       round,
			Split:       "train_internal_validation",
			ProfileHash: profileHash,
			Usage:       usage,
		})
		eventsSeen++
		if p.options.engineObserver != nil {
			if err := p.options.engineObserver(ctx, event); err != nil {
				return err
			}
		}
		if stopped, reason := budgetStop(
			report.ResolvedConfig.Gate,
			report.Resources.Cumulative,
		); stopped {
			return fmt.Errorf("%w: %s", errPromptIterBudgetExhausted, reason)
		}
		return nil
	}
	result, err := p.engine.Run(
		ctx,
		request,
		promptiterengine.WithObserver(observer),
	)
	if eventsSeen == 0 || err != nil {
		stage := "promptiter_unobserved"
		if err != nil {
			stage = "promptiter_failed"
		}
		p.appendEntry(report, candidate, ResourceEntry{
			Stage:       stage,
			Round:       round,
			Split:       "train_internal_validation",
			ProfileHash: profileHash,
			Usage: measuredUsage(
				p.options.resourceMeter,
				lastMeter,
				time.Since(lastEventAt).Milliseconds(),
			),
			Failed: err != nil,
		})
	}
	return result, err
}

func decideSearchFromNative(
	acceptance *promptiterengine.AcceptanceDecision,
	outerTrainDelta DeltaSummary,
) Decision {
	if acceptance == nil {
		return notEvaluableDecision("native PromptIter acceptance decision is nil")
	}
	if math.IsNaN(acceptance.ScoreDelta) || math.IsInf(acceptance.ScoreDelta, 0) {
		return notEvaluableDecision("native PromptIter acceptance score delta is not finite")
	}
	reason := strings.TrimSpace(acceptance.Reason)
	if reason == "" {
		reason = "native PromptIter returned no acceptance reason"
	}
	reasons := []string{
		reason,
		fmt.Sprintf("outer full-train score delta: %.6f", outerTrainDelta.ScoreDelta),
	}
	status := DecisionRejected
	if acceptance.Accepted {
		status = DecisionAccepted
	}
	return Decision{
		Status:     status,
		Reasons:    reasons,
		ScoreDelta: float64Pointer(acceptance.ScoreDelta),
	}
}

// ApplyStateTransition applies the fixed independent search/release state
// matrix. If either decision is not evaluable, neither pointer is updated.
func ApplyStateTransition(
	state *ProfileState,
	candidate ProfileRecord,
	candidateTrain *EvaluationSnapshot,
	candidateValidation *EvaluationSnapshot,
	searchDecision Decision,
	releaseDecision Decision,
) (StateTransition, error) {
	if state == nil {
		return StateTransition{}, errors.New("profile state is nil")
	}
	transition := unchangedTransition(
		state.Search.Hash,
		state.Released.Hash,
		"candidate rejected by search and release",
	)
	if searchDecision.Status == DecisionNotEvaluable ||
		releaseDecision.Status == DecisionNotEvaluable {
		transition.Explanation = "not-evaluable decision leaves search and release unchanged"
		return transition, nil
	}
	if !validEvaluableDecision(searchDecision.Status) {
		return StateTransition{}, fmt.Errorf("search decision status %q is invalid", searchDecision.Status)
	}
	if !validEvaluableDecision(releaseDecision.Status) {
		return StateTransition{}, fmt.Errorf("release decision status %q is invalid", releaseDecision.Status)
	}
	if candidate.Hash == "" {
		return StateTransition{}, errors.New("candidate profile hash is empty")
	}
	if searchDecision.Status == DecisionAccepted {
		if err := validateCandidateSnapshot("train", candidate.Hash, candidateTrain); err != nil {
			return StateTransition{}, err
		}
		if err := validateCandidateSnapshot("validation", candidate.Hash, candidateValidation); err != nil {
			return StateTransition{}, err
		}
		state.Search = withProfileRole(candidate, ProfileSearch)
		state.SearchTrain = candidateTrain
		state.SearchValidation = candidateValidation
		transition.SearchAfter = candidate.Hash
		transition.SearchUpdated = true
	}
	if releaseDecision.Status == DecisionAccepted {
		if err := validateCandidateSnapshot("validation", candidate.Hash, candidateValidation); err != nil {
			return StateTransition{}, err
		}
		state.Released = withProfileRole(candidate, ProfileReleased)
		state.ReleasedValidation = candidateValidation
		transition.ReleasedAfter = candidate.Hash
		transition.ReleaseUpdated = true
	}
	switch {
	case transition.SearchUpdated && transition.ReleaseUpdated:
		transition.Explanation = "candidate passed both search and release objectives and advanced both profiles"
	case transition.SearchUpdated:
		transition.Explanation = "candidate passed the search objective but failed release gates and advanced search only"
	case transition.ReleaseUpdated:
		transition.Explanation = "candidate passed the release objective but not the search objective and advanced released only"
	default:
		transition.Explanation = "candidate passed neither the search nor release objective; both profiles remain unchanged"
	}
	return transition, nil
}

//nolint:gocyclo // Fail-closed configuration checks enumerate independent invariants.
func validateRunConfig(config *RunConfig) error {
	if config == nil {
		return errors.New("run config is nil")
	}
	if err := validatePromptIterConfig(PromptIterConfig{
		SchemaVersion: SchemaVersion,
		Seed:          config.Seed,
		Policy:        config.PromptIter,
	}); err != nil {
		return fmt.Errorf("promptiter policy: %w", err)
	}
	if err := validateGatePolicy(config.Gate); err != nil {
		return fmt.Errorf("gate policy: %w", err)
	}
	if err := validateOutputConfig(config.Output); err != nil {
		return fmt.Errorf("output config: %w", err)
	}
	switch {
	case strings.TrimSpace(config.ReportID) == "":
		return errors.New("report id is empty")
	case strings.TrimSpace(config.RunID) == "":
		return errors.New("run id is empty")
	case config.GeneratedAt.IsZero():
		return errors.New("generated at is required")
	case config.InitialProfile == nil:
		return errors.New("initial profile is nil")
	case config.Train.EvalSetID == config.Validation.EvalSetID:
		return errors.New("train and held-out validation eval set ids must differ")
	case strings.TrimSpace(config.EvaluatorConfigHash) == "":
		return errors.New("evaluator config hash is empty")
	case strings.TrimSpace(config.MetricPolicyHash) == "":
		return errors.New("metric policy hash is empty")
	case strings.TrimSpace(config.Runtime.Engine) == "":
		return errors.New("runtime engine is empty")
	case config.Runtime.Seed != config.Seed:
		return fmt.Errorf(
			"runtime seed %d does not match run seed %d",
			config.Runtime.Seed,
			config.Seed,
		)
	case config.EvidenceLimit <= 0:
		return errors.New("evidence limit must be greater than zero")
	case config.EvidenceLimit > 100:
		return errors.New("evidence limit must not exceed 100")
	}
	if _, offset := config.GeneratedAt.Zone(); offset != 0 {
		return errors.New("generated at must use UTC")
	}
	for _, item := range []struct {
		name    string
		dataset DatasetSpec
	}{
		{name: "train", dataset: config.Train},
		{name: "validation", dataset: config.Validation},
	} {
		name := item.name
		dataset := item.dataset
		if strings.TrimSpace(dataset.EvalSetID) == "" ||
			strings.TrimSpace(dataset.EvalSetHash) == "" ||
			strings.TrimSpace(dataset.MetricsHash) == "" {
			return fmt.Errorf("%s dataset provenance is incomplete", name)
		}
		if len(dataset.CaseIDs) == 0 {
			return fmt.Errorf("%s case inventory is empty", name)
		}
		if len(dataset.MetricNames) == 0 {
			return fmt.Errorf("%s metric inventory is empty", name)
		}
		if err := validateUniqueNonempty(name+" case", dataset.CaseIDs); err != nil {
			return err
		}
		if err := validateUniqueNonempty(name+" metric", dataset.MetricNames); err != nil {
			return err
		}
		if len(dataset.NormalizedInputHashes) != len(dataset.CaseIDs) {
			return fmt.Errorf(
				"%s normalized input hash inventory has %d entries, want %d",
				name,
				len(dataset.NormalizedInputHashes),
				len(dataset.CaseIDs),
			)
		}
		hashOwners := make(map[string]string, len(dataset.CaseIDs))
		caseIDs := stringSet(dataset.CaseIDs)
		for caseID := range dataset.NormalizedInputHashes {
			if _, ok := caseIDs[caseID]; !ok {
				return fmt.Errorf(
					"%s normalized input hash has unexpected case %q",
					name,
					caseID,
				)
			}
		}
		for _, caseID := range dataset.CaseIDs {
			inputHash := strings.TrimSpace(dataset.NormalizedInputHashes[caseID])
			if inputHash == "" {
				return fmt.Errorf(
					"%s normalized input hash for case %q is missing",
					name,
					caseID,
				)
			}
			if previous, exists := hashOwners[inputHash]; exists {
				return fmt.Errorf(
					"%s cases %q and %q have duplicate normalized input hashes",
					name,
					previous,
					caseID,
				)
			}
			hashOwners[inputHash] = caseID
		}
	}
	if err := validateHeldoutExclusion(config.Train, config.Validation); err != nil {
		return err
	}
	if err := verifyInventory("train/validation metric", config.Train.MetricNames, config.Validation.MetricNames); err != nil {
		return err
	}
	if config.Train.MetricsHash != config.Validation.MetricsHash {
		return errors.New("train and validation metrics hashes differ")
	}
	metricNames := stringSet(config.Train.MetricNames)
	if _, ok := metricNames[config.Gate.PrimaryMetric]; !ok {
		return fmt.Errorf("primary metric %q is not in dataset inventory", config.Gate.PrimaryMetric)
	}
	if len(config.Gate.MetricDirections) != len(metricNames) {
		return fmt.Errorf(
			"metric direction inventory has %d entries, want %d",
			len(config.Gate.MetricDirections),
			len(metricNames),
		)
	}
	for metricName := range config.Gate.MetricDirections {
		if _, ok := metricNames[metricName]; !ok {
			return fmt.Errorf("metric direction has unexpected metric %q", metricName)
		}
	}
	for _, metricName := range config.Train.MetricNames {
		switch config.Gate.MetricDirections[metricName] {
		case ScoreHigherIsBetter, ScoreLowerIsBetter:
		default:
			return fmt.Errorf("metric %q has no valid score direction", metricName)
		}
	}
	validationCases := stringSet(config.Validation.CaseIDs)
	for _, item := range []struct {
		name    string
		caseIDs []string
	}{
		{name: "critical case", caseIDs: config.CriticalCaseIDs},
		{name: "hard failure case", caseIDs: config.HardFailureCaseIDs},
	} {
		if err := validateUniqueNonempty(item.name, item.caseIDs); err != nil {
			return err
		}
		for _, caseID := range item.caseIDs {
			if _, ok := validationCases[caseID]; !ok {
				return fmt.Errorf("%s %q is not in held-out validation", item.name, caseID)
			}
		}
	}
	requiredInputHashes := []string{
		"trainEvalSet",
		"validationEvalSet",
		"metrics",
		"baselinePrompt",
		"promptIterConfig",
		"regressionConfig",
	}
	if len(config.InputHashes) != len(requiredInputHashes) {
		return fmt.Errorf(
			"input hash inventory has %d entries, want %d",
			len(config.InputHashes),
			len(requiredInputHashes),
		)
	}
	for _, name := range requiredInputHashes {
		if strings.TrimSpace(config.InputHashes[name]) == "" {
			return fmt.Errorf("input hash %q is empty", name)
		}
	}
	switch {
	case config.InputHashes["trainEvalSet"] != config.Train.EvalSetHash:
		return errors.New("train eval-set input hash does not match train dataset hash")
	case config.InputHashes["validationEvalSet"] != config.Validation.EvalSetHash:
		return errors.New("validation eval-set input hash does not match validation dataset hash")
	case config.InputHashes["metrics"] != config.Train.MetricsHash:
		return errors.New("metrics input hash does not match dataset metrics hash")
	}
	gateJSON, err := json.Marshal(config.Gate)
	if err != nil {
		return fmt.Errorf("marshal metric gate policy: %w", err)
	}
	expectedMetricPolicyHash := hashStrings(
		"native-metric-policy-v1",
		config.InputHashes["metrics"],
		string(gateJSON),
	)
	if config.MetricPolicyHash != expectedMetricPolicyHash {
		return errors.New("metric policy hash does not match metrics and gate policy")
	}
	expectedBinding := *config
	if err := BindRuntime(&expectedBinding, config.Runtime); err != nil {
		return fmt.Errorf("bind runtime: %w", err)
	}
	if config.RunID != expectedBinding.RunID {
		return fmt.Errorf("run id %q does not match input and runtime fingerprint", config.RunID)
	}
	if config.EvaluatorConfigHash != expectedBinding.EvaluatorConfigHash {
		return errors.New("evaluator config hash does not match runtime fingerprint")
	}
	expectedSourceHash, err := sourceConfigFingerprint(config)
	if err != nil {
		return err
	}
	if strings.TrimSpace(config.sourceConfigHash) == "" {
		return errors.New("source configuration binding is empty")
	}
	if config.sourceConfigHash != expectedSourceHash {
		return errors.New("source configuration does not match the loaded input binding")
	}
	if _, err := resolveInternalValidation(config.Train, config.PromptIter); err != nil {
		return err
	}
	return nil
}

func validateProfileAndTargets(
	snapshot *astructure.Snapshot,
	profile *promptiter.Profile,
	targetSurfaceIDs []string,
) error {
	if snapshot == nil {
		return errors.New("promptiter structure snapshot is nil")
	}
	if profile.StructureID != "" && profile.StructureID != snapshot.StructureID {
		return fmt.Errorf(
			"initial profile structure id %q does not match %q",
			profile.StructureID,
			snapshot.StructureID,
		)
	}
	surfaces := make(map[string]struct{}, len(snapshot.Surfaces))
	for _, surface := range snapshot.Surfaces {
		surfaces[surface.SurfaceID] = struct{}{}
	}
	for _, target := range targetSurfaceIDs {
		if _, ok := surfaces[target]; !ok {
			return fmt.Errorf("target surface id %q is not in structure", target)
		}
	}
	return nil
}

func bindProfileToStructure(
	profile *promptiter.Profile,
	structure *astructure.Snapshot,
) (*promptiter.Profile, error) {
	if profile == nil {
		return nil, errors.New("profile is nil")
	}
	if structure == nil || strings.TrimSpace(structure.StructureID) == "" {
		return nil, errors.New("promptiter structure id is empty")
	}
	if profile.StructureID != "" && profile.StructureID != structure.StructureID {
		return nil, fmt.Errorf(
			"profile structure id %q does not match %q",
			profile.StructureID,
			structure.StructureID,
		)
	}
	cloned := *profile
	cloned.Overrides = append([]promptiter.SurfaceOverride(nil), profile.Overrides...)
	cloned.StructureID = structure.StructureID
	return &cloned, nil
}

func resolveInternalValidation(
	train DatasetSpec,
	policy PromptIterPolicy,
) ([]string, error) {
	switch policy.InternalValidationStrategy {
	case internalValidationTrainCaseIDs:
		if len(policy.InternalValidationCaseIDs) == 0 {
			return nil, errors.New("train_case_ids internal validation requires case ids")
		}
	case internalValidationTrainAll:
		if len(policy.InternalValidationCaseIDs) != 0 {
			return nil, errors.New("train_all internal validation must not configure case ids")
		}
		return append([]string(nil), train.CaseIDs...), nil
	default:
		return nil, fmt.Errorf(
			"internal validation strategy %q is unsupported",
			policy.InternalValidationStrategy,
		)
	}
	trainCases := stringSet(train.CaseIDs)
	seen := make(map[string]struct{}, len(policy.InternalValidationCaseIDs))
	for _, caseID := range policy.InternalValidationCaseIDs {
		if _, ok := trainCases[caseID]; !ok {
			return nil, fmt.Errorf(
				"internal validation case %q is not in the train inventory",
				caseID,
			)
		}
		if _, ok := seen[caseID]; ok {
			return nil, fmt.Errorf("internal validation case %q is duplicated", caseID)
		}
		seen[caseID] = struct{}{}
	}
	return append([]string(nil), policy.InternalValidationCaseIDs...), nil
}

func buildLossHints(snapshot *EvaluationSnapshot) ([]promptiterengine.LossHint, error) {
	if !snapshotCompleted(snapshot) {
		return nil, errors.New("search train snapshot is not complete")
	}
	attributionIndex := make(map[string]FailureAttribution, len(snapshot.Attributions))
	for _, attribution := range snapshot.Attributions {
		if attribution.EvalSetID != snapshot.Provenance.EvalSetID ||
			attribution.EvaluationRunID != snapshot.Provenance.RunID ||
			attribution.ProfileHash != snapshot.Provenance.ProfileHash {
			return nil, fmt.Errorf(
				"attribution for case %q metric %q has incompatible provenance",
				attribution.EvalCaseID,
				attribution.MetricName,
			)
		}
		key := attribution.EvalCaseID + "\x00" + attribution.MetricName
		if _, ok := attributionIndex[key]; ok {
			return nil, fmt.Errorf(
				"duplicate attribution for case %q metric %q",
				attribution.EvalCaseID,
				attribution.MetricName,
			)
		}
		attributionIndex[key] = attribution
	}
	hints := make([]promptiterengine.LossHint, 0)
	for _, evalCase := range snapshot.Cases {
		for _, resultMetric := range evalCase.Metrics {
			if resultMetric.Passed {
				continue
			}
			key := evalCase.CaseID + "\x00" + resultMetric.MetricName
			attribution, ok := attributionIndex[key]
			if !ok {
				return nil, fmt.Errorf(
					"failed case %q metric %q has no attribution",
					evalCase.CaseID,
					resultMetric.MetricName,
				)
			}
			reason := strings.TrimSpace(attribution.Reason)
			if reason == "" {
				return nil, fmt.Errorf(
					"attribution for case %q metric %q has no reason",
					evalCase.CaseID,
					resultMetric.MetricName,
				)
			}
			evidence := lossHintEvidence(attribution.Evidence)
			if evidence != "" {
				reason += "; evidence: " + evidence
			}
			hints = append(hints, promptiterengine.LossHint{
				EvalCaseID: evalCase.CaseID,
				MetricName: resultMetric.MetricName,
				Severity:   promptiter.LossSeverity(attribution.Severity),
				Reason: fmt.Sprintf(
					"%s: %s",
					attribution.PrimaryCategory,
					reason,
				),
			})
		}
	}
	sort.SliceStable(hints, func(i, j int) bool {
		if hints[i].EvalCaseID != hints[j].EvalCaseID {
			return hints[i].EvalCaseID < hints[j].EvalCaseID
		}
		return hints[i].MetricName < hints[j].MetricName
	})
	return hints, nil
}

func lossHintEvidence(evidence []EvidenceReference) string {
	const (
		maxReferences = 4
		maxRunes      = 480
	)
	parts := make([]string, 0, min(len(evidence), maxReferences))
	for index, reference := range evidence {
		if index >= maxReferences {
			break
		}
		summary := strings.TrimSpace(reference.Summary)
		if summary == "" {
			continue
		}
		label := strings.TrimSpace(reference.Kind)
		if reference.ID != "" {
			label += "[" + reference.ID + "]"
		}
		if label == "" {
			label = "evidence"
		}
		parts = append(parts, label+": "+summary)
	}
	joined := strings.Join(parts, " | ")
	runes := []rune(joined)
	if len(runes) > maxRunes {
		joined = string(runes[:maxRunes]) + "…"
	}
	return joined
}

func (p *Pipeline) evaluateSnapshot(
	ctx context.Context,
	config *RunConfig,
	profile *promptiter.Profile,
	profileHash string,
	dataset DatasetSpec,
	split string,
	stage string,
	round int,
	globalLedger *ResourceLedger,
	candidateLedger *ResourceLedger,
) (*EvaluationSnapshot, error) {
	runID := fmt.Sprintf("%s/%s", config.RunID, stage)
	if round > 0 {
		runID = fmt.Sprintf("%s/%d", runID, round)
	}
	request := SnapshotRequest{
		EvaluationRunID:     runID,
		Profile:             profile,
		ExpectedProfileHash: profileHash,
		Dataset:             dataset,
		Split:               split,
		Seed:                config.Seed,
		EvaluatorConfigHash: config.EvaluatorConfigHash,
		MetricPolicyHash:    config.MetricPolicyHash,
		PrimaryMetric:       config.Gate.PrimaryMetric,
		MetricDirections:    config.Gate.MetricDirections,
		CriticalCaseIDs:     config.CriticalCaseIDs,
		HardFailureCaseIDs:  config.HardFailureCaseIDs,
		EvidenceLimit:       config.EvidenceLimit,
	}
	snapshot, evaluateErr := p.evaluator.Evaluate(ctx, request)
	responseErr := validateSnapshotResponse(request, snapshot)
	var contractErr error
	switch {
	case evaluateErr == nil && snapshot != nil && snapshot.Status != EvaluationCompleted:
		contractErr = fmt.Errorf(
			"snapshot evaluator returned status %q without an error",
			snapshot.Status,
		)
	case evaluateErr != nil && snapshotCompleted(snapshot):
		contractErr = errors.New("snapshot evaluator returned a completed snapshot with an error")
	}
	err := errors.Join(evaluateErr, responseErr, contractErr)
	if (responseErr != nil || contractErr != nil) && snapshot != nil {
		snapshot.Status = EvaluationNotEvaluable
		snapshot.Error = strings.TrimSpace(strings.Join(
			[]string{snapshot.Error, errors.Join(responseErr, contractErr).Error()},
			"\n",
		))
	}
	entry := ResourceEntry{
		Stage:       stage,
		Round:       round,
		Split:       split,
		ProfileHash: profileHash,
		Failed:      err != nil || !snapshotCompleted(snapshot),
	}
	if snapshot != nil {
		entry.Usage = snapshot.Resources
	}
	appendResourceEntry(globalLedger, entry, p.options.resourceObserver)
	if candidateLedger != nil {
		appendResourceEntry(candidateLedger, entry, nil)
	}
	return snapshot, err
}

//nolint:gocyclo // Snapshot completeness and binding checks stay linear for auditability.
func validateSnapshotResponse(
	request SnapshotRequest,
	snapshot *EvaluationSnapshot,
) error {
	if snapshot == nil {
		return errors.New("snapshot evaluator returned a nil snapshot")
	}
	expectedProvenance := EvaluationProvenance{
		RunID:               request.EvaluationRunID,
		ProfileHash:         request.ExpectedProfileHash,
		EvalSetID:           request.Dataset.EvalSetID,
		EvalSetHash:         request.Dataset.EvalSetHash,
		MetricsHash:         request.Dataset.MetricsHash,
		Split:               request.Split,
		Seed:                request.Seed,
		EvaluatorConfigHash: request.EvaluatorConfigHash,
		MetricPolicyHash:    request.MetricPolicyHash,
	}
	if snapshot.Provenance != expectedProvenance {
		return fmt.Errorf(
			"snapshot provenance does not match request: got %+v, want %+v",
			snapshot.Provenance,
			expectedProvenance,
		)
	}
	if !equalStringsExact(request.Dataset.CaseIDs, snapshot.Inventory.CaseIDs) {
		return fmt.Errorf(
			"snapshot case inventory %v does not exactly match request %v",
			snapshot.Inventory.CaseIDs,
			request.Dataset.CaseIDs,
		)
	}
	if !equalStringsExact(request.Dataset.MetricNames, snapshot.Inventory.MetricNames) {
		return fmt.Errorf(
			"snapshot metric inventory %v does not exactly match request %v",
			snapshot.Inventory.MetricNames,
			request.Dataset.MetricNames,
		)
	}
	switch snapshot.Status {
	case EvaluationCompleted, EvaluationNotEvaluable, EvaluationRunFailed:
	default:
		return fmt.Errorf("snapshot status %q is invalid", snapshot.Status)
	}
	if snapshot.LatencyMS < 0 {
		return errors.New("snapshot latency is negative")
	}
	if reasons := validateResourceUsage(snapshot.Resources); len(reasons) > 0 {
		return fmt.Errorf("snapshot resources are invalid: %s", strings.Join(reasons, "; "))
	}
	if request.EvidenceLimit <= 0 {
		return errors.New("snapshot request evidence limit is invalid")
	}

	caseInventory := stringSet(request.Dataset.CaseIDs)
	metricInventory := stringSet(request.Dataset.MetricNames)
	criticalCases := stringSet(request.CriticalCaseIDs)
	hardFailureCases := stringSet(request.HardFailureCaseIDs)
	caseSeen := make(map[string]struct{}, len(snapshot.Cases))
	failedMetrics := make(map[string]struct{})
	passedCases := 0
	failedCases := 0
	primaryScoreTotal := 0.0
	for caseIndex, evalCase := range snapshot.Cases {
		if _, ok := caseInventory[evalCase.CaseID]; !ok {
			return fmt.Errorf("snapshot contains unexpected case %q", evalCase.CaseID)
		}
		if _, duplicate := caseSeen[evalCase.CaseID]; duplicate {
			return fmt.Errorf("snapshot contains duplicate case %q", evalCase.CaseID)
		}
		caseSeen[evalCase.CaseID] = struct{}{}
		if evalCase.EvalSetID != request.Dataset.EvalSetID {
			return fmt.Errorf(
				"case %q eval-set id %q does not match %q",
				evalCase.CaseID,
				evalCase.EvalSetID,
				request.Dataset.EvalSetID,
			)
		}
		if evalCase.PrimaryMetric != request.PrimaryMetric {
			return fmt.Errorf(
				"case %q primary metric %q does not match %q",
				evalCase.CaseID,
				evalCase.PrimaryMetric,
				request.PrimaryMetric,
			)
		}
		if !isComparableResultStatus(evalCase.Status) {
			return fmt.Errorf(
				"case %q status %q is not comparable",
				evalCase.CaseID,
				evalCase.Status,
			)
		}
		if !statusMatchesPassed(evalCase.Status, evalCase.Passed) {
			return fmt.Errorf(
				"case %q status %q disagrees with passed=%t",
				evalCase.CaseID,
				evalCase.Status,
				evalCase.Passed,
			)
		}
		if _, expected := criticalCases[evalCase.CaseID]; evalCase.Critical != expected {
			return fmt.Errorf("case %q critical flag does not match request", evalCase.CaseID)
		}
		if _, expected := hardFailureCases[evalCase.CaseID]; evalCase.HardFailure != expected {
			return fmt.Errorf("case %q hard-failure flag does not match request", evalCase.CaseID)
		}
		if len(evalCase.ToolTrajectory) > request.EvidenceLimit ||
			len(evalCase.ExpectedTools) > request.EvidenceLimit ||
			len(evalCase.Trace) > request.EvidenceLimit {
			return fmt.Errorf("case %q exceeds evidence limit %d", evalCase.CaseID, request.EvidenceLimit)
		}
		if evalCase.ExpectNoTools && len(evalCase.ExpectedTools) > 0 {
			return fmt.Errorf(
				"case %q has both an explicit no-tool expectation and expected tool calls",
				evalCase.CaseID,
			)
		}
		if snapshot.Status == EvaluationCompleted && strings.TrimSpace(evalCase.Error) != "" {
			return fmt.Errorf(
				"completed case %q contains operational error %q",
				evalCase.CaseID,
				evalCase.Error,
			)
		}
		metricSeen := make(map[string]struct{}, len(evalCase.Metrics))
		allMetricsPassed := true
		primaryFound := false
		for metricIndex, resultMetric := range evalCase.Metrics {
			if _, ok := metricInventory[resultMetric.MetricName]; !ok {
				return fmt.Errorf(
					"case %q contains unexpected metric %q",
					evalCase.CaseID,
					resultMetric.MetricName,
				)
			}
			if _, duplicate := metricSeen[resultMetric.MetricName]; duplicate {
				return fmt.Errorf(
					"case %q contains duplicate metric %q",
					evalCase.CaseID,
					resultMetric.MetricName,
				)
			}
			metricSeen[resultMetric.MetricName] = struct{}{}
			if resultMetric.Direction != request.MetricDirections[resultMetric.MetricName] {
				return fmt.Errorf(
					"case %q metric %q direction %q does not match request",
					evalCase.CaseID,
					resultMetric.MetricName,
					resultMetric.Direction,
				)
			}
			if math.IsNaN(resultMetric.Score) || math.IsInf(resultMetric.Score, 0) ||
				math.IsNaN(resultMetric.Threshold) || math.IsInf(resultMetric.Threshold, 0) {
				return fmt.Errorf(
					"case %q metric %q score or threshold is not finite",
					evalCase.CaseID,
					resultMetric.MetricName,
				)
			}
			if !isComparableResultStatus(resultMetric.Status) {
				return fmt.Errorf(
					"case %q metric %q status %q is not comparable",
					evalCase.CaseID,
					resultMetric.MetricName,
					resultMetric.Status,
				)
			}
			if !statusMatchesPassed(resultMetric.Status, resultMetric.Passed) {
				return fmt.Errorf(
					"case %q metric %q status %q disagrees with passed=%t",
					evalCase.CaseID,
					resultMetric.MetricName,
					resultMetric.Status,
					resultMetric.Passed,
				)
			}
			if !resultMetric.Passed {
				allMetricsPassed = false
				failedMetrics[evalCase.CaseID+"\x00"+resultMetric.MetricName] = struct{}{}
			}
			if resultMetric.MetricName == request.PrimaryMetric {
				primaryFound = true
				primaryScoreTotal += resultMetric.Score
			}
			if snapshot.Status == EvaluationCompleted &&
				(caseIndex >= len(request.Dataset.CaseIDs) ||
					metricIndex >= len(request.Dataset.MetricNames) ||
					resultMetric.MetricName != request.Dataset.MetricNames[metricIndex]) {
				return fmt.Errorf(
					"case %q metric inventory does not exactly match request order",
					evalCase.CaseID,
				)
			}
		}
		if snapshot.Status != EvaluationCompleted {
			continue
		}
		if caseIndex >= len(request.Dataset.CaseIDs) ||
			evalCase.CaseID != request.Dataset.CaseIDs[caseIndex] {
			return errors.New("snapshot case results do not exactly match request order")
		}
		if len(evalCase.Metrics) != len(request.Dataset.MetricNames) {
			return fmt.Errorf(
				"case %q has %d metrics, want %d",
				evalCase.CaseID,
				len(evalCase.Metrics),
				len(request.Dataset.MetricNames),
			)
		}
		if !primaryFound {
			return fmt.Errorf(
				"case %q has no primary metric %q",
				evalCase.CaseID,
				request.PrimaryMetric,
			)
		}
		if evalCase.Passed != allMetricsPassed {
			return fmt.Errorf("case %q pass state disagrees with metric states", evalCase.CaseID)
		}
		if evalCase.Passed {
			passedCases++
		} else {
			failedCases++
		}
	}

	attributionSeen := make(map[string]struct{}, len(snapshot.Attributions))
	for _, attribution := range snapshot.Attributions {
		key := attribution.EvalCaseID + "\x00" + attribution.MetricName
		if _, duplicate := attributionSeen[key]; duplicate {
			return fmt.Errorf(
				"snapshot contains duplicate attribution for case %q metric %q",
				attribution.EvalCaseID,
				attribution.MetricName,
			)
		}
		attributionSeen[key] = struct{}{}
		if _, failed := failedMetrics[key]; !failed {
			return fmt.Errorf(
				"attribution for case %q metric %q is not bound to a failed metric",
				attribution.EvalCaseID,
				attribution.MetricName,
			)
		}
		if attribution.EvalSetID != request.Dataset.EvalSetID ||
			attribution.EvaluationRunID != request.EvaluationRunID ||
			attribution.ProfileHash != request.ExpectedProfileHash {
			return fmt.Errorf(
				"attribution for case %q metric %q does not match request provenance",
				attribution.EvalCaseID,
				attribution.MetricName,
			)
		}
		if strings.TrimSpace(attribution.Reason) == "" {
			return fmt.Errorf(
				"attribution for case %q metric %q has no reason",
				attribution.EvalCaseID,
				attribution.MetricName,
			)
		}
		if len(attribution.Evidence) == 0 {
			return fmt.Errorf(
				"attribution for case %q metric %q has no evidence",
				attribution.EvalCaseID,
				attribution.MetricName,
			)
		}
		if len(attribution.Evidence) > request.EvidenceLimit {
			return fmt.Errorf(
				"attribution for case %q metric %q exceeds evidence limit %d",
				attribution.EvalCaseID,
				attribution.MetricName,
				request.EvidenceLimit,
			)
		}
		for _, evidence := range attribution.Evidence {
			if strings.TrimSpace(evidence.Kind) == "" ||
				strings.TrimSpace(evidence.Summary) == "" {
				return fmt.Errorf(
					"attribution for case %q metric %q has incomplete evidence",
					attribution.EvalCaseID,
					attribution.MetricName,
				)
			}
		}
	}
	if snapshot.Status != EvaluationCompleted {
		return nil
	}
	if strings.TrimSpace(snapshot.Error) != "" {
		return fmt.Errorf("completed snapshot contains error %q", snapshot.Error)
	}
	if len(snapshot.Cases) != len(request.Dataset.CaseIDs) {
		return fmt.Errorf(
			"snapshot has %d cases, want %d",
			len(snapshot.Cases),
			len(request.Dataset.CaseIDs),
		)
	}
	if snapshot.Passed != passedCases || snapshot.Failed != failedCases ||
		snapshot.Passed+snapshot.Failed != len(snapshot.Cases) {
		return fmt.Errorf(
			"snapshot pass/fail counts %d/%d do not match case results %d/%d",
			snapshot.Passed,
			snapshot.Failed,
			passedCases,
			failedCases,
		)
	}
	if math.IsNaN(snapshot.OverallScore) || math.IsInf(snapshot.OverallScore, 0) {
		return errors.New("snapshot overall score is not finite")
	}
	expectedOverallScore := primaryScoreTotal / float64(len(snapshot.Cases))
	if math.Abs(snapshot.OverallScore-expectedOverallScore) > DefaultEpsilon {
		return fmt.Errorf(
			"snapshot overall score %.12f does not match primary metric mean %.12f",
			snapshot.OverallScore,
			expectedOverallScore,
		)
	}
	if len(attributionSeen) != len(failedMetrics) {
		return fmt.Errorf(
			"snapshot has %d failure attributions, want %d",
			len(attributionSeen),
			len(failedMetrics),
		)
	}
	for key := range failedMetrics {
		if _, ok := attributionSeen[key]; !ok {
			return errors.New("snapshot has a failed metric without attribution")
		}
	}
	return nil
}

func equalStringsExact(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (p *Pipeline) appendEntry(
	report *Report,
	candidate *CandidateReport,
	entry ResourceEntry,
) {
	appendResourceEntry(&report.Resources, entry, p.options.resourceObserver)
	appendResourceEntry(&candidate.Resources, entry, nil)
}

//nolint:gocyclo // Native result lineage checks intentionally identify the exact mismatch.
func validatePromptIterResult(
	result *promptiterengine.RunResult,
	request *promptiterengine.RunRequest,
	structure *astructure.Snapshot,
) error {
	switch {
	case result == nil:
		return errors.New("native PromptIter result is nil")
	case request == nil:
		return errors.New("native PromptIter request is nil")
	case structure == nil:
		return errors.New("native PromptIter structure is nil")
	case result.Status != promptiterengine.RunStatusSucceeded:
		return fmt.Errorf("native PromptIter status is %q", result.Status)
	case result.CurrentRound != 1:
		return fmt.Errorf("native PromptIter current round is %d, want 1", result.CurrentRound)
	case result.BaselineValidation == nil:
		return errors.New("native PromptIter baseline validation is nil")
	case len(result.Rounds) != 1:
		return fmt.Errorf("native PromptIter returned %d rounds, want 1", len(result.Rounds))
	}
	round := result.Rounds[0]
	switch {
	case round.Round != 1:
		return fmt.Errorf("native PromptIter round number is %d, want 1", round.Round)
	case round.InputProfile == nil:
		return errors.New("native PromptIter round input profile is nil")
	case round.Train == nil:
		return errors.New("native PromptIter round train result is nil")
	case round.Validation == nil:
		return errors.New("native PromptIter round validation result is nil")
	case round.Acceptance == nil:
		return errors.New("native PromptIter round acceptance is nil")
	case math.IsNaN(round.Acceptance.ScoreDelta) || math.IsInf(round.Acceptance.ScoreDelta, 0):
		return errors.New("native PromptIter round acceptance score delta is not finite")
	case strings.TrimSpace(round.Acceptance.Reason) == "":
		return errors.New("native PromptIter round acceptance reason is empty")
	case round.Patches == nil:
		return errors.New("native PromptIter round patch set is nil")
	case round.OutputProfile == nil:
		return errors.New("native PromptIter round output profile is nil")
	case result.AcceptedProfile == nil:
		return errors.New("native PromptIter accepted profile is nil")
	}
	expectedInput, err := normalizePromptIterProfile(structure, request.InitialProfile)
	if err != nil {
		return fmt.Errorf("normalize native PromptIter request profile: %w", err)
	}
	if err := requireSameProfile(
		"native PromptIter round input",
		expectedInput,
		round.InputProfile,
	); err != nil {
		return err
	}
	expectedOutput, err := applyPromptIterPatches(
		structure,
		expectedInput,
		round.Patches,
		request.TargetSurfaceIDs,
	)
	if err != nil {
		return fmt.Errorf("validate native PromptIter patches: %w", err)
	}
	if err := requireSameProfile(
		"native PromptIter round output",
		expectedOutput,
		round.OutputProfile,
	); err != nil {
		return err
	}
	expectedAccepted := expectedInput
	if round.Acceptance.Accepted {
		expectedAccepted = expectedOutput
	}
	return requireSameProfile(
		"native PromptIter accepted",
		expectedAccepted,
		result.AcceptedProfile,
	)
}

func normalizePromptIterProfile(
	structure *astructure.Snapshot,
	profile *promptiter.Profile,
) (*promptiter.Profile, error) {
	compiledStructure, err := profilecompiler.NewStructure(structure)
	if err != nil {
		return nil, err
	}
	compilerProfile := &profilecompiler.Profile{}
	if profile != nil {
		compilerProfile.StructureID = profile.StructureID
		compilerProfile.Overrides = make(
			[]profilecompiler.SurfaceOverride,
			0,
			len(profile.Overrides),
		)
		for _, override := range profile.Overrides {
			compilerProfile.Overrides = append(
				compilerProfile.Overrides,
				profilecompiler.SurfaceOverride{
					SurfaceID: override.SurfaceID,
					Value:     override.Value,
				},
			)
		}
	}
	normalized, err := compiledStructure.NormalizeProfile(compilerProfile)
	if err != nil {
		return nil, err
	}
	result := &promptiter.Profile{
		StructureID: normalized.StructureID,
		Overrides: make(
			[]promptiter.SurfaceOverride,
			0,
			len(normalized.Overrides),
		),
	}
	for _, override := range normalized.Overrides {
		result.Overrides = append(result.Overrides, promptiter.SurfaceOverride{
			SurfaceID: override.SurfaceID,
			Value:     override.Value,
		})
	}
	return result, nil
}

func applyPromptIterPatches(
	structure *astructure.Snapshot,
	input *promptiter.Profile,
	patchSet *promptiter.PatchSet,
	targetSurfaceIDs []string,
) (*promptiter.Profile, error) {
	if patchSet == nil {
		return nil, errors.New("patch set is nil")
	}
	targets := stringSet(targetSurfaceIDs)
	surfaces := make(map[string]astructure.Surface, len(structure.Surfaces))
	for _, surface := range structure.Surfaces {
		surfaces[surface.SurfaceID] = surface
	}
	overrides := make(map[string]promptiter.SurfaceOverride, len(input.Overrides)+len(patchSet.Patches))
	for _, override := range input.Overrides {
		overrides[override.SurfaceID] = override
	}
	seen := make(map[string]struct{}, len(patchSet.Patches))
	for _, patch := range patchSet.Patches {
		if strings.TrimSpace(patch.SurfaceID) == "" {
			return nil, errors.New("patch surface id is empty")
		}
		if strings.TrimSpace(patch.Reason) == "" {
			return nil, fmt.Errorf("patch %q reason is empty", patch.SurfaceID)
		}
		if _, exists := seen[patch.SurfaceID]; exists {
			return nil, fmt.Errorf("duplicate patch surface id %q", patch.SurfaceID)
		}
		seen[patch.SurfaceID] = struct{}{}
		if _, allowed := targets[patch.SurfaceID]; !allowed {
			return nil, fmt.Errorf(
				"patch surface id %q is outside configured target surfaces",
				patch.SurfaceID,
			)
		}
		surface, exists := surfaces[patch.SurfaceID]
		if !exists {
			return nil, fmt.Errorf("patch surface id %q is not in structure", patch.SurfaceID)
		}
		value, err := profilecompiler.SanitizePatchValue(surface, patch.Value)
		if err != nil {
			return nil, fmt.Errorf("sanitize patch %q: %w", patch.SurfaceID, err)
		}
		overrides[patch.SurfaceID] = promptiter.SurfaceOverride{
			SurfaceID: patch.SurfaceID,
			Value:     value,
		}
	}
	result := &promptiter.Profile{
		StructureID: structure.StructureID,
		Overrides:   make([]promptiter.SurfaceOverride, 0, len(overrides)),
	}
	for _, override := range overrides {
		result.Overrides = append(result.Overrides, override)
	}
	sort.SliceStable(result.Overrides, func(i, j int) bool {
		return result.Overrides[i].SurfaceID < result.Overrides[j].SurfaceID
	})
	return result, nil
}

func requireSameProfile(label string, expected, actual *promptiter.Profile) error {
	expectedHash, err := ProfileFingerprint(expected)
	if err != nil {
		return fmt.Errorf("fingerprint expected %s profile: %w", label, err)
	}
	actualHash, err := ProfileFingerprint(actual)
	if err != nil {
		return fmt.Errorf("fingerprint actual %s profile: %w", label, err)
	}
	if actualHash != expectedHash {
		return fmt.Errorf(
			"%s profile hash %q does not match expected %q",
			label,
			actualHash,
			expectedHash,
		)
	}
	return nil
}

func buildProfileRecord(
	role ProfileRole,
	profile *promptiter.Profile,
	targetSurfaceID string,
	structure *astructure.Snapshot,
) (ProfileRecord, error) {
	if profile == nil {
		return ProfileRecord{}, errors.New("profile is nil")
	}
	hash, err := ProfileFingerprint(profile)
	if err != nil {
		return ProfileRecord{}, err
	}
	prompt, err := profileSurfaceText(profile, targetSurfaceID, structure)
	if err != nil {
		return ProfileRecord{}, err
	}
	structureID := profile.StructureID
	if structureID == "" && structure != nil {
		structureID = structure.StructureID
	}
	return ProfileRecord{
		Role:            role,
		Hash:            hash,
		StructureID:     structureID,
		TargetSurfaceID: targetSurfaceID,
		Prompt:          prompt,
		Profile:         profile,
	}, nil
}

func profileSurfaceText(
	profile *promptiter.Profile,
	targetSurfaceID string,
	structure *astructure.Snapshot,
) (string, error) {
	for _, override := range profile.Overrides {
		if override.SurfaceID != targetSurfaceID {
			continue
		}
		if override.Value.Text != nil {
			return *override.Value.Text, nil
		}
		encoded, err := json.Marshal(override.Value)
		if err != nil {
			return "", err
		}
		return string(encoded), nil
	}
	if structure != nil {
		for _, surface := range structure.Surfaces {
			if surface.SurfaceID != targetSurfaceID {
				continue
			}
			if surface.Value.Text != nil {
				return *surface.Value.Text, nil
			}
			encoded, err := json.Marshal(surface.Value)
			if err != nil {
				return "", err
			}
			return string(encoded), nil
		}
	}
	return "", fmt.Errorf("target surface %q is absent from profile and structure", targetSurfaceID)
}

func adaptPatches(patchSet *promptiter.PatchSet) []PatchRecord {
	if patchSet == nil {
		return nil
	}
	patches := make([]PatchRecord, 0, len(patchSet.Patches))
	for _, patch := range patchSet.Patches {
		value := ""
		if patch.Value.Text != nil {
			value = *patch.Value.Text
		} else if encoded, err := json.Marshal(patch.Value); err == nil {
			value = string(encoded)
		}
		patches = append(patches, PatchRecord{
			SurfaceID: patch.SurfaceID,
			Value:     value,
			Reason:    patch.Reason,
		})
	}
	return patches
}

func patchReasons(patchSet *promptiter.PatchSet) string {
	if patchSet == nil {
		return ""
	}
	reasons := make([]string, 0, len(patchSet.Patches))
	for _, patch := range patchSet.Patches {
		if reason := strings.TrimSpace(patch.Reason); reason != "" {
			reasons = append(reasons, reason)
		}
	}
	return strings.Join(reasons, "; ")
}

func newReport(config RunConfig, initial ProfileRecord) *Report {
	generatedAt := config.GeneratedAt
	if generatedAt.IsZero() {
		generatedAt = time.Now().UTC()
	}
	return &Report{
		SchemaVersion:  SchemaVersion,
		ReportID:       config.ReportID,
		RunID:          config.RunID,
		GeneratedAt:    generatedAt,
		Status:         PipelineSucceeded,
		StopReason:     StopMaxRounds,
		ResolvedConfig: resolvedConfig(config),
		InputHashes:    cloneStringMap(config.InputHashes),
		Runtime:        config.Runtime,
		InitialProfile: initial,
		SearchProfile:  withProfileRole(initial, ProfileSearch),
		ReleasedProfile: withProfileRole(
			initial,
			ProfileReleased,
		),
		Candidates: []CandidateReport{},
		Resources:  ResourceLedger{Entries: []ResourceEntry{}},
		FinalDecision: Decision{
			Status:  DecisionRejected,
			Reasons: []string{"no candidate has passed held-out release gates"},
		},
	}
}

func resolvedConfig(config RunConfig) ResolvedConfig {
	return ResolvedConfig{
		Seed:               config.Seed,
		Train:              config.Train,
		Validation:         config.Validation,
		PromptIter:         config.PromptIter,
		Gate:               config.Gate,
		Output:             config.Output,
		CriticalCaseIDs:    append([]string(nil), config.CriticalCaseIDs...),
		HardFailureCaseIDs: append([]string(nil), config.HardFailureCaseIDs...),
		EvidenceLimit:      config.EvidenceLimit,
	}
}

func finalizeReport(report *Report, state ProfileState) {
	report.SearchProfile = state.Search
	report.ReleasedProfile = state.Released
	if state.Released.Hash != "" && state.Released.Hash != state.Initial.Hash {
		report.FinalDecision = Decision{
			Status:  DecisionAccepted,
			Reasons: []string{fmt.Sprintf("released profile %s passed held-out gates", state.Released.Hash)},
		}
		return
	}
	if len(report.Candidates) > 0 {
		last := report.Candidates[len(report.Candidates)-1]
		report.FinalDecision = last.ReleaseDecision
	}
}

func snapshotCompleted(snapshot *EvaluationSnapshot) bool {
	return snapshot != nil && snapshot.Status == EvaluationCompleted
}

func snapshotFailedCount(snapshot *EvaluationSnapshot) int {
	if snapshot == nil {
		return 0
	}
	if snapshot.Failed > 0 {
		return snapshot.Failed
	}
	failed := 0
	for _, evalCase := range snapshot.Cases {
		if !evalCase.Passed {
			failed++
		}
	}
	return failed
}

func budgetStop(policy GatePolicy, usage ResourceUsage) (bool, string) {
	if policy.MaxCumulativeModelCalls <= 0 {
		return false, ""
	}
	if !usage.ModelCalls.Available {
		return true, "configured model-call budget cannot be evaluated because call count is unavailable"
	}
	if usage.ModelCalls.Value >= policy.MaxCumulativeModelCalls {
		return true, fmt.Sprintf(
			"cumulative model calls %d reached budget %d",
			usage.ModelCalls.Value,
			policy.MaxCumulativeModelCalls,
		)
	}
	return false, ""
}

func validEvaluableDecision(status DecisionStatus) bool {
	return status == DecisionAccepted || status == DecisionRejected
}

func validateCandidateSnapshot(
	name string,
	profileHash string,
	snapshot *EvaluationSnapshot,
) error {
	if !snapshotCompleted(snapshot) {
		return fmt.Errorf("candidate %s snapshot is not complete", name)
	}
	if snapshot.Provenance.ProfileHash != profileHash {
		return fmt.Errorf(
			"candidate %s snapshot profile hash %q does not match %q",
			name,
			snapshot.Provenance.ProfileHash,
			profileHash,
		)
	}
	return nil
}

func withProfileRole(profile ProfileRecord, role ProfileRole) ProfileRecord {
	profile.Role = role
	return profile
}

func unchangedTransition(searchHash, releasedHash, explanation string) StateTransition {
	return StateTransition{
		SearchBefore:   searchHash,
		SearchAfter:    searchHash,
		ReleasedBefore: releasedHash,
		ReleasedAfter:  releasedHash,
		Explanation:    explanation,
	}
}

func notEvaluableDecision(reason string) Decision {
	return Decision{
		Status:  DecisionNotEvaluable,
		Reasons: []string{reason},
	}
}

func appendErrors(target []string, values ...error) []string {
	for _, value := range values {
		if value != nil {
			target = append(target, value.Error())
		}
	}
	return target
}

func shortHash(hash string) string {
	const length = 12
	if len(hash) <= length {
		return hash
	}
	return hash[:length]
}

func float64Pointer(value float64) *float64 {
	return &value
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
