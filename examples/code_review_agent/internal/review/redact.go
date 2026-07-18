//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import (
	"regexp"
	"strings"
)

var redactionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(password|passwd|token|api[_-]?key|client[_-]?secret|private[_-]?key)(\s*[:=]\s*)(?:"[^"]*"|'[^']*'|[^\s,;]+)`),
	regexp.MustCompile(`(?i)\b(gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|sk-[A-Za-z0-9_-]{16,}|xox[baprs]-[A-Za-z0-9-]{16,})\b`),
	regexp.MustCompile(`(?i)(postgres(?:ql)?|mysql)://[^\s"']+`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`),
}

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
	return value[:limit] + "\n...[truncated]", true
}

func looksSecret(value string) bool {
	lower := strings.ToLower(value)
	for _, key := range []string{"password", "passwd", "token", "api_key", "apikey", "secret", "private_key"} {
		if strings.Contains(lower, key) && strings.ContainsAny(value, `="':`) {
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
