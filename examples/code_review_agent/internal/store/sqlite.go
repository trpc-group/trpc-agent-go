//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package sqlite implements durable review storage with embedded migrations.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/reviewmodel"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/migrations"
)

const timeFormat = time.RFC3339Nano

// Finalize atomically persists findings, metrics, artifacts, report, and state.
func (s *SQLiteStore) Finalize(ctx context.Context, request FinalizeRequest) error {
	if request.Status != StatusCompleted && request.Status != StatusCompletedWithWarnings {
		return ErrInvalidTransition
	}
	return s.withTransaction(ctx, func(tx *sql.Tx) error {
		if err := insertFindings(ctx, tx, request.TaskID, request.Findings); err != nil {
			return err
		}
		if err := insertMetrics(ctx, tx, request.TaskID, request.Metrics); err != nil {
			return err
		}
		if err := insertArtifacts(ctx, tx, request.TaskID, request.Artifacts); err != nil {
			return err
		}
		if err := insertReport(ctx, tx, request.TaskID, request.Report); err != nil {
			return err
		}
		return finishTask(ctx, tx, request)
	})
}

// FailTask terminates a running task after infrastructure failure.
func (s *SQLiteStore) FailTask(ctx context.Context, request FailRequest) error {
	return s.withTransaction(ctx, func(tx *sql.Tx) error {
		const query = `UPDATE review_tasks SET status=?,finished_at=?,error=? WHERE id=? AND status=?`
		result, err := tx.ExecContext(ctx, query, StatusFailed, request.FinishedAt.UTC().Format(timeFormat), redact.String(request.Error), request.TaskID, StatusRunning)
		if err != nil {
			return fmt.Errorf("fail review task: %w", err)
		}
		if err := requireOneTransition(result); err != nil {
			return err
		}
		return insertMetrics(ctx, tx, request.TaskID, request.Metrics)
	})
}
func (s *SQLiteStore) withTransaction(ctx context.Context, operation func(*sql.Tx) error) (resultErr error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin review transaction: %w", err)
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, tx.Rollback())
		}
	}()
	if err := operation(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit review transaction: %w", err)
	}
	return nil
}
func insertFindings(ctx context.Context, tx *sql.Tx, taskID string, findings []reviewmodel.Finding) error {
	const query = `INSERT INTO findings
        (task_id,bucket,severity,category,file,line,title,evidence,recommendation,confidence,source,rule_id,dedup_key)
        VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`
	for _, finding := range findings {
		clean := redactFinding(finding)
		_, err := tx.ExecContext(ctx, query, taskID, clean.Bucket, clean.Severity, clean.Category, clean.File, clean.Line, clean.Title, clean.Evidence, clean.Recommendation, clean.Confidence, clean.Source, clean.RuleID, findingDedupKey(clean))
		if err != nil {
			return fmt.Errorf("insert finding: %w", err)
		}
	}
	return nil
}
func insertMetrics(ctx context.Context, tx *sql.Tx, taskID string, metrics Metrics) error {
	severity, err := encodeCountMap(metrics.SeverityCounts)
	if err != nil {
		return err
	}
	errorTypes, err := encodeCountMap(metrics.ErrorTypeCounts)
	if err != nil {
		return err
	}
	const query = `INSERT INTO review_metrics
        (task_id,total_duration_ms,sandbox_duration_ms,tool_calls,permission_blocks,finding_count,severity_json,error_types_json)
        VALUES(?,?,?,?,?,?,?,?)`
	_, err = tx.ExecContext(ctx, query, taskID, metrics.TotalDurationMS, metrics.SandboxDurationMS, metrics.ToolCalls, metrics.PermissionBlocks, metrics.FindingCount, severity, errorTypes)
	if err != nil {
		return fmt.Errorf("insert review metrics: %w", err)
	}
	return nil
}
func insertArtifacts(ctx context.Context, tx *sql.Tx, taskID string, artifacts []Artifact) error {
	const query = `INSERT INTO artifacts
        (id,task_id,run_id,kind,path,sha256,size_bytes,created_at) VALUES(?,?,?,?,?,?,?,?)`
	for _, artifact := range artifacts {
		var runID any
		if artifact.RunID != "" {
			runID = artifact.RunID
		}
		_, err := tx.ExecContext(ctx, query, artifact.ID, taskID, runID, redact.String(artifact.Kind), redact.String(artifact.Path), artifact.SHA256, artifact.SizeBytes, artifact.CreatedAt.UTC().Format(timeFormat))
		if err != nil {
			return fmt.Errorf("insert artifact: %w", err)
		}
	}
	return nil
}
func insertReport(ctx context.Context, tx *sql.Tx, taskID string, report Report) error {
	if redact.ContainsSecret(report.JSON) || redact.ContainsSecret(report.Markdown) {
		return errors.New("canonical report contains unredacted secret")
	}
	const query = `INSERT INTO reports
        (task_id,schema_version,conclusion,canonical_json,canonical_markdown,json_path,json_sha256,markdown_path,markdown_sha256)
        VALUES(?,?,?,?,?,?,?,?,?)`
	_, err := tx.ExecContext(ctx, query, taskID, report.SchemaVersion, redact.String(report.Conclusion), report.JSON, report.Markdown, redact.String(report.JSONPath), report.JSONSHA256, redact.String(report.MarkdownPath), report.MarkdownSHA256)
	if err != nil {
		return fmt.Errorf("insert report: %w", err)
	}
	return nil
}
func finishTask(ctx context.Context, tx *sql.Tx, request FinalizeRequest) error {
	const query = `UPDATE review_tasks SET status=?,finished_at=?,conclusion=? WHERE id=? AND status=?`
	result, err := tx.ExecContext(ctx, query, request.Status, request.FinishedAt.UTC().Format(timeFormat), redact.String(request.Conclusion), request.TaskID, StatusRunning)
	if err != nil {
		return fmt.Errorf("finish review task: %w", err)
	}
	return requireOneTransition(result)
}
func requireOneTransition(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read task transition count: %w", err)
	}
	if count != 1 {
		return ErrInvalidTransition
	}
	return nil
}
func redactFinding(finding reviewmodel.Finding) reviewmodel.Finding {
	finding.Severity = redact.String(finding.Severity)
	finding.Category = redact.String(finding.Category)
	finding.File = redact.String(finding.File)
	finding.Title = redact.String(finding.Title)
	finding.Evidence = redact.String(finding.Evidence)
	finding.Recommendation = redact.String(finding.Recommendation)
	finding.Source = redact.String(finding.Source)
	finding.RuleID = redact.String(finding.RuleID)
	return finding
}
func findingDedupKey(finding reviewmodel.Finding) string {
	value := fmt.Sprintf("%s\x00%d\x00%s", finding.File, finding.Line, finding.Category)
	digest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", digest[:])
}
func encodeCountMap(values map[string]int) (string, error) {
	clean := make(map[string]int, len(values))
	for key, value := range values {
		clean[redact.String(strings.TrimSpace(key))] = value
	}
	encoded, err := json.Marshal(clean)
	if err != nil {
		return "", fmt.Errorf("encode metric counts: %w", err)
	}
	return string(encoded), nil
}

// GetReview loads the full replayable aggregate for a task ID.
func (s *SQLiteStore) GetReview(ctx context.Context, taskID string) (Review, error) {
	result := Review{}
	var err error
	if result.Task, err = loadTask(ctx, s.db, taskID); err != nil {
		return Review{}, err
	}
	if result.Input, err = loadInputSummary(ctx, s.db, taskID); err != nil {
		return Review{}, err
	}
	if result.Runs, err = loadRuns(ctx, s.db, taskID); err != nil {
		return Review{}, err
	}
	if result.Decisions, err = loadDecisions(ctx, s.db, taskID); err != nil {
		return Review{}, err
	}
	if result.Findings, err = loadFindings(ctx, s.db, taskID); err != nil {
		return Review{}, err
	}
	if result.Metrics, err = loadMetrics(ctx, s.db, taskID); err != nil {
		return Review{}, err
	}
	if result.Artifacts, err = loadArtifacts(ctx, s.db, taskID); err != nil {
		return Review{}, err
	}
	if result.Report, err = loadReport(ctx, s.db, taskID); err != nil {
		return Review{}, err
	}
	return result, nil
}
func loadTask(ctx context.Context, db *sql.DB, taskID string) (Task, error) {
	const query = `SELECT id,status,input_kind,input_digest,started_at,finished_at,conclusion,error
        FROM review_tasks WHERE id=?`
	var task Task
	var started string
	var finished sql.NullString
	err := db.QueryRowContext(ctx, query, taskID).Scan(&task.ID, &task.Status, &task.InputKind, &task.InputDigest, &started, &finished, &task.Conclusion, &task.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, fmt.Errorf("query review task: %w", err)
	}
	if task.StartedAt, err = parseTime(started); err != nil {
		return Task{}, err
	}
	if finished.Valid {
		value, parseErr := parseTime(finished.String)
		if parseErr != nil {
			return Task{}, parseErr
		}
		task.FinishedAt = &value
	}
	return task, nil
}
func loadInputSummary(ctx context.Context, db *sql.DB, taskID string) (InputSummary, error) {
	const query = `SELECT file_count,hunk_count,added_lines,packages_json FROM input_summaries WHERE task_id=?`
	var result InputSummary
	var packages string
	err := db.QueryRowContext(ctx, query, taskID).Scan(&result.FileCount, &result.HunkCount, &result.AddedLines, &packages)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("query input summary: %w", err)
	}
	if err := json.Unmarshal([]byte(packages), &result.Packages); err != nil {
		return result, fmt.Errorf("decode input packages: %w", err)
	}
	return result, nil
}
func loadRuns(ctx context.Context, db *sql.DB, taskID string) (result []SandboxRun, resultErr error) {
	const query = `SELECT id,check_id,runtime,status,duration_ms,exit_code,timed_out,
        output_truncated,stdout,stderr,error_type,error FROM sandbox_runs WHERE task_id=? ORDER BY id`
	rows, err := db.QueryContext(ctx, query, taskID)
	if err != nil {
		return nil, fmt.Errorf("query sandbox runs: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, rows.Close())
	}()
	for rows.Next() {
		var run SandboxRun
		if err := rows.Scan(&run.ID, &run.CheckID, &run.Runtime, &run.Status, &run.DurationMS, &run.ExitCode, &run.TimedOut, &run.OutputTruncated, &run.Stdout, &run.Stderr, &run.ErrorType, &run.Error); err != nil {
			return nil, fmt.Errorf("scan sandbox run: %w", err)
		}
		result = append(result, run)
	}
	return result, rows.Err()
}
func loadDecisions(ctx context.Context, db *sql.DB, taskID string) (result []Decision, resultErr error) {
	const query = `SELECT id,stage,tool,check_id,args_digest,risk,action,reason,decided_at
        FROM governance_decisions WHERE task_id=? ORDER BY decided_at,id`
	rows, err := db.QueryContext(ctx, query, taskID)
	if err != nil {
		return nil, fmt.Errorf("query governance decisions: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, rows.Close())
	}()
	for rows.Next() {
		var decision Decision
		var decided string
		if err := rows.Scan(&decision.ID, &decision.Stage, &decision.Tool, &decision.CheckID, &decision.ArgsDigest, &decision.Risk, &decision.Action, &decision.Reason, &decided); err != nil {
			return nil, fmt.Errorf("scan governance decision: %w", err)
		}
		decision.At, err = parseTime(decided)
		if err != nil {
			return nil, err
		}
		result = append(result, decision)
	}
	return result, rows.Err()
}
func loadFindings(ctx context.Context, db *sql.DB, taskID string) (result []reviewmodel.Finding, resultErr error) {
	const query = `SELECT bucket,severity,category,file,line,title,evidence,recommendation,confidence,source,rule_id
        FROM findings WHERE task_id=? ORDER BY bucket,severity,file,line,category,rule_id`
	rows, err := db.QueryContext(ctx, query, taskID)
	if err != nil {
		return nil, fmt.Errorf("query findings: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, rows.Close())
	}()
	for rows.Next() {
		var finding reviewmodel.Finding
		if err := rows.Scan(&finding.Bucket, &finding.Severity, &finding.Category, &finding.File, &finding.Line, &finding.Title, &finding.Evidence, &finding.Recommendation, &finding.Confidence, &finding.Source, &finding.RuleID); err != nil {
			return nil, fmt.Errorf("scan finding: %w", err)
		}
		result = append(result, finding)
	}
	return result, rows.Err()
}
func loadMetrics(ctx context.Context, db *sql.DB, taskID string) (Metrics, error) {
	const query = `SELECT total_duration_ms,sandbox_duration_ms,tool_calls,permission_blocks,
        finding_count,severity_json,error_types_json FROM review_metrics WHERE task_id=?`
	var result Metrics
	var severity, errorTypes string
	err := db.QueryRowContext(ctx, query, taskID).Scan(&result.TotalDurationMS, &result.SandboxDurationMS, &result.ToolCalls, &result.PermissionBlocks, &result.FindingCount, &severity, &errorTypes)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("query review metrics: %w", err)
	}
	if err := json.Unmarshal([]byte(severity), &result.SeverityCounts); err != nil {
		return result, fmt.Errorf("decode severity metrics: %w", err)
	}
	if err := json.Unmarshal([]byte(errorTypes), &result.ErrorTypeCounts); err != nil {
		return result, fmt.Errorf("decode error metrics: %w", err)
	}
	return result, nil
}
func loadArtifacts(ctx context.Context, db *sql.DB, taskID string) (result []Artifact, resultErr error) {
	const query = `SELECT id,run_id,kind,path,sha256,size_bytes,created_at
        FROM artifacts WHERE task_id=? ORDER BY id`
	rows, err := db.QueryContext(ctx, query, taskID)
	if err != nil {
		return nil, fmt.Errorf("query artifacts: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, rows.Close())
	}()
	for rows.Next() {
		var artifact Artifact
		var runID sql.NullString
		var created string
		if err := rows.Scan(&artifact.ID, &runID, &artifact.Kind, &artifact.Path, &artifact.SHA256, &artifact.SizeBytes, &created); err != nil {
			return nil, fmt.Errorf("scan artifact: %w", err)
		}
		artifact.RunID = runID.String
		artifact.CreatedAt, err = parseTime(created)
		if err != nil {
			return nil, err
		}
		result = append(result, artifact)
	}
	return result, rows.Err()
}
func loadReport(ctx context.Context, db *sql.DB, taskID string) (Report, error) {
	const query = `SELECT schema_version,conclusion,canonical_json,canonical_markdown,
        json_path,json_sha256,markdown_path,markdown_sha256 FROM reports WHERE task_id=?`
	var result Report
	err := db.QueryRowContext(ctx, query, taskID).Scan(&result.SchemaVersion, &result.Conclusion, &result.JSON, &result.Markdown, &result.JSONPath, &result.JSONSHA256, &result.MarkdownPath, &result.MarkdownSHA256)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("query report: %w", err)
	}
	return result, nil
}
func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(timeFormat, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse stored timestamp: %w", err)
	}
	return parsed, nil
}

const (
	driverName      = "sqlite3"
	busyTimeoutMS   = "5000"
	maxConnections  = 1
	foreignKeysFlag = "on"
)

// Store persists complete review aggregates in SQLite.
type SQLiteStore struct{ db *sql.DB }

// Open creates or opens a store and applies embedded migrations.
func Open(ctx context.Context, path string) (*SQLiteStore, error) {
	dsn, err := dataSourceName(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("open review database: %w", err)
	}
	db.SetMaxOpenConns(maxConnections)
	db.SetMaxIdleConns(maxConnections)
	result := &SQLiteStore{db: db}
	if err := result.initialize(ctx); err != nil {
		return nil, errorsJoinClose(err, db)
	}
	return result, nil
}
func dataSourceName(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("database path is empty")
	}
	if path == ":memory:" {
		return "file:code-review-memory?mode=memory&cache=shared&_foreign_keys=on&_busy_timeout=" + busyTimeoutMS, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve database path: %w", err)
	}
	query := url.Values{"_foreign_keys": {foreignKeysFlag}, "_busy_timeout": {busyTimeoutMS}}
	return "file:" + filepath.ToSlash(abs) + "?" + query.Encode(), nil
}
func (s *SQLiteStore) initialize(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, migrations.InitialSchema); err != nil {
		return fmt.Errorf("apply review migration: %w", err)
	}
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping review database: %w", err)
	}
	return nil
}
func errorsJoinClose(operationErr error, db *sql.DB) error {
	closeErr := db.Close()
	if closeErr == nil {
		return operationErr
	}
	return errors.Join(operationErr, fmt.Errorf("close review database: %w", closeErr))
}

// Close releases the underlying database handle.
func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

const insertTaskSQL = `INSERT INTO review_tasks
    (id,status,input_kind,input_digest,started_at,conclusion,error)
    VALUES(?,?,?,?,?,?,?)`

// CreateTask inserts a running review task.
func (s *SQLiteStore) CreateTask(ctx context.Context, task Task) error {
	if task.ID == "" || task.Status != StatusRunning || task.StartedAt.IsZero() {
		return fmt.Errorf("create task: %w", ErrInvalidTransition)
	}
	_, err := s.db.ExecContext(ctx, insertTaskSQL, redact.String(task.ID), task.Status, redact.String(task.InputKind), task.InputDigest, task.StartedAt.UTC().Format(timeFormat), redact.String(task.Conclusion), redact.String(task.Error))
	if err != nil {
		return fmt.Errorf("create review task: %w", err)
	}
	return nil
}

// SaveInputSummary persists only bounded, non-source input metadata.
func (s *SQLiteStore) SaveInputSummary(ctx context.Context, taskID string, summary InputSummary) error {
	packages, err := encodeRedactedStrings(summary.Packages)
	if err != nil {
		return err
	}
	const query = `INSERT INTO input_summaries
        (task_id,file_count,hunk_count,added_lines,packages_json) VALUES(?,?,?,?,?)`
	_, err = s.db.ExecContext(ctx, query, taskID, summary.FileCount, summary.HunkCount, summary.AddedLines, packages)
	if err != nil {
		return fmt.Errorf("save input summary: %w", err)
	}
	return nil
}

// SaveRun persists one sandbox execution record.
func (s *SQLiteStore) SaveRun(ctx context.Context, taskID string, run SandboxRun) error {
	const query = `INSERT INTO sandbox_runs
        (id,task_id,check_id,runtime,status,duration_ms,exit_code,timed_out,output_truncated,stdout,stderr,error_type,error)
        VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`
	_, err := s.db.ExecContext(ctx, query, run.ID, taskID, redact.String(run.CheckID), redact.String(run.Runtime), redact.String(run.Status), run.DurationMS, run.ExitCode, run.TimedOut, run.OutputTruncated, redact.String(run.Stdout), redact.String(run.Stderr), redact.String(run.ErrorType), redact.String(run.Error))
	if err != nil {
		return fmt.Errorf("save sandbox run: %w", err)
	}
	return nil
}

// SaveDecision saves governance evidence before execution continues.
func (s *SQLiteStore) SaveDecision(ctx context.Context, taskID string, decision Decision) error {
	const query = `INSERT INTO governance_decisions
		(id,task_id,stage,tool,check_id,args_digest,risk,action,reason,decided_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)`
	_, err := s.db.ExecContext(ctx, query, decision.ID, taskID, redact.String(decision.Stage), redact.String(decision.Tool), redact.String(decision.CheckID), decision.ArgsDigest, redact.String(decision.Risk), redact.String(decision.Action), redact.String(decision.Reason), decision.At.UTC().Format(timeFormat))
	if err != nil {
		return fmt.Errorf("save governance decision: %w", err)
	}
	return nil
}
func encodeRedactedStrings(values []string) (string, error) {
	redacted := make([]string, len(values))
	for index, value := range values {
		redacted[index] = redact.String(value)
	}
	encoded, err := json.Marshal(redacted)
	if err != nil {
		return "", fmt.Errorf("encode redacted strings: %w", err)
	}
	return string(encoded), nil
}
