//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regloop

import (
	"strings"
	"testing"
)

func deltaWith(summary DeltaSummary, cases ...CaseDelta) DeltaReport {
	return DeltaReport{CaseDeltas: cases, Summary: summary}
}

// newlyFail builds a newly-failed metric delta for one case/metric.
func newlyFail(caseID, metric string) CaseDelta {
	return CaseDelta{EvalCaseID: caseID, MetricName: metric, Kind: DeltaNewlyFailed}
}

// gi builds a GateInput with model calls known (the common case).
func gi(profileAccepted bool, totalGain float64, rounds int, delta DeltaReport) GateInput {
	return GateInput{ProfileAccepted: profileAccepted, TotalGain: totalGain, Rounds: rounds, ModelCallsKnown: true, Delta: delta}
}

func TestGateReleasesOnGain(t *testing.T) {
	gate := ReleaseGate{MinTotalGain: 0.5, MaxRounds: 3}
	got := gate.Evaluate(gi(true, 0.7, 2, deltaWith(DeltaSummary{NewlyPassed: 2})))
	if !got.Released {
		t.Fatalf("expected released, reasons=%v", got.Reasons)
	}
}

func TestGateRejectsLowGain(t *testing.T) {
	gate := ReleaseGate{MinTotalGain: 0.5}
	got := gate.Evaluate(gi(true, 0.1, 1, deltaWith(DeltaSummary{})))
	if got.Released {
		t.Fatalf("expected rejected for low gain")
	}
}

func TestGateRejectsNewHardFail(t *testing.T) {
	gate := ReleaseGate{MinTotalGain: 0.0}
	got := gate.Evaluate(gi(true, 0.2, 1, deltaWith(DeltaSummary{NewlyFailed: 1}, newlyFail("c1", "m"))))
	if got.Released {
		t.Fatalf("expected rejected for new hard fail")
	}
}

func TestGateAllowsNewHardFailWhenPermitted(t *testing.T) {
	gate := ReleaseGate{MinTotalGain: 0.0, AllowNewHardFail: true}
	got := gate.Evaluate(gi(true, 0.2, 1, deltaWith(DeltaSummary{NewlyFailed: 1}, newlyFail("c1", "m"))))
	if !got.Released {
		t.Fatalf("expected released when new hard fail allowed, reasons=%v", got.Reasons)
	}
}

// TestGateNewlyFailedCountsDistinctCases: two failed metrics on the same case
// count as one newly-failed case (the reason text says "cases").
func TestGateNewlyFailedCountsDistinctCases(t *testing.T) {
	gate := ReleaseGate{MinTotalGain: 0.0}
	delta := deltaWith(DeltaSummary{NewlyFailed: 2}, newlyFail("c1", "m1"), newlyFail("c1", "m2"))
	got := gate.Evaluate(gi(true, 0.2, 1, delta))
	if got.Released {
		t.Fatalf("expected rejected")
	}
	if !strings.Contains(strings.Join(got.Reasons, " "), "1 newly failed cases") {
		t.Fatalf("reason must count 1 case, got %v", got.Reasons)
	}
}

func TestGateRejectsProtectedRegression(t *testing.T) {
	gate := ReleaseGate{MinTotalGain: 0.0, ProtectedCaseIDs: []string{"vip"}}
	delta := deltaWith(
		DeltaSummary{ScoreDown: 1},
		CaseDelta{EvalCaseID: "vip", MetricName: "m", Kind: DeltaScoreDown},
	)
	got := gate.Evaluate(gi(true, 0.3, 1, delta))
	if got.Released {
		t.Fatalf("expected rejected for protected case regression")
	}
}

func TestGateRejectsRoundBudgetOverrun(t *testing.T) {
	gate := ReleaseGate{MinTotalGain: 0.0, MaxRounds: 2}
	got := gate.Evaluate(gi(true, 0.9, 5, deltaWith(DeltaSummary{})))
	if got.Released {
		t.Fatalf("expected rejected for exceeding round budget")
	}
}

// TestGateModelCallsBudget covers under, exactly-at, and over the call budget.
func TestGateModelCallsBudget(t *testing.T) {
	gate := ReleaseGate{MinTotalGain: 0.0, MaxModelCalls: 10}
	base := GateInput{ProfileAccepted: true, TotalGain: 0.5, Rounds: 1, ModelCallsKnown: true, Delta: deltaWith(DeltaSummary{})}
	base.ModelCalls = 5
	if got := gate.Evaluate(base); !got.Released {
		t.Fatalf("5 calls under budget must release, reasons=%v", got.Reasons)
	}
	base.ModelCalls = 10
	if got := gate.Evaluate(base); !got.Released {
		t.Fatalf("10 calls at budget must release, reasons=%v", got.Reasons)
	}
	base.ModelCalls = 11
	if got := gate.Evaluate(base); got.Released {
		t.Fatalf("11 calls over budget must reject")
	}
}

// TestGateFailsClosedWhenModelCallsUnknown: with a budget configured but the
// count not instrumented, the gate must reject rather than treat 0 as real.
func TestGateFailsClosedWhenModelCallsUnknown(t *testing.T) {
	gate := ReleaseGate{MinTotalGain: 0.0, MaxModelCalls: 10}
	got := gate.Evaluate(GateInput{ProfileAccepted: true, TotalGain: 0.5, Rounds: 1, ModelCallsKnown: false, Delta: deltaWith(DeltaSummary{})})
	if got.Released {
		t.Fatalf("must fail closed when call count is unknown, reasons=%v", got.Reasons)
	}
	if !strings.Contains(strings.Join(got.Reasons, " "), "unavailable") {
		t.Fatalf("reason must flag unavailable count, got %v", got.Reasons)
	}
}

// TestGateBudgetDisabledIgnoresUnknownCount: with MaxModelCalls 0, an unknown
// count does not block release.
func TestGateBudgetDisabledIgnoresUnknownCount(t *testing.T) {
	gate := ReleaseGate{MinTotalGain: 0.0}
	got := gate.Evaluate(GateInput{ProfileAccepted: true, TotalGain: 0.5, Rounds: 1, ModelCallsKnown: false, Delta: deltaWith(DeltaSummary{})})
	if !got.Released {
		t.Fatalf("disabled budget must not block on unknown count, reasons=%v", got.Reasons)
	}
}

func TestGateRejectsWithoutAcceptedProfile(t *testing.T) {
	// With MinTotalGain 0 the gain check alone would pass, but no profile was
	// accepted, so there is nothing to release.
	gate := ReleaseGate{MinTotalGain: 0.0}
	got := gate.Evaluate(gi(false, 0.0, 1, deltaWith(DeltaSummary{})))
	if got.Released {
		t.Fatalf("expected rejected when no profile was accepted, reasons=%v", got.Reasons)
	}
}
