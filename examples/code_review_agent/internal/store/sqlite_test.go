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
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/reviewmodel"
)

const testSecret = "password=super-secret-value"
const testRedacted = "[REDACTED:named_secret:00000000]"

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
		Status: "passed", DurationMS: 12, Stdout: testSecret}
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

func TestMemoryStoresAreIsolated(t *testing.T) {
	ctx := context.Background()
	first := openTestStore(t)
	second := openTestStore(t)
	createTestTask(t, first, "task-isolated", testTime())
	if _, err := second.GetReview(ctx, "task-isolated"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second GetReview() error = %v", err)
	}
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
		Artifacts: []Artifact{{ID: "artifact-1", RunID: runID, Kind: "check-result",
			Path: "result.json", SHA256: "artifact-digest", SizeBytes: 10, CreatedAt: finished}},
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
}

func testTime() time.Time {
	return time.Date(2026, time.July, 22, 8, 0, 0, 0, time.UTC)
}
