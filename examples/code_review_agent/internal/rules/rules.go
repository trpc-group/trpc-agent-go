//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package rules 在 review.ParsedDiff 上执行确定性代码审查规则。
package rules

import (
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

// Analysis 是确定性规则执行结果。
type Analysis struct {
	Findings []review.Finding
	Warnings []review.Finding
}

// Options 配置确定性规则执行。
type Options struct {
	Redact func(string) string
}

// Run 执行确定性审查规则。
func Run(diff review.ParsedDiff, opts Options) Analysis {
	redact := opts.Redact
	if redact == nil {
		redact = func(s string) string { return s }
	}

	var out Analysis
	for _, file := range diff.Files {
		for _, hunk := range file.Hunks {
			hunkText := hunkJoinedText(hunk)
			for lineIndex, line := range hunk.Lines {
				if line.Kind != "add" {
					continue
				}
				text := strings.TrimSpace(line.Text)
				hunkBefore := hunkTextBefore(hunk, lineIndex)
				if file.Path == "" {
					continue
				}
				if strings.Contains(text, "TODO(") || strings.Contains(text, "FIXME") {
					out.Findings = append(out.Findings, review.Finding{
						Severity:       "medium",
						Category:       "maintainability",
						File:           file.Path,
						Line:           line.NewLine,
						Title:          "New code contains a TODO or FIXME marker",
						Evidence:       redact(text),
						Recommendation: "Remove the marker or turn it into a tracked issue before merging.",
						Confidence:     "high",
						Source:         "rule",
						RuleID:         "todo-marker",
						Status:         "finding",
					})
				}
				if strings.Contains(text, "panic(") {
					out.Findings = append(out.Findings, review.Finding{
						Severity:       "high",
						Category:       "error_handling",
						File:           file.Path,
						Line:           line.NewLine,
						Title:          "New function panics directly",
						Evidence:       redact(text),
						Recommendation: "Return an error or handle the failure path explicitly.",
						Confidence:     "high",
						Source:         "rule",
						RuleID:         "panic-direct",
						Status:         "finding",
					})
				}
				if reportsHTTPBodyLeak(text, hunkText) {
					out.Findings = append(out.Findings, review.Finding{
						Severity:       "high",
						Category:       "resource",
						File:           file.Path,
						Line:           line.NewLine,
						Title:          "HTTP response body is not closed",
						Evidence:       redact(text),
						Recommendation: "Close the response body with defer resp.Body.Close() after checking the request error.",
						Confidence:     "high",
						Source:         "rule",
						RuleID:         "http-body-close",
						Status:         "finding",
					})
				}
				if reportsSQLStringConcat(text) {
					out.Findings = append(out.Findings, review.Finding{
						Severity:       "critical",
						Category:       "security",
						File:           file.Path,
						Line:           line.NewLine,
						Title:          "SQL query is built with string concatenation",
						Evidence:       redact(text),
						Recommendation: "Use parameterized queries or placeholders instead of concatenating user-controlled values.",
						Confidence:     "high",
						Source:         "rule",
						RuleID:         "sql-string-concat",
						Status:         "finding",
					})
				}
				if reportsCommandInjection(text) {
					out.Findings = append(out.Findings, review.Finding{
						Severity:       "critical",
						Category:       "security",
						File:           file.Path,
						Line:           line.NewLine,
						Title:          "Command execution uses a shell or dynamic argument",
						Evidence:       redact(text),
						Recommendation: "Avoid shell execution and pass validated literal arguments to exec.CommandContext.",
						Confidence:     "high",
						Source:         "rule",
						RuleID:         "command-injection",
						Status:         "finding",
					})
				}
				if reportsContextBackgroundMisuse(text, hunkText) {
					out.Findings = append(out.Findings, review.Finding{
						Severity:       "medium",
						Category:       "lifecycle",
						File:           file.Path,
						Line:           line.NewLine,
						Title:          "context.Background is used inside a context-aware function",
						Evidence:       redact(text),
						Recommendation: "Propagate the existing ctx so cancellation, deadlines, and trace context are preserved.",
						Confidence:     "high",
						Source:         "rule",
						RuleID:         "context-background-misuse",
						Status:         "finding",
					})
				}
				if reportsMutexUnlockMissing(text, hunkText) {
					out.Findings = append(out.Findings, review.Finding{
						Severity:       "high",
						Category:       "concurrency",
						File:           file.Path,
						Line:           line.NewLine,
						Title:          "Mutex lock has no visible deferred unlock",
						Evidence:       redact(text),
						Recommendation: "Defer Unlock immediately after Lock to avoid deadlocks on early returns.",
						Confidence:     "high",
						Source:         "rule",
						RuleID:         "mutex-unlock-missing",
						Status:         "finding",
					})
				}
				if reportsDeferInLoop(text, hunkBefore) {
					out.Findings = append(out.Findings, review.Finding{
						Severity:       "medium",
						Category:       "resource",
						File:           file.Path,
						Line:           line.NewLine,
						Title:          "defer is used inside a loop",
						Evidence:       redact(text),
						Recommendation: "Move the loop body into a helper or close the resource before the next iteration.",
						Confidence:     "high",
						Source:         "rule",
						RuleID:         "defer-in-loop",
						Status:         "finding",
					})
				}
				if reportsBareReturnErr(text) {
					out.Findings = append(out.Findings, review.Finding{
						Severity:       "medium",
						Category:       "error_handling",
						File:           file.Path,
						Line:           line.NewLine,
						Title:          "Error is returned without context",
						Evidence:       redact(text),
						Recommendation: "Wrap the error with operation context using fmt.Errorf(\"operation: %w\", err).",
						Confidence:     "high",
						Source:         "rule",
						RuleID:         "bare-return-err",
						Status:         "finding",
					})
				}
				if reportsStringConcatLoop(text, hunkBefore, hunkText) {
					out.Warnings = append(out.Warnings, review.Finding{
						Severity:       "low",
						Category:       "performance",
						File:           file.Path,
						Line:           line.NewLine,
						Title:          "String concatenation in a loop may allocate repeatedly",
						Evidence:       redact(text),
						Recommendation: "Use strings.Builder or bytes.Buffer for repeated string assembly.",
						Confidence:     "low",
						Source:         "rule",
						RuleID:         "string-concat-loop",
						Status:         "needs_human_review",
					})
				}
				if file.IsTestFile {
					continue
				}
				if strings.HasPrefix(text, "func ") && !strings.Contains(text, "error") {
					out.Warnings = append(out.Warnings, review.Finding{
						Severity:       "low",
						Category:       "testing",
						File:           file.Path,
						Line:           line.NewLine,
						Title:          "New function may need a focused test",
						Evidence:       redact(text),
						Recommendation: "Add a unit test that exercises the new path.",
						Confidence:     "medium",
						Source:         "rule",
						RuleID:         "missing-test-hint",
						Status:         "warning",
					})
				}
				if strings.Contains(text, "go func") || strings.HasPrefix(text, "go ") {
					if !containsAny(hunkText, "WaitGroup", ".Done()", "errgroup", "done", "sync.") {
						out.Findings = append(out.Findings, review.Finding{
							Severity:       "high",
							Category:       "concurrency",
							File:           file.Path,
							Line:           line.NewLine,
							Title:          "New goroutine has no visible lifecycle guard",
							Evidence:       redact(text),
							Recommendation: "Bind the goroutine to a context, wait group, or explicit completion signal.",
							Confidence:     "high",
							Source:         "rule",
							RuleID:         "goroutine-leak",
							Status:         "finding",
						})
					}
				}
				if strings.Contains(text, "context.WithCancel") ||
					strings.Contains(text, "context.WithTimeout") ||
					strings.Contains(text, "context.WithDeadline") {
					if !contextHasCancelCleanup(text, hunkText) {
						out.Findings = append(out.Findings, review.Finding{
							Severity:       "high",
							Category:       "lifecycle",
							File:           file.Path,
							Line:           line.NewLine,
							Title:          "Derived context is not canceled",
							Evidence:       redact(text),
							Recommendation: "Store the cancel function and defer cancel() in the same scope.",
							Confidence:     "high",
							Source:         "rule",
							RuleID:         "context-leak",
							Status:         "finding",
						})
					}
				}
				if strings.Contains(text, "os.Open") || strings.Contains(text, "os.OpenFile") || strings.Contains(text, "os.Create") {
					if !resourceHasCleanup(text, hunkText) {
						out.Findings = append(out.Findings, review.Finding{
							Severity:       "high",
							Category:       "resource",
							File:           file.Path,
							Line:           line.NewLine,
							Title:          "Opened resource has no close path",
							Evidence:       redact(text),
							Recommendation: "Defer Close() immediately after the resource is opened.",
							Confidence:     "high",
							Source:         "rule",
							RuleID:         "resource-leak",
							Status:         "finding",
						})
					}
				}
				if strings.Contains(text, "sql.Open") || strings.Contains(text, ".BeginTx") || strings.Contains(text, ".Begin(") {
					if !databaseHasCleanup(text, hunkText) {
						out.Findings = append(out.Findings, review.Finding{
							Severity:       "high",
							Category:       "database",
							File:           file.Path,
							Line:           line.NewLine,
							Title:          "Database handle or transaction has no cleanup path",
							Evidence:       redact(text),
							Recommendation: "Defer Close() for handles and Rollback() for transactions in the same scope.",
							Confidence:     "high",
							Source:         "rule",
							RuleID:         "db-lifecycle",
							Status:         "finding",
						})
					}
				}
				if shouldReportSecret(text) {
					out.Findings = append(out.Findings, review.Finding{
						Severity:       "critical",
						Category:       "security",
						File:           file.Path,
						Line:           line.NewLine,
						Title:          "Potential secret appears in added code",
						Evidence:       redact(text),
						Recommendation: "Replace the literal with a secret manager or environment lookup.",
						Confidence:     "high",
						Source:         "rule",
						RuleID:         "secret-leak",
						Status:         "finding",
					})
				}
			}
		}
	}
	return out
}

func hunkJoinedText(hunk review.Hunk) string {
	var b strings.Builder
	for _, line := range hunk.Lines {
		b.WriteString(line.Text)
		b.WriteString("\n")
	}
	return b.String()
}

func hunkTextBefore(hunk review.Hunk, lineIndex int) string {
	var b strings.Builder
	for i := 0; i < lineIndex && i < len(hunk.Lines); i++ {
		b.WriteString(hunk.Lines[i].Text)
		b.WriteString("\n")
	}
	return b.String()
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func reportsHTTPBodyLeak(text string, hunkText string) bool {
	if !containsAny(text, "http.Get(", "http.Post(", "http.Head(", "http.DefaultClient.Do(", ".Do(") {
		return false
	}
	name := assignedVariable(text)
	if name != "" {
		return !strings.Contains(hunkText, name+".Body.Close()")
	}
	return !strings.Contains(hunkText, "Body.Close()")
}

func reportsSQLStringConcat(text string) bool {
	upper := strings.ToUpper(text)
	if !containsAny(upper, "SELECT ", "INSERT ", "UPDATE ", "DELETE ") {
		return false
	}
	return strings.Contains(text, "+") || strings.Contains(text, "fmt.Sprintf(")
}

func reportsCommandInjection(text string) bool {
	if !containsAny(text, "exec.Command(", "exec.CommandContext(") {
		return false
	}
	if strings.Contains(text, "\"-c\"") || strings.Contains(text, "'-c'") {
		return true
	}
	return commandCallHasDynamicExecutable(text)
}

func commandCallHasDynamicExecutable(text string) bool {
	start := strings.Index(text, "exec.Command")
	if start < 0 {
		return false
	}
	open := strings.Index(text[start:], "(")
	close := strings.LastIndex(text, ")")
	if open < 0 || close < start+open {
		return false
	}
	args := strings.Split(text[start+open+1:close], ",")
	executableIndex := 0
	if strings.HasPrefix(text[start:], "exec.CommandContext") {
		executableIndex = 1
	}
	return executableIndex >= len(args) || !isQuotedLiteral(strings.TrimSpace(args[executableIndex]))
}

func isQuotedLiteral(text string) bool {
	return (strings.HasPrefix(text, "\"") && strings.HasSuffix(text, "\"")) ||
		(strings.HasPrefix(text, "'") && strings.HasSuffix(text, "'")) ||
		(strings.HasPrefix(text, "`") && strings.HasSuffix(text, "`"))
}

func reportsContextBackgroundMisuse(text string, hunkText string) bool {
	return strings.Contains(text, "context.Background()") && strings.Contains(hunkText, "context.Context")
}

func reportsMutexUnlockMissing(text string, hunkText string) bool {
	if !strings.Contains(text, ".Lock()") || strings.Contains(text, ".RLock()") {
		return false
	}
	receiver := strings.TrimSpace(strings.TrimSuffix(text, ".Lock()"))
	return receiver == "" || !strings.Contains(hunkText, receiver+".Unlock()")
}

var assignmentVariablePattern = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s*(?:,\s*[A-Za-z_][A-Za-z0-9_]*)?\s*:=`)
var contextCancelPattern = regexp.MustCompile(`(?:[A-Za-z_][A-Za-z0-9_]*|_)\s*,\s*([A-Za-z_][A-Za-z0-9_]*)\s*:=\s*context\.With(?:Cancel|Timeout|Deadline)`)

func assignedVariable(text string) string {
	match := assignmentVariablePattern.FindStringSubmatch(text)
	if len(match) == 2 {
		return match[1]
	}
	return ""
}

func contextHasCancelCleanup(text, hunkText string) bool {
	match := contextCancelPattern.FindStringSubmatch(text)
	if len(match) != 2 {
		return false
	}
	return strings.Contains(hunkText, match[1]+"()")
}

func resourceHasCleanup(text, hunkText string) bool {
	name := assignedVariable(text)
	return name != "" && strings.Contains(hunkText, name+".Close()")
}

func databaseHasCleanup(text, hunkText string) bool {
	name := assignedVariable(text)
	if name == "" {
		return false
	}
	if strings.Contains(text, "sql.Open") {
		return strings.Contains(hunkText, name+".Close()")
	}
	return strings.Contains(hunkText, name+".Rollback()")
}

func reportsDeferInLoop(text string, hunkBefore string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), "defer ") && containsAny(hunkBefore, "for ", "range ")
}

func reportsBareReturnErr(text string) bool {
	return strings.TrimSpace(text) == "return err"
}

func reportsStringConcatLoop(text string, hunkBefore string, hunkText string) bool {
	if !strings.Contains(text, "+=") {
		return false
	}
	if !containsAny(hunkBefore, "for ", "range ") && !containsAny(text, "for ", "range ") {
		return false
	}
	lhs := stringConcatLHS(text)
	if lhs == "" {
		return false
	}
	if strings.Contains(text, "\"") || strings.Contains(text, "`") {
		return true
	}
	return containsAny(hunkText, lhs+" := \"\"", "var "+lhs+" string")
}

func stringConcatLHS(text string) string {
	lhs, _, ok := strings.Cut(text, "+=")
	if !ok {
		return ""
	}
	if strings.Contains(lhs, "{") {
		parts := strings.Split(lhs, "{")
		lhs = parts[len(parts)-1]
	}
	fields := strings.Fields(strings.TrimSpace(lhs))
	if len(fields) == 0 {
		return ""
	}
	return strings.Trim(fields[len(fields)-1], " \t;")
}

var (
	secretValuePattern = regexp.MustCompile(`(?i)(sk-[A-Za-z0-9_-]{8,}|ghp_[A-Za-z0-9_]{20,}|github_pat_[A-Za-z0-9_]{20,}|Bearer\s+[A-Za-z0-9\-._~+/=]{8,}|[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}|-----BEGIN [A-Z ]*PRIVATE KEY-----|[a-z][a-z0-9+.-]*://[^/\s:@]+:[^@\s/]+@)`)
	secretNamePattern  = regexp.MustCompile(`(?i)(api[_-]?key|apikey|llm[_-]?key|openai[_-]?(api[_-]?)?key|client[_-]?secret|secret|token|bearer[_-]?token|password|passwd|pwd|github[_-]?token|private[_-]?key)`)
	stringLiteralValue = regexp.MustCompile(`=\s*("([^"]*)"|'([^']*)'|` + "`" + `([^` + "`" + `]*)` + "`" + `)`)
	placeholderSecret  = regexp.MustCompile(`(?i)^(test|example|dummy|placeholder|changeme|change-me|your[-_ ]?token|your[-_ ]?key|xxx+|<.*>)$`)
)

func shouldReportSecret(text string) bool {
	if secretValuePattern.MatchString(text) {
		return true
	}
	if !secretNamePattern.MatchString(text) {
		return false
	}
	value, ok := extractAssignedString(text)
	if !ok {
		return false
	}
	value = strings.TrimSpace(value)
	if len(value) < 12 {
		return false
	}
	return !placeholderSecret.MatchString(value)
}

func extractAssignedString(text string) (string, bool) {
	match := stringLiteralValue.FindStringSubmatch(text)
	if len(match) == 0 {
		return "", false
	}
	for _, group := range match[2:] {
		if group != "" {
			return group, true
		}
	}
	return "", false
}
