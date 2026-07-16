//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Store struct {
	db *sql.DB
}

type ReviewStore interface {
	Close() error
	Init(ctx context.Context) error
	SchemaVersion(ctx context.Context) (int, error)
	SaveReport(ctx context.Context, report ReviewReport, pd ParsedDiff, jsonPath, mdPath string) error
	LoadTaskReport(ctx context.Context, taskID string) (ReviewReport, error)
}

const schemaVersion = 4

func OpenStore(ctx context.Context, path string) (ReviewStore, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Store{db: db}
	if err := s.Init(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) Init(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS review_tasks (
			id TEXT PRIMARY KEY,
			status TEXT NOT NULL,
			input_mode TEXT NOT NULL,
			started_at TEXT NOT NULL,
			ended_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS review_inputs (
			task_id TEXT PRIMARY KEY,
			diff_hash TEXT NOT NULL,
			summary_json TEXT NOT NULL,
			packages_json TEXT NOT NULL DEFAULT '[]',
			diff_preview TEXT NOT NULL,
			FOREIGN KEY(task_id) REFERENCES review_tasks(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS sandbox_runs (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			command TEXT NOT NULL,
			args_json TEXT NOT NULL,
			executor TEXT NOT NULL,
			status TEXT NOT NULL,
			exit_code INTEGER NOT NULL,
			stdout TEXT NOT NULL,
			stderr TEXT NOT NULL,
			error_type TEXT NOT NULL,
			started_at TEXT NOT NULL,
			duration_ms INTEGER NOT NULL,
			timed_out INTEGER NOT NULL,
			output_truncated INTEGER NOT NULL,
			FOREIGN KEY(task_id) REFERENCES review_tasks(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS permission_decisions (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			tool TEXT NOT NULL,
			command TEXT NOT NULL,
			action TEXT NOT NULL,
			disposition TEXT NOT NULL DEFAULT '',
			reason TEXT NOT NULL,
			created_at TEXT NOT NULL,
			FOREIGN KEY(task_id) REFERENCES review_tasks(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS findings (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			bucket TEXT NOT NULL,
			severity TEXT NOT NULL,
			category TEXT NOT NULL,
			file TEXT NOT NULL,
			line INTEGER NOT NULL,
			title TEXT NOT NULL,
			evidence TEXT NOT NULL,
			recommendation TEXT NOT NULL,
			confidence REAL NOT NULL,
			source TEXT NOT NULL,
			rule_id TEXT NOT NULL,
			fingerprint TEXT NOT NULL DEFAULT '',
			FOREIGN KEY(task_id) REFERENCES review_tasks(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS artifacts (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			name TEXT NOT NULL,
			path TEXT NOT NULL,
			mime_type TEXT NOT NULL,
			size_bytes INTEGER NOT NULL,
			created_at TEXT NOT NULL,
			FOREIGN KEY(task_id) REFERENCES review_tasks(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS reports (
			task_id TEXT PRIMARY KEY,
			json_path TEXT NOT NULL,
			md_path TEXT NOT NULL,
			conclusion TEXT NOT NULL,
			created_at TEXT NOT NULL,
			FOREIGN KEY(task_id) REFERENCES review_tasks(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS audit_metrics (
			task_id TEXT PRIMARY KEY,
			metrics_json TEXT NOT NULL,
			created_at TEXT NOT NULL,
			FOREIGN KEY(task_id) REFERENCES review_tasks(id) ON DELETE CASCADE
		)`,
	}
	if _, err := s.db.ExecContext(ctx, `PRAGMA foreign_keys=ON`); err != nil {
		return err
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	if err := s.migrate(ctx); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES (?, ?)`,
		schemaVersion, time.Now().Format(time.RFC3339Nano),
	); err != nil {
		return err
	}
	return nil
}

func (s *Store) migrate(ctx context.Context) error {
	if err := ensureColumn(ctx, s.db, "findings", "fingerprint", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, s.db, "review_inputs", "packages_json", "TEXT NOT NULL DEFAULT '[]'"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, s.db, "permission_decisions", "disposition", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	stmts := []string{
		`CREATE INDEX IF NOT EXISTS idx_sandbox_runs_task_id ON sandbox_runs(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_permission_decisions_task_id ON permission_decisions(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_findings_task_id ON findings(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_findings_task_fingerprint ON findings(task_id, bucket, fingerprint)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_findings_task_fingerprint_unique
		 ON findings(task_id, bucket, fingerprint) WHERE fingerprint <> ''`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func ensureColumn(ctx context.Context, db *sql.DB, table, column, definition string) error {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var dfltValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
			return err
		}
		if name == column {
			return rows.Err()
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, column, definition))
	return err
}

func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var version int
	err := s.db.QueryRowContext(ctx, `SELECT max(version) FROM schema_migrations`).Scan(&version)
	return version, err
}

func (s *Store) SaveReport(ctx context.Context, report ReviewReport, pd ParsedDiff, jsonPath, mdPath string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO review_tasks(id, status, input_mode, started_at, ended_at)
		 VALUES (?, ?, ?, ?, ?)`,
		report.Task.ID, report.Task.Status, report.Task.InputMode,
		report.Task.StartedAt.Format(time.RFC3339Nano),
		report.Task.EndedAt.Format(time.RFC3339Nano),
	); err != nil {
		return err
	}
	summaryJSON, _ := json.Marshal(pd.Summary)
	packagesJSON, _ := json.Marshal(pd.Packages)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO review_inputs(task_id, diff_hash, summary_json, packages_json, diff_preview)
		 VALUES (?, ?, ?, ?, ?)`,
		report.Task.ID, pd.RawHash, string(summaryJSON), string(packagesJSON),
		truncate(redactSecrets(pd.Raw), 4096),
	); err != nil {
		return err
	}
	for _, run := range report.SandboxRuns {
		argsJSON, _ := json.Marshal(run.Args)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO sandbox_runs(id, task_id, command, args_json, executor, status,
			 exit_code, stdout, stderr, error_type, started_at, duration_ms, timed_out,
			 output_truncated)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			run.ID, report.Task.ID, run.Command, string(argsJSON), run.Executor,
			run.Status, run.ExitCode, redactSecrets(run.Stdout),
			redactSecrets(run.Stderr), run.ErrorType,
			run.StartedAt.Format(time.RFC3339Nano),
			run.DurationMS, boolInt(run.TimedOut),
			boolInt(run.OutputTruncated),
		); err != nil {
			return err
		}
	}
	for _, decision := range report.Permissions {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO permission_decisions(id, task_id, tool, command, action, disposition, reason, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			decision.ID, report.Task.ID, decision.Tool, decision.Command,
			decision.Action, firstNonEmpty(decision.Disposition, permissionDisposition(decision.Action)), decision.Reason,
			decision.CreatedAt.Format(time.RFC3339Nano),
		); err != nil {
			return err
		}
	}
	if err := insertFindings(ctx, tx, report.Task.ID, "finding", report.Findings); err != nil {
		return err
	}
	if err := insertFindings(ctx, tx, report.Task.ID, "warning", report.Warnings); err != nil {
		return err
	}
	if err := insertFindings(ctx, tx, report.Task.ID, "needs_human_review", report.NeedsHumanReview); err != nil {
		return err
	}
	for _, artifact := range report.Artifacts {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO artifacts(id, task_id, name, path, mime_type, size_bytes, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			artifact.ID, report.Task.ID, artifact.Name, artifact.Path,
			artifact.MimeType, artifact.SizeBytes,
			artifact.CreatedAt.Format(time.RFC3339Nano),
		); err != nil {
			return err
		}
	}
	metricsJSON, _ := json.Marshal(report.Metrics)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO audit_metrics(task_id, metrics_json, created_at)
		 VALUES (?, ?, ?)`,
		report.Task.ID, string(metricsJSON), time.Now().Format(time.RFC3339Nano),
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO reports(task_id, json_path, md_path, conclusion, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		report.Task.ID, jsonPath, mdPath, report.Conclusion,
		time.Now().Format(time.RFC3339Nano),
	); err != nil {
		return err
	}
	return tx.Commit()
}

func insertFindings(ctx context.Context, tx *sql.Tx, taskID, bucket string, findings []Finding) error {
	for _, f := range findings {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO findings(id, task_id, bucket, severity, category, file, line,
			 title, evidence, recommendation, confidence, source, rule_id, fingerprint)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			newID("finding"), taskID, bucket, f.Severity, f.Category,
			f.File, f.Line, f.Title, redactSecrets(f.Evidence),
			redactSecrets(f.Recommendation), f.Confidence, f.Source, f.RuleID,
			firstNonEmpty(f.Fingerprint, findingFingerprint(f)),
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) LoadTaskReport(ctx context.Context, taskID string) (ReviewReport, error) {
	var report ReviewReport
	row := s.db.QueryRowContext(ctx,
		`SELECT id, status, input_mode, started_at, ended_at FROM review_tasks WHERE id = ?`,
		taskID,
	)
	var started, ended string
	if err := row.Scan(&report.Task.ID, &report.Task.Status, &report.Task.InputMode, &started, &ended); err != nil {
		return ReviewReport{}, err
	}
	report.Task.StartedAt, _ = time.Parse(time.RFC3339Nano, started)
	report.Task.EndedAt, _ = time.Parse(time.RFC3339Nano, ended)
	if err := s.loadInput(ctx, taskID, &report); err != nil {
		return ReviewReport{}, err
	}
	if err := s.loadFindings(ctx, taskID, &report); err != nil {
		return ReviewReport{}, err
	}
	if err := s.loadSandboxRuns(ctx, taskID, &report); err != nil {
		return ReviewReport{}, err
	}
	if err := s.loadPermissionDecisions(ctx, taskID, &report); err != nil {
		return ReviewReport{}, err
	}
	if err := s.loadArtifacts(ctx, taskID, &report); err != nil {
		return ReviewReport{}, err
	}
	if err := s.loadMetrics(ctx, taskID, &report); err != nil {
		return ReviewReport{}, err
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT conclusion FROM reports WHERE task_id = ?`, taskID,
	).Scan(&report.Conclusion); err != nil && err != sql.ErrNoRows {
		return ReviewReport{}, err
	}
	return report, nil
}

func (s *Store) loadInput(ctx context.Context, taskID string, report *ReviewReport) error {
	var summaryJSON, packagesJSON string
	err := s.db.QueryRowContext(ctx,
		`SELECT summary_json, packages_json FROM review_inputs WHERE task_id = ?`, taskID,
	).Scan(&summaryJSON, &packagesJSON)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(summaryJSON), &report.Input); err != nil {
		return err
	}
	return json.Unmarshal([]byte(packagesJSON), &report.Packages)
}

func (s *Store) loadFindings(ctx context.Context, taskID string, report *ReviewReport) error {
	rows, err := s.db.QueryContext(ctx,
		`SELECT bucket, severity, category, file, line, title, evidence,
		 recommendation, confidence, source, rule_id, fingerprint
		 FROM findings WHERE task_id = ? ORDER BY file, line, rule_id`,
		taskID,
	)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var bucket string
		var f Finding
		if err := rows.Scan(&bucket, &f.Severity, &f.Category, &f.File, &f.Line,
			&f.Title, &f.Evidence, &f.Recommendation, &f.Confidence,
			&f.Source, &f.RuleID, &f.Fingerprint); err != nil {
			return err
		}
		switch bucket {
		case "finding":
			report.Findings = append(report.Findings, f)
		case "warning":
			report.Warnings = append(report.Warnings, f)
		default:
			report.NeedsHumanReview = append(report.NeedsHumanReview, f)
		}
	}
	return rows.Err()
}

func (s *Store) loadSandboxRuns(ctx context.Context, taskID string, report *ReviewReport) error {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, command, args_json, executor, status, exit_code, stdout,
		 stderr, error_type, started_at, duration_ms, timed_out, output_truncated
		 FROM sandbox_runs WHERE task_id = ? ORDER BY started_at, id`,
		taskID,
	)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var run SandboxRun
		var argsJSON, started string
		var timedOut, outputTruncated int
		if err := rows.Scan(&run.ID, &run.Command, &argsJSON, &run.Executor,
			&run.Status, &run.ExitCode, &run.Stdout, &run.Stderr,
			&run.ErrorType, &started, &run.DurationMS, &timedOut,
			&outputTruncated); err != nil {
			return err
		}
		run.TaskID = taskID
		_ = json.Unmarshal([]byte(argsJSON), &run.Args)
		run.StartedAt, _ = time.Parse(time.RFC3339Nano, started)
		run.TimedOut = timedOut != 0
		run.OutputTruncated = outputTruncated != 0
		report.SandboxRuns = append(report.SandboxRuns, run)
	}
	return rows.Err()
}

func (s *Store) loadPermissionDecisions(ctx context.Context, taskID string, report *ReviewReport) error {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, tool, command, action, disposition, reason, created_at
		 FROM permission_decisions WHERE task_id = ? ORDER BY created_at, id`,
		taskID,
	)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var d PermissionDecisionRecord
		var created string
		if err := rows.Scan(&d.ID, &d.Tool, &d.Command, &d.Action, &d.Disposition, &d.Reason, &created); err != nil {
			return err
		}
		if d.Disposition == "" {
			d.Disposition = permissionDisposition(d.Action)
		}
		d.TaskID = taskID
		d.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		report.Permissions = append(report.Permissions, d)
	}
	return rows.Err()
}

func (s *Store) loadArtifacts(ctx context.Context, taskID string, report *ReviewReport) error {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, path, mime_type, size_bytes, created_at
		 FROM artifacts WHERE task_id = ? ORDER BY created_at, id`,
		taskID,
	)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var a ArtifactRecord
		var created string
		if err := rows.Scan(&a.ID, &a.Name, &a.Path, &a.MimeType, &a.SizeBytes, &created); err != nil {
			return err
		}
		a.TaskID = taskID
		a.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		report.Artifacts = append(report.Artifacts, a)
	}
	return rows.Err()
}

func (s *Store) loadMetrics(ctx context.Context, taskID string, report *ReviewReport) error {
	var raw string
	err := s.db.QueryRowContext(ctx,
		`SELECT metrics_json FROM audit_metrics WHERE task_id = ?`, taskID,
	).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(raw), &report.Metrics)
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "...[truncated]"
}

func wrapStoreErr(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", op, err)
}
