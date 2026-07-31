//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package rules

import (
	"os"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/cr_agent/internal/diff"
	"trpc.group/trpc-go/trpc-agent-go/examples/cr_agent/internal/types"
)

// makeDiff parses a raw diff string and returns the first FileChange.
func makeDiff(t *testing.T, diffText string) diff.FileChange {
	t.Helper()
	changes, err := diff.Parse(diffText)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(changes) == 0 {
		t.Fatal("expected at least 1 file change, got 0")
	}
	return changes[0]
}

// findingByID returns the first finding with the given rule ID, or nil.
func findingByID(findings []types.Finding, ruleID string) *types.Finding {
	for i := range findings {
		if findings[i].RuleID == ruleID {
			return &findings[i]
		}
	}
	return nil
}

// --- SEC-001: SQL Injection ---

func TestSQLInjection(t *testing.T) {
	d := makeDiff(t, `diff --git a/q.go b/q.go
--- a/q.go
+++ b/q.go
@@ -1,3 +1,3 @@
 func query() {
-	q := "SELECT * FROM t WHERE id = ?"
+	q := "SELECT * FROM t WHERE id = " + id
 }
`)
	r := &sqlInjectionRule{}
	findings := r.Evaluate(&d)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	f := findings[0]
	if f.Severity != types.SeverityCritical {
		t.Errorf("severity = %s, want critical", f.Severity)
	}
	if f.RuleID != "SEC-001" {
		t.Errorf("ruleID = %s, want SEC-001", f.RuleID)
	}
}

func TestSQLInjectionSafe(t *testing.T) {
	d := makeDiff(t, `diff --git a/q.go b/q.go
--- a/q.go
+++ b/q.go
@@ -1,3 +1,3 @@
 func query() {
-	q := "SELECT * FROM t"
+	q := "SELECT * FROM t WHERE id = ?"
 }
`)
	r := &sqlInjectionRule{}
	findings := r.Evaluate(&d)
	if len(findings) != 0 {
		t.Errorf("expected 0 findings for safe query, got %d", len(findings))
	}
}

// --- SEC-002: Hardcoded Secret ---

func TestHardcodedSecret(t *testing.T) {
	d := makeDiff(t, `diff --git a/auth.go b/auth.go
--- a/auth.go
+++ b/auth.go
@@ -1,3 +1,6 @@
 package auth
+const apiKey = "sk-live-9f8a7b6c5d4e3f2a1b0c9d8e7f6a5b4c"
+const password = "supersecret123"
+
`)
	r := &hardcodedSecretRule{}
	findings := r.Evaluate(&d)
	if len(findings) < 2 {
		t.Fatalf("expected at least 2 findings, got %d", len(findings))
	}
}

// --- GOR-001: Goroutine no exit ---

func TestGoroutineNoExit(t *testing.T) {
	d := makeDiff(t, `diff --git a/worker.go b/worker.go
--- a/worker.go
+++ b/worker.go
@@ -10,3 +10,12 @@ func start() {
 	for i := 0; i < n; i++ {
+		go func() {
+			for {
+				doWork()
+			}
+		}()
 	}
 }
`)
	r := &goroutineNoExitRule{}
	findings := r.Evaluate(&d)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	if findings[0].RuleID != "GOR-001" {
		t.Errorf("ruleID = %s, want GOR-001", findings[0].RuleID)
	}
}

func TestGoroutineWithExit(t *testing.T) {
	d := makeDiff(t, `diff --git a/worker.go b/worker.go
--- a/worker.go
+++ b/worker.go
@@ -10,3 +10,12 @@ func start() {
 	for i := 0; i < n; i++ {
+		go func() {
+			for {
+				select {
+				case <-ctx.Done():
+					return
+				}
+			}
+		}()
 	}
 }
`)
	r := &goroutineNoExitRule{}
	findings := r.Evaluate(&d)
	if len(findings) != 0 {
		t.Errorf("expected 0 findings for goroutine with ctx.Done, got %d", len(findings))
	}
}

// --- RES-001: Missing defer Close ---

func TestMissingDeferClose(t *testing.T) {
	d := makeDiff(t, `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -5,3 +5,8 @@ import (
 func read() {
+	f, err := os.Open(path)
+	if err != nil {
+		return err
+	}
+	data := io.ReadAll(f)
 }
`)
	r := &missingDeferCloseRule{}
	findings := r.Evaluate(&d)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	if findings[0].RuleID != "RES-001" {
		t.Errorf("ruleID = %s, want RES-001", findings[0].RuleID)
	}
}

func TestDeferClosePresent(t *testing.T) {
	d := makeDiff(t, `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -5,3 +5,8 @@ import (
 func read() {
+	f, err := os.Open(path)
+	if err != nil {
+		return err
+	}
+	defer f.Close()
 }
`)
	r := &missingDeferCloseRule{}
	findings := r.Evaluate(&d)
	if len(findings) != 0 {
		t.Errorf("expected 0 findings when defer Close present, got %d", len(findings))
	}
}

// --- RES-001: Removed defer Close ---

func TestRemovedDeferClose(t *testing.T) {
	d := makeDiff(t, `diff --git a/file.go b/file.go
--- a/file.go
+++ b/file.go
@@ -5,5 +5,3 @@ import (
 	f, err := os.Open(path)
 	if err != nil {
 		return err
 	}
-	defer f.Close()
`)
	r := &missingDeferCloseRule{}
	findings := r.Evaluate(&d)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding for removed defer Close, got %d", len(findings))
	}
	if !strings.Contains(findings[0].Title, "removed") {
		t.Errorf("title = %s, want 'removed'", findings[0].Title)
	}
}

// --- ERR-001: Ignored error ---

func TestIgnoredError(t *testing.T) {
	d := makeDiff(t, `diff --git a/cfg.go b/cfg.go
--- a/cfg.go
+++ b/cfg.go
@@ -10,3 +10,5 @@ func load() {
+	ValidateConfig(cfg)
+	return nil
 }
`)
	r := &ignoredErrorRule{}
	findings := r.Evaluate(&d)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	if findings[0].RuleID != "ERR-001" {
		t.Errorf("ruleID = %s, want ERR-001", findings[0].RuleID)
	}
}

// --- DB-001: Transaction without Rollback ---

func TestTransactionWithoutRollback(t *testing.T) {
	d := makeDiff(t, `diff --git a/tx.go b/tx.go
--- a/tx.go
+++ b/tx.go
@@ -10,3 +10,10 @@ import (
 func transfer() {
+	tx, err := db.Begin()
+	if err != nil {
+		return err
+	}
+	// no rollback here
+	tx.Exec("UPDATE accounts SET bal = bal - 1 WHERE id = 1")
+	return tx.Commit()
 }
`)
	r := &transactionNotRolledBackRule{}
	findings := r.Evaluate(&d)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	if findings[0].RuleID != "DB-001" {
		t.Errorf("ruleID = %s, want DB-001", findings[0].RuleID)
	}
}

func TestTransactionWithRollback(t *testing.T) {
	d := makeDiff(t, `diff --git a/tx.go b/tx.go
--- a/tx.go
+++ b/tx.go
@@ -10,3 +10,10 @@ import (
 func transfer() {
+	tx, err := db.Begin()
+	if err != nil {
+		return err
+	}
+	defer tx.Rollback()
+	tx.Exec("UPDATE accounts SET bal = bal - 1 WHERE id = 1")
+	return tx.Commit()
 }
`)
	r := &transactionNotRolledBackRule{}
	findings := r.Evaluate(&d)
	if len(findings) != 0 {
		t.Errorf("expected 0 findings when defer Rollback present, got %d", len(findings))
	}
}

// TestDB001CommentDoesNotCount ensures a comment mentioning
// "defer Rollback" does not satisfy the safety check.
func TestDB001CommentDoesNotCount(t *testing.T) {
	d := makeDiff(t, `diff --git a/tx.go b/tx.go
--- a/tx.go
+++ b/tx.go
@@ -10,3 +10,8 @@ import (
 func transfer() {
+	tx, err := db.Begin()
+	if err != nil {
+		return err
+	}
+	// Note: no defer tx.Rollback() here on purpose
 }
`)
	r := &transactionNotRolledBackRule{}
	findings := r.Evaluate(&d)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding (comment should not count), got %d", len(findings))
	}
}

// --- Registry ---

func TestRegistryLoadsAllRules(t *testing.T) {
	reg := NewRegistry()
	rules := reg.Rules()
	if len(rules) < 12 {
		t.Errorf("expected at least 12 rules, got %d", len(rules))
	}
	expected := []string{
		"SEC-001", "SEC-002", "SEC-003",
		"GOR-001", "GOR-002", "GOR-003",
		"RES-001", "RES-002",
		"ERR-001", "ERR-002",
		"TEST-001",
		"SENS-001",
		"DB-001",
	}
	for _, id := range expected {
		if reg.Get(id) == nil {
			t.Errorf("rule %s not found in registry", id)
		}
	}
}

// --- End-to-end fixture test ---

func TestFixtureAllRules(t *testing.T) {
	data, err := os.ReadFile("../../fixtures/sample_diff.patch")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	changes, err := diff.Parse(string(data))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	reg := NewRegistry()
	expectedRules := map[string]bool{
		"SEC-001": true,
		"GOR-001": true,
		"RES-001": true,
		"ERR-001": true,
		"SEC-002": true,
		"DB-001":  true,
	}
	var got []types.Finding
	for i := range changes {
		fc := changes[i]
		for _, rule := range reg.Rules() {
			got = append(got, rule.Evaluate(&fc)...)
		}
	}
	for ruleID := range expectedRules {
		if findingByID(got, ruleID) == nil {
			t.Errorf("expected finding for %s but got none", ruleID)
		}
	}
}
