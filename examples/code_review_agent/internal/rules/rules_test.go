//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package rules

import (
	"os"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/diffparse"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

func TestEvaluateDetectsExpectedRules(t *testing.T) {
	cases := []struct {
		fixture string
		ruleID  string
		status  string
	}{
		{"security_secret.diff", "security.secret_leak", review.FindingStatusFinding},
		{"goroutine_context_leak.diff", "concurrency.goroutine_context_leak", review.FindingStatusNeedsHumanReview},
		{"resource_not_closed.diff", "resource.close_missing", review.FindingStatusFinding},
		{"db_lifecycle.diff", "db.lifecycle", review.FindingStatusFinding},
		{"missing_tests.diff", "test.missing_coverage", review.FindingStatusWarning},
	}
	for _, tt := range cases {
		t.Run(tt.fixture, func(t *testing.T) {
			findings := evaluateFixture(t, tt.fixture)
			finding, ok := findByRule(findings, tt.ruleID)
			if !ok {
				t.Fatalf("rule %s not found in %#v", tt.ruleID, findings)
			}
			if finding.Status != tt.status {
				t.Fatalf("Status = %q, want %q", finding.Status, tt.status)
			}
		})
	}
}

func TestEvaluateDeduplicatesFindings(t *testing.T) {
	findings := evaluateFixture(t, "duplicate_findings.diff")
	count := 0
	for _, finding := range findings {
		if finding.RuleID == "error.ignored_error" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("ignored error count = %d, want one per changed line", count)
	}

	duplicated := []review.Finding{findings[0], findings[0]}
	got := review.NormalizeFindings(duplicated, review.DefaultConfig())
	if len(got) != 1 {
		t.Fatalf("NormalizeFindings duplicate len = %d, want 1", len(got))
	}
}

func TestEvaluateRedactsFindingEvidence(t *testing.T) {
	findings := evaluateFixture(t, "secret_redaction.diff")
	for _, finding := range findings {
		if strings.Contains(finding.Evidence, "supersecretvalue") {
			t.Fatalf("finding leaked secret: %#v", finding)
		}
	}
	redaction, ok := findByRule(findings, "security.redaction_required")
	if !ok {
		t.Fatalf("redaction finding not found")
	}
	if !strings.Contains(redaction.Evidence, redact.Placeholder) {
		t.Fatalf("redaction evidence = %q, want placeholder", redaction.Evidence)
	}
}

func evaluateFixture(t *testing.T, name string) []review.Finding {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/fixtures/" + name)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	files, err := diffparse.Parse(string(raw))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	return Evaluate(files)
}

func findByRule(findings []review.Finding, ruleID string) (review.Finding, bool) {
	for _, finding := range findings {
		if finding.RuleID == ruleID {
			return finding, true
		}
	}
	return review.Finding{}, false
}
