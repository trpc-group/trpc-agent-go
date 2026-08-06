//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
)

func TestRuleValueUsesStableTaggedJSONSchema(t *testing.T) {
	values := []RuleValue{
		BooleanRuleValue(true),
		IntegerRuleValue(-2),
		NumberRuleValue(.25),
		TextRuleValue("missing"),
		DurationRuleValue(250 * time.Millisecond),
	}
	for _, value := range values {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal rule value: %v", err)
		}
		var tagged string
		if err := json.Unmarshal(data, &tagged); err != nil {
			t.Fatalf("decode tagged rule value: %v", err)
		}
		if want := string(value.Type) + "|" + value.Value; tagged != want {
			t.Fatalf("rule value tagged JSON = %q, want %q", tagged, want)
		}
		var decoded RuleValue
		if err := json.Unmarshal(data, &decoded); err != nil || decoded != value {
			t.Fatalf("rule value JSON did not round-trip: value=%+v decoded=%+v err=%v", value, decoded, err)
		}
		if err := value.validate(); err != nil {
			t.Fatalf("valid rule value %q: %v", value, err)
		}
	}
	for _, value := range []RuleValue{
		{Type: RuleValueBoolean, Value: "truthy"},
		{Type: RuleValueInteger, Value: "1.5"},
		{Type: RuleValueNumber, Value: "NaN"},
		{Type: RuleValueDuration, Value: "tomorrow"},
		{Type: "map", Value: "{}"},
	} {
		if err := value.validate(); err == nil {
			t.Fatalf("invalid rule value %+v was accepted", value)
		}
		if _, err := json.Marshal(value); err == nil {
			t.Fatalf("invalid rule value %+v was marshaled", value)
		}
	}
}

func TestRuntimePolicySerializesFalseDeterministicValue(t *testing.T) {
	for _, deterministic := range []bool{false, true} {
		data, err := json.Marshal(RuntimePolicy{NumRuns: 1, Deterministic: deterministic})
		if err != nil {
			t.Fatalf("marshal runtime policy: %v", err)
		}
		var encoded map[string]json.RawMessage
		if err := json.Unmarshal(data, &encoded); err != nil {
			t.Fatalf("decode runtime policy: %v", err)
		}
		value, ok := encoded["deterministic"]
		if !ok || string(value) != strconv.FormatBool(deterministic) {
			t.Fatalf("deterministic JSON value = %s (present=%t), want explicit %t", value, ok, deterministic)
		}
	}
}

func TestSanitizeRuleValuePreservesCanonicalScalars(t *testing.T) {
	policy := AuditPolicy{MaxContentBytes: 1}
	for _, value := range []RuleValue{
		BooleanRuleValue(true),
		IntegerRuleValue(123),
		NumberRuleValue(1.25),
		DurationRuleValue(250 * time.Millisecond),
	} {
		assertEqualRuleValue(t, value, sanitizeRuleValue(value, policy))
	}
}

func assertEqualRuleValue(t *testing.T, want, got RuleValue) {
	t.Helper()
	if got != want {
		t.Fatalf("unexpected sanitized rule value: want=%+v got=%+v", want, got)
	}
}

func TestRunSpecValidation(t *testing.T) {
	spec := validSpec()
	if err := spec.Validate(); err != nil {
		t.Fatalf("valid spec: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*RunSpec)
	}{
		{name: "missing run id", mutate: func(spec *RunSpec) { spec.RunID = "" }},
		{name: "unsafe run id", mutate: func(spec *RunSpec) { spec.RunID = "nested/run" }},
		{name: "reserved Windows run id", mutate: func(spec *RunSpec) { spec.RunID = "CON" }},
		{name: "reserved Windows run id with extension", mutate: func(spec *RunSpec) { spec.RunID = "aux.report" }},
		{name: "run id ending in dot", mutate: func(spec *RunSpec) { spec.RunID = "report." }},
		{name: "oversized run id", mutate: func(spec *RunSpec) { spec.RunID = strings.Repeat("a", 129) }},
		{name: "missing target", mutate: func(spec *RunSpec) { spec.TargetSurfaceID = "" }},
		{name: "missing fingerprint", mutate: func(spec *RunSpec) { spec.InputFingerprint = "" }},
		{name: "unsafe fingerprint", mutate: func(spec *RunSpec) { spec.InputFingerprint = "api_key=secret\nnext" }},
		{name: "zero runs", mutate: func(spec *RunSpec) { spec.Runtime.NumRuns = 0 }},
		{name: "stability gate with one run", mutate: func(spec *RunSpec) {
			spec.Runtime.NumRuns = 1
			spec.Gate.MaxScoreStdDev = .1
		}},
		{name: "zero metric weight", mutate: func(spec *RunSpec) {
			spec.MetricPolicies["quality"] = MetricPolicy{}
		}},
		{name: "duplicate critical case", mutate: func(spec *RunSpec) {
			spec.CriticalCaseIDs = []string{"critical", "critical"}
		}},
		{name: "empty critical case", mutate: func(spec *RunSpec) { spec.CriticalCaseIDs = []string{" "} }},
		{name: "negative validation gain", mutate: func(spec *RunSpec) { spec.Gate.MinValidationGain = -1 }},
		{name: "negative case regression", mutate: func(spec *RunSpec) { spec.Gate.MaxCaseRegression = -1 }},
		{name: "negative generalization gap", mutate: func(spec *RunSpec) { spec.Gate.MaxGeneralizationGap = -1 }},
		{name: "negative score deviation", mutate: func(spec *RunSpec) { spec.Gate.MaxScoreStdDev = -1 }},
		{name: "non-finite validation gain", mutate: func(spec *RunSpec) { spec.Gate.MinValidationGain = math.NaN() }},
		{name: "non-finite case regression", mutate: func(spec *RunSpec) { spec.Gate.MaxCaseRegression = math.Inf(1) }},
		{name: "non-finite generalization gap", mutate: func(spec *RunSpec) { spec.Gate.MaxGeneralizationGap = math.NaN() }},
		{name: "non-finite score deviation", mutate: func(spec *RunSpec) { spec.Gate.MaxScoreStdDev = math.Inf(1) }},
		{name: "negative call budget", mutate: func(spec *RunSpec) { spec.Budget.MaxCalls = -1 }},
		{name: "negative token budget", mutate: func(spec *RunSpec) { spec.Budget.MaxTokens = -1 }},
		{name: "negative cost budget", mutate: func(spec *RunSpec) { spec.Budget.MaxEstimatedCost = -1 }},
		{name: "non-finite cost budget", mutate: func(spec *RunSpec) { spec.Budget.MaxEstimatedCost = math.NaN() }},
		{name: "negative PromptIter latency budget", mutate: func(spec *RunSpec) { spec.Budget.MaxPromptIterLatency = -1 }},
		{name: "negative audit content limit", mutate: func(spec *RunSpec) { spec.Audit.MaxContentBytes = -1 }},
		{name: "missing metric policies", mutate: func(spec *RunSpec) { spec.MetricPolicies = nil }},
		{name: "empty metric name", mutate: func(spec *RunSpec) {
			spec.MetricPolicies = map[string]MetricPolicy{" ": {Weight: 1}}
		}},
		{name: "non-finite metric weight", mutate: func(spec *RunSpec) {
			spec.MetricPolicies["quality"] = MetricPolicy{Weight: math.NaN()}
		}},
		{name: "negative metric floor", mutate: func(spec *RunSpec) {
			spec.MetricPolicies["quality"] = MetricPolicy{Weight: 1, Floor: -1}
		}},
		{name: "metric floor above one", mutate: func(spec *RunSpec) {
			spec.MetricPolicies["quality"] = MetricPolicy{Weight: 1, Floor: 2}
		}},
		{name: "non-finite metric floor", mutate: func(spec *RunSpec) {
			spec.MetricPolicies["quality"] = MetricPolicy{Weight: 1, Floor: math.Inf(1)}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := validSpec()
			test.mutate(candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatal("invalid spec was accepted")
			}
		})
	}
}

func TestNilRunSpecIsInvalid(t *testing.T) {
	var spec *RunSpec
	if err := spec.Validate(); err == nil {
		t.Fatal("nil spec was accepted")
	}
}

func TestRunSpecAllowsSingleRunWhenStabilityGateIsDisabled(t *testing.T) {
	spec := validSpec()
	spec.Runtime.NumRuns = 1
	if err := spec.Validate(); err != nil {
		t.Fatalf("single run without stability gate: %v", err)
	}
}

func TestProfileHashIsStableAndSensitiveToProfileContent(t *testing.T) {
	if _, err := profileHash(nil); err == nil {
		t.Fatal("nil profile was hashed")
	}
	text := "baseline"
	profile := &promptiter.Profile{
		StructureID: "structure",
		Overrides: []promptiter.SurfaceOverride{{
			SurfaceID: "agent#instruction",
			Value:     astructure.SurfaceValue{Text: &text},
		}},
	}
	first, err := profileHash(profile)
	if err != nil {
		t.Fatal(err)
	}
	second, err := profileHash(profile)
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || first != second {
		t.Fatalf("profile hash is not stable: %q %q", first, second)
	}
	*profile.Overrides[0].Value.Text = "candidate"
	changed, err := profileHash(profile)
	if err != nil {
		t.Fatal(err)
	}
	if changed == first {
		t.Fatal("profile content change did not change hash")
	}
}

func validSpec() *RunSpec {
	return &RunSpec{
		RunID:            "run",
		TargetSurfaceID:  "agent#instruction",
		InputFingerprint: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Runtime:          RuntimePolicy{Seed: 7, NumRuns: 2},
		MetricPolicies: map[string]MetricPolicy{
			"quality": {Weight: 1},
		},
		Gate: GatePolicy{
			MinValidationGain: .01,
			MaxCaseRegression: .1,
		},
	}
}
