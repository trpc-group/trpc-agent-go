//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var redactionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(password|passwd|token|api[_-]?key|client[_-]?secret|private[_-]?key|secret)(\s*[:=]\s*)(?:"[^"]*"|'[^']*'|[^\s,;]+)`),
	regexp.MustCompile(`(?i)\b(gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|sk-[A-Za-z0-9_-]{16,}|xox[baprs]-[A-Za-z0-9-]{16,})\b`),
	regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`),
	regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{16,}`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`),
	regexp.MustCompile(`(?i)(postgres(?:ql)?|mysql)://[^\s"']+`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`),
}

var secretLiteralAssignment = regexp.MustCompile(
	`(?i)\b(?:password|passwd|token|api[_-]?key|apikey|client[_-]?secret|secret|private[_-]?key)\b\s*(?::=|=|:)\s*("[^"]+"|'[^']+'|` + "`[^`]+`" + `)`,
)

func redact(value string) string {
	for index, pattern := range redactionPatterns {
		if index == 0 {
			value = pattern.ReplaceAllString(value, `$1$2"[REDACTED]"`)
		} else {
			value = pattern.ReplaceAllString(value, `[REDACTED]`)
		}
	}
	return value
}

func truncate(value string, limit int) (string, bool) {
	value = redact(value)
	if limit <= 0 || len(value) <= limit {
		return value, false
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut] + "\n...[truncated]", true
}

func looksSecret(value string) bool {
	if match := secretLiteralAssignment.FindStringSubmatch(value); len(match) == 2 {
		literal := strings.Trim(match[1], "\"'`")
		if plausibleSecretLiteral(literal) {
			return true
		}
	}
	for _, pattern := range redactionPatterns[1:] {
		if pattern.MatchString(value) {
			return true
		}
	}
	return false
}

func plausibleSecretLiteral(value string) bool {
	if len(value) < 8 || regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`).MatchString(value) {
		return false
	}
	lower := strings.ToLower(value)
	for _, marker := range []string{"x-token", "authorization", "content-type", "api-key", "bearer"} {
		if lower == marker {
			return false
		}
	}
	return true
}
