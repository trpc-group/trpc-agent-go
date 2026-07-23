//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regressionloop

import "sort"

// ComputeDeltas compares baseline and candidate validation case results.
func ComputeDeltas(baseline, candidate EvaluationSummary, criticalCaseIDs []string) ([]CaseDelta, DeltaSummary) {
	baselineByID := caseIndex(baseline.Cases)
	candidateByID := caseIndex(candidate.Cases)
	ids := make(map[string]struct{}, len(baselineByID)+len(candidateByID))
	for id := range baselineByID {
		ids[id] = struct{}{}
	}
	for id := range candidateByID {
		ids[id] = struct{}{}
	}
	critical := criticalIndex(criticalCaseIDs)
	ordered := make([]string, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	deltas := make([]CaseDelta, 0, len(ordered))
	var summary DeltaSummary
	for _, id := range ordered {
		base, hasBase := baselineByID[id]
		cand, hasCand := candidateByID[id]
		delta := CaseDelta{EvalID: id, Transition: TransitionMissing}
		if hasBase {
			delta.EvalSetID = base.EvalSetID
			delta.BaselineScore = base.Score
			delta.BaselinePassed = base.Passed
		}
		if hasCand {
			if delta.EvalSetID == "" {
				delta.EvalSetID = cand.EvalSetID
			}
			delta.CandidateScore = cand.Score
			delta.CandidatePassed = cand.Passed
			delta.NewHardFail = cand.HardFail && (!hasBase || !base.HardFail)
		}
		if hasBase && hasCand {
			delta.ScoreDelta = cand.Score - base.Score
			delta.Transition = classifyTransition(base, cand)
			delta.CriticalRegression = isCritical(base, cand, critical) && (delta.ScoreDelta < 0 || (base.Passed && !cand.Passed))
			delta.AttributionChange = attributionChange(base.Attributions, cand.Attributions)
		}
		addDeltaSummary(&summary, delta)
		deltas = append(deltas, delta)
	}
	return deltas, summary
}

func caseIndex(cases []CaseResult) map[string]CaseResult {
	index := make(map[string]CaseResult, len(cases))
	for _, c := range cases {
		index[c.EvalID] = c
	}
	return index
}

func criticalIndex(ids []string) map[string]struct{} {
	index := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		index[id] = struct{}{}
	}
	return index
}

func classifyTransition(base, cand CaseResult) Transition {
	switch {
	case !base.Passed && cand.Passed:
		return TransitionNewlyPassed
	case base.Passed && !cand.Passed:
		return TransitionNewlyFailed
	case cand.Score > base.Score:
		return TransitionImproved
	case cand.Score < base.Score:
		return TransitionRegressed
	default:
		return TransitionUnchanged
	}
}

func isCritical(base, cand CaseResult, configured map[string]struct{}) bool {
	if base.Critical || cand.Critical {
		return true
	}
	_, ok := configured[base.EvalID]
	return ok
}

func addDeltaSummary(summary *DeltaSummary, delta CaseDelta) {
	switch delta.Transition {
	case TransitionNewlyPassed:
		summary.NewlyPassed++
	case TransitionNewlyFailed:
		summary.NewlyFailed++
	case TransitionImproved:
		summary.Improved++
	case TransitionRegressed:
		summary.Regressed++
	case TransitionUnchanged:
		summary.Unchanged++
	}
	if delta.NewHardFail {
		summary.NewHardFails++
	}
	if delta.CriticalRegression {
		summary.CriticalRegressions++
	}
}

func attributionChange(base, cand []Attribution) string {
	if len(base) == 0 && len(cand) == 0 {
		return ""
	}
	if len(base) == 0 {
		return "candidate attribution added"
	}
	if len(cand) == 0 {
		return "candidate attribution cleared"
	}
	if base[0].Category != cand[0].Category {
		return string(base[0].Category) + " -> " + string(cand[0].Category)
	}
	return ""
}
