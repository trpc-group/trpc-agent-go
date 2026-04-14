//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"errors"
	"fmt"

	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

func printSummary(
	result *promptiterengine.RunResult,
	dataDir string,
	outputDir string,
	initialInstruction string,
	targetSurfaceID string,
) error {
	if result == nil || result.Structure == nil || len(result.Rounds) == 0 {
		return errors.New("run result is incomplete")
	}
	acceptedInstruction := initialInstruction
	if result.AcceptedProfile != nil {
		for _, override := range result.AcceptedProfile.Overrides {
			if override.SurfaceID != targetSurfaceID || override.Value.Text == nil {
				continue
			}
			acceptedInstruction = *override.Value.Text
			break
		}
	}
	initialScore := initialValidationScore(result)
	finalScore := finalAcceptedValidationScore(result)
	fmt.Println("✅ PromptIter asyncrun sports commentary example completed")
	fmt.Printf("Data directory: %s\n", dataDir)
	fmt.Printf("Result directory: %s\n", outputDir)
	fmt.Printf("Structure ID: %s\n", result.Structure.StructureID)
	fmt.Printf("Target node: %s\n", candidateAgentName)
	fmt.Printf("Target surface ID: %s\n", targetSurfaceID)
	fmt.Printf("Initial instruction: %q\n", initialInstruction)
	fmt.Printf("Accepted instruction: %q\n", acceptedInstruction)
	fmt.Printf("Initial validation score: %.2f\n", initialScore)
	fmt.Printf("Final accepted validation score: %.2f\n", finalScore)
	fmt.Printf("Rounds executed: %d\n", len(result.Rounds))
	for _, round := range result.Rounds {
		trainScore := evaluationResultScore(round.Train)
		validationScore := evaluationResultScore(round.Validation)
		fmt.Printf(
			"Round %d -> train %.2f, validation %.2f, accepted %t, delta %.2f, stop=%t (%s)\n",
			round.Round,
			trainScore,
			validationScore,
			round.Acceptance.Accepted,
			round.Acceptance.ScoreDelta,
			round.Stop.ShouldStop,
			round.Stop.Reason,
		)
		for _, patch := range round.Patches.Patches {
			if patch.SurfaceID != targetSurfaceID || patch.Value.Text == nil {
				continue
			}
			fmt.Printf("  Instruction patch [%s]: %q\n", candidateAgentName, *patch.Value.Text)
			fmt.Printf("  Patch reason: %s\n", patch.Reason)
		}
	}
	return nil
}

func initialValidationScore(result *promptiterengine.RunResult) float64 {
	if result.BaselineValidation != nil {
		return result.BaselineValidation.OverallScore
	}
	candidateScore := evaluationResultScore(result.Rounds[0].Validation)
	return candidateScore - result.Rounds[0].Acceptance.ScoreDelta
}

func finalAcceptedValidationScore(result *promptiterengine.RunResult) float64 {
	currentScore := initialValidationScore(result)
	for _, round := range result.Rounds {
		if !round.Acceptance.Accepted {
			continue
		}
		currentScore = evaluationResultScore(round.Validation)
	}
	return currentScore
}

func evaluationResultScore(result *promptiterengine.EvaluationResult) float64 {
	return result.OverallScore
}
