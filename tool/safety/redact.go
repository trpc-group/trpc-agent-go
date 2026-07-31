//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package safety

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

const maxReportTextBytes = 4096

var redactionPatterns = []struct {
	re          *regexp.Regexp
	replacement string
}{
	{
		regexp.MustCompile(`(?is)-----BEGIN [^-\r\n]*PRIVATE KEY-----.*?-----END [^-\r\n]*PRIVATE KEY-----`),
		"[REDACTED PRIVATE KEY]",
	},
	{
		regexp.MustCompile(`(?i)\b(authorization\s*[:=]\s*bearer\s+)[A-Za-z0-9._~+/=-]{8,}`),
		`${1}[REDACTED]`,
	},
	{
		regexp.MustCompile(`(?i)\b(api[_-]?key|access[_-]?token|auth[_-]?token|password|passwd|secret|token)(\s*[:=]\s*)('[^']*'|"[^"]*"|[^\s,;]+)`),
		`${1}${2}[REDACTED]`,
	},
	{
		regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]{16,}|gh[pousr]_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16})\b`),
		"[REDACTED TOKEN]",
	},
	{
		regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`),
		"[REDACTED JWT]",
	},
	{
		regexp.MustCompile(`(?i)(https?://[^/@:\s]+:)[^/@\s]+@`),
		`${1}[REDACTED]@`,
	},
}

// Redact replaces common credentials, tokens, passwords, and private keys in
// text. The boolean result reports whether text changed.
func Redact(text string) (string, bool) {
	out := text
	for _, pattern := range redactionPatterns {
		out = pattern.re.ReplaceAllString(out, pattern.replacement)
	}
	return out, out != text
}

func sanitizeReportText(text string) (string, bool) {
	text, changed := Redact(text)
	if len(text) <= maxReportTextBytes {
		return text, changed
	}
	const marker = "\n...[truncated]..."
	limit := maxReportTextBytes - len(marker)
	for limit > 0 && !utf8.ValidString(text[:limit]) {
		limit--
	}
	return strings.TrimSpace(text[:limit]) + marker, true
}
