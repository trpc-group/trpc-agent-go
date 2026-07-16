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

// SQLInjectionRule detects SQL query construction via string
// concatenation, which is vulnerable to SQL injection.
type SQLInjectionRule struct{}

func (r *SQLInjectionRule) ID() string       { return "SQL_INJECTION" }
func (r *SQLInjectionRule) Category() string { return "security" }
func (r *SQLInjectionRule) Description() string {
	return "Detects SQL queries built with string concatenation or " +
		"fmt.Sprintf instead of parameterized queries"
}

var (
	reSQLConcat     = regexp.MustCompile(`(?i)(select|insert|update|delete)\s+.*\+\s*\w+`)
	reSQLSprintf    = regexp.MustCompile(`(?i)(select|insert|update|delete)\s+.*%s`)
	reSQLFmtSprintf = regexp.MustCompile(`fmt\.Sprintf\s*\(\s*["'].*(?:SELECT|INSERT|UPDATE|DELETE)`)
)

func (r *SQLInjectionRule) Check(_ DiffFile, _ DiffHunk, line DiffLine) []Finding {
	content := strings.TrimSpace(line.Content)
	var findings []Finding

	if reSQLConcat.MatchString(content) {
		findings = append(findings, Finding{
			Severity: SeverityCritical,
			Title:    "SQL query built with string concatenation",
			Evidence: content,
			Recommendation: "Use parameterized queries with placeholders (?, $1) " +
				"instead of string concatenation to prevent SQL injection.",
			Confidence: 0.9,
		})
	}
	if reSQLSprintf.MatchString(content) || reSQLFmtSprintf.MatchString(content) {
		findings = append(findings, Finding{
			Severity: SeverityCritical,
			Title:    "SQL query built with fmt.Sprintf",
			Evidence: content,
			Recommendation: "Use parameterized queries with placeholders (?, $1) " +
				"instead of fmt.Sprintf to prevent SQL injection.",
			Confidence: 0.85,
		})
	}
	return findings
}

// CommandInjectionRule detects shell command construction via string
// concatenation with exec.Command or os/exec.
type CommandInjectionRule struct{}

func (r *CommandInjectionRule) ID() string       { return "CMD_INJECTION" }
func (r *CommandInjectionRule) Category() string { return "security" }
func (r *CommandInjectionRule) Description() string {
	return "Detects shell commands built with string concatenation"
}

var (
	reExecConcat  = regexp.MustCompile(`exec\.Command\s*\(\s*["']sh["']\s*,\s*["']-c["']\s*,\s*.*\+`)
	reExecSprintf = regexp.MustCompile(`exec\.CommandContext\s*\(\s*ctx\s*,\s*["']sh["']\s*,\s*["']-c["']\s*,\s*fmt\.Sprintf`)
)

func (r *CommandInjectionRule) Check(_ DiffFile, _ DiffHunk, line DiffLine) []Finding {
	content := strings.TrimSpace(line.Content)
	var findings []Finding

	if reExecConcat.MatchString(content) {
		findings = append(findings, Finding{
			Severity: SeverityCritical,
			Title:    "Shell command built with string concatenation",
			Evidence: content,
			Recommendation: "Pass arguments as separate elements to exec.Command " +
				"instead of building a shell string to prevent command injection.",
			Confidence: 0.85,
		})
	}
	if reExecSprintf.MatchString(content) {
		findings = append(findings, Finding{
			Severity: SeverityHigh,
			Title:    "Shell command built with fmt.Sprintf",
			Evidence: content,
			Recommendation: "Avoid fmt.Sprintf for shell commands; pass each " +
				"argument separately to exec.Command.",
			Confidence: 0.8,
		})
	}
	return findings
}

// HardcodedSecretRule detects hardcoded API keys, tokens, and
// passwords in source code.
type HardcodedSecretRule struct{}

func (r *HardcodedSecretRule) ID() string       { return "HARDCODED_SECRET" }
func (r *HardcodedSecretRule) Category() string { return "security" }
func (r *HardcodedSecretRule) Description() string {
	return "Detects hardcoded API keys, tokens, and passwords"
}

var (
	reHardcodedAPIKey = regexp.MustCompile(`(?i)(api[_-]?key|apikey)\s*(?::\s*string)?\s*(?::=|=)\s*"[\w\-]{16,}"`)
	reHardcodedToken  = regexp.MustCompile(`(?i)(token|access_token|secret)\s*(?::\s*string)?\s*(?::=|=)\s*"[\w\-\.]{20,}"`)
	reHardcodedPwd    = regexp.MustCompile(`(?i)(password|passwd|pwd)\s*(?::\s*string)?\s*(?::=|=)\s*"[^"]{4,}"`)
)

func (r *HardcodedSecretRule) Check(_ DiffFile, _ DiffHunk, line DiffLine) []Finding {
	content := strings.TrimSpace(line.Content)
	var findings []Finding

	if reHardcodedAPIKey.MatchString(content) {
		findings = append(findings, Finding{
			Severity: SeverityCritical,
			Title:    "Hardcoded API key detected",
			Evidence: content,
			Recommendation: "Never hardcode API keys in source code. Use " +
				"environment variables or a secrets manager.",
			Confidence: 0.95,
		})
	}
	if reHardcodedToken.MatchString(content) {
		findings = append(findings, Finding{
			Severity: SeverityCritical,
			Title:    "Hardcoded token or secret detected",
			Evidence: content,
			Recommendation: "Never hardcode tokens or secrets in source code. " +
				"Use environment variables or a secrets manager.",
			Confidence: 0.95,
		})
	}
	if reHardcodedPwd.MatchString(content) {
		findings = append(findings, Finding{
			Severity: SeverityHigh,
			Title:    "Hardcoded password detected",
			Evidence: content,
			Recommendation: "Never hardcode passwords in source code. Use " +
				"environment variables or a secrets manager.",
			Confidence: 0.9,
		})
	}
	return findings
}
