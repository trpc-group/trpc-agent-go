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
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/domain"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/report"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/sandbox"
)

func TestSQLiteStorePersistsFullAuditWithDatabaseSQL(t *testing.T) {
	path := t.TempDir() + "/audit.db"
	store, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	dto := report.DTO{
		TaskID:      "task-1",
		Status:      domain.StatusCompleted,
		Input:       report.InputSummary{Kind: "diff-file", Digest: "sha256:abc", Files: 1, Hunks: 2, AddedLines: 3},
		SandboxRuns: []sandbox.Result{{CommandID: "go-test", Outcome: sandbox.OutcomeSuccess, DurationMS: 123}},
		Metrics:     map[string]int{"duration_ms": 7},
		Artifacts:   []string{"review_report.json"},
		ArtifactDetails: []report.Artifact{{
			Path: "review_report.json", SHA256: "sha256:abc123", Bytes: 42,
			ContentType: "application/json", Durable: true,
		}},
		Findings: []domain.Finding{{
			Severity: domain.SeverityHigh, Category: domain.CategorySecurity, File: "a.go", Line: 1,
			Title: "x", Evidence: "password = \"super-secret-value\"", Recommendation: "fix",
			Confidence: 0.9, Source: "rule", RuleID: "security.command-injection",
		}},
	}
	if err := store.Finalize(context.Background(), dto); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	got, err := store.GetReview(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.TaskID != "task-1" || len(got.Findings) != 1 {
		t.Fatalf("review = %#v", got)
	}
	if got.Input.Kind != "diff-file" || got.Input.Digest != "sha256:abc" || got.Input.Files != 1 || got.Input.Hunks != 2 || got.Input.AddedLines != 3 {
		t.Fatalf("input summary = %#v", got.Input)
	}
	if len(got.ArtifactDetails) != 1 || got.ArtifactDetails[0].SHA256 != "sha256:abc123" || got.ArtifactDetails[0].Bytes != 42 {
		t.Fatalf("artifact metadata = %#v", got.ArtifactDetails)
	}
	var artifactDigest string
	var artifactBytes int64
	if err := store.DB().QueryRow("SELECT sha256,bytes FROM artifacts WHERE task_id=? AND path=?", "task-1", "review_report.json").Scan(&artifactDigest, &artifactBytes); err != nil {
		t.Fatal(err)
	}
	if artifactDigest != "sha256:abc123" || artifactBytes != 42 {
		t.Fatalf("artifact row digest=%s bytes=%d", artifactDigest, artifactBytes)
	}
	var runDuration int64
	if err := store.DB().QueryRow("SELECT duration_ms FROM sandbox_runs WHERE task_id=? AND command_id=?", "task-1", "go-test").Scan(&runDuration); err != nil {
		t.Fatal(err)
	}
	if runDuration != 123 {
		t.Fatalf("sandbox duration_ms = %d, want 123", runDuration)
	}
	var needsHumanReview bool
	if err := store.DB().QueryRow("SELECT needs_human_review FROM findings WHERE task_id=? AND rule_id=?", "task-1", "security.command-injection").Scan(&needsHumanReview); err != nil {
		t.Fatal(err)
	}
	if needsHumanReview {
		t.Fatalf("regular finding persisted as needs_human_review")
	}
	db := store.DB()
	if db == nil {
		t.Fatalf("store does not expose database/sql handle")
	}
	var fk int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("foreign_keys pragma: %v", err)
	}
	if fk != 1 {
		t.Fatalf("foreign_keys = %d, want 1", fk)
	}
	var journal string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil {
		t.Fatalf("journal_mode pragma: %v", err)
	}
	if journal != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journal)
	}
	assert0600(t, path)
}

func TestSQLiteStoreRedactsDBWALAndSHMBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.db")
	store, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	dto := report.DTO{
		TaskID: "task-2",
		Status: domain.StatusCompleted,
		Findings: []domain.Finding{{
			Severity: domain.SeverityHigh, Category: domain.CategorySecrets, File: "a.go", Line: 1,
			Title: "x", Evidence: "fixture-secret-value-github-token", Recommendation: "fix",
			Confidence: 0.98, Source: "rule", RuleID: "secrets.literal",
		}},
	}
	if err := store.Finalize(context.Background(), dto); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	canaries := [][]byte{
		[]byte("fixture-secret-value-github-token"),
		[]byte("super-secret-value"),
	}
	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		raw, err := os.ReadFile(name)
		if err != nil {
			if os.IsNotExist(err) && name != path {
				continue
			}
			t.Fatal(err)
		}
		for _, canary := range canaries {
			if bytes.Contains(raw, canary) {
				t.Fatalf("%s leaked canary %q", name, canary)
			}
		}
	}
}

func TestSQLiteStoreForeignKeysRejectOrphanRows(t *testing.T) {
	path := t.TempDir() + "/audit.db"
	store, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	_, err = store.DB().Exec("INSERT INTO sandbox_runs(task_id,command_id,outcome) VALUES(?,?,?)", "missing-task", "go-test", "success")
	if err == nil {
		t.Fatalf("orphan sandbox run was accepted")
	}
}

func TestSQLiteOpenEscapesSpecialCharacterPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "db ?#% dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "audit ?#%.db")
	store, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("open special path: %v", err)
	}
	defer store.Close()
	if err := store.Finalize(context.Background(), report.DTO{TaskID: "special-path", Status: domain.StatusCompleted}); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if _, err := store.GetReview(context.Background(), "special-path"); err != nil {
		t.Fatalf("reload special path review: %v", err)
	}
	assert0600(t, path)
}

func TestSQLiteStoreDistinguishesHumanReviewFindingsAndNotFound(t *testing.T) {
	store, err := OpenSQLite(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	_, err = store.GetReview(context.Background(), "missing")
	if !errors.Is(err, ErrReviewNotFound) {
		t.Fatalf("missing review err = %v, want ErrReviewNotFound", err)
	}
	dto := report.DTO{TaskID: "human-review", Status: domain.StatusNeedsHumanReview, NeedsHumanReview: []domain.Finding{{
		Severity: domain.SeverityMedium, Category: domain.CategoryTests, File: "a.go", Line: 0,
		Title: "needs review", Evidence: "low confidence", Recommendation: "check", Confidence: 0.5, Source: "rule", RuleID: "tests.missing-related-test",
	}}}
	if err := store.Finalize(context.Background(), dto); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	var needsHumanReview bool
	if err := store.DB().QueryRow("SELECT needs_human_review FROM findings WHERE task_id=? AND rule_id=?", "human-review", "tests.missing-related-test").Scan(&needsHumanReview); err != nil {
		t.Fatal(err)
	}
	if !needsHumanReview {
		t.Fatalf("needs_human_review flag was not persisted")
	}
}

func TestSQLiteFinalizeUpsertPreservesRecordedGovernanceDecision(t *testing.T) {
	store, err := OpenSQLite(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.RecordDecision(ctx, "task-upsert", DecisionRecord{Action: "allow", Reason: "ok", PlanDigest: "plan-123"}); err != nil {
		t.Fatalf("record decision: %v", err)
	}
	if err := store.Finalize(ctx, report.DTO{TaskID: "task-upsert", Status: domain.StatusCompleted}); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	var reason, digest string
	if err := store.DB().QueryRow("SELECT reason,plan_digest FROM governance_decisions WHERE task_id=?", "task-upsert").Scan(&reason, &digest); err != nil {
		t.Fatal(err)
	}
	if reason != "ok" || digest != "plan-123" {
		t.Fatalf("governance decision reason=%q digest=%q", reason, digest)
	}
}

func assert0600(t *testing.T, path string) {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 0600", st.Mode().Perm())
	}
}
