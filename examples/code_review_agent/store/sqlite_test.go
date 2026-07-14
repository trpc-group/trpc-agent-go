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
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
)

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
