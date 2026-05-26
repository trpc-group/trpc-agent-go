//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"fmt"
	"time"

	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

func printProgress(runID string, run *promptiterengine.RunResult) {
	fmt.Printf("[%s] Run %s progress: %s\n", time.Now().Format("15:04:05"), runID, describeRunProgress(run))
}

func reportBaseline(runID string, run *promptiterengine.RunResult, reported bool) bool {
	if reported || run.BaselineValidation == nil {
		return reported
	}
	fmt.Printf("Run %s baseline validation score: %.2f\n", runID, run.BaselineValidation.OverallScore)
	return true
}

func reportRoundMilestones(
	runID string,
	run *promptiterengine.RunResult,
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

func describeRunProgress(run *promptiterengine.RunResult) string {
	if run == nil {
		return ""
	}
	switch run.Status {
	case promptiterengine.RunStatusQueued:
		return "queued"
	case promptiterengine.RunStatusRunning:
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

func currentRoundResult(run *promptiterengine.RunResult, roundNumber int) *promptiterengine.RoundResult {
	for i := range run.Rounds {
		if run.Rounds[i].Round != roundNumber {
			continue
		}
		return &run.Rounds[i]
	}
	return nil
}

func printRunSummary(result *promptiterengine.RunResult, candidateSurfaceIDs []string) {
	fmt.Println("PromptIter multinode sports recap example completed.")
	fmt.Printf("Status: %s\n", result.Status)
	fmt.Printf("Baseline validation score: %.2f\n", result.BaselineValidation.OverallScore)
	fmt.Printf("Final accepted validation score: %.2f\n", finalAcceptedValidationScore(result))
	fmt.Printf("Rounds executed: %d\n", len(result.Rounds))
	fmt.Println("Candidate surfaces:")
	for _, surfaceID := range candidateSurfaceIDs {
		fmt.Printf("  %s\n", surfaceID)
	}
	for _, round := range result.Rounds {
		fmt.Printf("Round %d -> train %.2f, validation %.2f, accepted %t, delta %.2f\n", round.Round, round.Train.OverallScore, round.Validation.OverallScore, round.Acceptance.Accepted, round.Acceptance.ScoreDelta)
		for _, patch := range round.Patches.Patches {
			if patch.Value.Text == nil {
				continue
			}
			fmt.Printf("  Patch %s: %q\n", patch.SurfaceID, *patch.Value.Text)
		}
	}
}

func finalAcceptedValidationScore(result *promptiterengine.RunResult) float64 {
	score := result.BaselineValidation.OverallScore
	for _, round := range result.Rounds {
		if round.Acceptance != nil && round.Acceptance.Accepted {
			score = round.Validation.OverallScore
		}
	}
	return score
}
