//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package internal

import (
	"regexp"
	"sort"
	"strings"
)

// Severity levels for findings.
const (
	SeverityCritical = "critical"
	SeverityHigh     = "high"
	SeverityMedium   = "medium"
	SeverityLow      = "low"
)

// Source types for findings.
const (
	SourceRule        = "rule"
	SourceSandbox     = "sandbox"
	SourceStaticcheck = "staticcheck"
)

// ConfidenceThreshold is the minimum confidence for a finding to be
// included in the main findings list. Findings below this threshold
// are moved to warnings.
const ConfidenceThreshold = 0.5

// Finding represents a single code review issue.
type Finding struct {
	ID             string  `json:"id"`
	Severity       string  `json:"severity"`
	Category       string  `json:"category"`
	File           string  `json:"file"`
	Line           int     `json:"line"`
	Title          string  `json:"title"`
	Evidence       string  `json:"evidence"`
	Recommendation string  `json:"recommendation"`
	Confidence     float64 `json:"confidence"`
	Source         string  `json:"source"`
	RuleID         string  `json:"rule_id"`
}

// Warning is a low-confidence finding that needs human review.
type Warning Finding

// dedupKey returns the key used for de-duplication.
// Same (file, line, category) → same key.
func (f Finding) dedupKey() string {
	return f.File + ":" + itoa(f.Line) + ":" + f.Category
}

// DedupFindings removes duplicate findings with the same
// (file, line, category), keeping only the one with the highest
// confidence. It also sorts the result by severity (critical first)
// then by file and line.
func DedupFindings(findings []Finding) []Finding {
	if len(findings) == 0 {
		return nil
	}
	best := make(map[string]Finding, len(findings))
	for _, f := range findings {
		k := f.dedupKey()
		existing, ok := best[k]
		if !ok || f.Confidence > existing.Confidence {
			best[k] = f
		}
	}
	result := make([]Finding, 0, len(best))
	for _, f := range best {
		result = append(result, f)
	}
	sortFindings(result)
	return result
}

// SplitFindings separates findings into high-confidence findings and
// low-confidence warnings based on ConfidenceThreshold.
func SplitFindings(findings []Finding) (confirmed []Finding, warnings []Warning) {
	for _, f := range findings {
		if f.Confidence >= ConfidenceThreshold {
			confirmed = append(confirmed, f)
		} else {
			warnings = append(warnings, Warning(f))
		}
	}
	sortFindings(confirmed)
	return confirmed, warnings
}

var severityOrder = map[string]int{
	SeverityCritical: 0,
	SeverityHigh:     1,
	SeverityMedium:   2,
	SeverityLow:      3,
}

func sortFindings(f []Finding) {
	sort.Slice(f, func(i, j int) bool {
		si := severityOrder[f[i].Severity]
		sj := severityOrder[f[j].Severity]
		if si != sj {
			return si < sj
		}
		if f[i].File != f[j].File {
			return f[i].File < f[j].File
		}
		return f[i].Line < f[j].Line
	})
}

// CountBySeverity returns a map of severity → count.
func CountBySeverity(findings []Finding) map[string]int {
	m := map[string]int{
		SeverityCritical: 0,
		SeverityHigh:     0,
		SeverityMedium:   0,
		SeverityLow:      0,
	}
	for _, f := range findings {
		m[f.Severity]++
	}
	return m
}

// --- Sensitive information redaction ---

// redactedValue is the replacement for detected secrets.
const redactedValue = "***REDACTED***"

// sensitivePatterns are pre-compiled regexes for common secret formats.
var sensitivePatterns = []*regexp.Regexp{
	// API keys: api_key = "xxx", API_KEY := "xxx", apiKey: "xxx"
	regexp.MustCompile(`(?i)(api[_-]?key|apikey)\s*[:=]\s*["']?[A-Za-z0-9_\-]{16,}["']?`),
	// Token: token = "xxx", ACCESS_TOKEN := "xxx"
	regexp.MustCompile(`(?i)(access[_-]?token|auth[_-]?token|token)\s*[:=]\s*["']?[A-Za-z0-9_\-\.]{20,}["']?`),
	// Password: password = "xxx", passwd := "xxx"
	regexp.MustCompile(`(?i)(password|passwd|pwd)\s*[:=]\s*["']?[^\s"']{4,}["']?`),
	// Secret: secret = "xxx"
	regexp.MustCompile(`(?i)(secret[_-]?key|secret)\s*[:=]\s*["']?[A-Za-z0-9_\-]{8,}["']?`),
	// Bearer tokens: Bearer xxx
	regexp.MustCompile(`(?i)(bearer)\s+[A-Za-z0-9_\-\.]{20,}`),
	// AWS-style keys
	regexp.MustCompile(`(?i)(aws[_-]?(access[_-]?key|secret))\s*[:=]\s*["']?[A-Za-z0-9/+=]{16,}["']?`),
	// Private key markers
	regexp.MustCompile(`-----BEGIN (RSA |EC |DSA |OPENSSH |)PRIVATE KEY-----`),
	// Connection string with password: postgres://user:password@host
	regexp.MustCompile(`(?i)(postgres|mysql|redis|mongodb)://[^:]+:[^@]+@`),
	// Hardcoded credentials in Go const/var: const APIKey = "..."
	regexp.MustCompile(`(?i)(apikey|api_key|token|password|secret)\s*(?:string\s*)?=\s*"[^"]{8,}"`),
}

// RedactSensitiveInfo replaces API keys, tokens, passwords and other
// sensitive values in the input string with ***REDACTED***.
func RedactSensitiveInfo(input string) string {
	for _, re := range sensitivePatterns {
		input = re.ReplaceAllString(input, "$1="+redactedValue)
	}
	return input
}

// ContainsSensitiveInfo reports whether the input contains any
// pattern that looks like a secret.
func ContainsSensitiveInfo(input string) bool {
	for _, re := range sensitivePatterns {
		if re.MatchString(input) {
			return true
		}
	}
	return false
}

// itoa is a lightweight int-to-string to avoid strconv import in tests.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// trimSpace is a lightweight helper.
func trimSpace(s string) string {
	return strings.TrimSpace(s)
}
