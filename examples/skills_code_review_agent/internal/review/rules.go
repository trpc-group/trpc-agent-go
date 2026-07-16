//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import (
	"regexp"
	"strings"
)

var (
	secretLineRE = regexp.MustCompile(`(?i)(api[_-]?key|access[_-]?key|secret[_-]?access[_-]?key|client[_-]?secret|refresh[_-]?token|session[_-]?token|auth[_-]?token|token|secret|credential|password|passwd|authorization|private key)\s*[:=]`)
	osOpenRE     = regexp.MustCompile(`\b(os\.Open|os\.OpenFile|os\.Create|os\.CreateTemp|ioutil\.TempFile|sql\.Open|sql\.OpenDB|http\.Get|http\.Post|net\.Dial|net\.Listen|template\.ParseFiles)\b`)
	httpDoRE     = regexp.MustCompile(`\b([A-Za-z0-9_]+\.)?Do\s*\(`)
	rowsQueryRE  = regexp.MustCompile(`\b[A-Za-z0-9_]+\s*(?:,|:=|=).*\b[A-Za-z0-9_]+\.(Query|QueryContext)\s*\(`)
	dbBeginRE    = regexp.MustCompile(`\b(Begin|BeginTx)\s*\(`)
	errIgnoreRE  = regexp.MustCompile(`(^|[^[:alnum:]_])_\s*(,|:=|=)`)
	sqlExecRE    = regexp.MustCompile(`(?i)\b(db|tx|stmt)\s*\.\s*(Query|Exec|QueryRow|QueryContext|ExecContext|QueryRowContext)\s*\(`)
	sqlConcatRE  = regexp.MustCompile(`\+\s*[[:alnum:]_]+|fmt\.Sprintf\s*\(`)
	execCmdRE    = regexp.MustCompile(`\bexec\.Command(?:Context)?\s*\(`)
	mutexLockRE  = regexp.MustCompile(`\b[[:alnum:]_]+\.Lock\(\)`)
	ctxCancelRE  = regexp.MustCompile(`\bcontext\.With(Cancel|Timeout|Deadline)\s*\(`)
	bareErrRE    = regexp.MustCompile(`^return\s+(?:nil,\s*)?err$`)
	mapWriteRE   = regexp.MustCompile(`\[[^\]]+\]\s*=`)
	stringLitRE  = regexp.MustCompile(`["']([^"']{32,})["']`)
	urlCredRE    = regexp.MustCompile(`(?i)://[^:/\s]+:[^@\s/]+@`)
	basicAuthRE  = regexp.MustCompile(`(?i)SetBasicAuth\(\s*"[^"]+"\s*,\s*"[^"]+"\s*\)`)
	authHeaderRE = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{12,}`)
)

func AnalyzeDiff(pd ParsedDiff) (findings, warnings, needsHuman []Finding) {
	var all []Finding
	for _, h := range pd.Hunks {
		if !strings.HasSuffix(h.File, ".go") {
			continue
		}
		hunkText := hunkAddedAndContext(h)
		for _, line := range h.Lines {
			if line.Kind != '+' {
				continue
			}
			text := strings.TrimSpace(line.Text)
			if text == "" || strings.HasPrefix(text, "//") {
				continue
			}
			all = append(all, lineRules(h.File, line.NewLine, text, hunkText)...)
		}
		all = append(all, hunkRules(h.File, h)...)
	}
	all = append(all, astFindings(pd)...)
	all = append(all, missingTestFindings(pd)...)
	all = DedupeFindings(all)
	for _, f := range all {
		switch {
		case f.Confidence < 0.55:
			warnings = append(warnings, f)
		case f.Confidence < 0.70 || f.Severity == SeverityLow:
			needsHuman = append(needsHuman, f)
		default:
			findings = append(findings, f)
		}
	}
	return findings, warnings, needsHuman
}

func lineRules(file string, line int, text string, hunkText string) []Finding {
	var out []Finding
	if secretLineRE.MatchString(text) || textContainsCredentialLiteral(text) {
		out = append(out, Finding{
			Severity:       SeverityCritical,
			Category:       "security",
			File:           file,
			Line:           line,
			Title:          "Hard-coded secret or credential-like value",
			Evidence:       redactSecrets(text),
			Recommendation: "Move secrets to a managed secret store or environment variable and rotate any exposed value.",
			Confidence:     0.95,
			Source:         "rule",
			RuleID:         "go/security/secret-literal",
		})
	}
	if looksHighEntropySecretLiteral(text) {
		out = append(out, Finding{
			Severity:       SeverityHigh,
			Category:       "security",
			File:           file,
			Line:           line,
			Title:          "High-entropy literal looks like an embedded secret",
			Evidence:       redactSecrets(text),
			Recommendation: "Move opaque credentials or tokens to a managed secret store and rotate the exposed value if it is real.",
			Confidence:     0.72,
			Source:         "rule",
			RuleID:         "go/security/secret-literal",
		})
	}
	if urlCredRE.MatchString(text) || basicAuthRE.MatchString(text) {
		out = append(out, Finding{
			Severity:       SeverityCritical,
			Category:       "security",
			File:           file,
			Line:           line,
			Title:          "Credential is embedded directly in code",
			Evidence:       redactSecrets(text),
			Recommendation: "Move embedded credentials to a managed secret store and rotate any exposed value.",
			Confidence:     0.94,
			Source:         "rule",
			RuleID:         "go/security/secret-literal",
		})
	}
	if strings.Contains(text, "go func") || strings.HasPrefix(text, "go ") {
		if !strings.Contains(hunkText, "context.") &&
			!strings.Contains(hunkText, ".Done()") &&
			!strings.Contains(hunkText, "ctx") {
			out = append(out, Finding{
				Severity:       SeverityHigh,
				Category:       "concurrency",
				File:           file,
				Line:           line,
				Title:          "Goroutine has no visible context cancellation path",
				Evidence:       redactSecrets(text),
				Recommendation: "Pass context into the goroutine and exit on ctx.Done() or another bounded lifecycle signal.",
				Confidence:     0.78,
				Source:         "rule",
				RuleID:         "go/concurrency/goroutine-context",
			})
		}
	}
	if bareErrRE.MatchString(text) {
		out = append(out, Finding{
			Severity:       SeverityMedium,
			Category:       "error_handling",
			File:           file,
			Line:           line,
			Title:          "Error is returned without added context",
			Evidence:       redactSecrets(text),
			Recommendation: "Wrap errors with operation context, for example fmt.Errorf(\"load config: %w\", err), unless the caller already has enough context.",
			Confidence:     0.58,
			Source:         "rule",
			RuleID:         "go/error/bare-return",
		})
	}
	if uncheckedErrorCall(text) {
		out = append(out, Finding{
			Severity:       SeverityMedium,
			Category:       "error_handling",
			File:           file,
			Line:           line,
			Title:          "Call result appears to be unchecked",
			Evidence:       redactSecrets(text),
			Recommendation: "Capture and inspect returned errors or document why the result can be safely ignored.",
			Confidence:     0.70,
			Source:         "rule",
			RuleID:         "go/error/unchecked-call",
		})
	}
	if osOpenRE.MatchString(text) && !strings.Contains(hunkText, ".Close()") {
		out = append(out, Finding{
			Severity:       SeverityHigh,
			Category:       "resource_lifecycle",
			File:           file,
			Line:           line,
			Title:          "Opened resource is not visibly closed",
			Evidence:       redactSecrets(text),
			Recommendation: "Close files, response bodies, rows, or DB handles with defer after checking the open error.",
			Confidence:     0.76,
			Source:         "rule",
			RuleID:         "go/resource/missing-close",
		})
	}
	if httpDoRE.MatchString(text) && hunkMentionsHTTP(hunkText) && !strings.Contains(hunkText, "Body.Close()") {
		out = append(out, Finding{
			Severity:       SeverityHigh,
			Category:       "resource_lifecycle",
			File:           file,
			Line:           line,
			Title:          "HTTP response body is not visibly closed",
			Evidence:       redactSecrets(text),
			Recommendation: "Close resp.Body with defer after checking the request error and nil response.",
			Confidence:     0.78,
			Source:         "rule",
			RuleID:         "go/resource/http-body-close",
		})
	}
	if rowsQueryRE.MatchString(text) && !strings.Contains(hunkText, ".Close()") {
		out = append(out, Finding{
			Severity:       SeverityHigh,
			Category:       "database_lifecycle",
			File:           file,
			Line:           line,
			Title:          "SQL rows are not visibly closed",
			Evidence:       redactSecrets(text),
			Recommendation: "Defer rows.Close() after checking the query error and inspect rows.Err() after iteration.",
			Confidence:     0.77,
			Source:         "rule",
			RuleID:         "go/db/rows-close",
		})
	}
	if errIgnoreRE.MatchString(text) && strings.Contains(text, "err") {
		out = append(out, Finding{
			Severity:       SeverityMedium,
			Category:       "error_handling",
			File:           file,
			Line:           line,
			Title:          "Error value appears to be ignored",
			Evidence:       redactSecrets(text),
			Recommendation: "Handle the error explicitly, return it with context, or document why it is safe to ignore.",
			Confidence:     0.74,
			Source:         "rule",
			RuleID:         "go/error/ignored-error",
		})
	}
	if dbBeginRE.MatchString(text) && !strings.Contains(hunkText, "Rollback") && !strings.Contains(hunkText, "Commit") {
		out = append(out, Finding{
			Severity:       SeverityHigh,
			Category:       "database_lifecycle",
			File:           file,
			Line:           line,
			Title:          "Transaction lifecycle is incomplete",
			Evidence:       redactSecrets(text),
			Recommendation: "Defer tx.Rollback() after Begin/BeginTx and commit only after all operations succeed.",
			Confidence:     0.80,
			Source:         "rule",
			RuleID:         "go/db/transaction-lifecycle",
		})
	}
	if dbBeginRE.MatchString(text) && !strings.Contains(hunkText, "Rollback") && strings.Contains(hunkText, "Commit") {
		out = append(out, Finding{
			Severity:       SeverityHigh,
			Category:       "database_lifecycle",
			File:           file,
			Line:           line,
			Title:          "Transaction has commit path but no visible rollback",
			Evidence:       redactSecrets(text),
			Recommendation: "Defer tx.Rollback() after Begin/BeginTx so early returns release the transaction before the final Commit.",
			Confidence:     0.78,
			Source:         "rule",
			RuleID:         "go/db/transaction-lifecycle",
		})
	}
	if strings.Contains(text, "context.Background()") && strings.Contains(text, "http.") {
		out = append(out, Finding{
			Severity:       SeverityMedium,
			Category:       "context",
			File:           file,
			Line:           line,
			Title:          "Request path uses context.Background instead of caller context",
			Evidence:       redactSecrets(text),
			Recommendation: "Thread caller context through request paths so cancellation and deadlines propagate.",
			Confidence:     0.68,
			Source:         "rule",
			RuleID:         "go/context/background-in-request",
		})
	}
	if strings.Contains(text, "context.Background()") &&
		!strings.Contains(file, "_test.go") &&
		!strings.Contains(text, "main(") {
		out = append(out, Finding{
			Severity:       SeverityMedium,
			Category:       "context",
			File:           file,
			Line:           line,
			Title:          "New code uses context.Background",
			Evidence:       redactSecrets(text),
			Recommendation: "Prefer a caller-provided context so cancellation, deadlines, and request scoped values propagate.",
			Confidence:     0.66,
			Source:         "rule",
			RuleID:         "go/context/background-in-production",
		})
	}
	if ctxCancelRE.MatchString(text) && !strings.Contains(hunkText, "cancel()") {
		out = append(out, Finding{
			Severity:       SeverityMedium,
			Category:       "context",
			File:           file,
			Line:           line,
			Title:          "Derived context cancel function is not called",
			Evidence:       redactSecrets(text),
			Recommendation: "Capture and call the cancel function, usually with defer cancel(), to release timers and child context resources.",
			Confidence:     0.78,
			Source:         "rule",
			RuleID:         "go/context/missing-cancel",
		})
	}
	if sqlExecRE.MatchString(text) && (sqlConcatRE.MatchString(text) || sqlExecutionUsesBuiltQuery(text, hunkText)) {
		out = append(out, Finding{
			Severity:       SeverityHigh,
			Category:       "security",
			File:           file,
			Line:           line,
			Title:          "SQL query appears to use string concatenation",
			Evidence:       redactSecrets(text),
			Recommendation: "Use parameterized queries and avoid concatenating table names, predicates, or user-controlled values into SQL.",
			Confidence:     0.76,
			Source:         "rule",
			RuleID:         "go/security/sql-concat",
		})
	}
	if execCmdRE.MatchString(text) && commandInvocationLooksDynamic(text, hunkText) {
		out = append(out, Finding{
			Severity:       SeverityHigh,
			Category:       "security",
			File:           file,
			Line:           line,
			Title:          "Command execution uses dynamic arguments",
			Evidence:       redactSecrets(text),
			Recommendation: "Keep command names fixed, validate dynamic arguments, and avoid shell expansion for user-controlled input.",
			Confidence:     0.70,
			Source:         "rule",
			RuleID:         "go/security/dynamic-exec-command",
		})
	}
	if mutexLockRE.MatchString(text) && !strings.Contains(hunkText, ".Unlock()") {
		out = append(out, Finding{
			Severity:       SeverityHigh,
			Category:       "concurrency",
			File:           file,
			Line:           line,
			Title:          "Mutex lock has no visible unlock path",
			Evidence:       redactSecrets(text),
			Recommendation: "Defer mu.Unlock() immediately after Lock() so error paths cannot leave the mutex locked.",
			Confidence:     0.74,
			Source:         "rule",
			RuleID:         "go/concurrency/mutex-missing-unlock",
		})
	}
	if strings.Contains(text, "defer ") && hunkLooksInLoop(hunkText) {
		out = append(out, Finding{
			Severity:       SeverityMedium,
			Category:       "resource_lifecycle",
			File:           file,
			Line:           line,
			Title:          "Defer inside loop may delay cleanup until function return",
			Evidence:       redactSecrets(text),
			Recommendation: "Extract the loop body into a helper or close the resource explicitly at the end of each iteration.",
			Confidence:     0.70,
			Source:         "rule",
			RuleID:         "go/resource/defer-in-loop",
		})
	}
	if strings.Contains(text, "panic(") && strings.Contains(hunkText, "go func") {
		out = append(out, Finding{
			Severity:       SeverityCritical,
			Category:       "error_handling",
			File:           file,
			Line:           line,
			Title:          "panic inside goroutine can crash the process",
			Evidence:       redactSecrets(text),
			Recommendation: "Return errors through a channel or recover at the goroutine boundary instead of panicking.",
			Confidence:     0.82,
			Source:         "rule",
			RuleID:         "go/error/panic-in-goroutine",
		})
	}
	if hunkContainsGoroutine(hunkText) && !hunkHasVisibleSynchronization(hunkText) && sharedMutationLooksUnsafe(text) {
		out = append(out, Finding{
			Severity:       SeverityHigh,
			Category:       "concurrency",
			File:           file,
			Line:           line,
			Title:          "Goroutine mutates shared state without visible synchronization",
			Evidence:       redactSecrets(text),
			Recommendation: "Guard shared maps, slices, counters, or struct fields with a mutex/channel or keep mutation outside the goroutine.",
			Confidence:     0.72,
			Source:         "rule",
			RuleID:         "go/concurrency/shared-mutation",
		})
	}
	return out
}

func hunkRules(file string, h DiffHunk) []Finding {
	hunkText := hunkAddedAndContext(h)
	line := firstAddedLineContaining(h, "go ")
	if line == 0 || !hunkContainsGoroutine(hunkText) {
		return nil
	}
	var out []Finding
	if goroutineLooksUnbounded(hunkText) {
		out = append(out, Finding{
			Severity:       SeverityHigh,
			Category:       "concurrency",
			File:           file,
			Line:           line,
			Title:          "Goroutine loop has no visible cancellation path",
			Evidence:       redactSecrets(firstAddedTextAtLine(h, line)),
			Recommendation: "Add a select on ctx.Done() or another bounded shutdown signal inside long-running goroutines.",
			Confidence:     0.76,
			Source:         "rule",
			RuleID:         "go/concurrency/goroutine-context",
		})
	}
	if goroutineMayBlockOnUnbufferedSend(hunkText) {
		out = append(out, Finding{
			Severity:       SeverityHigh,
			Category:       "concurrency",
			File:           file,
			Line:           line,
			Title:          "Goroutine may block on an unbuffered channel send",
			Evidence:       redactSecrets(firstAddedTextAtLine(h, line)),
			Recommendation: "Use a buffered channel, a receiver with bounded lifetime, or select on ctx.Done() around sends from goroutines.",
			Confidence:     0.72,
			Source:         "rule",
			RuleID:         "go/concurrency/goroutine-context",
		})
	}
	return out
}

func commandInvocationLooksDynamic(text string, hunkText string) bool {
	args, ok := firstCallArgs(text)
	if !ok {
		return true
	}
	if commandArgsUseOnlyLiteralBindings(args, hunkText) {
		return false
	}
	return strings.Contains(args, "+") ||
		strings.Contains(args, "fmt.Sprintf") ||
		!regexp.MustCompile(`^\s*"[^"]*"(?:\s*,\s*"[^"]*")*\s*$`).MatchString(args)
}

func uncheckedErrorCall(text string) bool {
	trimmed := strings.TrimSpace(text)
	for _, prefix := range []string{"if ", "return ", "defer ", "go ", "switch ", "case ", "default:"} {
		if strings.HasPrefix(trimmed, prefix) {
			return false
		}
	}
	open := strings.Index(trimmed, "(")
	if open <= 0 {
		return false
	}
	before := trimmed[:open]
	if strings.Contains(before, "=") || strings.Contains(before, ":") || strings.Contains(before, ",") {
		return false
	}
	call := strings.ToLower(strings.TrimSpace(before))
	for _, suffix := range []string{
		".exec", ".execcontext", ".query", ".querycontext",
		".do", "http.get", "http.post", "os.remove", "os.rename",
		"json.marshal", "json.unmarshal",
	} {
		if strings.HasSuffix(call, suffix) || call == suffix {
			return true
		}
	}
	return false
}

func firstCallArgs(text string) (string, bool) {
	start := strings.Index(text, "(")
	if start < 0 {
		return "", false
	}
	inString := false
	escaped := false
	depth := 0
	for i, r := range text[start:] {
		switch {
		case escaped:
			escaped = false
		case r == '\\' && inString:
			escaped = true
		case r == '"':
			inString = !inString
		case !inString && r == '(':
			depth++
		case !inString && r == ')':
			depth--
			if depth == 0 {
				return text[start+1 : start+i], true
			}
		}
	}
	return "", false
}

func commandArgsUseOnlyLiteralBindings(args string, hunkText string) bool {
	parts := splitCallArgs(args)
	if len(parts) == 0 {
		return false
	}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if regexp.MustCompile(`^"[^"]*"$`).MatchString(part) {
			continue
		}
		if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(part) {
			return false
		}
		if !regexp.MustCompile(`(?m)\b` + regexp.QuoteMeta(part) + `\s*(?::=|=)\s*"[^"]*"`).MatchString(hunkText) {
			return false
		}
	}
	return true
}

func splitCallArgs(args string) []string {
	var out []string
	var b strings.Builder
	inString := false
	escaped := false
	depth := 0
	for _, r := range args {
		switch {
		case escaped:
			escaped = false
		case r == '\\' && inString:
			escaped = true
		case r == '"':
			inString = !inString
		case !inString && (r == '(' || r == '[' || r == '{'):
			depth++
		case !inString && (r == ')' || r == ']' || r == '}') && depth > 0:
			depth--
		case !inString && depth == 0 && r == ',':
			out = append(out, strings.TrimSpace(b.String()))
			b.Reset()
			continue
		}
		b.WriteRune(r)
	}
	if strings.TrimSpace(b.String()) != "" {
		out = append(out, strings.TrimSpace(b.String()))
	}
	return out
}

func hunkLooksInLoop(hunkText string) bool {
	for _, line := range strings.Split(hunkText, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "for ") || trimmed == "for {" {
			return true
		}
	}
	return false
}

func hunkMentionsHTTP(hunkText string) bool {
	return strings.Contains(hunkText, "http.") ||
		strings.Contains(hunkText, "http.Client") ||
		strings.Contains(hunkText, "*http.Request") ||
		strings.Contains(hunkText, "NewRequest")
}

func hunkContainsGoroutine(hunkText string) bool {
	return strings.Contains(hunkText, "go func") || regexp.MustCompile(`(?m)^\s*go\s+`).MatchString(hunkText)
}

func goroutineLooksUnbounded(hunkText string) bool {
	if !hunkContainsGoroutine(hunkText) {
		return false
	}
	lower := strings.ToLower(hunkText)
	if !strings.Contains(lower, "for {") && !regexp.MustCompile(`(?m)^\s*for\s*(?:$|\{)`).MatchString(hunkText) {
		return false
	}
	return !strings.Contains(hunkText, ".Done()") &&
		!strings.Contains(hunkText, "ctx.Done") &&
		!strings.Contains(lower, "select")
}

func goroutineMayBlockOnUnbufferedSend(hunkText string) bool {
	if !hunkContainsGoroutine(hunkText) ||
		!regexp.MustCompile(`(?s)go\s+func[^{]*\{.*\b[A-Za-z_][A-Za-z0-9_]*\s*<-`).MatchString(hunkText) {
		return false
	}
	return regexp.MustCompile(`make\s*\(\s*chan\s+[^,\)]+\)`).MatchString(hunkText)
}

func hunkHasVisibleSynchronization(hunkText string) bool {
	return strings.Contains(hunkText, ".Lock()") ||
		strings.Contains(hunkText, ".Unlock()") ||
		strings.Contains(hunkText, "sync.") ||
		strings.Contains(hunkText, "atomic.") ||
		strings.Contains(hunkText, "make(chan") ||
		strings.Contains(hunkText, "<-") ||
		regexp.MustCompile(`\bchan\s+`).MatchString(hunkText)
}

func sharedMutationLooksUnsafe(text string) bool {
	return mapWriteRE.MatchString(text) ||
		strings.Contains(text, "append(") ||
		regexp.MustCompile(`\b[A-Za-z0-9_]+\s*(\+\+|--|\+=|-=)`).MatchString(text)
}

func sqlExecutionUsesBuiltQuery(text string, hunkText string) bool {
	for _, name := range []string{"query", "sql", "stmt"} {
		if regexp.MustCompile(`\b`+name+`\b`).MatchString(text) &&
			regexp.MustCompile(`(?m)\b`+name+`\s*:=.*(\+|fmt\.Sprintf)`).MatchString(hunkText) {
			return true
		}
	}
	return false
}

func textContainsCredentialLiteral(text string) bool {
	lower := strings.ToLower(text)
	return authHeaderRE.MatchString(text) ||
		strings.Contains(lower, "-----begin") && strings.Contains(lower, "private key") ||
		strings.Contains(lower, "akia") ||
		strings.Contains(lower, "aiza") ||
		strings.Contains(lower, "eyj") ||
		strings.Contains(lower, "ghp_") ||
		strings.Contains(lower, "github_pat_") ||
		strings.Contains(lower, "glpat-") ||
		strings.Contains(lower, "xoxb-") ||
		strings.Contains(lower, "xoxa-") ||
		strings.Contains(lower, "sg.") ||
		strings.Contains(lower, "sk-") ||
		strings.Contains(lower, "sk_live_") ||
		strings.Contains(lower, "rk_live_") ||
		strings.Contains(lower, "npm_")
}

func looksHighEntropySecretLiteral(text string) bool {
	for _, match := range stringLitRE.FindAllStringSubmatch(text, -1) {
		if highEntropyToken(match[1]) {
			return true
		}
	}
	return false
}

func highEntropyToken(token string) bool {
	if len(token) < 32 || strings.Contains(token, " ") {
		return false
	}
	var lower, upper, digit, symbol bool
	for _, r := range token {
		switch {
		case r >= 'a' && r <= 'z':
			lower = true
		case r >= 'A' && r <= 'Z':
			upper = true
		case r >= '0' && r <= '9':
			digit = true
		case strings.ContainsRune("_-+/=.", r):
			symbol = true
		default:
			return false
		}
	}
	return (lower && upper && digit && len(token) >= 36) ||
		(lower && digit && symbol && len(token) >= 40) ||
		(upper && digit && symbol && len(token) >= 40)
}

func hunkAddedAndContext(h DiffHunk) string {
	var b strings.Builder
	for _, l := range h.Lines {
		if l.Kind != '-' {
			b.WriteString(l.Text)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func firstAddedLineContaining(h DiffHunk, needle string) int {
	for _, l := range h.Lines {
		if l.Kind == '+' && strings.Contains(l.Text, needle) {
			return l.NewLine
		}
	}
	for _, l := range h.Lines {
		if l.Kind == '+' {
			return l.NewLine
		}
	}
	return 0
}

func firstAddedTextAtLine(h DiffHunk, line int) string {
	for _, l := range h.Lines {
		if l.Kind == '+' && l.NewLine == line {
			return strings.TrimSpace(l.Text)
		}
	}
	return ""
}

func missingTestFindings(pd ParsedDiff) []Finding {
	changedGo := map[string]bool{}
	testChanged := false
	for _, f := range pd.Files {
		if !f.IsGo {
			continue
		}
		if f.IsTest {
			testChanged = true
			continue
		}
		changedGo[f.NewPath] = true
	}
	if len(changedGo) == 0 || testChanged {
		return nil
	}
	out := make([]Finding, 0, len(changedGo))
	for file := range changedGo {
		out = append(out, Finding{
			Severity:       SeverityLow,
			Category:       "test_coverage",
			File:           file,
			Line:           1,
			Title:          "Production Go change has no accompanying test change",
			Evidence:       "No *_test.go file changed in this diff.",
			Recommendation: "Add or update focused tests for changed behavior, especially error and lifecycle paths.",
			Confidence:     0.64,
			Source:         "rule",
			RuleID:         "go/test/missing-test-change",
		})
	}
	return out
}
