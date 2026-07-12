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
	"os"
	"path/filepath"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/diff"
	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/findings"
)

func loadFixture(t *testing.T, name string) *diff.Diff {
	t.Helper()
	path := filepath.Join("..", "..", "fixtures", name+".diff")
	d, err := diff.LoadFromFile(path)
	if err != nil {
		t.Fatalf("load fixture %s: %v", name, err)
	}
	return d
}

func TestAnalyzeSecurityFixture(t *testing.T) {
	got := Analyze(loadFixture(t, "02_security"))
	if len(got) < 2 {
		t.Fatalf("findings = %d, want at least 2", len(got))
	}
	rules := map[string]bool{}
	for _, f := range got {
		rules[f.RuleID] = true
	}
	if !rules["SEC-001"] || !rules["SENS-001"] {
		t.Fatalf("expected SEC-001 and SENS-001, got %+v", got)
	}
}

func TestAnalyzeCleanFixture(t *testing.T) {
	got := Analyze(loadFixture(t, "01_clean"))
	if len(got) != 0 {
		t.Fatalf("findings = %+v, want 0", got)
	}
}

func TestAnalyzeGoroutineFixture(t *testing.T) {
	got := Analyze(loadFixture(t, "03_goroutine_leak"))
	if len(got) != 1 || got[0].RuleID != "CONC-001" {
		t.Fatalf("findings = %+v", got)
	}
}

func TestAnalyzeResourceFixture(t *testing.T) {
	got := Analyze(loadFixture(t, "04_resource_leak"))
	if len(got) != 1 || got[0].RuleID != "RES-001" {
		t.Fatalf("findings = %+v", got)
	}
}

func TestAnalyzeDBFixture(t *testing.T) {
	got := Analyze(loadFixture(t, "05_db_connection"))
	if len(got) != 1 || got[0].RuleID != "DB-001" {
		t.Fatalf("findings = %+v", got)
	}
}

func TestAnalyzeMissingTestFixture(t *testing.T) {
	got := Analyze(loadFixture(t, "06_missing_test"))
	if len(got) != 1 || got[0].RuleID != "TEST-001" {
		t.Fatalf("findings = %+v", got)
	}
}

func TestAnalyzeDuplicateDedup(t *testing.T) {
	raw := Analyze(loadFixture(t, "07_duplicate_finding"))
	if len(raw) < 2 {
		t.Fatalf("raw findings = %d, want at least 2 before dedup", len(raw))
	}
	got := findings.Dedup(raw)
	if len(got) != 1 {
		t.Fatalf("dedup findings = %+v, want 1", got)
	}
	if got[0].Category != "security" {
		t.Fatalf("category = %q", got[0].Category)
	}
}

func TestAnalyzeSandboxFailFixture(t *testing.T) {
	got := Analyze(loadFixture(t, "08_sandbox_fail"))
	if len(got) != 1 || got[0].RuleID != "ERR-001" {
		t.Fatalf("findings = %+v", got)
	}
}

func TestMatchSecuritySQLNegative(t *testing.T) {
	_, ok := matchSecuritySQL("a.go", 1, `db.Query("SELECT id FROM users WHERE id = ?", id)`)
	if ok {
		t.Fatal("expected no match for parameterized query")
	}
}

func TestMatchSensitiveNegative(t *testing.T) {
	_, ok := matchSensitiveData("a.go", 1, `log.Printf("login failed")`)
	if ok {
		t.Fatal("expected no sensitive match")
	}
}

func TestMain(m *testing.M) {
	if wd, err := os.Getwd(); err == nil {
		_ = wd
	}
	os.Exit(m.Run())
}
