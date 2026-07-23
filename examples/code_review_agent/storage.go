//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	sqliteDriverName    = "sqlite3"
	reviewSchemaVersion = 1

	decisionKindFilter     = "filter"
	decisionKindPermission = "permission"

	findingDispositionFinding = "finding"
	findingDispositionWarning = "warning"
)

var errReviewTaskNotFound = errors.New("review task not found")

type reviewStore interface {
	SaveReview(context.Context, reviewReport) error
	LoadReview(context.Context, string) (reviewReport, error)
	Close() error
}

type sqliteReviewStore struct {
	db *sql.DB
}

type memoryReviewStore struct {
	mu      sync.Mutex
	reports map[string]reviewReport
	saveErr error
}

const reviewSchemaSQL = `
CREATE TABLE IF NOT EXISTS review_tasks (
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

CREATE TABLE IF NOT EXISTS decisions (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	task_id TEXT NOT NULL REFERENCES review_tasks(task_id) ON DELETE CASCADE,
	kind TEXT NOT NULL CHECK (kind IN ('filter', 'permission')),
	ordinal INTEGER NOT NULL,
	command TEXT NOT NULL,
	decision TEXT NOT NULL,
	reason TEXT
);

CREATE TABLE IF NOT EXISTS sandbox_runs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	task_id TEXT NOT NULL REFERENCES review_tasks(task_id) ON DELETE CASCADE,
	ordinal INTEGER NOT NULL,
	runtime TEXT NOT NULL,
	command TEXT NOT NULL,
	exit_code INTEGER NOT NULL,
	stdout TEXT,
	stderr TEXT,
	timed_out INTEGER NOT NULL,
	duration_ms INTEGER NOT NULL,
	error TEXT,
	skipped INTEGER NOT NULL,
	warnings_json TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS findings (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	task_id TEXT NOT NULL REFERENCES review_tasks(task_id) ON DELETE CASCADE,
	disposition TEXT NOT NULL CHECK (disposition IN ('finding', 'warning')),
	ordinal INTEGER NOT NULL,
	severity TEXT NOT NULL,
	category TEXT NOT NULL,
	file TEXT NOT NULL,
	line INTEGER NOT NULL,
	title TEXT NOT NULL,
	evidence TEXT,
	recommendation TEXT,
	confidence REAL NOT NULL,
	source TEXT,
	rule_id TEXT
);

CREATE TABLE IF NOT EXISTS artifacts (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	task_id TEXT NOT NULL REFERENCES review_tasks(task_id) ON DELETE CASCADE,
	ordinal INTEGER NOT NULL,
	kind TEXT NOT NULL CHECK (kind IN ('review_report_json', 'review_report_markdown')),
	path TEXT NOT NULL,
	sha256 TEXT,
	bytes INTEGER NOT NULL
);
`

func openConfiguredReviewStore(ctx context.Context, cfg config, hooks runtimeHooks) (reviewStore, bool, error) {
	if hooks.reviewStore != nil {
		return hooks.reviewStore, false, nil
	}
	store, err := openSQLiteReviewStore(ctx, cfg.dbPath)
	return store, true, err
}

func openSQLiteReviewStore(ctx context.Context, dbPath string) (reviewStore, error) {
	if strings.TrimSpace(dbPath) == "" {
		return nil, errors.New("sqlite db path must not be empty")
	}
	if !sqlDriverAvailable(sqliteDriverName) {
		return nil, errors.New("sqlite storage unavailable: sqlite3 driver is not registered; enable CGO to use github.com/mattn/go-sqlite3")
	}
	dir := filepath.Dir(dbPath)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create sqlite directory: %w", err)
		}
	}
	db, err := sql.Open(sqliteDriverName, dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	store := &sqliteReviewStore{db: db}
	if err := store.init(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func sqlDriverAvailable(name string) bool {
	for _, driver := range sql.Drivers() {
		if driver == name {
			return true
		}
	}
	return false
}

func (s *sqliteReviewStore) init(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, "PRAGMA foreign_keys=ON"); err != nil {
		return fmt.Errorf("enable sqlite foreign keys: %w", err)
	}
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read sqlite schema version: %w", err)
	}
	if version > reviewSchemaVersion {
		return fmt.Errorf("sqlite schema version %d is newer than supported version %d", version, reviewSchemaVersion)
	}
	if _, err := s.db.ExecContext(ctx, reviewSchemaSQL); err != nil {
		return fmt.Errorf("initialize sqlite schema: %w", err)
	}
	if version == 0 {
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", reviewSchemaVersion)); err != nil {
			return fmt.Errorf("set sqlite schema version: %w", err)
		}
	}
	return nil
}

func (s *sqliteReviewStore) SaveReview(ctx context.Context, report reviewReport) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin review transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	inputJSON, err := marshalJSONText(report.Input)
	if err != nil {
		return err
	}
	runtimeJSON, err := marshalJSONText(report.Runtime)
	if err != nil {
		return err
	}
	parseJSON, err := marshalJSONText(report.Parse)
	if err != nil {
		return err
	}
	rulesJSON, err := marshalJSONText(report.Rules)
	if err != nil {
		return err
	}
	metricsJSON, err := marshalJSONText(report.Metrics)
	if err != nil {
		return err
	}
	reportPathsJSON, err := marshalJSONText(report.ReportPaths)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
INSERT INTO review_tasks (
	task_id, status, conclusion, started_at, finished_at, duration_ms,
	input_json, runtime_json, parse_json, rules_json, metrics_json, report_paths_json,
	skill_name, skill_digest, commands_planned, commands_allowed, commands_blocked,
	permission_blocks
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		report.TaskID,
		report.Status,
		report.Conclusion,
		formatReportTime(report.StartedAt),
		formatReportTime(report.FinishedAt),
		report.DurationMS,
		inputJSON,
		runtimeJSON,
		parseJSON,
		rulesJSON,
		metricsJSON,
		reportPathsJSON,
		report.Governance.SkillName,
		report.Governance.SkillDigest,
		report.Governance.CommandsPlanned,
		report.Governance.CommandsAllowed,
		report.Governance.CommandsBlocked,
		report.Governance.PermissionBlocks,
	)
	if err != nil {
		return fmt.Errorf("insert review task: %w", err)
	}

	if err := insertDecisions(ctx, tx, report.TaskID, decisionKindFilter, report.Governance.FilterDecisions); err != nil {
		return err
	}
	if err := insertDecisions(ctx, tx, report.TaskID, decisionKindPermission, report.Governance.PermissionDecisions); err != nil {
		return err
	}
	if err := insertSandboxRuns(ctx, tx, report.TaskID, report.Governance.SandboxRuns); err != nil {
		return err
	}
	if err := insertFindings(ctx, tx, report.TaskID, findingDispositionFinding, report.Findings); err != nil {
		return err
	}
	if err := insertFindings(ctx, tx, report.TaskID, findingDispositionWarning, report.Warnings); err != nil {
		return err
	}
	if err := insertArtifacts(ctx, tx, report.TaskID, report.Artifacts); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit review transaction: %w", err)
	}
	committed = true
	return nil
}

func insertDecisions(
	ctx context.Context,
	tx *sql.Tx,
	taskID string,
	kind string,
	decisions []governanceDecision,
) error {
	for i, decision := range decisions {
		_, err := tx.ExecContext(ctx, `
INSERT INTO decisions (task_id, kind, ordinal, command, decision, reason)
VALUES (?, ?, ?, ?, ?, ?)`,
			taskID, kind, i, decision.Command, decision.Decision, decision.Reason)
		if err != nil {
			return fmt.Errorf("insert %s decision: %w", kind, err)
		}
	}
	return nil
}

func insertSandboxRuns(ctx context.Context, tx *sql.Tx, taskID string, runs []sandboxRun) error {
	for i, run := range runs {
		warningsJSON, err := marshalJSONText(run.Warnings)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
INSERT INTO sandbox_runs (
	task_id, ordinal, runtime, command, exit_code, stdout, stderr, timed_out,
	duration_ms, error, skipped, warnings_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			taskID,
			i,
			run.Runtime,
			run.Command,
			run.ExitCode,
			run.Stdout,
			run.Stderr,
			boolToInt(run.TimedOut),
			run.DurationMS,
			run.Error,
			boolToInt(run.Skipped),
			warningsJSON,
		)
		if err != nil {
			return fmt.Errorf("insert sandbox run: %w", err)
		}
	}
	return nil
}

func insertFindings(
	ctx context.Context,
	tx *sql.Tx,
	taskID string,
	disposition string,
	findings []reviewFinding,
) error {
	for i, finding := range findings {
		_, err := tx.ExecContext(ctx, `
INSERT INTO findings (
	task_id, disposition, ordinal, severity, category, file, line, title,
	evidence, recommendation, confidence, source, rule_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			taskID,
			disposition,
			i,
			finding.Severity,
			finding.Category,
			finding.File,
			finding.Line,
			finding.Title,
			finding.Evidence,
			finding.Recommendation,
			finding.Confidence,
			finding.Source,
			finding.RuleID,
		)
		if err != nil {
			return fmt.Errorf("insert %s: %w", disposition, err)
		}
	}
	return nil
}

func insertArtifacts(ctx context.Context, tx *sql.Tx, taskID string, artifacts []reportArtifact) error {
	for i, artifact := range artifacts {
		_, err := tx.ExecContext(ctx, `
INSERT INTO artifacts (task_id, ordinal, kind, path, sha256, bytes)
VALUES (?, ?, ?, ?, ?, ?)`,
			taskID, i, artifact.Kind, artifact.Path, artifact.SHA256, artifact.Bytes)
		if err != nil {
			return fmt.Errorf("insert artifact: %w", err)
		}
	}
	return nil
}

func (s *sqliteReviewStore) LoadReview(ctx context.Context, taskID string) (reviewReport, error) {
	var report reviewReport
	var startedAt string
	var finishedAt string
	var inputJSON string
	var runtimeJSON string
	var parseJSON string
	var rulesJSON string
	var metricsJSON string
	var reportPathsJSON string
	row := s.db.QueryRowContext(ctx, `
SELECT task_id, status, conclusion, started_at, finished_at, duration_ms,
	input_json, runtime_json, parse_json, rules_json, metrics_json, report_paths_json,
	skill_name, skill_digest, commands_planned, commands_allowed, commands_blocked,
	permission_blocks
FROM review_tasks
WHERE task_id = ?`, taskID)
	err := row.Scan(
		&report.TaskID,
		&report.Status,
		&report.Conclusion,
		&startedAt,
		&finishedAt,
		&report.DurationMS,
		&inputJSON,
		&runtimeJSON,
		&parseJSON,
		&rulesJSON,
		&metricsJSON,
		&reportPathsJSON,
		&report.Governance.SkillName,
		&report.Governance.SkillDigest,
		&report.Governance.CommandsPlanned,
		&report.Governance.CommandsAllowed,
		&report.Governance.CommandsBlocked,
		&report.Governance.PermissionBlocks,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return reviewReport{}, errReviewTaskNotFound
	}
	if err != nil {
		return reviewReport{}, fmt.Errorf("query review task: %w", err)
	}
	report.StartedAt, err = parseReportTime(startedAt)
	if err != nil {
		return reviewReport{}, err
	}
	report.FinishedAt, err = parseReportTime(finishedAt)
	if err != nil {
		return reviewReport{}, err
	}
	if err := unmarshalJSONText(inputJSON, &report.Input); err != nil {
		return reviewReport{}, err
	}
	if err := unmarshalJSONText(runtimeJSON, &report.Runtime); err != nil {
		return reviewReport{}, err
	}
	if err := unmarshalJSONText(parseJSON, &report.Parse); err != nil {
		return reviewReport{}, err
	}
	if err := unmarshalJSONText(rulesJSON, &report.Rules); err != nil {
		return reviewReport{}, err
	}
	if err := unmarshalJSONText(metricsJSON, &report.Metrics); err != nil {
		return reviewReport{}, err
	}
	if err := unmarshalJSONText(reportPathsJSON, &report.ReportPaths); err != nil {
		return reviewReport{}, err
	}

	report.Governance.FilterDecisions, err = loadDecisions(ctx, s.db, taskID, decisionKindFilter)
	if err != nil {
		return reviewReport{}, err
	}
	report.Governance.PermissionDecisions, err = loadDecisions(ctx, s.db, taskID, decisionKindPermission)
	if err != nil {
		return reviewReport{}, err
	}
	report.Governance.SandboxRuns, err = loadSandboxRuns(ctx, s.db, taskID)
	if err != nil {
		return reviewReport{}, err
	}
	report.Findings, err = loadFindings(ctx, s.db, taskID, findingDispositionFinding)
	if err != nil {
		return reviewReport{}, err
	}
	report.Warnings, err = loadFindings(ctx, s.db, taskID, findingDispositionWarning)
	if err != nil {
		return reviewReport{}, err
	}
	report.Artifacts, err = loadArtifacts(ctx, s.db, taskID)
	if err != nil {
		return reviewReport{}, err
	}
	return report, nil
}

func loadDecisions(ctx context.Context, db *sql.DB, taskID string, kind string) ([]governanceDecision, error) {
	rows, err := db.QueryContext(ctx, `
SELECT command, decision, reason
FROM decisions
WHERE task_id = ? AND kind = ?
ORDER BY ordinal`, taskID, kind)
	if err != nil {
		return nil, fmt.Errorf("query %s decisions: %w", kind, err)
	}
	defer rows.Close()
	var decisions []governanceDecision
	for rows.Next() {
		var decision governanceDecision
		if err := rows.Scan(&decision.Command, &decision.Decision, &decision.Reason); err != nil {
			return nil, fmt.Errorf("scan %s decision: %w", kind, err)
		}
		decisions = append(decisions, decision)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s decisions: %w", kind, err)
	}
	return decisions, nil
}

func loadSandboxRuns(ctx context.Context, db *sql.DB, taskID string) ([]sandboxRun, error) {
	rows, err := db.QueryContext(ctx, `
SELECT runtime, command, exit_code, stdout, stderr, timed_out, duration_ms,
	error, skipped, warnings_json
FROM sandbox_runs
WHERE task_id = ?
ORDER BY ordinal`, taskID)
	if err != nil {
		return nil, fmt.Errorf("query sandbox runs: %w", err)
	}
	defer rows.Close()
	var runs []sandboxRun
	for rows.Next() {
		var run sandboxRun
		var timedOut int
		var skipped int
		var warningsJSON string
		if err := rows.Scan(
			&run.Runtime,
			&run.Command,
			&run.ExitCode,
			&run.Stdout,
			&run.Stderr,
			&timedOut,
			&run.DurationMS,
			&run.Error,
			&skipped,
			&warningsJSON,
		); err != nil {
			return nil, fmt.Errorf("scan sandbox run: %w", err)
		}
		run.TimedOut = timedOut != 0
		run.Skipped = skipped != 0
		if err := unmarshalJSONText(warningsJSON, &run.Warnings); err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sandbox runs: %w", err)
	}
	return runs, nil
}

func loadFindings(ctx context.Context, db *sql.DB, taskID string, disposition string) ([]reviewFinding, error) {
	rows, err := db.QueryContext(ctx, `
SELECT severity, category, file, line, title, evidence, recommendation,
	confidence, source, rule_id
FROM findings
WHERE task_id = ? AND disposition = ?
ORDER BY ordinal`, taskID, disposition)
	if err != nil {
		return nil, fmt.Errorf("query %s records: %w", disposition, err)
	}
	defer rows.Close()
	var findings []reviewFinding
	for rows.Next() {
		var finding reviewFinding
		if err := rows.Scan(
			&finding.Severity,
			&finding.Category,
			&finding.File,
			&finding.Line,
			&finding.Title,
			&finding.Evidence,
			&finding.Recommendation,
			&finding.Confidence,
			&finding.Source,
			&finding.RuleID,
		); err != nil {
			return nil, fmt.Errorf("scan %s: %w", disposition, err)
		}
		findings = append(findings, finding)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s records: %w", disposition, err)
	}
	return findings, nil
}

func loadArtifacts(ctx context.Context, db *sql.DB, taskID string) ([]reportArtifact, error) {
	rows, err := db.QueryContext(ctx, `
SELECT kind, path, sha256, bytes
FROM artifacts
WHERE task_id = ?
ORDER BY ordinal`, taskID)
	if err != nil {
		return nil, fmt.Errorf("query artifacts: %w", err)
	}
	defer rows.Close()
	var artifacts []reportArtifact
	for rows.Next() {
		var artifact reportArtifact
		if err := rows.Scan(&artifact.Kind, &artifact.Path, &artifact.SHA256, &artifact.Bytes); err != nil {
			return nil, fmt.Errorf("scan artifact: %w", err)
		}
		artifacts = append(artifacts, artifact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate artifacts: %w", err)
	}
	return artifacts, nil
}

func (s *sqliteReviewStore) Close() error {
	return s.db.Close()
}

func newMemoryReviewStore() *memoryReviewStore {
	return &memoryReviewStore{reports: map[string]reviewReport{}}
}

func (s *memoryReviewStore) SaveReview(_ context.Context, report reviewReport) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reports[report.TaskID] = cloneReviewReport(report)
	return nil
}

func (s *memoryReviewStore) LoadReview(_ context.Context, taskID string) (reviewReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	report, ok := s.reports[taskID]
	if !ok {
		return reviewReport{}, errReviewTaskNotFound
	}
	return cloneReviewReport(report), nil
}

func (s *memoryReviewStore) Close() error {
	return nil
}

func cloneReviewReport(report reviewReport) reviewReport {
	data, err := json.Marshal(report)
	if err != nil {
		return report
	}
	var clone reviewReport
	if err := json.Unmarshal(data, &clone); err != nil {
		return report
	}
	return clone
}

func marshalJSONText(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("marshal storage json: %w", err)
	}
	return string(data), nil
}

func unmarshalJSONText(text string, target any) error {
	if strings.TrimSpace(text) == "" {
		text = "null"
	}
	if err := json.Unmarshal([]byte(text), target); err != nil {
		return fmt.Errorf("unmarshal storage json: %w", err)
	}
	return nil
}

func formatReportTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseReportTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse report time: %w", err)
	}
	return parsed, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
