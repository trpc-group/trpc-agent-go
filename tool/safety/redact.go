//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"regexp"
	"unicode/utf8"
)

const (
	redactedValue  = "[REDACTED]"
	truncatedValue = "\n[TRUNCATED BY TOOL SAFETY POLICY]"
)

var sensitivePatterns = []struct {
	regex       *regexp.Regexp
	replacement string
}{
	{
		regexp.MustCompile(
			`(?is)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?` +
				`-----END [A-Z0-9 ]*PRIVATE KEY-----`,
		),
		redactedValue,
	},
	{
		regexp.MustCompile(
			`(?i)\b(api[_-]?key|access[_-]?token|auth[_-]?token|` +
				`token|password|passwd|secret)(\s*[:=]\s*)` +
				`[^\s,;"']+`,
		),
		`${1}${2}` + redactedValue,
	},
	{
		regexp.MustCompile(`(?i)\bbearer\s+[a-z0-9._~+/=-]{8,}`),
		"Bearer " + redactedValue,
	},
	{
		regexp.MustCompile(`\bAKIA[A-Z0-9]{16}\b`),
		redactedValue,
	},
	{
		regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}\b`),
		redactedValue,
	},
}

func redactText(value string, maxBytes int) (
	out string,
	redacted bool,
	truncated bool,
) {
	out = value
	for _, pattern := range sensitivePatterns {
		replaced := pattern.regex.ReplaceAllString(out, pattern.replacement)
		if replaced != out {
			redacted = true
			out = replaced
		}
	}
	if maxBytes <= 0 || len(out) <= maxBytes {
		return out, redacted, false
	}
	out = utf8Prefix(out, maxBytes) + truncatedValue
	return out, redacted, true
}

func containsSensitiveLiteral(value string) bool {
	_, redacted, _ := redactText(value, 0)
	return redacted
}

func utf8Prefix(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	prefix := value[:maxBytes]
	for !utf8.ValidString(prefix) {
		prefix = prefix[:len(prefix)-1]
	}
	return prefix
}
