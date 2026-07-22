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

func TestComputeDeltaUnpairedCandidateIsUnexpected(t *testing.T) {
	// A candidate-only metric has no baseline pair: the compared metric sets
	// differ, so it is recorded as Missing/unexpected rather than silently skipped.
	baseline := evalR(0, caseR("c1", metricR("m", 0, status.EvalStatusFailed, "")))
	candidate := evalR(1,
		caseR("c1", metricR("m", 1, status.EvalStatusPassed, "")),
		caseR("c2", metricR("m", 1, status.EvalStatusPassed, "")),
	)
	got := ComputeDelta(baseline, candidate)
	if len(got.CaseDeltas) != 2 || got.Summary.UnexpectedMetrics != 1 {
		t.Fatalf("caseDeltas=%d unexpected=%d want 2/1", len(got.CaseDeltas), got.Summary.UnexpectedMetrics)
	}
}

func TestComputeDeltaDroppedFailingMetricIsMissing(t *testing.T) {
	// A baseline metric that vanishes from the candidate did NOT preserve its
	// score: the phases measured different metric sets. It must be recorded as
	// Missing (not Unchanged), so the gate can refuse the incomparable pair.
	baseline := evalR(0.0, caseR("c1", metricR("m", 0.0, status.EvalStatusFailed, "text mismatch")))
	candidate := evalR(0.0, caseR("c1"))
	got := ComputeDelta(baseline, candidate)
	if got.Summary.Unchanged != 0 || got.Summary.MissingMetrics != 1 {
		t.Fatalf("summary=%+v want Unchanged=0 MissingMetrics=1", got.Summary)
	}
	if got.CaseDeltas[0].Kind != DeltaMissing || got.CaseDeltas[0].CandidateStatus != statusAbsent {
		t.Fatalf("delta=%+v want kind Missing with candidate status absent", got.CaseDeltas[0])
	}
}

func TestComputeDeltaVanishedFailingMetricCannotInflateGain(t *testing.T) {
	// Baseline: one passing + one zero-scored failing metric (aggregate 0.5). The
	// candidate reports only the passing metric (aggregate 1.0). The vanished
	// failing metric must surface as MissingMetrics so the gate rejects the
	// inflated gain instead of releasing on it.
	baseline := evalR(0.5, caseR("c1",
		metricR("pass_metric", 1.0, status.EvalStatusPassed, ""),
		metricR("fail_metric", 0.0, status.EvalStatusFailed, "text mismatch"),
	))
	candidate := evalR(1.0, caseR("c1", metricR("pass_metric", 1.0, status.EvalStatusPassed, "")))
	delta := ComputeDelta(baseline, candidate)
	if delta.Summary.MissingMetrics != 1 || delta.Summary.NewlyFailed != 0 {
		t.Fatalf("summary=%+v want MissingMetrics=1", delta.Summary)
	}
	gate := ReleaseGate{MinTotalGain: 0.2}.Evaluate(GateInput{
		ProfileAccepted: true,
		TotalGain:       0.5,
		Delta:           delta,
	})
	if gate.Released {
		t.Fatalf("vanished failing metric must not release, reasons=%v", gate.Reasons)
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

func TestComputeDeltaDroppedPassingMetricIsMissing(t *testing.T) {
	baseline := evalR(1.0, caseR("c1", metricR("m", 1.0, status.EvalStatusPassed, "")))
	// Candidate case c1 exists but the metric m is gone (e.g. it stopped being
	// evaluated); the passing baseline metric must not be silently dropped.
	candidate := evalR(0.0, caseR("c1"))
	got := ComputeDelta(baseline, candidate)
	if got.Summary.MissingMetrics != 1 {
		t.Fatalf("missingMetrics=%d want 1 (dropped passing baseline metric)", got.Summary.MissingMetrics)
	}
	if len(got.CaseDeltas) != 1 || got.CaseDeltas[0].CandidateStatus != statusAbsent {
		t.Fatalf("expected one delta with candidate status %q, got %+v", statusAbsent, got.CaseDeltas)
	}
}
