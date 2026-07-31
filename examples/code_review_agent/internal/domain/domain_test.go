//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package domain

import "testing"

func TestFindingValidateRequiresContractFields(t *testing.T) {
	f := Finding{
		Severity:       SeverityHigh,
		Category:       CategorySecurity,
		File:           "internal/app/app.go",
		Line:           12,
		Title:          "dynamic shell command",
		Evidence:       "exec.Command receives request input",
		Recommendation: "pass fixed argv and validate arguments",
		Confidence:     0.92,
		Source:         "rule",
		RuleID:         "security.command-injection",
	}
	if err := f.Validate(); err != nil {
		t.Fatalf("valid finding rejected: %v", err)
	}
	f.RuleID = ""
	if err := f.Validate(); err == nil {
		t.Fatalf("missing rule_id accepted")
	}
}

func TestFindingLineZeroIsAllowedForFileLevelIssues(t *testing.T) {
	f := Finding{
		Severity:       SeverityMedium,
		Category:       CategoryTests,
		File:           "internal/app/app.go",
		Line:           0,
		Title:          "production change lacks test coverage",
		Evidence:       "no related _test.go diff",
		Recommendation: "add focused tests for changed behavior",
		Confidence:     0.66,
		Source:         "rule",
		RuleID:         "tests.missing-related-test",
	}
	if err := f.Validate(); err != nil {
		t.Fatalf("file-level finding rejected: %v", err)
	}
}

func TestConfidenceBucketBoundaries(t *testing.T) {
	cases := []struct {
		conf float64
		want Bucket
	}{
		{0.99, BucketFinding},
		{0.80, BucketFinding},
		{0.79, BucketHumanReview},
		{0.55, BucketHumanReview},
		{0.54, BucketSuppressed},
	}
	for _, tc := range cases {
		if got := BucketForConfidence(tc.conf); got != tc.want {
			t.Fatalf("BucketForConfidence(%v) = %s, want %s", tc.conf, got, tc.want)
		}
	}
}

func TestStatusTransitions(t *testing.T) {
	valid := []Status{
		StatusRunning,
		StatusFinalizing,
		StatusNeedsHumanReview,
	}
	for _, next := range valid {
		if !CanTransition(StatusRunning, next) {
			t.Fatalf("running should transition to %s", next)
		}
	}
	if CanTransition(StatusCompleted, StatusRunning) {
		t.Fatalf("completed task transitioned back to running")
	}
}

func TestSortFindingsStable(t *testing.T) {
	in := []Finding{
		{Severity: SeverityLow, File: "b.go", Line: 3, Category: CategoryErrors, RuleID: "z"},
		{Severity: SeverityCritical, File: "b.go", Line: 2, Category: CategorySecurity, RuleID: "a"},
		{Severity: SeverityCritical, File: "a.go", Line: 9, Category: CategorySecurity, RuleID: "b"},
	}
	SortFindings(in)
	if got := in[0].File; got != "a.go" {
		t.Fatalf("first file = %s, want a.go", got)
	}
	if got := in[1].Line; got != 2 {
		t.Fatalf("second line = %d, want 2", got)
	}
}
