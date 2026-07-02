//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"regexp"
	"strings"
)

// Redactor masks credentials and other sensitive material that may appear in
// commands, code blocks, or audit log entries.
//
// The default redactor replaces secret-like substrings with "***REDACTED***"
// while preserving the surrounding context (key name, prefix, suffix length).
type Redactor struct {
	// patterns maps a regex (case-insensitive) to the replacement value.
	patterns []*redactPattern
	// Replacement is the placeholder used for redacted text.
	Replacement string
	// KeepBoundaryLen controls how many characters of context are kept on
	// each side of a redaction, in addition to the matched key name.
	KeepBoundaryLen int
}

// redactPattern pairs a literal substring (preferred, no backtracking)
// with a compiled regex (fallback for non-literal shapes like JWTs and
// AWS key ids). Exactly one of exact and re is set per entry.
type redactPattern struct {
	// re is the compiled regex used when exact is empty.
	re *regexp.Regexp
	// exact is the literal substring; if non-empty, the match is done by
	// plain string replace (faster, no regex backtracking) using the same
	// surrounding boundary logic.
	exact string
}

// NewRedactor returns a Redactor with the default credential patterns
// (API keys, tokens, passwords, JWTs, AWS keys, GitHub PATs, etc.).
func NewRedactor() *Redactor {
	r := &Redactor{
		Replacement:     "***REDACTED***",
		KeepBoundaryLen: 4,
	}
	r.patterns = []*redactPattern{
		// KEY=VALUE or KEY="VALUE" style assignments.
		{exact: "api_key="},
		{exact: "apikey="},
		{exact: "api_secret="},
		{exact: "apisecret="},
		{exact: "access_key="},
		{exact: "secret_key="},
		{exact: "private_key="},
		{exact: "password="},
		{exact: "passwd="},
		{exact: "passphrase="},
		{exact: "db_password="},
		{exact: "db_pass="},
		{exact: "auth_token="},
		{exact: "refresh_token="},
		{exact: "token="},
		// Quoted bearer tokens: "Authorization: Bearer <token>".
		{re: regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._\-]{8,}`)},
		// AWS access key id.
		{re: regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
		// GitHub personal access token (ghp_ / gho_ / ghu_ / ghs_ / ghr_).
		{re: regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36}\b`)},
		// Generic JWT (3 dot-separated base64url segments).
		{re: regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]+\.eyJ[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+\b`)},
	}
	return r
}

// Redact returns a copy of s with credential-like substrings replaced by
// the redactor's Replacement placeholder.
func (r *Redactor) Redact(s string) string {
	if s == "" {
		return s
	}
	out := s
	for _, p := range r.patterns {
		if p.exact != "" {
			out = redactLiteral(out, p.exact, r.Replacement, r.KeepBoundaryLen)
			continue
		}
		if p.re != nil {
			out = p.re.ReplaceAllStringFunc(out, func(match string) string {
				return r.wrapBoundary(match, r.Replacement)
			})
		}
	}
	return out
}

// RedactCommand is a convenience wrapper for ScanInput.Command.
func (r *Redactor) RedactCommand(cmd string) string {
	return r.Redact(cmd)
}

// RedactReport returns a ScanReport with sensitive fields redacted.
func (r *Redactor) RedactReport(report ScanReport) ScanReport {
	report.Command = r.Redact(report.Command)
	report.Evidence = r.Redact(report.Evidence)
	report.Reason = r.Redact(report.Reason)
	return report
}

// RedactAuditEvent returns an AuditEvent with sensitive fields redacted
// and the Sanitized flag flipped to true.
func (r *Redactor) RedactAuditEvent(event AuditEvent) AuditEvent {
	event.Command = r.Redact(event.Command)
	event.Evidence = r.Redact(event.Evidence)
	event.Sanitized = true
	return event
}

// wrapBoundary keeps at most n chars of prefix/suffix from the original match
// to provide audit context without leaking the full secret.
func (r *Redactor) wrapBoundary(match, replacement string) string {
	n := r.KeepBoundaryLen
	if n <= 0 || len(match) <= n*2+len(replacement)+1 {
		return replacement
	}
	prefix := match[:n]
	suffix := match[len(match)-n:]
	return prefix + replacement + suffix
}

// redactLiteral performs a case-insensitive literal replace of literal in s
// while preserving at most n chars of context on each side of the value.
// Optional surrounding quotes are preserved in the output.
func redactLiteral(s, literal, replacement string, keep int) string {
	if literal == "" {
		return s
	}
	lowerS := strings.ToLower(s)
	lowerL := strings.ToLower(literal)
	var out strings.Builder
	cursor := 0
	for {
		idx := strings.Index(lowerS[cursor:], lowerL)
		if idx < 0 {
			out.WriteString(s[cursor:])
			return out.String()
		}
		idx += cursor
		// Emit the text up to (and including) the literal.
		out.WriteString(s[cursor : idx+len(literal)])
		// Detect an optional opening quote immediately after the literal.
		valueStart := idx + len(literal)
		hasOpenQuote := valueStart < len(s) && (s[valueStart] == '"' || s[valueStart] == '\'')
		// Find the end of the value (and a closing quote if present).
		openQuoteChar := byte(0)
		secretStart := valueStart
		var secretEnd int
		if hasOpenQuote {
			openQuoteChar = s[valueStart]
			secretStart = valueStart + 1
			secretEnd = secretStart
			for secretEnd < len(s) && s[secretEnd] != openQuoteChar && s[secretEnd] != '\n' {
				secretEnd++
			}
		} else {
			secretEnd = valueStart
			for secretEnd < len(s) && s[secretEnd] != ' ' && s[secretEnd] != '\n' {
				secretEnd++
			}
		}
		// Emit the open quote (if any) without leaking the value.
		if hasOpenQuote {
			out.WriteByte(openQuoteChar)
		}
		if keep > 0 && secretEnd-secretStart > keep*2 {
			out.WriteString(s[secretStart : secretStart+keep])
			out.WriteString(replacement)
			out.WriteString(s[secretEnd-keep : secretEnd])
		} else {
			out.WriteString(replacement)
		}
		// Emit the closing quote (if any) without leaking the value.
		if hasOpenQuote && secretEnd < len(s) && s[secretEnd] == openQuoteChar {
			out.WriteByte(openQuoteChar)
			secretEnd++
		}
		cursor = secretEnd
	}
}
