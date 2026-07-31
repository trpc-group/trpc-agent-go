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
	"path/filepath"
	"regexp"
	"sort"
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
	prodByPackage := map[string]string{}
	testByPackage := map[string]bool{}
	for _, f := range diff.Files {
		key := packageKey(f)
		if strings.HasSuffix(f.NewPath, "_test.go") {
			if len(f.Added) > 0 {
				testByPackage[key] = true
			}
		} else if strings.HasSuffix(f.NewPath, ".go") && len(f.Added) > 0 {
			if _, ok := prodByPackage[key]; !ok {
				prodByPackage[key] = f.NewPath
			}
		}
		for _, line := range f.Added {
			candidates = append(candidates, e.rulesForLine(f.NewPath, line)...)
		}
		for _, block := range lifecycleBlocks(f.Added) {
			candidates = append(candidates, e.lifecycleRulesForBlock(f.NewPath, block)...)
		}
	}
	for _, key := range sortedPackageKeys(prodByPackage) {
		if testByPackage[key] {
			continue
		}
		file := prodByPackage[key]
		candidates = append(candidates, domain.Finding{
			Severity: domain.SeverityMedium, Category: domain.CategoryTests, File: file,
			Line: 0, Title: "production change lacks related tests",
			Evidence:       fmt.Sprintf("package %s has production changes without related _test.go changes", key),
			Recommendation: "add focused tests for the changed behavior in the same package",
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
	if isIgnoredError(text) {
		add(domain.SeverityMedium, domain.CategoryErrors, "error result is ignored",
			text, "check and propagate or handle the returned error", 0.88, "errors.ignored")
	}
	return out
}

func (e Engine) lifecycleRulesForBlock(file string, lines []input.AddedLine) []domain.Finding {
	if len(lines) == 0 {
		return nil
	}
	text := make([]string, 0, len(lines))
	for _, line := range lines {
		text = append(text, line.Text)
	}
	block := strings.Join(text, "\n")
	anchor := lines[0]
	for _, line := range lines {
		if strings.Contains(line.Text, "go func") || strings.Contains(line.Text, "os.Open(") ||
			strings.Contains(line.Text, "http.Get(") || strings.Contains(line.Text, ".Query(") {
			anchor = line
			break
		}
	}
	add := func(sev domain.Severity, cat, title, rec string, conf float64, ruleID string) domain.Finding {
		return domain.Finding{
			Severity: sev, Category: cat, File: file, Line: anchor.Line,
			Title: title, Evidence: anchor.Text, Recommendation: rec,
			Confidence: conf, Source: "rule", RuleID: ruleID,
		}
	}
	var out []domain.Finding
	if isUnsafeGoRoutine(block) {
		out = append(out, add(domain.SeverityMedium, domain.CategoryConcurrency,
			"goroutine has no visible lifetime control",
			"tie goroutine lifetime to context cancellation or an owned shutdown path", 0.82,
			"concurrency.goroutine-lifetime"))
	}
	if isResourceLeak(block) {
		out = append(out, add(domain.SeverityMedium, domain.CategoryResources,
			"opened resource is not closed",
			"close the resource in the same function, usually with defer after error handling", 0.86,
			"resources.close-missing"))
	}
	if isRowsLifecycle(block) {
		out = append(out, add(domain.SeverityMedium, domain.CategoryDatabase,
			"database rows are not closed",
			"call rows.Close and check rows.Err when iterating query results", 0.87,
			"database.rows-close-missing"))
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
	if !strings.Contains(s, "os.Open(") && !strings.Contains(s, "http.Get(") {
		return false
	}
	names := lifecycleResourceNames(s)
	if len(names) == 0 {
		return true
	}
	for _, name := range names {
		if resourceClosedOrTransferred(s, name) {
			continue
		}
		return true
	}
	return false
}

var ignoredErrRE = regexp.MustCompile(`(^|[^A-Za-z0-9_]),\s*_\s*:=|(^|[^A-Za-z0-9_]),\s*_\s*=`)

func isIgnoredError(s string) bool {
	return (ignoredErrRE.MatchString(s) || strings.Contains(s, " _ := risky(")) &&
		!strings.Contains(s, "rows, _ :=")
}

func isRowsLifecycle(s string) bool {
	if !strings.Contains(s, ".Query(") {
		return false
	}
	for _, name := range queryResultNames(s) {
		if strings.Contains(s, name+".Close()") {
			return false
		}
	}
	return true
}

var lifecycleAssignmentRE = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\s*,\s*[^:=\n]+:=\s*(?:os\.Open|http\.Get)\(`)
var queryAssignmentRE = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\s*,\s*[^:=\n]+:=\s*[^\n;]*\.Query\(`)

func resourceClosedOrTransferred(s, name string) bool {
	if strings.Contains(s, name+".Close()") {
		return true
	}
	return resourceReturnedRE(name).MatchString(s)
}

func resourceReturnedRE(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^\s*return\s+(?:[^\n,]+,\s*)*` + regexp.QuoteMeta(name) + `(?:\s*,|\s*$)`)
}

func lifecycleResourceNames(s string) []string {
	matches := lifecycleAssignmentRE.FindAllStringSubmatch(s, -1)
	names := make([]string, 0, len(matches))
	for _, match := range matches {
		names = append(names, match[1])
	}
	return names
}

func queryResultNames(s string) []string {
	matches := queryAssignmentRE.FindAllStringSubmatch(s, -1)
	names := make([]string, 0, len(matches))
	for _, match := range matches {
		names = append(names, match[1])
	}
	return names
}

func lifecycleBlocks(lines []input.AddedLine) [][]input.AddedLine {
	var blocks [][]input.AddedLine
	var current []input.AddedLine
	depth := 0
	inFunction := false
	flush := func() {
		if len(current) > 0 {
			blocks = append(blocks, current)
			current = nil
		}
		depth = 0
		inFunction = false
	}
	for _, line := range lines {
		if strings.Contains(line.Text, "func ") && len(current) > 0 && !inFunction {
			flush()
		}
		current = append(current, line)
		if strings.Contains(line.Text, "func ") {
			inFunction = true
		}
		depth += braceDelta(line.Text)
		if inFunction && depth <= 0 {
			flush()
		}
	}
	flush()
	return blocks
}

func braceDelta(s string) int {
	return strings.Count(s, "{") - strings.Count(s, "}")
}

func packageKey(f input.FileDiff) string {
	path := f.NewPath
	if path == "" {
		path = f.OldPath
	}
	dir := filepath.ToSlash(filepath.Dir(path))
	if dir == "." {
		dir = ""
	}
	pkg := f.Package
	if pkg == "" {
		for _, line := range f.Added {
			if line.Package != "" {
				pkg = line.Package
				break
			}
		}
	}
	pkg = strings.TrimSuffix(pkg, "_test")
	if pkg == "" {
		return dir
	}
	if dir == "" {
		return pkg
	}
	return dir + ":" + pkg
}

func sortedPackageKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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
