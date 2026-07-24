//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/storage"
)

func TestSaveReviewRollsBackEveryRecordOnFailure(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "review.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Second)
	finding := review.Finding{
		Severity: "high", Category: "security", File: "main.go", Line: 9,
		Title: "Hardcoded secret", Source: "rule", RuleID: "secret-leak",
	}
	err = store.SaveReview(context.Background(), storage.ReviewRecord{
		Task:     storage.Task{ID: "task-rollback", InputType: "diff", InputRef: "fixture.diff", InputDigest: "abc", Status: "done", Mode: "rule-only", CreatedAt: now},
		Findings: []review.Finding{finding, finding}, // duplicate primary key forces the transaction to fail
		Report:   storage.ReportRecord{JSON: []byte(`{"ok":true}`), Markdown: []byte("# report"), CreatedAt: now},
	})
	if err == nil {
		t.Fatal("SaveReview should fail on duplicate findings")
	}
	if _, err := store.TaskByID(context.Background(), "task-rollback"); err != sql.ErrNoRows {
		t.Fatalf("failed review must not leave its task behind, got %v", err)
	}
	if _, err := store.ReportByTaskID(context.Background(), "task-rollback"); err != sql.ErrNoRows {
		t.Fatalf("failed review must not leave its report behind, got %v", err)
	}
}

func TestSchemaRejectsAuditRowsWithoutTask(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "review.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()

	err = store.SaveDecision(context.Background(), storage.DecisionRecord{
		TaskID: "missing-task", Command: "go test ./...", Action: "allow", At: time.Now(),
	})
	if err == nil {
		t.Fatal("permission decision without a review task should violate its foreign key")
	}
}

func TestStorePersistsAndLoadsTaskData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review.db")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Second)
	task := Task{
		ID:          "task-1",
		InputType:   "diff",
		InputRef:    "fixture.diff",
		InputDigest: "abc123",
		RepoPath:    "/repo",
		Status:      "done",
		Mode:        "rule-only",
		CreatedAt:   now,
		StartedAt:   now,
		FinishedAt:  now,
	}
	if err := store.SaveTask(context.Background(), task); err != nil {
		t.Fatalf("SaveTask returned error: %v", err)
	}

	finding := review.Finding{
		Severity: "high", Category: "security", File: "main.go", Line: 9,
		Title: "Hardcoded secret", Source: "rule", RuleID: "secret-leak",
	}
	if err := store.SaveFinding(context.Background(), "task-1", finding); err != nil {
		t.Fatalf("SaveFinding returned error: %v", err)
	}
	if err := store.SaveReport(context.Background(), "task-1", []byte(`{"ok":true}`), []byte("# report")); err != nil {
		t.Fatalf("SaveReport returned error: %v", err)
	}

	got, err := store.TaskByID(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("TaskByID returned error: %v", err)
	}
	if got.ID != task.ID || got.Status != task.Status {
		t.Fatalf("unexpected loaded task: %+v", got)
	}
	findings, err := store.FindingsByTaskID(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("FindingsByTaskID returned error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	report, err := store.ReportByTaskID(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("ReportByTaskID returned error: %v", err)
	}
	if string(report.JSON) != `{"ok":true}` {
		t.Fatalf("unexpected report json: %s", string(report.JSON))
	}

	if err := store.SaveDecision(context.Background(), DecisionRecord{
		TaskID:  "task-1",
		Command: "go test ./...",
		Action:  "allow",
		Reason:  "ok",
		At:      now,
	}); err != nil {
		t.Fatalf("SaveDecision returned error: %v", err)
	}
	if err := store.SaveSandboxRun(context.Background(), SandboxRunRecord{
		TaskID:        "task-1",
		Command:       "go test ./...",
		Status:        "ok",
		Output:        "PASS",
		At:            now,
		FinishedAt:    now.Add(time.Second),
		ArtifactCount: 3,
	}); err != nil {
		t.Fatalf("SaveSandboxRun returned error: %v", err)
	}
	runs, err := store.SandboxRunsByTaskID(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("SandboxRunsByTaskID returned error: %v", err)
	}
	if len(runs) != 1 || runs[0].ArtifactCount != 3 || runs[0].FinishedAt.IsZero() {
		t.Fatalf("unexpected sandbox run audit fields: %+v", runs)
	}
	if err := store.SaveMetrics(context.Background(), MetricsRecord{
		TaskID:               "task-1",
		TotalDurationMS:      10,
		SandboxDurationMS:    5,
		ModelDurationMS:      3,
		ToolCallCount:        1,
		ModelCallCount:       1,
		ModelProvider:        "fake",
		ModelName:            "fake_model",
		ModelBackend:         "trpc-agent-go/model.Model",
		PermissionBlockCount: 0,
		FindingCount:         1,
		ModelFindingCount:    1,
		ModelExceptionCount:  0,
		SeverityCountsJSON:   `{"high":1}`,
		ExceptionCountsJSON:  `{}`,
		RedactionCount:       1,
		At:                   now,
	}); err != nil {
		t.Fatalf("SaveMetrics returned error: %v", err)
	}

	gotMetrics, err := store.MetricsByTaskID(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("MetricsByTaskID returned error: %v", err)
	}
	if gotMetrics.FindingCount != 1 || gotMetrics.ToolCallCount != 1 {
		t.Fatalf("unexpected metrics: %+v", gotMetrics)
	}
	if gotMetrics.ModelDurationMS != 3 || gotMetrics.ModelCallCount != 1 || gotMetrics.ModelFindingCount != 1 {
		t.Fatalf("unexpected model metrics: %+v", gotMetrics)
	}
	if gotMetrics.ModelProvider != "fake" || gotMetrics.ModelName != "fake_model" || gotMetrics.ModelBackend != "trpc-agent-go/model.Model" {
		t.Fatalf("unexpected model audit fields: %+v", gotMetrics)
	}
}

func TestStoreMigratesModelAuditColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-metrics.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	_, err = db.Exec(`
CREATE TABLE metrics (
  task_id TEXT PRIMARY KEY,
  total_duration_ms INTEGER NOT NULL,
  sandbox_duration_ms INTEGER NOT NULL,
  model_duration_ms INTEGER NOT NULL DEFAULT 0,
  tool_call_count INTEGER NOT NULL,
  model_call_count INTEGER NOT NULL DEFAULT 0,
  permission_block_count INTEGER NOT NULL,
  finding_count INTEGER NOT NULL,
  model_finding_count INTEGER NOT NULL DEFAULT 0,
  model_exception_count INTEGER NOT NULL DEFAULT 0,
  severity_counts_json TEXT NOT NULL,
  exception_counts_json TEXT NOT NULL,
  redaction_count INTEGER NOT NULL,
  created_at TEXT NOT NULL
);`)
	if err != nil {
		t.Fatalf("create legacy metrics: %v", err)
	}
	_, err = db.Exec(`INSERT INTO metrics(task_id,total_duration_ms,sandbox_duration_ms,model_duration_ms,tool_call_count,model_call_count,permission_block_count,finding_count,model_finding_count,model_exception_count,severity_counts_json,exception_counts_json,redaction_count,created_at) VALUES('legacy-task',1,0,0,1,0,0,0,0,0,'{}','{}',0,'2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert legacy metrics: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open should migrate legacy metrics: %v", err)
	}
	defer store.Close()
	legacy, err := store.MetricsByTaskID(context.Background(), "legacy-task")
	if err != nil {
		t.Fatalf("read migrated legacy metrics: %v", err)
	}
	if legacy.Mode != nil || legacy.SandboxRequested != nil || legacy.SandboxExecuted != nil || legacy.ModelRequested != nil || legacy.ModelExecuted != nil {
		t.Fatalf("legacy capability state must remain unknown: %+v", legacy)
	}

	now := time.Now().UTC().Truncate(time.Second)
	if err := store.SaveMetrics(context.Background(), MetricsRecord{
		TaskID:               "task-model-audit",
		Mode:                 stringPointer("review"),
		SandboxRequested:     boolPointer(false),
		SandboxExecuted:      boolPointer(false),
		ModelRequested:       boolPointer(true),
		ModelExecuted:        boolPointer(true),
		TotalDurationMS:      10,
		SandboxDurationMS:    2,
		ModelDurationMS:      3,
		ToolCallCount:        1,
		ModelCallCount:       1,
		ModelProvider:        "deepseek",
		ModelName:            "deepseek-chat",
		ModelBackend:         "trpc-agent-go/model/openai",
		PermissionBlockCount: 0,
		FindingCount:         1,
		ModelFindingCount:    1,
		ModelExceptionCount:  0,
		SeverityCountsJSON:   `{"high":1}`,
		ExceptionCountsJSON:  `{}`,
		RedactionCount:       0,
		At:                   now,
	}); err != nil {
		t.Fatalf("SaveMetrics after migration returned error: %v", err)
	}
	got, err := store.MetricsByTaskID(context.Background(), "task-model-audit")
	if err != nil {
		t.Fatalf("MetricsByTaskID returned error: %v", err)
	}
	if got.ModelProvider != "deepseek" || got.ModelName != "deepseek-chat" || got.ModelBackend != "trpc-agent-go/model/openai" {
		t.Fatalf("migration did not preserve model audit fields: %+v", got)
	}
	if got.Mode == nil || *got.Mode != "review" || got.SandboxRequested == nil || *got.SandboxRequested || got.ModelRequested == nil || !*got.ModelRequested {
		t.Fatalf("new capability state must be known even when false: %+v", got)
	}
}

func boolPointer(value bool) *bool { return &value }

func stringPointer(value string) *string { return &value }

func TestStoreMigratesSandboxRunAuditColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	_, err = db.Exec(`
CREATE TABLE sandbox_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id TEXT NOT NULL,
  command TEXT NOT NULL,
  runtime TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  timeout_ms INTEGER NOT NULL DEFAULT 0,
  output_limit_bytes INTEGER NOT NULL DEFAULT 0,
  env_whitelist TEXT NOT NULL DEFAULT '',
  exit_code INTEGER NOT NULL DEFAULT 0,
  stdout_digest TEXT NOT NULL DEFAULT '',
  stderr_digest TEXT NOT NULL DEFAULT '',
  duration_ms INTEGER NOT NULL DEFAULT 0,
  output TEXT,
  created_at TEXT NOT NULL
);`)
	if err != nil {
		t.Fatalf("create legacy sandbox_runs: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open should migrate legacy db: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Second)
	if err := store.SaveSandboxRun(context.Background(), SandboxRunRecord{
		TaskID:        "task-legacy",
		Command:       "go vet ./...",
		Status:        "ok",
		At:            now,
		FinishedAt:    now.Add(time.Second),
		ArtifactCount: 2,
	}); err != nil {
		t.Fatalf("SaveSandboxRun after migration returned error: %v", err)
	}
	runs, err := store.SandboxRunsByTaskID(context.Background(), "task-legacy")
	if err != nil {
		t.Fatalf("SandboxRunsByTaskID returned error: %v", err)
	}
	if len(runs) != 1 || runs[0].ArtifactCount != 2 || runs[0].FinishedAt.IsZero() {
		t.Fatalf("migration did not preserve new audit fields: %+v", runs)
	}
}

func TestStorePersistsFilterDecisionsAndArtifacts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review.db")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Second)
	task := Task{
		ID:          "task-artifacts",
		InputType:   "diff",
		InputRef:    "fixture.diff",
		InputDigest: "abc123",
		Status:      "done",
		Mode:        "rule-only",
		CreatedAt:   now,
	}
	if err := store.SaveTask(context.Background(), task); err != nil {
		t.Fatalf("SaveTask returned error: %v", err)
	}
	if err := store.SaveFilterDecision(context.Background(), FilterDecisionRecord{
		TaskID: "task-artifacts",
		Target: "finding.evidence",
		Action: "redact",
		Reason: "secret pattern",
		At:     now,
	}); err != nil {
		t.Fatalf("SaveFilterDecision returned error: %v", err)
	}
	if err := store.SaveArtifact(context.Background(), ArtifactRecord{
		TaskID: "task-artifacts",
		Name:   "review_report.json",
		Kind:   "report",
		Path:   "review_report.json",
		Digest: "digest-1",
		Size:   128,
		At:     now,
	}); err != nil {
		t.Fatalf("SaveArtifact returned error: %v", err)
	}

	decisions, err := store.FilterDecisionsByTaskID(context.Background(), "task-artifacts")
	if err != nil {
		t.Fatalf("FilterDecisionsByTaskID returned error: %v", err)
	}
	if len(decisions) != 1 || decisions[0].Action != "redact" {
		t.Fatalf("unexpected filter decisions: %+v", decisions)
	}
	artifacts, err := store.ArtifactsByTaskID(context.Background(), "task-artifacts")
	if err != nil {
		t.Fatalf("ArtifactsByTaskID returned error: %v", err)
	}
	if len(artifacts) != 1 || artifacts[0].Name != "review_report.json" || artifacts[0].Digest != "digest-1" || artifacts[0].Size != 128 {
		t.Fatalf("unexpected artifacts: %+v", artifacts)
	}
}

func TestArtifactsTableStoresReferencesOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review.db")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer store.Close()

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite directly: %v", err)
	}
	defer db.Close()

	rows, err := db.QueryContext(context.Background(), `PRAGMA table_info(artifacts)`)
	if err != nil {
		t.Fatalf("query artifact schema: %v", err)
	}
	defer rows.Close()

	columns := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan artifact schema: %v", err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate artifact schema: %v", err)
	}
	for _, forbidden := range []string{"content", "data", "payload", "blob", "json_report", "markdown_report"} {
		if columns[forbidden] {
			t.Fatalf("artifacts table should store references only, found column %q", forbidden)
		}
	}
	for _, required := range []string{"task_id", "name", "kind", "path", "digest", "size_bytes", "created_at"} {
		if !columns[required] {
			t.Fatalf("artifacts table missing reference column %q, columns=%+v", required, columns)
		}
	}
}

func TestStoreMigratesArtifactSizeColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-artifacts.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	_, err = db.Exec(`
CREATE TABLE artifacts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id TEXT NOT NULL,
  name TEXT NOT NULL,
  kind TEXT NOT NULL,
  path TEXT,
  digest TEXT,
  created_at TEXT NOT NULL
);`)
	if err != nil {
		t.Fatalf("create legacy artifacts: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open should migrate legacy artifacts: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Second)
	if err := store.SaveArtifact(context.Background(), ArtifactRecord{
		TaskID: "task-legacy-artifact",
		Name:   "review_report.md",
		Kind:   "report",
		Path:   "review_report.md",
		Digest: "digest-md",
		Size:   64,
		At:     now,
	}); err != nil {
		t.Fatalf("SaveArtifact after migration returned error: %v", err)
	}
	artifacts, err := store.ArtifactsByTaskID(context.Background(), "task-legacy-artifact")
	if err != nil {
		t.Fatalf("ArtifactsByTaskID returned error: %v", err)
	}
	if len(artifacts) != 1 || artifacts[0].Size != 64 {
		t.Fatalf("migration did not preserve artifact size: %+v", artifacts)
	}
}
