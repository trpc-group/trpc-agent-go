//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package regression

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestReportJSONMarkdownAndBackwardCompatibility(t *testing.T) {
	value := &Report{
		Version: 1, Seed: 42, ModelConfig: ModelConfig{Mode: "fake", Name: "deterministic"},
		Baseline:  BaselineSnapshot{Train: EvaluationSnapshot{Score: 0.2}, Validation: EvaluationSnapshot{Score: 0.3}},
		Rounds:    []RoundReport{{Round: 1, PromptIterAccepted: true, ReleaseGate: GateDecision{Accepted: false, Reasons: []string{"release_rejected"}}}},
		WriteBack: WriteBackDecision{RecommendedForWriteBack: false, Performed: false, AcceptedProfileRef: "baseline/input_profile.json"},
	}
	payload, err := JSON(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"baseline", "rounds", "writeBack", "usage", "estimatedCost", "failureAttributionStats"} {
		if _, ok := decoded[field]; !ok {
			t.Errorf("missing JSON field %q", field)
		}
	}
	markdown := Markdown(value)
	for _, section := range []string{"## Baseline", "## Candidate Decisions", "## Failure Attribution Summary", "## Write-Back Decision", "## Usage and Cost"} {
		if !bytes.Contains(markdown, []byte(section)) {
			t.Errorf("missing markdown section %q", section)
		}
	}

	legacy := []byte(`{"seed":1,"baseline":{"train":{"score":0.2},"validation":{"score":0.3}},"rounds":[],"decision":{"acceptedProfileRef":"old"},"unknownLegacyField":true}`)
	var compatible Report
	if err := json.Unmarshal(legacy, &compatible); err != nil {
		t.Fatalf("legacy report no longer decodes: %v", err)
	}
}

func TestFailureAttributionStats(t *testing.T) {
	snapshot := EvaluationSnapshot{PerCase: []CaseSummary{
		{CaseID: "one", FailureReasons: []FailureReason{{Code: FailureFinalResponseMismatch}, {Code: FailureToolArgumentError}, {Code: FailureToolArgumentError}}},
		{CaseID: "two", FailureReasons: []FailureReason{{Code: FailureFinalResponseMismatch}}},
	}}
	stats := buildFailureAttributionStats(BaselineSnapshot{Train: snapshot}, []RoundReport{{Round: 1, Validation: snapshot}})
	if stats.BaselineTrain[FailureFinalResponseMismatch] != 2 || stats.BaselineTrain[FailureToolArgumentError] != 1 {
		t.Fatalf("baseline failure stats = %#v", stats.BaselineTrain)
	}
	if len(stats.Rounds) != 1 || stats.Rounds[0].CandidateValidation[FailureFinalResponseMismatch] != 2 {
		t.Fatalf("round failure stats = %#v", stats.Rounds)
	}
}

func TestMarkdownIncludesFailureAttributionSummary(t *testing.T) {
	value := &Report{FailureAttributionStats: FailureAttributionStats{
		BaselineTrain: map[string]int{FailureFormatError: 2}, BaselineValidation: map[string]int{},
		Rounds: []RoundFailureAttributionStats{{Round: 1, CandidateTrain: map[string]int{FailureToolCallError: 1}, CandidateValidation: map[string]int{}}},
	}}
	markdown := Markdown(value)
	for _, expected := range []string{"## Failure Attribution Summary", "| Baseline train | format_error | 2 |", "| Round 1 train | tool_call_error | 1 |"} {
		if !bytes.Contains(markdown, []byte(expected)) {
			t.Errorf("markdown missing %q", expected)
		}
	}
}
