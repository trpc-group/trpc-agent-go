//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package store

import (
	"context"
	"database/sql"
	"encoding/json"
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

func TestSQLiteRedactsAllPersistedCallerStrings(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "review.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const plaintext = "abcdefghijklmnop"
	secret := "token=" + plaintext
	task := review.ReviewTask{ID: "task-redaction", Status: review.StatusRunning,
		InputType: review.InputTypeDiffFile, InputSummary: secret, RepoPath: secret,
		StartedAt: time.Now()}
	if err := db.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	finding := review.Finding{Severity: review.SeverityHigh, Category: secret,
		File: secret, Line: 1, Title: secret, Evidence: secret,
		Recommendation: secret, Confidence: 0.9, Source: secret, RuleID: secret}
	report := review.ReviewReport{
		Findings: []review.Finding{finding}, Summary: secret,
		SandboxRuns: []review.SandboxRun{{Command: secret, Status: "failed",
			StdoutExcerpt: secret, StderrExcerpt: secret, Error: secret,
			FailureKind: review.FailureKindExecutor}},
		PermissionDecisions: []review.PermissionDecision{{Command: secret, Decision: "deny", Reason: secret}},
		FilterDecisions: []review.FilterDecision{{RuleID: secret, File: secret,
			Source: secret, Stage: review.FilterStageConfidence,
			Decision: review.FilterDecisionKeep, Reason: secret}},
	}
	artifacts := []review.Artifact{{Kind: secret, Path: secret, SHA256: secret}}
	if err := db.SaveReview(ctx, task.ID, report, artifacts, secret, secret); err != nil {
		t.Fatal(err)
	}
	snapshot, err := db.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), plaintext) {
		t.Fatalf("SQLite snapshot leaked plaintext: %s", data)
	}
}

func TestSQLiteRejectsSensitiveTaskID(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "review.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	err = db.CreateTask(ctx, review.ReviewTask{
		ID: "token=abcdefghijklmnop", Status: review.StatusRunning,
		InputType: review.InputTypeDiffFile, StartedAt: time.Now(),
	})
	if err == nil || !strings.Contains(err.Error(), "task id contains sensitive data") {
		t.Fatalf("sensitive task id was accepted: %v", err)
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

func TestSaveReviewRollsBackAllFinalRecords(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "review.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	task := review.ReviewTask{
		ID: "task-atomic", Status: review.StatusRunning,
		InputType: review.InputTypeDiffFile, StartedAt: time.Now(),
	}
	if err := db.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	// Seed the unique report row so the final insert fails after findings
	// have been inserted inside SaveReview's transaction.
	if err := db.SaveReport(ctx, task.ID, review.ReviewReport{}, "old.json", "old.md"); err != nil {
		t.Fatal(err)
	}
	report := review.ReviewReport{Findings: []review.Finding{{
		Severity: review.SeverityHigh, Category: "security", File: "a.go",
		Line: 1, Title: "finding", Evidence: "evidence",
		Recommendation: "fix", Confidence: 0.9, Source: "test", RuleID: "R1",
	}}}
	if err := db.SaveReview(ctx, task.ID, report, nil, "new.json", "new.md"); err == nil {
		t.Fatal("SaveReview unexpectedly succeeded")
	}
	count, err := db.CountFindings(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("partial findings survived rollback: %d", count)
	}
}

func TestSQLiteSchemaVersionAndForeignKeys(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "review.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err := db.db.QueryRowContext(ctx,
		`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 2 {
		t.Fatalf("schema version=%d, want 2", version)
	}
	_, err = db.db.ExecContext(ctx, `
INSERT INTO artifacts(task_id, kind, path) VALUES ('missing', 'json', 'report.json')`)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "foreign key") {
		t.Fatalf("orphan artifact was accepted: %v", err)
	}
}

func TestSaveReviewPreservesBucketsAndConclusion(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "review.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	task := review.ReviewTask{ID: "task-buckets", Status: review.StatusRunning,
		InputType: review.InputTypeDiffFile, StartedAt: time.Now()}
	if err := db.CreateTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	finding := func(rule string) review.Finding {
		return review.Finding{Severity: review.SeverityMedium, Category: "test",
			File: "a.go", Line: 1, Title: rule, Evidence: rule,
			Recommendation: "fix", Confidence: 0.5, Source: "test", RuleID: rule}
	}
	report := review.ReviewReport{
		Findings:         []review.Finding{finding("FINDING")},
		Warnings:         []review.Finding{finding("WARNING")},
		NeedsHumanReview: []review.Finding{finding("HUMAN")},
		Summary:          "final conclusion",
	}
	if err := db.SaveReview(ctx, task.ID, report, nil, "report.json", "report.md"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := db.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Findings) != 1 || len(snapshot.Warnings) != 1 ||
		len(snapshot.NeedsHumanReview) != 1 {
		t.Fatalf("buckets were not preserved: %+v", snapshot)
	}
	if snapshot.Report.Conclusion != report.Summary {
		t.Fatalf("conclusion=%q, want %q", snapshot.Report.Conclusion, report.Summary)
	}
}

func TestSQLiteMigratesV1WithoutLosingRows(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "review.db")
	raw, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`,
		`INSERT INTO schema_migrations VALUES (1, 'now')`,
		`CREATE TABLE review_tasks(id TEXT PRIMARY KEY, status TEXT NOT NULL, input_type TEXT NOT NULL, input_summary TEXT NOT NULL, repo_path TEXT, started_at TEXT NOT NULL, finished_at TEXT, error TEXT)`,
		`INSERT INTO review_tasks VALUES ('old', 'completed', 'diff_file', 'old input', '', '2026-01-01T00:00:00Z', '', '')`,
		`CREATE TABLE review_findings(id INTEGER PRIMARY KEY AUTOINCREMENT, task_id TEXT NOT NULL, severity TEXT NOT NULL, category TEXT NOT NULL, file TEXT NOT NULL, line INTEGER NOT NULL, title TEXT NOT NULL, evidence TEXT NOT NULL, recommendation TEXT NOT NULL, confidence REAL NOT NULL, source TEXT NOT NULL, rule_id TEXT NOT NULL)`,
		`INSERT INTO review_findings(task_id,severity,category,file,line,title,evidence,recommendation,confidence,source,rule_id) VALUES ('old','high','security','a.go',1,'old','e','fix',0.9,'rule-only','OLD')`,
		`CREATE TABLE sandbox_runs(id INTEGER PRIMARY KEY AUTOINCREMENT, task_id TEXT NOT NULL, command TEXT NOT NULL, status TEXT NOT NULL, exit_code INTEGER, duration_ms INTEGER, stdout_excerpt TEXT, stderr_excerpt TEXT, error TEXT)`,
		`CREATE TABLE review_reports(task_id TEXT PRIMARY KEY, json_path TEXT NOT NULL, markdown_path TEXT NOT NULL, summary_json TEXT NOT NULL)`,
		`INSERT INTO review_reports VALUES ('old','old.json','old.md','{}')`,
	}
	for _, statement := range statements {
		if _, err := raw.ExecContext(ctx, statement); err != nil {
			_ = raw.Close()
			t.Fatal(err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	snapshot, err := db.GetTask(ctx, "old")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Findings) != 1 || snapshot.Findings[0].RuleID != "OLD" {
		t.Fatalf("v1 finding was not preserved: %+v", snapshot.Findings)
	}
	var version int
	if err := db.db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 2 {
		t.Fatalf("schema version=%d, want 2", version)
	}
}
