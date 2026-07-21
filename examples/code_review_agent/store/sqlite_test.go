//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
)

// TestSQLiteStoreSavesFinding verifies findings round-trip through SQLite.
func TestSQLiteStoreSavesFinding(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "review.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	task := review.ReviewTask{
		ID:           "task-1",
		Status:       review.StatusRunning,
		InputType:    review.InputTypeDiffFile,
		InputSummary: "secret token=plain",
		StartedAt:    time.Now(),
	}
	if err := db.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveFindings(ctx, task.ID, []review.Finding{{
		Severity: review.SeverityHigh, Category: "security", File: "a.go", Line: 1,
		Title: "secret", Evidence: "token=plain", Recommendation: "rotate", Confidence: 0.9,
		Source: "test", RuleID: "SEC001",
	}}); err != nil {
		t.Fatal(err)
	}
	n, err := db.CountFindings(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("count=%d", n)
	}
	snapshot, err := db.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Task.ID != task.ID {
		t.Fatalf("snapshot task=%q", snapshot.Task.ID)
	}
	if len(snapshot.Findings) != 1 {
		t.Fatalf("snapshot findings=%d", len(snapshot.Findings))
	}
}

// TestSQLiteStoreFilterDecisionRoundTrip verifies filter decisions persist.
func TestSQLiteStoreFilterDecisionRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "review.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	task := review.ReviewTask{
		ID:        "task-2",
		Status:    review.StatusRunning,
		InputType: review.InputTypeDiffFile,
		StartedAt: time.Now(),
	}
	if err := db.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	created := time.Now().UTC().Truncate(time.Millisecond)
	decisions := []review.FilterDecision{
		{
			RuleID: "SEC001", File: "a.go", Line: 3, Source: "llm",
			Confidence: 0.7, Stage: review.FilterStageDedup,
			Decision:  review.FilterDecisionDropDuplicate,
			Reason:    "duplicate with token=plain evidence",
			CreatedAt: created,
		},
		{
			RuleID: "CTX001", File: "a.go", Line: 9, Source: "rule-only",
			Confidence: 0.55, Stage: review.FilterStageConfidence,
			Decision:  review.FilterDecisionHumanReview,
			Reason:    "confidence 0.55 in [0.45, 0.75) routes to human review",
			CreatedAt: created,
		},
	}
	if err := db.SaveFilterDecisions(ctx, task.ID, decisions); err != nil {
		t.Fatal(err)
	}
	snapshot, err := db.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.FilterDecisions) != 2 {
		t.Fatalf("filter decisions=%d, want 2", len(snapshot.FilterDecisions))
	}
	got := snapshot.FilterDecisions[0]
	if got.RuleID != "SEC001" || got.Stage != review.FilterStageDedup ||
		got.Decision != review.FilterDecisionDropDuplicate {
		t.Fatalf("bad first decision: %+v", got)
	}
	if strings.Contains(got.Reason, "token=plain") {
		t.Fatalf("reason was not redacted: %q", got.Reason)
	}
	if !got.CreatedAt.Equal(created) {
		t.Fatalf("created_at round trip: got %v want %v", got.CreatedAt, created)
	}
	if snapshot.FilterDecisions[1].Decision != review.FilterDecisionHumanReview {
		t.Fatalf("bad second decision: %+v", snapshot.FilterDecisions[1])
	}
}

// TestGetTaskEmptyFilterDecisions verifies snapshots tolerate empty audit rows.
func TestGetTaskEmptyFilterDecisions(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "review.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	task := review.ReviewTask{
		ID:        "task-3",
		Status:    review.StatusRunning,
		InputType: review.InputTypeDiffFile,
		StartedAt: time.Now(),
	}
	if err := db.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	snapshot, err := db.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.FilterDecisions == nil {
		t.Fatal("filter decisions should default to an empty slice")
	}
}
