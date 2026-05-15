//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package promptiter

import "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"

func slimRunResult(result *engine.RunResult, slimming engine.RunResultSlimming) *engine.RunResult {
	if result == nil || runResultSlimmingIsZero(slimming) {
		return result
	}
	slimmed := *result
	if slimming.OmitStructure {
		slimmed.Structure = nil
	}
	if slimming.OmitProfiles {
		slimmed.AcceptedProfile = nil
	}
	slimmed.BaselineValidation = slimEvaluationResult(result.BaselineValidation, slimming)
	slimmed.Rounds = slimRounds(result.Rounds, slimming)
	return &slimmed
}

func runResultSlimmingIsZero(slimming engine.RunResultSlimming) bool {
	return !slimming.OmitStructure &&
		!slimming.OmitEvaluationCases &&
		!slimming.OmitBackward &&
		!slimming.OmitAggregation &&
		!slimming.OmitPatches &&
		!slimming.OmitProfiles &&
		!slimming.OmitLosses
}

func slimRounds(rounds []engine.RoundResult, slimming engine.RunResultSlimming) []engine.RoundResult {
	if len(rounds) == 0 {
		return rounds
	}
	slimmed := make([]engine.RoundResult, len(rounds))
	for i, round := range rounds {
		next := round
		if slimming.OmitProfiles {
			next.InputProfile = nil
			next.OutputProfile = nil
		}
		if slimming.OmitLosses {
			next.Losses = nil
		}
		if slimming.OmitBackward {
			next.Backward = nil
		}
		if slimming.OmitAggregation {
			next.Aggregation = nil
		}
		if slimming.OmitPatches {
			next.Patches = nil
		}
		next.Train = slimEvaluationResult(round.Train, slimming)
		next.Validation = slimEvaluationResult(round.Validation, slimming)
		slimmed[i] = next
	}
	return slimmed
}

func slimEvaluationResult(result *engine.EvaluationResult, slimming engine.RunResultSlimming) *engine.EvaluationResult {
	if result == nil || !slimming.OmitEvaluationCases {
		return result
	}
	slimmed := *result
	if len(result.EvalSets) == 0 {
		return &slimmed
	}
	slimmed.EvalSets = make([]engine.EvalSetResult, len(result.EvalSets))
	for i, evalSet := range result.EvalSets {
		next := evalSet
		next.Cases = nil
		slimmed.EvalSets[i] = next
	}
	return &slimmed
}
