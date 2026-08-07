//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package analysis provides the rule engine for detecting issues in Go code.
package analysis

import (
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/diffparse"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/reviewmodel"
)

// Rule defines a single review rule.
type Rule struct {
	ID             string  `json:"id"`
	Category       string  `json:"category"`
	Severity       string  `json:"severity"`
	Title          string  `json:"title"`
	Pattern        string  `json:"pattern"`
	Recommendation string  `json:"recommendation"`
	Confidence     float64 `json:"confidence"`
}

// Analyzer runs rules against parsed diffs.
type Analyzer struct {
	rules []Rule
}

// NewAnalyzer creates an Analyzer with the built-in rules.
func NewAnalyzer() *Analyzer {
	return &Analyzer{rules: defaultRules()}
}

// NewAnalyzerWithRules creates an Analyzer with custom rules.
func NewAnalyzerWithRules(rules []Rule) *Analyzer {
	return &Analyzer{rules: rules}
}

// Analyze runs all rules against the parsed diff and returns findings.
func (a *Analyzer) Analyze(pd *diffparse.ParsedDiff) []reviewmodel.Finding {
	var findings []reviewmodel.Finding
	addedLines := diffparse.AllAddedLines(pd)

	for _, rule := range a.rules {
		re, err := regexp.Compile(rule.Pattern)
		if err != nil {
			continue
		}
		for filePath, lines := range addedLines {
			for _, line := range lines {
				if re.MatchString(line.Content) {
					evidence := line.Content
					if len(evidence) > 200 {
						evidence = evidence[:200] + "..."
					}
					f := reviewmodel.Finding{
						Severity:       rule.Severity,
						Category:       rule.Category,
						FilePath:       filePath,
						Line:           line.NewLine,
						Title:          rule.Title,
						Evidence:       evidence,
						Recommendation: rule.Recommendation,
						Confidence:     rule.Confidence,
						Source:         "rule",
						RuleID:         rule.ID,
					}
					findings = append(findings, f)
				}
			}
		}
	}

	// Run secret detection separately.
	secretFindings := detectSecrets(addedLines)
	findings = append(findings, secretFindings...)

	// Run missing test detection.
	if len(pd.Files) > 0 {
		testFindings := detectMissingTests(pd)
		findings = append(findings, testFindings...)
	}

	return findings
}

func detectSecrets(addedLines map[string][]diffparse.ChangedLine) []reviewmodel.Finding {
	var findings []reviewmodel.Finding
	for filePath, lines := range addedLines {
		for _, line := range lines {
			if redact.ContainsSecret(line.Content) {
				evidence := redact.String(line.Content)
				if len(evidence) > 200 {
					evidence = evidence[:200] + "..."
				}
				findings = append(findings, reviewmodel.Finding{
					Severity:       reviewmodel.SeverityCritical,
					Category:       reviewmodel.CategorySensitive,
					FilePath:       filePath,
					Line:           line.NewLine,
					Title:          "Potential secret or credential in code",
					Evidence:       evidence,
					Recommendation: "Move secrets to environment variables or a secret management service. Never hardcode credentials.",
					Confidence:     0.9,
					Source:         "rule",
					RuleID:         "GO-SECRET-001",
				})
			}
		}
	}
	return findings
}

func detectMissingTests(pd *diffparse.ParsedDiff) []reviewmodel.Finding {
	var findings []reviewmodel.Finding
	for _, cf := range pd.Files {
		name := cf.NewPath
		if name == "" || name == "/dev/null" {
			name = cf.OldPath
		}
		if name == "/dev/null" || cf.Deleted {
			continue
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		hasAddedFuncs := false
		for _, h := range cf.Hunks {
			for _, l := range h.Lines {
				if l.Kind == "+" && strings.HasPrefix(strings.TrimSpace(l.Content), "func ") {
					hasAddedFuncs = true
					break
				}
			}
			if hasAddedFuncs {
				break
			}
		}

		if hasAddedFuncs {
			testFile := strings.TrimSuffix(name, ".go") + "_test.go"
			hasTest := false
			for _, f := range pd.Files {
				if f.NewPath == testFile || f.OldPath == testFile {
					hasTest = true
					break
				}
			}
			if !hasTest {
				findings = append(findings, reviewmodel.Finding{
					Severity:       reviewmodel.SeverityMedium,
					Category:       reviewmodel.CategoryTest,
					FilePath:       name,
					Line:           0,
					Title:          "New or modified functions without corresponding tests",
					Evidence:       "File " + name + " has added functions but no corresponding test file (" + testFile + ") found in diff.",
					Recommendation: "Add unit tests for new functions in " + testFile + ".",
					Confidence:     0.7,
					Source:         "rule",
					RuleID:         "GO-TEST-001",
				})
			}
		}
	}
	return findings
}

func defaultRules() []Rule {
	return []Rule{
		{
			ID:             "GO-GOROUTINE-001",
			Category:       reviewmodel.CategoryGoroutine,
			Severity:       reviewmodel.SeverityHigh,
			Title:          "Goroutine started without context propagation",
			Pattern:        `go\s+func\s*\(\s*\)\s*\{`,
			Recommendation: "Pass context.Context to goroutines and use select with ctx.Done() for cancellation.",
			Confidence:     0.7,
		},
		{
			ID:             "GO-SECURITY-001",
			Category:       reviewmodel.CategorySecurity,
			Severity:       reviewmodel.SeverityCritical,
			Title:          "Use of os/exec.Command with unsanitized input",
			Pattern:        `exec\.Command\(`,
			Recommendation: "Validate and sanitize all inputs to exec.Command. Consider using exec.CommandContext for timeout support.",
			Confidence:     0.8,
		},
		{
			ID:             "GO-RESOURCE-001",
			Category:       reviewmodel.CategoryResource,
			Severity:       reviewmodel.SeverityHigh,
			Title:          "Resource opened without defer close",
			Pattern:        `os\.Open\(|os\.Create\(`,
			Recommendation: "Use defer to close resources immediately after opening. Ensure error paths also close the resource.",
			Confidence:     0.75,
		},
		{
			ID:             "GO-RESOURCE-002",
			Category:       reviewmodel.CategoryResource,
			Severity:       reviewmodel.SeverityMedium,
			Title:          "HTTP response body not closed",
			Pattern:        `http\.Get\(|http\.Post\(|client\.Do\(`,
			Recommendation: "Always close http.Response.Body with defer resp.Body.Close().",
			Confidence:     0.8,
		},
		{
			ID:             "GO-DB-001",
			Category:       reviewmodel.CategoryDB,
			Severity:       reviewmodel.SeverityHigh,
			Title:          "Database connection opened without lifecycle management",
			Pattern:        `sql\.Open\(`,
			Recommendation: "Use connection pooling (sql.DB). Set connection limits, max lifetime, and ensure Close() is called on shutdown.",
			Confidence:     0.8,
		},
		{
			ID:             "GO-ERROR-001",
			Category:       reviewmodel.CategoryErrorHandling,
			Severity:       reviewmodel.SeverityMedium,
			Title:          "Error not checked after function call",
			Pattern:        `^\s*\w+\s*,\s*_\s*:=\s*`,
			Recommendation: "Do not ignore returned errors with _. Handle errors explicitly or document why they are ignored.",
			Confidence:     0.6,
		},
		{
			ID:             "GO-ERROR-002",
			Category:       reviewmodel.CategoryErrorHandling,
			Severity:       reviewmodel.SeverityLow,
			Title:          "Bare panic call in production code",
			Pattern:        `^\s*panic\(`,
			Recommendation: "Avoid panic in library/production code. Return errors or use structured error handling.",
			Confidence:     0.7,
		},
	}
}

// Deduplicate merges findings with the same file, line, and category.
// The highest-confidence finding is retained, and unique fields are joined.
func Deduplicate(findings []reviewmodel.Finding) []reviewmodel.Finding {
	if len(findings) <= 1 {
		return findings
	}
	type key struct {
		file     string
		line     int
		category string
	}
	seen := make(map[key]*reviewmodel.Finding)
	order := make([]key, 0)

	for i := range findings {
		f := &findings[i]
		k := key{file: f.FilePath, line: f.Line, category: f.Category}
		if existing, ok := seen[k]; ok {
			if f.Confidence > existing.Confidence {
				existing.Confidence = f.Confidence
				existing.Evidence = f.Evidence
			}
			if f.RuleID != existing.RuleID {
				existing.RuleID = joinUnique(existing.RuleID, f.RuleID)
			}
			if f.Source != existing.Source {
				existing.Source = joinUnique(existing.Source, f.Source)
			}
		} else {
			seen[k] = f
			order = append(order, k)
		}
	}

	result := make([]reviewmodel.Finding, 0, len(order))
	for _, k := range order {
		result = append(result, *seen[k])
	}
	return result
}

func joinUnique(a, b string) string {
	aParts := strings.Split(a, ",")
	for _, part := range strings.Split(b, ",") {
		found := false
		for _, ap := range aParts {
			if strings.TrimSpace(ap) == strings.TrimSpace(part) {
				found = true
				break
			}
		}
		if !found {
			aParts = append(aParts, part)
		}
	}
	return strings.Join(aParts, ",")
}
