//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import "fmt"

const (
	deltaNewPass    = "new_pass"
	deltaNewFailure = "new_failure"
	deltaImproved   = "improved"
	deltaRegressed  = "regressed"
	deltaUnchanged  = "unchanged"
)

func computeDelta(baseline, candidate evaluationSummary) []caseDelta {
	baselineByID := make(map[string]caseResult, len(baseline.Cases))
	for _, result := range baseline.Cases {
		baselineByID[result.ID] = result
	}
	deltas := make([]caseDelta, 0, len(candidate.Cases))
	for _, result := range candidate.Cases {
		base := baselineByID[result.ID]
		delta := caseDelta{
			CaseID: result.ID, BaselineScore: base.Score, CandidateScore: result.Score,
			Delta: result.Score - base.Score, Hard: result.Hard,
		}
		switch {
		case !base.Passed && result.Passed:
			delta.Status = deltaNewPass
		case base.Passed && !result.Passed:
			delta.Status = deltaNewFailure
		case delta.Delta > 0:
			delta.Status = deltaImproved
		case delta.Delta < 0:
			delta.Status = deltaRegressed
		default:
			delta.Status = deltaUnchanged
		}
		deltas = append(deltas, delta)
	}
	return deltas
}

func decideGate(cfg gateConfig, baseline, candidate evaluationSummary, deltas []caseDelta, cost costSummary) gateDecision {
	decision := gateDecision{Accepted: true}
	gain := candidate.Score - baseline.Score
	if gain < cfg.MinValidationGain {
		decision.Accepted = false
		decision.Reasons = append(decision.Reasons, fmt.Sprintf(
			"validation gain %.3f is below required %.3f", gain, cfg.MinValidationGain,
		))
	}
	for _, delta := range deltas {
		if cfg.ForbidNewFailures && delta.Status == deltaNewFailure {
			decision.Accepted = false
			decision.Reasons = append(decision.Reasons, fmt.Sprintf("case %s became a new failure", delta.CaseID))
		}
		if cfg.NoHardRegression && delta.Hard && delta.Delta < 0 {
			decision.Accepted = false
			decision.Reasons = append(decision.Reasons, fmt.Sprintf("hard case %s regressed", delta.CaseID))
		}
	}
	if cost.Calls > cfg.MaxCalls {
		decision.Accepted = false
		decision.Reasons = append(decision.Reasons, fmt.Sprintf("call count %d exceeds budget %d", cost.Calls, cfg.MaxCalls))
	}
	if cost.EstimatedTokens > cfg.MaxEstimatedTokens {
		decision.Accepted = false
		decision.Reasons = append(decision.Reasons, fmt.Sprintf(
			"estimated tokens %d exceed budget %d", cost.EstimatedTokens, cfg.MaxEstimatedTokens,
		))
	}
	if decision.Accepted {
		decision.Reasons = []string{fmt.Sprintf("validation gain %.3f passed all regression and budget gates", gain)}
	}
	return decision
}
