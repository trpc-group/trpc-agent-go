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
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/internal/profilecompiler"
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
	Rounds          []roundReport
}

func (p *pipeline) run(ctx context.Context) (*pipelineResult, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	snapshot, err := p.engine.Describe(ctx)
	if err != nil {
		return nil, fmt.Errorf("describe agent structure: %w", err)
	}
	structure, err := profilecompiler.NewStructure(snapshot)
	if err != nil {
		return nil, fmt.Errorf("build profile structure: %w", err)
	}
	initial, err := normalizeProfile(structure, nil)
	if err != nil {
		return nil, fmt.Errorf("normalize initial profile: %w", err)
	}
	if _, ok := structure.SurfaceIndex[p.targetSurfaceID]; !ok {
		return nil, fmt.Errorf("target surface %q does not exist", p.targetSurfaceID)
	}

	baselineTrain, err := p.evaluateProfile(ctx, structure, initial, p.cfg.TrainEvalSetID, p.trainCatalog)
	if err != nil {
		return nil, fmt.Errorf("evaluate baseline training set: %w", err)
	}
	baselineValidation, err := p.evaluateProfile(ctx, structure, initial, p.cfg.ValidationEvalSetID, p.validationCatalog)
	if err != nil {
		return nil, fmt.Errorf("evaluate baseline held-out set: %w", err)
	}
	result := &pipelineResult{
		InitialProfile: initial, SearchProfile: initial, ReleasedProfile: initial,
		BaselineTrain: baselineTrain, Baseline: baselineValidation,
		Rounds: make([]roundReport, 0, p.cfg.MaxRounds),
	}
	releasedSnapshot := baselineValidation
	lossHints := trainingLossHints(baselineTrain)

	for round := 1; round <= p.cfg.MaxRounds; round++ {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := p.ledger.canReserve(promptIterReservation(p.trainCatalog), p.gatePolicy()); err != nil {
			return result, fmt.Errorf("reserve promptiter budget: %w", err)
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
		candidate, err = normalizeProfile(structure, candidate)
		if err != nil {
			result.Rounds = append(result.Rounds, failedRound(round, releasedSnapshot, p.ledger.snapshot(), err.Error()))
			continue
		}
		if err := p.ledger.canReserve(validationReservation(p.validationCatalog), p.gatePolicy()); err != nil {
			result.Rounds = append(result.Rounds, failedRound(round, releasedSnapshot, p.ledger.snapshot(), err.Error()))
			continue
		}
		candidateSnapshot, evalErr := p.evaluateProfile(
			ctx, structure, candidate, p.cfg.ValidationEvalSetID, p.validationCatalog,
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
	structure *profilecompiler.Structure,
	profile *promptiter.Profile,
	evalSetID string,
	expected *catalog,
) (evaluationSnapshot, error) {
	runOptions, err := compileProfileOptions(structure, profile)
	if err != nil {
		return evaluationSnapshot{}, fmt.Errorf("compile profile: %w", err)
	}
	result, err := p.evaluator.Evaluate(ctx, evalSetID,
		evaluation.WithRunOptions(runOptions...),
		evaluation.WithEvalCaseParallelism(p.cfg.EvalCaseParallelism),
		evaluation.WithEvalCaseParallelInferenceEnabled(p.cfg.ParallelInference),
		evaluation.WithEvalCaseParallelEvaluationEnabled(p.cfg.ParallelEvaluation),
	)
	if err != nil {
		return evaluationSnapshot{}, err
	}
	return normalizeEvaluation(result, expected)
}

func (p *pipeline) runRequest(profile *promptiter.Profile, hints []promptiterengine.LossHint) *promptiterengine.RunRequest {
	train := promptiterengine.EvalSetInput{EvalSetID: p.cfg.TrainEvalSetID, LossHints: hints}
	return &promptiterengine.RunRequest{
		Train:          []promptiterengine.EvalSetInput{train},
		Validation:     []promptiterengine.EvalSetInput{{EvalSetID: p.cfg.SearchEvalSetID}},
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

func normalizeProfile(structure *profilecompiler.Structure, profile *promptiter.Profile) (*promptiter.Profile, error) {
	var compilerProfile *profilecompiler.Profile
	if profile != nil {
		compilerProfile = &profilecompiler.Profile{StructureID: profile.StructureID}
		for _, override := range profile.Overrides {
			compilerProfile.Overrides = append(compilerProfile.Overrides, profilecompiler.SurfaceOverride{
				SurfaceID: override.SurfaceID, Value: override.Value,
			})
		}
	}
	normalized, err := structure.NormalizeProfile(compilerProfile)
	if err != nil {
		return nil, err
	}
	result := &promptiter.Profile{StructureID: normalized.StructureID, Overrides: make([]promptiter.SurfaceOverride, 0, len(normalized.Overrides))}
	for _, override := range normalized.Overrides {
		result.Overrides = append(result.Overrides, promptiter.SurfaceOverride{SurfaceID: override.SurfaceID, Value: override.Value})
	}
	return result, nil
}

func compileProfileOptions(structure *profilecompiler.Structure, profile *promptiter.Profile) ([]agent.RunOption, error) {
	normalized, err := normalizeProfile(structure, profile)
	if err != nil {
		return nil, err
	}
	compilerProfile := &profilecompiler.Profile{StructureID: normalized.StructureID}
	for _, override := range normalized.Overrides {
		compilerProfile.Overrides = append(compilerProfile.Overrides, profilecompiler.SurfaceOverride{SurfaceID: override.SurfaceID, Value: override.Value})
	}
	compilerProfile, err = structure.NormalizeProfile(compilerProfile)
	if err != nil {
		return nil, err
	}
	options, err := profilecompiler.CompileRunOptions(compilerProfile, true)
	if err != nil {
		return nil, err
	}
	if len(compilerProfile.Overrides) != 0 {
		options = append(options, profilecompiler.WithProfile(compilerProfile))
	}
	return options, nil
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
			if metric.Status == "passed" {
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
		if evalCase.Status != "passed" {
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

func validationReservation(expected *catalog) usageSummary {
	return usageSummary{ModelCalls: knownInt(len(expected.EvalCaseIDs))}
}
