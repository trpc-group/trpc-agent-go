//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import "regexp"

const redacted = "[REDACTED]"

// Redactor removes sensitive data from scan results before output.
// It matches against a set of built-in secret patterns (API keys, tokens,
// private keys, passwords, etc.) and replaces matches with [REDACTED].
type Redactor struct {
	patterns []*regexp.Regexp
}

// NewRedactor creates a Redactor with built-in secret patterns.
// The patterns cover common credential formats:
//   - AWS Access Key IDs (AKIA...)
//   - AWS Secret Access Key assignments
//   - Generic API key / access key assignments
//   - PEM private key headers
//   - Bearer tokens
//   - Passwords in URLs
//   - Generic token / password / secret assignments
//   - GitHub Personal Access Tokens (ghp_...)
//   - Slack tokens (xox[baprs]-...)
func NewRedactor() *Redactor {
	patterns := []string{
		// AWS Access Key ID
		`AKIA[0-9A-Z]{16}`,
		// AWS Secret Access Key
		`(?i)aws_secret_access_key\s*[=:]\s*\S+`,
		// Generic API key / access key
		`(?i)(api[_-]?key|apikey|access[_-]?key)\s*[=:]\s*['"]?\w{20,}`,
		// PEM private key block
		`-----BEGIN(?: [A-Z0-9]+)* PRIVATE KEY-----[\s\S]+?-----END(?: [A-Z0-9]+)* PRIVATE KEY-----`,
		// Bearer token
		`(?i)bearer\s+[A-Za-z0-9\-._~+/]+=*`,
		// Password in URL
		`://[^/:]+:[^/@]+@`,
		// Generic token / password / secret assignment
		`(?i)(token|password|passwd|secret)\s*[=:]\s*['"]?\S{8,}`,
		// GitHub Personal Access Token
		`ghp_[A-Za-z0-9]{36}`,
		// Slack token
		`xox[baprs]-[A-Za-z0-9\-]+`,
	}

	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re := regexp.MustCompile(p)
		compiled = append(compiled, re)
	}
	return &Redactor{patterns: compiled}
}

// RedactString replaces detected secrets in s with [REDACTED].
func (r *Redactor) RedactString(s string) string {
	for _, re := range r.patterns {
		s = re.ReplaceAllString(s, redacted)
	}
	return s
}

// RedactFindings redacts Evidence fields in a copy of the findings slice.
func (r *Redactor) RedactFindings(findings []Finding) []Finding {
	result := make([]Finding, len(findings))
	for i, f := range findings {
		result[i] = f
		result[i].Evidence = r.RedactString(f.Evidence)
	}
	return result
}

// RedactReport redacts sensitive data in a report in place.
// It redacts the Command field and the Evidence in each Finding.
func (r *Redactor) RedactReport(report *Report) {
	if report == nil {
		return
	}
	report.Command = r.RedactString(report.Command)
	report.Findings = r.RedactFindings(report.Findings)
}

// RedactAuditEvent redacts sensitive data in an audit event in place.
// Currently, audit events do not carry free-text evidence fields,
// so this is a no-op placeholder for future fields.
func (r *Redactor) RedactAuditEvent(event *AuditEvent) {
	// AuditEvent does not contain free-text evidence fields to redact.
	// This method exists for forward compatibility.
}
