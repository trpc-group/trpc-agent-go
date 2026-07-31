//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package review

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/domain"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/input"
)

func TestEngineEmitsAllSevenCategoriesWithLowNoise(t *testing.T) {
	diff := mustParse(t, allCategoryDiff())
	out := NewEngine(NewRedactor()).Review(diff)
	want := map[string]bool{
		domain.CategorySecurity:    false,
		domain.CategorySecrets:     false,
		domain.CategoryConcurrency: false,
		domain.CategoryResources:   false,
		domain.CategoryErrors:      false,
		domain.CategoryDatabase:    false,
		domain.CategoryTests:       false,
	}
	for _, f := range append(out.Findings, out.NeedsHumanReview...) {
		want[f.Category] = true
		if f.Line < 0 || f.File == "" || f.RuleID == "" {
			t.Fatalf("invalid finding: %+v", f)
		}
	}
	for cat, ok := range want {
		if !ok {
			t.Fatalf("missing category %s in %#v", cat, out)
		}
	}
}

func TestEngineAvoidsDocumentedSafeNegatives(t *testing.T) {
	diff := mustParse(t, "diff --git a/safe.go b/safe.go\n--- a/safe.go\n+++ b/safe.go\n@@ -1,1 +1,6 @@\n package safe\n+password := os.Getenv(\"PASSWORD\")\n+cmd := exec.Command(\"go\", \"test\", \"./...\")\n+defer rows.Close()\n+if err := do(); err != nil { return err }\n+go func(ctx context.Context) { <-ctx.Done() }(ctx)\ndiff --git a/safe_test.go b/safe_test.go\n--- a/safe_test.go\n+++ b/safe_test.go\n@@ -1,1 +1,2 @@\n package safe\n+func TestSafe(t *testing.T) {}\n")
	out := NewEngine(NewRedactor()).Review(diff)
	if len(out.Findings) != 0 || len(out.NeedsHumanReview) != 0 {
		t.Fatalf("safe negatives produced findings: %#v", out)
	}
}

func TestEngineCompactHoldoutScore(t *testing.T) {
	cases := []struct {
		name     string
		line     string
		category string
		ruleID   string
		wantHit  bool
	}{
		{name: "shell-positive", line: `func run(q string) { exec.Command("sh", "-c", "grep "+q) }`, category: domain.CategorySecurity, ruleID: "security.dynamic-input", wantHit: true},
		{name: "shell-safe-fixed-argv", line: `func run(q string) { exec.Command("grep", "--", q) }`, wantHit: false},
		{name: "sql-positive", line: `func q(db *sql.DB, id string) { db.Query("select * from u where id="+id) }`, category: domain.CategorySecurity, ruleID: "security.dynamic-input", wantHit: true},
		{name: "sql-safe-parameterized", line: `func q(db *sql.DB, id string) { db.Query("select * from u where id=?", id) }`, wantHit: false},
		{name: "resource-positive", line: `func f() { f, err := os.Open("x"); if err != nil { return }; _ = f }`, category: domain.CategoryResources, ruleID: "resources.close-missing", wantHit: true},
		{name: "resource-safe-close-binding", line: `func f() { f, err := os.Open("x"); if err != nil { return }; defer f.Close() }`, wantHit: false},
		{name: "short-if-safe-error", line: `func f() error { if err := do(); err != nil { return fmt.Errorf("do: %w", err) }; return nil }`, wantHit: false},
		{name: "ctx-positive", line: `func f(ctx context.Context) { go func() { work(context.Background()) }() }`, category: domain.CategoryConcurrency, ruleID: "concurrency.goroutine-lifetime", wantHit: true},
		{name: "ctx-safe-propagated", line: `func f(ctx context.Context) { go func(ctx context.Context) { select { case <-ctx.Done(): return } }(ctx) }`, wantHit: false},
		{name: "rows-positive", line: `func f(db *sql.DB) { rows, err := db.Query("select 1"); if err != nil { return }; _ = rows }`, category: domain.CategoryDatabase, ruleID: "database.rows-close-missing", wantHit: true},
		{name: "rows-safe-close-binding", line: `func f(db *sql.DB) { rows, err := db.Query("select 1"); if err != nil { return }; defer rows.Close() }`, wantHit: false},
	}
	var tp, fn, fp int
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := NewEngine(NewRedactor()).Review(mustParse(t, oneLineDiff(tc.line)))
			hit := false
			for _, f := range out.Findings {
				if f.Category == tc.category && f.RuleID == tc.ruleID && f.Confidence >= 0.80 {
					hit = true
				}
			}
			switch {
			case tc.wantHit && hit:
				tp++
			case tc.wantHit && !hit:
				fn++
			case !tc.wantHit && hit:
				fp++
			}
			if tc.wantHit && !hit {
				t.Fatalf("expected high-confidence %s/%s in %#v", tc.category, tc.ruleID, out)
			}
			if !tc.wantHit && hit {
				t.Fatalf("safe holdout produced false positive: %#v", out)
			}
		})
	}
	if tp < 5 || fn != 0 || fp != 0 {
		t.Fatalf("holdout score tp=%d fn=%d fp=%d", tp, fn, fp)
	}
}

func TestDedupeKeepsHighestSeverityAndConfidence(t *testing.T) {
	in := []domain.Finding{
		{Severity: domain.SeverityLow, Category: domain.CategorySecurity, File: "a.go", Line: 7, Confidence: 0.81, Source: "rule-a", RuleID: "a", Evidence: "a", Title: "a", Recommendation: "a"},
		{Severity: domain.SeverityHigh, Category: domain.CategorySecurity, File: "a.go", Line: 7, Confidence: 0.95, Source: "rule-b", RuleID: "b", Evidence: "b", Title: "b", Recommendation: "b"},
	}
	got := DedupeFindings(in)
	if len(got) != 1 {
		t.Fatalf("dedupe len = %d, want 1", len(got))
	}
	if got[0].Severity != domain.SeverityHigh || got[0].RuleID != "b" {
		t.Fatalf("wrong finding kept: %+v", got[0])
	}
}

func oneLineDiff(line string) string {
	return "diff --git a/app.go b/app.go\n--- a/app.go\n+++ b/app.go\n@@ -1,1 +1,3 @@\n package app\n+" + line + "\n"
}

func mustParse(t *testing.T, diff string) input.Diff {
	t.Helper()
	parsed, err := input.ParseUnifiedDiffString(diff, input.Limits{MaxBytes: 1 << 20, MaxLines: 1000})
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func allCategoryDiff() string {
	return "diff --git a/app.go b/app.go\n--- a/app.go\n+++ b/app.go\n@@ -1,2 +1,12 @@\n package app\n+const token = \"fixture-secret-value-github-token\"\n+func run(user string) { exec.Command(\"sh\", \"-c\", user) }\n+func leak(ctx context.Context) { go func() { work() }() }\n+func file() { f, _ := os.Open(\"x\"); _ = f }\n+func ignored() { result, _ := risky() ; _ = result }\n+func query(db *sql.DB, name string) { db.Query(\"select * from users where name=\" + name) }\n+func rows(db *sql.DB) { rows, _ := db.Query(\"select 1\"); _ = rows }\n"
}
