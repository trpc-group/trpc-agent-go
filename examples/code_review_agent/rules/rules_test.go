//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package rules

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/diffparser"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
)

// TestScanSecretAndDedup verifies secret detection and duplicate collapsing.
func TestScanSecretAndDedup(t *testing.T) {
	diff := []byte(`diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -1,3 +1,5 @@
 package foo
 
+var apiKey = "sk-abcdefghijklmnopqrstuvwxyz123456"
+var apiKey = "sk-abcdefghijklmnopqrstuvwxyz123456"
`)
	files, err := diffparser.ParseUnifiedDiff(diff)
	if err != nil {
		t.Fatal(err)
	}
	result := Scan(files)
	if len(result.Findings) == 0 {
		t.Fatal("expected finding")
	}
	if result.Findings[0].RuleID != "SEC001" {
		t.Fatalf("rule=%s", result.Findings[0].RuleID)
	}
	if result.Findings[0].Evidence == `var apiKey = "sk-abcdefghijklmnopqrstuvwxyz123456"` {
		t.Fatal("evidence was not redacted")
	}
}

// TestDeduplicateKeepsHigherSeverity verifies dedup keeps the worst finding.
func TestDeduplicateKeepsHigherSeverity(t *testing.T) {
	in := []review.Finding{
		{File: "a.go", Line: 3, Category: "security", RuleID: "SEC001", Severity: review.SeverityLow, Confidence: 0.9},
		{File: "a.go", Line: 3, Category: "security", RuleID: "SEC001", Severity: review.SeverityCritical, Confidence: 0.8},
	}
	out := Deduplicate(in)
	if len(out) != 1 {
		t.Fatalf("dedup len=%d", len(out))
	}
	if out[0].Severity != review.SeverityCritical {
		t.Fatalf("severity=%s", out[0].Severity)
	}
}

// TestMergeReclassifiesModelFindings verifies model findings re-enter the buckets.
func TestMergeReclassifiesModelFindings(t *testing.T) {
	base := Result{
		Findings: []review.Finding{
			{File: "a.go", Line: 3, Category: "security", RuleID: "SEC001",
				Severity: review.SeverityCritical, Confidence: 0.96},
		},
	}
	extra := []review.Finding{
		// Duplicate of the rule finding with lower confidence: dropped.
		{File: "a.go", Line: 3, Category: "security", RuleID: "SEC001",
			Severity: review.SeverityCritical, Confidence: 0.7, Source: "llm"},
		// New model finding with mid confidence: goes to human review.
		{File: "a.go", Line: 9, Category: "model_review", RuleID: "LLM-CTX",
			Severity: review.SeverityMedium, Confidence: 0.55, Source: "llm"},
		// High-confidence model finding: promoted to findings.
		{File: "b.go", Line: 1, Category: "concurrency", RuleID: "LLM-CONC",
			Severity: review.SeverityHigh, Confidence: 0.9, Source: "llm"},
	}
	out := Merge(base, extra)
	if len(out.Findings) != 2 {
		t.Fatalf("findings=%d, want 2 (%+v)", len(out.Findings), out.Findings)
	}
	if len(out.NeedsHumanReview) != 1 || out.NeedsHumanReview[0].RuleID != "LLM-CTX" {
		t.Fatalf("needs_human_review=%+v", out.NeedsHumanReview)
	}
	for _, f := range out.Findings {
		if f.File == "a.go" && f.Confidence != 0.96 {
			t.Fatalf("dedup kept the wrong duplicate: %+v", f)
		}
	}
}

// TestFilterPipelineRecordsDecisions verifies every keep, demote, and drop is recorded.
func TestFilterPipelineRecordsDecisions(t *testing.T) {
	in := []review.Finding{
		// Winner of the dedup pair.
		{File: "a.go", Line: 3, Category: "security", RuleID: "SEC001",
			Severity: review.SeverityCritical, Confidence: 0.96, Source: "rule-only"},
		// Loser of the dedup pair: must produce a drop decision.
		{File: "a.go", Line: 3, Category: "security", RuleID: "SEC001",
			Severity: review.SeverityCritical, Confidence: 0.7, Source: "llm"},
		// Mid confidence: routed to human review.
		{File: "a.go", Line: 9, Category: "context", RuleID: "CTX001",
			Severity: review.SeverityMedium, Confidence: 0.55, Source: "rule-only"},
		// Low confidence: demoted to warning.
		{File: "b.go", Line: 1, Category: "testing", RuleID: "TEST001",
			Severity: review.SeverityMedium, Confidence: 0.3, Source: "rule-only"},
	}
	out := filterPipeline(in)
	if len(out.FilterDecisions) != 4 {
		t.Fatalf("decisions=%d, want 4: %+v", len(out.FilterDecisions), out.FilterDecisions)
	}
	counts := map[string]int{}
	for _, d := range out.FilterDecisions {
		counts[d.Decision]++
		if d.Reason == "" || d.Stage == "" || d.CreatedAt.IsZero() {
			t.Fatalf("incomplete decision: %+v", d)
		}
	}
	want := map[string]int{
		review.FilterDecisionDropDuplicate: 1,
		review.FilterDecisionKeep:          1,
		review.FilterDecisionHumanReview:   1,
		review.FilterDecisionWarning:       1,
	}
	for decision, n := range want {
		if counts[decision] != n {
			t.Fatalf("decision %s count=%d, want %d (%+v)", decision, counts[decision], n, counts)
		}
	}
	for _, d := range out.FilterDecisions {
		if d.Decision == review.FilterDecisionDropDuplicate {
			if d.Stage != review.FilterStageDedup || d.Source != "llm" {
				t.Fatalf("drop decision should target the losing duplicate: %+v", d)
			}
		} else if d.Stage != review.FilterStageConfidence {
			t.Fatalf("bucket decision has wrong stage: %+v", d)
		}
	}
}

// TestScanIdenticalDuplicatesKeepOneDecision verifies identical hits leave one decision.
func TestScanIdenticalDuplicatesKeepOneDecision(t *testing.T) {
	// Two byte-identical findings: one keep + one drop decision.
	in := []review.Finding{
		{File: "a.go", Line: 3, Category: "security", RuleID: "SEC001",
			Severity: review.SeverityCritical, Confidence: 0.96, Source: "rule-only"},
		{File: "a.go", Line: 3, Category: "security", RuleID: "SEC001",
			Severity: review.SeverityCritical, Confidence: 0.96, Source: "rule-only"},
	}
	out := filterPipeline(in)
	if len(out.Findings) != 1 {
		t.Fatalf("findings=%d, want 1", len(out.Findings))
	}
	drops, keeps := 0, 0
	for _, d := range out.FilterDecisions {
		switch d.Decision {
		case review.FilterDecisionDropDuplicate:
			drops++
		case review.FilterDecisionKeep:
			keeps++
		}
	}
	if drops != 1 || keeps != 1 {
		t.Fatalf("drops=%d keeps=%d, want 1/1 (%+v)", drops, keeps, out.FilterDecisions)
	}
}
