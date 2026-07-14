//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package storage

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"

	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/findings"
)

//go:embed schema.sql
var schemaSQL string

// SQLiteStore implements Store with SQLite.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore opens a SQLite-backed store.
func NewSQLiteStore(dsn string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	return &SQLiteStore{db: db}, nil
}

// Init creates schema tables.
func (s *SQLiteStore) Init(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("init schema: %w", err)
	}
	return s.migrate(ctx)
}

func (s *SQLiteStore) migrate(ctx context.Context) error {
	// Best-effort column adds for databases created before Phase 2 metrics.
	alters := []string{
		`ALTER TABLE review_metrics ADD COLUMN sandbox_duration_ms INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE review_metrics ADD COLUMN tool_call_count INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE review_metrics ADD COLUMN permission_deny_count INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE review_metrics ADD COLUMN exception_json TEXT NOT NULL DEFAULT '{}'`,
		`ALTER TABLE sandbox_runs ADD COLUMN runtime TEXT NOT NULL DEFAULT 'local'`,
		`ALTER TABLE sandbox_runs ADD COLUMN error_type TEXT`,
	}
	for _, stmt := range alters {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			if !isSQLiteDuplicateColumn(err) {
				return fmt.Errorf("migrate: %w", err)
			}
		}
	}
	return nil
}

func isSQLiteDuplicateColumn(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "duplicate column name")
}

// SaveReview persists a review record and related rows.
func (s *SQLiteStore) SaveReview(ctx context.Context, review *ReviewRecord) error {
	if review == nil {
		return fmt.Errorf("review is nil")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx, `
INSERT INTO review_tasks (id, status, input_summary, repo_path, created_at, finished_at, duration_ms)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		review.TaskID,
		review.Status,
		review.InputSummary,
		review.RepoPath,
		review.CreatedAt.UTC().Format(time.RFC3339),
		review.FinishedAt.UTC().Format(time.RFC3339),
		review.DurationMs,
	)
	if err != nil {
		return fmt.Errorf("insert task: %w", err)
	}

	for _, f := range review.Findings {
		if err := insertFinding(ctx, tx, review.TaskID, f); err != nil {
			return err
		}
	}
	for _, f := range review.Warnings {
		if err := insertFinding(ctx, tx, review.TaskID, f); err != nil {
			return err
		}
	}

	severityJSON, err := json.Marshal(review.Metrics.SeverityCounts)
	if err != nil {
		return fmt.Errorf("marshal severity: %w", err)
	}
	exceptionJSON, err := json.Marshal(review.Metrics.ExceptionCounts)
	if err != nil {
		return fmt.Errorf("marshal exceptions: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO review_metrics (
	task_id, finding_count, warning_count, total_duration_ms,
	sandbox_duration_ms, tool_call_count, permission_deny_count,
	severity_json, exception_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		review.TaskID,
		review.Metrics.FindingCount,
		review.Metrics.WarningCount,
		review.Metrics.TotalDurationMs,
		review.Metrics.SandboxDurationMs,
		review.Metrics.ToolCallCount,
		review.Metrics.PermissionDenyCount,
		string(severityJSON),
		string(exceptionJSON),
	)
	if err != nil {
		return fmt.Errorf("insert metrics: %w", err)
	}

	for _, a := range review.Artifacts {
		_, err = tx.ExecContext(ctx, `
INSERT INTO artifacts (id, task_id, name, content)
VALUES (?, ?, ?, ?)`,
			a.ID,
			review.TaskID,
			a.Name,
			a.Content,
		)
		if err != nil {
			return fmt.Errorf("insert artifact: %w", err)
		}
	}

	for _, p := range review.PermissionDecisions {
		_, err = tx.ExecContext(ctx, `
INSERT INTO permission_decisions (id, task_id, tool_name, command, action, reason)
VALUES (?, ?, ?, ?, ?, ?)`,
			p.ID, p.TaskID, p.ToolName, p.Command, p.Action, p.Reason,
		)
		if err != nil {
			return fmt.Errorf("insert permission decision: %w", err)
		}
	}

	for _, r := range review.SandboxRuns {
		_, err = tx.ExecContext(ctx, `
INSERT INTO sandbox_runs (id, task_id, command, runtime, status, exit_code, duration_ms, stdout, stderr, error_type)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.ID, r.TaskID, r.Command, r.Runtime, r.Status, r.ExitCode, r.DurationMs, r.Stdout, r.Stderr, r.ErrorType,
		)
		if err != nil {
			return fmt.Errorf("insert sandbox run: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func insertFinding(ctx context.Context, tx *sql.Tx, taskID string, f findings.Finding) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO findings (
	id, task_id, severity, category, file, line, title, evidence, recommendation, confidence, source, rule_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uuid.NewString(),
		taskID,
		f.Severity,
		f.Category,
		f.File,
		f.Line,
		f.Title,
		f.Evidence,
		f.Recommendation,
		f.Confidence,
		f.Source,
		f.RuleID,
	)
	if err != nil {
		return fmt.Errorf("insert finding: %w", err)
	}
	return nil
}

// GetReview loads a review by task ID.
func (s *SQLiteStore) GetReview(ctx context.Context, taskID string) (*ReviewRecord, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT status, input_summary, repo_path, created_at, finished_at, duration_ms
FROM review_tasks WHERE id = ?`, taskID)

	var (
		status, inputSummary, repoPath, createdAt, finishedAt string
		durationMs                                            int
	)
	if err := row.Scan(&status, &inputSummary, &repoPath, &createdAt, &finishedAt, &durationMs); err != nil {
		return nil, fmt.Errorf("get task: %w", err)
	}

	created, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}
	finished, err := time.Parse(time.RFC3339, finishedAt)
	if err != nil {
		return nil, fmt.Errorf("parse finished_at: %w", err)
	}

	findingRows, err := s.db.QueryContext(ctx, `
SELECT severity, category, file, line, title, evidence, recommendation, confidence, source, rule_id
FROM findings WHERE task_id = ? ORDER BY file, line`, taskID)
	if err != nil {
		return nil, fmt.Errorf("query findings: %w", err)
	}
	defer findingRows.Close()

	var confirmed, warnings []findings.Finding
	for findingRows.Next() {
		var f findings.Finding
		if err := findingRows.Scan(
			&f.Severity, &f.Category, &f.File, &f.Line, &f.Title, &f.Evidence,
			&f.Recommendation, &f.Confidence, &f.Source, &f.RuleID,
		); err != nil {
			return nil, fmt.Errorf("scan finding: %w", err)
		}
		if f.Confidence < 0.6 {
			warnings = append(warnings, f)
			continue
		}
		confirmed = append(confirmed, f)
	}
	if err := findingRows.Err(); err != nil {
		return nil, err
	}

	var (
		findingCount, warningCount, metricsDuration  int
		sandboxDuration, toolCalls, permissionDenies int
		severityJSON, exceptionJSON                  string
	)
	if err := s.db.QueryRowContext(ctx, `
SELECT finding_count, warning_count, total_duration_ms,
       sandbox_duration_ms, tool_call_count, permission_deny_count,
       severity_json, exception_json
FROM review_metrics WHERE task_id = ?`, taskID).Scan(
		&findingCount, &warningCount, &metricsDuration,
		&sandboxDuration, &toolCalls, &permissionDenies,
		&severityJSON, &exceptionJSON,
	); err != nil {
		return nil, fmt.Errorf("get metrics: %w", err)
	}
	severityCounts := map[string]int{}
	if err := json.Unmarshal([]byte(severityJSON), &severityCounts); err != nil {
		return nil, fmt.Errorf("unmarshal severity: %w", err)
	}
	exceptionCounts := map[string]int{}
	if exceptionJSON != "" {
		if err := json.Unmarshal([]byte(exceptionJSON), &exceptionCounts); err != nil {
			return nil, fmt.Errorf("unmarshal exceptions: %w", err)
		}
	}

	artifactRows, err := s.db.QueryContext(ctx, `
SELECT id, name, content FROM artifacts WHERE task_id = ?`, taskID)
	if err != nil {
		return nil, fmt.Errorf("query artifacts: %w", err)
	}
	defer artifactRows.Close()

	var artifacts []ArtifactRecord
	for artifactRows.Next() {
		var a ArtifactRecord
		if err := artifactRows.Scan(&a.ID, &a.Name, &a.Content); err != nil {
			return nil, fmt.Errorf("scan artifact: %w", err)
		}
		a.TaskID = taskID
		artifacts = append(artifacts, a)
	}
	if err := artifactRows.Err(); err != nil {
		return nil, err
	}

	permRows, err := s.db.QueryContext(ctx, `
SELECT id, tool_name, command, action, reason
FROM permission_decisions WHERE task_id = ?`, taskID)
	if err != nil {
		return nil, fmt.Errorf("query permissions: %w", err)
	}
	defer permRows.Close()
	var permissions []PermissionRecord
	for permRows.Next() {
		var p PermissionRecord
		if err := permRows.Scan(&p.ID, &p.ToolName, &p.Command, &p.Action, &p.Reason); err != nil {
			return nil, fmt.Errorf("scan permission: %w", err)
		}
		p.TaskID = taskID
		permissions = append(permissions, p)
	}
	if err := permRows.Err(); err != nil {
		return nil, err
	}

	sbRows, err := s.db.QueryContext(ctx, `
SELECT id, command, runtime, status, exit_code, duration_ms, stdout, stderr, error_type
FROM sandbox_runs WHERE task_id = ?`, taskID)
	if err != nil {
		return nil, fmt.Errorf("query sandbox runs: %w", err)
	}
	defer sbRows.Close()
	var sandboxRuns []SandboxRunRecord
	for sbRows.Next() {
		var r SandboxRunRecord
		if err := sbRows.Scan(&r.ID, &r.Command, &r.Runtime, &r.Status, &r.ExitCode, &r.DurationMs, &r.Stdout, &r.Stderr, &r.ErrorType); err != nil {
			return nil, fmt.Errorf("scan sandbox run: %w", err)
		}
		r.TaskID = taskID
		sandboxRuns = append(sandboxRuns, r)
	}
	if err := sbRows.Err(); err != nil {
		return nil, err
	}

	return &ReviewRecord{
		TaskID:       taskID,
		Status:       status,
		InputSummary: inputSummary,
		RepoPath:     repoPath,
		CreatedAt:    created,
		FinishedAt:   finished,
		DurationMs:   durationMs,
		Findings:     confirmed,
		Warnings:     warnings,
		Metrics: findings.ReviewMetrics{
			TotalDurationMs:     metricsDuration,
			SandboxDurationMs:   sandboxDuration,
			FindingCount:        findingCount,
			WarningCount:        warningCount,
			ToolCallCount:       toolCalls,
			PermissionDenyCount: permissionDenies,
			SeverityCounts:      severityCounts,
			ExceptionCounts:     exceptionCounts,
		},
		Artifacts:           artifacts,
		PermissionDecisions: permissions,
		SandboxRuns:         sandboxRuns,
	}, nil
}

// Close closes the database connection.
func (s *SQLiteStore) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}
