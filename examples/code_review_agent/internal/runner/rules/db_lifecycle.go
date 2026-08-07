//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package rules

import (
	"context"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/finding"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/runner"
)

// DBLifecycleRule detects database transaction and connection lifecycle issues.
type DBLifecycleRule struct {
	runner.RuleBase
}

// NewDBLifecycleRule creates a new database lifecycle rule.
func NewDBLifecycleRule() *DBLifecycleRule {
	return &DBLifecycleRule{
		RuleBase: runner.RuleBase{
			IDValue:       "GO_DB_TX_NO_ROLLBACK",
			CategoryValue: finding.CategoryDBLifecycle,
			DefaultSev:    finding.SeverityHigh,
		},
	}
}

// Check examines file content for database lifecycle issues.
func (r *DBLifecycleRule) Check(ctx context.Context, file finding.ChangedFileInfo, content string) ([]finding.Finding, error) {
	if !strings.HasSuffix(file.File, ".go") {
		return nil, nil
	}

	var findings []finding.Finding
	lines := strings.Split(content, "\n")

	// Detect BeginTx / Begin() without deferred Rollback.
	for i, line := range lines {
		lineNum := i + 1
		trimmed := strings.TrimSpace(line)

		// Pattern 1: tx, err := db.Begin() or db.BeginTx() without defer tx.Rollback().
		if isBeginTransaction(trimmed) {
			if !hasDeferredRollback(lines, i) {
				findings = append(findings, runner.NewFinding(
					&r.RuleBase, file.File, lineNum,
					"Transaction without deferred Rollback",
					trimmed,
					"Add 'defer tx.Rollback()' immediately after Begin to ensure rollback on error",
					finding.ConfidenceHigh,
				))
			}
		}

		// Pattern 2: rows.Close() without rows.Err() check.
		if strings.Contains(trimmed, "rows.Close()") && strings.HasPrefix(trimmed, "defer ") {
			evidence := trimmed
			findings = append(findings, runner.NewFinding(
				&r.RuleBase, file.File, lineNum,
				"rows deferred Close without Err() check",
				evidence,
				"Add 'defer rows.Close()' followed by 'if err := rows.Err(); err != nil { ... }'",
				finding.ConfidenceMedium,
			))
		}

		// Pattern 3: Transaction with external HTTP call inside (long transaction risk).
		if isNestedHTTPInTransaction(trimmed, lines, i) {
			findings = append(findings, runner.NewFinding(
				&r.RuleBase, file.File, lineNum,
				"External HTTP call inside a database transaction",
				trimmed,
				"Avoid external HTTP calls inside transactions; they can cause long-running locks",
				finding.ConfidenceLow,
			))
		}

		// Pattern 4: SetMaxOpenConns / SetMaxIdleConns / SetConnMaxLifetime missing in init.
		if strings.Contains(trimmed, "sql.Open(") || strings.Contains(trimmed, "sqlx.Connect(") {
			if !hasConnPoolConfig(lines, i) {
				findings = append(findings, runner.NewFinding(
					&r.RuleBase, file.File, lineNum,
					"DB connection opened without pool configuration",
					trimmed,
					"Configure SetMaxOpenConns, SetMaxIdleConns, and SetConnMaxLifetime on the sql.DB",
					finding.ConfidenceLow,
				))
			}
		}
	}

	return findings, nil
}

func isBeginTransaction(line string) bool {
	return strings.Contains(line, ".BeginTx(") || strings.HasSuffix(strings.TrimSpace(line), ".Begin()") ||
		strings.Contains(line, ".BeginTxx(") || strings.Contains(line, ".Beginx(")
}

func hasDeferredRollback(lines []string, startIdx int) bool {
	end := startIdx + 5
	if end > len(lines) {
		end = len(lines)
	}
	for i := startIdx; i < end; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "defer ") && strings.Contains(trimmed, ".Rollback()") {
			return true
		}
		if strings.HasPrefix(trimmed, "defer ") && strings.Contains(trimmed, ".Rollback(") {
			return true
		}
	}
	return false
}

func isNestedHTTPInTransaction(line string, allLines []string, idx int) bool {
	isHTTP := strings.Contains(line, "http.Get(") || strings.Contains(line, "http.Post(") ||
		strings.Contains(line, "http.PostForm(") || strings.Contains(line, "http.DefaultClient.Do(")
	if !isHTTP {
		return false
	}
	// Check if there's a BeginTx or Begin() call within a reasonable range above.
	start := idx - 15
	if start < 0 {
		start = 0
	}
	for i := start; i < idx; i++ {
		if isBeginTransaction(strings.TrimSpace(allLines[i])) {
			return true
		}
	}
	return false
}

func hasConnPoolConfig(lines []string, startIdx int) bool {
	end := startIdx + 10
	if end > len(lines) {
		end = len(lines)
	}
	for i := startIdx; i < end; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.Contains(trimmed, "SetMaxOpenConns") ||
			strings.Contains(trimmed, "SetMaxIdleConns") ||
			strings.Contains(trimmed, "SetConnMaxLifetime") {
			return true
		}
	}
	return false
}

// DBRowsErrCheckRule detects missing rows.Err() after rows iteration.
type DBRowsErrCheckRule struct {
	runner.RuleBase
}

// NewDBRowsErrCheckRule creates a new rows.Err() check rule.
func NewDBRowsErrCheckRule() *DBRowsErrCheckRule {
	return &DBRowsErrCheckRule{
		RuleBase: runner.RuleBase{
			IDValue:       "GO_DB_ROWS_NO_ERRCHECK",
			CategoryValue: finding.CategoryDBLifecycle,
			DefaultSev:    finding.SeverityMedium,
		},
	}
}

// Check examines file content for missing rows.Err() after iteration.
func (r *DBRowsErrCheckRule) Check(ctx context.Context, file finding.ChangedFileInfo, content string) ([]finding.Finding, error) {
	if !strings.HasSuffix(file.File, ".go") {
		return nil, nil
	}

	var findings []finding.Finding
	lines := strings.Split(content, "\n")

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Detect rows.Next() loop without rows.Err() check after.
		if strings.Contains(trimmed, "rows.Next()") || trimmed == "for rows.Next() {" {
			if !hasErrCheckAfter(lines, i) {
				findings = append(findings, runner.NewFinding(
					&r.RuleBase, file.File, i+1,
					"Missing rows.Err() check after rows iteration",
					trimmed,
					"Add 'if err := rows.Err(); err != nil { ... }' after the rows.Next() loop",
					finding.ConfidenceMedium,
				))
			}
		}
	}

	return findings, nil
}

func hasErrCheckAfter(lines []string, startIdx int) bool {
	end := startIdx + 8
	if end > len(lines) {
		end = len(lines)
	}
	for i := startIdx + 1; i < end; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.Contains(trimmed, "rows.Err()") {
			return true
		}
		// End of function, stop scanning.
		if strings.HasPrefix(trimmed, "func ") {
			return false
		}
	}
	return false
}
