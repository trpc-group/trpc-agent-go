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
	return nil
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
	_, err = tx.ExecContext(ctx, `
INSERT INTO review_metrics (task_id, finding_count, warning_count, total_duration_ms, severity_json)
VALUES (?, ?, ?, ?, ?)`,
		review.TaskID,
		review.Metrics.FindingCount,
		review.Metrics.WarningCount,
		review.Metrics.TotalDurationMs,
		string(severityJSON),
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
		findingCount, warningCount, metricsDuration int
		severityJSON                                string
	)
	if err := s.db.QueryRowContext(ctx, `
SELECT finding_count, warning_count, total_duration_ms, severity_json
FROM review_metrics WHERE task_id = ?`, taskID).Scan(
		&findingCount, &warningCount, &metricsDuration, &severityJSON,
	); err != nil {
		return nil, fmt.Errorf("get metrics: %w", err)
	}
	severityCounts := map[string]int{}
	if err := json.Unmarshal([]byte(severityJSON), &severityCounts); err != nil {
		return nil, fmt.Errorf("unmarshal severity: %w", err)
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
			TotalDurationMs: metricsDuration,
			FindingCount:    findingCount,
			WarningCount:    warningCount,
			SeverityCounts:  severityCounts,
		},
		Artifacts: artifacts,
	}, nil
}

// Close closes the database connection.
func (s *SQLiteStore) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}
