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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/pipeline"
	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/storage"
)

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
		t.Fatalf("Run should not fail without sandbox: %v", err)
	}
	if result.TaskID == "" {
		t.Fatal("expected task id")
	}
}
