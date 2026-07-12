//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package rules applies deterministic review rules to parsed diffs.
package rules

import (
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/diff"
	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/findings"
)

// go 代码审计规则
var (
	sqlConcatRE     = regexp.MustCompile(`(?i)(SELECT|INSERT|UPDATE|DELETE).*(?:\+|fmt\.Sprintf)`)
	sqlConcatRevRE  = regexp.MustCompile(`(?i)(?:\+|fmt\.Sprintf).*(SELECT|INSERT|UPDATE|DELETE)`)
	execCommandRE   = regexp.MustCompile(`exec\.Command\([^)]*\+`)
	goFuncRE        = regexp.MustCompile(`\bgo\s+func\s*\(`)
	ctxContextRE    = regexp.MustCompile(`context\.Context`)
	openRE          = regexp.MustCompile(`\b(?:os|sql)\.Open\(`)
	deferCloseRE    = regexp.MustCompile(`defer\s+\w+\.Close\(`)
	ignoreErrRE     = regexp.MustCompile(`_\s*=\s*\w*err`)
	emptyErrIfRE    = regexp.MustCompile(`if\s+err\s*!=\s*nil\s*\{\s*\}`)
	exportedFuncRE  = regexp.MustCompile(`^func\s+([A-Z]\w*)\(`)
	sensitiveRE     = regexp.MustCompile(`(?i)(password|api[_-]?key|token|secret)\s*[:=]`)
	sensitiveLogRE  = regexp.MustCompile(`(?i)(log\.|fmt\.Print).*(password|token|api[_-]?key|secret)`)
	beginTxRE       = regexp.MustCompile(`\.Begin\(`)
	commitRollbackRE = regexp.MustCompile(`\.(Commit|Rollback)\(`)
)

// Analyze runs deterministic rules against a parsed diff.
func Analyze(d *diff.Diff) []findings.Finding {
	if d == nil {
		return nil
	}
	var out []findings.Finding
	changed := d.ChangedFiles()
	for _, hunk := range d.AllHunks() {
		block := strings.Join(hunk.AllLines, "\n")
		for _, line := range hunk.AddedLines {
			content := line.Content
			if f, ok := matchSecuritySQL(hunk.File, line.Line, content); ok {
				out = append(out, f)
			}
			if f, ok := matchExecCommand(hunk.File, line.Line, content); ok {
				out = append(out, f)
			}
			if f, ok := matchGoroutineLeak(hunk.File, line.Line, content, block); ok {
				out = append(out, f)
			}
			if f, ok := matchContextPropagation(hunk.File, line.Line, content, block); ok {
				out = append(out, f)
			}
			if f, ok := matchResourceLeak(hunk.File, line.Line, content, block); ok {
				out = append(out, f)
			}
			if f, ok := matchErrorHandling(hunk.File, line.Line, content); ok {
				out = append(out, f)
			}
			if f, ok := matchSensitiveData(hunk.File, line.Line, content); ok {
				out = append(out, f)
			}
			if f, ok := matchDBLifecycle(hunk.File, line.Line, content, block); ok {
				out = append(out, f)
			}
			if f, ok := matchMissingTest(hunk.File, line.Line, content, changed); ok {
				out = append(out, f)
			}
		}
	}
	return out
}

func matchSecuritySQL(file string, line int, content string) (findings.Finding, bool) {
	if !sqlConcatRE.MatchString(content) && !sqlConcatRevRE.MatchString(content) {
		return findings.Finding{}, false
	}
	return findings.Finding{
		Severity:       "high",
		Category:       "security",
		File:           file,
		Line:           line,
		Title:          "Potential SQL injection via string concatenation",
		Evidence:       content,
		Recommendation: "Use parameterized queries or prepared statements instead of concatenating SQL strings.",
		Confidence:     0.9,
		Source:         "rule",
		RuleID:         "SEC-001",
	}, true
}

func matchExecCommand(file string, line int, content string) (findings.Finding, bool) {
	if !execCommandRE.MatchString(content) {
		return findings.Finding{}, false
	}
	return findings.Finding{
		Severity:       "high",
		Category:       "security",
		File:           file,
		Line:           line,
		Title:          "Command execution with variable concatenation",
		Evidence:       content,
		Recommendation: "Avoid building shell commands from user input; validate and sanitize arguments.",
		Confidence:     0.85,
		Source:         "rule",
		RuleID:         "SEC-002",
	}, true
}

func matchGoroutineLeak(file string, line int, content, block string) (findings.Finding, bool) {
	if !goFuncRE.MatchString(content) {
		return findings.Finding{}, false
	}
	if strings.Contains(block, "ctx.Done()") || strings.Contains(block, "cancel()") ||
		strings.Contains(block, "context.WithCancel") {
		return findings.Finding{}, false
	}
	return findings.Finding{
		Severity:       "high",
		Category:       "concurrency",
		File:           file,
		Line:           line,
		Title:          "Goroutine may leak without cancellation",
		Evidence:       content,
		Recommendation: "Pass context with cancel and ensure goroutines exit when ctx.Done() fires.",
		Confidence:     0.8,
		Source:         "rule",
		RuleID:         "CONC-001",
	}, true
}

func matchContextPropagation(file string, line int, content, block string) (findings.Finding, bool) {
	if !goFuncRE.MatchString(content) || !ctxContextRE.MatchString(block) {
		return findings.Finding{}, false
	}
	if strings.Contains(content, "ctx") {
		return findings.Finding{}, false
	}
	return findings.Finding{
		Severity:       "medium",
		Category:       "concurrency",
		File:           file,
		Line:           line,
		Title:          "Context not propagated into goroutine",
		Evidence:       content,
		Recommendation: "Capture and pass context.Context into goroutines for cancellation and deadlines.",
		Confidence:     0.75,
		Source:         "rule",
		RuleID:         "CONC-002",
	}, true
}

func matchResourceLeak(file string, line int, content, block string) (findings.Finding, bool) {
	if !openRE.MatchString(content) || deferCloseRE.MatchString(block) {
		return findings.Finding{}, false
	}
	return findings.Finding{
		Severity:       "high",
		Category:       "resource",
		File:           file,
		Line:           line,
		Title:          "Resource opened without deferred close",
		Evidence:       content,
		Recommendation: "Use defer to close files, connections, or rows promptly after open.",
		Confidence:     0.85,
		Source:         "rule",
		RuleID:         "RES-001",
	}, true
}

func matchErrorHandling(file string, line int, content string) (findings.Finding, bool) {
	if !ignoreErrRE.MatchString(content) && !emptyErrIfRE.MatchString(content) {
		return findings.Finding{}, false
	}
	return findings.Finding{
		Severity:       "medium",
		Category:       "error_handling",
		File:           file,
		Line:           line,
		Title:          "Error ignored or not handled",
		Evidence:       content,
		Recommendation: "Handle or return errors explicitly; avoid blank error branches.",
		Confidence:     0.9,
		Source:         "rule",
		RuleID:         "ERR-001",
	}, true
}

func matchSensitiveData(file string, line int, content string) (findings.Finding, bool) {
	if !sensitiveRE.MatchString(content) && !sensitiveLogRE.MatchString(content) {
		return findings.Finding{}, false
	}
	return findings.Finding{
		Severity:       "critical",
		Category:       "sensitive_data",
		File:           file,
		Line:           line,
		Title:          "Sensitive credential or secret detected",
		Evidence:       content,
		Recommendation: "Load secrets from environment or a secret manager; never hardcode credentials.",
		Confidence:     0.95,
		Source:         "rule",
		RuleID:         "SENS-001",
	}, true
}

func matchDBLifecycle(file string, line int, content, block string) (findings.Finding, bool) {
	if !beginTxRE.MatchString(content) || commitRollbackRE.MatchString(block) {
		return findings.Finding{}, false
	}
	return findings.Finding{
		Severity:       "high",
		Category:       "resource",
		File:           file,
		Line:           line,
		Title:          "Database transaction without commit or rollback",
		Evidence:       content,
		Recommendation: "Always commit or rollback transactions in a defer/finally block.",
		Confidence:     0.85,
		Source:         "rule",
		RuleID:         "DB-001",
	}, true
}

func matchMissingTest(file string, line int, content string, changed []string) (findings.Finding, bool) {
	if strings.HasSuffix(file, "_test.go") {
		return findings.Finding{}, false
	}
	m := exportedFuncRE.FindStringSubmatch(strings.TrimSpace(content))
	if m == nil {
		return findings.Finding{}, false
	}
	testFile := strings.TrimSuffix(file, ".go") + "_test.go"
	for _, f := range changed {
		if f == testFile {
			return findings.Finding{}, false
		}
	}
	return findings.Finding{
		Severity:       "low",
		Category:       "testing",
		File:           file,
		Line:           line,
		Title:          "Exported function added without corresponding test changes",
		Evidence:       content,
		Recommendation: "Add unit tests covering the new exported function behavior.",
		Confidence:     0.7,
		Source:         "rule",
		RuleID:         "TEST-001",
	}, true
}
