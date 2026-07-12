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

// GoroutineLeakRule detects `go func()` calls without a context,
// which may lead to goroutine leaks.
type GoroutineLeakRule struct{}

func (r *GoroutineLeakRule) ID() string       { return "GOROUTINE_LEAK" }
func (r *GoroutineLeakRule) Category() string { return "goroutine_leak" }
func (r *GoroutineLeakRule) Description() string {
	return "Detects goroutines started without context cancellation, " +
		"which can leak if the parent is cancelled"
}

var (
	reGoFunc      = regexp.MustCompile(`go\s+func\s*\(`)
	reGoFuncNoCtx = regexp.MustCompile(`go\s+func\s*\(\s*\)`)
	reGoCall      = regexp.MustCompile(`go\s+\w+\(`)
)

func (r *GoroutineLeakRule) Check(_ DiffFile, _ DiffHunk, line DiffLine) []Finding {
	content := strings.TrimSpace(line.Content)
	var findings []Finding

	// `go func()` with no parameters — likely no context passed.
	if reGoFuncNoCtx.MatchString(content) {
		findings = append(findings, Finding{
			Severity: SeverityHigh,
			Title:    "Goroutine started without context (possible leak)",
			Evidence: content,
			Recommendation: "Pass a context.Context to the goroutine and check " +
				"ctx.Done() to allow cancellation, preventing goroutine leaks.",
			Confidence: 0.8,
		})
	}

	// `go someFunc()` without context — check if the called function
	// accepts context. This is heuristic (confidence 0.6).
	if reGoCall.MatchString(content) && !strings.Contains(content, "ctx") &&
		!strings.Contains(content, "context") {
		findings = append(findings, Finding{
			Severity: SeverityMedium,
			Title:    "Goroutine call without context parameter",
			Evidence: content,
			Recommendation: "Ensure the goroutine function accepts a context " +
				"to allow cancellation and prevent leaks.",
			Confidence: 0.6,
		})
	}

	return findings
}

// ContextNotPassedRule detects function calls that should receive a
// context but receive context.Background() or context.TODO() in a
// request handler.
type ContextNotPassedRule struct{}

func (r *ContextNotPassedRule) ID() string       { return "CONTEXT_NOT_PASSED" }
func (r *ContextNotPassedRule) Category() string { return "goroutine_leak" }
func (r *ContextNotPassedRule) Description() string {
	return "Detects context.Background() or context.TODO() in request " +
		"handler code instead of passing the parent context"
}

var (
	reCtxBackground = regexp.MustCompile(`context\.Background\(\)`)
	reCtxTODO       = regexp.MustCompile(`context\.TODO\(\)`)
)

func (r *ContextNotPassedRule) Check(_ DiffFile, _ DiffHunk, line DiffLine) []Finding {
	content := strings.TrimSpace(line.Content)
	var findings []Finding

	// Only flag if this looks like it's inside a handler (not in main/init).
	if reCtxBackground.MatchString(content) && !strings.Contains(content, "func main") {
		findings = append(findings, Finding{
			Severity: SeverityMedium,
			Title:    "context.Background() used instead of parent context",
			Evidence: content,
			Recommendation: "In request handlers, pass the parent context " +
				"instead of context.Background() to allow cancellation.",
			Confidence: 0.7,
		})
	}
	if reCtxTODO.MatchString(content) {
		findings = append(findings, Finding{
			Severity: SeverityLow,
			Title:    "context.TODO() used (should be replaced)",
			Evidence: content,
			Recommendation: "Replace context.TODO() with the actual parent " +
				"context before production use.",
			Confidence: 0.5,
		})
	}
	return findings
}
