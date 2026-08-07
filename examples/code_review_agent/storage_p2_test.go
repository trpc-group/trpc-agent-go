//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSQLiteRejectsDSNLikePaths(t *testing.T) {
	requireSQLiteDriver(t)
	tempDir := t.TempDir()
	basePath := filepath.Join(tempDir, "reviews.db")
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "file uri", path: "FiLe:" + filepath.ToSlash(basePath), want: "file URI"},
		{name: "memory", path: ":MeMoRy:", want: "in-memory"},
		{name: "query", path: basePath + "?mode=memory", want: "query parameters"},
		{name: "nul", path: basePath + "\x00suffix", want: "NUL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, err := openSQLiteReviewStore(context.Background(), tt.path)
			if store != nil {
				_ = store.Close()
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("open sqlite store err = %v, want %q", err, tt.want)
			}
		})
	}
	if _, err := os.Lstat(basePath); !os.IsNotExist(err) {
		t.Fatalf("rejected DSN path created database %q: %v", basePath, err)
	}
}

func TestSQLiteUsesOneResolvedFilesystemPath(t *testing.T) {
	requireSQLiteDriver(t)
	relativeRoot, err := os.MkdirTemp(".", "sqlite-p2-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(relativeRoot) })
	absolute := filepath.Join(relativeRoot, "directory with spaces", "reviews.db")
	relative, err := filepath.Rel(".", absolute)
	if err != nil {
		t.Fatal(err)
	}
	store, err := openSQLiteReviewStore(context.Background(), relative)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	sqliteStore := store.(*sqliteReviewStore)
	want, err := filepath.Abs(filepath.Clean(relative))
	if err != nil {
		t.Fatal(err)
	}
	if sqliteStore.dbPath != want {
		t.Fatalf("resolved db path = %q, want %q", sqliteStore.dbPath, want)
	}
	if got := sqliteStore.db.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("max open connections = %d, want 1", got)
	}
	var foreignKeys int
	if err := sqliteStore.db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d, want 1", foreignKeys)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(want); err != nil {
		t.Fatalf("resolved database missing: %v", err)
	}
}

func TestSQLiteMigratesV1ReviewTasks(t *testing.T) {
	requireSQLiteDriver(t)
	dbPath := filepath.Join(t.TempDir(), "reviews.db")
	db, err := sql.Open(sqliteDriverName, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	const schemaV1 = `
CREATE TABLE review_tasks (
	task_id TEXT PRIMARY KEY,
	status TEXT NOT NULL,
	conclusion TEXT NOT NULL CHECK (conclusion IN ('pass', 'findings', 'needs_human_review')),
	started_at TEXT NOT NULL,
	finished_at TEXT NOT NULL,
	duration_ms INTEGER NOT NULL,
	input_json TEXT NOT NULL,
	runtime_json TEXT NOT NULL,
	parse_json TEXT NOT NULL,
	rules_json TEXT NOT NULL,
	metrics_json TEXT NOT NULL,
	report_paths_json TEXT NOT NULL,
	skill_name TEXT,
	skill_digest TEXT,
	commands_planned INTEGER NOT NULL,
	commands_allowed INTEGER NOT NULL,
	commands_blocked INTEGER NOT NULL,
	permission_blocks INTEGER NOT NULL
);
CREATE TABLE artifacts (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	task_id TEXT NOT NULL REFERENCES review_tasks(task_id) ON DELETE CASCADE,
	ordinal INTEGER NOT NULL,
	kind TEXT NOT NULL CHECK (kind IN ('review_report_json', 'review_report_markdown')),
	path TEXT NOT NULL,
	sha256 TEXT,
	bytes INTEGER NOT NULL
);
PRAGMA user_version=1;`
	if _, err := db.Exec(schemaV1); err != nil {
		t.Fatalf("create v1 schema: %v", err)
	}
	if _, err := db.Exec(`
INSERT INTO review_tasks (
	task_id, status, conclusion, started_at, finished_at, duration_ms,
	input_json, runtime_json, parse_json, rules_json, metrics_json, report_paths_json,
	skill_name, skill_digest, commands_planned, commands_allowed, commands_blocked,
	permission_blocks
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"v1-task", reviewStatusCompleted, reviewConclusionPass,
		"2026-01-01T00:00:00Z", "2026-01-01T00:00:01Z", 1000,
		`{"kind":"fixture"}`, `{}`, `{}`, `{}`, `{}`, `{}`,
		"code-review", "digest", 1, 1, 0, 0,
	); err != nil {
		t.Fatalf("insert v1 review: %v", err)
	}
	if _, err := db.Exec(`
INSERT INTO artifacts (task_id, ordinal, kind, path, sha256, bytes)
VALUES ('v1-task', 0, 'review_report_json', 'v1-task/review_report.json', '', 1)`); err != nil {
		t.Fatalf("insert v1 artifact: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := openSQLiteReviewStore(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("migrate sqlite store: %v", err)
	}
	defer store.Close()
	sqliteStore := store.(*sqliteReviewStore)
	var version int
	if err := sqliteStore.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != reviewSchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, reviewSchemaVersion)
	}
	report, err := store.LoadReview(context.Background(), "v1-task")
	if err != nil {
		t.Fatalf("load migrated review: %v", err)
	}
	if report.Stage != reviewStageCompleted || report.Failure != nil ||
		len(report.Artifacts) != 1 || report.Artifacts[0].Path != "v1-task/review_report.json" {
		t.Fatalf("migrated report = %+v", report)
	}
}

func TestSQLiteCheckpointPreservesChildrenAndFinalSaveReplacesThem(t *testing.T) {
	requireSQLiteDriver(t)
	store, err := openSQLiteReviewStore(context.Background(), filepath.Join(t.TempDir(), "reviews.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	report := sampleReviewReport("task-upsert")
	if err := store.SaveReview(context.Background(), report); err != nil {
		t.Fatal(err)
	}
	checkpoint := report
	checkpoint.Status = reviewStatusFailed
	checkpoint.Stage = reviewStagePersistence
	checkpoint.Failure = &reviewFailure{Stage: reviewStagePersistence, Message: "failed"}
	checkpoint.Artifacts = nil
	if err := store.CheckpointReview(context.Background(), checkpoint); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadReview(context.Background(), report.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != reviewStatusFailed || len(loaded.Artifacts) != len(report.Artifacts) {
		t.Fatalf("checkpointed report = %+v", loaded)
	}

	final := report
	final.Findings = nil
	final.Artifacts = report.Artifacts[:1]
	if err := store.SaveReview(context.Background(), final); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.LoadReview(context.Background(), report.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Findings) != 0 || len(loaded.Artifacts) != 1 || loaded.Status != reviewStatusCompleted {
		t.Fatalf("replaced report = %+v", loaded)
	}
}
