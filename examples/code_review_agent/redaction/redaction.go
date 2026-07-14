//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package redaction removes secrets before data is persisted or reported.
package redaction

import "regexp"

var patterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)([A-Za-z_][A-Za-z0-9_]*(?:api[_-]?key|token|password|passwd|secret)[A-Za-z0-9_]*)(\s*[:=]\s*)(?:\\?["'])?[^\\"',;\s]+(?:\\?["'])?`),
	regexp.MustCompile(`(?i)(api[_-]?key|token|password|passwd|secret)(\s*[:=]\s*)(?:\\?["'])?[^\\"',;\s]+(?:\\?["'])?`),
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{16,}`),
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9_]{20,}`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`(?i)(tencentcloud_secret(?:id|key))(\s*[:=]\s*)(?:\\?["'])?[^\\"',;\s]+(?:\\?["'])?`),
	regexp.MustCompile(`[A-Za-z0-9+/]{40,}={0,2}`),
	regexp.MustCompile(`\b[0-9a-fA-F]{48,}\b`),
}

// RedactText replaces likely secrets with a stable placeholder.
func RedactText(s string) string {
	out := s
	for _, re := range patterns {
		out = re.ReplaceAllStringFunc(out, func(match string) string {
			if sub := re.FindStringSubmatch(match); len(sub) >= 3 {
				return sub[1] + sub[2] + "[REDACTED_SECRET]"
			}
			return "[REDACTED_SECRET]"
		})
	}
	return out
}
