//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import (
	"os"
	"path/filepath"
	"strings"
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
	base, err := exampleDir()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(base, "fixtures", "clean.diff"))
	if err != nil {
		t.Fatal(err)
	}
	input, err := ParseUnifiedDiff(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	findings, warnings, human := analyze(input)
	if len(findings)+len(warnings)+len(human) != 0 {
		t.Fatalf("unexpected observations: %+v %+v %+v", findings, warnings, human)
	}
}

func TestLooksSecretRequiresLiteralCredentialEvidence(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{`token := os.Getenv("TOKEN")`, false},
		{"Token string `json:\"token\"`", false},
		{`cfg.APIKey = lookup("API_KEY")`, false},
		{`const tokenHeader = "X-Token"`, false},
		{`const apiKeyEnv = "API_KEY"`, false},
		{`password := "correct horse battery staple"`, true},
		{`const token = "ghp_abcdefghijklmnopqrstuvwxyz123456"`, true},
	}
	for _, test := range cases {
		if got := looksSecret(test.line); got != test.want {
			t.Errorf("looksSecret(%q) = %t, want %t", test.line, got, test.want)
		}
	}
}

func TestCleanupInAnotherHunkDoesNotSuppressFinding(t *testing.T) {
	raw := "diff --git a/read.go b/read.go\n--- a/read.go\n+++ b/read.go\n@@ -1 +1,2 @@\n package read\n+f, err := os.Open(name)\n@@ -20 +21,2 @@\n func other() {}\n+defer other.Close()\n"
	input, err := ParseUnifiedDiff(raw)
	if err != nil {
		t.Fatal(err)
	}
	findings, _, _ := analyze(input)
	if !hasRule(findings, "go/resource/close") {
		t.Fatalf("unrelated hunk suppressed resource finding: %+v", findings)
	}
}

func TestCleanupMustMatchAcquiredVariable(t *testing.T) {
	raw := "diff --git a/read.go b/read.go\n--- a/read.go\n+++ b/read.go\n@@ -1 +1,4 @@\n package read\n+f, err := os.Open(name)\n+defer other.Close()\n+return err\n"
	input, err := ParseUnifiedDiff(raw)
	if err != nil {
		t.Fatal(err)
	}
	findings, _, _ := analyze(input)
	if !hasRule(findings, "go/resource/close") {
		t.Fatalf("unrelated variable suppressed resource finding: %+v", findings)
	}
}

func TestSkillRuleIDsControlEnabledRules(t *testing.T) {
	input := ParsedInput{Lines: []ChangedLine{{File: "a.go", Line: 1, Text: `password := "correct horse battery staple"`, Hunk: -1}}}
	findings, _, _, _ := analyzeWithRuleIDs(input, map[string]bool{"go/error/ignored": true})
	if hasRule(findings, "go/security/hardcoded-secret") {
		t.Fatal("rule absent from the loaded Skill was executed")
	}
}

func TestRedactsCommonCredentialFormats(t *testing.T) {
	values := []string{
		"AKIAABCDEFGHIJKLMNOP",
		"Authorization: Bearer abcdefghijklmnopqrstuvwxyz",
		"eyJabcdefghijk.abcdefghijkl.abcdefghijkl",
	}
	for _, value := range values {
		if got := redact(value); strings.Contains(got, value) || !strings.Contains(got, "[REDACTED]") {
			t.Errorf("credential was not redacted: %q", got)
		}
	}
}

func TestRuleAssociationHelpersRejectMissingNames(t *testing.T) {
	if assignedPrimaryName("value = call()") != "" || assignedSecondaryName("value = call()") != "" {
		t.Fatal("assignment helper invented a variable")
	}
	if callsMethodOrFunction("cancel()", "", "") {
		t.Fatal("empty variable name matched a call")
	}
	if !callsMethodOrFunction("defer tx.Rollback()", "tx", ".Rollback") {
		t.Fatal("qualified cleanup call was not matched")
	}
}

func TestPlausibleSecretLiteralFiltersLabels(t *testing.T) {
	for _, value := range []string{"short", "API_KEY", "authorization", "content-type", "api-key", "bearer"} {
		if plausibleSecretLiteral(value) {
			t.Errorf("label %q was treated as secret material", value)
		}
	}
	if !plausibleSecretLiteral("correct horse battery staple") {
		t.Fatal("credential-like literal was rejected")
	}
}

func TestContextFixtureDoesNotTreatDiscardedContextAsError(t *testing.T) {
	base, err := exampleDir()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(base, "fixtures", "context.diff"))
	if err != nil {
		t.Fatal(err)
	}
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

func TestMissingTestsMustBeInSameDirectoryAndNotDeleted(t *testing.T) {
	input := ParsedInput{
		Files:    []string{"pkg/service.go", "other/service_test.go", "pkg/old_test.go"},
		Statuses: map[string]FileStatus{"pkg/old_test.go": fileDeleted},
	}
	if got := missingTestFile(input); got != "pkg/service.go" {
		t.Fatalf("unexpected missing-test file %q", got)
	}
	input.Files = append(input.Files, "pkg/service_test.go")
	if got := missingTestFile(input); got != "" {
		t.Fatalf("same-package test was ignored: %q", got)
	}
}

func TestDynamicShellOnlyFlagsDynamicShellCode(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{`exec.Command("bash", "-c", userInput)`, true},
		{`exec.CommandContext(ctx, "sh", "-c", fmt.Sprintf("echo %s", value))`, true},
		{`exec.Command("bash", "-c", "echo safe")`, false},
		{`exec.Command("go", "test", "./"+pkg)`, false},
		{`fmt.Sprintf("bash -c %s", value)`, false},
		{`exec.Command("python", "-c", script)`, false},
		{`exec.Command("bash", script)`, false},
		{`exec.Command("bash", "-c")`, false},
		{`exec.Command("bash", "-c", "unterminated)`, true},
	}
	for _, test := range cases {
		if got := isDynamicShellCommand(test.line); got != test.want {
			t.Errorf("isDynamicShellCommand(%q) = %t, want %t", test.line, got, test.want)
		}
	}
}

func TestDynamicShellDetectsMultilineInvocation(t *testing.T) {
	raw := "diff --git a/run.go b/run.go\n--- a/run.go\n+++ b/run.go\n@@ -1 +1,6 @@\n package run\n+cmd := exec.CommandContext(\n+    ctx,\n+    \"bash\",\n+    \"-c\",\n+    userInput,\n+)\n"
	input, err := ParseUnifiedDiff(raw)
	if err != nil {
		t.Fatal(err)
	}
	findings, _, _ := analyze(input)
	if !hasRule(findings, "go/security/dynamic-shell") {
		t.Fatalf("multiline dynamic shell was missed: %+v", findings)
	}
}

func TestDynamicShellDoesNotTaintSafeInvocationInSameHunk(t *testing.T) {
	raw := "diff --git a/run.go b/run.go\n--- a/run.go\n+++ b/run.go\n@@ -1 +1,3 @@\n package run\n+safe := exec.Command(\"go\", \"test\", \"./...\")\n+unsafe := exec.Command(\"bash\", \"-c\", userInput)\n"
	input, err := ParseUnifiedDiff(raw)
	if err != nil {
		t.Fatal(err)
	}
	findings, _, _ := analyze(input)
	count, line := 0, 0
	for _, finding := range findings {
		if finding.RuleID == "go/security/dynamic-shell" {
			count++
			line = finding.Line
		}
	}
	if count != 1 || line != 3 {
		t.Fatalf("dynamic shell findings = %d at line %d: %+v", count, line, findings)
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
