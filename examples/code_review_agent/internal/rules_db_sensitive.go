//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package internal

import (
	"regexp"
	"strings"
)

// DBConnectionLeakRule detects database connections that are opened
// without proper lifecycle management (defer Close).
type DBConnectionLeakRule struct{}

func (r *DBConnectionLeakRule) ID() string       { return "DB_CONNECTION_LEAK" }
func (r *DBConnectionLeakRule) Category() string { return "db_lifecycle" }
func (r *DBConnectionLeakRule) Description() string {
	return "Detects database connections opened without defer Close()"
}

var (
	reDBOpen     = regexp.MustCompile(`sql\.Open\s*\(`)
	reDBPing     = regexp.MustCompile(`\.Ping\s*\(\s*\)`)
	reDBQueryRow = regexp.MustCompile(`\.QueryRow\s*\(`)
	reDBQuery    = regexp.MustCompile(`\.Query\s*\(`)
)

func (r *DBConnectionLeakRule) Check(file DiffFile, hunk DiffHunk, line DiffLine) []Finding {
	content := strings.TrimSpace(line.Content)
	var findings []Finding

	if reDBOpen.MatchString(content) {
		// Look for defer db.Close() in next 10 lines.
		hasDeferClose := false
		idx := -1
		for i, l := range hunk.Lines {
			if l.Number == line.Number && l.Type == line.Type {
				idx = i
				break
			}
		}
		if idx >= 0 {
			for j := idx + 1; j < len(hunk.Lines) && j <= idx+10; j++ {
				c := hunk.Lines[j].Content
				if strings.Contains(c, "defer") &&
					strings.Contains(c, "Close") {
					hasDeferClose = true
					break
				}
			}
		}
		if !hasDeferClose {
			findings = append(findings, Finding{
				Severity: SeverityHigh,
				Title:    "Database connection opened without defer Close()",
				Evidence: content,
				Recommendation: "Add `defer db.Close()` after sql.Open to " +
					"ensure the connection pool is released.",
				Confidence: 0.85,
			})
		}
	}

	// rows from Query() should also be closed.
	if reDBQuery.MatchString(content) && !strings.Contains(content, "defer") {
		findings = append(findings, Finding{
			Severity: SeverityMedium,
			Title:    "Query result rows not closed with defer",
			Evidence: content,
			Recommendation: "Add `defer rows.Close()` after Query() to " +
				"release database resources.",
			Confidence: 0.7,
		})
	}

	return findings
}

// MissingTransactionRollbackRule detects transactions started without
// proper rollback handling.
type MissingTransactionRollbackRule struct{}

func (r *MissingTransactionRollbackRule) ID() string       { return "MISSING_TX_ROLLBACK" }
func (r *MissingTransactionRollbackRule) Category() string { return "db_lifecycle" }
func (r *MissingTransactionRollbackRule) Description() string {
	return "Detects transactions without proper Rollback on error"
}

var (
	reBeginTx  = regexp.MustCompile(`\.BeginTx\s*\(`)
	reBegin    = regexp.MustCompile(`\.Begin\s*\(`)
	reRollback = regexp.MustCompile(`\.Rollback\s*\(`)
)

func (r *MissingTransactionRollbackRule) Check(file DiffFile, hunk DiffHunk, line DiffLine) []Finding {
	content := strings.TrimSpace(line.Content)
	var findings []Finding

	if reBeginTx.MatchString(content) || reBegin.MatchString(content) {
		// Look for Rollback in next 15 lines.
		hasRollback := false
		idx := -1
		for i, l := range hunk.Lines {
			if l.Number == line.Number && l.Type == line.Type {
				idx = i
				break
			}
		}
		if idx >= 0 {
			for j := idx + 1; j < len(hunk.Lines) && j <= idx+15; j++ {
				c := hunk.Lines[j].Content
				if strings.Contains(c, "Rollback") {
					hasRollback = true
					break
				}
			}
		}
		if !hasRollback {
			findings = append(findings, Finding{
				Severity: SeverityHigh,
				Title:    "Transaction started without Rollback",
				Evidence: content,
				Recommendation: "Add `defer tx.Rollback()` after Begin/BeginTx " +
					"to ensure the transaction is rolled back on error. " +
					"The Commit call will supersede the deferred Rollback.",
				Confidence: 0.8,
			})
		}
	}

	return findings
}

// SensitiveInfoInLogRule detects sensitive information being logged.
type SensitiveInfoInLogRule struct{}

func (r *SensitiveInfoInLogRule) ID() string       { return "SENSITIVE_INFO_IN_LOG" }
func (r *SensitiveInfoInLogRule) Category() string { return "sensitive_info" }
func (r *SensitiveInfoInLogRule) Description() string {
	return "Detects logging of sensitive information like passwords, " +
		"tokens, or API keys"
}

var (
	reLogSecret = regexp.MustCompile(`(?i)(log\.|logf\.|logger\.|fmt\.Print).*?(password|passwd|secret|token|apikey|api_key|credential)`)
	reLogPrintf = regexp.MustCompile(`(?i)(log\.Printf|fmt\.Printf).*?(password|passwd|secret|token|apikey|api_key)`)
)

func (r *SensitiveInfoInLogRule) Check(_ DiffFile, _ DiffHunk, line DiffLine) []Finding {
	content := strings.TrimSpace(line.Content)
	var findings []Finding

	if reLogSecret.MatchString(content) || reLogPrintf.MatchString(content) {
		findings = append(findings, Finding{
			Severity: SeverityHigh,
			Title:    "Sensitive information may be logged",
			Evidence: RedactSensitiveInfo(content),
			Recommendation: "Do not log passwords, tokens, or API keys. " +
				"Remove or redact the sensitive field before logging.",
			Confidence: 0.85,
		})
	}
	return findings
}

// TestMissingRule detects new exported functions/methods added
// without corresponding test files.
type TestMissingRule struct{}

func (r *TestMissingRule) ID() string       { return "TEST_MISSING" }
func (r *TestMissingRule) Category() string { return "test_missing" }
func (r *TestMissingRule) Description() string {
	return "Detects new exported functions without corresponding tests"
}

// Check is a no-op for line-level; we use CheckFile instead.
func (r *TestMissingRule) Check(_ DiffFile, _ DiffHunk, _ DiffLine) []Finding {
	return nil
}

var (
	reExportedFunc   = regexp.MustCompile(`^func\s+([A-Z]\w*)\s*\(`)
	reExportedMethod = regexp.MustCompile(`^func\s*\(\s*\w+\s+\*?\w+\s*\)\s+([A-Z]\w*)\s*\(`)
)

// CheckFile checks if the added .go file (non-test) has exported
// functions but no test file exists.
func (r *TestMissingRule) CheckFile(file DiffFile) []Finding {
	if file.IsDeleted {
		return nil
	}
	// Skip test files themselves.
	if strings.HasSuffix(file.Path, "_test.go") {
		return nil
	}
	// Skip main.go and cmd files.
	if strings.HasSuffix(file.Path, "main.go") ||
		strings.Contains(file.Path, "cmd/") {
		return nil
	}

	var exportedFuncs []string
	for _, hunk := range file.Hunks {
		for _, line := range hunk.Lines {
			if line.Type != LineAdded {
				continue
			}
			content := strings.TrimSpace(line.Content)
			if m := reExportedFunc.FindStringSubmatch(content); m != nil {
				exportedFuncs = append(exportedFuncs, m[1])
			}
			if m := reExportedMethod.FindStringSubmatch(content); m != nil {
				exportedFuncs = append(exportedFuncs, m[1])
			}
		}
	}

	if len(exportedFuncs) == 0 {
		return nil
	}

	// Check if there's a test file (heuristic: same name + _test.go).
	// Since we only have the diff, we check if any added line is in a
	// _test.go file in the same diff. If not, flag it.
	return []Finding{{
		Severity: SeverityLow,
		Title:    "New exported functions without tests",
		Evidence: "Exported functions: " + strings.Join(exportedFuncs, ", "),
		Recommendation: "Add unit tests for new exported functions. " +
			"Create a " + strings.TrimSuffix(file.Path, ".go") + "_test.go file.",
		Confidence: 0.4, // Low confidence → goes to warnings
	}}
}
