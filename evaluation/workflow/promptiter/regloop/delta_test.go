//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regloop

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

func TestComputeDeltaKinds(t *testing.T) {
	baseline := evalR(0.25,
		caseR("c1", metricR("m", 0.0, status.EvalStatusFailed, "")), // -> NewlyPassed
		caseR("c2", metricR("m", 1.0, status.EvalStatusPassed, "")), // -> NewlyFailed
		caseR("c3", metricR("m", 0.4, status.EvalStatusFailed, "")), // -> ScoreUp (still failed)
		caseR("c4", metricR("m", 0.8, status.EvalStatusPassed, "")), // -> ScoreDown (still passed)
		caseR("c5", metricR("m", 1.0, status.EvalStatusPassed, "")), // -> Unchanged
	)
	candidate := evalR(0.75,
		caseR("c1", metricR("m", 1.0, status.EvalStatusPassed, "")),
		caseR("c2", metricR("m", 0.0, status.EvalStatusFailed, "")),
		caseR("c3", metricR("m", 0.6, status.EvalStatusFailed, "")),
		caseR("c4", metricR("m", 0.7, status.EvalStatusPassed, "")),
		caseR("c5", metricR("m", 1.0, status.EvalStatusPassed, "")),
	)
	got := ComputeDelta(baseline, candidate)
	want := DeltaSummary{NewlyPassed: 1, NewlyFailed: 1, ScoreUp: 1, ScoreDown: 1, Unchanged: 1}
	if got.Summary != want {
		t.Fatalf("summary=%+v want %+v", got.Summary, want)
	}
	if len(got.CaseDeltas) != 5 {
		t.Fatalf("caseDeltas=%d want 5", len(got.CaseDeltas))
	}
}

func TestComputeDeltaUnpairedIgnored(t *testing.T) {
	baseline := evalR(0, caseR("c1", metricR("m", 0, status.EvalStatusFailed, "")))
	candidate := evalR(1,
		caseR("c1", metricR("m", 1, status.EvalStatusPassed, "")),
		caseR("c2", metricR("m", 1, status.EvalStatusPassed, "")), // no baseline pair -> skipped
	)
	got := ComputeDelta(baseline, candidate)
	if len(got.CaseDeltas) != 1 {
		t.Fatalf("caseDeltas=%d want 1 (unpaired candidate skipped)", len(got.CaseDeltas))
	}
}

func TestComputeDeltaDroppedFailingMetricIsUnchanged(t *testing.T) {
	// A baseline metric that was already failing at score 0 and then vanishes has
	// nothing to lose: it must not be reported as a 0 -> 0 ScoreDown.
	baseline := evalR(0.0, caseR("c1", metricR("m", 0.0, status.EvalStatusFailed, "text mismatch")))
	candidate := evalR(0.0, caseR("c1"))
	got := ComputeDelta(baseline, candidate)
	if got.Summary.ScoreDown != 0 || got.Summary.Unchanged != 1 {
		t.Fatalf("summary=%+v want ScoreDown=0 Unchanged=1", got.Summary)
	}
	if got.CaseDeltas[0].Kind != DeltaUnchanged {
		t.Fatalf("kind=%s want Unchanged", got.CaseDeltas[0].Kind)
	}
}

func TestAcceptedValidationPrefersAcceptedRound(t *testing.T) {
	v1 := evalR(0.4, caseR("c1", metricR("m", 0.4, status.EvalStatusFailed, "")))
	v2 := evalR(1.0, caseR("c1", metricR("m", 1.0, status.EvalStatusPassed, "")))
	result := &engine.RunResult{
		BaselineValidation: evalR(0, caseR("c1", metricR("m", 0, status.EvalStatusFailed, ""))),
		Rounds: []engine.RoundResult{
			lossRound(1, v1, false, 0.4),
			lossRound(2, v2, true, 0.6),
		},
	}
	got, round := acceptedValidation(result)
	if round != 2 || got != v2 {
		t.Fatalf("acceptedValidation round=%d want 2", round)
	}
}

func TestAcceptedValidationFallsBackToBaseline(t *testing.T) {
	baseline := evalR(0)
	v1 := evalR(0.4, caseR("c1", metricR("m", 0.4, status.EvalStatusFailed, "")))
	result := &engine.RunResult{
		BaselineValidation: baseline,
		// One rejected round: acceptedValidation must NOT return its validation,
		// otherwise the report would present a candidate the engine rejected.
		Rounds: []engine.RoundResult{lossRound(1, v1, false, 0.4)},
	}
	got, round := acceptedValidation(result)
	if round != 0 || got != baseline {
		t.Fatalf("fallback round=%d got!=baseline; want baseline fallback for a run with no accepted round", round)
	}
}

func TestComputeDeltaDroppedBaselineMetricIsRegression(t *testing.T) {
	baseline := evalR(1.0, caseR("c1", metricR("m", 1.0, status.EvalStatusPassed, "")))
	// Candidate case c1 exists but the metric m is gone (e.g. it stopped being
	// evaluated); the passing baseline metric must not be silently dropped.
	candidate := evalR(0.0, caseR("c1"))
	got := ComputeDelta(baseline, candidate)
	if got.Summary.NewlyFailed != 1 {
		t.Fatalf("newlyFailed=%d want 1 (dropped passing baseline metric)", got.Summary.NewlyFailed)
	}
	if len(got.CaseDeltas) != 1 || got.CaseDeltas[0].CandidateStatus != statusAbsent {
		t.Fatalf("expected one delta with candidate status %q, got %+v", statusAbsent, got.CaseDeltas)
	}
}
