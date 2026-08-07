//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner

import (
	"regexp"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/finding"
)

// Common patterns for Go tool output.
var (
	// VetOutputPattern matches "file:line:col: message" format.
	vetOutputPattern = regexp.MustCompile(`^([^:]+):(\d+):(\d+):\s*(.+)$`)
	// VetOutputPatternNoCol matches "file:line: message" format.
	vetOutputPatternNoCol = regexp.MustCompile(`^([^:]+):(\d+):\s*(.+)$`)
	// TestOutputPattern matches "--- FAIL: TestName" and location.
	testFailPattern = regexp.MustCompile(`^\s*---\s*FAIL:\s*Test(\w+)\s*\(`)
	// TestFileLocation matches "file.go:25:" inside test output.
	testFileLocation = regexp.MustCompile(`^([^:]+):(\d+):\s*(.+)$`)
)

// GoVetOutput represents a single parsed go vet diagnostic.
type GoVetOutput struct {
	File    string
	Line    int
	Column  int
	Message string
}

// ParseGoVetOutput parses go vet output into structured findings.
// go vet output format: file:line:col: message
func ParseGoVetOutput(output string, taskID string) []finding.Finding {
	var findings []finding.Finding
	lines := strings.Split(output, "\n")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// Skip non-diagnostic lines.
		if !isDiagnosticLine(trimmed) {
			continue
		}

		if d := parseVetLine(trimmed); d != nil {
			f := finding.Finding{
				Severity:       finding.SeverityWarning,
				Category:       finding.CategoryBestPractice,
				File:           d.File,
				Line:           d.Line,
				Column:         d.Column,
				Title:          d.Message,
				Evidence:       trimmed,
				Recommendation: "Review the go vet diagnostic and fix the issue",
				Confidence:     finding.ConfidenceHigh,
				Source:         finding.SourceGoVet,
				RuleID:         "GO_VET_DIAGNOSTIC",
			}
			if d.File != "" {
				findings = append(findings, f)
			}
		}
	}
	return findings
}

func parseVetLine(line string) *GoVetOutput {
	if m := vetOutputPattern.FindStringSubmatch(line); len(m) >= 5 {
		lineNum, _ := strconv.Atoi(m[2])
		colNum, _ := strconv.Atoi(m[3])
		return &GoVetOutput{
			File:    stripDotSlash(m[1]),
			Line:    lineNum,
			Column:  colNum,
			Message: strings.TrimSpace(m[4]),
		}
	}
	if m := vetOutputPatternNoCol.FindStringSubmatch(line); len(m) >= 4 {
		lineNum, _ := strconv.Atoi(m[2])
		return &GoVetOutput{
			File:    stripDotSlash(m[1]),
			Line:    lineNum,
			Column:  0,
			Message: strings.TrimSpace(m[3]),
		}
	}
	return nil
}

// stripDotSlash removes a leading "./" prefix from a file path if present.
func stripDotSlash(path string) string {
	if len(path) >= 2 && path[:2] == "./" {
		return path[2:]
	}
	return path
}

// ParseStaticcheckOutput parses staticcheck output into findings.
// staticcheck uses the same file:line:col: message format as go vet.
func ParseStaticcheckOutput(output string, taskID string) []finding.Finding {
	findings := ParseGoVetOutput(output, taskID)
	// Override source to staticcheck.
	for i := range findings {
		findings[i].Source = finding.SourceStaticcheck
		findings[i].RuleID = "STATICCHECK_DIAGNOSTIC"
	}
	return findings
}

// ParseGoTestOutput parses go test output and extracts failures as findings.
func ParseGoTestOutput(output string, taskID string) []finding.Finding {
	var findings []finding.Finding
	lines := strings.Split(output, "\n")

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// Detect test failure header.
		if testFailPattern.MatchString(trimmed) {
			testName := testFailPattern.FindStringSubmatch(trimmed)[1]

			// Look for file/line info in the subsequent lines.
			var file, evidence string
			lineNum := 0
			for j := i + 1; j < len(lines) && j < i+10; j++ {
				nextLine := strings.TrimSpace(lines[j])
				if m := testFileLocation.FindStringSubmatch(nextLine); len(m) >= 4 {
					file = m[1]
					lineNum, _ = strconv.Atoi(m[2])
					evidence = strings.TrimSpace(m[3])
					break
				}
				// Stop at next test function or end block.
				if strings.HasPrefix(nextLine, "---") || nextLine == "" {
					break
				}
			}

			findings = append(findings, finding.Finding{
				Severity:       finding.SeverityHigh,
				Category:       finding.CategoryBestPractice,
				File:           file,
				Line:           lineNum,
				Title:          "Test failed: " + testName,
				Evidence:       evidence,
				Recommendation: "Investigate and fix the failing test",
				Confidence:     finding.ConfidenceHigh,
				Source:         finding.SourceGoVet,
				RuleID:         "GO_TEST_FAILURE",
			})
		}
	}
	return findings
}

// isDiagnosticLine returns true if the line looks like a compiler/vet diagnostic
// (starts with a file path pattern, not a command or progress message).
func isDiagnosticLine(line string) bool {
	// Skip progress and command output (but not file:line diagnostics).
	if strings.HasPrefix(line, "# ") || strings.HasPrefix(line, "ok ") ||
		strings.HasPrefix(line, "? ") || strings.HasPrefix(line, "FAIL") ||
		strings.HasPrefix(line, "go: ") || strings.HasPrefix(line, "---") {
		return false
	}
	// Must look like a file:line diagnostic.
	return vetOutputPattern.MatchString(line) || vetOutputPatternNoCol.MatchString(line)
}
