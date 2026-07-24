//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights
// reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package store

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
)

// newTestStore opens a fresh in-memory store, applies the schema and returns
// it. The concrete *sqliteStore is returned so tests that need direct database
// access (e.g. to assert row counts or to issue raw INSERTs that should fail)
// can reach the underlying *sql.DB. Each call uses a distinct ":memory:"
// database so tests never share state.
func newTestStore(t *testing.T) *sqliteStore {
	t.Helper()
	st := New(":memory:").(*sqliteStore)
	if err := st.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// sampleTaskReport builds a fully-populated TaskReport for the CRUD round-trip
// test. The task_id is parameterised so callers can supply a unique value.
func sampleTaskReport(taskID string) TaskReport {
	return TaskReport{
		Task: ReviewTask{
			TaskID:            taskID,
			CreatedAt:         "2025-01-04T10:00:00Z",
			RepoPath:          "/repo/path",
			DiffSource:        "git diff HEAD~1",
			Status:            "completed",
			Conclusion:        "needs-changes",
			TotalDurationMs:   12_000,
			SandboxDurationMs: 3_500,
		},
		Findings: []Finding{
			{
				TaskID:         taskID,
				Severity:       "high",
				Category:       "security",
				File:           "auth.go",
				Line:           42,
				Title:          "Hardcoded secret",
				Evidence:       "password = \"abc\"",
				Recommendation: "Read from env var",
				Confidence:     0.95,
				Source:         "rule-engine",
				RuleID:         "SI-001",
				Fingerprint:    "fp-1",
				CreatedAt:      "2025-01-04T10:00:01Z",
			},
			{
				TaskID:         taskID,
				Severity:       "low",
				Category:       "style",
				File:           "util.go",
				Line:           7,
				Title:          "Unused import",
				Evidence:       "\"fmt\" imported but not used",
				Recommendation: "Remove the import",
				Confidence:     0.80,
				Source:         "rule-engine",
				RuleID:         "ST-001",
				Fingerprint:    "fp-2",
				CreatedAt:      "2025-01-04T10:00:02Z",
			},
		},
		SandboxRuns: []SandboxRun{
			{
				TaskID:     taskID,
				Command:    "go test ./...",
				Status:     "completed",
				ExitCode:   sql.NullInt64{Int64: 0, Valid: true},
				DurationMs: 1_200,
				TimedOut:   false,
				Truncated:  false,
				Stdout:     sql.NullString{String: "ok", Valid: true},
				Stderr:     sql.NullString{Valid: false},
				CreatedAt:  "2025-01-04T10:00:03Z",
			},
		},
		Permissions: []PermissionDecision{
			{
				TaskID:    taskID,
				Command:   "rm -rf /",
				Action:    "blocked",
				Reason:    "destructive",
				CreatedAt: "2025-01-04T10:00:04Z",
			},
		},
		Artifacts: []Artifact{
			{
				TaskID:    taskID,
				Name:      "review_report.json",
				Path:      "/out/review_report.json",
				SizeBytes: 2048,
				CreatedAt: "2025-01-04T10:00:05Z",
			},
			{
				TaskID:    taskID,
				Name:      "review_report.md",
				Path:      "/out/review_report.md",
				SizeBytes: 1536,
				CreatedAt: "2025-01-04T10:00:05Z",
			},
		},
		Report: ReportRow{
			TaskID:       taskID,
			JSONPath:     "/out/report.json",
			MarkdownPath: "/out/report.md",
			CreatedAt:    "2025-01-04T10:00:05Z",
		},
		Metrics: TelemetryMetrics{
			TaskID:                 taskID,
			TotalDurationMs:        12_000,
			SandboxDurationMs:      3_500,
			ToolCalls:              7,
			PermissionBlockedCount: 1,
			FindingCount:           2,
			SeverityCritical:       0,
			SeverityHigh:           1,
			SeverityMedium:         0,
			SeverityLow:            1,
			CreatedAt:              "2025-01-04T10:00:06Z",
		},
	}
}

// TestCRUDRoundTrip verifies that a fully-populated TaskReport can be saved and
// then reloaded with every field preserved. Nullable columns (exit_code,
// stdout, stderr) are exercised with both Valid and Invalid values.
func TestCRUDRoundTrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	const taskID = "cr-roundtrip-001"

	want := sampleTaskReport(taskID)
	if err := st.SaveTaskReport(ctx, want); err != nil {
		t.Fatalf("SaveTaskReport: %v", err)
	}

	got, err := st.LoadTaskReport(ctx, taskID)
	if err != nil {
		t.Fatalf("LoadTaskReport: %v", err)
	}

	// ReviewTask has no auto-generated fields, so compare directly.
	if got.Task != want.Task {
		t.Errorf("ReviewTask mismatch:\n got  %+v\n want %+v", got.Task, want.Task)
	}

	// All child rows have an auto-generated INTEGER PRIMARY KEY id. Zero those
	// ids out and use reflect.DeepEqual for the rest. reflect.DeepEqual handles
	// sql.NullInt64 / sql.NullString correctly (they are plain structs). This
	// keeps the test readable and its cyclomatic complexity well under 20.
	assertChildRowsEqual(t, got, &want)
}

// assertChildRowsEqual compares the child slices/structs of a loaded
// TaskReport against the expected values, ignoring auto-generated IDs. It
// first verifies that every loaded ID is non-zero (proving the rows were
// actually inserted and assigned a primary key), then zeros both sides and
// uses reflect.DeepEqual for a field-wise comparison.
func assertChildRowsEqual(t *testing.T, got, want *TaskReport) {
	t.Helper()

	// Findings
	if len(got.Findings) != len(want.Findings) {
		t.Fatalf("Findings len = %d, want %d", len(got.Findings), len(want.Findings))
	}
	for i := range got.Findings {
		if got.Findings[i].ID == 0 {
			t.Errorf("Findings[%d].ID = 0, want non-zero", i)
		}
		got.Findings[i].ID = 0
	}
	if !reflect.DeepEqual(got.Findings, want.Findings) {
		t.Errorf("Findings mismatch:\n got  %+v\n want %+v", got.Findings, want.Findings)
	}

	// SandboxRuns
	if len(got.SandboxRuns) != len(want.SandboxRuns) {
		t.Fatalf("SandboxRuns len = %d, want %d", len(got.SandboxRuns), len(want.SandboxRuns))
	}
	for i := range got.SandboxRuns {
		if got.SandboxRuns[i].ID == 0 {
			t.Errorf("SandboxRuns[%d].ID = 0, want non-zero", i)
		}
		got.SandboxRuns[i].ID = 0
	}
	if !reflect.DeepEqual(got.SandboxRuns, want.SandboxRuns) {
		t.Errorf("SandboxRuns mismatch:\n got  %+v\n want %+v", got.SandboxRuns, want.SandboxRuns)
	}

	// Permissions
	if len(got.Permissions) != len(want.Permissions) {
		t.Fatalf("Permissions len = %d, want %d", len(got.Permissions), len(want.Permissions))
	}
	for i := range got.Permissions {
		if got.Permissions[i].ID == 0 {
			t.Errorf("Permissions[%d].ID = 0, want non-zero", i)
		}
		got.Permissions[i].ID = 0
	}
	if !reflect.DeepEqual(got.Permissions, want.Permissions) {
		t.Errorf("Permissions mismatch:\n got  %+v\n want %+v", got.Permissions, want.Permissions)
	}

	assertArtifactsEqual(t, got, want)

	// Report
	if got.Report.ID == 0 {
		t.Errorf("Report.ID = 0, want non-zero")
	}
	got.Report.ID = 0
	if !reflect.DeepEqual(got.Report, want.Report) {
		t.Errorf("Report mismatch:\n got  %+v\n want %+v", got.Report, want.Report)
	}

	// Metrics
	if got.Metrics.ID == 0 {
		t.Errorf("Metrics.ID = 0, want non-zero")
	}
	got.Metrics.ID = 0
	if !reflect.DeepEqual(got.Metrics, want.Metrics) {
		t.Errorf("TelemetryMetrics mismatch:\n got  %+v\n want %+v", got.Metrics, want.Metrics)
	}
}

// assertArtifactsEqual compares the artifact slices of got and want, ignoring
// auto-generated IDs. Extracted from assertChildRowsEqual to keep its
// cyclomatic complexity under 20.
func assertArtifactsEqual(t *testing.T, got, want *TaskReport) {
	t.Helper()
	if len(got.Artifacts) != len(want.Artifacts) {
		t.Fatalf("Artifacts len = %d, want %d", len(got.Artifacts), len(want.Artifacts))
	}
	for i := range got.Artifacts {
		if got.Artifacts[i].ID == 0 {
			t.Errorf("Artifacts[%d].ID = 0, want non-zero", i)
		}
		got.Artifacts[i].ID = 0
	}
	if !reflect.DeepEqual(got.Artifacts, want.Artifacts) {
		t.Errorf("Artifacts mismatch:\n got  %+v\n want %+v", got.Artifacts, want.Artifacts)
	}
}

// TestForeignKeyConstraint verifies that PRAGMA foreign_keys=ON is in effect:
// inserting a finding that references a non-existent task_id must fail.
func TestForeignKeyConstraint(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	db := st.db

	_, err := db.ExecContext(ctx, `
INSERT INTO finding
(task_id, severity, category, file, line, title, evidence, recommendation,
 confidence, source, rule_id, fingerprint, created_at)
VALUES ('nonexistent', 'high', 'security', 'a.go', 1, 't', 'e', 'r',
        0.5, 'src', 'RULE', 'fp-x', '2025-01-04T00:00:00Z');`)
	if err == nil {
		t.Fatal("INSERT into finding with non-existent task_id should have failed (FK ON), got nil")
	}
}

// TestLoadTaskReport_Missing verifies that loading a task_id that does not
// exist returns ErrTaskNotFound.
func TestLoadTaskReport_Missing(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	_, err := st.LoadTaskReport(ctx, "does-not-exist")
	if err == nil {
		t.Fatal("LoadTaskReport on missing task should return an error, got nil")
	}
	if !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("LoadTaskReport error = %v, want ErrTaskNotFound (errors.Is)", err)
	}
}

// TestListTasks inserts three tasks with distinct created_at timestamps and
// verifies that ListTasks returns them newest-first and honours the limit.
func TestListTasks(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	// Three tasks with increasing created_at timestamps. We save them in a
	// non-sorted order to make sure ListTasks sorts by created_at, not by
	// insertion order.
	ids := []string{"cr-list-1", "cr-list-2", "cr-list-3"}
	times := []string{
		"2025-01-03T00:00:00Z", // oldest
		"2025-01-01T00:00:00Z", // middle (saved 2nd)
		"2025-01-05T00:00:00Z", // newest
	}
	for i, id := range ids {
		r := sampleTaskReport(id)
		r.Task.CreatedAt = times[i]
		// Keep child created_at values in the same order so they remain
		// internally consistent. Fingerprints must also be unique per task
		// because the fingerprint column has a global UNIQUE constraint and
		// SaveTaskReport deduplicates on it via INSERT OR IGNORE.
		for j := range r.Findings {
			r.Findings[j].CreatedAt = times[i]
			r.Findings[j].Fingerprint = "fp-" + id + "-" + r.Findings[j].Fingerprint
		}
		if err := st.SaveTaskReport(ctx, r); err != nil {
			t.Fatalf("SaveTaskReport(%s): %v", id, err)
		}
	}

	// limit = 2: should return the two newest tasks.
	got, err := st.ListTasks(ctx, 2)
	if err != nil {
		t.Fatalf("ListTasks(2): %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListTasks(2) returned %d rows, want 2", len(got))
	}
	// Newest first: cr-list-3 (2025-01-05), then cr-list-1 (2025-01-03).
	if got[0].TaskID != "cr-list-3" {
		t.Errorf("ListTasks[0].TaskID = %q, want %q", got[0].TaskID, "cr-list-3")
	}
	if got[0].CreatedAt != "2025-01-05T00:00:00Z" {
		t.Errorf("ListTasks[0].CreatedAt = %q, want newest", got[0].CreatedAt)
	}
	if got[1].TaskID != "cr-list-1" {
		t.Errorf("ListTasks[1].TaskID = %q, want %q", got[1].TaskID, "cr-list-1")
	}
	// Each task has 2 findings (inherited from sampleTaskReport).
	if got[0].FindingCount != 2 {
		t.Errorf("ListTasks[0].FindingCount = %d, want 2", got[0].FindingCount)
	}

	// limit = 3: should return all three, newest first.
	gotAll, err := st.ListTasks(ctx, 3)
	if err != nil {
		t.Fatalf("ListTasks(3): %v", err)
	}
	if len(gotAll) != 3 {
		t.Fatalf("ListTasks(3) returned %d rows, want 3", len(gotAll))
	}
	wantOrder := []string{"cr-list-3", "cr-list-1", "cr-list-2"}
	for i, want := range wantOrder {
		if gotAll[i].TaskID != want {
			t.Errorf("ListTasks(3)[%d].TaskID = %q, want %q", i, gotAll[i].TaskID, want)
		}
	}
}

// TestSaveTaskReport_RollbackOnError verifies that when SaveTaskReport hits an
// error mid-transaction, no partial rows survive.
//
// The trigger is a FOREIGN KEY violation: the second finding references a
// task_id that does not exist. SQLite always aborts on FK violations (even
// with INSERT OR IGNORE, since ON CONFLICT does not apply to FK constraints),
// so the second finding insert fails and the whole transaction must roll
// back, leaving zero findings and zero review_task rows.
//
// Note: the task brief suggested an empty rule_id NOT NULL violation, but the
// Finding struct uses a plain Go string. An empty string "" is a valid
// (non-NULL) TEXT value in SQLite, so it would not actually trigger a NOT
// NULL violation. The FK-violation trigger below achieves the same goal:
// assert that SaveTaskReport errors AND that no partial rows survived.
func TestSaveTaskReport_RollbackOnError(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	const taskID = "cr-rollback-001"

	r := sampleTaskReport(taskID)
	// Replace the findings: the first is well-formed, the second references a
	// non-existent task_id to provoke a FK violation.
	r.Findings = []Finding{
		{
			TaskID:         taskID,
			Severity:       "high",
			Category:       "security",
			File:           "a.go",
			Line:           1,
			Title:          "ok",
			Evidence:       "e",
			Recommendation: "r",
			Confidence:     0.5,
			Source:         "src",
			RuleID:         "R1",
			Fingerprint:    "fp-good",
			CreatedAt:      "2025-01-04T00:00:00Z",
		},
		{
			TaskID:         "nonexistent-task", // FK violation: not in review_task
			Severity:       "low",
			Category:       "style",
			File:           "b.go",
			Line:           2,
			Title:          "bad",
			Evidence:       "e",
			Recommendation: "r",
			Confidence:     0.4,
			Source:         "src",
			RuleID:         "R2",
			Fingerprint:    "fp-bad",
			CreatedAt:      "2025-01-04T00:00:01Z",
		},
	}

	if err := st.SaveTaskReport(ctx, r); err == nil {
		t.Fatal("SaveTaskReport should have returned an error (FK violation), got nil")
	}

	// No partial rows should have survived the rollback.
	var findingCount, taskCount int
	db := st.db
	if err := db.QueryRowContext(ctx,
		"SELECT count(*) FROM finding WHERE task_id = ?", taskID,
	).Scan(&findingCount); err != nil {
		t.Fatalf("count findings: %v", err)
	}
	if findingCount != 0 {
		t.Errorf("finding count = %d, want 0 (transaction should have rolled back)", findingCount)
	}
	if err := db.QueryRowContext(ctx,
		"SELECT count(*) FROM review_task WHERE task_id = ?", taskID,
	).Scan(&taskCount); err != nil {
		t.Fatalf("count review_task: %v", err)
	}
	if taskCount != 0 {
		t.Errorf("review_task count = %d, want 0 (transaction should have rolled back)", taskCount)
	}
}

// TestNewTaskID_Uniqueness sanity-checks that NewTaskID returns ids that look
// like "cr-<ts>-<hash>-<nonce>" and that two rapid calls produce distinct ids
// (thanks to the random nonce).
func TestNewTaskID_Uniqueness(t *testing.T) {
	id1 := NewTaskID("/repo/a")
	id2 := NewTaskID("/repo/a")

	if !startsWith(id1, "cr-") {
		t.Errorf("NewTaskID = %q, want prefix %q", id1, "cr-")
	}
	if id1 == id2 {
		t.Errorf("two NewTaskID calls produced the same id %q; nonce should differ", id1)
	}
}

// startsWith reports whether s begins with prefix.
func startsWith(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	return s[:len(prefix)] == prefix
}

// TestInitRecordsSchemaVersion verifies that Init inserts the current
// schema version into schema_migrations. Borrowed from competitor PR
// #2243.
func TestInitRecordsSchemaVersion(t *testing.T) {
	st := newTestStore(t)
	v, err := st.SchemaVersion(context.Background())
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if v != CurrentSchemaVersion {
		t.Errorf("SchemaVersion = %q, want %q", v, CurrentSchemaVersion)
	}
}

// TestMigrateIdempotent verifies that calling Migrate on an already-
// up-to-date database applies zero migrations and returns no error.
func TestMigrateIdempotent(t *testing.T) {
	st := newTestStore(t)
	n, err := st.Migrate(context.Background())
	if err != nil {
		t.Fatalf("Migrate (first call): %v", err)
	}
	if n != 0 {
		t.Errorf("first Migrate applied %d, want 0 (Init already recorded v1)", n)
	}
	// Call Migrate again — should still be a no-op.
	n, err = st.Migrate(context.Background())
	if err != nil {
		t.Fatalf("Migrate (second call): %v", err)
	}
	if n != 0 {
		t.Errorf("second Migrate applied %d, want 0", n)
	}
}

// TestSchemaVersionEmptyOnFreshDB verifies that a database with no
// schema_migrations rows returns "" (not an error). This covers the
// edge case where schema_migrations exists but is empty, which should
// not happen with the current Init but is the contract of
// SchemaVersion.
func TestSchemaVersionEmptyOnFreshDB(t *testing.T) {
	st := New(":memory:").(*sqliteStore)
	// Open + apply schema but skip the version insert by calling the
	// raw schema apply directly (simulates a pre-migration database).
	if err := st.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	// Delete the version row to simulate a pre-migration database.
	if _, err := st.db.ExecContext(context.Background(),
		`DELETE FROM schema_migrations;`); err != nil {
		t.Fatalf("delete: %v", err)
	}
	v, err := st.SchemaVersion(context.Background())
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if v != "" {
		t.Errorf("SchemaVersion = %q, want empty string", v)
	}
	// Migrate should re-apply the v1 row.
	n, err := st.Migrate(context.Background())
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if n != 1 {
		t.Errorf("Migrate applied %d, want 1 (v1 was missing)", n)
	}
	v, err = st.SchemaVersion(context.Background())
	if err != nil {
		t.Fatalf("SchemaVersion after Migrate: %v", err)
	}
	if v != CurrentSchemaVersion {
		t.Errorf("SchemaVersion after Migrate = %q, want %q", v, CurrentSchemaVersion)
	}
}
