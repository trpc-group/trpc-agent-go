//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package rules

import (
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/cr_agent/internal/diff"
	"trpc.group/trpc-go/trpc-agent-go/examples/cr_agent/internal/types"
)

// --- SEC-001: SQL Injection ---
//
// Detects SQL queries built with string concatenation or fmt.Sprintf
// instead of parameterized queries.

var sqlInjectionRe = compileRegexp(
	`(?:fmt\.Sprintf\s*\(\s*["']SELECT|fmt\.Sprintf\s*\(\s*["']INSERT|fmt\.Sprintf\s*\(\s*["']UPDATE|fmt\.Sprintf\s*\(\s*["']DELETE|"SELECT\s+.*"\s*\+\s*|"INSERT\s+.*"\s*\+\s*|"UPDATE\s+.*"\s*\+\s*|"DELETE\s+.*"\s*\+\s*)`,
)

type sqlInjectionRule struct{}

func (r *sqlInjectionRule) ID() string               { return "SEC-001" }
func (r *sqlInjectionRule) Category() types.Category { return types.CategorySecurity }

func (r *sqlInjectionRule) Evaluate(fc *diff.FileChange) []types.Finding {
	var findings []types.Finding
	for _, li := range addedLines(fc) {
		if sqlInjectionRe.MatchString(li.content) {
			f := finding(r.ID(), types.SeverityCritical, r.Category(),
				fc, li.lineNum,
				"SQL query built via string concatenation or fmt.Sprintf",
				strings.TrimSpace(li.content),
				"Use parameterized queries (db.Query/Exec with ? placeholders) "+
					"or prepared statements to prevent SQL injection.",
			)
			f.Confidence = 0.9
			findings = append(findings, f)
		}
	}
	return findings
}

// --- SEC-002: Hardcoded Secret ---
//
// Detects hardcoded API keys, passwords, and tokens in source code.

var hardcodedSecretRe = compileRegexp(
	`(?i)(?:password|passwd|secret|api[_-]?key|access[_-]?key|token|private[_-]?key)\s*[:=]\s*["'][^"']{8,}["']`,
)

type hardcodedSecretRule struct{}

func (r *hardcodedSecretRule) ID() string               { return "SEC-002" }
func (r *hardcodedSecretRule) Category() types.Category { return types.CategorySecurity }

func (r *hardcodedSecretRule) Evaluate(fc *diff.FileChange) []types.Finding {
	var findings []types.Finding
	for _, li := range addedLines(fc) {
		if hardcodedSecretRe.MatchString(li.content) {
			f := finding(r.ID(), types.SeverityCritical, r.Category(),
				fc, li.lineNum,
				"Hardcoded secret or credential in source code",
				strings.TrimSpace(li.content),
				"Load secrets from environment variables, a secrets manager, "+
					"or a config file excluded from version control.",
			)
			f.Confidence = 0.85
			findings = append(findings, f)
		}
	}
	return findings
}

// --- SEC-003: Command Injection ---
//
// Detects exec.Command or exec.CommandContext called with
// user-controlled or string-concatenated arguments.

var commandInjectionRe = compileRegexp(
	`exec\.Command(?:Context)?\s*\([^)]*["'].*["']\s*\+`,
)

type commandInjectionRule struct{}

func (r *commandInjectionRule) ID() string               { return "SEC-003" }
func (r *commandInjectionRule) Category() types.Category { return types.CategorySecurity }

func (r *commandInjectionRule) Evaluate(fc *diff.FileChange) []types.Finding {
	var findings []types.Finding
	for _, li := range addedLines(fc) {
		if commandInjectionRe.MatchString(li.content) {
			f := finding(r.ID(), types.SeverityHigh, r.Category(),
				fc, li.lineNum,
				"exec.Command called with string concatenation (command injection risk)",
				strings.TrimSpace(li.content),
				"Pass arguments as separate parameters to exec.Command rather "+
					"than building a command string via concatenation.",
			)
			f.Confidence = 0.7
			findings = append(findings, f)
		}
	}
	return findings
}
