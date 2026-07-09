//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regloop

import "testing"

func deltaWith(summary DeltaSummary, cases ...CaseDelta) DeltaReport {
	return DeltaReport{CaseDeltas: cases, Summary: summary}
}

func TestGateReleasesOnGain(t *testing.T) {
	gate := ReleaseGate{MinTotalGain: 0.5, MaxRounds: 3}
	got := gate.Evaluate(true, 0.7, 2, deltaWith(DeltaSummary{NewlyPassed: 2}))
	if !got.Released {
		t.Fatalf("expected released, reasons=%v", got.Reasons)
	}
}

func TestGateRejectsLowGain(t *testing.T) {
	gate := ReleaseGate{MinTotalGain: 0.5}
	got := gate.Evaluate(true, 0.1, 1, deltaWith(DeltaSummary{}))
	if got.Released {
		t.Fatalf("expected rejected for low gain")
	}
}

func TestGateRejectsNewHardFail(t *testing.T) {
	gate := ReleaseGate{MinTotalGain: 0.0}
	got := gate.Evaluate(true, 0.2, 1, deltaWith(DeltaSummary{NewlyFailed: 1}))
	if got.Released {
		t.Fatalf("expected rejected for new hard fail")
	}
}

func TestGateAllowsNewHardFailWhenPermitted(t *testing.T) {
	gate := ReleaseGate{MinTotalGain: 0.0, AllowNewHardFail: true}
	got := gate.Evaluate(true, 0.2, 1, deltaWith(DeltaSummary{NewlyFailed: 1}))
	if !got.Released {
		t.Fatalf("expected released when new hard fail allowed, reasons=%v", got.Reasons)
	}
}

func TestGateRejectsProtectedRegression(t *testing.T) {
	gate := ReleaseGate{MinTotalGain: 0.0, ProtectedCaseIDs: []string{"vip"}}
	delta := deltaWith(
		DeltaSummary{ScoreDown: 1},
		CaseDelta{EvalCaseID: "vip", MetricName: "m", Kind: DeltaScoreDown},
	)
	got := gate.Evaluate(true, 0.3, 1, delta)
	if got.Released {
		t.Fatalf("expected rejected for protected case regression")
	}
}

func TestGateRejectsBudgetOverrun(t *testing.T) {
	gate := ReleaseGate{MinTotalGain: 0.0, MaxRounds: 2}
	got := gate.Evaluate(true, 0.9, 5, deltaWith(DeltaSummary{}))
	if got.Released {
		t.Fatalf("expected rejected for exceeding round budget")
	}
}

func TestGateRejectsWithoutAcceptedProfile(t *testing.T) {
	// With MinTotalGain 0 the gain check alone would pass, but no profile was
	// accepted, so there is nothing to release.
	gate := ReleaseGate{MinTotalGain: 0.0}
	got := gate.Evaluate(false, 0.0, 1, deltaWith(DeltaSummary{}))
	if got.Released {
		t.Fatalf("expected rejected when no profile was accepted, reasons=%v", got.Reasons)
	}
}
