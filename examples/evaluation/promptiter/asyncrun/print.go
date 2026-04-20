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
	if result == nil {
		return errors.New("run result is nil")
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
	structureID := ""
	if result.Structure != nil {
		structureID = result.Structure.StructureID
	}
	fmt.Println("✅ PromptIter asyncrun sports commentary example completed")
	fmt.Printf("Data directory: %s\n", dataDir)
	fmt.Printf("Result directory: %s\n", outputDir)
	if structureID != "" {
		fmt.Printf("Structure ID: %s\n", structureID)
	}
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
		accepted := false
		scoreDelta := 0.0
		if round.Acceptance != nil {
			accepted = round.Acceptance.Accepted
			scoreDelta = round.Acceptance.ScoreDelta
		}
		shouldStop := false
		stopReason := ""
		if round.Stop != nil {
			shouldStop = round.Stop.ShouldStop
			stopReason = round.Stop.Reason
		}
		fmt.Printf(
			"Round %d -> train %.2f, validation %.2f, accepted %t, delta %.2f, stop=%t (%s)\n",
			round.Round,
			trainScore,
			validationScore,
			accepted,
			scoreDelta,
			shouldStop,
			stopReason,
		)
		if round.Patches == nil {
			continue
		}
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
	if result == nil {
		return 0
	}
	if result.BaselineValidation != nil {
		return result.BaselineValidation.OverallScore
	}
	if len(result.Rounds) == 0 || result.Rounds[0].Validation == nil || result.Rounds[0].Acceptance == nil {
		return 0
	}
	candidateScore := evaluationResultScore(result.Rounds[0].Validation)
	return candidateScore - result.Rounds[0].Acceptance.ScoreDelta
}

func finalAcceptedValidationScore(result *promptiterengine.RunResult) float64 {
	if result == nil {
		return 0
	}
	currentScore := initialValidationScore(result)
	for _, round := range result.Rounds {
		if round.Acceptance == nil || !round.Acceptance.Accepted || round.Validation == nil {
			continue
		}
		currentScore = evaluationResultScore(round.Validation)
	}
	return currentScore
}

func evaluationResultScore(result *promptiterengine.EvaluationResult) float64 {
	if result == nil {
		return 0
	}
	return result.OverallScore
}
