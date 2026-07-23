//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package rules implements deterministic Go code-review checks.
package rules

import (
	"regexp"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/input"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/safety"
)

// Engine runs deterministic review rules against a DiffBundle.
type Engine struct{}

// Analyze runs all built-in rules.
func (Engine) Analyze(bundle *input.DiffBundle) []review.Finding {
	if bundle == nil {
		return nil
	}
	var out []review.Finding
	out = append(out, checkSecurity(bundle)...)
	out = append(out, checkSecrets(bundle)...)
	out = append(out, checkConcurrency(bundle)...)
	out = append(out, checkResource(bundle)...)
	out = append(out, checkDB(bundle)...)
	out = append(out, checkErrorHandling(bundle)...)
	out = append(out, checkMissingTests(bundle)...)
	return out
}

var (
	reDiscardedCall = regexp.MustCompile(`^\s*_\s*=\s*[^=].*\(.*\)`)
	reDiscardedBoth = regexp.MustCompile(`^\s*_,\s*_\s*=`)
	// e.g. tx, _ := db.Begin() — error return discarded.
	reDiscardedRHS = regexp.MustCompile(`,\s*_\s*(:=|=)\s*.+\(`)
	rePanicCall    = regexp.MustCompile(`\bpanic\s*\(`)
	reGoFunc       = regexp.MustCompile(`\bgo\s+func\s*\(`)
	reSQLConcat    = regexp.MustCompile(`(?i)(SELECT|INSERT|UPDATE|DELETE).*\+`)
	reInsecureTLS  = regexp.MustCompile(`InsecureSkipVerify\s*:\s*true`)
	reOpeners      = []*regexp.Regexp{
		regexp.MustCompile(`\bos\.Open\(`),
		regexp.MustCompile(`\bos\.OpenFile\(`),
		regexp.MustCompile(`\bhttp\.(Get|Post)\(`),
		regexp.MustCompile(`\.Query\(`),
		regexp.MustCompile(`\.QueryContext\(`),
	}
)

// checkErrorHandling detects discarded call errors and panic usage.
func checkErrorHandling(bundle *input.DiffBundle) []review.Finding {
	var out []review.Finding
	forEachAdded(bundle, func(file string, line int, text string) {
		trim := strings.TrimSpace(text)
		if isDiscardedCallError(trim) {
			out = append(out, finding(
				review.SeverityMedium, "error_handling", file, line,
				"Function call error discarded",
				text,
				"Handle the returned error instead of assigning to _.",
				0.72, "CR-ERR-001",
			))
		}
		if rePanicCall.MatchString(trim) && !strings.Contains(strings.ToLower(file), "test") {
			out = append(out, finding(
				review.SeverityMedium, "error_handling", file, line,
				"panic used in non-test code",
				text,
				"Return an error to the caller instead of panicking in library/business code.",
				0.7, "CR-ERR-001",
			))
		}
	})
	return out
}

// isDiscardedCallError reports discarded call errors such as `_ = foo(...)`,
// `_, _ = foo(...)`, or `tx, _ := db.Begin()`.
func isDiscardedCallError(trim string) bool {
	if trim == "" || !strings.Contains(trim, "(") {
		return false
	}
	// Explicit error binding is fine even if other values are discarded.
	if bindsErrorIdent(trim) {
		return false
	}
	if reDiscardedBoth.MatchString(trim) || reDiscardedRHS.MatchString(trim) {
		return true
	}
	return reDiscardedCall.MatchString(trim)
}

// bindsErrorIdent reports whether the statement binds an err identifier.
func bindsErrorIdent(trim string) bool {
	// Common Go error-binding shapes on the same statement.
	markers := []string{
		", err ", ", err:", ", err=", ", err:=",
		" err ", " err:", " err=", " err:=",
	}
	for _, m := range markers {
		if strings.Contains(trim, m) {
			return true
		}
	}
	return strings.HasSuffix(trim, "err")
}

// checkSecurity detects SQL concatenation and insecure TLS settings.
func checkSecurity(bundle *input.DiffBundle) []review.Finding {
	var out []review.Finding
	forEachAdded(bundle, func(file string, line int, text string) {
		if reSQLConcat.MatchString(text) && strings.Contains(text, "+") {
			out = append(out, finding(
				review.SeverityHigh, "security", file, line,
				"Possible SQL string concatenation",
				text,
				"Use parameterized queries or a query builder instead of string concatenation.",
				0.9, "CR-SEC-001",
			))
		}
		if reInsecureTLS.MatchString(text) {
			out = append(out, finding(
				review.SeverityCritical, "security", file, line,
				"TLS InsecureSkipVerify enabled",
				text,
				"Do not disable certificate verification in production code.",
				0.95, "CR-SEC-001",
			))
		}
	})
	return out
}

// checkSecrets detects hard-coded secrets in added lines.
func checkSecrets(bundle *input.DiffBundle) []review.Finding {
	var out []review.Finding
	forEachAdded(bundle, func(file string, line int, text string) {
		if safety.ContainsSecret(text) {
			out = append(out, finding(
				review.SeverityCritical, "secrets", file, line,
				"Hard-coded secret detected",
				safety.Redact(text),
				"Remove secrets from source; load from a secret manager or environment at runtime.",
				0.95, "CR-SEC-002",
			))
		}
	})
	return out
}

// checkConcurrency detects goroutines started without a derived context.
func checkConcurrency(bundle *input.DiffBundle) []review.Finding {
	var out []review.Finding
	for _, f := range bundle.Files {
		if f.Language != "go" {
			continue
		}
		added := collectAdded(f)
		for i, l := range added {
			if !reGoFunc.MatchString(l.Text) {
				continue
			}
			window := joinWindow(added, i, 4)
			if strings.Contains(window, "ctx") || strings.Contains(window, "context.") {
				continue
			}
			out = append(out, finding(
				review.SeverityHigh, "concurrency", f.Path, l.NewLineNo,
				"goroutine started without derived context",
				l.Text,
				"Pass a derived context and ensure the goroutine exits on cancel to avoid leaks.",
				0.86, "CR-CON-001",
			))
		}
	}
	return out
}

// checkResource detects opened resources without a nearby Close.
func checkResource(bundle *input.DiffBundle) []review.Finding {
	var out []review.Finding
	for _, f := range bundle.Files {
		if f.Language != "go" {
			continue
		}
		added := collectAdded(f)
		for i, l := range added {
			if !matchesAny(reOpeners, l.Text) {
				continue
			}
			// Look ahead in the added hunk for Close / defer Close.
			if hasCloseNearby(added, i, 20) {
				continue
			}
			out = append(out, finding(
				review.SeverityHigh, "resource", f.Path, l.NewLineNo,
				"Resource opened without Close",
				l.Text,
				"Ensure the resource is closed with defer Close() on all paths.",
				0.85, "CR-RES-001",
			))
		}
	}
	return out
}

// checkDB detects database handles and transactions without cleanup.
func checkDB(bundle *input.DiffBundle) []review.Finding {
	var out []review.Finding
	for _, f := range bundle.Files {
		if f.Language != "go" {
			continue
		}
		added := collectAdded(f)
		for i, l := range added {
			if strings.Contains(l.Text, "sql.Open(") || strings.Contains(l.Text, "sql.OpenDB(") {
				if !hasCloseNearby(added, i, 30) {
					out = append(out, finding(
						review.SeverityHigh, "db", f.Path, l.NewLineNo,
						"Database handle opened without Close",
						l.Text,
						"Close *sql.DB when finished, typically with defer db.Close().",
						0.88, "CR-DB-001",
					))
				}
			}
			if strings.Contains(l.Text, "Begin(") || strings.Contains(l.Text, "BeginTx(") {
				window := joinWindow(added, i, 25)
				if !strings.Contains(window, "Commit(") && !strings.Contains(window, "Rollback(") {
					out = append(out, finding(
						review.SeverityHigh, "db", f.Path, l.NewLineNo,
						"Transaction started without Commit/Rollback",
						l.Text,
						"Always Commit or Rollback the transaction, preferably with defer Rollback.",
						0.87, "CR-DB-001",
					))
				}
			}
		}
	}
	return out
}

// checkMissingTests flags changed Go files that lack corresponding tests in the diff.
func checkMissingTests(bundle *input.DiffBundle) []review.Finding {
	var out []review.Finding
	for _, path := range bundle.AddedGoFiles() {
		if bundle.HasTestFile(path) {
			continue
		}
		line := 1
		ev := path
		for _, f := range bundle.Files {
			if f.Path != path {
				continue
			}
			for _, h := range f.Hunks {
				for _, l := range h.Lines {
					if l.Kind == '+' {
						line = l.NewLineNo
						ev = l.Text
						break
					}
				}
			}
		}
		out = append(out, finding(
			review.SeverityMedium, "testing", path, line,
			"Changed Go file has no corresponding test in the diff",
			ev,
			"Add or update a _test.go covering the changed behavior.",
			0.75, "CR-TEST-001",
		))
	}
	return out
}

// finding constructs a Finding with the provided fields.
func finding(sev, cat, file string, line int, title, evidence, rec string, conf float64, rule string) review.Finding {
	return review.Finding{
		Severity:       sev,
		Category:       cat,
		File:           file,
		Line:           line,
		Title:          title,
		Evidence:       strings.TrimSpace(evidence),
		Recommendation: rec,
		Confidence:     conf,
		Source:         "rule",
		RuleID:         rule,
	}
}

// forEachAdded calls fn for every added line in the bundle.
func forEachAdded(bundle *input.DiffBundle, fn func(file string, line int, text string)) {
	for _, f := range bundle.Files {
		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				if l.Kind == '+' {
					fn(f.Path, l.NewLineNo, l.Text)
				}
			}
		}
	}
}

// collectAdded returns all added DiffLine values for a file.
func collectAdded(f input.ChangedFile) []input.DiffLine {
	var out []input.DiffLine
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			if l.Kind == '+' {
				out = append(out, l)
			}
		}
	}
	return out
}

// matchesAny reports whether text matches any of the regexps.
func matchesAny(res []*regexp.Regexp, text string) bool {
	for _, re := range res {
		if re.MatchString(text) {
			return true
		}
	}
	return false
}

// joinWindow joins up to n added lines starting at start into one string.
func joinWindow(lines []input.DiffLine, start, n int) string {
	end := start + n
	if end > len(lines) {
		end = len(lines)
	}
	var b strings.Builder
	for _, l := range lines[start:end] {
		b.WriteString(l.Text)
		b.WriteByte('\n')
	}
	return b.String()
}

// hasCloseNearby reports whether Close appears within a nearby window.
func hasCloseNearby(lines []input.DiffLine, start, n int) bool {
	window := joinWindow(lines, start, n)
	return strings.Contains(window, ".Close()") ||
		(strings.Contains(window, "defer") && strings.Contains(window, "Close"))
}

// DedupKey builds the deduplication key for a finding.
func DedupKey(f review.Finding) string {
	return strings.ToLower(f.File) + "|" + strconv.Itoa(f.Line) + "|" + f.RuleID
}

// Dedup merges findings with the same file|line|rule_id.
func Dedup(in []review.Finding) []review.Finding {
	best := make(map[string]review.Finding, len(in))
	order := make([]string, 0, len(in))
	for _, f := range in {
		k := DedupKey(f)
		prev, ok := best[k]
		if !ok {
			best[k] = f
			order = append(order, k)
			continue
		}
		if f.Confidence > prev.Confidence ||
			(f.Confidence == prev.Confidence && len(f.Evidence) > len(prev.Evidence)) {
			best[k] = f
		}
	}
	out := make([]review.Finding, 0, len(order))
	for _, k := range order {
		out = append(out, best[k])
	}
	return out
}

// Classify splits findings by confidence threshold.
func Classify(in []review.Finding, threshold float64) (findings, warnings []review.Finding) {
	if threshold <= 0 {
		threshold = 0.75
	}
	for _, f := range in {
		switch {
		case f.Confidence >= threshold:
			findings = append(findings, f)
		case f.Confidence >= 0.40:
			warnings = append(warnings, f)
		}
	}
	return findings, warnings
}

// RedactFindings redacts sensitive text in finding fields.
func RedactFindings(in []review.Finding) []review.Finding {
	out := make([]review.Finding, len(in))
	for i, f := range in {
		f.Evidence = safety.Redact(f.Evidence)
		f.Title = safety.Redact(f.Title)
		f.Recommendation = safety.Redact(f.Recommendation)
		out[i] = f
	}
	return out
}
