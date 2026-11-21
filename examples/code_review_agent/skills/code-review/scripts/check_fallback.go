//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	goscanner "go/scanner"
	"go/token"
	"os"
	"regexp"
	"strconv"
	"strings"
)

type finding struct {
	Severity       string `json:"severity"`
	Category       string `json:"category"`
	File           string `json:"file"`
	Line           int    `json:"line"`
	Title          string `json:"title"`
	Evidence       string `json:"evidence"`
	Recommendation string `json:"recommendation"`
	Confidence     string `json:"confidence"`
	Source         string `json:"source"`
	RuleID         string `json:"rule_id"`
	Status         string `json:"status"`
}

func main() {
	path := os.Args[1]
	data, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}

	findings := make([]finding, 0)
	warnings := make([]finding, 0)
	emittedFindings := map[string]bool{}
	emittedWarnings := map[string]bool{}
	currentFile := ""
	currentHunk := make([]string, 0)
	newLine := 0
	oldRemaining := 0
	newRemaining := 0
	sawDiffStructure := false
	hunkStart := regexp.MustCompile(`\+(\d+)`)
	diff := string(data)
	hunkScopes := buildHunkScopes(diff)

	// The input layer has already enforced the configured total input bound.
	// Allow any single line within that accepted input to fit in the scanner.
	maxTokenBytes := len(data) + 1
	if maxTokenBytes < 1024 {
		maxTokenBytes = 1024
	}
	scanner := bufio.NewScanner(strings.NewReader(diff))
	scanner.Buffer(make([]byte, 1024), maxTokenBytes)
	lineIndex := 0
	for scanner.Scan() {
		index := lineIndex
		lineIndex++
		line := scanner.Text()
		if strings.HasPrefix(line, "diff --git ") {
			sawDiffStructure = true
		}
		switch {
		case oldRemaining <= 0 && newRemaining <= 0 && strings.HasPrefix(line, "+++ "):
			sawDiffStructure = true
			currentFile = normalizeDiffPath(strings.TrimPrefix(line, "+++ "))
			continue
		case strings.HasPrefix(line, "@@ "):
			if oldRemaining > 0 || newRemaining > 0 {
				panic("new hunk started before the previous hunk was complete")
			}
			if !parseHunkCounts(line, &oldRemaining, &newRemaining) {
				panic(fmt.Errorf("invalid hunk header: %q", line))
			}
			sawDiffStructure = true
			match := hunkStart.FindStringSubmatch(line)
			newLine = 0
			if len(match) == 2 {
				_, _ = fmt.Sscanf(match[1], "%d", &newLine)
				newLine--
			}
			currentHunk = currentHunk[:0]
			continue
		case newRemaining > 0 && strings.HasPrefix(line, "+"):
			newLine++
			newRemaining--
			text := strings.TrimSpace(strings.TrimPrefix(line, "+"))
			currentHunk = append(currentHunk, text)
			scope := hunkScopes[index]
			if scope.FunctionText == "" {
				scope.FunctionText = strings.Join(currentHunk, "\n")
				scope.CleanupText = scope.FunctionText
			}

			addFinding := func(severity, category, title, recommendation, ruleID string) {
				key := dedupeKey(currentFile, newLine, category, ruleID)
				if emittedFindings[key] {
					return
				}
				emittedFindings[key] = true
				findings = append(findings, finding{
					Severity: severity, Category: category, File: currentFile, Line: newLine,
					Title: title, Evidence: redact(text), Recommendation: recommendation,
					Confidence: "high", Source: "skill_run", RuleID: ruleID, Status: "finding",
				})
			}
			addWarning := func(severity, category, title, recommendation, ruleID string) {
				key := dedupeKey(currentFile, newLine, category, ruleID)
				if emittedWarnings[key] {
					return
				}
				emittedWarnings[key] = true
				warnings = append(warnings, finding{
					Severity: severity, Category: category, File: currentFile, Line: newLine,
					Title: title, Evidence: redact(text), Recommendation: recommendation,
					Confidence: "medium", Source: "skill_run", RuleID: ruleID, Status: "warning",
				})
			}

			if strings.Contains(text, "TODO(") || strings.Contains(text, "FIXME") {
				addFinding("medium", "maintainability", "New code contains a TODO or FIXME marker",
					"Remove the marker or turn it into a tracked issue before merging.", "todo-marker")
			}
			if shouldReportSecret(text) {
				addFinding("critical", "security", "Potential secret appears in added code", "Replace the literal with a secret manager or environment lookup.", "secret-leak")
			}
			if !isGoFile(currentFile) {
				continue
			}
			if strings.Contains(text, "panic(") {
				addFinding("high", "error_handling", "New function panics directly",
					"Return an error or handle the failure path explicitly.", "panic-direct")
			}
			if reportsHTTPBodyLeak(text, scope.CleanupText) {
				addFinding("high", "resource", "HTTP response body is not closed",
					"Close the response body with defer resp.Body.Close() after checking the request error.", "http-body-close")
			}
			if reportsSQLStringConcat(text) {
				addFinding("critical", "security", "SQL query is built with string concatenation",
					"Use parameterized queries or placeholders instead of concatenating user-controlled values.", "sql-string-concat")
			}
			if reportsCommandInjection(text) {
				addFinding("critical", "security", "Command execution uses a shell or dynamic argument",
					"Avoid shell execution and pass validated literal arguments to exec.CommandContext.", "command-injection")
			}
			if reportsContextBackgroundMisuse(text, scope.FunctionText) {
				addFinding("medium", "lifecycle", "context.Background is used inside a context-aware function",
					"Propagate the existing ctx so cancellation, deadlines, and trace context are preserved.", "context-background-misuse")
			}
			if reportsMutexUnlockMissing(text, scope.CleanupText) {
				addFinding("high", "concurrency", "Mutex lock has no visible deferred unlock",
					"Defer Unlock immediately after Lock to avoid deadlocks on early returns.", "mutex-unlock-missing")
			}
			if reportsDeferInLoop(text, scope.InLoop) {
				addFinding("medium", "resource", "defer is used inside a loop",
					"Move the loop body into a helper or close the resource before the next iteration.", "defer-in-loop")
			}
			if reportsBareReturnErr(text) {
				addFinding("medium", "error_handling", "Error is returned without context",
					"Wrap the error with operation context using fmt.Errorf(\"operation: %w\", err).", "bare-return-err")
			}
			if reportsStringConcatLoop(text, scope.InLoop, scope.FunctionText) {
				addWarning("low", "performance", "String concatenation in a loop may allocate repeatedly",
					"Use strings.Builder or bytes.Buffer for repeated string assembly.", "string-concat-loop")
				warnings[len(warnings)-1].Confidence = "low"
				warnings[len(warnings)-1].Status = "needs_human_review"
			}
			if strings.HasSuffix(currentFile, ".go") &&
				!strings.HasSuffix(currentFile, "_test.go") && strings.HasPrefix(text, "func ") &&
				!strings.Contains(text, "error") {
				addWarning("low", "testing", "New function may need a focused test",
					"Add a unit test that exercises the new path.", "missing-test-hint")
			}
			if (strings.Contains(text, "go func") || strings.HasPrefix(text, "go ")) &&
				!containsAny(scope.FunctionText, "WaitGroup", ".Done()", "ctx.Done", "errgroup", "done", "sync.") {
				addFinding("high", "concurrency", "New goroutine has no visible lifecycle guard",
					"Bind the goroutine to a context, wait group, or explicit completion signal.", "goroutine-leak")
			}
			if containsAny(text, "context.WithCancel", "context.WithTimeout", "context.WithDeadline") &&
				!contextHasCancelCleanup(text, scope.CleanupText) {
				addFinding("high", "lifecycle", "Derived context is not canceled",
					"Store the cancel function and defer cancel() in the same scope.", "context-leak")
			}
			if containsAny(text, "os.Open", "os.OpenFile", "os.Create") &&
				!resourceHasCleanup(text, scope.CleanupText) {
				addFinding("high", "resource", "Opened resource has no close path",
					"Defer Close() immediately after the resource is opened.", "resource-leak")
			}
			if containsAny(text, "sql.Open", ".BeginTx", ".Begin(") &&
				!databaseHasCleanup(text, scope.CleanupText) {
				addFinding("high", "database", "Database handle or transaction has no cleanup path",
					"Defer Close() for handles and Rollback() for transactions in the same scope.", "db-lifecycle")
			}
		case oldRemaining > 0 && strings.HasPrefix(line, "-"):
			oldRemaining--
		case oldRemaining > 0 && newRemaining > 0 && strings.HasPrefix(line, " "):
			oldRemaining--
			newRemaining--
			newLine++
			currentHunk = append(currentHunk, strings.TrimPrefix(line, " "))
		case (oldRemaining > 0 || newRemaining > 0) && line != `\ No newline at end of file`:
			panic(fmt.Errorf("invalid hunk line: %q", line))
		case (strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++ ")) ||
			(strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "--- ")) ||
			strings.HasPrefix(line, " "):
			panic(fmt.Errorf("excess hunk line: %q", line))
		}
	}
	if err := scanner.Err(); err != nil {
		panic(fmt.Errorf("scan diff: %w", err))
	}
	if oldRemaining != 0 || newRemaining != 0 {
		panic(fmt.Errorf("incomplete hunk: %d old and %d new lines remain", oldRemaining, newRemaining))
	}
	if strings.TrimSpace(diff) != "" && !sawDiffStructure {
		panic("input is not a unified diff")
	}

	out, _ := json.Marshal(map[string]any{"findings": findings, "warnings": warnings})
	fmt.Println(string(out))
}

func redact(text string) string {
	out := text
	replacers := []struct {
		re   *regexp.Regexp
		with string
	}{
		{regexp.MustCompile(`(?i)\b(api[_-]?key|apikey|llm[_-]?key|openai[_-]?(api[_-]?)?key|client[_-]?secret|secret|token|bearer[_-]?token|password|passwd|pwd|github[_-]?token|private[_-]?key)\b\s*(?::=|[:=])\s*("[^"]+"|'[^']+'|[^\s,;]+)`), `$1=[REDACTED]`},
		{regexp.MustCompile(`(?i)\bearer\s+[A-Za-z0-9\-._~+/=]+`), `Bearer [REDACTED]`},
		{regexp.MustCompile(`sk-[A-Za-z0-9_-]{8,}`), `[REDACTED]`},
		{regexp.MustCompile(`ghp_[A-Za-z0-9_]{20,}`), `[REDACTED]`},
		{regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`), `[REDACTED]`},
		{regexp.MustCompile(`[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`), `[REDACTED]`},
		{regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`), `[REDACTED_PRIVATE_KEY]`},
		{regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://[^/\s:@]+):([^@\s/]+)@`), `${1}:[REDACTED]@`},
		{regexp.MustCompile(`(?i)(password=)[^&\s]+`), `${1}[REDACTED]`},
	}
	for _, replacer := range replacers {
		out = replacer.re.ReplaceAllString(out, replacer.with)
	}
	return out
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

func dedupeKey(file string, line int, category string, ruleID string) string {
	return fmt.Sprintf("%s|%d|%s|%s", file, line, category, ruleID)
}

func containsAny(text string, items ...string) bool {
	for _, item := range items {
		if strings.Contains(text, item) {
			return true
		}
	}
	return false
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

func buildHunkScopes(data string) map[int]hunkScope {
	out := map[int]hunkScope{}
	lines := strings.Split(strings.ReplaceAll(data, "\r\n", "\n"), "\n")
	hunkLines := make([]scopedHunkLine, 0)
	blocks := make([]lexicalBlock, 0)
	segment := 0
	nextBlockID := 0
	oldRemaining := 0
	newRemaining := 0
	flush := func() {
		if len(hunkLines) == 0 {
			return
		}
		functionBuilders := make(map[int]*strings.Builder)
		cleanupBuilders := make(map[string]*strings.Builder)
		for index := range hunkLines {
			line := &hunkLines[index]
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
		for _, line := range hunkLines {
			cleanup := cleanupText[lexicalCleanupKey(line.segment, line.blockPath)]
			out[line.index] = hunkScope{
				FunctionText: functionText[line.segment], CleanupText: cleanup[line.cleanupOffset:], InLoop: line.inLoop,
			}
		}
	}
	appendLine := func(index int, text string) {
		text = strings.TrimSpace(text)
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
		hunkLines = append(hunkLines, scopedHunkLine{
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

	for index, line := range lines {
		switch {
		case parseHunkCounts(line, &oldRemaining, &newRemaining):
			flush()
			hunkLines = hunkLines[:0]
			blocks = blocks[:0]
			segment = 0
		case oldRemaining <= 0 && newRemaining <= 0 && (strings.HasPrefix(line, "diff --git ") || strings.HasPrefix(line, "+++ ")):
			continue
		case newRemaining > 0 && strings.HasPrefix(line, "+"):
			appendLine(index, strings.TrimPrefix(line, "+"))
			newRemaining--
		case oldRemaining > 0 && newRemaining > 0 && strings.HasPrefix(line, " "):
			appendLine(index, strings.TrimPrefix(line, " "))
			oldRemaining--
			newRemaining--
		case oldRemaining > 0 && strings.HasPrefix(line, "-"):
			oldRemaining--
		}
	}
	flush()
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
	var lexer goscanner.Scanner
	lexer.Init(file, []byte(text), func(token.Position, string) {}, goscanner.ScanComments)
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

func parseHunkCounts(line string, oldRemaining, newRemaining *int) bool {
	match := regexp.MustCompile(`^@@ -\d+(?:,(\d+))? \+\d+(?:,(\d+))? @@`).FindStringSubmatch(line)
	if len(match) != 3 {
		return false
	}
	*oldRemaining = hunkCount(match[1])
	*newRemaining = hunkCount(match[2])
	return true
}

func hunkCount(raw string) int {
	if raw == "" {
		return 1
	}
	count, _ := strconv.Atoi(raw)
	return count
}

func reportsHTTPBodyLeak(text string, hunkText string) bool {
	if !containsAny(text, "http.Get(", "http.Post(", "http.Head(", "http.DefaultClient.Do(", ".Do(") {
		return false
	}
	return !containsAny(hunkText, "Body.Close()", "defer resp.Body.Close()", "defer response.Body.Close()")
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
	if containsAny(text, "\"-c\"", "'-c'", "\"-lc\"", "'-lc'") {
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

var (
	assignmentVariablePattern = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s*(?:,\s*[A-Za-z_][A-Za-z0-9_]*)?\s*:=`)
	contextCancelPattern      = regexp.MustCompile(`(?:[A-Za-z_][A-Za-z0-9_]*|_)\s*,\s*([A-Za-z_][A-Za-z0-9_]*)\s*:=\s*context\.With(?:Cancel|Timeout|Deadline)`)
)

func assignedVariable(text string) string {
	match := assignmentVariablePattern.FindStringSubmatch(text)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

func contextHasCancelCleanup(text string, hunkText string) bool {
	match := contextCancelPattern.FindStringSubmatch(text)
	return len(match) == 2 && strings.Contains(hunkText, match[1]+"()")
}

func resourceHasCleanup(text string, hunkText string) bool {
	name := assignedVariable(text)
	return name != "" && strings.Contains(hunkText, name+".Close()")
}

func databaseHasCleanup(text string, hunkText string) bool {
	name := assignedVariable(text)
	if name == "" {
		return false
	}
	cleanup := "Close"
	if !strings.Contains(text, "sql.Open") {
		cleanup = "Rollback"
	}
	return strings.Contains(hunkText, name+"."+cleanup+"()")
}

func reportsMutexUnlockMissing(text string, hunkText string) bool {
	if !strings.Contains(text, ".Lock()") || strings.Contains(text, ".RLock()") {
		return false
	}
	return !containsAny(hunkText, ".Unlock()", "defer mu.Unlock()", "defer mutex.Unlock()")
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

func normalizeDiffPath(raw string) string {
	path := strings.TrimSpace(raw)
	if len(path) >= 2 && path[0] == '"' && path[len(path)-1] == '"' {
		if unquoted, err := strconv.Unquote(path); err == nil {
			path = unquoted
		}
	} else if idx := strings.IndexByte(path, '\t'); idx >= 0 {
		path = strings.TrimSpace(path[:idx])
	}
	if strings.HasPrefix(path, "a/") || strings.HasPrefix(path, "b/") {
		path = path[2:]
	}
	return path
}

func isGoFile(path string) bool { return strings.HasSuffix(path, ".go") }
