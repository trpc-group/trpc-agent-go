//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"bytes"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
)

func TestLoadInputsUsesStandardEvaluationAssets(t *testing.T) {
	first, err := loadInputs("data")
	if err != nil {
		t.Fatal(err)
	}
	second, err := loadInputs("data")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.train.EvalCases) != 4 || len(first.validation.EvalCases) != 5 || len(first.metrics) != 6 {
		t.Fatalf("unexpected assets: train=%d validation=%d metrics=%d",
			len(first.train.EvalCases), len(first.validation.EvalCases), len(first.metrics))
	}
	if first.fingerprint == "" || first.fingerprint != second.fingerprint {
		t.Fatalf("input fingerprint is not reproducible: %q %q", first.fingerprint, second.fingerprint)
	}
	for _, evaluationCase := range append(first.train.EvalCases, first.validation.EvalCases...) {
		if evaluationCase == nil || len(evaluationCase.Conversation) == 0 ||
			evaluationCase.Conversation[0].UserContent == nil ||
			evaluationCase.Conversation[0].FinalResponse == nil {
			t.Fatalf("case does not use standard Evaluation conversation schema: %+v", evaluationCase)
		}
	}
	if !strings.Contains(first.baselinePrompt, "Never reveal another customer's order data") {
		t.Fatalf("baseline safety rule missing: %q", first.baselinePrompt)
	}
	if len(first.config.CriticalCaseIDs) != 1 || first.config.CriticalCaseIDs[0] != "validation-private-order" {
		t.Fatalf("critical cases are not configured in gate policy: %v", first.config.CriticalCaseIDs)
	}
	if first.config.MaxRounds != 4 || first.config.TargetScore == nil || *first.config.TargetScore != .95 {
		t.Fatalf("progressive optimization target is not configured: %+v", first.config)
	}
}

func TestNormalizeTextMakesLineEndingsReproducible(t *testing.T) {
	lf := normalizeText([]byte("first\nsecond\n"))
	crlf := normalizeText([]byte("first\r\nsecond\r\n"))
	cr := normalizeText([]byte("first\rsecond\r"))
	if !bytes.Equal(lf, crlf) || !bytes.Equal(lf, cr) {
		t.Fatalf("line endings differ: lf=%q crlf=%q cr=%q", lf, crlf, cr)
	}
}

func TestDecodeStrictRejectsUnknownAndTrailingJSON(t *testing.T) {
	for name, content := range map[string]string{
		"unknown field":  `{"seed":7,"unknown":true}`,
		"trailing value": `{"seed":7} {"seed":8}`,
	} {
		t.Run(name, func(t *testing.T) {
			var config runConfig
			if err := decodeStrict("promptiter.json", []byte(content), &config); err == nil {
				t.Fatal("invalid JSON was accepted")
			}
		})
	}
}

func TestInputsValidationRejectsDuplicateCaseIDs(t *testing.T) {
	loaded, err := loadInputs("data")
	if err != nil {
		t.Fatal(err)
	}
	loaded.validation.EvalCases[0].EvalID = loaded.train.EvalCases[0].EvalID
	if err := loaded.validate(); err == nil {
		t.Fatal("duplicate train/validation case id was accepted")
	}
}

func TestInputsValidationRejectsIncompleteAssets(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*inputs)
	}{
		{"same eval set id", func(value *inputs) { value.validation.EvalSetID = value.train.EvalSetID }},
		{"too few train cases", func(value *inputs) { value.train.EvalCases = value.train.EvalCases[:2] }},
		{"missing metrics", func(value *inputs) { value.metrics = nil }},
		{"missing metric policies", func(value *inputs) { value.config.MetricPolicies = nil }},
		{"missing baseline prompt", func(value *inputs) { value.baselinePrompt = "" }},
		{"invalid num runs", func(value *inputs) { value.config.NumRuns = 0 }},
		{"invalid max rounds", func(value *inputs) { value.config.MaxRounds = 0 }},
		{"invalid target score", func(value *inputs) {
			value.config.TargetScore = float64Pointer(1.1)
		}},
		{"nil eval case", func(value *inputs) { value.train.EvalCases[0] = nil }},
		{"empty eval id", func(value *inputs) { value.train.EvalCases[0].EvalID = "" }},
		{"missing conversation", func(value *inputs) { value.train.EvalCases[0].Conversation = nil }},
		{"nil invocation", func(value *inputs) { value.train.EvalCases[0].Conversation[0] = nil }},
		{"missing final response", func(value *inputs) { value.train.EvalCases[0].Conversation[0].FinalResponse = nil }},
		{"nil metric", func(value *inputs) { value.metrics[0] = nil }},
		{"missing metric name", func(value *inputs) { value.metrics[0].MetricName = "" }},
		{"missing evaluator name", func(value *inputs) { value.metrics[0].EvaluatorName = "" }},
		{"missing release policy", func(value *inputs) { delete(value.config.MetricPolicies, value.metrics[0].MetricName) }},
		{"duplicate metric", func(value *inputs) { value.metrics[1].MetricName = value.metrics[0].MetricName }},
		{"policy for absent metric", func(value *inputs) {
			value.config.MetricPolicies["not_in_metrics_json"] = value.config.MetricPolicies[value.metrics[0].MetricName]
		}},
		{"critical case absent from validation", func(value *inputs) {
			value.config.CriticalCaseIDs = []string{"train-refund-window"}
		}},
		{"negative audit content limit", func(value *inputs) { value.config.Audit.MaxContentBytes = -1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			loaded, err := loadInputs("data")
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(loaded)
			if err := loaded.validate(); err == nil {
				t.Fatal("incomplete assets were accepted")
			}
		})
	}
}

func float64Pointer(value float64) *float64 { return &value }

func TestValidateEvalCaseRejectsMissingUserContent(t *testing.T) {
	caseValue := &evalset.EvalCase{
		EvalID:       "case",
		Conversation: []*evalset.Invocation{{FinalResponse: nil}},
	}
	if err := validateEvalCase(caseValue); err == nil {
		t.Fatal("case without user content was accepted")
	}
}

func TestLoadInputsReportsMissingAssetDirectory(t *testing.T) {
	if _, err := loadInputs(t.TempDir()); err == nil {
		t.Fatal("missing input assets were accepted")
	}
}

func TestFingerprintsAreStableAndOrderIndependent(t *testing.T) {
	first := fingerprint(map[string][]byte{"a": []byte("one"), "b": []byte("two")})
	second := fingerprint(map[string][]byte{"b": []byte("two"), "a": []byte("one")})
	if first == "" || first != second {
		t.Fatalf("fingerprint is not stable: %q %q", first, second)
	}
	if scenarioFingerprint(first, "success") == scenarioFingerprint(first, "overfit") {
		t.Fatal("scenario was not included in scenario fingerprint")
	}
}
