//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFixtureRules(t *testing.T) {
	base, err := exampleDir()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ fixture, rule string }{
		{"secret", "go/security/hardcoded-secret"},
		{"goroutine", "go/concurrency/unbounded-goroutine"},
		{"context", "go/context/cancel-leak"},
		{"resource", "go/resource/close"},
		{"database", "go/database/transaction-rollback"},
		{"errors", "go/error/ignored"},
		{"sql_injection", "go/database/sql-concatenation"},
	}
	for _, test := range cases {
		t.Run(test.fixture, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(base, "fixtures", test.fixture+".diff"))
			if err != nil {
				t.Fatal(err)
			}
			input, err := ParseUnifiedDiff(string(raw))
			if err != nil {
				t.Fatal(err)
			}
			findings, _, _ := analyze(input)
			if !hasRule(findings, test.rule) {
				t.Fatalf("rule %s missing from %+v", test.rule, findings)
			}
		})
	}
}

func TestCleanFixtureHasNoObservation(t *testing.T) {
	base, _ := exampleDir()
	raw, _ := os.ReadFile(filepath.Join(base, "fixtures", "clean.diff"))
	input, err := ParseUnifiedDiff(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	findings, warnings, human := analyze(input)
	if len(findings)+len(warnings)+len(human) != 0 {
		t.Fatalf("unexpected observations: %+v %+v %+v", findings, warnings, human)
	}
}

func TestContextFixtureDoesNotTreatDiscardedContextAsError(t *testing.T) {
	base, _ := exampleDir()
	raw, _ := os.ReadFile(filepath.Join(base, "fixtures", "context.diff"))
	input, err := ParseUnifiedDiff(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	findings, _, _ := analyze(input)
	if len(findings) != 1 || findings[0].RuleID != "go/context/cancel-leak" {
		t.Fatalf("unexpected findings: %+v", findings)
	}
}

func TestUnchangedHunkContextCanProveResourceClose(t *testing.T) {
	raw := "diff --git a/read.go b/read.go\n--- a/read.go\n+++ b/read.go\n@@ -1,3 +1,4 @@\n package read\n+f, err := os.Open(name)\n defer f.Close()\n return err\n"
	input, err := ParseUnifiedDiff(raw)
	if err != nil {
		t.Fatal(err)
	}
	findings, _, _ := analyze(input)
	if hasRule(findings, "go/resource/close") {
		t.Fatalf("resource close context was ignored: %+v", findings)
	}
}

func TestDedupeKeepsHighestConfidencePerLocationAndCategory(t *testing.T) {
	values := []Finding{
		fingerprint(Finding{File: "a.go", Line: 7, Category: "security", RuleID: "one", Confidence: .7}),
		fingerprint(Finding{File: "a.go", Line: 7, Category: "security", RuleID: "two", Confidence: .9}),
	}
	got := dedupe(values)
	if len(got) != 1 || got[0].RuleID != "two" {
		t.Fatalf("unexpected dedupe: %+v", got)
	}
	decisions := filterDecisions(values, "finding", FilterKeep)
	if len(decisions) != 2 || decisions[0].Action != FilterDropDuplicate || decisions[1].Action != FilterKeep {
		t.Fatalf("dedupe decisions are not auditable: %+v", decisions)
	}
}

func TestMissingTestRoutesToHumanReview(t *testing.T) {
	input := ParsedInput{Files: []string{"service.go"}}
	_, _, human, decisions := analyzeWithDecisions(input)
	if !hasRule(human, "go/test/missing-change") {
		t.Fatalf("missing test observation: %+v", human)
	}
	if len(decisions) != 1 || decisions[0].Action != FilterRouteHuman {
		t.Fatalf("human-review routing is not auditable: %+v", decisions)
	}
}

func hasRule(values []Finding, ruleID string) bool {
	for _, value := range values {
		if value.RuleID == ruleID {
			return true
		}
	}
	return false
}
