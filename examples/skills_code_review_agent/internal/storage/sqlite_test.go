//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/findings"
)

func TestSQLiteStoreSaveAndGet(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "reviews.db")
	store, err := NewSQLiteStore("file:" + dbPath + "?_busy_timeout=5000")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	if err := store.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	taskID := uuid.NewString()
	now := time.Now().UTC().Truncate(time.Second)
	review := &ReviewRecord{
		TaskID:       taskID,
		Status:       "completed",
		InputSummary: "changed files: a.go",
		CreatedAt:    now,
		FinishedAt:   now,
		DurationMs:   42,
		Findings: []findings.Finding{
			{
				Severity: "high", Category: "security", File: "a.go", Line: 10,
				Title: "issue", Evidence: "safe", Recommendation: "fix",
				Confidence: 0.9, Source: "rule", RuleID: "SEC-001",
			},
		},
		Warnings: []findings.Finding{
			{
				Severity: "low", Category: "testing", File: "a.go", Line: 20,
				Title: "maybe", Evidence: "maybe", Recommendation: "check",
				Confidence: 0.5, Source: "rule", RuleID: "TEST-001",
			},
		},
		Metrics: findings.ReviewMetrics{
			TotalDurationMs: 42,
			FindingCount:    1,
			WarningCount:    1,
			SeverityCounts:  map[string]int{"high": 1},
			ExceptionCounts: map[string]int{},
		},
		Artifacts: []ArtifactRecord{
			{ID: uuid.NewString(), TaskID: taskID, Name: "review_report.json", Content: `{"ok":true}`},
		},
	}
	if err := store.SaveReview(ctx, review); err != nil {
		t.Fatalf("SaveReview: %v", err)
	}

	got, err := store.GetReview(ctx, taskID)
	if err != nil {
		t.Fatalf("GetReview: %v", err)
	}
	if got.TaskID != taskID || got.Status != "completed" {
		t.Fatalf("task = %+v", got)
	}
	if len(got.Findings) != 1 || len(got.Warnings) != 1 {
		t.Fatalf("findings/warnings = %d/%d", len(got.Findings), len(got.Warnings))
	}
	if len(got.Artifacts) != 1 {
		t.Fatalf("artifacts = %d", len(got.Artifacts))
	}
}
