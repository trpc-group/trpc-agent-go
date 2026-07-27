//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package redaction removes secrets before data is persisted or reported.
package redaction

import "regexp"

type pattern struct {
	re          *regexp.Regexp
	replacement string
}

var patterns = []pattern{
	{regexp.MustCompile(`(?i)((?:[A-Za-z_][A-Za-z0-9_-]*)?(?:api[_-]?key|token|password|passwd|pwd|secret|credential)[A-Za-z0-9_-]*)(\s*(?::=|=|:)\s*)(?:\\?["'])[^\\"'\r\n]{4,}(?:\\?["'])`), `${1}${2}[REDACTED_SECRET]`},
	{regexp.MustCompile(`(?i)(?:\b)(api[_-]?key|token|password|passwd|pwd|secret|credential)(\s*(?::=|=|:)\s*)[A-Za-z0-9_+/=-]{8,}`), `${1}${2}[REDACTED_SECRET]`},
	{regexp.MustCompile(`(?i)(?:\b)(token)(\s*(?::=|=|:)\s*)[A-Za-z0-9_+/=-]{5,}`), `${1}${2}[REDACTED_SECRET]`},
	{regexp.MustCompile(`\bsk-(?:proj-)?[A-Za-z0-9_-]{16,}\b`), `[REDACTED_SECRET]`},
	{regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9_]{20,}\b`), `[REDACTED_SECRET]`},
	{regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}\b`), `[REDACTED_SECRET]`},
	{regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`), `[REDACTED_SECRET]`},
	{regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), `[REDACTED_SECRET]`},
	{regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{30,}\b`), `[REDACTED_SECRET]`},
	{regexp.MustCompile(`\b(?:sk|rk)_live_[A-Za-z0-9]{16,}\b`), `[REDACTED_SECRET]`},
	{regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{12,}`), `Bearer [REDACTED_SECRET]`},
	{regexp.MustCompile(`(?i)\bBasic\s+[A-Za-z0-9+/]{12,}={0,2}`), `Basic [REDACTED_SECRET]`},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`), `[REDACTED_SECRET]`},
	{regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*)://[^/@\s:]+:[^/@\s]+@`), `${1}://[REDACTED_SECRET]@`},
	{regexp.MustCompile(`(?s)-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----.*?-----END (?:RSA |EC |OPENSSH )?PRIVATE KEY-----`), `[REDACTED_PRIVATE_KEY]`},
	{regexp.MustCompile(`(?i)(tencentcloud_secret(?:id|key))(\s*[:=]\s*)(?:\\?["'])?[^\\"',;\s]+(?:\\?["'])?`), `${1}${2}[REDACTED_SECRET]`},
}

// RedactText replaces likely secrets with a stable placeholder.
func RedactText(s string) string {
	out := s
	for _, p := range patterns {
		out = p.re.ReplaceAllString(out, p.replacement)
	}
	return out
}
