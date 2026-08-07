//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package rules

import (
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

const source = "deterministic_rules"

// Evaluate applies deterministic review rules to parsed diff files.
func Evaluate(files []review.DiffFile) []review.Finding {
	var findings []review.Finding
	for _, file := range files {
		for _, hunk := range file.Hunks {
			added := addedLines(hunk)
			for _, line := range added {
				findings = append(findings, evaluateLine(file, line, added)...)
			}
		}
	}
	findings = append(findings, missingTestFindings(files)...)
	return review.NormalizeFindings(findings, review.DefaultConfig())
}

func evaluateLine(file review.DiffFile, line review.DiffLine, hunkAdded []review.DiffLine) []review.Finding {
	var findings []review.Finding
	content := strings.TrimSpace(line.Content)
	redacted := redact.Text(line.Content).Text
	if redact.ContainsSecret(line.Content) {
		findings = append(findings, review.Finding{
			Severity:       review.SeverityCritical,
			Category:       "security",
			File:           file.NewPath,
			Line:           line.NewLine,
			Title:          "Potential secret committed in diff",
			Evidence:       redacted,
			Recommendation: "Move secrets to a managed secret store and rotate the exposed credential.",
			Confidence:     0.98,
			Source:         source,
			RuleID:         "security.secret_leak",
		})
		findings = append(findings, review.Finding{
			Severity:       review.SeverityLow,
			Category:       "security",
			File:           file.NewPath,
			Line:           line.NewLine,
			Title:          "Secret-like value was redacted",
			Evidence:       redacted,
			Recommendation: "Verify all persisted reports and audit records contain only redacted values.",
			Confidence:     0.99,
			Source:         source,
			RuleID:         "security.redaction_required",
		})
	}
	if strings.Contains(content, "go func") && !hunkContains(hunkAdded, "context.") && !hunkContains(hunkAdded, "ctx") {
		findings = append(findings, review.Finding{
			Severity:       review.SeverityHigh,
			Category:       "concurrency",
			File:           file.NewPath,
			Line:           line.NewLine,
			Title:          "Goroutine lacks visible context cancellation",
			Evidence:       line.Content,
			Recommendation: "Thread context into the goroutine and exit on cancellation.",
			Confidence:     0.78,
			Source:         source,
			RuleID:         "concurrency.goroutine_context_leak",
		})
	}
	if opensResource(content) && !hunkContains(hunkAdded, ".Close()") {
		findings = append(findings, review.Finding{
			Severity:       review.SeverityHigh,
			Category:       "resource",
			File:           file.NewPath,
			Line:           line.NewLine,
			Title:          "Opened resource is not closed nearby",
			Evidence:       line.Content,
			Recommendation: "Defer Close after checking the open/query error.",
			Confidence:     0.87,
			Source:         source,
			RuleID:         "resource.close_missing",
		})
	}
	if ignoresError(content) {
		findings = append(findings, review.Finding{
			Severity:       review.SeverityMedium,
			Category:       "error",
			File:           file.NewPath,
			Line:           line.NewLine,
			Title:          "Error value is ignored",
			Evidence:       line.Content,
			Recommendation: "Handle, return, or explicitly document why the error is safe to ignore.",
			Confidence:     0.9,
			Source:         source,
			RuleID:         "error.ignored_error",
		})
	}
	if beginsTransaction(content) && !hunkContains(hunkAdded, "Commit()") && !hunkContains(hunkAdded, "Rollback()") {
		findings = append(findings, review.Finding{
			Severity:       review.SeverityHigh,
			Category:       "database",
			File:           file.NewPath,
			Line:           line.NewLine,
			Title:          "Transaction lacks commit or rollback handling",
			Evidence:       line.Content,
			Recommendation: "Ensure every successful transaction commits and every failed path rolls back.",
			Confidence:     0.88,
			Source:         source,
			RuleID:         "db.lifecycle",
		})
	}
	return findings
}

func addedLines(hunk review.DiffHunk) []review.DiffLine {
	var out []review.DiffLine
	for _, line := range hunk.Lines {
		if line.Kind == "add" {
			out = append(out, line)
		}
	}
	return out
}

func hunkContains(lines []review.DiffLine, needle string) bool {
	for _, line := range lines {
		if strings.Contains(line.Content, needle) {
			return true
		}
	}
	return false
}

func opensResource(content string) bool {
	return strings.Contains(content, "os.Open(") ||
		strings.Contains(content, "http.Get(") ||
		strings.Contains(content, ".Query(") ||
		strings.Contains(content, ".QueryContext(")
}

func ignoresError(content string) bool {
	return strings.Contains(content, "_ = err") ||
		strings.Contains(content, "_, _ :=") ||
		strings.Contains(content, "_, _ =")
}

func beginsTransaction(content string) bool {
	return strings.Contains(content, ".Begin()") ||
		strings.Contains(content, ".BeginTx(")
}

func missingTestFindings(files []review.DiffFile) []review.Finding {
	changedTests := make(map[string]bool)
	for _, file := range files {
		if strings.HasSuffix(file.NewPath, "_test.go") {
			changedTests[file.PackageDir] = true
		}
	}
	var findings []review.Finding
	for _, file := range files {
		if !file.IsNew || file.NewPath == "" ||
			!strings.HasSuffix(file.NewPath, ".go") ||
			strings.HasSuffix(file.NewPath, "_test.go") ||
			changedTests[file.PackageDir] {
			continue
		}
		findings = append(findings, review.Finding{
			Severity:       review.SeverityMedium,
			Category:       "test",
			File:           file.NewPath,
			Line:           firstAddedLine(file),
			Title:          "New Go file has no related test change",
			Evidence:       filepath.Base(file.NewPath),
			Recommendation: "Add or update tests covering the new behavior.",
			Confidence:     0.72,
			Source:         source,
			RuleID:         "test.missing_coverage",
		})
	}
	return findings
}

func firstAddedLine(file review.DiffFile) int {
	for _, hunk := range file.Hunks {
		for _, line := range hunk.Lines {
			if line.Kind == "add" {
				return line.NewLine
			}
		}
	}
	return 1
}
