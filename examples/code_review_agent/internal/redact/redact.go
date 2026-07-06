//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package redact

import "regexp"

const Placeholder = "[REDACTED_SECRET]"

var patterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(api[_-]?key|token|secret|password)\s*[:=]\s*["']?[A-Za-z0-9_\-./+=]{8,}`),
	regexp.MustCompile(`ghp_[A-Za-z0-9_]{20,}`),
	regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`),
	regexp.MustCompile(`sk-[A-Za-z0-9_\-]{8,}`),
	regexp.MustCompile(`eyJ[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`),
}

// Result contains redacted text and the number of replacements.
type Result struct {
	Text  string
	Count int
}

// Text redacts suspected secrets from a string.
func Text(in string) Result {
	out := in
	count := 0
	for _, pattern := range patterns {
		matches := pattern.FindAllStringIndex(out, -1)
		if len(matches) == 0 {
			continue
		}
		count += len(matches)
		out = pattern.ReplaceAllString(out, Placeholder)
	}
	return Result{Text: out, Count: count}
}

// ContainsSecret reports whether the text contains a supported secret shape.
func ContainsSecret(in string) bool {
	return Text(in).Count > 0
}
