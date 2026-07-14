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

	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/findings"
	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/pipeline"
	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/storage"
)

func TestPipelineAllFixtures(t *testing.T) {
	cases := []struct {
		fixture      string
		minFindings  int
		maxFindings  int
		mustRules    []string
		sandboxFail  bool
	}{
		{fixture: "01_clean", maxFindings: 0},
		{fixture: "02_security", minFindings: 2, mustRules: []string{"SEC-001", "SENS-001"}},
		{fixture: "03_goroutine_leak", minFindings: 1, maxFindings: 1, mustRules: []string{"CONC-001"}},
		{fixture: "04_resource_leak", minFindings: 1, maxFindings: 1, mustRules: []string{"RES-001"}},
		{fixture: "05_db_connection", minFindings: 1, maxFindings: 1, mustRules: []string{"DB-001"}},
		{fixture: "06_missing_test", minFindings: 1, maxFindings: 1, mustRules: []string{"TEST-001"}},
		{fixture: "07_duplicate_finding", minFindings: 1, maxFindings: 1, mustRules: []string{"SEC-001"}},
		{fixture: "08_sandbox_fail", minFindings: 1, maxFindings: 1, mustRules: []string{"ERR-001"}, sandboxFail: true},
	}

	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			dir := t.TempDir()
			result, err := pipeline.Run(context.Background(), pipeline.Options{
				Fixture:   tc.fixture,
				DryRun:    true,
				DBPath:    filepath.Join(dir, "reviews.db"),
				OutputDir: filepath.Join(dir, "output"),
			})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if _, err := os.Stat(result.JSONPath); err != nil {
				t.Fatalf("json report: %v", err)
			}
			if _, err := os.Stat(result.Markdown); err != nil {
				t.Fatalf("markdown report: %v", err)
			}

			store, err := storage.NewSQLiteStore("file:" + filepath.Join(dir, "reviews.db") + "?_busy_timeout=5000")
			if err != nil {
				t.Fatalf("open store: %v", err)
			}
			review, err := store.GetReview(context.Background(), result.TaskID)
			store.Close()
			if err != nil {
				t.Fatalf("GetReview: %v", err)
			}

			n := len(review.Findings)
			if n < tc.minFindings || (tc.maxFindings > 0 && n > tc.maxFindings) {
				t.Fatalf("findings = %d, want [%d,%d]", n, tc.minFindings, tc.maxFindings)
			}
			for _, rule := range tc.mustRules {
				if !hasRule(review.Findings, rule) {
					t.Fatalf("missing rule %s in %+v", rule, review.Findings)
				}
			}
			if tc.sandboxFail {
				if len(review.SandboxRuns) != 1 || review.SandboxRuns[0].Status != "failed" {
					t.Fatalf("sandbox runs = %+v", review.SandboxRuns)
				}
			}
		})
	}
}

func TestPipelineFakeModel(t *testing.T) {
	dir := t.TempDir()
	result, err := pipeline.Run(context.Background(), pipeline.Options{
		Fixture:   "01_clean",
		DryRun:    true,
		FakeModel: true,
		DBPath:    filepath.Join(dir, "reviews.db"),
		OutputDir: filepath.Join(dir, "output"),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	data, err := os.ReadFile(result.JSONPath)
	if err != nil {
		t.Fatalf("read json: %v", err)
	}
	var report findings.ReviewResult
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(report.Warnings) == 0 {
		t.Fatal("expected fake-model supplemental finding in warnings")
	}
	if report.Warnings[0].Source != "llm" {
		t.Fatalf("source = %q", report.Warnings[0].Source)
	}
}

func TestPipelineSecurityFixture(t *testing.T) {
	dir := t.TempDir()
	result, err := pipeline.Run(context.Background(), pipeline.Options{
		Fixture:   "02_security",
		DryRun:    true,
		DBPath:    filepath.Join(dir, "reviews.db"),
		OutputDir: filepath.Join(dir, "output"),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	jsonBytes, err := os.ReadFile(result.JSONPath)
	if err != nil {
		t.Fatalf("read json: %v", err)
	}
	content := string(jsonBytes)
	if strings.Contains(content, "sk-abcdefghijklmnopqrstuvwxyz123456") {
		t.Fatal("api key leaked into report")
	}
	if !strings.Contains(content, "SEC-001") || !strings.Contains(content, "SENS-001") {
		t.Fatalf("expected security findings in report: %s", content)
	}

	store, err := storage.NewSQLiteStore("file:" + filepath.Join(dir, "reviews.db") + "?_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	review, err := store.GetReview(context.Background(), result.TaskID)
	if err != nil {
		t.Fatalf("GetReview: %v", err)
	}
	if len(review.Findings) < 2 {
		t.Fatalf("stored findings = %d", len(review.Findings))
	}
}

func TestPipelineDuplicateFixture(t *testing.T) {
	dir := t.TempDir()
	result, err := pipeline.Run(context.Background(), pipeline.Options{
		Fixture:   "07_duplicate_finding",
		DryRun:    true,
		DBPath:    filepath.Join(dir, "reviews.db"),
		OutputDir: filepath.Join(dir, "output"),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	store, err := storage.NewSQLiteStore("file:" + filepath.Join(dir, "reviews.db") + "?_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	review, err := store.GetReview(context.Background(), result.TaskID)
	if err != nil {
		t.Fatalf("GetReview: %v", err)
	}
	if len(review.Findings) != 1 {
		t.Fatalf("deduped findings = %d, want 1", len(review.Findings))
	}
}

func TestPipelineSandboxFailFixture(t *testing.T) {
	dir := t.TempDir()
	result, err := pipeline.Run(context.Background(), pipeline.Options{
		Fixture:   "08_sandbox_fail",
		DryRun:    true,
		DBPath:    filepath.Join(dir, "reviews.db"),
		OutputDir: filepath.Join(dir, "output"),
	})
	if err != nil {
		t.Fatalf("Run should not fail when sandbox check fails: %v", err)
	}
	if result.TaskID == "" {
		t.Fatal("expected task id")
	}

	store, err := storage.NewSQLiteStore("file:" + filepath.Join(dir, "reviews.db") + "?_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	review, err := store.GetReview(context.Background(), result.TaskID)
	if err != nil {
		t.Fatalf("GetReview: %v", err)
	}
	if len(review.SandboxRuns) != 1 || review.SandboxRuns[0].Status != "failed" {
		t.Fatalf("sandbox runs = %+v", review.SandboxRuns)
	}
	if len(review.PermissionDecisions) < 2 {
		t.Fatalf("permission decisions = %d, want at least 2", len(review.PermissionDecisions))
	}
}

func hasRule(items []findings.Finding, ruleID string) bool {
	for _, f := range items {
		if f.RuleID == ruleID {
			return true
		}
	}
	return false
}
