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
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

func printProgress(runID string, run *engine.RunResult) {
	fmt.Printf("[%s] Run %s progress: %s\n", time.Now().Format("15:04:05"), runID, describeRunProgress(run))
}

func reportBaseline(runID string, run *engine.RunResult, reported bool) bool {
	if reported || run.BaselineValidation == nil {
		return reported
	}
	fmt.Printf("Run %s baseline validation score: %.2f\n", runID, run.BaselineValidation.OverallScore)
	return true
}

func reportRoundMilestones(
	runID string,
	run *engine.RunResult,
	reportedTrainRounds map[int]struct{},
	reportedValidationRounds map[int]struct{},
	reportedCompletedRounds map[int]struct{},
) {
	for i := range run.Rounds {
		round := &run.Rounds[i]
		if round.Train != nil {
			if _, ok := reportedTrainRounds[round.Round]; !ok {
				fmt.Printf("Run %s round %d train score: %.2f\n", runID, round.Round, round.Train.OverallScore)
				reportedTrainRounds[round.Round] = struct{}{}
			}
		}
		if round.Validation != nil {
			if _, ok := reportedValidationRounds[round.Round]; !ok {
				fmt.Printf("Run %s round %d validation score: %.2f\n", runID, round.Round, round.Validation.OverallScore)
				reportedValidationRounds[round.Round] = struct{}{}
			}
		}
		if round.Acceptance != nil && round.Stop != nil {
			if _, ok := reportedCompletedRounds[round.Round]; !ok {
				fmt.Printf("Run %s round %d completed: accepted=%t, delta=%.2f, stop=%t (%s)\n", runID, round.Round, round.Acceptance.Accepted, round.Acceptance.ScoreDelta, round.Stop.ShouldStop, round.Stop.Reason)
				reportedCompletedRounds[round.Round] = struct{}{}
			}
		}
	}
}

func describeRunProgress(run *engine.RunResult) string {
	if run == nil {
		return ""
	}
	switch run.Status {
	case engine.RunStatusQueued:
		return "queued"
	case engine.RunStatusRunning:
	default:
		return string(run.Status)
	}
	if run.BaselineValidation == nil {
		return "baseline validation"
	}
	if run.CurrentRound == 0 {
		return "waiting to start round 1"
	}
	round := currentRoundResult(run, run.CurrentRound)
	if round == nil {
		return fmt.Sprintf("round %d started", run.CurrentRound)
	}
	if round.Train == nil {
		return fmt.Sprintf("round %d train evaluation", round.Round)
	}
	if round.Losses == nil {
		return fmt.Sprintf("round %d terminal loss extraction", round.Round)
	}
	if round.Backward == nil {
		return fmt.Sprintf("round %d backward pass", round.Round)
	}
	if round.Aggregation == nil {
		return fmt.Sprintf("round %d gradient aggregation", round.Round)
	}
	if round.Patches == nil {
		return fmt.Sprintf("round %d optimizer", round.Round)
	}
	if round.OutputProfile == nil {
		return fmt.Sprintf("round %d applying patch set", round.Round)
	}
	if round.Validation == nil {
		return fmt.Sprintf("round %d validation evaluation", round.Round)
	}
	if round.Acceptance == nil || round.Stop == nil {
		return fmt.Sprintf("round %d acceptance and stop checks", round.Round)
	}
	if round.Acceptance.Accepted {
		return fmt.Sprintf("round %d completed and accepted", round.Round)
	}
	return fmt.Sprintf("round %d completed and rejected", round.Round)
}

func currentRoundResult(run *engine.RunResult, roundNumber int) *engine.RoundResult {
	for i := range run.Rounds {
		if run.Rounds[i].Round != roundNumber {
			continue
		}
		return &run.Rounds[i]
	}
	return nil
}

func printSummary(
	result *engine.RunResult,
	dataDir string,
	outputDir string,
	targetSurfaceIDs []string,
) error {
	if result == nil || len(result.Rounds) == 0 {
		return errors.New("run result is incomplete")
	}
	targets := targetSurfaceSet(targetSurfaceIDs)
	initialScore := initialValidationScore(result)
	finalScore := finalAcceptedValidationScore(result)
	fmt.Println("PromptIter remote sports recap example completed")
	fmt.Printf("Data directory: %s\n", dataDir)
	fmt.Printf("Result directory: %s\n", outputDir)
	fmt.Printf("Remote target app: %s\n", candidateAppName)
	fmt.Printf("Target surface IDs: %v\n", targetSurfaceIDs)
	printAcceptedInstructions(result, targets)
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
		fmt.Printf("Round %d -> train %.2f, validation %.2f, accepted %t, delta %.2f, stop=%t (%s)\n", round.Round, trainScore, validationScore, accepted, scoreDelta, shouldStop, stopReason)
		if round.Patches == nil {
			continue
		}
		for _, patch := range round.Patches.Patches {
			if _, ok := targets[patch.SurfaceID]; !ok || patch.Value.Text == nil {
				continue
			}
			fmt.Printf("  Instruction patch [%s]: %q\n", patch.SurfaceID, *patch.Value.Text)
			fmt.Printf("  Patch reason: %s\n", patch.Reason)
		}
	}
	return nil
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

func targetSurfaceSet(surfaceIDs []string) map[string]struct{} {
	set := make(map[string]struct{}, len(surfaceIDs))
	for _, surfaceID := range surfaceIDs {
		set[surfaceID] = struct{}{}
	}
	return set
}

func printAcceptedInstructions(result *engine.RunResult, targets map[string]struct{}) {
	fmt.Println("Accepted instruction overrides:")
	if result.AcceptedProfile == nil {
		fmt.Println("  <none>")
		return
	}
	printed := false
	for _, override := range result.AcceptedProfile.Overrides {
		if _, ok := targets[override.SurfaceID]; !ok || override.Value.Text == nil {
			continue
		}
		fmt.Printf("  %s: %q\n", override.SurfaceID, *override.Value.Text)
		printed = true
	}
	if !printed {
		fmt.Println("  <none>")
	}
}
