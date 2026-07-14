//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package rules implements deterministic code review rules.
package rules

import (
	"regexp"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/redaction"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
)

const (
	highConfidence = 0.75
	lowConfidence  = 0.45
)

var secretLineRE = regexp.MustCompile(`(?i)(api[_-]?key|token|password|passwd|secret)\s*[:=]`)

// Result separates high-confidence findings from review warnings.
type Result struct {
	Findings         []review.Finding
	Warnings         []review.Finding
	NeedsHumanReview []review.Finding
}

// Scan evaluates deterministic rules against changed files.
func Scan(files []review.ChangedFile) Result {
	var all []review.Finding
	for _, file := range files {
		if file.Language != "go" {
			continue
		}
		for _, hunk := range file.Hunks {
			all = append(all, scanHunk(file, hunk)...)
		}
		if missingTest(file, files) {
			all = append(all, review.Finding{
				Severity:       review.SeverityMedium,
				Category:       "testing",
				File:           file.NewPath,
				Line:           firstAddedLine(file),
				Title:          "Changed Go code without nearby test changes",
				Evidence:       "No _test.go file is present in the same diff.",
				Recommendation: "Add or update tests that cover the changed behavior.",
				Confidence:     0.58,
				Source:         "rule-only",
				RuleID:         "TEST001",
			})
		}
	}
	all = Deduplicate(all)
	return splitByConfidence(all)
}

func scanHunk(file review.ChangedFile, hunk review.Hunk) []review.Finding {
	var findings []review.Finding
	hunkText := hunkText(hunk)
	for _, line := range hunk.Lines {
		if line.Kind != "added" {
			continue
		}
		trimmed := strings.TrimSpace(line.Content)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		add := func(f review.Finding) {
			f.File = file.NewPath
			f.Line = line.NewLine
			f.Evidence = redaction.RedactText(strings.TrimSpace(f.Evidence))
			findings = append(findings, f)
		}
		if secretLineRE.MatchString(trimmed) || strings.Contains(trimmed, "sk-") || strings.Contains(trimmed, "ghp_") {
			add(review.Finding{
				Severity:       review.SeverityCritical,
				Category:       "security",
				Title:          "Potential hard-coded secret",
				Evidence:       trimmed,
				Recommendation: "Move secrets to a secret manager or environment variable and rotate the exposed credential.",
				Confidence:     0.96,
				Source:         "rule-only",
				RuleID:         "SEC001",
			})
		}
		if strings.Contains(trimmed, "go func(") || strings.Contains(trimmed, "go func()") {
			confidence := 0.78
			if strings.Contains(hunkText, "ctx.Done()") || strings.Contains(hunkText, "select {") {
				confidence = 0.52
			}
			add(review.Finding{
				Severity:       review.SeverityHigh,
				Category:       "concurrency",
				Title:          "Goroutine may not have a cancellation path",
				Evidence:       trimmed,
				Recommendation: "Thread context cancellation through the goroutine and exit on ctx.Done().",
				Confidence:     confidence,
				Source:         "rule-only",
				RuleID:         "GOR001",
			})
		}
		if strings.Contains(trimmed, "context.Background()") || strings.Contains(trimmed, "context.TODO()") {
			add(review.Finding{
				Severity:       review.SeverityMedium,
				Category:       "context",
				Title:          "Request context is replaced",
				Evidence:       trimmed,
				Recommendation: "Pass the caller context through instead of creating a background context in request-scoped code.",
				Confidence:     0.72,
				Source:         "rule-only",
				RuleID:         "CTX001",
			})
		}
		if opensResource(trimmed) && !strings.Contains(hunkText, ".Close()") {
			add(review.Finding{
				Severity:       review.SeverityHigh,
				Category:       "resource",
				Title:          "Opened resource may not be closed",
				Evidence:       trimmed,
				Recommendation: "Close the returned resource with defer after checking the error.",
				Confidence:     0.82,
				Source:         "rule-only",
				RuleID:         "RES001",
			})
		}
		if ignoresError(trimmed) {
			add(review.Finding{
				Severity:       review.SeverityMedium,
				Category:       "error_handling",
				Title:          "Error result is ignored",
				Evidence:       trimmed,
				Recommendation: "Handle or return the error so failures are observable.",
				Confidence:     0.8,
				Source:         "rule-only",
				RuleID:         "ERR001",
			})
		}
		if strings.Contains(trimmed, ".Begin(") || strings.Contains(trimmed, ".BeginTx(") {
			if !strings.Contains(hunkText, ".Commit()") || !strings.Contains(hunkText, ".Rollback()") {
				add(review.Finding{
					Severity:       review.SeverityHigh,
					Category:       "database",
					Title:          "Transaction lifecycle is incomplete",
					Evidence:       trimmed,
					Recommendation: "Ensure every transaction has rollback on failure and commit on success.",
					Confidence:     0.84,
					Source:         "rule-only",
					RuleID:         "DB001",
				})
			}
		}
		if strings.Contains(trimmed, "panic(") || strings.Contains(trimmed, "log.Fatal") {
			add(review.Finding{
				Severity:       review.SeverityMedium,
				Category:       "reliability",
				Title:          "Library code may terminate the process",
				Evidence:       trimmed,
				Recommendation: "Return an error to the caller instead of panicking or calling log.Fatal.",
				Confidence:     0.77,
				Source:         "rule-only",
				RuleID:         "PANIC001",
			})
		}
	}
	return findings
}

// Deduplicate keeps the highest-confidence finding for the same file/line/rule.
func Deduplicate(in []review.Finding) []review.Finding {
	best := map[string]review.Finding{}
	for _, f := range in {
		key := f.File + "\x00" + f.RuleID + "\x00" + f.Category + "\x00" + itoa(f.Line)
		if existing, ok := best[key]; !ok || better(f, existing) {
			best[key] = f
		}
	}
	out := make([]review.Finding, 0, len(best))
	for _, f := range best {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].RuleID < out[j].RuleID
	})
	return out
}

func splitByConfidence(in []review.Finding) Result {
	var out Result
	for _, f := range in {
		switch {
		case f.Confidence >= highConfidence:
			out.Findings = append(out.Findings, f)
		case f.Confidence >= lowConfidence:
			out.NeedsHumanReview = append(out.NeedsHumanReview, f)
		default:
			out.Warnings = append(out.Warnings, f)
		}
	}
	return out
}

func better(a, b review.Finding) bool {
	if severityRank(a.Severity) != severityRank(b.Severity) {
		return severityRank(a.Severity) > severityRank(b.Severity)
	}
	if a.Confidence != b.Confidence {
		return a.Confidence > b.Confidence
	}
	return len(a.Evidence) > len(b.Evidence)
}

func severityRank(s string) int {
	switch s {
	case review.SeverityCritical:
		return 4
	case review.SeverityHigh:
		return 3
	case review.SeverityMedium:
		return 2
	case review.SeverityLow:
		return 1
	default:
		return 0
	}
}

func hunkText(h review.Hunk) string {
	var b strings.Builder
	for _, l := range h.Lines {
		b.WriteString(l.Content)
		b.WriteByte('\n')
	}
	return b.String()
}

func opensResource(line string) bool {
	needles := []string{"os.Open(", "os.OpenFile(", "http.Get(", "http.Post(", ".Query(", ".QueryContext(", "sql.Open("}
	for _, needle := range needles {
		if strings.Contains(line, needle) {
			return true
		}
	}
	return false
}

func ignoresError(line string) bool {
	if strings.Contains(line, "_ =") {
		return true
	}
	return strings.Contains(line, ", _ :=") || strings.Contains(line, ", _ =")
}

func missingTest(file review.ChangedFile, files []review.ChangedFile) bool {
	if strings.HasSuffix(file.NewPath, "_test.go") {
		return false
	}
	hasAddedGoCode := false
	for _, h := range file.Hunks {
		for _, line := range h.Lines {
			if line.Kind == "added" && looksLikeCode(line.Content) {
				hasAddedGoCode = true
				break
			}
		}
	}
	if !hasAddedGoCode {
		return false
	}
	dir := dirName(file.NewPath)
	for _, f := range files {
		if strings.HasSuffix(f.NewPath, "_test.go") && dirName(f.NewPath) == dir {
			return false
		}
	}
	return true
}

func looksLikeCode(line string) bool {
	line = strings.TrimSpace(line)
	return strings.HasPrefix(line, "func ") ||
		strings.HasPrefix(line, "type ") ||
		strings.HasPrefix(line, "var ") ||
		strings.Contains(line, ":=") ||
		strings.Contains(line, "return ")
}

func firstAddedLine(file review.ChangedFile) int {
	for _, h := range file.Hunks {
		for _, line := range h.Lines {
			if line.Kind == "added" {
				return line.NewLine
			}
		}
	}
	return 1
}

func dirName(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[:i]
	}
	return "."
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
