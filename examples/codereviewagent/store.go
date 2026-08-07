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
	_ "embed"
	"encoding/json"
	"fmt"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

type reviewStore struct {
	db *sql.DB
}

func openReviewStore(path string) (*reviewStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open review database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable review database foreign keys: %w", err)
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize review database: %w", err)
	}
	return &reviewStore{db: db}, nil
}

func (s *reviewStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *reviewStore) Save(ctx context.Context, report *reviewReport, artifacts []artifact) error {
	if s == nil || s.db == nil || report == nil {
		return fmt.Errorf("review store or report is nil")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin review transaction: %w", err)
	}
	defer tx.Rollback()
	for _, statement := range []string{
		"DELETE FROM reports WHERE task_id=?",
		"DELETE FROM artifacts WHERE task_id=?",
		"DELETE FROM findings WHERE task_id=?",
		"DELETE FROM sandbox_runs WHERE task_id=?",
		"DELETE FROM permission_decisions WHERE task_id=?",
		"DELETE FROM review_tasks WHERE id=?",
	} {
		if _, err := tx.ExecContext(ctx, statement, report.TaskID); err != nil {
			return fmt.Errorf("replace prior review task: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO review_tasks
    (id, status, input_source, diff_sha256, created_at, duration_ms) VALUES (?, ?, ?, ?, ?, ?)`,
		report.TaskID, report.Status, report.InputSource, report.DiffSHA256,
		report.CreatedAt.Format(timeFormat), report.Metrics.DurationMS); err != nil {
		return fmt.Errorf("insert review task: %w", err)
	}
	commandJSON, _ := json.Marshal(report.Permission.Command)
	if _, err := tx.ExecContext(ctx, `INSERT INTO permission_decisions
    (task_id, action, reason, command_json) VALUES (?, ?, ?, ?)`,
		report.TaskID, report.Permission.Action, report.Permission.Reason, string(commandJSON)); err != nil {
		return fmt.Errorf("insert permission decision: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sandbox_runs
    (task_id, status, command_json, exit_code, timed_out, output, error, duration_ms)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, report.TaskID, report.Sandbox.Status,
		string(commandJSON), report.Sandbox.ExitCode, report.Sandbox.TimedOut,
		report.Sandbox.Output, report.Sandbox.Error, report.Sandbox.DurationMS); err != nil {
		return fmt.Errorf("insert sandbox run: %w", err)
	}
	for _, finding := range report.Findings {
		key := fmt.Sprintf("%s:%d:%s", finding.File, finding.StartLine, finding.RuleID)
		if _, err := tx.ExecContext(ctx, `INSERT INTO findings
      (task_id, dedupe_key, file, start_line, end_line, severity, category, confidence,
       source, rule_id, status, message, suggestion) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			report.TaskID, key, finding.File, finding.StartLine, finding.EndLine,
			finding.Severity, finding.Category, finding.Confidence, finding.Source,
			finding.RuleID, finding.Status, finding.Message, finding.Suggestion); err != nil {
			return fmt.Errorf("insert finding: %w", err)
		}
	}
	for _, artifact := range artifacts {
		if _, err := tx.ExecContext(ctx, `INSERT INTO artifacts
      (task_id, kind, path, sha256, size_bytes) VALUES (?, ?, ?, ?, ?)`,
			report.TaskID, artifact.Kind, artifact.Path, artifact.SHA256, artifact.SizeBytes); err != nil {
			return fmt.Errorf("insert artifact: %w", err)
		}
	}
	metricsJSON, _ := json.Marshal(report.Metrics)
	if _, err := tx.ExecContext(ctx, `INSERT INTO reports
    (task_id, json_path, markdown_path, summary, metrics_json) VALUES (?, ?, ?, ?, ?)`,
		report.TaskID, "review_report.json", "review_report.md", report.Summary, string(metricsJSON)); err != nil {
		return fmt.Errorf("insert report: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit review transaction: %w", err)
	}
	return nil
}

func (s *reviewStore) GetTask(ctx context.Context, taskID string) (storedTask, error) {
	var result storedTask
	err := s.db.QueryRowContext(ctx, `SELECT t.id, t.status,
    (SELECT COUNT(*) FROM findings f WHERE f.task_id=t.id),
    (SELECT COUNT(*) FROM sandbox_runs s WHERE s.task_id=t.id),
    (SELECT COUNT(*) FROM permission_decisions p WHERE p.task_id=t.id),
    (SELECT COUNT(*) FROM reports r WHERE r.task_id=t.id)
    FROM review_tasks t WHERE t.id=?`, taskID).Scan(
		&result.ID, &result.Status, &result.FindingCount, &result.SandboxCount,
		&result.DecisionCount, &result.ReportCount,
	)
	if err != nil {
		return storedTask{}, fmt.Errorf("query review task: %w", err)
	}
	return result, nil
}
