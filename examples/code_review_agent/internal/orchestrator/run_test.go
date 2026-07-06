//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/store"
)

func TestRunAllowsFakeRuntimeWithoutModel(t *testing.T) {
	outDir := t.TempDir()
	result, err := Run(context.Background(), Options{
		FixtureDir: filepath.Join("..", "..", "testdata", "fixtures"),
		OutDir:     outDir,
		DBPath:     filepath.Join(outDir, "review_agent.db"),
		Runtime:    "fake",
		Now:        fixedTestTime(),
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Report.Plan.Model != "mock-model" {
		t.Fatalf("plan model = %q, want mock-model", result.Report.Plan.Model)
	}
	if result.Report.Plan.Provider != "mock" {
		t.Fatalf("plan provider = %q, want mock", result.Report.Plan.Provider)
	}
}

func TestRunRequiresModelForNonFakeRuntime(t *testing.T) {
	outDir := t.TempDir()
	dbPath := filepath.Join(outDir, "review_agent.db")
	_, err := Run(context.Background(), Options{
		FixtureDir: filepath.Join("..", "..", "testdata", "fixtures"),
		OutDir:     outDir,
		DBPath:     dbPath,
		Runtime:    "container",
		Now:        fixedTestTime(),
	})
	if err == nil {
		t.Fatal("Run() error = nil, want model configuration error")
	}
	if !strings.Contains(err.Error(), "model orchestration requires --model or MODEL") {
		t.Fatalf("Run() error = %q, want missing model message", err)
	}
	assertFailedTaskStored(t, dbPath)
}

func TestRunRecordsConfiguredModelPlan(t *testing.T) {
	outDir := t.TempDir()
	result, err := Run(context.Background(), Options{
		FixtureDir: filepath.Join("..", "..", "testdata", "fixtures"),
		OutDir:     outDir,
		DBPath:     filepath.Join(outDir, "review_agent.db"),
		Model:      "gpt-review",
		Runtime:    "container",
		Now:        fixedTestTime(),
		Planner:    EnvPlanner{APIKey: "test-key"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Report.Plan.Model != "gpt-review" {
		t.Fatalf("plan model = %q, want gpt-review", result.Report.Plan.Model)
	}
	if result.Report.Plan.Provider != "openai_compatible" {
		t.Fatalf("plan provider = %q, want openai_compatible", result.Report.Plan.Provider)
	}
	raw, err := os.ReadFile(result.MarkdownPath)
	if err != nil {
		t.Fatalf("ReadFile(markdown) error = %v", err)
	}
	if !strings.Contains(string(raw), "## Model Plan") || !strings.Contains(string(raw), "- model: gpt-review") {
		t.Fatalf("markdown report does not contain configured model plan:\n%s", raw)
	}
}

func assertFailedTaskStored(t *testing.T, dbPath string) {
	t.Helper()
	st, err := store.NewSQLite(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("NewSQLite() error = %v", err)
	}
	defer st.Close()
	rawDiff, _, err := readFixtures(filepath.Join("..", "..", "testdata", "fixtures"))
	if err != nil {
		t.Fatalf("readFixtures() error = %v", err)
	}
	report, err := st.LoadTaskReport(context.Background(), stableTaskID(rawDiff, fixedTestTime()))
	if err != nil {
		t.Fatalf("LoadTaskReport() error = %v", err)
	}
	if report.Task.Status != review.TaskStatusFailed {
		t.Fatalf("stored task status = %q, want failed", report.Task.Status)
	}
	if !strings.Contains(report.Task.Error, "model orchestration requires --model or MODEL") {
		t.Fatalf("stored task error = %q, want missing model message", report.Task.Error)
	}
}

func fixedTestTime() time.Time {
	return time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC)
}
