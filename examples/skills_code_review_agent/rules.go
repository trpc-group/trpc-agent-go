//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Finding represents a single code review finding.
type Finding struct {
	ID             string  `json:"id"`
	TaskID         string  `json:"task_id"`
	Severity       string  `json:"severity"` // "high", "medium", "low", "warning"
	Category       string  `json:"category"`
	File           string  `json:"file"`
	Line           int     `json:"line"`
	Title          string  `json:"title"`
	Evidence       string  `json:"evidence"`
	Recommendation string  `json:"recommendation"`
	Confidence     float64 `json:"confidence"`
	Source         string  `json:"source"`  // "static_rule", "skill", "sandbox"
	RuleID         string  `json:"rule_id"`
}

var (
	secretRegex       = regexp.MustCompile(`(?i)(api[_-]?key|secret|password|passwd|token|private[_-]?key)\s*(?::=|=)\s*["']([^"']{8,})["']`)
	goroutineRegex    = regexp.MustCompile(`\bgo\s+func\s*\(`)
	ignoredErrRegex   = regexp.MustCompile(`_\s*,\s*err\s*:=|_\s*=\s*.*\(.*\)`)
	dbTxRegex         = regexp.MustCompile(`\bBeginTx\(|\bBegin\(`)
	exportedFuncRegex = regexp.MustCompile(`^func\s+([A-Z][a-zA-Z0-9_]*)\s*\(`)
)

// AnalyzeFileChanges applies all static rules to parsed file changes.
func AnalyzeFileChanges(taskID, repoPath string, changes []FileChange) []Finding {
	var rawFindings []Finding

	for _, file := range changes {
		// Rule 5: Check missing test coverage
		if strings.HasSuffix(file.NewPath, ".go") && !strings.HasSuffix(file.NewPath, "_test.go") {
			hasTestFile := false
			testPath := strings.TrimSuffix(file.NewPath, ".go") + "_test.go"
			for _, other := range changes {
				if other.NewPath == testPath {
					hasTestFile = true
					break
				}
			}
			if !hasTestFile && repoPath != "" {
				fullTestPath := filepath.Join(repoPath, testPath)
				if _, err := os.Stat(fullTestPath); err == nil {
					hasTestFile = true
				}
			}

			if !hasTestFile {
				for _, hunk := range file.Hunks {
					for _, line := range hunk.Lines {
						if line.Type == "+" && exportedFuncRegex.MatchString(line.Content) {
							matches := exportedFuncRegex.FindStringSubmatch(line.Content)
							funcName := matches[1]
							rawFindings = append(rawFindings, Finding{
								TaskID:         taskID,
								Severity:       "medium",
								Category:       "Missing Test",
								File:           file.NewPath,
								Line:           line.NewLine,
								Title:          fmt.Sprintf("Exported function '%s' added without unit test file", funcName),
								Evidence:       strings.TrimSpace(line.Content),
								Recommendation: fmt.Sprintf("Add unit test for %s in %s", funcName, testPath),
								Confidence:     0.85,
								Source:         "static_rule",
								RuleID:         "GOP-005",
							})
						}
					}
				}
			}
		}

		for _, hunk := range file.Hunks {
			for _, line := range hunk.Lines {
				if line.Type != "+" {
					continue
				}

				content := line.Content

				// Rule 1: Goroutine Safety
				if goroutineRegex.MatchString(content) && !strings.Contains(content, "ctx") && !strings.Contains(content, "wg") {
					rawFindings = append(rawFindings, Finding{
						TaskID:         taskID,
						Severity:       "high",
						Category:       "Goroutine Safety",
						File:           file.NewPath,
						Line:           line.NewLine,
						Title:          "Potential unmanaged goroutine leak",
						Evidence:       strings.TrimSpace(content),
						Recommendation: "Ensure goroutine passes context.Context and uses sync.WaitGroup or done channel for shutdown.",
						Confidence:     0.90,
						Source:         "static_rule",
						RuleID:         "GOP-001",
					})
				}

				// Rule 2: Resource Closure & DB Tx Rollback
				if dbTxRegex.MatchString(content) {
					rawFindings = append(rawFindings, Finding{
						TaskID:         taskID,
						Severity:       "high",
						Category:       "Database Transaction",
						File:           file.NewPath,
						Line:           line.NewLine,
						Title:          "Database transaction created without defer Rollback() check",
						Evidence:       strings.TrimSpace(content),
						Recommendation: "Add defer tx.Rollback() immediately after beginning transaction.",
						Confidence:     0.95,
						Source:         "static_rule",
						RuleID:         "GOP-006",
					})
				}

				if strings.Contains(content, "http.Get(") || strings.Contains(content, "http.Post(") || strings.Contains(content, "os.Open(") {
					rawFindings = append(rawFindings, Finding{
						TaskID:         taskID,
						Severity:       "medium",
						Category:       "Resource Lifecycle",
						File:           file.NewPath,
						Line:           line.NewLine,
						Title:          "Resource opened without explicit Close() deferred on next line",
						Evidence:       strings.TrimSpace(content),
						Recommendation: "Defer resource.Close() immediately after error check.",
						Confidence:     0.88,
						Source:         "static_rule",
						RuleID:         "GOP-002",
					})
				}

				// Rule 3: Ignored Errors
				if ignoredErrRegex.MatchString(content) {
					rawFindings = append(rawFindings, Finding{
						TaskID:         taskID,
						Severity:       "medium",
						Category:       "Error Handling",
						File:           file.NewPath,
						Line:           line.NewLine,
						Title:          "Explicitly ignored returned error or tuple",
						Evidence:       strings.TrimSpace(content),
						Recommendation: "Check and handle returned error instead of blank identifier assignment.",
						Confidence:     0.92,
						Source:         "static_rule",
						RuleID:         "GOP-003",
					})
				}

				// Rule 4: Secret Exposure
				if secretRegex.MatchString(content) {
					rawFindings = append(rawFindings, Finding{
						TaskID:         taskID,
						Severity:       "high",
						Category:       "Security Risk",
						File:           file.NewPath,
						Line:           line.NewLine,
						Title:          "Hardcoded credential or API secret detected",
						Evidence:       RedactSecret(strings.TrimSpace(content)),
						Recommendation: "Remove hardcoded secret and load from environment or secret manager.",
						Confidence:     0.99,
						Source:         "static_rule",
						RuleID:         "GOP-004",
					})
				}
			}
		}
	}

	return DeduplicateFindings(rawFindings)
}

// RedactSecret masks sensitive values in string content.
func RedactSecret(input string) string {
	return secretRegex.ReplaceAllStringFunc(input, func(m string) string {
		parts := secretRegex.FindStringSubmatch(m)
		if len(parts) >= 3 {
			val := parts[2]
			maskedVal := val[:2] + "****" + val[len(val)-2:]
			return strings.Replace(m, val, maskedVal, 1)
		}
		return m
	})
}

// DeduplicateFindings removes duplicate findings on (file, line, category).
func DeduplicateFindings(findings []Finding) []Finding {
	seen := make(map[string]bool)
	var result []Finding

	for i, f := range findings {
		key := fmt.Sprintf("%s:%d:%s", f.File, f.Line, f.Category)
		if !seen[key] {
			seen[key] = true
			f.ID = fmt.Sprintf("%s-finding-%d", f.TaskID, i+1)
			result = append(result, f)
		}
	}
	return result
}
