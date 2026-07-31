//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import "regexp"

var secretPatterns = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	{regexp.MustCompile(`(?i)(api[_-]?key|token|password|secret|client[_-]?secret|access[_-]?token|access[_-]?key)(\s*[:=]\s*)["']?[^\s"']{8,}`), `${1}${2}[REDACTED]`},
	{regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}\b`), `[REDACTED]`},
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}\b`), `[REDACTED]`},
	{regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), `[REDACTED]`},
	{regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`), `[REDACTED]`},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`), `[REDACTED]`},
	{regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._~-]{12,}`), `[REDACTED]`},
	// Header-only detection is intentional: the analyzer passes one changed diff
	// line at a time, so private-key body lines are not matched or flagged alone.
	{regexp.MustCompile(`-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----`), `[REDACTED]`},
}

func redact(text string) (string, bool) {
	redacted := text
	changed := false
	for _, entry := range secretPatterns {
		before := redacted
		redacted = entry.pattern.ReplaceAllString(redacted, entry.replacement)
		changed = changed || before != redacted
	}
	return redacted, changed
}
