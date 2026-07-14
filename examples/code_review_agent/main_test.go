//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/store"
)

func TestFixtureReportsExpectedRules(t *testing.T) {
	expected := map[string][]string{
		"security_secret":        {"SEC001"},
		"goroutine_context_leak": {"GOR001"},
		"resource_not_closed":    {"RES001"},
		"db_lifecycle":           {"DB001"},
		"missing_test":           {"TEST001"},
		"duplicate_findings":     {"ERR001"},
		"redaction":              {"SEC001"},
	}
	outDir := filepath.Join(t.TempDir(), "out")
	dbPath := filepath.Join(t.TempDir(), "review.db")
	for fixture, wantRules := range expected {
		cfg := config{
			fixture:     fixture,
			outDir:      filepath.Join(outDir, fixture),
			dbPath:      dbPath,
			mode:        "rule-only",
			sandboxKind: "mock",
			dryRun:      true,
			timeout:     time.Second,
		}
		if err := run(context.Background(), cfg); err != nil {
			t.Fatalf("run fixture %s: %v", fixture, err)
		}
		report := readReport(t, filepath.Join(cfg.outDir, "review_report.json"))
		for _, ruleID := range wantRules {
			if !hasRule(report, ruleID) {
				t.Fatalf("fixture %s missing rule %s", fixture, ruleID)
			}
		}
		assertNoFixtureSecrets(t, filepath.Join(cfg.outDir, "review_report.json"))
		assertNoFixtureSecrets(t, filepath.Join(cfg.outDir, "review_report.md"))
	}
	db, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	report := readReport(t, filepath.Join(outDir, "security_secret", "review_report.json"))
	snapshot, err := db.GetTask(context.Background(), report.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Task.ID != report.Task.ID || len(snapshot.Findings) == 0 {
		t.Fatalf("bad snapshot: task=%q findings=%d", snapshot.Task.ID, len(snapshot.Findings))
	}
}

func TestFilesInputBuildsReview(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "secret.go")
	if err := os.WriteFile(src, []byte(`package demo

var apiKey = "sk-abcdefghijklmnopqrstuvwxyz123456"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config{
		files:       "secret.go",
		repoPath:    dir,
		outDir:      filepath.Join(dir, "out"),
		dbPath:      filepath.Join(dir, "review.db"),
		mode:        "rule-only",
		sandboxKind: "mock",
		dryRun:      true,
		timeout:     time.Second,
	}
	if err := run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	report := readReport(t, filepath.Join(cfg.outDir, "review_report.json"))
	if report.Task.InputType != review.InputTypeFiles {
		t.Fatalf("input type=%s", report.Task.InputType)
	}
	if !hasRule(report, "SEC001") {
		t.Fatal("files input did not detect SEC001")
	}
	assertNoFixtureSecrets(t, filepath.Join(cfg.outDir, "review_report.json"))
}

func readReport(t *testing.T, path string) review.ReviewReport {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report review.ReviewReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	return report
}

func hasRule(report review.ReviewReport, ruleID string) bool {
	for _, f := range report.Findings {
		if f.RuleID == ruleID {
			return true
		}
	}
	for _, f := range report.NeedsHumanReview {
		if f.RuleID == ruleID {
			return true
		}
	}
	for _, f := range report.Warnings {
		if f.RuleID == ruleID {
			return true
		}
	}
	return false
}

func assertNoFixtureSecrets(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	secrets := []string{
		"sk-abcdefghijklmnopqrstuvwxyz123456",
		"do-not-store-me",
		"ghp_abcdefghijklmnopqrstuvwxyz1234567890",
		"abcdefghijklmnopqrstuvwxyz1234567890",
	}
	for _, secret := range secrets {
		if strings.Contains(text, secret) {
			t.Fatalf("%s leaked %q", path, secret)
		}
	}
}
