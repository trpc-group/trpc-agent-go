//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights
// reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package redact redacts sensitive information (API keys, passwords,
// private keys, tokens, etc.) from text across the code-review pipeline:
// diff content, planned commands, finding fields, and sandbox output.
package redact

import (
	"bytes"
	"io"
	"regexp"
)

type redactPattern struct {
	re          *regexp.Regexp
	replacement string
}

// Package-level compiled patterns. Apply sequentially.
var patterns = []redactPattern{
	{regexp.MustCompile(`(?i)(api[_-]?key|apikey)\s*[=:]\s*["']?[A-Za-z0-9_\-]{16,}["']?`), "[REDACTED:api_key]"},
	{regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9_\-\.=]{20,}`), "[REDACTED:bearer]"},
	{regexp.MustCompile(`(?i)(password|passwd|pwd)\s*[=:]\s*["']?[^\s"']{4,}["']?`), "[REDACTED:password]"},
	{regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`), "[REDACTED:private_key]"},
	{regexp.MustCompile(`\b(?:\d[ -]*?){13,16}\b`), "[REDACTED:credit_card]"},
	// DB schemes first (more specific), then generic URL userinfo so
	// credentialed proxies (GOPROXY, HTTPS feeds) are scrubbed before
	// sandbox stdout / reports are persisted.
	{regexp.MustCompile(`(?i)(postgres|mysql|mongodb|redis)://[^\s"']{8,}`), "[REDACTED:connection_string]"},
	{regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^/\s"'@]+:[^/\s"'@]+@[^\s"']+`), "[REDACTED:url_userinfo]"},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{8,}\.eyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}\b`), "[REDACTED:jwt]"},
	{regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), "[REDACTED:aws_access_key]"},
	{regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9\-]{10,}`), "[REDACTED:slack_token]"},
	{regexp.MustCompile(`(?i)(secret|token|key|auth)\s*[=:]\s*["']?[A-Za-z0-9_\-]{8,}["']?`), "[REDACTED:secret]"},
	{regexp.MustCompile(`\bghp_[A-Za-z0-9]{36}\b`), "[REDACTED:github_pat]"},
}

func applyPatterns(s string, pats []redactPattern) (string, int) {
	count := 0
	for _, p := range pats {
		newS := p.re.ReplaceAllString(s, p.replacement)
		count += len(p.re.FindAllStringIndex(s, -1))
		s = newS
	}
	return s, count
}

// Text redacts sensitive patterns in s, returning the redacted string
// and the total count of replacements.
func Text(s string) (string, int) {
	return applyPatterns(s, patterns)
}

// TextBytes is the byte-slice form of Text.
func TextBytes(b []byte) ([]byte, int) {
	s, n := Text(string(b))
	return []byte(s), n
}

// MustText redacts and returns only the string (never panics).
func MustText(s string) string {
	out, _ := Text(s)
	return out
}

// DiffText redacts a diff string.
func DiffText(s string) (string, int) { return Text(s) }

// CommandText redacts a planned command line.
func CommandText(s string) (string, int) { return Text(s) }

// FindingFields redacts evidence and recommendation strings, returning
// the redacted values and total replacement count.
func FindingFields(evidence, recommendation string) (string, string, int) {
	e, n1 := Text(evidence)
	r, n2 := Text(recommendation)
	return e, r, n1 + n2
}

// StreamReader wraps r so redaction is applied as bytes flow through.
// It reads in 4KB chunks, redacts each chunk, and concatenates.
//
// Limitation: patterns that span chunk boundaries (e.g., a multi-line
// PRIVATE KEY block split across two 4KB reads) will not be fully
// redacted, because each chunk is redacted independently. For bounded
// output (max 1MB upstream) this is acceptable. The full-content Text
// function is the primary API; StreamReader is a best-effort streaming
// wrapper. Callers requiring cross-boundary correctness should buffer
// the full content and call Text instead.
func StreamReader(r io.Reader) io.Reader {
	return &streamRedactor{r: r, buf: bytes.Buffer{}}
}

type streamRedactor struct {
	r       io.Reader
	buf     bytes.Buffer
	pending error // stored non-EOF error, returned once buf is drained
}

func (s *streamRedactor) Read(p []byte) (int, error) {
	if s.buf.Len() == 0 {
		if s.pending != nil {
			return 0, s.pending
		}
		chunk := make([]byte, 4096)
		n, err := s.r.Read(chunk)
		if n > 0 {
			redacted, _ := TextBytes(chunk[:n])
			s.buf.Write(redacted)
		}
		if err != nil && err != io.EOF {
			s.pending = err
		}
		if err == io.EOF && n == 0 && s.buf.Len() == 0 {
			return 0, io.EOF
		}
	}
	return s.buf.Read(p)
}
