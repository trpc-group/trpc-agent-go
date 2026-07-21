//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/persistence"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/store"

	_ "modernc.org/sqlite"
)

func TestSubmitReviewResultsReplacesCompleteSnapshot(t *testing.T) {
	ctx := context.Background()
	reviewStore, closeStore, _ := openReviewStore(t, ctx)
	defer closeStore()
	saveReviewTask(t, ctx, reviewStore, "replace-results")

	first := []store.ReviewResultRecord{
		reviewResult("finding", "security", "command.go", 13, "GO-SEC-001"),
		reviewResult("warning", "tests", "command_test.go", 0, "GO-TEST-001"),
	}
	counts, err := reviewStore.SubmitReviewResults(
		ctx,
		"replace-results",
		first,
		"First complete result.",
	)
	if err != nil {
		t.Fatal(err)
	}
	if counts != (store.ReviewResultCounts{FindingCount: 1, WarningCount: 1}) {
		t.Fatalf("first committed counts = %#v", counts)
	}

	second := []store.ReviewResultRecord{
		reviewResult(
			"needs_human_review",
			"correctness",
			"generated.go",
			0,
			"GO-CORR-001",
		),
	}
	counts, err = reviewStore.SubmitReviewResults(
		ctx,
		"replace-results",
		second,
		"Replacement result.",
	)
	if err != nil {
		t.Fatal(err)
	}
	if counts != (store.ReviewResultCounts{HumanReviewCount: 1}) {
		t.Fatalf("replacement committed counts = %#v", counts)
	}

	// Retrying the complete snapshot replaces the prior projection instead of
	// accumulating line=0 records.
	if _, err := reviewStore.SubmitReviewResults(
		ctx,
		"replace-results",
		second,
		"Replacement result.",
	); err != nil {
		t.Fatal(err)
	}
	snapshot, err := reviewStore.LoadTaskSnapshot(ctx, "replace-results")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Task.Conclusion != "Replacement result." {
		t.Fatalf("conclusion = %q", snapshot.Task.Conclusion)
	}
	if len(snapshot.Results) != 1 {
		t.Fatalf("results = %#v, want one replacement result", snapshot.Results)
	}
	got := snapshot.Results[0]
	if got.ResultKind != "needs_human_review" ||
		got.File != "generated.go" ||
		got.Line != 0 ||
		got.RuleID != "GO-CORR-001" {
		t.Fatalf("replacement result = %#v", got)
	}

	counts, err = reviewStore.SubmitReviewResults(
		ctx,
		"replace-results",
		nil,
		"No actionable issues remain.",
	)
	if err != nil {
		t.Fatal(err)
	}
	if counts != (store.ReviewResultCounts{}) {
		t.Fatalf("clean committed counts = %#v", counts)
	}
	snapshot, err = reviewStore.LoadTaskSnapshot(ctx, "replace-results")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Task.Conclusion != "No actionable issues remain." ||
		len(snapshot.Results) != 0 {
		t.Fatalf("clean submission retained stale results: %#v", snapshot)
	}
}

func TestSubmitReviewResultsConstraintFailurePreservesPriorSnapshot(t *testing.T) {
	ctx := context.Background()
	reviewStore, closeStore, _ := openReviewStore(t, ctx)
	defer closeStore()
	saveReviewTask(t, ctx, reviewStore, "constraint-rollback")

	baseline := []store.ReviewResultRecord{
		reviewResult("finding", "security", "command.go", 13, "GO-SEC-001"),
	}
	if _, err := reviewStore.SubmitReviewResults(
		ctx,
		"constraint-rollback",
		baseline,
		"Baseline conclusion.",
	); err != nil {
		t.Fatal(err)
	}

	conflicting := []store.ReviewResultRecord{
		reviewResult("finding", "security", "worker.go", 20, "GO-CONC-001"),
		reviewResult("warning", "concurrency", "worker.go", 20, "GO-CONC-001"),
	}
	if _, err := reviewStore.SubmitReviewResults(
		ctx,
		"constraint-rollback",
		conflicting,
		"Conflicting replacement.",
	); err == nil {
		t.Fatal("conflicting location/rule identity unexpectedly committed")
	}

	snapshot, err := reviewStore.LoadTaskSnapshot(ctx, "constraint-rollback")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Task.Conclusion != "Baseline conclusion." ||
		len(snapshot.Results) != 1 ||
		snapshot.Results[0].File != "command.go" {
		t.Fatalf("failed replacement changed prior snapshot: %#v", snapshot)
	}
}

func TestSubmitReviewResultsRejectsNegativeLineAndRollsBack(t *testing.T) {
	ctx := context.Background()
	reviewStore, closeStore, _ := openReviewStore(t, ctx)
	defer closeStore()
	saveReviewTask(t, ctx, reviewStore, "negative-line")

	result := reviewResult("finding", "correctness", "math.go", -1, "GO-CORR-001")
	if _, err := reviewStore.SubmitReviewResults(
		ctx,
		"negative-line",
		[]store.ReviewResultRecord{result},
		"Must not commit.",
	); err == nil {
		t.Fatal("negative line unexpectedly committed")
	}
	snapshot, err := reviewStore.LoadTaskSnapshot(ctx, "negative-line")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Task.Conclusion != "" || len(snapshot.Results) != 0 {
		t.Fatalf("negative line left a partial snapshot: %#v", snapshot)
	}
}

func TestSubmitReviewResultsMissingTaskLeavesNoRows(t *testing.T) {
	ctx := context.Background()
	reviewStore, closeStore, dbPath := openReviewStore(t, ctx)
	defer closeStore()

	if _, err := reviewStore.SubmitReviewResults(
		ctx,
		"missing-task",
		[]store.ReviewResultRecord{
			reviewResult("finding", "security", "command.go", 13, "GO-SEC-001"),
		},
		"Must not commit.",
	); err == nil {
		t.Fatal("missing task unexpectedly accepted a result snapshot")
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM review_results WHERE task_id = ?`,
		"missing-task",
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("missing task left %d review result rows", count)
	}
}

func TestReviewResultsSchemaUsesLocationAndRuleIdentity(t *testing.T) {
	ctx := context.Background()
	_, closeStore, dbPath := openReviewStore(t, ctx)
	defer closeStore()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, `PRAGMA table_info(review_results)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			position   int
			name       string
			columnType string
			notNull    int
			defaultVal sql.NullString
			primaryKey int
		)
		if err := rows.Scan(
			&position,
			&name,
			&columnType,
			&notNull,
			&defaultVal,
			&primaryKey,
		); err != nil {
			t.Fatal(err)
		}
		if name == "dedupe_key" {
			t.Fatal("review_results still contains the opaque dedupe_key column")
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	var indexSQL string
	if err := db.QueryRowContext(ctx, `
SELECT sql FROM sqlite_master
WHERE type = 'index' AND name = 'review_results_task_location_rule_unique'`,
	).Scan(&indexSQL); err != nil {
		t.Fatal(err)
	}
	normalized := strings.Join(strings.Fields(strings.ToLower(indexSQL)), " ")
	if !strings.Contains(
		normalized,
		"on review_results (task_id, file_path, line, rule_id) where line > 0",
	) {
		t.Fatalf("location/rule index SQL = %q", indexSQL)
	}
}

func openReviewStore(
	t *testing.T,
	ctx context.Context,
) (*store.SQLite, func(), string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "review.db")
	resources, err := persistence.Open(
		ctx,
		dbPath,
		redact.AppendEventHook(redact.New()),
	)
	if err != nil {
		t.Fatal(err)
	}
	return resources.ReviewStore, func() {
		if err := resources.Close(); err != nil {
			t.Errorf("close persistence resources: %v", err)
		}
	}, dbPath
}

func saveReviewTask(
	t *testing.T,
	ctx context.Context,
	reviewStore *store.SQLite,
	taskID string,
) {
	t.Helper()
	if err := reviewStore.SaveTask(ctx, store.ReviewTaskRecord{
		TaskID:  taskID,
		AppName: "code_review_agent",
		UserID:  "reviewer",
		Status:  "running",
	}); err != nil {
		t.Fatal(err)
	}
}

func reviewResult(
	kind string,
	category string,
	file string,
	line int,
	ruleID string,
) store.ReviewResultRecord {
	return store.ReviewResultRecord{
		ResultKind:     kind,
		Severity:       "high",
		Category:       category,
		File:           file,
		Line:           line,
		Title:          "Review result",
		Evidence:       "Concrete evidence.",
		Recommendation: "Apply the corresponding fix.",
		Confidence:     0.95,
		Source:         "agent",
		RuleID:         ruleID,
	}
}
