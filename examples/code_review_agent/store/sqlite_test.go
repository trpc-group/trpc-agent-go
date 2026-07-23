//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package store_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/store"
)

// TestSQLiteStore_GetTaskBundle verifies related behavior.
func TestSQLiteStore_GetTaskBundle(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "review.db")
	st, err := store.OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ctx := context.Background()
	id, err := st.CreateTask(ctx, store.CreateTaskReq{
		Mode:         review.ModeRuleOnly,
		Executor:     "local",
		InputKind:    "fixture",
		InputDigest:  "abc",
		InputSummary: "1 files",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveInput(ctx, id, store.InputRecord{
		DiffTextRedacted: "diff",
		FileListJSON:     `["a.go"]`,
		PackageListJSON:  `["main"]`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SavePermission(ctx, id, review.PermissionDecision{
		ToolName:  "sandbox_exec",
		Command:   "curl x",
		Action:    "deny",
		Reason:    "blocked",
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveSandboxRun(ctx, id, review.SandboxRunSummary{
		ID:       "run-1",
		Executor: "local",
		Command:  "echo []",
		Status:   "ok",
	}); err != nil {
		t.Fatal(err)
	}
	findings := []review.Finding{{
		Severity: "high", Category: "concurrency", File: "a.go", Line: 3,
		Title: "t", Evidence: "e", Recommendation: "r", Confidence: 0.9,
		Source: "rule", RuleID: "CR-CON-001",
	}}
	if err := st.SaveFindings(ctx, id, findings, nil); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveMetrics(ctx, id, review.MetricsSummary{
		TotalDurationMS: 10,
		FindingCount:    1,
		SeverityDist:    map[string]int{"high": 1},
		ExceptionDist:   map[string]int{},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveReport(ctx, id, store.ReportRecord{
		JSONPath: "a.json", MDPath: "a.md", ReportJSON: "{}", ReportMD: "#",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateTaskStatus(ctx, id, review.StatusCompleted, "ok", ""); err != nil {
		t.Fatal(err)
	}

	bundle, err := st.GetTaskBundle(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Status != review.StatusCompleted {
		t.Fatalf("status=%s", bundle.Status)
	}
	if len(bundle.Findings) != 1 {
		t.Fatalf("findings=%d", len(bundle.Findings))
	}
	if len(bundle.Permissions) != 1 || bundle.Permissions[0].Action != "deny" {
		t.Fatalf("permissions=%+v", bundle.Permissions)
	}
	if len(bundle.SandboxRuns) != 1 {
		t.Fatalf("sandbox=%d", len(bundle.SandboxRuns))
	}
	if bundle.ReportJSON != "{}" {
		t.Fatalf("report=%q", bundle.ReportJSON)
	}
}
