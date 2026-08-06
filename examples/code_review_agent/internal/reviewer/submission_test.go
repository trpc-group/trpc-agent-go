//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package reviewer

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/persistence"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/store"
)

func TestCanonicalizeReviewSubmissionMergesRuleLocationDuplicates(t *testing.T) {
	submission, err := canonicalizeReviewSubmission([]store.ReviewResultRecord{
		{
			ResultKind: "finding", Severity: "high", Category: "correctness",
			File: "./internal/reviewer/tool.go", Line: 17,
			Title: "First title", Evidence: "first evidence",
			Recommendation: "first fix", Confidence: 0.80,
			Source: "agent", RuleID: "GO-COR-001",
		},
		{
			ResultKind: " FINDING ", Severity: " CRITICAL ", Category: " CORRECTNESS ",
			File: `internal\reviewer\tool.go`, Line: 17,
			Title: "Stronger title", Evidence: "stronger evidence",
			Recommendation: "stronger fix", Confidence: 0.95,
			Source: " SKILL ", RuleID: " GO-COR-001 ",
		},
		{
			ResultKind: "finding", Severity: "high", Category: "correctness",
			File: "internal/reviewer/tool.go", Line: 17,
			Title: "Repeated observation", Evidence: "first evidence",
			Recommendation: "repeated fix", Confidence: 0.90,
			Source: "agent", RuleID: "GO-COR-001",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(submission.Results) != 1 {
		t.Fatalf("canonical results = %#v, want one result", submission.Results)
	}
	got := submission.Results[0]
	if got.Severity != "critical" || got.Title != "Stronger title" ||
		got.Recommendation != "stronger fix" || got.Confidence != 0.95 ||
		got.Source != "skill" {
		t.Fatalf("representative result = %#v, want the stronger complete record", got)
	}
	if got.File != "internal/reviewer/tool.go" || got.RuleID != "GO-COR-001" {
		t.Fatalf("normalized identity = %q/%q", got.File, got.RuleID)
	}
	if got.Evidence != "first evidence Supporting evidence: stronger evidence" {
		t.Fatalf("merged evidence = %q, want unique evidence in first-seen order", got.Evidence)
	}
}

func TestCanonicalizeReviewSubmissionUsesConfidenceAfterEqualSeverity(t *testing.T) {
	base := validSubmissionResult()
	base.Confidence = 0.80
	strongerConfidence := base
	strongerConfidence.Title = "Higher-confidence title"
	strongerConfidence.Evidence = "higher-confidence evidence"
	strongerConfidence.Recommendation = "higher-confidence fix"
	strongerConfidence.Confidence = 0.95

	submission, err := canonicalizeReviewSubmission([]store.ReviewResultRecord{
		base,
		strongerConfidence,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(submission.Results) != 1 ||
		submission.Results[0].Title != strongerConfidence.Title ||
		submission.Results[0].Recommendation != strongerConfidence.Recommendation ||
		submission.Results[0].Confidence != strongerConfidence.Confidence {
		t.Fatalf(
			"representative result = %#v, want the complete higher-confidence record",
			submission.Results,
		)
	}
}

func TestCanonicalizeReviewSubmissionKeepsDistinctRulesLinesAndRuleIDCase(t *testing.T) {
	base := validSubmissionResult()
	otherRule := base
	otherRule.RuleID = "GO-COR-002"
	otherCase := base
	otherCase.RuleID = "go-cor-001"
	otherLine := base
	otherLine.Line = 18

	submission, err := canonicalizeReviewSubmission([]store.ReviewResultRecord{
		base,
		otherRule,
		otherCase,
		otherLine,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(submission.Results) != 4 {
		t.Fatalf(
			"canonical results = %#v, want distinct rules, lines, and case-sensitive rule IDs",
			submission.Results,
		)
	}
}

func TestCanonicalizeReviewSubmissionTreatsRuleIDAsOpaque(t *testing.T) {
	tests := []struct {
		name     string
		source   string
		category string
		ruleID   string
	}{
		{
			name:   "catalog rule from direct reasoning",
			source: "agent", category: "resource_lifecycle",
			ruleID: "GO-RES-001",
		},
		{
			name:   "agent-defined issue class",
			source: "agent", category: "resource_lifecycle",
			ruleID: "AGENT-RESOURCE-LIFECYCLE-UNRELEASED-CUSTOM-LEASE",
		},
		{
			name:   "non-catalog identity from another source",
			source: "skill", category: "correctness",
			ruleID: "TEAM-STABLE-RULE-42",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := validSubmissionResult()
			result.Source = test.source
			result.Category = test.category
			result.RuleID = test.ruleID
			submission, err := canonicalizeReviewSubmission(
				[]store.ReviewResultRecord{result},
			)
			if err != nil {
				t.Fatal(err)
			}
			if len(submission.Results) != 1 ||
				submission.Results[0].RuleID != test.ruleID {
				t.Fatalf("canonical results = %#v", submission.Results)
			}
		})
	}
}

func TestCanonicalizeReviewToolSubmissionReportsBothConflictOrigins(t *testing.T) {
	base := validReviewResultInput()
	categoryConflict := base
	categoryConflict.Category = "security"
	kindConflict := base

	tests := []struct {
		name      string
		input     submitReviewResultsInput
		wantError string
	}{
		{
			name: "category",
			input: submitReviewResultsInput{
				Findings: []reviewResultInput{base, categoryConflict},
			},
			wantError: `review result category conflict for internal/reviewer/tool.go:17 rule "GO-COR-001": finding[0] uses "correctness", finding[1] uses "security"`,
		},
		{
			name: "result kind",
			input: submitReviewResultsInput{
				Findings: []reviewResultInput{base},
				Warnings: []reviewResultInput{kindConflict},
			},
			wantError: `review result kind conflict for internal/reviewer/tool.go:17 rule "GO-COR-001": finding[0] uses "finding", warning[0] uses "warning"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := canonicalizeReviewToolSubmission(test.input)
			if err == nil || err.Error() != test.wantError {
				t.Fatalf("canonicalize error = %q, want %q", err, test.wantError)
			}
		})
	}
}

func TestCanonicalizeReviewSubmissionHandlesUnknownLineLiterally(t *testing.T) {
	base := validSubmissionResult()
	base.Line = 0
	duplicate := base
	duplicate.Title = " " + duplicate.Title + " "
	duplicate.Evidence = "\n" + duplicate.Evidence + "\n"
	duplicate.Recommendation = "\t" + duplicate.Recommendation

	submission, err := canonicalizeReviewSubmission([]store.ReviewResultRecord{
		base,
		duplicate,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(submission.Results) != 1 {
		t.Fatalf("literal line-zero duplicates = %#v, want one", submission.Results)
	}

	differentEvidence := base
	differentEvidence.Evidence = "different evidence"
	submission, err = canonicalizeReviewSubmission([]store.ReviewResultRecord{
		base,
		differentEvidence,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(submission.Results) != 2 {
		t.Fatalf("distinct line-zero results = %#v, want two", submission.Results)
	}

	differentKind := base
	differentKind.ResultKind = "warning"
	differentCategory := base
	differentCategory.Category = "security"
	submission, err = canonicalizeReviewSubmission([]store.ReviewResultRecord{
		base,
		differentKind,
		differentCategory,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(submission.Results) != 3 {
		t.Fatalf(
			"line-zero records that differ by result kind or category = %#v, want three",
			submission.Results,
		)
	}
}

func TestCanonicalizeReviewSubmissionRejectsNegativeLine(t *testing.T) {
	result := validSubmissionResult()
	result.Line = -1
	_, err := canonicalizeReviewSubmission([]store.ReviewResultRecord{result})
	if err == nil || !strings.Contains(err.Error(), "non-negative line") {
		t.Fatalf("canonicalize error = %v, want negative-line rejection", err)
	}
}

func TestSubmitReviewResultsReturnsCommittedCanonicalCounts(t *testing.T) {
	ctx := context.Background()
	resources, err := persistence.Open(
		ctx,
		filepath.Join(t.TempDir(), "review.db"),
		redact.AppendEventHook(redact.New()),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer resources.Close()

	const taskID = "canonical-tool-submission"
	if err := resources.ReviewStore.SaveTask(ctx, store.ReviewTaskRecord{
		TaskID: taskID, AppName: codeReviewAgentName, UserID: "reviewer", InputKind: "fixture",
	}); err != nil {
		t.Fatal(err)
	}
	result := reviewResultInput{
		Severity: "high", Category: "correctness",
		File: "internal/reviewer/tool.go", Line: 17,
		Title: "Changed behavior is wrong", Evidence: "first evidence",
		Recommendation: "restore the established return value", Confidence: 0.90,
		Source: "agent", RuleID: "GO-COR-001",
	}
	duplicate := result
	duplicate.Severity = "critical"
	duplicate.Title = "Stronger title"
	duplicate.Evidence = "supporting evidence"
	duplicate.Confidence = 0.95

	output, err := submitReviewResults(
		reviewInvocationContext(ctx, taskID),
		newReviewRecorder(resources.ReviewStore, redact.New()),
		submitReviewResultsInput{
			Findings:   []reviewResultInput{result, duplicate},
			Warnings:   []reviewResultInput{},
			Conclusion: "One canonical result.",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if output.Status != "accepted" ||
		output.FindingCount != 1 ||
		output.WarningCount != 0 ||
		output.HumanReviewCount != 0 {
		t.Fatalf("tool output = %#v, want one committed finding", output)
	}
	snapshot, err := resources.ReviewStore.LoadTaskSnapshot(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Results) != 1 ||
		snapshot.Results[0].Severity != "critical" ||
		!strings.Contains(snapshot.Results[0].Evidence, "first evidence") ||
		!strings.Contains(snapshot.Results[0].Evidence, "supporting evidence") {
		t.Fatalf("committed canonical results = %#v", snapshot.Results)
	}
}

func validSubmissionResult() store.ReviewResultRecord {
	return store.ReviewResultRecord{
		ResultKind: "finding", Severity: "high", Category: "correctness",
		File: "internal/reviewer/tool.go", Line: 17,
		Title: "Changed behavior is wrong", Evidence: "the changed return value violates the contract",
		Recommendation: "restore the established return value", Confidence: 0.90,
		Source: "agent", RuleID: "GO-COR-001",
	}
}

func validReviewResultInput() reviewResultInput {
	return reviewResultInput{
		Severity: "high", Category: "correctness",
		File: "internal/reviewer/tool.go", Line: 17,
		Title: "Changed behavior is wrong", Evidence: "the changed return value violates the contract",
		Recommendation: "restore the established return value", Confidence: 0.90,
		Source: "agent", RuleID: "GO-COR-001",
	}
}
