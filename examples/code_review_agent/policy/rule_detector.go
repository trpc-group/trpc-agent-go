//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package policy

import (
	"fmt"
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/parser"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/storage"
)

type Rule struct {
	ID          string
	Name        string
	Description string
	Severity    storage.FindingSeverity
	Category    storage.FindingCategory
	Pattern     *regexp.Regexp
	Confidence  float64
	NeedsReview bool
	Suggestion  string
}

var rules = []Rule{
	{
		ID:          "GOROUTINE_LEAK",
		Name:        "Goroutine Leak",
		Description: "Detects potential goroutine leaks",
		Severity:    storage.SeverityHigh,
		Category:    storage.CategoryReliability,
		Pattern:     regexp.MustCompile(`go\s+func`),
		Confidence:  0.85,
		NeedsReview: false,
		Suggestion:  "Ensure the goroutine has proper synchronization or use a worker pool pattern",
	},
	{
		ID:          "CHANNEL_UNBUFFERED",
		Name:        "Unbuffered Channel",
		Description: "Detects unbuffered channel creation without proper handling",
		Severity:    storage.SeverityMedium,
		Category:    storage.CategoryReliability,
		Pattern:     regexp.MustCompile(`make\s*\(\s*chan\s+[^,)]+\s*\)`),
		Confidence:  0.7,
		NeedsReview: true,
		Suggestion:  "Consider using buffered channels or ensure proper synchronization",
	},
	{
		ID:          "DEFER_IN_LOOP",
		Name:        "Defer in Loop",
		Description: "Detects defer statements inside loops which can cause resource leaks",
		Severity:    storage.SeverityHigh,
		Category:    storage.CategoryReliability,
		Pattern:     regexp.MustCompile(`^\s*for\s+.*\{[\s\S]*?^\s*defer\s+`),
		Confidence:  0.95,
		NeedsReview: false,
		Suggestion:  "Move defer outside the loop or use a helper function inside the loop",
	},
	{
		ID:          "ERROR_IGNORED",
		Name:        "Error Ignored",
		Description: "Detects ignored error returns",
		Severity:    storage.SeverityMedium,
		Category:    storage.CategoryReliability,
		Pattern:     regexp.MustCompile(`_\s*:?=\s*[\w\.]+\(`),
		Confidence:  0.8,
		NeedsReview: false,
		Suggestion:  "Always check and handle errors properly",
	},
	{
		ID:          "SECRET_HARDCODED",
		Name:        "Hardcoded Secret",
		Description: "Detects potential hardcoded secrets",
		Severity:    storage.SeverityHigh,
		Category:    storage.CategorySecurity,
		Pattern:     regexp.MustCompile(`(?i)(api[_-]?key|secret[_-]?key|access[_-]?token|password|token|apikey|secretkey|accesstoken)\s*=\s*["']`),
		Confidence:  0.9,
		NeedsReview: false,
		Suggestion:  "Use environment variables or secure secret management",
	},
	{
		ID:          "JWT_TOKEN",
		Name:        "JWT Token",
		Description: "Detects hardcoded JWT tokens",
		Severity:    storage.SeverityHigh,
		Category:    storage.CategorySecurity,
		Pattern:     regexp.MustCompile(`(?i)(eyJ[a-zA-Z0-9_-]*\.[a-zA-Z0-9_-]*(\.[a-zA-Z0-9_-]*)?)`),
		Confidence:  0.95,
		NeedsReview: false,
		Suggestion:  "Use secure token management instead of hardcoding JWT tokens",
	},
	{
		ID:          "SQL_INJECTION",
		Name:        "SQL Injection Risk",
		Description: "Detects potential SQL injection vulnerabilities",
		Severity:    storage.SeverityHigh,
		Category:    storage.CategorySecurity,
		Pattern:     regexp.MustCompile(`fmt\.Sprintf\s*\(\s*["'].*%s.*["']`),
		Confidence:  0.85,
		NeedsReview: true,
		Suggestion:  "Use prepared statements or parameterized queries",
	},
	{
		ID:          "BUFFER_OVERFLOW",
		Name:        "Buffer Overflow Risk",
		Description: "Detects potential buffer overflow risks",
		Severity:    storage.SeverityHigh,
		Category:    storage.CategorySecurity,
		Pattern:     regexp.MustCompile(`make\(\[\]byte,\s*\d+\)\s*\[`),
		Confidence:  0.75,
		NeedsReview: true,
		Suggestion:  "Ensure proper bounds checking before array access",
	},
	{
		ID:          "EMPTY_ERROR_CHECK",
		Name:        "Empty Error Check",
		Description: "Detects empty error check blocks",
		Severity:    storage.SeverityMedium,
		Category:    storage.CategoryReliability,
		Pattern:     regexp.MustCompile(`if\s+err\s*!=?\s*nil\s*\{?\s*\}`),
		Confidence:  0.9,
		NeedsReview: false,
		Suggestion:  "Add proper error handling or return the error",
	},
	{
		ID:          "UNUSED_VARIABLE",
		Name:        "Unused Variable",
		Description: "Detects unused variables",
		Severity:    storage.SeverityLow,
		Category:    storage.CategoryMaintainability,
		Pattern:     regexp.MustCompile(`^\s*(var|func|type)\s+\w+\s*=`),
		Confidence:  0.6,
		NeedsReview: true,
		Suggestion:  "Remove unused variables or use them",
	},
	{
		ID:          "MISSING_CLOSE",
		Name:        "Missing Resource Close",
		Description: "Detects potential missing resource cleanup",
		Severity:    storage.SeverityMedium,
		Category:    storage.CategoryReliability,
		Pattern:     regexp.MustCompile(`(os\.Open|http\.Get|net\.Dial)\(`),
		Confidence:  0.7,
		NeedsReview: true,
		Suggestion:  "Ensure resources are properly closed with defer",
	},
}

type RuleDetector struct {
	rules []Rule
}

func NewRuleDetector() *RuleDetector {
	return &RuleDetector{rules: rules}
}

func (d *RuleDetector) Detect(diff *parser.DiffResult) []storage.Finding {
	var findings []storage.Finding

	for _, file := range diff.Files {
		if !strings.HasSuffix(file.NewPath, ".go") {
			continue
		}

		for _, hunk := range file.Hunks {
			hunkContent := strings.Join(hunk.Content, "\n")

			for _, rule := range d.rules {
				if rule.Pattern.MatchString(hunkContent) {
					matches := rule.Pattern.FindAllStringIndex(hunkContent, -1)
					for _, match := range matches {
						startLine := countLines(hunkContent[:match[0]]) + hunk.NewStart
						evidence := hunkContent[match[0]:match[1]]
						findings = append(findings, storage.Finding{
							RuleID:      rule.ID,
							Filepath:    file.NewPath,
							LineNumber:  startLine,
							Severity:    rule.Severity,
							Category:    rule.Category,
							Message:     rule.Name + ": " + rule.Description,
							Evidence:    evidence,
							Suggestion:  rule.Suggestion,
							Confidence:  rule.Confidence,
							NeedsReview: rule.NeedsReview,
						})
					}
				}
			}
		}
	}

	return findings
}

func countLines(s string) int {
	return strings.Count(s, "\n")
}

func (d *RuleDetector) DetectInCode(code string, filepath string) []storage.Finding {
	var findings []storage.Finding

	for _, rule := range d.rules {
		if rule.Pattern.MatchString(code) {
			findings = append(findings, storage.Finding{
				RuleID:      rule.ID,
				Filepath:    filepath,
				LineNumber:  -1,
				Severity:    rule.Severity,
				Category:    rule.Category,
				Message:     rule.Name + ": " + rule.Description,
				Evidence:    code,
				Suggestion:  rule.Suggestion,
				Confidence:  rule.Confidence,
				NeedsReview: rule.NeedsReview,
			})
		}
	}

	return findings
}

func (d *RuleDetector) GetRules() []Rule {
	return d.rules
}

func RemoveDuplicates(findings []storage.Finding) []storage.Finding {
	seen := make(map[string]bool)
	var result []storage.Finding

	for _, f := range findings {
		key := fmt.Sprintf("%s:%d:%s", f.Filepath, f.LineNumber, f.RuleID)
		if !seen[key] {
			seen[key] = true
			result = append(result, f)
		}
	}

	return result
}
