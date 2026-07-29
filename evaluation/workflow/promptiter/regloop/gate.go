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
// own MinScoreGain acceptance. It is applied through Analyze, which is the only
// release path: Analyze derives the gate's evidence from the RunResult and
// enforces the release preconditions (run status, per-case data, accepted
// profile artifact) that the gate rules alone cannot see.
type ReleaseGate struct {
	// MinTotalGain is the minimum validation overall-score gain required to release.
	MinTotalGain float64
	// AllowNewHardFail permits releasing even when the candidate newly fails a case.
	AllowNewHardFail bool
	// ProtectedCases must never regress (no NewlyFailed and no ScoreDown).
	ProtectedCases []ProtectedCase
	// MaxRounds caps the optimization round budget; 0 disables the check.
	MaxRounds int
	// MaxModelCalls caps the total model-call budget; 0 disables the check.
	MaxModelCalls int
}

// ProtectedCase pins one case by its full engine identity (EvalSetID,
// EvalCaseID), since one run may evaluate multiple sets that reuse a case ID.
// An empty EvalSetID protects the case ID in every eval set.
type ProtectedCase struct {
	EvalSetID  string `json:"evalSetId,omitempty"`
	EvalCaseID string `json:"evalCaseId"`
}

// gateInput carries everything the gate needs to decide on one run. It is
// internal: only Analyze may build it, so callers cannot hand the gate
// unvalidated evidence.
type gateInput struct {
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

// evaluate applies the gate to one run. A candidate is releasable only when the
// engine accepted a profile; without an accepted profile there is nothing to
// release, regardless of the score gain (which is otherwise zero-gain when the
// candidate falls back to the baseline).
func (g ReleaseGate) evaluate(in gateInput) GateResult {
	if !in.ProfileAccepted {
		return GateResult{Released: false, Reasons: []string{"no candidate profile was accepted by the engine"}}
	}
	released := true
	reasons := make([]string, 0, 5)

	// Fail closed without per-case evidence: an aggregate gain alone cannot prove
	// the absence of regressions, so an empty delta is never releasable.
	if len(in.Delta.CaseDeltas) == 0 {
		released = false
		reasons = append(reasons, "no per-case delta evidence; cannot verify regressions")
	}

	if in.TotalGain+scoreEpsilon >= g.MinTotalGain {
		reasons = append(reasons, fmt.Sprintf("total gain %.3f >= threshold %.3f", in.TotalGain, g.MinTotalGain))
	} else {
		released = false
		reasons = append(reasons, fmt.Sprintf("total gain %.3f < threshold %.3f", in.TotalGain, g.MinTotalGain))
	}

	// Fail closed when the metric evidence is not comparable: a metric present in
	// only one phase, or one whose status is non-terminal on either side, means
	// part of the validation never ran — a candidate that stopped reporting a
	// failing metric would otherwise inflate its aggregate gain.
	miss, unexpected, incomparable := in.Delta.Summary.MissingMetrics, in.Delta.Summary.UnexpectedMetrics, in.Delta.Summary.IncomparableMetrics
	if miss > 0 || unexpected > 0 || incomparable > 0 {
		released = false
		reasons = append(reasons, fmt.Sprintf(
			"metric evidence not comparable: %d baseline metric(s) missing from candidate, %d candidate-only metric(s), %d non-terminal pair(s)",
			miss, unexpected, incomparable))
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

	if regressed := protectedRegressions(g.ProtectedCases, in.Delta); len(regressed) > 0 {
		released = false
		reasons = append(reasons, fmt.Sprintf("protected cases regressed: %v", regressed))
	} else if len(g.ProtectedCases) > 0 {
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

// newlyFailedCaseCount returns the number of distinct eval cases — identified
// by (evalSetID, evalCaseID) — that have at least one newly-failed metric.
func newlyFailedCaseCount(delta DeltaReport) int {
	seen := map[string]struct{}{}
	for _, d := range delta.CaseDeltas {
		if d.Kind == DeltaNewlyFailed {
			seen[d.EvalSetID+"/"+d.EvalCaseID] = struct{}{}
		}
	}
	return len(seen)
}

// protectedRegressions returns the regressed protected cases, each rendered as
// "evalSetID/evalCaseID" so gate reasons identify which set's case moved.
func protectedRegressions(protected []ProtectedCase, delta DeltaReport) []string {
	if len(protected) == 0 {
		return nil
	}
	matches := func(d CaseDelta) bool {
		for _, p := range protected {
			if p.EvalCaseID == d.EvalCaseID && (p.EvalSetID == "" || p.EvalSetID == d.EvalSetID) {
				return true
			}
		}
		return false
	}
	seen := map[string]struct{}{}
	regressed := make([]string, 0)
	for _, d := range delta.CaseDeltas {
		if d.Kind != DeltaNewlyFailed && d.Kind != DeltaScoreDown {
			continue
		}
		if !matches(d) {
			continue
		}
		key := d.EvalSetID + "/" + d.EvalCaseID
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		regressed = append(regressed, key)
	}
	return regressed
}
