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
	"sort"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

func printSummary(
	result *engine.RunResult,
	dataDir string,
	outputDir string,
	targetSurfaceID string,
) error {
	if result == nil || result.Structure == nil || len(result.Rounds) == 0 {
		return errors.New("run result is incomplete")
	}
	initialTools := initialToolRefs(result, targetSurfaceID)
	acceptedTools := acceptedToolRefs(result, targetSurfaceID)
	initialScore := initialValidationScore(result)
	finalScore := finalAcceptedValidationScore(result)
	fmt.Println("PromptIter tool description example completed")
	fmt.Printf("Data directory: %s\n", dataDir)
	fmt.Printf("Result directory: %s\n", outputDir)
	fmt.Printf("Structure ID: %s\n", result.Structure.StructureID)
	fmt.Printf("Target node: %s\n", candidateAgentName)
	fmt.Printf("Target surface ID: %s\n", targetSurfaceID)
	printToolBlock("Initial tool declarations", initialTools)
	printToolBlock("Accepted tool declarations", acceptedTools)
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
			if patch.SurfaceID != targetSurfaceID {
				continue
			}
			for _, toolRef := range patch.Value.Tools {
				fmt.Printf("  Tool patch [%s]: %q\n", toolRef.ID, toolRef.Description)
			}
			fmt.Printf("  Patch reason: %s\n", patch.Reason)
		}
	}
	return nil
}

func printToolBlock(title string, refs []astructure.ToolRef) {
	fmt.Printf("%s:\n", title)
	for _, ref := range sortedToolRefs(refs) {
		fmt.Printf("  %s: %q\n", ref.ID, ref.Description)
	}
}

func sortedToolRefs(refs []astructure.ToolRef) []astructure.ToolRef {
	out := append([]astructure.ToolRef(nil), refs...)
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

func initialValidationScore(result *engine.RunResult) float64 {
	if result.BaselineValidation != nil {
		return result.BaselineValidation.OverallScore
	}
	candidateScore := evaluationResultScore(result.Rounds[0].Validation)
	if result.Rounds[0].Acceptance == nil {
		return candidateScore
	}
	return candidateScore - result.Rounds[0].Acceptance.ScoreDelta
}

func finalAcceptedValidationScore(result *engine.RunResult) float64 {
	currentScore := initialValidationScore(result)
	for _, round := range result.Rounds {
		if round.Acceptance == nil || !round.Acceptance.Accepted {
			continue
		}
		currentScore = evaluationResultScore(round.Validation)
	}
	return currentScore
}

func evaluationResultScore(result *engine.EvaluationResult) float64 {
	if result == nil {
		return 0
	}
	return result.OverallScore
}

func initialToolRefs(
	result *engine.RunResult,
	targetSurfaceID string,
) []astructure.ToolRef {
	if result == nil || result.Structure == nil {
		return nil
	}
	for _, surface := range result.Structure.Surfaces {
		if surface.SurfaceID == targetSurfaceID {
			return surface.Value.Tools
		}
		if surface.Type != astructure.SurfaceTypeTool {
			continue
		}
		for _, ref := range surface.Value.Tools {
			if astructure.SurfaceID(
				surface.NodeID,
				astructure.SurfaceTypeTool,
				ref.ID,
			) == targetSurfaceID {
				return []astructure.ToolRef{ref}
			}
		}
	}
	return nil
}

func acceptedToolRefs(
	result *engine.RunResult,
	targetSurfaceID string,
) []astructure.ToolRef {
	accepted := initialToolRefs(result, targetSurfaceID)
	if result.AcceptedProfile == nil {
		return accepted
	}
	for _, override := range result.AcceptedProfile.Overrides {
		if override.SurfaceID != targetSurfaceID {
			continue
		}
		return override.Value.Tools
	}
	return accepted
}
