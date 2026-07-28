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
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/reviewmodel"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/migrations"
)

const testSecret = "password=super-secret-value"
const testRedacted = "[REDACTED:named_secret:00000000]"
const testResultSHA256 = "0000000000000000000000000000000000000000000000000000000000000000"

var _ Store = (*SQLiteStore)(nil)

func TestStoreFullRoundTripAndRedaction(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t)
	started := testTime()
	createTestTask(t, database, "task-roundtrip", started)

	if err := database.SaveInputSummary(ctx, "task-roundtrip", InputSummary{
		FileCount: 2, HunkCount: 3, AddedLines: 4, Packages: []string{"example/pkg"},
	}); err != nil {
		t.Fatalf("SaveInputSummary() error = %v", err)
	}
	run := SandboxRun{ID: "run-1", CheckID: "go-test", Runtime: "container",
		Status: "passed", DurationMS: 12, Stdout: testSecret,
		ResultSHA256: testResultSHA256, ResultSizeBytes: 42}
	if err := database.SaveRun(ctx, "task-roundtrip", run); err != nil {
		t.Fatalf("SaveRun() error = %v", err)
	}
	decision := Decision{ID: "decision-1", Stage: "permission", Tool: "code_review_check",
		CheckID: "go-test", ArgsDigest: "digest", Risk: "medium", Action: "allow",
		Reason: testSecret, At: started}
	if err := database.SaveDecision(ctx, "task-roundtrip", decision); err != nil {
		t.Fatalf("SaveDecision() error = %v", err)
	}
	request := completeRequest("task-roundtrip", "run-1", started.Add(time.Second))
	if err := database.Finalize(ctx, request); err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}

	review, err := database.GetReview(ctx, "task-roundtrip")
	if err != nil {
		t.Fatalf("GetReview() error = %v", err)
	}
	assertCompleteReview(t, review)
	encoded, err := json.Marshal(review)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if strings.Contains(string(encoded), testSecret) || !strings.Contains(string(encoded), "[REDACTED:") {
		t.Fatalf("stored review redaction failed: %s", encoded)
	}
}

func TestFinalizeRollbackAndTransitions(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t)
	started := testTime()
	createTestTask(t, database, "task-rollback", started)
	request := completeRequest("task-rollback", "", started.Add(time.Second))
	request.Artifacts = nil
	request.Findings = append(request.Findings, request.Findings[0])
	if err := database.Finalize(ctx, request); err == nil {
		t.Fatal("Finalize() duplicate error = nil")
	}
	review, err := database.GetReview(ctx, "task-rollback")
	if err != nil {
		t.Fatalf("GetReview() error = %v", err)
	}
	if review.Task.Status != StatusRunning || len(review.Findings) != 0 || review.Report.JSON != "" {
		t.Fatalf("transaction was not rolled back: %#v", review)
	}
	if err := database.FailTask(ctx, FailRequest{
		TaskID: "task-rollback", Error: testSecret, FinishedAt: started.Add(2 * time.Second),
	}); err != nil {
		t.Fatalf("FailTask() error = %v", err)
	}
	if err := database.FailTask(ctx, FailRequest{
		TaskID: "task-rollback", Error: "again", FinishedAt: started.Add(3 * time.Second),
	}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("second FailTask() error = %v", err)
	}
	review, err = database.GetReview(ctx, "task-rollback")
	if err != nil || review.Task.Status != StatusFailed || strings.Contains(review.Task.Error, testSecret) {
		t.Fatalf("failed review = %#v, error = %v", review, err)
	}
}

func TestStoreConstraintsAndNotFound(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t)
	if _, err := database.GetReview(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetReview(missing) error = %v", err)
	}
	if err := database.CreateTask(ctx, Task{ID: "bad", Status: StatusCompleted}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("CreateTask(completed) error = %v", err)
	}
	if err := database.CreateTask(ctx, Task{ID: "token=secret-task-value", Status: StatusRunning,
		StartedAt: testTime()}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("CreateTask(secret ID) error = %v", err)
	}
	if err := database.SaveRun(ctx, "missing", SandboxRun{ID: "run-missing"}); err == nil {
		t.Fatal("SaveRun(missing task) error = nil")
	}
	var foreignKeys int
	if err := database.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("query foreign_keys: %v", err)
	}
	if foreignKeys != 1 || database.db.Stats().MaxOpenConnections != maxConnections {
		t.Fatalf("database settings foreign_keys=%d max=%d", foreignKeys, database.db.Stats().MaxOpenConnections)
	}
	err := database.Finalize(ctx, FinalizeRequest{
		TaskID: "missing", Status: StatusFailed,
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("Finalize(failed) error = %v", err)
	}
}

func TestSaveRunRejectsInvalidResultEvidence(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t)
	createTestTask(t, database, "task-invalid-evidence", testTime())
	for _, run := range []SandboxRun{
		{ID: "run-size-only", ResultSizeBytes: 1},
		{ID: "run-digest-only", ResultSHA256: testResultSHA256},
		{ID: "run-bad-digest", ResultSHA256: "not-a-digest", ResultSizeBytes: 1},
		{ID: "run-too-large", ResultSHA256: testResultSHA256, ResultSizeBytes: (160 << 10) + 1},
	} {
		if err := database.SaveRun(ctx, "task-invalid-evidence", run); err == nil {
			t.Fatalf("SaveRun(%s) error = nil", run.ID)
		}
	}
}

func TestFinalizeRejectsTemporaryResultArtifact(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t)
	createTestTask(t, database, "task-temp-artifact", testTime())
	request := completeRequest(
		"task-temp-artifact",
		"",
		testTime().Add(time.Second),
	)
	request.Artifacts = []Artifact{{
		ID:        "temporary-result",
		Kind:      "check-result",
		Path:      "out/result.json",
		SHA256:    testResultSHA256,
		SizeBytes: 42,
		CreatedAt: testTime(),
	}}
	if err := database.Finalize(ctx, request); err == nil ||
		!strings.Contains(err.Error(), "cannot be persisted") {
		t.Fatalf("Finalize(temporary artifact) error = %v", err)
	}
	review, err := database.GetReview(ctx, "task-temp-artifact")
	if err != nil || review.Task.Status != StatusRunning ||
		len(review.Artifacts) != 0 || len(review.Findings) != 0 {
		t.Fatalf("rolled-back review = %#v, %v", review, err)
	}
}

func TestLegacyDatabaseBackfillsResultEvidence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy-data.db")
	raw := createLegacyDatabase(t, path)
	if _, err := raw.ExecContext(ctx, `
		INSERT INTO review_tasks
			(id,status,input_kind,input_digest,started_at,conclusion,error)
			VALUES('legacy-task','running','','','2026-01-01T00:00:00Z','','');
		INSERT INTO sandbox_runs
			(id,task_id,check_id,runtime,status,duration_ms,exit_code,
			 timed_out,output_truncated,stdout,stderr,error_type,error)
			VALUES('legacy-run','legacy-task','','','passed',0,0,0,0,'','','','');
		INSERT INTO artifacts
			(id,task_id,run_id,kind,path,sha256,size_bytes,created_at)
			VALUES(
				'legacy-result','legacy-task','legacy-run','check-result',
				'out/result.json','`+testResultSHA256+`',42,
				'2026-01-01T00:00:00Z'
			);
		INSERT INTO artifacts
			(id,task_id,run_id,kind,path,sha256,size_bytes,created_at)
			VALUES(
				'durable-log','legacy-task','legacy-run','audit-log',
				'audit.log','`+testResultSHA256+`',7,
				'2026-01-01T00:00:00Z'
			);
		INSERT INTO reports
			(task_id,schema_version,conclusion,canonical_json,
			 canonical_markdown,json_path,json_sha256,markdown_path,
			 markdown_sha256)
			VALUES(
				'legacy-task','1','',
				'{"sandbox_runs":[{"id":"legacy-run"}],
				  "artifacts":[{"id":"legacy-result","kind":"check-result",
				  "path":"out/result.json"},{"id":"durable-log",
				  "kind":"audit-log","path":"audit.log"}]}',
				'old report

## Sandbox runs
- go-test: passed, 0 ms, exit=0, timeout=false, truncated=false

## Monitoring

## Artifacts
- check-result: out/result.json
- audit-log: audit.log',
				'old.json','old-json-digest','old.md','old-md-digest'
			);
	`); err != nil {
		t.Fatalf("insert legacy data: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	database, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open(legacy data) error = %v", err)
	}
	defer database.Close()
	review, err := database.GetReview(ctx, "legacy-task")
	if err != nil {
		t.Fatalf("GetReview(legacy data) error = %v", err)
	}
	if len(review.Runs) != 1 ||
		review.Runs[0].ResultSHA256 != testResultSHA256 ||
		review.Runs[0].ResultSizeBytes != 42 {
		t.Fatalf("legacy run evidence = %#v", review.Runs)
	}
	if len(review.Artifacts) != 1 ||
		review.Artifacts[0].ID != "durable-log" ||
		strings.Contains(review.Report.JSON, "out/result.json") ||
		strings.Contains(review.Report.Markdown, "out/result.json") ||
		!strings.Contains(review.Report.JSON, testResultSHA256) ||
		!strings.Contains(review.Report.JSON, "audit.log") ||
		!strings.Contains(review.Report.Markdown, "audit.log") ||
		!strings.Contains(review.Report.Markdown, "result=42 bytes") ||
		review.Report.SchemaVersion != evidenceReportSchemaVersion ||
		!strings.Contains(
			review.Report.JSON,
			`"schema_version": "`+evidenceReportSchemaVersion+`"`,
		) ||
		review.Report.JSONPath != "" || review.Report.MarkdownPath != "" ||
		review.Report.JSONSHA256 == "" ||
		review.Report.MarkdownSHA256 == "" {
		t.Fatalf("stale legacy output remained: %#v", review)
	}
}

func TestLegacyDatabaseRejectsInvalidResultEvidence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy-invalid.db")
	raw := createLegacyDatabase(t, path)
	if _, err := raw.ExecContext(ctx, `
		INSERT INTO review_tasks
			(id,status,input_kind,input_digest,started_at,conclusion,error)
			VALUES('legacy-task','running','','','2026-01-01T00:00:00Z','','');
		INSERT INTO sandbox_runs
			(id,task_id,check_id,runtime,status,duration_ms,exit_code,
			 timed_out,output_truncated,stdout,stderr,error_type,error)
			VALUES('legacy-run','legacy-task','','','passed',0,0,0,0,'','','','');
		INSERT INTO artifacts
			(id,task_id,run_id,kind,path,sha256,size_bytes,created_at)
			VALUES(
				'legacy-result','legacy-task','legacy-run','check-result',
				'out/result.json','not-a-digest',42,
				'2026-01-01T00:00:00Z'
			);
	`); err != nil {
		t.Fatalf("insert invalid legacy data: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}
	database, err := Open(ctx, path)
	if database != nil {
		_ = database.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "invalid sandbox result evidence") {
		t.Fatalf("Open(invalid legacy data) = %#v, %v", database, err)
	}
}

func TestMigrateLegacyReportJSONRejectsNullDocument(t *testing.T) {
	if _, err := migrateLegacyReportJSON(
		"null",
		legacyResultEvidence{},
	); err == nil || !strings.Contains(err.Error(), "must be an object") {
		t.Fatalf("migrateLegacyReportJSON(null) error = %v", err)
	}
}

func TestLegacyDatabaseRejectsCrossTaskResultArtifact(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy-cross-task.db")
	raw := createLegacyDatabase(t, path)
	if _, err := raw.ExecContext(ctx, `
		INSERT INTO review_tasks
			(id,status,input_kind,input_digest,started_at,conclusion,error)
			VALUES
				('task-one','running','','','2026-01-01T00:00:00Z','',''),
				('task-two','running','','','2026-01-01T00:00:00Z','','');
		INSERT INTO sandbox_runs
			(id,task_id,check_id,runtime,status,duration_ms,exit_code,
			 timed_out,output_truncated,stdout,stderr,error_type,error)
			VALUES('run-two','task-two','','','passed',0,0,0,0,'','','','');
		INSERT INTO artifacts
			(id,task_id,run_id,kind,path,sha256,size_bytes,created_at)
			VALUES(
				'cross-task','task-one','run-two','check-result',
				'out/result.json','`+testResultSHA256+`',42,
				'2026-01-01T00:00:00Z'
			);
	`); err != nil {
		t.Fatalf("insert cross-task legacy data: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}
	database, err := Open(ctx, path)
	if database != nil {
		_ = database.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "crosses review tasks") {
		t.Fatalf("Open(cross-task legacy data) = %#v, %v", database, err)
	}
}

func TestConcurrentLegacySchemaMigration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy-empty.db")
	if err := createLegacyDatabase(t, path).Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			database, err := Open(ctx, path)
			if database != nil {
				err = errors.Join(err, database.Close())
			}
			results <- err
		}()
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent Open() error = %v", err)
		}
	}
}

func TestMemoryStoresAreIsolated(t *testing.T) {
	ctx := context.Background()
	first := openTestStore(t)
	second := openTestStore(t)
	createTestTask(t, first, "task-isolated", testTime())
	if _, err := second.GetReview(ctx, "task-isolated"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second GetReview() error = %v", err)
	}
}

func createLegacyDatabase(t *testing.T, path string) *sql.DB {
	t.Helper()
	legacySchema := strings.Replace(
		migrations.InitialSchema,
		"    error TEXT NOT NULL,\n"+
			"    result_sha256 TEXT NOT NULL DEFAULT '',\n"+
			"    result_size_bytes INTEGER NOT NULL DEFAULT 0 CHECK (result_size_bytes >= 0)\n",
		"    error TEXT NOT NULL\n",
		1,
	)
	if legacySchema == migrations.InitialSchema {
		t.Fatal("legacy schema replacement did not match")
	}
	database, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("sql.Open(legacy) error = %v", err)
	}
	if _, err := database.Exec(legacySchema); err != nil {
		_ = database.Close()
		t.Fatalf("create legacy schema: %v", err)
	}
	return database
}

func TestTaskIDRoundTripsWithoutRedaction(t *testing.T) {
	ctx := context.Background()
	database := openTestStore(t)
	const taskID = "task-Case_123"
	createTestTask(t, database, taskID, testTime())
	if err := database.SaveRun(ctx, taskID, SandboxRun{ID: "run-task-id", Status: "passed"}); err != nil {
		t.Fatalf("SaveRun() error = %v", err)
	}
	review, err := database.GetReview(ctx, taskID)
	if err != nil || review.Task.ID != taskID || len(review.Runs) != 1 {
		t.Fatalf("GetReview() = %#v, %v", review, err)
	}
}

func TestReviewLoadersUseOneSQLiteSnapshot(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "review.db")
	reader, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open(reader) error = %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	writer, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open(writer) error = %v", err)
	}
	t.Cleanup(func() { _ = writer.Close() })

	const taskID = "task-snapshot"
	createTestTask(t, reader, taskID, testTime())
	request := completeRequest(taskID, "run-snapshot", testTime().Add(time.Second))
	if err := reader.SaveRun(ctx, taskID, SandboxRun{ID: "run-snapshot", Status: "passed"}); err != nil {
		t.Fatalf("SaveRun() error = %v", err)
	}
	firstRead := make(chan struct{})
	prepared := make(chan error, 1)
	allowCommit := make(chan struct{})
	releaseCommit := sync.OnceFunc(func() { close(allowCommit) })
	defer releaseCommit()
	finalized := make(chan error, 1)
	usedTransaction := false
	reader.loadSnapshot = func(ctx context.Context, queryer reviewQueryer, id string) (Review, error) {
		_, usedTransaction = queryer.(*sql.Tx)
		task, loadErr := loadTask(ctx, queryer, id)
		close(firstRead)
		if loadErr != nil || task.Status != StatusRunning {
			return Review{}, errors.Join(loadErr, errors.New("first snapshot read was not running"))
		}
		if prepareErr := <-prepared; prepareErr != nil {
			return Review{}, prepareErr
		}
		return loadReview(ctx, queryer, id)
	}
	go func() {
		<-firstRead
		tx, txErr := writer.db.BeginTx(ctx, nil)
		if txErr == nil {
			txErr = insertFindings(ctx, tx, request.TaskID, request.Findings)
		}
		if txErr == nil {
			txErr = insertMetrics(ctx, tx, request.TaskID, request.Metrics)
		}
		if txErr == nil {
			txErr = insertArtifacts(ctx, tx, request.TaskID, request.Artifacts)
		}
		if txErr == nil {
			txErr = insertReport(ctx, tx, request.TaskID, request.Report)
		}
		if txErr == nil {
			txErr = finishTask(ctx, tx, request)
		}
		prepared <- txErr
		if txErr != nil {
			if tx != nil {
				_ = tx.Rollback()
			}
			finalized <- txErr
			return
		}
		<-allowCommit
		finalized <- tx.Commit()
	}()

	before, err := reader.GetReview(ctx, taskID)
	if err != nil || !usedTransaction || before.Task.Status != StatusRunning || len(before.Findings) != 0 {
		t.Fatalf("pre-commit review = %#v, %v", before, err)
	}
	releaseCommit()
	if err := <-finalized; err != nil {
		t.Fatalf("commit finalization: %v", err)
	}
	reader.loadSnapshot = nil
	after, err := reader.GetReview(ctx, taskID)
	if err != nil || after.Task.Status != StatusCompleted || len(after.Findings) != 1 {
		t.Fatalf("post-commit review = %#v, %v", after, err)
	}
}

func openTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return database
}

func createTestTask(t *testing.T, database *SQLiteStore, id string, started time.Time) {
	t.Helper()
	err := database.CreateTask(context.Background(), Task{ID: id,
		Status: StatusRunning, InputKind: "fixture", InputDigest: "digest", StartedAt: started})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
}

func completeRequest(taskID, runID string, finished time.Time) FinalizeRequest {
	finding := reviewmodel.Finding{Bucket: reviewmodel.BucketFindings, Severity: "high",
		Category: "security", File: "config.go", Line: 3, Title: "secret",
		Evidence: testSecret, Recommendation: "remove secret", Confidence: 0.95,
		Source: "patch", RuleID: "GO-SECRET-001"}
	return FinalizeRequest{TaskID: taskID, Status: StatusCompleted,
		Conclusion: "changes requested", Findings: []reviewmodel.Finding{finding},
		Metrics: Metrics{TotalDurationMS: 20, SandboxDurationMS: 12, ToolCalls: 1,
			FindingCount: 1, SeverityCounts: map[string]int{"high": 1}, ErrorTypeCounts: map[string]int{}},
		Artifacts: []Artifact{{ID: "artifact-1", RunID: runID, Kind: "audit-log",
			Path: "audit.log", SHA256: "artifact-digest", SizeBytes: 10, CreatedAt: finished}},
		Report: Report{SchemaVersion: "1", Conclusion: "changes requested",
			JSON: `{"evidence":"` + testRedacted + `"}`, Markdown: testRedacted,
			JSONPath: "review_report.json", JSONSHA256: "json-digest",
			MarkdownPath: "review_report.md", MarkdownSHA256: "markdown-digest"}, FinishedAt: finished}
}

func assertCompleteReview(t *testing.T, review Review) {
	t.Helper()
	if review.Task.Status != StatusCompleted || review.Task.FinishedAt == nil {
		t.Fatalf("task = %#v", review.Task)
	}
	if len(review.Runs) != 1 || len(review.Decisions) != 1 || len(review.Findings) != 1 ||
		len(review.Artifacts) != 1 || review.Metrics.FindingCount != 1 || review.Report.JSON == "" {
		t.Fatalf("incomplete review = %#v", review)
	}
	if review.Runs[0].ResultSHA256 != testResultSHA256 || review.Runs[0].ResultSizeBytes != 42 {
		t.Fatalf("sandbox result evidence = %#v", review.Runs[0])
	}
}

func testTime() time.Time {
	return time.Date(2026, time.July, 22, 8, 0, 0, 0, time.UTC)
}
