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
	"go/scanner"
	"go/token"
	"regexp"
	"strconv"
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
			scopes := analyzeHunkScopes(hunk)
			for lineIndex, line := range hunk.Lines {
				if line.Kind != "add" {
					continue
				}
				text := strings.TrimSpace(line.Text)
				scope := scopes[lineIndex]
				if file.Path == "" {
					continue
				}
				isGoFile := file.Language == "go" || strings.HasSuffix(file.Path, ".go")
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
				if shouldReportSecret(text) {
					out.Findings = append(out.Findings, review.Finding{
						Severity: "critical", Category: "security", File: file.Path, Line: line.NewLine,
						Title: "Potential secret appears in added code", Evidence: redact(text),
						Recommendation: "Replace the literal with a secret manager or environment lookup.",
						Confidence:     "high", Source: "rule", RuleID: "secret-leak", Status: "finding",
					})
				}
				if !isGoFile {
					continue
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
				if reportsHTTPBodyLeak(text, scope.CleanupText) {
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
				if reportsContextBackgroundMisuse(text, scope.FunctionText) {
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
				if reportsMutexUnlockMissing(text, scope.CleanupText) {
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
				if reportsDeferInLoop(text, scope.InLoop) {
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
				if reportsStringConcatLoop(text, scope.InLoop, scope.FunctionText) {
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
					if !containsAny(scope.FunctionText, "WaitGroup", ".Done()", "errgroup", "done", "sync.") {
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
					if !contextHasCancelCleanup(text, scope.CleanupText) {
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
					if !resourceHasCleanup(text, scope.CleanupText) {
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
					if !databaseHasCleanup(text, scope.CleanupText) {
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
			}
		}
	}
	return out
}

type hunkScope struct {
	FunctionText string
	CleanupText  string
	InLoop       bool
}

type scopedHunkLine struct {
	index         int
	text          string
	segment       int
	blockPath     string
	inLoop        bool
	cleanupOffset int
}

type lexicalBlock struct {
	id     int
	isLoop bool
}

// analyzeHunkScopes keeps heuristic rules inside the visible function and
// lexical block. It intentionally fails closed when a surrounding block is not
// present in the hunk instead of borrowing cleanup from unrelated functions.
func analyzeHunkScopes(hunk review.Hunk) map[int]hunkScope {
	lines := make([]scopedHunkLine, 0, len(hunk.Lines))
	blocks := make([]lexicalBlock, 0)
	segment := 0
	nextBlockID := 0
	for index, line := range hunk.Lines {
		if line.Kind == "del" {
			continue
		}
		text := strings.TrimSpace(line.Text)
		if isFunctionStart(text) {
			segment++
			blocks = blocks[:0]
		}
		tokens := goStructureTokens(text)
		for len(tokens) > 0 && tokens[0] == "}" {
			if len(blocks) > 0 {
				blocks = blocks[:len(blocks)-1]
			}
			tokens = tokens[1:]
		}
		lines = append(lines, scopedHunkLine{
			index: index, text: text, segment: segment,
			blockPath: lexicalBlockPath(blocks), inLoop: lexicalLoopActive(blocks),
		})
		pendingLoop := false
		for _, structureToken := range tokens {
			switch structureToken {
			case "for":
				pendingLoop = true
			case "{":
				nextBlockID++
				blocks = append(blocks, lexicalBlock{id: nextBlockID, isLoop: pendingLoop})
				pendingLoop = false
			case "}":
				if len(blocks) > 0 {
					blocks = blocks[:len(blocks)-1]
				}
			}
		}
	}

	functionBuilders := make(map[int]*strings.Builder)
	cleanupBuilders := make(map[string]*strings.Builder)
	for index := range lines {
		line := &lines[index]
		functionBuilder := functionBuilders[line.segment]
		if functionBuilder == nil {
			functionBuilder = &strings.Builder{}
			functionBuilders[line.segment] = functionBuilder
		}
		functionBuilder.WriteString(line.text)
		functionBuilder.WriteByte('\n')
		cleanupKey := lexicalCleanupKey(line.segment, line.blockPath)
		cleanupBuilder := cleanupBuilders[cleanupKey]
		if cleanupBuilder == nil {
			cleanupBuilder = &strings.Builder{}
			cleanupBuilders[cleanupKey] = cleanupBuilder
		}
		line.cleanupOffset = cleanupBuilder.Len()
		cleanupBuilder.WriteString(line.text)
		cleanupBuilder.WriteByte('\n')
	}
	functionText := make(map[int]string, len(functionBuilders))
	for segment, builder := range functionBuilders {
		functionText[segment] = builder.String()
	}
	cleanupText := make(map[string]string, len(cleanupBuilders))
	for key, builder := range cleanupBuilders {
		cleanupText[key] = builder.String()
	}
	out := make(map[int]hunkScope, len(lines))
	for _, line := range lines {
		cleanup := cleanupText[lexicalCleanupKey(line.segment, line.blockPath)]
		out[line.index] = hunkScope{
			FunctionText: functionText[line.segment],
			CleanupText:  cleanup[line.cleanupOffset:],
			InLoop:       line.inLoop,
		}
	}
	return out
}

func lexicalCleanupKey(segment int, blockPath string) string {
	return strconv.Itoa(segment) + ":" + blockPath
}

func isFunctionStart(text string) bool {
	return strings.HasPrefix(text, "func ") || strings.HasPrefix(text, "func(")
}

func goStructureTokens(text string) []string {
	fileSet := token.NewFileSet()
	file := fileSet.AddFile("hunk.go", fileSet.Base(), len(text))
	var lexer scanner.Scanner
	lexer.Init(file, []byte(text), func(token.Position, string) {}, scanner.ScanComments)
	var out []string
	for {
		_, tok, _ := lexer.Scan()
		switch tok {
		case token.EOF:
			return out
		case token.FOR:
			out = append(out, "for")
		case token.LBRACE:
			out = append(out, "{")
		case token.RBRACE:
			out = append(out, "}")
		}
	}
}

func lexicalBlockPath(blocks []lexicalBlock) string {
	var b strings.Builder
	for _, block := range blocks {
		b.WriteString(strconv.Itoa(block.id))
		b.WriteByte('/')
	}
	return b.String()
}

func lexicalLoopActive(blocks []lexicalBlock) bool {
	for _, block := range blocks {
		if block.isLoop {
			return true
		}
	}
	return false
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

func reportsDeferInLoop(text string, inLoop bool) bool {
	return strings.HasPrefix(strings.TrimSpace(text), "defer ") && inLoop
}

func reportsBareReturnErr(text string) bool {
	return strings.TrimSpace(text) == "return err"
}

func reportsStringConcatLoop(text string, inLoop bool, functionText string) bool {
	if !strings.Contains(text, "+=") {
		return false
	}
	if !inLoop {
		return false
	}
	lhs := stringConcatLHS(text)
	if lhs == "" {
		return false
	}
	if strings.Contains(text, "\"") || strings.Contains(text, "`") {
		return true
	}
	return containsAny(functionText, lhs+" := \"\"", "var "+lhs+" string")
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
