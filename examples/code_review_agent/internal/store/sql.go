//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package store

// SQL statements for the review store. Column order in each statement is the
// source of truth for the matching argument list or Scan destinations in
// store.go / query.go — keep those sites in the same order when either changes.
const (
	_sqlUpsertReviewTask = `
INSERT INTO review_tasks (
	task_id,
	app_name,
	user_id,
	task_status,
	input_kind,
	input_summary_json,
	input_artifact_name,
	input_artifact_version,
	monitoring_summary_json,
	conclusion,
	json_report_name,
	json_report_version,
	markdown_report_name,
	markdown_report_version,
	started_at,
	finished_at,
	error_type,
	error_message
) VALUES (
	?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
)
ON CONFLICT(task_id) DO UPDATE SET
	app_name = excluded.app_name,
	user_id = excluded.user_id,
	task_status = excluded.task_status,
	input_kind = excluded.input_kind,
	input_summary_json = excluded.input_summary_json,
	input_artifact_name = excluded.input_artifact_name,
	input_artifact_version = excluded.input_artifact_version,
	monitoring_summary_json = excluded.monitoring_summary_json,
	conclusion = excluded.conclusion,
	json_report_name = excluded.json_report_name,
	json_report_version = excluded.json_report_version,
	markdown_report_name = excluded.markdown_report_name,
	markdown_report_version = excluded.markdown_report_version,
	started_at = excluded.started_at,
	finished_at = excluded.finished_at,
	error_type = excluded.error_type,
	error_message = excluded.error_message,
	updated_at = CURRENT_TIMESTAMP
`

	_sqlUpdateTaskInput = `
UPDATE review_tasks SET
	input_kind = ?,
	input_summary_json = ?,
	input_artifact_name = ?,
	input_artifact_version = ?,
	updated_at = CURRENT_TIMESTAMP
WHERE task_id = ?
`

	_sqlSavePermissionDecision = `
INSERT INTO permission_decisions (
	task_id,
	tool_call_id,
	decision_kind,
	operation,
	tool_name,
	command_preview,
	decision,
	reason,
	decided_at
) VALUES (
	?, ?, ?, ?, ?, ?, ?, ?, ?
)
`

	_sqlSaveSandboxRun = `
INSERT INTO sandbox_runs (
	task_id,
	tool_call_id,
	backend,
	workdir,
	command_preview,
	sandbox_status,
	exit_code,
	timed_out,
	output_summary,
	output_truncated,
	redaction_count,
	started_at,
	finished_at,
	duration_ms,
	error_type,
	error_message
) VALUES (
	?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
)
`

	_sqlTouchReviewTask = `
UPDATE review_tasks SET updated_at = updated_at WHERE task_id = ?
`

	_sqlDeleteReviewResults = `
DELETE FROM review_results WHERE task_id = ?
`

	_sqlInsertReviewResult = `
INSERT INTO review_results (
	task_id,
	result_kind,
	severity,
	category,
	file_path,
	line,
	title,
	evidence,
	recommendation,
	confidence,
	source,
	rule_id,
	created_at
) VALUES (
	?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
)
`

	_sqlUpdateTaskConclusion = `
UPDATE review_tasks SET
	conclusion = ?,
	updated_at = CURRENT_TIMESTAMP
WHERE task_id = ?
`

	_sqlFinalizeTask = `
UPDATE review_tasks SET
	task_status = ?,
	monitoring_summary_json = ?,
	json_report_name = ?,
	json_report_version = ?,
	markdown_report_name = ?,
	markdown_report_version = ?,
	finished_at = ?,
	error_type = ?,
	error_message = ?,
	updated_at = CURRENT_TIMESTAMP
WHERE task_id = ?
`

	_sqlSelectReviewTask = `
SELECT
	task_id,
	app_name,
	user_id,
	task_status,
	input_kind,
	input_summary_json,
	input_artifact_name,
	input_artifact_version,
	monitoring_summary_json,
	conclusion,
	json_report_name,
	json_report_version,
	markdown_report_name,
	markdown_report_version,
	started_at,
	finished_at,
	error_type,
	error_message
FROM review_tasks
WHERE task_id = ?
`

	_sqlSelectPermissionDecisions = `
SELECT
	COALESCE(tool_call_id, ''),
	decision_kind,
	operation,
	COALESCE(tool_name, ''),
	COALESCE(command_preview, ''),
	decision,
	COALESCE(reason, ''),
	decided_at
FROM permission_decisions
WHERE task_id = ?
ORDER BY id
`

	_sqlSelectSandboxRuns = `
SELECT
	COALESCE(tool_call_id, ''),
	backend,
	COALESCE(workdir, ''),
	command_preview,
	sandbox_status,
	exit_code,
	timed_out,
	COALESCE(output_summary, ''),
	output_truncated,
	redaction_count,
	started_at,
	finished_at,
	duration_ms,
	COALESCE(error_type, ''),
	COALESCE(error_message, '')
FROM sandbox_runs
WHERE task_id = ?
ORDER BY id
`

	_sqlSelectReviewResults = `
SELECT
	result_kind,
	severity,
	category,
	file_path,
	line,
	title,
	evidence,
	COALESCE(recommendation, ''),
	confidence,
	source,
	rule_id,
	created_at
FROM review_results
WHERE task_id = ?
ORDER BY id
`
)
