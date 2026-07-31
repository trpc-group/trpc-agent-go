//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package review

import (
	"fmt"
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/domain"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/input"
)

// Output is the deterministic rule review result.
type Output struct {
	Findings         []domain.Finding
	NeedsHumanReview []domain.Finding
	Suppressed       int
}

// Engine evaluates deterministic bundled rules.
type Engine struct {
	redactor Redactor
}

// NewEngine creates a rule engine.
func NewEngine(redactor Redactor) Engine {
	return Engine{redactor: redactor}
}

// Review evaluates changed added lines and routes findings by confidence.
func (e Engine) Review(diff input.Diff) Output {
	var candidates []domain.Finding
	hasProd, hasTest := false, false
	for _, f := range diff.Files {
		if strings.HasSuffix(f.NewPath, "_test.go") {
			hasTest = true
		} else if strings.HasSuffix(f.NewPath, ".go") && len(f.Added) > 0 {
			hasProd = true
		}
		for _, line := range f.Added {
			candidates = append(candidates, e.rulesForLine(f.NewPath, line)...)
		}
	}
	if hasProd && !hasTest {
		candidates = append(candidates, domain.Finding{
			Severity: domain.SeverityMedium, Category: domain.CategoryTests, File: firstProdFile(diff.Files),
			Line: 0, Title: "production change lacks related tests",
			Evidence:       "no changed _test.go file was present in the diff",
			Recommendation: "add focused tests for the changed behavior",
			Confidence:     0.66, Source: "rule", RuleID: "tests.missing-related-test",
		})
	}
	candidates = DedupeFindings(candidates)
	var out Output
	for _, f := range candidates {
		f.Evidence = e.redactor.Redact(f.Evidence)
		f.Recommendation = e.redactor.Redact(f.Recommendation)
		switch domain.BucketForConfidence(f.Confidence) {
		case domain.BucketFinding:
			out.Findings = append(out.Findings, f)
		case domain.BucketHumanReview:
			out.NeedsHumanReview = append(out.NeedsHumanReview, f)
		default:
			out.Suppressed++
		}
	}
	domain.SortFindings(out.Findings)
	domain.SortFindings(out.NeedsHumanReview)
	return out
}

func (e Engine) rulesForLine(file string, line input.AddedLine) []domain.Finding {
	text := line.Text
	var out []domain.Finding
	add := func(sev domain.Severity, cat, title, evidence, rec string, conf float64, ruleID string) {
		out = append(out, domain.Finding{
			Severity: sev, Category: cat, File: file, Line: line.Line,
			Title: title, Evidence: evidence, Recommendation: rec,
			Confidence: conf, Source: "rule", RuleID: ruleID,
		})
	}
	if e.redactor.Detect(text) {
		add(domain.SeverityHigh, domain.CategorySecrets, "secret literal added",
			text, "remove the literal secret and load it from a managed secret source", 0.98, "secrets.literal")
	}
	if isShellInjection(text) || isDynamicSQL(text) {
		add(domain.SeverityHigh, domain.CategorySecurity, "unsafe dynamic execution or query",
			text, "use fixed commands or parameterized queries with validated arguments", 0.90, "security.dynamic-input")
	}
	if isUnsafeGoRoutine(text) {
		add(domain.SeverityMedium, domain.CategoryConcurrency, "goroutine has no visible lifetime control",
			text, "tie goroutine lifetime to context cancellation or an owned shutdown path", 0.82, "concurrency.goroutine-lifetime")
	}
	if isResourceLeak(text) {
		add(domain.SeverityMedium, domain.CategoryResources, "opened resource is not closed",
			text, "close the resource in the same function, usually with defer after error handling", 0.86, "resources.close-missing")
	}
	if isIgnoredError(text) {
		add(domain.SeverityMedium, domain.CategoryErrors, "error result is ignored",
			text, "check and propagate or handle the returned error", 0.88, "errors.ignored")
	}
	if isRowsLifecycle(text) {
		add(domain.SeverityMedium, domain.CategoryDatabase, "database rows are not closed",
			text, "call rows.Close and check rows.Err when iterating query results", 0.87, "database.rows-close-missing")
	}
	return out
}

func isShellInjection(s string) bool {
	return strings.Contains(s, `exec.Command("sh", "-c"`) ||
		strings.Contains(s, `exec.Command("bash", "-c"`)
}

func isDynamicSQL(s string) bool {
	return (strings.Contains(s, ".Query(") || strings.Contains(s, ".Exec(")) &&
		strings.Contains(s, "+")
}

func isUnsafeGoRoutine(s string) bool {
	if !strings.Contains(s, "go func") {
		return false
	}
	return !(strings.Contains(s, "ctx context.Context") && strings.Contains(s, "ctx.Done()"))
}

func isResourceLeak(s string) bool {
	return (strings.Contains(s, "os.Open(") || strings.Contains(s, "http.Get(")) &&
		!strings.Contains(s, ".Close()") && !strings.Contains(s, "defer ")
}

var ignoredErrRE = regexp.MustCompile(`(^|[^A-Za-z0-9_]),\s*_\s*:=|(^|[^A-Za-z0-9_]),\s*_\s*=`)

func isIgnoredError(s string) bool {
	return (ignoredErrRE.MatchString(s) || strings.Contains(s, " _ := risky(")) &&
		!strings.Contains(s, "rows, _ :=")
}

func isRowsLifecycle(s string) bool {
	return strings.Contains(s, "rows") && strings.Contains(s, ".Query(") && !strings.Contains(s, "rows.Close()")
}

func firstProdFile(files []input.FileDiff) string {
	for _, f := range files {
		if strings.HasSuffix(f.NewPath, ".go") && !strings.HasSuffix(f.NewPath, "_test.go") {
			return f.NewPath
		}
	}
	return "unknown"
}

// DedupeFindings deduplicates by normalized file, line, and category.
func DedupeFindings(in []domain.Finding) []domain.Finding {
	best := map[string]domain.Finding{}
	for _, f := range in {
		key := fmt.Sprintf("%s:%d:%s", f.File, f.Line, f.Category)
		if cur, ok := best[key]; !ok || betterFinding(f, cur) {
			best[key] = f
		}
	}
	out := make([]domain.Finding, 0, len(best))
	for _, f := range best {
		out = append(out, f)
	}
	domain.SortFindings(out)
	return out
}

func betterFinding(a, b domain.Finding) bool {
	ra, rb := severityRank(a.Severity), severityRank(b.Severity)
	if ra != rb {
		return ra < rb
	}
	if a.Confidence != b.Confidence {
		return a.Confidence > b.Confidence
	}
	return a.RuleID < b.RuleID
}

func severityRank(s domain.Severity) int {
	switch s {
	case domain.SeverityCritical:
		return 0
	case domain.SeverityHigh:
		return 1
	case domain.SeverityMedium:
		return 2
	case domain.SeverityLow:
		return 3
	default:
		return 4
	}
}
