//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package analysis

import (
	"fmt"
	"go/ast"
	"go/types"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/diffparse"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/reviewmodel"
)

func TestAnalyzeCoversAllRules(t *testing.T) {
	findings := Analyze(newFile(t, "review.go", `package review
import "strconv"
var key = "sk-abcdefghijklmnopqrstuvwxyz123456"
func risky(ctx context.Context, db *sql.DB) {
 exec.Command("sh", "-c", userInput)
 child, cancel := context.WithCancel(ctx)
 go func() { for { work() } }()
 file, err := os.Open("data")
 value, _ := strconv.Atoi("bad")
 tx, err := db.Begin()
 rows, err := db.Query("select 1")
 _ = child
 _ = file
}`))
	seen := make(map[string]bool)
	for _, finding := range findings {
		seen[finding.RuleID] = true
	}
	for _, id := range []string{"GO-SECRET-001", "GO-SEC-001", "GO-CTX-001", "GO-GOR-001", "GO-RES-001", "GO-ERR-001", "GO-DB-001", "GO-TEST-001"} {
		if !seen[id] {
			t.Fatalf("rule %s did not report; got %#v", id, findings)
		}
	}
}

func TestASTRejectsOnlyTypedErrorDiscardAndExactShell(t *testing.T) {
	files := newFile(t, "safe.go", `package safe
import "os/exec"
func safe(values map[string]int, value any) {
 _, ok := values["key"]
 _, _ = values["key"]
 _ = ok
 _ = value
 exec.Command("git", "-c", "safe.directory=*")
}
var _ = exec.Command
var _ = missing.Symbol`)
	for _, finding := range Analyze(files) {
		if finding.RuleID == "GO-ERR-001" || finding.RuleID == "GO-SEC-001" {
			t.Fatalf("benign blank or non-shell command reported: %#v", finding)
		}
	}
}

func TestASTRequiresTransactionRollbackFallback(t *testing.T) {
	files := newFile(t, "database.go", `package database
func update(db *sql.DB) error {
 tx, err := db.Begin()
 return tx.Commit()
}`)
	if countRule(Analyze(files), "GO-DB-001") == 0 {
		t.Fatal("transaction without rollback fallback was not reported")
	}
}

func TestAnalyzeConfiguredUsesSkillMetadataAndModes(t *testing.T) {
	files := newFile(t, "config.go", `package config
var key = "sk-abcdefghijklmnopqrstuvwxyz123456"`)
	config := RuleConfig{ID: "GO-SECRET-001", Category: "configured",
		Severity: "low", Confidence: 0.25, Modes: []string{"patch"}, Enabled: true}
	findings := AnalyzeConfigured(files, nil, []RuleConfig{config})
	if len(findings) != 1 || findings[0].Category != "configured" ||
		findings[0].Severity != "low" || findings[0].Confidence != 0.25 {
		t.Fatalf("configured findings = %#v", findings)
	}
	config.Modes = []string{"ast"}
	if findings := AnalyzeConfigured(files, nil, []RuleConfig{config}); len(findings) != 0 {
		t.Fatalf("disabled patch mode findings = %#v", findings)
	}
}

func TestCompositeDuplicateFindingScenarioDeduplicatesCandidates(t *testing.T) {
	files := newFile(t, "duplicate.go", `package duplicate
import "os/exec"
func run(input string) error {
 return exec.Command("sh", "-c", input).Run()
}`)
	candidates := Analyze(files)
	securityCandidates := 0
	for _, finding := range candidates {
		if finding.File == "duplicate.go" && finding.Category == "security" {
			securityCandidates++
		}
	}
	findings := Findings(candidates)
	if securityCandidates < 2 || countFinding(findings, "duplicate.go", "security") != 1 {
		t.Fatalf("candidates=%d normalized=%#v", securityCandidates, findings)
	}
}

func countFinding(findings []reviewmodel.Finding, file, category string) int {
	count := 0
	for _, finding := range findings {
		if finding.File == file && finding.Category == category {
			count++
		}
	}
	return count
}

func countRule(findings []reviewmodel.Finding, rule string) int {
	count := 0
	for _, finding := range findings {
		if finding.RuleID == rule {
			count++
		}
	}
	return count
}

func parsePatch(t *testing.T, patch []byte) []diffparse.ChangedFile {
	t.Helper()
	files, err := diffparse.Parse(patch)
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func newFile(t *testing.T, name, source string) []diffparse.ChangedFile {
	t.Helper()
	lines := strings.Count(source, "\n") + 1
	body := "+" + strings.ReplaceAll(source, "\n", "\n+")
	patch := fmt.Sprintf("diff --git a/%s b/%s\n--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n%s\n",
		name, name, name, lines, body)
	return parsePatch(t, []byte(patch))
}

func TestAnalyzeExclusions(t *testing.T) {
	patch := []byte(`diff --git a/safe.go b/safe.go
--- a/safe.go
+++ b/safe.go
@@ -1 +1,9 @@
 package safe
+func safe(ctx context.Context, db *sql.DB) {
+ child, cancel := context.WithCancel(ctx)
+ defer cancel()
+ file, err := os.Open("data")
+ defer file.Close()
+ tx, err := db.Begin()
+ defer tx.Rollback()
+ tx.Commit()
+}
diff --git a/safe_test.go b/safe_test.go
--- /dev/null
+++ b/safe_test.go
@@ -0,0 +1,2 @@
+package safe
+func TestSafe(t *testing.T) {}
`)
	for _, finding := range Analyze(parsePatch(t, patch)) {
		if finding.RuleID != "GO-ERR-001" {
			t.Fatalf("unexpected finding: %#v", finding)
		}
	}
	for _, test := range []struct{ name, source string }{
		{"generated.go", "// Code generated by tool. DO NOT EDIT.\npackage generated\nvar key = \"sk-abcdefghijklmnopqrstuvwxyz123456\""},
		{"vendor/pkg/file.go", "package pkg\nvar key = \"sk-abcdefghijklmnopqrstuvwxyz123456\""},
		{"comments.go", "package comments\n// example password=placeholder-secret-value\n// go func() { for { exec.Command(\"sh\", \"-c\", input) } }()"},
	} {
		if findings := Analyze(newFile(t, test.name, test.source)); len(findings) != 0 {
			t.Fatalf("ignored %s findings = %#v", test.name, findings)
		}
	}
	for _, source := range []string{"package p\nvar key = \"sk-abcdefghijklmnopqrstuvwxyz123456\" // dummy", "package p\nconst marker = \"Code generated DO NOT EDIT.\"\nvar key = \"sk-abcdefghijklmnopqrstuvwxyz123456\"", "package p\nvar dsn = \"postgres://admin:real-password@db.example/app\"", "package p\nvar password = \"real-secret-value\" // example", "package p\nvar password = \"testing-real-secret\"", "package p\nvar token = \"dummyproduction-secret\""} {
		if countRule(Analyze(newFile(t, "real.go", source)), "GO-SECRET-001") == 0 {
			t.Fatalf("strong credential suppressed: %q", source)
		}
	}
}

func TestPatchFallbackMatchers(t *testing.T) {
	tests := []struct {
		name, line, safe string
		match            func(addedLine, string) bool
	}{
		{"context", "ctx, cancel := context.WithTimeout(parent, timeout)", "cancel()", matchContextLeak},
		{"ticker", "ticker := time.NewTicker(time.Second)", "ticker.Stop()", matchContextLeak},
		{"resource", `file, err := os.Open("x")`, "file.Close()", matchResourceLeak},
		{"database", "tx, err := db.Begin()", "tx.Rollback()\ntx.Commit()", matchDatabaseLeak},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			line := addedLine{text: test.line}
			if !test.match(line, test.line) || test.match(line, test.line+"\n"+test.safe) ||
				test.match(addedLine{text: "value := 1"}, "value := 1") {
				t.Fatalf("fallback mismatch for %q", test.line)
			}
		})
	}
}

func TestASTLifecycleCallsDoNotCrossFunctions(t *testing.T) {
	findings := Analyze(newFile(t, "scoped.go", `package scoped
import "os"
func leaks() {
 file, _ := os.Open("leak")
 _ = file
}
func closes() {
 file, _ := os.Open("closed")
 defer file.Close()
}
func closesExplicitly() {
 file, _ := os.Open("closed-explicitly")
 file.Close()
}
func returnsClose() error {
 file, _ := os.Open("returned-close")
 return file.Close()
}
func assignsClose() {
 file, _ := os.Open("assigned-close")
 closeErr := file.Close()
 _ = closeErr
}
func initializesClose() {
 file, _ := os.Open("initialized-close")
 if err := file.Close(); err != nil { return }
}
func conditional(flag bool) {
 file, _ := os.Open("conditional")
 if flag {
  defer file.Close()
 }
}
func nested() {
 file, _ := os.Open("nested")
 func() {
  defer file.Close()
 }()
}`))
	if countFinding(findings, "scoped.go", "resource_lifecycle") != 3 {
		t.Fatalf("resource findings = %#v", findings)
	}
}

func TestFullAnalysisSourceAndAssignedTypeBranches(t *testing.T) {
	full := []byte("// license\n\npackage sample\nfunc value() {}\n")
	trimmed := fullAnalysisSource(full, map[int]string{1: "package sample"})
	if trimmed != "package sample\nfunc value() {}\n" {
		t.Fatalf("fullAnalysisSource() = %q", trimmed)
	}
	if got := fullAnalysisSource(full, map[int]string{1: "func value()"}); got != string(full) {
		t.Fatalf("non-package source changed: %q", got)
	}
	if got := fullAnalysisSource(full, map[int]string{1: "package missing"}); got != string(full) {
		t.Fatalf("missing package source changed: %q", got)
	}

	left, right := ast.NewIdent("left"), ast.NewIdent("right")
	assignment := &ast.AssignStmt{Lhs: []ast.Expr{left}, Rhs: []ast.Expr{right}}
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{
		right: {Type: types.Typ[types.Int]},
	}}
	if got := assignedType(info, assignment, 0); got != types.Typ[types.Int] {
		t.Fatalf("assignedType() = %v", got)
	}
	if assignedType(nil, assignment, 0) != nil || assignedType(info, assignment, 1) != nil {
		t.Fatal("assignedType accepted invalid input")
	}
}

func TestFindingsDedupBucketAndRedact(t *testing.T) {
	input := []reviewmodel.Finding{
		{Severity: "medium", Category: "security", File: "a/../a/x.go", Line: 4, Evidence: "password=top-secret-value", Confidence: 0.70, Source: "patch", RuleID: "B"},
		{Severity: "high", Category: "security", File: "a/x.go", Line: 4, Evidence: `run("sh", "-c")`, Confidence: 0.91, Source: "ast", RuleID: "A"},
		{Severity: "medium", Category: "missing_tests", File: "z.go", Line: 1, Confidence: 0.75, RuleID: "T"},
	}
	got := Findings(input)
	if len(got) != 2 {
		t.Fatalf("Findings() count = %d", len(got))
	}
	if got[0].Bucket != reviewmodel.BucketFindings || got[0].Confidence != 0.91 {
		t.Fatalf("merged finding = %#v", got[0])
	}
	if got[0].RuleID != "A,B" || got[0].Source != "ast,patch" {
		t.Fatalf("merged provenance = %q, %q", got[0].RuleID, got[0].Source)
	}
	if got[0].Evidence != `run("sh", "-c")` {
		t.Fatalf("merged evidence = %q", got[0].Evidence)
	}
	if strings.Contains(got[0].Evidence, "top-secret-value") {
		t.Fatal("secret remains in evidence")
	}
	if got[1].Bucket != reviewmodel.BucketWarnings {
		t.Fatalf("warning bucket = %q", got[1].Bucket)
	}
}

func TestFindingsHumanReviewNotPromoted(t *testing.T) {
	input := []reviewmodel.Finding{{Bucket: reviewmodel.BucketHumanReview, Severity: "high", Category: "goroutine_leak", File: "x.go", Line: 2, Confidence: 0.99}}
	if got := Findings(input); got[0].Bucket != reviewmodel.BucketHumanReview {
		t.Fatalf("bucket = %q", got[0].Bucket)
	}
}
