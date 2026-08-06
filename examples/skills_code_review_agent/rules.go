//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

const findingConfidenceThreshold = 0.80

var (
	shellCommandPattern = regexp.MustCompile(
		`\bexec\.Command(?:Context)?\([^,\n]+,\s*["']-c["']`,
	)
	sqlInterpolationPattern = regexp.MustCompile(
		`(?i)(fmt\.Sprintf\(\s*["'][^"']*(?:SELECT|INSERT|UPDATE|DELETE)|["'][^"']*(?:SELECT|INSERT|UPDATE|DELETE)[^"']*["']\s*\+)`,
	)
	goroutinePattern = regexp.MustCompile(
		`\bgo\s+(?:func\s*\(|[A-Za-z_][A-Za-z0-9_.]*\s*\()`,
	)
	resourceOpenPattern = regexp.MustCompile(
		`^\s*([A-Za-z_][A-Za-z0-9_]*)\s*(?:,\s*[A-Za-z_][A-Za-z0-9_]*)?\s*:=\s*(?:os\.(?:Open|Create)|http\.(?:Get|Post))\s*\(`,
	)
	rowsOpenPattern = regexp.MustCompile(
		`^\s*([A-Za-z_][A-Za-z0-9_]*)\s*(?:,\s*[A-Za-z_][A-Za-z0-9_]*)?\s*:=\s*(?:[A-Za-z_][A-Za-z0-9_.]*\.)?Query(?:Context)?\s*\(`,
	)
	dbOpenPattern = regexp.MustCompile(
		`^\s*([A-Za-z_][A-Za-z0-9_]*)\s*(?:,\s*[A-Za-z_][A-Za-z0-9_]*)?\s*:=\s*sql\.Open\s*\(`,
	)
	transactionPattern = regexp.MustCompile(
		`^\s*([A-Za-z_][A-Za-z0-9_]*)\s*(?:,\s*[A-Za-z_][A-Za-z0-9_]*)?\s*:=\s*(?:[A-Za-z_][A-Za-z0-9_.]*\.)?Begin(?:Tx)?\s*\(`,
	)
	contextCancelPattern = regexp.MustCompile(
		`^\s*[A-Za-z_][A-Za-z0-9_]*\s*,\s*([A-Za-z_][A-Za-z0-9_]*)\s*:=\s*context\.With(?:Cancel|Timeout|Deadline)\s*\(`,
	)
	swallowedErrorPattern = regexp.MustCompile(
		`if\s+err\s*!=\s*nil\s*\{[^}]*return\s+(?:nil|0|false|""|'')\s*(?:[,}]|$)`,
	)
	ignoredErrorPattern = regexp.MustCompile(
		`(?:^|\s)(?:_,\s*)?_\s*(?::=|=)\s*(?:json\.(?:Marshal|Unmarshal)|io\.Copy|os\.(?:Remove|WriteFile)|[A-Za-z_][A-Za-z0-9_.]*\.(?:Commit|Rollback|Close))\s*\(`,
	)
)

// AnalyzeDiff applies deterministic Go review rules and separates uncertain
// results from high-confidence findings.
func AnalyzeDiff(diff ParsedDiff) ([]Finding, []Finding) {
	var all []Finding
	changedTests := changedTestFiles(diff)
	for _, file := range diff.Files {
		contextText := fileContext(file)
		for _, hunk := range file.Hunks {
			all = append(all, analyzeHunk(file, hunk, contextText)...)
		}
		if missingTestCoverage(file, changedTests) {
			all = append(all, newFinding(
				severityLow,
				"testing",
				file.Path,
				firstAddedLine(file),
				"Changed Go code has no matching test change",
				"No _test.go file changed in the same package.",
				"Add or update focused tests for the changed behavior.",
				0.62,
				"TST001",
			))
		}
	}
	all = DeduplicateFindings(all)
	var findings, warnings []Finding
	for _, finding := range all {
		if finding.Confidence >= findingConfidenceThreshold {
			findings = append(findings, finding)
		} else {
			warnings = append(warnings, finding)
		}
	}
	return findings, warnings
}

func analyzeHunk(
	file ChangedFile,
	hunk Hunk,
	contextText string,
) []Finding {
	var findings []Finding
	for _, line := range hunk.Lines {
		if line.Kind != '+' {
			continue
		}
		evidence := Redact(strings.TrimSpace(line.Text))
		if ContainsSecret(line.Text) {
			findings = append(findings, newFinding(
				severityCritical, "sensitive_information", file.Path,
				line.NewLine, "Hard-coded credential in changed code",
				evidence,
				"Remove the credential, rotate it, and load it from an approved secret store.",
				0.99, "SEC001",
			))
		}
		if shellCommandPattern.MatchString(line.Text) {
			findings = append(findings, newFinding(
				severityHigh, "security", file.Path, line.NewLine,
				"Shell command can execute untrusted input",
				evidence,
				"Avoid a shell and pass validated arguments directly to exec.CommandContext.",
				0.94, "SEC002",
			))
		}
		if sqlInterpolationPattern.MatchString(line.Text) {
			findings = append(findings, newFinding(
				severityHigh, "security", file.Path, line.NewLine,
				"SQL statement is built by string interpolation",
				evidence,
				"Use a parameterized query and pass values separately.",
				0.93, "SEC003",
			))
		}
		if goroutinePattern.MatchString(line.Text) &&
			!hasGoroutineTermination(contextText) {
			confidence := goroutineConfidence(contextText, line.Text)
			findings = append(findings, newFinding(
				severityHigh, "concurrency", file.Path, line.NewLine,
				"Goroutine has no visible cancellation or join path",
				evidence,
				"Bind the goroutine to context cancellation and ensure its owner waits for shutdown.",
				confidence, "CON001",
			))
		}
		if match := contextCancelPattern.FindStringSubmatch(line.Text); len(match) == 2 &&
			!hasCloseCall(contextText, match[1]) {
			findings = append(findings, newFinding(
				severityHigh, "context_lifecycle", file.Path, line.NewLine,
				"Context cancellation function is not called",
				evidence,
				fmt.Sprintf("Call defer %s() immediately after checking constructor errors.", match[1]),
				0.94, "CON002",
			))
		}
		if match := resourceOpenPattern.FindStringSubmatch(line.Text); len(match) == 2 &&
			!hasCloseCall(contextText, match[1]) {
			findings = append(findings, newFinding(
				severityHigh, "resource_lifecycle", file.Path, line.NewLine,
				"Opened resource is not closed",
				evidence,
				fmt.Sprintf("Close %s on every path, normally with defer after the error check.", match[1]),
				0.95, "RES001",
			))
		}
		if match := transactionPattern.FindStringSubmatch(line.Text); len(match) == 2 &&
			!strings.Contains(contextText, match[1]+".Rollback(") {
			findings = append(findings, newFinding(
				severityHigh, "database_lifecycle", file.Path, line.NewLine,
				"Transaction has no rollback guard",
				evidence,
				fmt.Sprintf("Defer %s.Rollback() after Begin succeeds; commit only after all operations succeed.", match[1]),
				0.96, "DB001",
			))
		}
		if match := rowsOpenPattern.FindStringSubmatch(line.Text); len(match) == 2 &&
			!hasCloseCall(contextText, match[1]) {
			findings = append(findings, newFinding(
				severityHigh, "database_lifecycle", file.Path, line.NewLine,
				"Database rows are not closed",
				evidence,
				fmt.Sprintf("Call defer %s.Close() after checking the query error.", match[1]),
				0.96, "DB002",
			))
		}
		if match := dbOpenPattern.FindStringSubmatch(line.Text); len(match) == 2 &&
			!hasCloseCall(contextText, match[1]) {
			findings = append(findings, newFinding(
				severityHigh, "database_lifecycle", file.Path, line.NewLine,
				"Database handle has no explicit lifecycle",
				evidence,
				fmt.Sprintf("Assign ownership of %s and close it during application shutdown.", match[1]),
				0.92, "DB003",
			))
		}
		if (strings.Contains(line.Text, "if err") &&
			swallowedErrorPattern.MatchString(contextText)) ||
			ignoredErrorPattern.MatchString(line.Text) {
			findings = append(findings, newFinding(
				severityMedium, "error_handling", file.Path, line.NewLine,
				"Error is discarded or converted to success",
				evidence,
				"Handle the error or return it with operation context using fmt.Errorf and %w.",
				0.90, "ERR001",
			))
		}
	}
	return findings
}

func newFinding(
	severity, category, file string,
	line int,
	title, evidence, recommendation string,
	confidence float64,
	ruleID string,
) Finding {
	return Finding{
		Severity:       severity,
		Category:       category,
		File:           file,
		Line:           line,
		Title:          title,
		Evidence:       Redact(evidence),
		Recommendation: recommendation,
		Confidence:     confidence,
		Source:         sourceRule,
		RuleID:         ruleID,
	}
}

func hunkContext(hunk Hunk) string {
	var lines []string
	for _, line := range hunk.Lines {
		if line.Kind != '-' {
			lines = append(lines, line.Text)
		}
	}
	return strings.Join(lines, "\n")
}

func fileContext(file ChangedFile) string {
	var hunks []string
	for _, hunk := range file.Hunks {
		hunks = append(hunks, hunkContext(hunk))
	}
	return strings.Join(hunks, "\n")
}

func hasGoroutineTermination(text string) bool {
	markers := []string{
		"ctx.Done()", "<-done", "errgroup.", ".Wait()", "WaitGroup", "close(",
	}
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func goroutineConfidence(contextText, line string) float64 {
	highRiskMarkers := []string{
		"for {", "time.NewTicker", "ListenAndServe", "runForever",
		"serveForever", "range ticker", "<-",
	}
	combined := contextText + "\n" + line
	for _, marker := range highRiskMarkers {
		if strings.Contains(combined, marker) {
			return 0.90
		}
	}
	return 0.72
}

func hasCloseCall(text, variable string) bool {
	return strings.Contains(text, variable+".Close()") ||
		strings.Contains(text, variable+"()")
}

func changedTestFiles(diff ParsedDiff) map[string]bool {
	result := make(map[string]bool)
	for _, file := range diff.Files {
		if strings.HasSuffix(file.Path, "_test.go") {
			result[path.Dir(file.Path)] = true
		}
	}
	return result
}

func missingTestCoverage(file ChangedFile, tests map[string]bool) bool {
	return strings.HasSuffix(file.Path, ".go") &&
		!strings.HasSuffix(file.Path, "_test.go") &&
		len(file.Hunks) > 0 &&
		firstAddedLine(file) > 0 &&
		!tests[path.Dir(file.Path)]
}

func firstAddedLine(file ChangedFile) int {
	for _, hunk := range file.Hunks {
		for _, line := range hunk.Lines {
			if line.Kind == '+' {
				return line.NewLine
			}
		}
	}
	return 0
}

// DeduplicateFindings keeps the strongest result per file, line, and category.
func DeduplicateFindings(findings []Finding) []Finding {
	unique := make(map[string]Finding)
	for _, finding := range findings {
		key := fmt.Sprintf("%s\x00%d\x00%s",
			finding.File, finding.Line, finding.Category)
		current, exists := unique[key]
		if !exists || strongerFinding(finding, current) {
			unique[key] = finding
		}
	}
	result := make([]Finding, 0, len(unique))
	for _, finding := range unique {
		result = append(result, finding)
	}
	sort.Slice(result, func(i, j int) bool {
		if severityRank(result[i].Severity) != severityRank(result[j].Severity) {
			return severityRank(result[i].Severity) > severityRank(result[j].Severity)
		}
		if result[i].File != result[j].File {
			return result[i].File < result[j].File
		}
		if result[i].Line != result[j].Line {
			return result[i].Line < result[j].Line
		}
		return result[i].Category < result[j].Category
	})
	return result
}

func strongerFinding(left, right Finding) bool {
	if severityRank(left.Severity) != severityRank(right.Severity) {
		return severityRank(left.Severity) > severityRank(right.Severity)
	}
	return left.Confidence > right.Confidence
}

func severityRank(severity string) int {
	switch severity {
	case severityCritical:
		return 4
	case severityHigh:
		return 3
	case severityMedium:
		return 2
	case severityLow:
		return 1
	default:
		return 0
	}
}
