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
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const highConfidenceThreshold = 0.75
const humanReviewThreshold = 0.50

// AnalyzeDiff applies deterministic Go code review rules.
func AnalyzeDiff(summary DiffSummary) ([]Finding, []Finding, []Finding) {
	candidates := make([]Finding, 0, len(summary.AddedLines))
	hunks := hunkContext(summary.AddedLines)
	for _, line := range summary.AddedLines {
		if strings.TrimSpace(line.Content) == "" {
			continue
		}
		candidates = append(candidates, lineRules(line, hunks[line.File])...)
	}
	candidates = append(candidates, testCoverageRule(summary)...)
	return PartitionFindings(DeduplicateFindings(candidates))
}

func lineRules(line AddedLine, fileLines []AddedLine) []Finding {
	content := strings.TrimSpace(line.Content)
	findings := []Finding{}
	if matchesAnySecret(content) {
		findings = append(findings, Finding{
			Severity:       severityHigh,
			Category:       "security",
			File:           line.File,
			Line:           line.Line,
			Title:          "Hard-coded secret-like value",
			Evidence:       line.Content,
			Recommendation: "Move credentials to a secret manager or injected configuration and rotate exposed values.",
			Confidence:     0.97,
			Source:         "deterministic-rule",
			RuleID:         "go.security.secret",
		})
	}
	if strings.Contains(content, "go func(") && !contextMentioned(fileLines) {
		findings = append(findings, Finding{
			Severity:       severityMedium,
			Category:       "concurrency",
			File:           line.File,
			Line:           line.Line,
			Title:          "Goroutine lacks visible context cancellation",
			Evidence:       line.Content,
			Recommendation: "Pass context into the goroutine and exit on ctx.Done() to avoid leaks.",
			Confidence:     0.78,
			Source:         "deterministic-rule",
			RuleID:         "go.concurrency.context",
		})
	}
	if opensResource(content) && !nearbyClose(fileLines, line.Line) {
		findings = append(findings, Finding{
			Severity:       severityMedium,
			Category:       "resource_lifecycle",
			File:           line.File,
			Line:           line.Line,
			Title:          "Opened resource is not visibly closed",
			Evidence:       line.Content,
			Recommendation: "Close response bodies, files, rows, or database handles with defer after checking the error.",
			Confidence:     0.80,
			Source:         "deterministic-rule",
			RuleID:         "go.resource.close",
		})
	}
	if strings.Contains(content, ", _ :=") || strings.Contains(content, ", _ =") {
		findings = append(findings, Finding{
			Severity:       severityMedium,
			Category:       "error_handling",
			File:           line.File,
			Line:           line.Line,
			Title:          "Error return is discarded",
			Evidence:       line.Content,
			Recommendation: "Check and handle the returned error, or document why it is safe to ignore.",
			Confidence:     0.90,
			Source:         "deterministic-rule",
			RuleID:         "go.error.discarded",
		})
	}
	if strings.Contains(content, "sql.Open(") && !nearbyClose(fileLines, line.Line) {
		findings = append(findings, Finding{
			Severity:       severityMedium,
			Category:       "database",
			File:           line.File,
			Line:           line.Line,
			Title:          "Database handle lifecycle is unclear",
			Evidence:       line.Content,
			Recommendation: "Ensure the database handle is reused intentionally and closed by the owner during shutdown.",
			Confidence:     0.72,
			Source:         "deterministic-rule",
			RuleID:         "go.database.lifecycle",
		})
	}
	return findings
}

func matchesAnySecret(s string) bool {
	for _, re := range secretPatterns {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

func opensResource(s string) bool {
	return strings.Contains(s, "os.Open(") ||
		strings.Contains(s, "http.Get(") ||
		strings.Contains(s, ".Query(") ||
		strings.Contains(s, ".QueryContext(") ||
		strings.Contains(s, "sql.Open(")
}

func contextMentioned(lines []AddedLine) bool {
	for _, line := range lines {
		if strings.Contains(line.Content, "context.Context") ||
			strings.Contains(line.Content, "ctx.Done()") ||
			strings.Contains(line.Content, "<-ctx.Done()") {
			return true
		}
	}
	return false
}

func nearbyClose(lines []AddedLine, base int) bool {
	for _, line := range lines {
		if line.Line < base || line.Line > base+5 {
			continue
		}
		if strings.Contains(line.Content, ".Close()") ||
			strings.Contains(line.Content, "defer ") && strings.Contains(line.Content, "Close") {
			return true
		}
	}
	return false
}

func hunkContext(lines []AddedLine) map[string][]AddedLine {
	out := make(map[string][]AddedLine)
	for _, line := range lines {
		out[line.File] = append(out[line.File], line)
	}
	return out
}

func testCoverageRule(summary DiffSummary) []Finding {
	changedGo := false
	testChanged := false
	firstFile := ""
	for _, file := range summary.Files {
		p := file.NewPath
		if p == "" {
			p = file.OldPath
		}
		if filepath.Ext(p) != ".go" {
			continue
		}
		if strings.HasSuffix(p, "_test.go") {
			testChanged = true
			continue
		}
		if firstFile == "" {
			firstFile = p
		}
		changedGo = true
	}
	if !changedGo || testChanged {
		return nil
	}
	return []Finding{{
		Severity:       severityLow,
		Category:       "testing",
		File:           firstFile,
		Line:           1,
		Title:          "Go code changed without test changes",
		Evidence:       "Diff changes Go implementation files but no *_test.go file is present.",
		Recommendation: "Add or update focused tests for the changed behavior, or record why existing coverage is sufficient.",
		Confidence:     0.66,
		Source:         "deterministic-rule",
		RuleID:         "go.testing.missing",
	}}
}

// DeduplicateFindings removes duplicate reports by file, line, category, and rule.
func DeduplicateFindings(in []Finding) []Finding {
	if len(in) == 0 {
		return []Finding{}
	}
	sort.SliceStable(in, func(i, j int) bool {
		if in[i].File != in[j].File {
			return in[i].File < in[j].File
		}
		if in[i].Line != in[j].Line {
			return in[i].Line < in[j].Line
		}
		return in[i].RuleID < in[j].RuleID
	})
	seen := make(map[string]struct{}, len(in))
	out := make([]Finding, 0, len(in))
	for _, f := range in {
		key := f.File + "\x00" + f.Category + "\x00" + f.RuleID + "\x00" + strconv.Itoa(f.Line)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, redactFinding(f))
	}
	return out
}

// PartitionFindings separates high-confidence findings from warnings and
// human-review items.
func PartitionFindings(in []Finding) ([]Finding, []Finding, []Finding) {
	findings := []Finding{}
	warnings := []Finding{}
	needsHuman := []Finding{}
	for _, f := range in {
		switch {
		case f.Confidence >= highConfidenceThreshold:
			findings = append(findings, f)
		case f.Confidence >= humanReviewThreshold:
			needsHuman = append(needsHuman, f)
		default:
			warnings = append(warnings, f)
		}
	}
	return findings, warnings, needsHuman
}
