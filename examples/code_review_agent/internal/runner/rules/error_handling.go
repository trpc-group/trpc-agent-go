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

// ErrorHandlingRule detects error handling issues: silently ignored errors,
// missing error returns, and improper error handling patterns.
type ErrorHandlingRule struct {
	runner.RuleBase
}

// NewErrorHandlingRule creates a new error handling rule.
func NewErrorHandlingRule() *ErrorHandlingRule {
	return &ErrorHandlingRule{
		RuleBase: runner.RuleBase{
			IDValue:       "GO_ERROR_SILENT_IGNORE",
			CategoryValue: finding.CategoryErrorHandling,
			DefaultSev:    finding.SeverityMedium,
		},
	}
}

// Check examines file content for error handling issues.
func (r *ErrorHandlingRule) Check(ctx context.Context, file finding.ChangedFileInfo, content string) ([]finding.Finding, error) {
	if !strings.HasSuffix(file.File, ".go") {
		return nil, nil
	}

	var findings []finding.Finding
	lines := strings.Split(content, "\n")

	for i, line := range lines {
		lineNum := i + 1
		trimmed := strings.TrimSpace(line)

		// Pattern 1: `_ = fn()` - silent error ignore.
		if strings.HasPrefix(trimmed, "_ = ") {
			findings = append(findings, runner.NewFinding(
				&r.RuleBase, file.File, lineNum,
				"Error return value silently ignored with `_ =`",
				trimmed,
				"Check the error explicitly instead of discarding it with `_ =`",
				finding.ConfidenceHigh,
			))
		}

		// Pattern 2: `if err != nil` followed by no return/panic/log within 3 lines.
		if strings.HasPrefix(trimmed, "if err != nil") || strings.HasPrefix(trimmed, "if err != nil {") {
			if !hasErrorHandlingAfter(lines, i) {
				findings = append(findings, runner.NewFinding(
					&r.RuleBase, file.File, lineNum,
					"Error check without handling: `if err != nil` does not return/panic/log",
					trimmed,
					"Add return, panic, or log statement inside the error check block",
					finding.ConfidenceLow,
				))
			}
		}

		// Pattern 3: `recover()` not in a deferred function.
		if strings.Contains(trimmed, "recover()") && !strings.HasPrefix(trimmed, "defer ") {
			// Check if this is inside a deferred call.
			if !isInsideDefer(trimmed) {
				findings = append(findings, runner.NewFinding(
					&r.RuleBase, file.File, lineNum,
					"recover() must be called directly inside a deferred function",
					trimmed,
					"recover() only works when called directly from a deferred function; wrap in 'defer func() { recover() }()'",
					finding.ConfidenceHigh,
				))
			}
		}
	}

	return findings, nil
}

// hasErrorHandlingAfter checks if the lines after an `if err != nil` contain
// return, panic, or log statements.
func hasErrorHandlingAfter(lines []string, startIdx int) bool {
	end := startIdx + 4
	if end > len(lines) {
		end = len(lines)
	}
	// Skip the `if err != nil {` line itself.
	for i := startIdx + 1; i < end; i++ {
		trimmed := strings.TrimSpace(lines[i])
		// End of block or empty line.
		if trimmed == "}" || trimmed == "" {
			return false
		}
		// Has some error handling.
		if strings.HasPrefix(trimmed, "return ") || strings.HasPrefix(trimmed, "return") ||
			strings.HasPrefix(trimmed, "panic(") || strings.HasPrefix(trimmed, "log.") ||
			strings.HasPrefix(trimmed, "fmt.Fprintf") || strings.HasPrefix(trimmed, "fmt.Println") ||
			strings.HasPrefix(trimmed, "os.Exit(") || strings.HasPrefix(trimmed, "continue") ||
			strings.HasPrefix(trimmed, "break") || strings.HasPrefix(trimmed, "goto ") {
			return true
		}
	}
	return false
}

func isInsideDefer(line string) bool {
	return strings.Contains(line, "defer ") && strings.Contains(line, "recover()")
}

// ErrorNoReturnRule detects missing error return patterns more broadly.
type ErrorNoReturnRule struct {
	runner.RuleBase
}

// NewErrorNoReturnRule creates a new error-no-return rule.
func NewErrorNoReturnRule() *ErrorNoReturnRule {
	return &ErrorNoReturnRule{
		RuleBase: runner.RuleBase{
			IDValue:       "GO_ERROR_NO_RETURN",
			CategoryValue: finding.CategoryErrorHandling,
			DefaultSev:    finding.SeverityMedium,
		},
	}
}

// Check examines file content for missing error returns.
func (r *ErrorNoReturnRule) Check(ctx context.Context, file finding.ChangedFileInfo, content string) ([]finding.Finding, error) {
	if !strings.HasSuffix(file.File, ".go") {
		return nil, nil
	}

	var findings []finding.Finding
	lines := strings.Split(content, "\n")

	for i, line := range lines {
		lineNum := i + 1
		trimmed := strings.TrimSpace(line)

		// Detect functions that return error but ignore an error in the body.
		if strings.HasPrefix(trimmed, "if err := ") || strings.HasPrefix(trimmed, "if err = ") {
			if !strings.Contains(trimmed, "!= nil") {
				continue
			}
			if !hasErrorHandlingAfter(lines, i) {
				findings = append(findings, runner.NewFinding(
					&r.RuleBase, file.File, lineNum,
					"Error not handled in if err assignment",
					trimmed,
					"Handle the error with a return or log statement",
					finding.ConfidenceLow,
				))
			}
		}
	}

	return findings, nil
}
