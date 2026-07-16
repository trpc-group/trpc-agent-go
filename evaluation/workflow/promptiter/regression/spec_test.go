//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"math"
	"strings"
	"testing"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
)

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
		{name: "zero runs", mutate: func(spec *RunSpec) { spec.Runtime.NumRuns = 0 }},
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

func TestProfileHashIsStableAndSensitiveToProfileContent(t *testing.T) {
	if _, err := ProfileHash(nil); err == nil {
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
	first, err := ProfileHash(profile)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ProfileHash(profile)
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || first != second {
		t.Fatalf("profile hash is not stable: %q %q", first, second)
	}
	*profile.Overrides[0].Value.Text = "candidate"
	changed, err := ProfileHash(profile)
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
		InputFingerprint: "fingerprint",
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
