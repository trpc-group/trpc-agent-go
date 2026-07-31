//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import "regexp"

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(api[_-]?key|token|password|secret|client[_-]?secret|access[_-]?token|access[_-]?key)(\s*[:=]\s*)["']?[^\s"']{8,}`),
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}\b`),
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}\b`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`),
	regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._~-]{12,}`),
	regexp.MustCompile(`-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----`),
}

func redact(text string) (string, bool) {
	redacted := text
	changed := false
	for index, pattern := range secretPatterns {
		before := redacted
		if index == 0 {
			redacted = pattern.ReplaceAllString(redacted, `${1}${2}[REDACTED]`)
		} else {
			redacted = pattern.ReplaceAllString(redacted, `[REDACTED]`)
		}
		changed = changed || before != redacted
	}
	return redacted, changed
}
