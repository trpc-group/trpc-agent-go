//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regloop

import "fmt"

// ReleaseGate is the harness-level publish policy layered on top of the engine's
// own MinScoreGain acceptance. It decides whether an engine-accepted candidate is
// safe to write back to the source prompt.
type ReleaseGate struct {
	// MinTotalGain is the minimum validation overall-score gain required to release.
	MinTotalGain float64
	// AllowNewHardFail permits releasing even when the candidate newly fails a case.
	AllowNewHardFail bool
	// ProtectedCaseIDs must never regress (no NewlyFailed and no ScoreDown).
	ProtectedCaseIDs []string
	// MaxRounds caps the optimization round budget; 0 disables the check.
	MaxRounds int
	// MaxModelCalls caps the total model-call budget; 0 disables the check.
	MaxModelCalls int
}

// GateInput carries everything the gate needs to decide on one run.
type GateInput struct {
	// ProfileAccepted is whether the engine accepted a candidate profile.
	ProfileAccepted bool
	// TotalGain is the validation overall-score gain vs baseline.
	TotalGain float64
	// Rounds is the number of optimization rounds executed.
	Rounds int
	// ModelCalls is the total model-call count; only trusted when ModelCallsKnown.
	ModelCalls int
	// ModelCallsKnown is false when the caller did not instrument model calls; a
	// call budget then cannot be verified and the gate fails closed.
	ModelCallsKnown bool
	// Delta is the baseline-vs-candidate per-case delta.
	Delta DeltaReport
}

// Evaluate applies the gate to one run. A candidate is releasable only when the
// engine accepted a profile; without an accepted profile there is nothing to
// release, regardless of the score gain (which is otherwise zero-gain when the
// candidate falls back to the baseline).
func (g ReleaseGate) Evaluate(in GateInput) GateResult {
	if !in.ProfileAccepted {
		return GateResult{Released: false, Reasons: []string{"no candidate profile was accepted by the engine"}}
	}
	released := true
	reasons := make([]string, 0, 5)

	if in.TotalGain+scoreEpsilon >= g.MinTotalGain {
		reasons = append(reasons, fmt.Sprintf("total gain %.3f >= threshold %.3f", in.TotalGain, g.MinTotalGain))
	} else {
		released = false
		reasons = append(reasons, fmt.Sprintf("total gain %.3f < threshold %.3f", in.TotalGain, g.MinTotalGain))
	}

	// Count distinct cases with a newly-failed metric, so the reason text ("cases")
	// matches what is actually measured (DeltaSummary.NewlyFailed is per-metric).
	newlyFailedCases := newlyFailedCaseCount(in.Delta)
	if newlyFailedCases == 0 {
		reasons = append(reasons, "no newly failed cases")
	} else if g.AllowNewHardFail {
		reasons = append(reasons, fmt.Sprintf("%d newly failed cases allowed by policy", newlyFailedCases))
	} else {
		released = false
		reasons = append(reasons, fmt.Sprintf("%d newly failed cases", newlyFailedCases))
	}

	if regressed := protectedRegressions(g.ProtectedCaseIDs, in.Delta); len(regressed) > 0 {
		released = false
		reasons = append(reasons, fmt.Sprintf("protected cases regressed: %v", regressed))
	} else if len(g.ProtectedCaseIDs) > 0 {
		reasons = append(reasons, "protected cases intact")
	}

	if g.MaxRounds > 0 {
		if in.Rounds <= g.MaxRounds {
			reasons = append(reasons, fmt.Sprintf("rounds %d within budget %d", in.Rounds, g.MaxRounds))
		} else {
			released = false
			reasons = append(reasons, fmt.Sprintf("rounds %d exceed budget %d", in.Rounds, g.MaxRounds))
		}
	}

	if g.MaxModelCalls > 0 {
		switch {
		case !in.ModelCallsKnown:
			// Fail closed: a call budget cannot be verified without a count.
			released = false
			reasons = append(reasons, "model call count unavailable; cannot verify call budget")
		case in.ModelCalls <= g.MaxModelCalls:
			reasons = append(reasons, fmt.Sprintf("model calls %d within budget %d", in.ModelCalls, g.MaxModelCalls))
		default:
			released = false
			reasons = append(reasons, fmt.Sprintf("model calls %d exceed budget %d", in.ModelCalls, g.MaxModelCalls))
		}
	}

	return GateResult{Released: released, Reasons: reasons}
}

// newlyFailedCaseCount returns the number of distinct eval cases that have at
// least one newly-failed metric.
func newlyFailedCaseCount(delta DeltaReport) int {
	seen := map[string]struct{}{}
	for _, d := range delta.CaseDeltas {
		if d.Kind == DeltaNewlyFailed {
			seen[d.EvalCaseID] = struct{}{}
		}
	}
	return len(seen)
}

// protectedRegressions returns the protected case IDs that regressed.
func protectedRegressions(protectedIDs []string, delta DeltaReport) []string {
	if len(protectedIDs) == 0 {
		return nil
	}
	protectedSet := make(map[string]struct{}, len(protectedIDs))
	for _, id := range protectedIDs {
		protectedSet[id] = struct{}{}
	}
	seen := map[string]struct{}{}
	regressed := make([]string, 0)
	for _, d := range delta.CaseDeltas {
		if _, ok := protectedSet[d.EvalCaseID]; !ok {
			continue
		}
		if d.Kind != DeltaNewlyFailed && d.Kind != DeltaScoreDown {
			continue
		}
		if _, ok := seen[d.EvalCaseID]; ok {
			continue
		}
		seen[d.EvalCaseID] = struct{}{}
		regressed = append(regressed, d.EvalCaseID)
	}
	return regressed
}
