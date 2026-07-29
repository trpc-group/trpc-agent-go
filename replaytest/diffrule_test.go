//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"math"
	"testing"
)

// --- MergeDiffRules ---

func TestMergeDiffRules_SpecOnly(t *testing.T) {
	spec := []DiffRule{
		{Path: "$.events[*].id", Kind: "auto_id", Strategy: "ignore"},
	}
	result := MergeDiffRules(nil, spec)
	if len(result) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(result))
	}
	if result[0].Path != "$.events[*].id" {
		t.Errorf("expected spec rule path, got %s", result[0].Path)
	}
}

func TestMergeDiffRules_GlobalOnly(t *testing.T) {
	global := []DiffRule{
		{Path: "$.events[*].id", Kind: "auto_id", Strategy: "ignore"},
	}
	result := MergeDiffRules(global, nil)
	if len(result) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(result))
	}
	if result[0].Path != "$.events[*].id" {
		t.Errorf("expected global rule path, got %s", result[0].Path)
	}
}

func TestMergeDiffRules_SpecOverridesGlobal(t *testing.T) {
	global := []DiffRule{
		{Path: "$.events[*].id", Kind: "auto_id", Strategy: "ignore"},
		{Path: "$.events[*].timestamp", Kind: "timestamp_drift", Strategy: "allow_drift", MaxDrift: &DriftSpec{DurationMS: 5000}},
	}
	spec := []DiffRule{
		{Path: "$.events[*].timestamp", Kind: "timestamp_drift", Strategy: "allow_drift", MaxDrift: &DriftSpec{DurationMS: 10000}},
	}
	result := MergeDiffRules(global, spec)
	if len(result) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(result))
	}
	// Spec rule should take precedence (appears first).
	if result[0].Path != "$.events[*].timestamp" {
		t.Errorf("spec rule (path match) should be first, got %s", result[0].Path)
	}
	if result[0].MaxDrift.DurationMS != 10000 {
		t.Errorf("spec rule MaxDrift should be 10000, got %d", result[0].MaxDrift.DurationMS)
	}
	// Global rule that doesn't conflict should still be present.
	if result[1].Path != "$.events[*].id" {
		t.Errorf("non-conflicting global rule should be present, got %s", result[1].Path)
	}
}

func TestMergeDiffRules_MultipleSpecRules(t *testing.T) {
	global := []DiffRule{
		{Path: "$.events[*].id", Kind: "auto_id", Strategy: "ignore"},
		{Path: "$.events[*].timestamp", Kind: "timestamp_drift", Strategy: "allow_drift"},
		{Path: "$.events[*].requestID", Kind: "auto_id", Strategy: "ignore"},
	}
	spec := []DiffRule{
		{Path: "$.events[*].id", Kind: "auto_id", Strategy: "ignore", Backends: []string{"sqlite"}},
		{Path: "$.state[*]", Kind: "backend_metadata", Strategy: "ignore"},
	}
	result := MergeDiffRules(global, spec)
	// 2 spec + 2 non-conflicting global = 4
	if len(result) != 4 {
		t.Fatalf("expected 4 rules, got %d", len(result))
	}
	// First two should be spec rules (order preserved).
	if result[0].Path != "$.events[*].id" {
		t.Errorf("[0] should be spec events.id, got %s", result[0].Path)
	}
	if result[1].Path != "$.state[*]" {
		t.Errorf("[1] should be spec state, got %s", result[1].Path)
	}
}

func TestMergeDiffRules_EmptyInputs(t *testing.T) {
	result := MergeDiffRules(nil, nil)
	if len(result) != 0 {
		t.Errorf("both nil should return empty, got %d", len(result))
	}
	result = MergeDiffRules([]DiffRule{}, []DiffRule{})
	if len(result) != 0 {
		t.Errorf("both empty should return empty, got %d", len(result))
	}
}

// --- IsDriftAllowed ---

func TestIsDriftAllowed_TimestampDriftWithinMS(t *testing.T) {
	d := &DriftSpec{DurationMS: 5000}
	if !d.IsDriftAllowed("timestamp_drift", 3000) {
		t.Error("drift 3000ms within 5000ms should be allowed")
	}
}

func TestIsDriftAllowed_TimestampDriftExceedsMS(t *testing.T) {
	d := &DriftSpec{DurationMS: 5000}
	if d.IsDriftAllowed("timestamp_drift", 6000) {
		t.Error("drift 6000ms exceeds 5000ms, should not be allowed")
	}
}

func TestIsDriftAllowed_TimestampDriftWithinNS(t *testing.T) {
	d := &DriftSpec{DurationNS: 100}
	if !d.IsDriftAllowed("timestamp_drift", 50) {
		t.Error("drift 50ns within 100ns should be allowed")
	}
}

func TestIsDriftAllowed_FloatPrecision(t *testing.T) {
	d := &DriftSpec{FloatEpsilon: 1e-4}
	if !d.IsDriftAllowed("float_precision", 5e-5) {
		t.Error("drift 0.00005 within epsilon 0.0001 should be allowed")
	}
	if d.IsDriftAllowed("float_precision", 2e-4) {
		t.Error("drift 0.0002 exceeds epsilon 0.0001, should not be allowed")
	}
}

func TestIsDriftAllowed_DurationDrift(t *testing.T) {
	d := &DriftSpec{DurationNS: 1000}
	if !d.IsDriftAllowed("duration_drift", 500) {
		t.Error("duration drift 500ns within 1000ns should be allowed")
	}
	if d.IsDriftAllowed("duration_drift", 2000) {
		t.Error("duration drift 2000ns exceeds 1000ns, should not be allowed")
	}
}

func TestIsDriftAllowed_UnknownKind(t *testing.T) {
	d := &DriftSpec{DurationMS: 5000, FloatEpsilon: 1e-4}
	if d.IsDriftAllowed("unknown_kind", 10) {
		t.Error("unknown kind should not be allowed")
	}
}

func TestIsDriftAllowed_NilSpec(t *testing.T) {
	var d *DriftSpec
	if d.IsDriftAllowed("timestamp_drift", 10) {
		t.Error("nil DriftSpec should not allow any drift")
	}
}

func TestIsDriftAllowed_ZeroValues(t *testing.T) {
	d := &DriftSpec{}
	if d.IsDriftAllowed("timestamp_drift", 0) {
		t.Error("zero DurationMS should not allow drift (drift=0)")
	}
	if d.IsDriftAllowed("float_precision", 0) {
		t.Error("zero FloatEpsilon should not allow drift")
	}
}

// --- floatEqual ---

func TestFloatEqual_WithinEpsilon(t *testing.T) {
	if !floatEqual(1.00001, 1.00002, 1e-4) {
		t.Error("diff 0.00001 <= 0.0001 should be equal")
	}
}

func TestFloatEqual_ExceedsEpsilon(t *testing.T) {
	if floatEqual(1.0, 2.0, 1e-4) {
		t.Error("diff 1.0 > 0.0001 should not be equal")
	}
}

func TestFloatEqual_ExactlyEqual(t *testing.T) {
	if !floatEqual(3.14, 3.14, 0) {
		t.Error("exact equal should be equal even with epsilon=0")
	}
}

func TestFloatEqual_NaN(t *testing.T) {
	if !floatEqual(math.NaN(), math.NaN(), 1e-4) {
		t.Error("NaN == NaN should be true")
	}
}

func TestFloatEqual_NaNvsNumber(t *testing.T) {
	if floatEqual(math.NaN(), 1.0, 1e-4) {
		t.Error("NaN vs number should not be equal")
	}
}

func TestFloatEqual_PosInf(t *testing.T) {
	if !floatEqual(math.Inf(1), math.Inf(1), 1e-4) {
		t.Error("+Inf == +Inf should be true")
	}
}

func TestFloatEqual_NegInf(t *testing.T) {
	if !floatEqual(math.Inf(-1), math.Inf(-1), 1e-4) {
		t.Error("-Inf == -Inf should be true")
	}
}

func TestFloatEqual_PosVsNegInf(t *testing.T) {
	if floatEqual(math.Inf(1), math.Inf(-1), 1e-4) {
		t.Error("+Inf vs -Inf should not be equal")
	}
}

func TestFloatEqual_InfVsMaxFloat(t *testing.T) {
	if floatEqual(math.Inf(1), math.MaxFloat64, 1e10) {
		t.Error("Inf vs MaxFloat64 should not be equal")
	}
}

// --- MatchBackend ---

func TestDiffRule_MatchBackend_Empty(t *testing.T) {
	r := DiffRule{}
	if !r.MatchBackend("any") {
		t.Error("empty Backends should match anything")
	}
	if !r.MatchBackend("") {
		t.Error("empty Backends should match empty string")
	}
}

func TestDiffRule_MatchBackend_Exact(t *testing.T) {
	r := DiffRule{Backends: []string{"sqlite", "postgres"}}
	if !r.MatchBackend("sqlite") {
		t.Error("should match exact backend name")
	}
	if !r.MatchBackend("postgres") {
		t.Error("should match second backend name")
	}
	if r.MatchBackend("inmemory") {
		t.Error("should not match unlisted backend")
	}
}

// --- DefaultDiffRules ---

func TestDefaultDiffRules_NonEmpty(t *testing.T) {
	rules := DefaultDiffRules()
	if len(rules) == 0 {
		t.Fatal("DefaultDiffRules should not be empty")
	}
	// Verify key rules exist.
	paths := make(map[string]bool)
	for _, r := range rules {
		paths[r.Path] = true
	}
	expected := []string{
		"$.events[*].id",
		"$.events[*].timestamp",
		"$.events[*].requestID",
		"$.events[*].invocationId",
		"$.events[*].parentInvocationId",
		"$.events[*].usage[*]",
		"$.events[*].response.id",
		"$.tracks[*].events[*].timestamp",
		"$.memories[*].memory.eventTime",
		"$.memories[*].score",
	}
	for _, p := range expected {
		if !paths[p] {
			t.Errorf("DefaultDiffRules missing expected path: %s", p)
		}
	}
}

func TestDefaultDiffRules_Strategies(t *testing.T) {
	rules := DefaultDiffRules()
	for _, r := range rules {
		switch r.Strategy {
		case "ignore", "allow_drift":
			// valid
		default:
			t.Errorf("unexpected strategy %q in default rule %s", r.Strategy, r.Path)
		}
	}
}
