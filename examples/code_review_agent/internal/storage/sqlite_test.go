//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func testDSN() string {
	dir, _ := os.MkdirTemp("", "cr-test-*")
	return filepath.Join(dir, "test.db")
}

func TestNewSQLite_CreateAndPing(t *testing.T) {
	dsn := testDSN()
	defer os.RemoveAll(filepath.Dir(dsn))

	store, err := NewSQLite(dsn)
	if err != nil {
		t.Fatalf("NewSQLite failed: %v", err)
	}
	defer store.Close()

	if err := store.Ping(context.Background()); err != nil {
		t.Fatalf("Ping failed: %v", err)
	}
}

func TestCreateAndGetTask(t *testing.T) {
	dsn := testDSN()
	defer os.RemoveAll(filepath.Dir(dsn))
	store, _ := NewSQLite(dsn)
	defer store.Close()
	ctx := context.Background()

	task := TaskRow{
		ID: "task-1", Status: "pending", InputType: "diff_file",
		InputDiffHash: "abc123", ModelMode: "dry_run",
	}
	if err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	got, err := store.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if got.Status != "pending" {
		t.Errorf("Status = %q, want pending", got.Status)
	}

	if err := store.UpdateTask(ctx, "task-1", map[string]any{"status": "completed"}); err != nil {
		t.Fatalf("UpdateTask failed: %v", err)
	}
	got2, _ := store.GetTask(ctx, "task-1")
	if got2.Status != "completed" {
		t.Errorf("Status after update = %q, want completed", got2.Status)
	}
}

func TestInsertAndQueryFindings(t *testing.T) {
	dsn := testDSN()
	defer os.RemoveAll(filepath.Dir(dsn))
	store, _ := NewSQLite(dsn)
	defer store.Close()
	ctx := context.Background()

	store.CreateTask(ctx, TaskRow{ID: "task-1", Status: "pending", InputType: "diff_file", InputDiffHash: "abc", ModelMode: "dry_run"})

	findings := []FindingRow{
		{ID: "f1", TaskID: "task-1", Severity: "high", Category: "security", File: "a.go", Line: 10, Title: "SQL injection", Confidence: 1.0, Source: "rule_engine"},
		{ID: "f2", TaskID: "task-1", Severity: "medium", Category: "error_handling", File: "a.go", Line: 15, Title: "Error ignored", Confidence: 1.0, Source: "rule_engine"},
	}
	if err := store.InsertFindings(ctx, findings); err != nil {
		t.Fatalf("InsertFindings failed: %v", err)
	}

	got, err := store.GetFindingsByTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetFindingsByTask failed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(got))
	}
}

func TestSandboxRunAndReportRoundtrip(t *testing.T) {
	dsn := testDSN()
	defer os.RemoveAll(filepath.Dir(dsn))
	store, _ := NewSQLite(dsn)
	defer store.Close()
	ctx := context.Background()

	store.CreateTask(ctx, TaskRow{ID: "task-1", Status: "pending", InputType: "diff_file", InputDiffHash: "abc", ModelMode: "live"})

	sr := SandboxRunRow{ID: "sr1", TaskID: "task-1", ExecutorType: "local", CommandName: "go_vet", Command: "go vet ./...", ExitCode: 0, DurationMs: 500}
	if err := store.InsertSandboxRun(ctx, sr); err != nil {
		t.Fatalf("InsertSandboxRun failed: %v", err)
	}

	r := ReportRow{ID: "r1", TaskID: "task-1", FindingsCount: 0, WarningsCount: 0}
	if err := store.InsertReport(ctx, r); err != nil {
		t.Fatalf("InsertReport failed: %v", err)
	}

	sruns, _ := store.GetSandboxRunsByTask(ctx, "task-1")
	if len(sruns) != 1 {
		t.Errorf("expected 1 sandbox run, got %d", len(sruns))
	}
	rep, _ := store.GetReport(ctx, "task-1")
	if rep == nil {
		t.Error("GetReport returned nil")
	}
}

func TestInsertFindings_EmptyList(t *testing.T) {
	dsn := testDSN()
	defer os.RemoveAll(filepath.Dir(dsn))
	store, _ := NewSQLite(dsn)
	defer store.Close()

	if err := store.InsertFindings(context.Background(), nil); err != nil {
		t.Errorf("nil findings should not error: %v", err)
	}
	if err := store.InsertFindings(context.Background(), []FindingRow{}); err != nil {
		t.Errorf("empty findings should not error: %v", err)
	}
}
