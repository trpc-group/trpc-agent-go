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

	"trpc.group/trpc-go/trpc-agent-go/agent"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

type pipeline struct {
	cfg               config
	engine            promptiterengine.Engine
	evaluator         evaluation.AgentEvaluator
	ledger            *ledger
	trainCatalog      *catalog
	validationCatalog *catalog
	targetSurfaceID   string
}

type pipelineResult struct {
	InitialProfile  *promptiter.Profile
	SearchProfile   *promptiter.Profile
	ReleasedProfile *promptiter.Profile
	BaselineTrain   evaluationSnapshot
	Baseline        evaluationSnapshot
	Released        evaluationSnapshot
	Rounds          []roundReport
}

func (p *pipeline) run(ctx context.Context) (*pipelineResult, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	p.ledger.setModelCallLimit(p.cfg.MaxModelCalls)
	snapshot, err := p.engine.Describe(ctx)
	if err != nil {
		return nil, fmt.Errorf("describe agent structure: %w", err)
	}
	initial, initialRunOptions, err := promptiterengine.CompileProfile(snapshot, nil)
	if err != nil {
		return nil, fmt.Errorf("normalize initial profile: %w", err)
	}
	if !snapshotHasSurface(snapshot, p.targetSurfaceID) {
		return nil, fmt.Errorf("target surface %q does not exist", p.targetSurfaceID)
	}

	if err := p.canReserve(baselineReservation(
		p.trainCatalog,
		p.validationCatalog,
	)); err != nil {
		return nil, fmt.Errorf("reserve complete baseline budget: %w", err)
	}
	baselineTrain, err := p.evaluateProfile(
		ctx, initialRunOptions, p.cfg.TrainEvalSetID, p.trainCatalog,
	)
	if err != nil {
		return nil, fmt.Errorf("evaluate baseline training set: %w", err)
	}
	baselineValidation, err := p.evaluateProfile(
		ctx, initialRunOptions, p.cfg.ValidationEvalSetID, p.validationCatalog,
	)
	if err != nil {
		return nil, fmt.Errorf("evaluate baseline held-out set: %w", err)
	}
	result := &pipelineResult{
		InitialProfile: initial, SearchProfile: initial, ReleasedProfile: initial,
		BaselineTrain: baselineTrain, Baseline: baselineValidation,
		Released: baselineValidation,
		Rounds:   make([]roundReport, 0, p.cfg.MaxRounds),
	}
	releasedSnapshot := baselineValidation
	lossHints := trainingLossHints(baselineTrain)

	for round := 1; round <= p.cfg.MaxRounds; round++ {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := p.canReserve(promptIterReservation(p.trainCatalog)); err != nil {
			reservationErr := fmt.Errorf("reserve promptiter budget: %w", err)
			result.Rounds = append(result.Rounds, failedRound(
				round, releasedSnapshot, p.ledger.snapshot(), reservationErr.Error(),
			))
			return result, reservationErr
		}
		request := p.runRequest(result.SearchProfile, lossHints)
		engineResult, runErr := p.engine.Run(ctx, request)
		if runErr != nil {
			result.Rounds = append(result.Rounds, failedRound(round, releasedSnapshot, p.ledger.snapshot(), runErr.Error()))
			return result, fmt.Errorf("run promptiter round %d: %w", round, runErr)
		}
		candidate, candidateErr := outputProfile(engineResult)
		if len(engineResult.Rounds) == 1 {
			lossHints = engineLossHints(engineResult.Rounds[0].Losses)
		}
		if candidateErr != nil {
			result.Rounds = append(result.Rounds, failedRound(round, releasedSnapshot, p.ledger.snapshot(), candidateErr.Error()))
			continue
		}
		candidate, candidateRunOptions, err := promptiterengine.CompileProfile(
			snapshot, candidate,
		)
		if err != nil {
			result.Rounds = append(result.Rounds, failedRound(round, releasedSnapshot, p.ledger.snapshot(), err.Error()))
			continue
		}
		if err := p.canReserve(evaluationReservation(p.validationCatalog)); err != nil {
			result.Rounds = append(result.Rounds, failedRound(round, releasedSnapshot, p.ledger.snapshot(), err.Error()))
			continue
		}
		candidateSnapshot, evalErr := p.evaluateProfile(
			ctx, candidateRunOptions, p.cfg.ValidationEvalSetID, p.validationCatalog,
		)
		if evalErr != nil {
			result.Rounds = append(result.Rounds, failedRound(round, releasedSnapshot, p.ledger.snapshot(), evalErr.Error()))
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			continue
		}
		delta, deltaErr := compareSnapshots(releasedSnapshot, candidateSnapshot)
		decision := decide(gateInput{
			Policy: p.gatePolicy(), Baseline: releasedSnapshot, Candidate: candidateSnapshot,
			Usage: p.ledger.snapshot(), RunError: errorString(deltaErr),
		})
		roundReport := roundReport{
			Number: round, CandidatePrompt: profilePrompt(candidate, p.targetSurfaceID),
			CandidateSource: "promptiter_output_profile", Delta: delta, Gate: decision,
			Attributions: snapshotAttributions(candidateSnapshot), Usage: p.ledger.snapshot(),
		}
		result.Rounds = append(result.Rounds, roundReport)
		if decision.Accepted {
			result.ReleasedProfile = candidate
			result.SearchProfile = candidate
			result.Released = candidateSnapshot
			releasedSnapshot = candidateSnapshot
		}
	}
	return result, nil
}

func (p *pipeline) validate() error {
	switch {
	case p.engine == nil:
		return errors.New("pipeline engine is nil")
	case p.evaluator == nil:
		return errors.New("pipeline evaluator is nil")
	case p.ledger == nil:
		return errors.New("pipeline ledger is nil")
	case p.trainCatalog == nil:
		return errors.New("training catalog is nil")
	case p.validationCatalog == nil:
		return errors.New("validation catalog is nil")
	case p.targetSurfaceID == "":
		return errors.New("target surface ID is empty")
	default:
		return p.cfg.validate()
	}
}

func (p *pipeline) evaluateProfile(
	ctx context.Context,
	runOptions []agent.RunOption,
	evalSetID string,
	expected *catalog,
) (evaluationSnapshot, error) {
	result, err := p.evaluator.Evaluate(ctx, evalSetID,
		evaluation.WithRunOptions(runOptions...),
		evaluation.WithEvalCaseParallelism(p.cfg.EvalCaseParallelism),
		evaluation.WithEvalCaseParallelInferenceEnabled(p.cfg.ParallelInference),
		evaluation.WithEvalCaseParallelEvaluationEnabled(p.cfg.ParallelEvaluation),
	)
	if err != nil {
		return evaluationSnapshot{}, err
	}
	normalized, err := normalizeEvaluation(result, expected)
	if err != nil {
		return evaluationSnapshot{}, err
	}
	if p.cfg.Mode == modeDeterministic {
		normalized.Duration = 0
	}
	return normalized, nil
}

func (p *pipeline) runRequest(profile *promptiter.Profile, hints []promptiterengine.LossHint) *promptiterengine.RunRequest {
	train := promptiterengine.EvalSetInput{EvalSetID: p.cfg.TrainEvalSetID, LossHints: hints}
	return &promptiterengine.RunRequest{
		Train:          []promptiterengine.EvalSetInput{train},
		Validation:     []promptiterengine.EvalSetInput{{EvalSetID: p.cfg.TrainEvalSetID}},
		InitialProfile: profile,
		EvaluationOptions: promptiterengine.EvaluationOptions{
			EvalCaseParallelism:               p.cfg.EvalCaseParallelism,
			EvalCaseParallelInferenceEnabled:  p.cfg.ParallelInference,
			EvalCaseParallelEvaluationEnabled: p.cfg.ParallelEvaluation,
		},
		AcceptancePolicy: promptiterengine.AcceptancePolicy{MinScoreGain: 0},
		StopPolicy:       promptiterengine.StopPolicy{MaxRoundsWithoutAcceptance: 1},
		MaxRounds:        1, TargetSurfaceIDs: []string{p.targetSurfaceID},
	}
}

func (p *pipeline) gatePolicy() gatePolicy {
	return gatePolicy{
		MinGain: p.cfg.MinValidationGain, MaxHardFailures: p.cfg.MaxHardFailures,
		MaxCaseScoreDrop: p.cfg.MaxCaseScoreDrop, MaxModelCalls: p.cfg.MaxModelCalls,
		MaxToolCalls: p.cfg.MaxToolCalls, MaxTokens: p.cfg.MaxTokens,
		MaxEstimatedCost: p.cfg.MaxEstimatedCost, MaxLatencyMillis: p.cfg.MaxLatencyMillis,
		Critical: p.cfg.Critical,
	}
}

func (p *pipeline) canReserve(reservation usageSummary) error {
	return p.ledger.canReserve(reservation, gatePolicy{
		MaxModelCalls: p.cfg.MaxModelCalls,
	})
}

func snapshotHasSurface(snapshot *astructure.Snapshot, surfaceID string) bool {
	for _, surface := range snapshot.Surfaces {
		if surface.SurfaceID == surfaceID {
			return true
		}
	}
	return false
}

func outputProfile(result *promptiterengine.RunResult) (*promptiter.Profile, error) {
	if result == nil {
		return nil, errors.New("promptiter result is nil")
	}
	if result.Status != promptiterengine.RunStatusSucceeded {
		return nil, fmt.Errorf("promptiter status is %s: %s", result.Status, result.ErrorMessage)
	}
	if len(result.Rounds) != 1 {
		return nil, fmt.Errorf("promptiter returned %d rounds, expected one", len(result.Rounds))
	}
	if result.Rounds[0].OutputProfile == nil {
		return nil, errors.New("promptiter output profile is nil")
	}
	return result.Rounds[0].OutputProfile, nil
}

func trainingLossHints(snapshot evaluationSnapshot) []promptiterengine.LossHint {
	var hints []promptiterengine.LossHint
	for _, evalCase := range snapshot.Cases {
		for _, metric := range evalCase.Metrics {
			if metric.Status == status.EvalStatusPassed {
				continue
			}
			hints = append(hints, promptiterengine.LossHint{EvalCaseID: evalCase.EvalCaseID, MetricName: metric.Name, Reason: metric.Reason})
		}
	}
	return hints
}

func engineLossHints(losses []promptiter.CaseLoss) []promptiterengine.LossHint {
	var hints []promptiterengine.LossHint
	for _, evalCase := range losses {
		for _, loss := range evalCase.TerminalLosses {
			hints = append(hints, promptiterengine.LossHint{
				EvalCaseID: loss.EvalCaseID, MetricName: loss.MetricName,
				Severity: loss.Severity, Reason: loss.Loss,
			})
		}
	}
	return hints
}

func snapshotAttributions(snapshot evaluationSnapshot) []caseAttribution {
	var result []caseAttribution
	for _, evalCase := range snapshot.Cases {
		if evalCase.Status != status.EvalStatusPassed {
			result = append(result, attributeCase(evalCase))
		}
	}
	return result
}

func profilePrompt(profile *promptiter.Profile, surfaceID string) string {
	if profile == nil {
		return ""
	}
	for _, override := range profile.Overrides {
		if override.SurfaceID == surfaceID && override.Value.Text != nil {
			return *override.Value.Text
		}
	}
	return ""
}

func failedRound(number int, baseline evaluationSnapshot, usage usageSummary, message string) roundReport {
	return roundReport{
		Number: number, Error: message, Usage: usage,
		Gate: decide(gateInput{Policy: gatePolicy{}, Baseline: baseline, Candidate: baseline, Usage: usage, RunError: message}),
	}
}

func promptIterReservation(expected *catalog) usageSummary {
	calls := len(expected.EvalCaseIDs)*(len(expected.MetricNames)+2) + 2
	return usageSummary{ModelCalls: knownInt(calls)}
}

func evaluationReservation(expected *catalog) usageSummary {
	return usageSummary{ModelCalls: knownInt(evaluationModelCalls(expected))}
}

func baselineReservation(catalogs ...*catalog) usageSummary {
	calls := 0
	for _, expected := range catalogs {
		calls += evaluationModelCalls(expected)
	}
	return usageSummary{ModelCalls: knownInt(calls)}
}

func evaluationModelCalls(expected *catalog) int {
	return len(expected.EvalCaseIDs) * (len(expected.MetricNames) + 1)
}
