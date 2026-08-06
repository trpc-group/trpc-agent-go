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

// --- GOR-001: Goroutine with no exit condition ---
//
// Detects `go func() { ... }()` calls that do not reference a
// context.Done() or done channel, indicating the goroutine may run
// indefinitely without cancellation.

type goroutineNoExitRule struct{}

func (r *goroutineNoExitRule) ID() string { return "GOR-001" }
func (r *goroutineNoExitRule) Category() types.Category {
	return types.CategoryGoroutineLeak
}

func (r *goroutineNoExitRule) Evaluate(fc *diff.FileChange) []types.Finding {
	var findings []types.Finding
	lines := addedLines(fc)
	for i, li := range lines {
		if !strings.Contains(li.content, "go func()") &&
			!strings.Contains(li.content, "go func(") {
			continue
		}
		// Check the next few lines for ctx.Done() or <-done.
		hasExit := false
		end := i + 10
		if end > len(lines) {
			end = len(lines)
		}
		for j := i; j < end; j++ {
			c := lines[j].content
			if strings.Contains(c, "ctx.Done()") ||
				strings.Contains(c, "<-done") ||
				strings.Contains(c, "<-stop") {
				hasExit = true
				break
			}
		}
		if !hasExit {
			f := finding(r.ID(), types.SeverityHigh, r.Category(),
				fc, li.lineNum,
				"Goroutine started without a visible exit condition",
				strings.TrimSpace(li.content),
				"Pass a context.Context to the goroutine and select on "+
					"ctx.Done() to ensure it can be cancelled.",
			)
			f.Confidence = 0.65
			findings = append(findings, f)
		}
	}
	return findings
}

// --- GOR-002: Uncanceled context ---
//
// Detects context.Background() or context.TODO() used directly in a
// function that starts long-running work (e.g. HTTP handler, goroutine).

var uncanceledContextRe = compileRegexp(
	`(?:context\.Background|context\.TODO)\(\)`,
)

type uncanceledContextRule struct{}

func (r *uncanceledContextRule) ID() string { return "GOR-002" }
func (r *uncanceledContextRule) Category() types.Category {
	return types.CategoryGoroutineLeak
}

func (r *uncanceledContextRule) Evaluate(fc *diff.FileChange) []types.Finding {
	var findings []types.Finding
	lines := allLines(fc)
	for _, li := range lines {
		if !uncanceledContextRe.MatchString(li.content) {
			continue
		}
		// Only flag if the line also references go, http, or time.
		if strings.Contains(li.content, "go ") ||
			strings.Contains(li.content, "http.") ||
			strings.Contains(li.content, "time.") {
			f := finding(r.ID(), types.SeverityMedium, r.Category(),
				fc, li.lineNum,
				"context.Background()/TODO() used in long-running path",
				strings.TrimSpace(li.content),
				"Use a cancellable context (context.WithCancel/WithTimeout) "+
					"so the operation can be aborted on shutdown.",
			)
			f.Confidence = 0.55
			findings = append(findings, f)
		}
	}
	return findings
}

// --- GOR-003: Blocked send ---
//
// Detects `ch <- value` without a select statement on the same or
// next line, which can block forever if the receiver stops reading.

type blockedSendRule struct{}

func (r *blockedSendRule) ID() string { return "GOR-003" }
func (r *blockedSendRule) Category() types.Category {
	return types.CategoryGoroutineLeak
}

func (r *blockedSendRule) Evaluate(fc *diff.FileChange) []types.Finding {
	var findings []types.Finding
	lines := addedLines(fc)
	for i, li := range lines {
		// Look for a bare channel send (not in a select).
		trimmed := strings.TrimSpace(li.content)
		if !strings.Contains(trimmed, "<-") ||
			strings.HasPrefix(trimmed, "<-") ||
			strings.HasPrefix(trimmed, "//") ||
			strings.Contains(trimmed, "select") ||
			strings.Contains(trimmed, "case ") {
			continue
		}
		// Check that it's actually a send (channel <- value), not a
		// receive (<-ch).
		idx := strings.Index(trimmed, " <-")
		if idx < 0 {
			idx = strings.Index(trimmed, "<- ")
		}
		if idx < 0 {
			continue
		}
		// Check previous and next line for select.
		hasSelect := false
		for j := max(0, i-1); j <= min(len(lines)-1, i+1); j++ {
			if strings.Contains(lines[j].content, "select {") ||
				strings.Contains(lines[j].content, "select{") {
				hasSelect = true
				break
			}
		}
		if !hasSelect {
			f := finding(r.ID(), types.SeverityMedium, r.Category(),
				fc, li.lineNum,
				"Channel send without select/default may block indefinitely",
				strings.TrimSpace(li.content),
				"Use a select with a default or ctx.Done() case to avoid "+
					"blocking forever if the receiver stops reading.",
			)
			f.Confidence = 0.5
			findings = append(findings, f)
		}
	}
	return findings
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
