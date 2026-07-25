//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

// printRunSummary prints a per-round view of the PromptIter engine run. Richer, machine-readable
// audit reporting (optimization_report.json/.md) is added in later phases; this stays as a quick
// human-facing console summary of the raw optimization run.
func printRunSummary(result *engine.RunResult, initialInstruction, surfaceID string) error {
	if result == nil || len(result.Rounds) == 0 {
		return errors.New("run result is incomplete")
	}
	accepted := acceptedInstructionText(result, initialInstruction, surfaceID)
	fmt.Println("✅ PromptIter regression-loop optimization run completed")
	fmt.Printf("Target surface ID: %s\n", surfaceID)
	fmt.Printf("Initial instruction: %q\n", initialInstruction)
	fmt.Printf("Accepted instruction: %q\n", accepted)
	fmt.Printf("Initial validation score: %.3f\n", initialValidationScore(result))
	fmt.Printf("Final accepted validation score: %.3f\n", finalAcceptedValidationScore(result))
	fmt.Printf("Rounds executed: %d\n", len(result.Rounds))
	for _, round := range result.Rounds {
		fmt.Printf(
			"Round %d -> train %.3f, validation %.3f, accepted %t, delta %.3f, stop=%t (%s)\n",
			round.Round,
			evaluationResultScore(round.Train),
			evaluationResultScore(round.Validation),
			round.Acceptance.Accepted,
			round.Acceptance.ScoreDelta,
			round.Stop.ShouldStop,
			round.Stop.Reason,
		)
	}
	return nil
}

func initialValidationScore(result *engine.RunResult) float64 {
	if result.BaselineValidation != nil {
		return result.BaselineValidation.OverallScore
	}
	return evaluationResultScore(result.Rounds[0].Validation) - result.Rounds[0].Acceptance.ScoreDelta
}

func finalAcceptedValidationScore(result *engine.RunResult) float64 {
	current := initialValidationScore(result)
	for _, round := range result.Rounds {
		if !round.Acceptance.Accepted {
			continue
		}
		current = evaluationResultScore(round.Validation)
	}
	return current
}

func evaluationResultScore(result *engine.EvaluationResult) float64 {
	if result == nil {
		return 0
	}
	return result.OverallScore
}

func acceptedInstructionText(result *engine.RunResult, initialInstruction, surfaceID string) string {
	accepted := initialInstruction
	if result.AcceptedProfile == nil {
		return accepted
	}
	for _, override := range result.AcceptedProfile.Overrides {
		if override.SurfaceID != surfaceID || override.Value.Text == nil {
			continue
		}
		accepted = *override.Value.Text
		break
	}
	return accepted
}
