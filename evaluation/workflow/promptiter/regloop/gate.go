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

// Evaluate applies the gate to one run. A candidate is releasable only when the
// engine accepted a profile; without an accepted profile there is nothing to
// release, regardless of the score gain (which is otherwise zero-gain when the
// candidate falls back to the baseline).
func (g ReleaseGate) Evaluate(profileAccepted bool, totalGain float64, rounds, modelCalls int, delta DeltaReport) GateResult {
	if !profileAccepted {
		return GateResult{Released: false, Reasons: []string{"no candidate profile was accepted by the engine"}}
	}
	released := true
	reasons := make([]string, 0, 5)

	if totalGain+scoreEpsilon >= g.MinTotalGain {
		reasons = append(reasons, fmt.Sprintf("total gain %.3f >= threshold %.3f", totalGain, g.MinTotalGain))
	} else {
		released = false
		reasons = append(reasons, fmt.Sprintf("total gain %.3f < threshold %.3f", totalGain, g.MinTotalGain))
	}

	// Count distinct cases with a newly-failed metric, so the reason text ("cases")
	// matches what is actually measured (DeltaSummary.NewlyFailed is per-metric).
	newlyFailedCases := newlyFailedCaseCount(delta)
	if newlyFailedCases == 0 {
		reasons = append(reasons, "no newly failed cases")
	} else if g.AllowNewHardFail {
		reasons = append(reasons, fmt.Sprintf("%d newly failed cases allowed by policy", newlyFailedCases))
	} else {
		released = false
		reasons = append(reasons, fmt.Sprintf("%d newly failed cases", newlyFailedCases))
	}

	if regressed := protectedRegressions(g.ProtectedCaseIDs, delta); len(regressed) > 0 {
		released = false
		reasons = append(reasons, fmt.Sprintf("protected cases regressed: %v", regressed))
	} else if len(g.ProtectedCaseIDs) > 0 {
		reasons = append(reasons, "protected cases intact")
	}

	if g.MaxRounds > 0 {
		if rounds <= g.MaxRounds {
			reasons = append(reasons, fmt.Sprintf("rounds %d within budget %d", rounds, g.MaxRounds))
		} else {
			released = false
			reasons = append(reasons, fmt.Sprintf("rounds %d exceed budget %d", rounds, g.MaxRounds))
		}
	}

	if g.MaxModelCalls > 0 {
		if modelCalls <= g.MaxModelCalls {
			reasons = append(reasons, fmt.Sprintf("model calls %d within budget %d", modelCalls, g.MaxModelCalls))
		} else {
			released = false
			reasons = append(reasons, fmt.Sprintf("model calls %d exceed budget %d", modelCalls, g.MaxModelCalls))
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
