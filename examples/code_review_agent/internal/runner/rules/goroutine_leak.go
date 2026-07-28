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

// GoroutineLeakRule detects goroutines launched without context cancellation handling.
type GoroutineLeakRule struct {
	runner.RuleBase
}

// NewGoroutineLeakRule creates a new goroutine leak rule.
func NewGoroutineLeakRule() *GoroutineLeakRule {
	return &GoroutineLeakRule{
		RuleBase: runner.RuleBase{
			IDValue:       "GO_GOROUTINE_NO_CANCEL",
			CategoryValue: finding.CategoryGoroutineLeak,
			DefaultSev:    finding.SeverityHigh,
		},
	}
}

// Check examines file content for goroutine/context leak patterns.
func (r *GoroutineLeakRule) Check(ctx context.Context, file finding.ChangedFileInfo, content string) ([]finding.Finding, error) {
	if !strings.HasSuffix(file.File, ".go") {
		return nil, nil
	}

	var findings []finding.Finding
	lines := strings.Split(content, "\n")

	for i, line := range lines {
		lineNum := i + 1
		trimmed := strings.TrimSpace(line)

		// Detect: go func() { ... }() without ctx.Done/select
		if strings.HasPrefix(trimmed, "go func()") || strings.HasPrefix(trimmed, "go func(") {
			// Check if this goroutine is in a select with ctx.Done().
			if !hasContextGuard(lines, i) {
				findings = append(findings, runner.NewFinding(
					&r.RuleBase, file.File, lineNum,
					"Goroutine launched without context cancellation handling",
					trimmed,
					"Add select with <-ctx.Done() to allow goroutine cleanup on cancellation",
					finding.ConfidenceMedium,
				))
			}
		}

		// Detect: context.WithCancel / WithTimeout / WithDeadline without calling cancel.
		if isContextWithFunc(trimmed) {
			if !hasDeferCancel(lines, i) {
				findings = append(findings, runner.NewFinding(
					&r.RuleBase, file.File, lineNum,
					"context.WithCancel/WithTimeout cancel function not deferred",
					trimmed,
					"Add 'defer cancel()' immediately after creating the cancellable context",
					finding.ConfidenceHigh,
				))
			}
		}
	}
	return findings, nil
}

// hasContextGuard checks if the code around the goroutine has context.Done handling.
func hasContextGuard(lines []string, startIdx int) bool {
	// Look ahead up to 10 lines for a select with ctx.Done().
	end := startIdx + 10
	if end > len(lines) {
		end = len(lines)
	}
	for i := startIdx; i < end; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.Contains(trimmed, "<-ctx.Done()") || strings.Contains(trimmed, "<-ctx.Done(") {
			return true
		}
		if strings.Contains(trimmed, "time.After") && strings.Contains(trimmed, "select") {
			// Might have timeout protection, but not context cancellation.
			continue
		}
	}
	return false
}

// hasDeferCancel checks if there's a defer cancel() call after a context.WithCancel/WithTimeout.
func hasDeferCancel(lines []string, startIdx int) bool {
	end := startIdx + 5
	if end > len(lines) {
		end = len(lines)
	}
	for i := startIdx; i < end; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "defer ") && strings.Contains(trimmed, "cancel(") {
			return true
		}
	}
	return false
}

func isContextWithFunc(line string) bool {
	return strings.Contains(line, "context.WithCancel(") ||
		strings.Contains(line, "context.WithTimeout(") ||
		strings.Contains(line, "context.WithDeadline(") ||
		strings.Contains(line, "context.WithCancelCause(")
}
