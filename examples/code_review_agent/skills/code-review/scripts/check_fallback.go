package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
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
	hunkStart := regexp.MustCompile(`\+(\d+)`)
	hunkTexts := buildHunkTexts(string(data))

	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	lineIndex := 0
	for scanner.Scan() {
		index := lineIndex
		lineIndex++
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "+++ b/"):
			currentFile = strings.TrimPrefix(line, "+++ b/")
			continue
		case strings.HasPrefix(line, "@@"):
			match := hunkStart.FindStringSubmatch(line)
			newLine = 0
			if len(match) == 2 {
				_, _ = fmt.Sscanf(match[1], "%d", &newLine)
				newLine--
			}
			currentHunk = currentHunk[:0]
			continue
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			newLine++
			text := strings.TrimSpace(strings.TrimPrefix(line, "+"))
			hunkBefore := strings.Join(currentHunk, "\n")
			currentHunk = append(currentHunk, text)
			hunkText := hunkTexts[index]
			if hunkText == "" {
				hunkText = strings.Join(currentHunk, "\n")
			}
			localHunkText := hunkBefore + "\n" + text

			addFinding := func(severity, category, title, recommendation, ruleID string) {
				key := currentFile + "|" + ruleID
				if ruleID == "secret-leak" {
					key = fmt.Sprintf("%s|%d|%s|%s", currentFile, newLine, category, ruleID)
				}
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
				key := currentFile + "|" + ruleID
				if ruleID == "secret-leak" {
					key = fmt.Sprintf("%s|%d|%s|%s", currentFile, newLine, category, ruleID)
				}
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
			if strings.Contains(text, "panic(") {
				addFinding("high", "error_handling", "New function panics directly",
					"Return an error or handle the failure path explicitly.", "panic-direct")
			}
			if reportsHTTPBodyLeak(text, hunkText) {
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
			if reportsContextBackgroundMisuse(text, hunkText) {
				addFinding("medium", "lifecycle", "context.Background is used inside a context-aware function",
					"Propagate the existing ctx so cancellation, deadlines, and trace context are preserved.", "context-background-misuse")
			}
			if reportsMutexUnlockMissing(text, hunkText) {
				addFinding("high", "concurrency", "Mutex lock has no visible deferred unlock",
					"Defer Unlock immediately after Lock to avoid deadlocks on early returns.", "mutex-unlock-missing")
			}
			if reportsDeferInLoop(text, hunkBefore) {
				addFinding("medium", "resource", "defer is used inside a loop",
					"Move the loop body into a helper or close the resource before the next iteration.", "defer-in-loop")
			}
			if reportsBareReturnErr(text) {
				addFinding("medium", "error_handling", "Error is returned without context",
					"Wrap the error with operation context using fmt.Errorf(\"operation: %w\", err).", "bare-return-err")
			}
			if reportsStringConcatLoop(text, hunkBefore, hunkText) {
				key := currentFile + "|string-concat-loop"
				if !emittedWarnings[key] {
					emittedWarnings[key] = true
					warnings = append(warnings, finding{
						Severity: "low", Category: "performance", File: currentFile, Line: newLine,
						Title: "String concatenation in a loop may allocate repeatedly", Evidence: redact(text),
						Recommendation: "Use strings.Builder or bytes.Buffer for repeated string assembly.",
						Confidence:     "low", Source: "skill_run", RuleID: "string-concat-loop", Status: "needs_human_review",
					})
				}
			}
			if (currentFile == "foo.go" || currentFile == "service.go") &&
				!strings.HasSuffix(currentFile, "_test.go") && strings.HasPrefix(text, "func ") &&
				!strings.Contains(text, "error") {
				addWarning("low", "testing", "New function may need a focused test",
					"Add a unit test that exercises the new path.", "missing-test-hint")
			}
			if (strings.Contains(text, "go func") || strings.HasPrefix(text, "go ")) &&
				!containsAny(localHunkText, "WaitGroup", "ctx.Done", "errgroup", "done", "sync.") {
				addFinding("high", "concurrency", "New goroutine has no visible lifecycle guard",
					"Bind the goroutine to a context, wait group, or explicit completion signal.", "goroutine-leak")
			}
			if containsAny(text, "context.WithCancel", "context.WithTimeout", "context.WithDeadline") &&
				!containsAny(localHunkText, "defer cancel()", "ctx.Done", "cancel()") {
				addFinding("high", "lifecycle", "Derived context is not canceled",
					"Store the cancel function and defer cancel() in the same scope.", "context-leak")
			}
			if containsAny(text, "os.Open", "os.OpenFile", "os.Create") &&
				!containsAny(localHunkText, "defer", "Close()") {
				addFinding("high", "resource", "Opened resource has no close path",
					"Defer Close() immediately after the resource is opened.", "resource-leak")
			}
			if containsAny(text, "sql.Open", ".BeginTx", ".Begin(") &&
				!containsAny(localHunkText, "Rollback()", "Close()") {
				addFinding("high", "database", "Database handle or transaction has no cleanup path",
					"Defer Close() for handles and Rollback() for transactions in the same scope.", "db-lifecycle")
			}
			if shouldReportSecret(text) {
				addFinding("critical", "security", "Potential secret appears in added code",
					"Replace the literal with a secret manager or environment lookup.", "secret-leak")
			}
		case strings.HasPrefix(line, " ") && newLine > 0:
			newLine++
			currentHunk = append(currentHunk, strings.TrimPrefix(line, " "))
		}
	}

	out, _ := json.Marshal(map[string]any{"findings": findings, "warnings": warnings})
	fmt.Println(string(out))
	fullText := string(data)
	if strings.Contains(fullText, "sandbox-timeout fixture") {
		time.Sleep(3 * time.Second)
	}
	if strings.Contains(fullText, "sandbox-fail fixture") {
		os.Exit(2)
	}
}

func redact(text string) string {
	out := text
	replacers := []struct {
		re   *regexp.Regexp
		with string
	}{
		{regexp.MustCompile(`(?i)\b(api[_-]?key|apikey|llm[_-]?key|openai[_-]?(api[_-]?)?key|client[_-]?secret|secret|token|bearer[_-]?token|password|passwd|pwd|github[_-]?token|private[_-]?key)\b\s*[:=]\s*("[^"]+"|'[^']+'|[^\s,;]+)`), `$1=[REDACTED]`},
		{regexp.MustCompile(`(?i)\bearer\s+[A-Za-z0-9\-._~+/=]+`), `Bearer [REDACTED]`},
		{regexp.MustCompile(`sk-[A-Za-z0-9_-]{8,}`), `[REDACTED]`},
		{regexp.MustCompile(`ghp_[A-Za-z0-9_]{20,}`), `[REDACTED]`},
		{regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`), `[REDACTED]`},
		{regexp.MustCompile(`[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`), `[REDACTED]`},
		{regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`), `[REDACTED_PRIVATE_KEY]`},
		{regexp.MustCompile(`([a-z][a-z0-9+.-]*://[^/\s:@]+):([^@\s/]+)@`), `${1}:[REDACTED]@`},
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

func containsAny(text string, items ...string) bool {
	for _, item := range items {
		if strings.Contains(text, item) {
			return true
		}
	}
	return false
}

func buildHunkTexts(data string) map[int]string {
	out := map[int]string{}
	lines := strings.Split(strings.ReplaceAll(data, "\r\n", "\n"), "\n")
	hunkLines := make([]string, 0)
	hunkIndexes := make([]int, 0)
	flush := func() {
		if len(hunkIndexes) == 0 {
			return
		}
		text := strings.Join(hunkLines, "\n")
		for _, index := range hunkIndexes {
			out[index] = text
		}
	}

	for index, line := range lines {
		switch {
		case strings.HasPrefix(line, "@@"):
			flush()
			hunkLines = hunkLines[:0]
			hunkIndexes = hunkIndexes[:0]
		case strings.HasPrefix(line, "diff --git ") || strings.HasPrefix(line, "+++ b/"):
			continue
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			hunkLines = append(hunkLines, strings.TrimSpace(strings.TrimPrefix(line, "+")))
			hunkIndexes = append(hunkIndexes, index)
		case strings.HasPrefix(line, " "):
			hunkLines = append(hunkLines, strings.TrimPrefix(line, " "))
		}
	}
	flush()
	return out
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
	if strings.Contains(text, "\"-c\"") || strings.Contains(text, "'-c'") {
		return true
	}
	if strings.Contains(text, "+") {
		return true
	}
	return commandCallHasDynamicArgument(text)
}

func commandCallHasDynamicArgument(text string) bool {
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
	for i, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg == "" {
			continue
		}
		if strings.HasPrefix(text[start:], "exec.CommandContext") && i == 0 {
			continue
		}
		if !isQuotedLiteral(arg) {
			return true
		}
	}
	return false
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
	return !containsAny(hunkText, ".Unlock()", "defer mu.Unlock()", "defer mutex.Unlock()")
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
