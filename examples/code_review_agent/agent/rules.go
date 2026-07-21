package agent

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type RuleEngine struct {
	redactor Redactor
}

func NewRuleEngine(redactor Redactor) RuleEngine {
	return RuleEngine{redactor: redactor}
}

var (
	secretLineRE        = regexp.MustCompile(`(?i)(api[_-]?key|access[_-]?token|auth[_-]?token|token|secret|password|passwd|pwd)\s*(?::=|=|:)`)
	dangerousSQLRE      = regexp.MustCompile(`(?i)(Query|QueryContext|Exec|ExecContext)\s*\([^\n]*(fmt\.Sprintf|\+\s*[A-Za-z0-9_])`)
	sqlStringBuildRE    = regexp.MustCompile(`(?i)(:=|=)\s*fmt\.Sprintf\s*\(\s*"\s*(select|insert|update|delete|with)\b|(:=|=)\s*"\s*(select|insert|update|delete|with)\b[^\n"]*"\s*\+`)
	goroutineRE         = regexp.MustCompile(`\bgo\s+(func\s*\(|[A-Za-z0-9_\.]+\()`)
	resourceOpenRE      = regexp.MustCompile(`\b(os\.Open|os\.Create|http\.Get|http\.Post|sql\.Open|template\.ParseFiles)\s*\(`)
	rowsQueryRE         = regexp.MustCompile(`\b(Query|QueryContext)\s*\(`)
	txBeginRE           = regexp.MustCompile(`\b(Begin|BeginTx)\s*\(`)
	ignoredErrorRE      = regexp.MustCompile(`(^|[^A-Za-z0-9_])_\s*:?=|,\s*_\s*:?=`)
	functionAddRE       = regexp.MustCompile(`^\s*func\s+[A-Za-z0-9_]+\s*\(`)
	contextBackgroundRE = regexp.MustCompile(`context\.(Background|TODO)\s*\(`)
)

func (e RuleEngine) Analyze(input ReviewInput) []Finding {
	var findings []Finding
	for _, file := range input.Files {
		if !strings.HasSuffix(file.NewPath, ".go") {
			continue
		}
		for _, hunk := range file.Hunks {
			findings = append(findings, e.analyzeHunk(file, hunk)...)
		}
	}
	findings = append(findings, e.missingTestWarnings(input)...)
	return findings
}

func (e RuleEngine) analyzeHunk(file ChangedFile, hunk Hunk) []Finding {
	var findings []Finding
	all := hunkText(hunk, true)
	added := addedLines(hunk)
	for _, line := range added {
		trimmed := strings.TrimSpace(line.Content)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}

		if secretLineRE.MatchString(trimmed) || strings.Contains(trimmed, "AKIA") || strings.Contains(trimmed, "ghp_") {
			findings = append(findings, Finding{
				Severity:       SeverityCritical,
				Category:       "sensitive_information",
				File:           file.NewPath,
				Line:           line.NewLine,
				Title:          "Hard-coded secret or credential introduced",
				Evidence:       e.redactor.Redact(trimmed),
				Recommendation: "Move the value to a secret manager or environment variable, rotate the leaked credential, and add a regression test or secret scanning rule.",
				Confidence:     0.99,
				Source:         "skill:code-review/rules/security",
				RuleID:         "GO-SEC-001",
			})
		}

		if dangerousSQLRE.MatchString(trimmed) || sqlStringBuildRE.MatchString(trimmed) {
			findings = append(findings, Finding{
				Severity:       SeverityHigh,
				Category:       "security",
				File:           file.NewPath,
				Line:           line.NewLine,
				Title:          "SQL statement is built with string formatting or concatenation",
				Evidence:       e.redactor.Redact(trimmed),
				Recommendation: "Use parameterized QueryContext or ExecContext arguments instead of interpolating user-controlled values into SQL.",
				Confidence:     0.86,
				Source:         "skill:code-review/rules/security",
				RuleID:         "GO-SEC-002",
			})
		}

		if goroutineRE.MatchString(trimmed) && !strings.Contains(all, "ctx.Done()") && !strings.Contains(all, "context.Context") {
			findings = append(findings, Finding{
				Severity:       SeverityMedium,
				Category:       "goroutine_context_lifecycle",
				File:           file.NewPath,
				Line:           line.NewLine,
				Title:          "Goroutine has no visible cancellation path",
				Evidence:       e.redactor.Redact(trimmed),
				Recommendation: "Pass context.Context into the goroutine and stop work on ctx.Done(), or document why the goroutine is process-scoped.",
				Confidence:     0.78,
				Source:         "skill:code-review/rules/concurrency",
				RuleID:         "GO-CONC-001",
			})
		}

		if strings.Contains(trimmed, "time.Tick(") {
			findings = append(findings, Finding{
				Severity:       SeverityHigh,
				Category:       "goroutine_context_lifecycle",
				File:           file.NewPath,
				Line:           line.NewLine,
				Title:          "time.Tick cannot be stopped and may leak",
				Evidence:       e.redactor.Redact(trimmed),
				Recommendation: "Use time.NewTicker, defer ticker.Stop(), and select on ctx.Done() for request-scoped or job-scoped loops.",
				Confidence:     0.91,
				Source:         "skill:code-review/rules/concurrency",
				RuleID:         "GO-CONC-002",
			})
		}

		if contextBackgroundRE.MatchString(trimmed) && looksRequestScoped(all) {
			findings = append(findings, Finding{
				Severity:       SeverityMedium,
				Category:       "context_lifecycle",
				File:           file.NewPath,
				Line:           line.NewLine,
				Title:          "Request-scoped code creates a detached context",
				Evidence:       e.redactor.Redact(trimmed),
				Recommendation: "Propagate the caller request context instead of context.Background or context.TODO so cancellation and deadlines are preserved.",
				Confidence:     0.74,
				Source:         "skill:code-review/rules/concurrency",
				RuleID:         "GO-CONC-003",
			})
		}

		if resourceOpenRE.MatchString(trimmed) && !hasCloseNear(all, trimmed) {
			findings = append(findings, Finding{
				Severity:       SeverityHigh,
				Category:       "resource_lifecycle",
				File:           file.NewPath,
				Line:           line.NewLine,
				Title:          "Opened resource has no close path in the changed hunk",
				Evidence:       e.redactor.Redact(trimmed),
				Recommendation: "Close files, response bodies, databases, or parsed resources on every path. Prefer defer close immediately after nil-error checks.",
				Confidence:     0.82,
				Source:         "skill:code-review/rules/resources",
				RuleID:         "GO-RES-001",
			})
		}

		if rowsQueryRE.MatchString(trimmed) && strings.Contains(trimmed, ":=") && !strings.Contains(all, "rows.Close()") && !strings.Contains(all, ".Close()") {
			findings = append(findings, Finding{
				Severity:       SeverityHigh,
				Category:       "database_lifecycle",
				File:           file.NewPath,
				Line:           line.NewLine,
				Title:          "Query rows may not be closed",
				Evidence:       e.redactor.Redact(trimmed),
				Recommendation: "Call defer rows.Close() after checking err, then inspect rows.Err() after iteration.",
				Confidence:     0.84,
				Source:         "skill:code-review/rules/database",
				RuleID:         "GO-DB-001",
			})
		}

		if txBeginRE.MatchString(trimmed) && !strings.Contains(all, "Rollback()") && !strings.Contains(all, "Commit()") {
			findings = append(findings, Finding{
				Severity:       SeverityHigh,
				Category:       "database_lifecycle",
				File:           file.NewPath,
				Line:           line.NewLine,
				Title:          "Transaction has no visible commit or rollback",
				Evidence:       e.redactor.Redact(trimmed),
				Recommendation: "Defer tx.Rollback() after BeginTx succeeds and return tx.Commit() only after all writes complete.",
				Confidence:     0.87,
				Source:         "skill:code-review/rules/database",
				RuleID:         "GO-DB-002",
			})
		}

		if ignoredErrorRE.MatchString(trimmed) && mightReturnError(trimmed) {
			findings = append(findings, Finding{
				Severity:       SeverityMedium,
				Category:       "error_handling",
				File:           file.NewPath,
				Line:           line.NewLine,
				Title:          "Returned error is ignored",
				Evidence:       e.redactor.Redact(trimmed),
				Recommendation: "Handle the error, wrap it with context, or justify why it is safe to ignore in a targeted comment.",
				Confidence:     0.79,
				Source:         "skill:code-review/rules/error-handling",
				RuleID:         "GO-ERR-001",
			})
		}
	}
	return findings
}

func (e RuleEngine) missingTestWarnings(input ReviewInput) []Finding {
	changedGo := false
	testChanged := false
	firstFile := ""
	firstLine := 1
	addsFunction := false
	for _, file := range input.Files {
		if !strings.HasSuffix(file.NewPath, ".go") {
			continue
		}
		if strings.HasSuffix(file.NewPath, "_test.go") {
			testChanged = true
			continue
		}
		changedGo = true
		if firstFile == "" {
			firstFile = file.NewPath
		}
		for _, h := range file.Hunks {
			for _, line := range h.Lines {
				if line.Kind == "add" {
					if firstLine == 1 {
						firstLine = line.NewLine
					}
					if functionAddRE.MatchString(line.Content) {
						addsFunction = true
					}
				}
			}
		}
	}
	if !changedGo || testChanged || firstFile == "" || !addsFunction {
		return nil
	}
	return []Finding{{
		Severity:       SeverityLow,
		Category:       "test_coverage",
		File:           firstFile,
		Line:           firstLine,
		Title:          "Production Go behavior changed without a test update",
		Evidence:       "Go function added or changed, but no *_test.go file appears in the diff.",
		Recommendation: "Add a focused unit or integration test covering the changed behavior, or mark the PR for human review if test coverage is intentionally deferred.",
		Confidence:     0.58,
		Source:         "skill:code-review/rules/testing",
		RuleID:         "GO-TEST-001",
		NeedsHuman:     true,
	}}
}

func DeduplicateAndTriage(raw []Finding) (findings []Finding, warnings []Finding, needsHuman []Finding) {
	best := map[string]Finding{}
	for _, f := range raw {
		key := fmt.Sprintf("%s:%d:%s", f.File, f.Line, f.Category)
		current, ok := best[key]
		if !ok || findingRank(f) > findingRank(current) || (findingRank(f) == findingRank(current) && f.Confidence > current.Confidence) {
			best[key] = f
		}
	}
	merged := make([]Finding, 0, len(best))
	for _, f := range best {
		merged = append(merged, f)
	}
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].File != merged[j].File {
			return merged[i].File < merged[j].File
		}
		if merged[i].Line != merged[j].Line {
			return merged[i].Line < merged[j].Line
		}
		return merged[i].RuleID < merged[j].RuleID
	})
	for _, f := range merged {
		if f.NeedsHuman || f.Confidence < 0.65 {
			needsHuman = append(needsHuman, f)
			continue
		}
		if f.Confidence < 0.72 || f.Severity == SeverityInfo {
			warnings = append(warnings, f)
			continue
		}
		findings = append(findings, f)
	}
	return findings, warnings, needsHuman
}

func hunkText(h Hunk, includeDeleted bool) string {
	parts := make([]string, 0, len(h.Lines))
	for _, line := range h.Lines {
		if line.Kind == "delete" && !includeDeleted {
			continue
		}
		parts = append(parts, line.Content)
	}
	return strings.Join(parts, "\n")
}

func addedLines(h Hunk) []DiffLine {
	out := make([]DiffLine, 0, len(h.Lines))
	for _, line := range h.Lines {
		if line.Kind == "add" {
			out = append(out, line)
		}
	}
	return out
}

func hasCloseNear(hunk, line string) bool {
	if strings.Contains(line, "defer") && strings.Contains(line, "Close()") {
		return true
	}
	return strings.Contains(hunk, "Close()") || strings.Contains(hunk, "defer resp.Body.Close()") || strings.Contains(hunk, "defer db.Close()")
}

func mightReturnError(line string) bool {
	candidates := []string{"os.", "http.", "json.", "strconv.", "db.", "tx.", "Query", "Exec", "Open", "Create", "Read", "Write"}
	for _, c := range candidates {
		if strings.Contains(line, c) {
			return true
		}
	}
	return strings.Contains(line, "err")
}

func looksRequestScoped(hunk string) bool {
	return strings.Contains(hunk, "http.ResponseWriter") || strings.Contains(hunk, "*http.Request") || strings.Contains(hunk, "gin.Context") || strings.Contains(hunk, ".Request.Context()")
}

func findingRank(f Finding) int {
	severity := map[string]int{SeverityInfo: 1, SeverityLow: 2, SeverityMedium: 3, SeverityHigh: 4, SeverityCritical: 5}[f.Severity]
	return severity*100 + int(f.Confidence*100)
}
