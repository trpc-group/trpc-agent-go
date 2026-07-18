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
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDryRunCompletesReportsAndPersistence(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "reviews.sqlite")
	report, paths, err := Run(context.Background(), Config{
		Fixture: "secret", OutputDir: dir, DatabasePath: dbPath,
		DryRun: true, FakeModel: true, Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Task.Status != "completed" || len(report.Findings) == 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if report.Mode != "deterministic-rule-only+fake-model" {
		t.Fatalf("unexpected mode: %s", report.Mode)
	}
	for _, path := range []string{paths.JSON, paths.Markdown, dbPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing %s: %v", path, err)
		}
	}
	store, err := openStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	stored, err := store.Load(context.Background(), report.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Task.ID != report.Task.ID || len(stored.PermissionDecisions) == 0 || len(stored.SandboxRuns) == 0 {
		t.Fatalf("stored report incomplete: %+v", stored)
	}
	task, err := store.LoadTask(context.Background(), report.Task.ID)
	if err != nil || task.Status != "completed" {
		t.Fatalf("task state is not queryable: %+v, %v", task, err)
	}
	runs, err := store.LoadRuns(context.Background(), report.Task.ID)
	if err != nil || len(runs) != len(report.SandboxRuns) {
		t.Fatalf("sandbox runs are not queryable: %+v, %v", runs, err)
	}
	decisions, err := store.LoadDecisions(context.Background(), report.Task.ID)
	if err != nil || len(decisions) != len(report.PermissionDecisions) {
		t.Fatalf("permission decisions are not queryable: %+v, %v", decisions, err)
	}
	filterDecisions, err := store.LoadFilterDecisions(context.Background(), report.Task.ID)
	if err != nil || len(filterDecisions) != len(report.FilterDecisions) {
		t.Fatalf("filter decisions are not queryable: %+v, %v", filterDecisions, err)
	}
	metrics, err := store.LoadMetrics(context.Background(), report.Task.ID)
	if err != nil || metrics.ToolCallCount != report.Metrics.ToolCallCount {
		t.Fatalf("metrics are not queryable: %+v, %v", metrics, err)
	}
	findings, err := store.LoadFindings(context.Background(), report.Task.ID, "finding")
	if err != nil || len(findings) != len(report.Findings) {
		t.Fatalf("findings are not queryable: %+v, %v", findings, err)
	}
	artifacts, err := store.LoadArtifacts(context.Background(), report.Task.ID)
	if err != nil || len(artifacts) != len(report.Artifacts) {
		t.Fatalf("artifacts are not queryable: %+v, %v", artifacts, err)
	}
	for _, path := range []string{paths.JSON, paths.Markdown, dbPath} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "sk-abcdefghijklmnopqrstuvwxyz123456") {
			t.Fatalf("plaintext secret leaked into %s", path)
		}
	}
}

func TestSandboxFailureDoesNotAbortReview(t *testing.T) {
	dir := t.TempDir()
	report, _, err := Run(context.Background(), Config{Fixture: "sandbox_failure", OutputDir: dir, DatabasePath: filepath.Join(dir, "reviews.sqlite"), Executor: "fake-fail", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if report.Task.Status != "completed" || !hasCategory(report.NeedsHumanReview, "sandbox") {
		t.Fatalf("failure was not retained: %+v", report)
	}
}

func TestSnapshotIncludesGoAndExcludesSecrets(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".env"), []byte("TOKEN=plaintext\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, cleanup, err := safeSnapshot(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if _, err := os.Stat(filepath.Join(snapshot, "main.go")); err != nil {
		t.Fatal("go source missing from snapshot")
	}
	if _, err := os.Stat(filepath.Join(snapshot, ".env")); !os.IsNotExist(err) {
		t.Fatal("environment file entered snapshot")
	}
}

func TestMarkdownContainsRequiredAuditSections(t *testing.T) {
	report := Report{Task: Task{ID: "review-test", Status: "completed"}, Metrics: Metrics{SeverityDistribution: map[string]int{}}, Conclusion: "done"}
	text := string(renderMarkdown(report))
	for _, section := range []string{"## Summary", "## Findings", "## Human review", "## Governance decisions", "## Filter decisions", "## Sandbox summary", "## Monitoring", "## Conclusion"} {
		if !strings.Contains(text, section) {
			t.Fatalf("missing %s", section)
		}
	}
}

func TestExecutorInitializationFailureIsReported(t *testing.T) {
	dir := t.TempDir()
	runner := &sandbox{executor: "container", initErr: context.DeadlineExceeded, outputDir: dir}
	runs, decisions, artifacts := runner.run(context.Background(), "task-init-failure", "", ParsedInput{})
	if len(runs) != 1 || runs[0].Status != "failed" || runs[0].ErrorType != "setup_error" {
		t.Fatalf("unexpected runs: %+v", runs)
	}
	if len(decisions) != 1 || len(artifacts) != 1 {
		t.Fatalf("audit evidence missing: decisions=%+v artifacts=%+v", decisions, artifacts)
	}
}

func TestTaskIDValidation(t *testing.T) {
	for _, value := range []string{"../escape", "has space", strings.Repeat("a", 81)} {
		cfg := Config{TaskID: value}
		if err := normalizeConfig(&cfg); err == nil {
			t.Fatalf("task id %q was accepted", value)
		}
	}
}

func hasCategory(values []Finding, category string) bool {
	for _, value := range values {
		if value.Category == category {
			return true
		}
	}
	return false
}
