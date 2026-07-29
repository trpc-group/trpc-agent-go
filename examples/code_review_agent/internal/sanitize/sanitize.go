// sanitize provides WriteInterceptor hooks for sensitive data redaction.
// Called at five mandatory checkpoints per design spec:
//
//	DiffParser → SandboxRunner → LLMAnalyzer → ReportGenerator → StorageWriter
package sanitize

import (
	"regexp"
	"strings"
)

// DefaultPatterns is the built-in set of sensitive data patterns.
var DefaultPatterns = []string{
	`(?i)(api[_-]?key|secret|token|password|passwd)\s*[:=]\s*["'][A-Za-z0-9_\-\.]{8,}["']`,
	`(?i)(sk-[A-Za-z0-9_\-]{20,})`,
	`(?i)(Bearer\s+[A-Za-z0-9_\-\.]{20,})`,
	`(?i)(-----BEGIN\s+(RSA\s+)?PRIVATE\s+KEY-----)`,
}

// Redactor performs regex-based sensitive data redaction.
type Redactor struct {
	patterns    []*regexp.Regexp
	replacement string
}

// NewRedactor creates a Redactor with the given patterns and replacement string.
func NewRedactor(patterns []string, replacement string) *Redactor {
	if replacement == "" {
		replacement = "***REDACTED***"
	}
	if len(patterns) == 0 {
		patterns = DefaultPatterns
	}
	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err == nil {
			compiled = append(compiled, re)
		}
	}
	return &Redactor{patterns: compiled, replacement: replacement}
}

// Redact applies all patterns to the input string and returns the sanitized version.
func (r *Redactor) Redact(s string) string {
	for _, re := range r.patterns {
		s = re.ReplaceAllString(s, r.replacement)
	}
	return s
}

// RedactFinding sanitizes sensitive data in a finding's Evidence field.
func (r *Redactor) RedactFinding(evidence string) string {
	return r.Redact(evidence)
}

// RedactSandboxOutput sanitizes sandbox stdout/stderr.
func (r *Redactor) RedactSandboxOutput(output string) string {
	return r.Redact(output)
}

// RedactReport sanitizes the entire report text before writing.
func (r *Redactor) RedactReport(report string) string {
	return r.Redact(report)
}

// ContainsSensitive checks whether the text likely contains sensitive data.
func (r *Redactor) ContainsSensitive(s string) bool {
	for _, re := range r.patterns {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

// SanitizeFinding sanitizes a single finding's evidence in-place.
func SanitizeFinding(evidence string, patterns []string, replacement string) string {
	r := NewRedactor(patterns, replacement)
	return r.RedactFinding(evidence)
}

// SanitizeOutput sanitizes sandbox stdout/stderr in-place.
func SanitizeOutput(output string, patterns []string, replacement string) string {
	r := NewRedactor(patterns, replacement)
	return r.RedactSandboxOutput(output)
}

// DiffsPresent reports whether anything was redacted by comparing before/after.
func DiffsPresent(before, after string) bool {
	return before != after
}

// Summary produces a one-line report of what was redacted.
func Summary(before, after string) string {
	if before == after {
		return "no sensitive data detected"
	}
	var redacted []string
	for _, p := range DefaultPatterns {
		re := regexp.MustCompile(p)
		matches := re.FindAllString(before, -1)
		for _, m := range matches {
			redacted = append(redacted, m[:min(len(m), 40)]+"...")
		}
	}
	if len(redacted) == 0 {
		return "sensitive data redacted"
	}
	return "redacted " + strings.Join(redacted, ", ")
}
