//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import "testing"

func TestCandidateTargetsCurrentFailuresMatchesSecondaryAttribution(t *testing.T) {
	currentTrain := evaluationSummary{
		Cases: []caseEvaluation{{
			FailureAttributions: []failureAttribution{
				{Category: failureRoute, Evidence: "expected tool was not called"},
				{Category: failureToolCall, Evidence: "expected tool call is missing"},
			},
		}},
	}

	if !candidateTargetsCurrentFailures(candidateConfig{
		TargetFailures: []failureCategory{failureToolCall},
	}, currentTrain) {
		t.Fatal("candidate did not match a secondary failure attribution")
	}
	if candidateTargetsCurrentFailures(candidateConfig{
		TargetFailures: []failureCategory{failureFormat},
	}, currentTrain) {
		t.Fatal("candidate matched a failure category that is not present")
	}
}
