//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package pipeline

import "testing"

func TestDiffResultsClassifiesEveryTransition(t *testing.T) {
	baseline := makeResult("val",
		makeCase("newpass", false, 0.0, critFinalResponseText(), ""),
		makeCase("newfail", true, 1.0, critFinalResponseText(), ""),
		makeCase("scoreup", true, 0.5, critFinalResponseText(), ""),
		makeCase("scoredown", true, 0.8, critFinalResponseText(), ""),
		makeCase("unchanged", true, 1.0, critFinalResponseText(), ""),
		makeCase("dropped", true, 1.0, critFinalResponseText(), ""),
	)
	candidate := makeResult("val",
		makeCase("newpass", true, 1.0, critFinalResponseText(), ""),
		makeCase("newfail", false, 0.0, critFinalResponseText(), ""),
		makeCase("scoreup", true, 0.9, critFinalResponseText(), ""),
		makeCase("scoredown", true, 0.5, critFinalResponseText(), ""),
		makeCase("unchanged", true, 1.0, critFinalResponseText(), ""),
		// "dropped" absent from the candidate → treated as a regression.
	)
	want := map[string]DeltaClass{
		"newpass":   DeltaNewPass,
		"newfail":   DeltaNewFail,
		"scoreup":   DeltaScoreUp,
		"scoredown": DeltaScoreDown,
		"unchanged": DeltaUnchanged,
		"dropped":   DeltaNewFail,
	}
	deltas := DiffResults(baseline, candidate)
	if len(deltas) != len(want) {
		t.Fatalf("got %d deltas, want %d", len(deltas), len(want))
	}
	for _, d := range deltas {
		if d.Class != want[d.EvalCaseID] {
			t.Errorf("case %q: class = %q, want %q", d.EvalCaseID, d.Class, want[d.EvalCaseID])
		}
	}
}

func TestApplyGateAcceptsGenuineImprovement(t *testing.T) {
	baseline := makeResult("val",
		makeCase("a", false, 0.0, critFinalResponseText(), ""),
		makeCase("b", true, 1.0, critFinalResponseText(), ""),
	)
	candidate := makeResult("val",
		makeCase("a", true, 1.0, critFinalResponseText(), ""),
		makeCase("b", true, 1.0, critFinalResponseText(), ""),
	)
	d := ApplyGate(GatePolicy{MinValidationGain: 0.01}, baseline, candidate, GateObservations{})
	if !d.Accepted {
		t.Fatalf("expected accept, got reject: %v", d.Reasons)
	}
	if d.ValidationGain <= 0 {
		t.Errorf("expected positive gain, got %v", d.ValidationGain)
	}
}

func TestApplyGateRejectsOverfitEvenWhenMeanRises(t *testing.T) {
	// Mean rises 0.333 → 0.667, but the key case regresses pass→fail: this is the overfit the gate
	// must veto, matching issue #2003's hard requirement.
	baseline := makeResult("val",
		makeCase("a", false, 0.0, critFinalResponseText(), ""),
		makeCase("b", false, 0.0, critFinalResponseText(), ""),
		makeCase("KEY", true, 1.0, critFinalResponseText(), ""),
	)
	candidate := makeResult("val",
		makeCase("a", true, 1.0, critFinalResponseText(), ""),
		makeCase("b", true, 1.0, critFinalResponseText(), ""),
		makeCase("KEY", false, 0.0, critFinalResponseText(), ""),
	)
	d := ApplyGate(GatePolicy{MinValidationGain: 0.01, KeyCaseIDs: []string{"KEY"}}, baseline, candidate, GateObservations{})
	if d.Accepted {
		t.Fatalf("expected reject on overfit, got accept")
	}
	if d.ValidationGain <= 0 {
		t.Errorf("gain should still be positive (%v) — the point is that it is rejected despite that", d.ValidationGain)
	}
	if len(d.KeyRegressions) != 1 || d.KeyRegressions[0] != "KEY" {
		t.Errorf("KeyRegressions = %v, want [KEY]", d.KeyRegressions)
	}
	if !containsSubstring(d.Reasons, "overfitting") {
		t.Errorf("reasons %v should mention overfitting", d.Reasons)
	}
}

func TestApplyGateRejectsWhenGainBelowThreshold(t *testing.T) {
	baseline := makeResult("val",
		makeCase("a", true, 1.0, critFinalResponseText(), ""),
		makeCase("b", false, 0.0, critFinalResponseText(), ""),
	)
	candidate := makeResult("val",
		makeCase("a", true, 1.0, critFinalResponseText(), ""),
		makeCase("b", false, 0.0, critFinalResponseText(), ""),
	)
	d := ApplyGate(GatePolicy{MinValidationGain: 0.01}, baseline, candidate, GateObservations{})
	if d.Accepted {
		t.Fatalf("expected reject on zero gain, got accept")
	}
	if len(d.Regressions) != 0 {
		t.Errorf("expected no regressions, got %v", d.Regressions)
	}
}

func TestApplyGateRejectsNonKeyRegressionViaNoHardFail(t *testing.T) {
	// A non-key case regresses pass→fail while the mean still rises and no key cases are configured.
	// Acceptance must be vetoed solely by no_new_hard_fail (key_cases_protected stays green), and the
	// rejection reason must be the plain-regression branch, NOT the "overfitting" key-case branch.
	baseline := makeResult("val",
		makeCase("a", false, 0.0, critFinalResponseText(), ""),
		makeCase("b", true, 1.0, critFinalResponseText(), ""),
		makeCase("c", false, 0.0, critFinalResponseText(), ""),
	)
	candidate := makeResult("val",
		makeCase("a", true, 1.0, critFinalResponseText(), ""),
		makeCase("b", false, 0.0, critFinalResponseText(), ""),
		makeCase("c", true, 1.0, critFinalResponseText(), ""),
	)
	d := ApplyGate(GatePolicy{MinValidationGain: 0.01}, baseline, candidate, GateObservations{})
	if d.Accepted {
		t.Fatalf("expected reject: a non-key case regressed pass→fail")
	}
	if d.ValidationGain <= 0 {
		t.Errorf("mean gain should be positive (%v); rejection must come from no_new_hard_fail, not gain", d.ValidationGain)
	}
	if c, _ := criterionByName(d, "no_new_hard_fail"); c.Passed {
		t.Errorf("no_new_hard_fail should fail on the non-key regression")
	}
	if c, _ := criterionByName(d, "key_cases_protected"); !c.Passed {
		t.Errorf("key_cases_protected should pass (no key cases configured)")
	}
	if len(d.KeyRegressions) != 0 {
		t.Errorf("KeyRegressions should be empty, got %v", d.KeyRegressions)
	}
	if !containsSubstring(d.Reasons, "[b]") {
		t.Errorf("reasons should name the regressed non-key case [b], got %v", d.Reasons)
	}
	if containsSubstring(d.Reasons, "overfitting") {
		t.Errorf("non-key regression must not use the overfitting reason branch, got %v", d.Reasons)
	}
}

func TestApplyGateMeansIncludeDroppedCase(t *testing.T) {
	// A previously-passing case is dropped from the candidate. DiffResults scores it 0/failed, and
	// the gate's means must reflect that (candidate mean 0.5, gain -0.5) rather than silently
	// excluding it from the denominator (which would show gain 0). The aggregate must agree with
	// ValidationDeltas.
	baseline := makeResult("val",
		makeCase("a", true, 1.0, critFinalResponseText(), ""),
		makeCase("b", true, 1.0, critFinalResponseText(), ""),
	)
	candidate := makeResult("val",
		makeCase("a", true, 1.0, critFinalResponseText(), ""),
		// "b" dropped.
	)
	d := ApplyGate(GatePolicy{MinValidationGain: 0.01}, baseline, candidate, GateObservations{})
	if d.CandidateValidationMean != 0.5 {
		t.Errorf("CandidateValidationMean = %v, want 0.5 (dropped case counted as 0)", d.CandidateValidationMean)
	}
	if d.ValidationGain != -0.5 {
		t.Errorf("ValidationGain = %v, want -0.5", d.ValidationGain)
	}
	if d.Accepted {
		t.Errorf("expected reject: dropped case is a regression")
	}
}

func containsSubstring(reasons []string, needle string) bool {
	for _, r := range reasons {
		for i := 0; i+len(needle) <= len(r); i++ {
			if r[i:i+len(needle)] == needle {
				return true
			}
		}
	}
	return false
}

func criterionByName(d GateDecision, name string) (GateCriterion, bool) {
	for _, c := range d.Criteria {
		if c.Name == name {
			return c, true
		}
	}
	return GateCriterion{}, false
}

func TestApplyGateExposesFourDistinctCriteria(t *testing.T) {
	baseline := makeResult("val", makeCase("a", false, 0.0, critFinalResponseText(), ""))
	candidate := makeResult("val", makeCase("a", true, 1.0, critFinalResponseText(), ""))
	d := ApplyGate(GatePolicy{MinValidationGain: 0.01, KeyCaseIDs: []string{"a"}}, baseline, candidate, GateObservations{})
	for _, name := range []string{"validation_improves", "no_new_hard_fail", "key_cases_protected", "within_budget"} {
		if _, ok := criterionByName(d, name); !ok {
			t.Errorf("missing gate criterion %q", name)
		}
	}
}

func TestApplyGateBudgetCriterion(t *testing.T) {
	baseline := makeResult("val", makeCase("a", false, 0.0, critFinalResponseText(), ""))
	candidate := makeResult("val", makeCase("a", true, 1.0, critFinalResponseText(), ""))

	// Budget disabled (0) → within_budget passes and accept holds.
	d := ApplyGate(GatePolicy{MinValidationGain: 0.01}, baseline, candidate, GateObservations{CandidateModelCalls: 999})
	if c, _ := criterionByName(d, "within_budget"); !c.Passed {
		t.Errorf("budget disabled should pass, got %+v", c)
	}
	if !d.Accepted {
		t.Errorf("expected accept with budget disabled")
	}

	// Budget exceeded → within_budget fails and accept is vetoed.
	d = ApplyGate(GatePolicy{MinValidationGain: 0.01, MaxCandidateModelCalls: 10}, baseline, candidate, GateObservations{CandidateModelCalls: 11})
	if c, _ := criterionByName(d, "within_budget"); c.Passed {
		t.Errorf("budget exceeded should fail")
	}
	if d.Accepted {
		t.Errorf("expected reject when budget exceeded")
	}

	// Budget met → within_budget passes.
	d = ApplyGate(GatePolicy{MinValidationGain: 0.01, MaxCandidateModelCalls: 10}, baseline, candidate, GateObservations{CandidateModelCalls: 10})
	if c, _ := criterionByName(d, "within_budget"); !c.Passed {
		t.Errorf("budget met (equal) should pass")
	}
}
