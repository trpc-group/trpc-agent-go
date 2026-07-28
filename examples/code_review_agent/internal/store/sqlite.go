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
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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
		if artifact.Kind == "check-result" {
			return errors.New(
				"temporary check result cannot be persisted as an artifact",
			)
		}
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
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Review{}, fmt.Errorf("begin review snapshot: %w", err)
	}
	loader := loadReview
	if s.loadSnapshot != nil {
		loader = s.loadSnapshot
	}
	result, err := loader(ctx, tx, taskID)
	if err != nil {
		return Review{}, errors.Join(err, tx.Rollback())
	}
	if err := tx.Commit(); err != nil {
		return Review{}, fmt.Errorf("commit review snapshot: %w", err)
	}
	return result, nil
}

type reviewQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func loadReview(ctx context.Context, queryer reviewQueryer, taskID string) (Review, error) {
	result := Review{}
	var err error
	if result.Task, err = loadTask(ctx, queryer, taskID); err != nil {
		return Review{}, err
	}
	if result.Input, err = loadInputSummary(ctx, queryer, taskID); err != nil {
		return Review{}, err
	}
	if result.Runs, err = loadRuns(ctx, queryer, taskID); err != nil {
		return Review{}, err
	}
	if result.Decisions, err = loadDecisions(ctx, queryer, taskID); err != nil {
		return Review{}, err
	}
	if result.Findings, err = loadFindings(ctx, queryer, taskID); err != nil {
		return Review{}, err
	}
	if result.Metrics, err = loadMetrics(ctx, queryer, taskID); err != nil {
		return Review{}, err
	}
	if result.Artifacts, err = loadArtifacts(ctx, queryer, taskID); err != nil {
		return Review{}, err
	}
	if result.Report, err = loadReport(ctx, queryer, taskID); err != nil {
		return Review{}, err
	}
	return result, nil
}
func loadTask(ctx context.Context, db reviewQueryer, taskID string) (Task, error) {
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
func loadInputSummary(ctx context.Context, db reviewQueryer, taskID string) (InputSummary, error) {
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
func loadRuns(ctx context.Context, db reviewQueryer, taskID string) (result []SandboxRun, resultErr error) {
	const query = `SELECT id,check_id,runtime,status,duration_ms,exit_code,timed_out,
        output_truncated,stdout,stderr,error_type,error,result_sha256,result_size_bytes
        FROM sandbox_runs WHERE task_id=? ORDER BY id`
	rows, err := db.QueryContext(ctx, query, taskID)
	if err != nil {
		return nil, fmt.Errorf("query sandbox runs: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, rows.Close())
	}()
	for rows.Next() {
		var run SandboxRun
		if err := rows.Scan(&run.ID, &run.CheckID, &run.Runtime, &run.Status, &run.DurationMS, &run.ExitCode, &run.TimedOut, &run.OutputTruncated, &run.Stdout, &run.Stderr, &run.ErrorType, &run.Error, &run.ResultSHA256, &run.ResultSizeBytes); err != nil {
			return nil, fmt.Errorf("scan sandbox run: %w", err)
		}
		result = append(result, run)
	}
	return result, rows.Err()
}
func loadDecisions(ctx context.Context, db reviewQueryer, taskID string) (result []Decision, resultErr error) {
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
func loadFindings(ctx context.Context, db reviewQueryer, taskID string) (result []reviewmodel.Finding, resultErr error) {
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
func loadMetrics(ctx context.Context, db reviewQueryer, taskID string) (Metrics, error) {
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
func loadArtifacts(ctx context.Context, db reviewQueryer, taskID string) (result []Artifact, resultErr error) {
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
func loadReport(ctx context.Context, db reviewQueryer, taskID string) (Report, error) {
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
	driverName                  = "sqlite3"
	busyTimeoutMS               = "5000"
	maxConnections              = 1
	foreignKeysFlag             = "on"
	evidenceReportSchemaVersion = "1.1.0"
)

// SQLiteStore persists complete review aggregates in SQLite.
type SQLiteStore struct {
	db           *sql.DB
	loadSnapshot func(context.Context, reviewQueryer, string) (Review, error)
}

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
		return "file::memory:?cache=private&_foreign_keys=on&_busy_timeout=" + busyTimeoutMS, nil
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
	if err := s.ensureSandboxRunEvidenceColumns(ctx); err != nil {
		return err
	}
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping review database: %w", err)
	}
	return nil
}

func (s *SQLiteStore) ensureSandboxRunEvidenceColumns(ctx context.Context) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open migration connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("begin sandbox run evidence migration: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
		}
	}()

	rows, err := conn.QueryContext(ctx, `PRAGMA table_info(sandbox_runs)`)
	if err != nil {
		return fmt.Errorf("inspect sandbox run schema: %w", err)
	}
	columns := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return errors.Join(fmt.Errorf("scan sandbox run schema: %w", err), rows.Close())
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		return errors.Join(fmt.Errorf("read sandbox run schema: %w", err), rows.Close())
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close sandbox run schema rows: %w", err)
	}
	evidenceMigrations := []struct {
		name, statement string
	}{
		{"result_sha256", `ALTER TABLE sandbox_runs ADD COLUMN result_sha256 TEXT NOT NULL DEFAULT ''`},
		{"result_size_bytes", `ALTER TABLE sandbox_runs ADD COLUMN result_size_bytes INTEGER NOT NULL DEFAULT 0 CHECK (result_size_bytes >= 0)`},
	}
	missing := false
	for _, migration := range evidenceMigrations {
		if !columns[migration.name] {
			missing = true
			break
		}
	}
	if missing {
		legacyEvidence, err := loadLegacyResultEvidence(ctx, conn)
		if err != nil {
			return err
		}
		for _, migration := range evidenceMigrations {
			if columns[migration.name] {
				continue
			}
			if _, err := conn.ExecContext(ctx, migration.statement); err != nil {
				return fmt.Errorf("add sandbox run %s: %w", migration.name, err)
			}
		}
		for runID, evidence := range legacyEvidence.byRun {
			if _, err := conn.ExecContext(ctx, `
				UPDATE sandbox_runs
				SET result_sha256=?, result_size_bytes=?
				WHERE id=?
			`, evidence.ResultSHA256, evidence.ResultSizeBytes, runID); err != nil {
				return fmt.Errorf("backfill sandbox run evidence: %w", err)
			}
		}
		if err := migrateLegacyReports(ctx, conn, legacyEvidence); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx,
			`DELETE FROM artifacts WHERE kind = 'check-result'`,
		); err != nil {
			return fmt.Errorf("remove legacy result artifacts: %w", err)
		}
	}

	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("commit sandbox run evidence migration: %w", err)
	}
	committed = true
	return nil
}

type legacyResultEvidence struct {
	byRun         map[string]SandboxRun
	artifactIDs   map[string]bool
	affectedTasks map[string]bool
}

func loadLegacyResultEvidence(
	ctx context.Context,
	conn *sql.Conn,
) (legacyResultEvidence, error) {
	result := legacyResultEvidence{
		byRun:         make(map[string]SandboxRun),
		artifactIDs:   make(map[string]bool),
		affectedTasks: make(map[string]bool),
	}
	rows, err := conn.QueryContext(ctx, `
		SELECT
			artifacts.id,
			artifacts.task_id,
			artifacts.run_id,
			artifacts.sha256,
			artifacts.size_bytes,
			sandbox_runs.task_id
		FROM artifacts
		LEFT JOIN sandbox_runs ON sandbox_runs.id=artifacts.run_id
		WHERE artifacts.kind='check-result'
		ORDER BY artifacts.id
	`)
	if err != nil {
		return result, fmt.Errorf("query legacy result artifacts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var artifactID, taskID, digest string
		var runID, runTaskID sql.NullString
		var size int64
		if err := rows.Scan(
			&artifactID,
			&taskID,
			&runID,
			&digest,
			&size,
			&runTaskID,
		); err != nil {
			return result, fmt.Errorf("scan legacy result artifact: %w", err)
		}
		result.artifactIDs[artifactID] = true
		result.affectedTasks[taskID] = true
		if !runID.Valid {
			continue
		}
		if !runTaskID.Valid || runTaskID.String != taskID {
			return result, fmt.Errorf(
				"legacy result artifact %q crosses review tasks",
				artifactID,
			)
		}
		evidence := SandboxRun{
			ResultSHA256:    digest,
			ResultSizeBytes: size,
		}
		if err := validateRunEvidence(evidence); err != nil {
			return result, fmt.Errorf(
				"validate legacy result artifact %q: %w",
				artifactID,
				err,
			)
		}
		if _, exists := result.byRun[runID.String]; exists {
			return result, fmt.Errorf(
				"multiple legacy result artifacts for run %q",
				runID.String,
			)
		}
		result.byRun[runID.String] = evidence
	}
	if err := rows.Err(); err != nil {
		return result, fmt.Errorf("read legacy result artifacts: %w", err)
	}
	return result, nil
}

func migrateLegacyReports(
	ctx context.Context,
	conn *sql.Conn,
	evidence legacyResultEvidence,
) error {
	for taskID := range evidence.affectedTasks {
		var canonicalJSON, canonicalMarkdown string
		err := conn.QueryRowContext(ctx, `
			SELECT canonical_json,canonical_markdown
			FROM reports WHERE task_id=?
		`, taskID).Scan(&canonicalJSON, &canonicalMarkdown)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("load legacy report %q: %w", taskID, err)
		}
		migratedJSON, err := migrateLegacyReportJSON(
			canonicalJSON,
			evidence,
		)
		if err != nil {
			return fmt.Errorf("migrate legacy report %q: %w", taskID, err)
		}
		migratedMarkdown, err := migrateLegacyReportMarkdown(
			canonicalMarkdown,
			canonicalJSON,
			evidence,
		)
		if err != nil {
			return fmt.Errorf(
				"migrate legacy report Markdown %q: %w",
				taskID,
				err,
			)
		}
		jsonDigest := sha256.Sum256([]byte(migratedJSON))
		markdownDigest := sha256.Sum256([]byte(migratedMarkdown))
		if _, err := conn.ExecContext(ctx, `
			UPDATE reports SET
				schema_version=?,
				canonical_json=?,
				canonical_markdown=?,
				json_path='',
				json_sha256=?,
				markdown_path='',
				markdown_sha256=?
			WHERE task_id=?
		`,
			evidenceReportSchemaVersion,
			migratedJSON,
			migratedMarkdown,
			hex.EncodeToString(jsonDigest[:]),
			hex.EncodeToString(markdownDigest[:]),
			taskID,
		); err != nil {
			return fmt.Errorf("save migrated report %q: %w", taskID, err)
		}
	}
	return nil
}

func migrateLegacyReportJSON(
	value string,
	evidence legacyResultEvidence,
) (string, error) {
	var document map[string]any
	if err := json.Unmarshal([]byte(value), &document); err != nil {
		return "", fmt.Errorf("decode canonical JSON: %w", err)
	}
	if document == nil {
		return "", errors.New("canonical JSON must be an object")
	}
	document["schema_version"] = evidenceReportSchemaVersion
	if runs, ok := document["sandbox_runs"].([]any); ok {
		for _, value := range runs {
			run, ok := value.(map[string]any)
			if !ok {
				continue
			}
			runID, _ := run["id"].(string)
			if item, exists := evidence.byRun[runID]; exists {
				run["result_sha256"] = item.ResultSHA256
				run["result_size_bytes"] = item.ResultSizeBytes
			}
		}
	}
	if artifacts, ok := document["artifacts"].([]any); ok {
		filtered := artifacts[:0]
		for _, value := range artifacts {
			artifact, ok := value.(map[string]any)
			artifactID, _ := artifact["id"].(string)
			if ok && evidence.artifactIDs[artifactID] {
				continue
			}
			filtered = append(filtered, value)
		}
		document["artifacts"] = filtered
	}
	content, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode canonical JSON: %w", err)
	}
	return string(append(content, '\n')), nil
}

func migrateLegacyReportMarkdown(
	value string,
	canonicalJSON string,
	evidence legacyResultEvidence,
) (string, error) {
	var index struct {
		SandboxRuns []struct {
			ID string `json:"id"`
		} `json:"sandbox_runs"`
		Artifacts []struct {
			ID string `json:"id"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal([]byte(canonicalJSON), &index); err != nil {
		return "", fmt.Errorf("decode canonical JSON index: %w", err)
	}

	const (
		runsHeading       = "\n## Sandbox runs\n"
		monitoringHeading = "\n## Monitoring\n"
		artifactsHeading  = "\n## Artifacts\n"
	)
	runsStart := strings.Index(value, runsHeading)
	monitoringStart := strings.Index(value, monitoringHeading)
	artifactsStart := strings.Index(value, artifactsHeading)
	if runsStart < 0 || monitoringStart < runsStart ||
		artifactsStart < monitoringStart {
		return "", errors.New("canonical Markdown sections are missing")
	}
	runsBodyStart := runsStart + len(runsHeading)
	runsBody := value[runsBodyStart:monitoringStart]
	runLines := strings.Split(runsBody, "\n")
	runIndex := 0
	for lineIndex, line := range runLines {
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		if runIndex >= len(index.SandboxRuns) {
			return "", errors.New("canonical Markdown has extra sandbox runs")
		}
		runID := index.SandboxRuns[runIndex].ID
		if item, exists := evidence.byRun[runID]; exists {
			runLines[lineIndex] += fmt.Sprintf(
				", result=%d bytes, sha256=%s",
				item.ResultSizeBytes,
				item.ResultSHA256,
			)
		}
		runIndex++
	}
	if runIndex != len(index.SandboxRuns) {
		return "", errors.New("canonical Markdown sandbox runs are incomplete")
	}
	migratedRuns := strings.Join(runLines, "\n")

	artifactsBodyStart := artifactsStart + len(artifactsHeading)
	artifactLines := strings.Split(value[artifactsBodyStart:], "\n")
	artifactIndex := 0
	keptArtifacts := 0
	filteredLines := make([]string, 0, len(artifactLines))
	for _, line := range artifactLines {
		if !strings.HasPrefix(line, "- ") {
			filteredLines = append(filteredLines, line)
			continue
		}
		if artifactIndex >= len(index.Artifacts) {
			return "", errors.New("canonical Markdown has extra artifacts")
		}
		artifactID := index.Artifacts[artifactIndex].ID
		artifactIndex++
		if evidence.artifactIDs[artifactID] {
			continue
		}
		keptArtifacts++
		filteredLines = append(filteredLines, line)
	}
	if artifactIndex != len(index.Artifacts) {
		return "", errors.New("canonical Markdown artifacts are incomplete")
	}
	if keptArtifacts == 0 {
		filteredLines = []string{"No artifacts.", ""}
	}
	migratedArtifacts := strings.Join(filteredLines, "\n")
	return value[:runsBodyStart] + migratedRuns +
		value[monitoringStart:artifactsBodyStart] + migratedArtifacts, nil
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

// CreateTask inserts a running review task. IDs that contain detectable
// credentials are rejected so relationship keys are never redacted or leaked.
func (s *SQLiteStore) CreateTask(ctx context.Context, task Task) error {
	if task.ID == "" || redact.ContainsSecret(task.ID) || task.Status != StatusRunning || task.StartedAt.IsZero() {
		return fmt.Errorf("create task: %w", ErrInvalidTransition)
	}
	_, err := s.db.ExecContext(ctx, insertTaskSQL, task.ID, task.Status, redact.String(task.InputKind), task.InputDigest, task.StartedAt.UTC().Format(timeFormat), redact.String(task.Conclusion), redact.String(task.Error))
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
	if err := validateRunEvidence(run); err != nil {
		return err
	}
	const query = `INSERT INTO sandbox_runs
        (id,task_id,check_id,runtime,status,duration_ms,exit_code,timed_out,output_truncated,stdout,stderr,error_type,error,result_sha256,result_size_bytes)
        VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	_, err := s.db.ExecContext(ctx, query, run.ID, taskID, redact.String(run.CheckID), redact.String(run.Runtime), redact.String(run.Status), run.DurationMS, run.ExitCode, run.TimedOut, run.OutputTruncated, redact.String(run.Stdout), redact.String(run.Stderr), redact.String(run.ErrorType), redact.String(run.Error), run.ResultSHA256, run.ResultSizeBytes)
	if err != nil {
		return fmt.Errorf("save sandbox run: %w", err)
	}
	return nil
}

func validateRunEvidence(run SandboxRun) error {
	if run.ResultSHA256 == "" && run.ResultSizeBytes == 0 {
		return nil
	}
	if run.ResultSizeBytes <= 0 || run.ResultSizeBytes > 160<<10 ||
		run.ResultSHA256 != strings.ToLower(run.ResultSHA256) {
		return errors.New("invalid sandbox result evidence")
	}
	decoded, err := hex.DecodeString(run.ResultSHA256)
	if err != nil || len(decoded) != sha256.Size {
		return errors.New("invalid sandbox result evidence")
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
