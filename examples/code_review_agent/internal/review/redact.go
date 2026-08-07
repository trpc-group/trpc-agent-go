//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package review

import (
	"regexp"
	"strings"
)

// Redactor removes secrets before data reaches any durable or user-visible sink.
type Redactor struct {
	patterns []*regexp.Regexp
}

// NewRedactor builds the default review redactor.
func NewRedactor() Redactor {
	return Redactor{patterns: []*regexp.Regexp{
		regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
		regexp.MustCompile(`github_pat_[A-Za-z0-9_]{40,}`),
		regexp.MustCompile(`fixture-secret-value-[A-Za-z0-9-]+`),
		regexp.MustCompile(`(?i)(password|passwd|token|secret)\s*[:=]\s*["'][^"']+["']`),
		regexp.MustCompile(`https?://[^/\s:]+:[^@\s]+@`),
		regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`),
	}}
}

// Redact returns s with secrets replaced by a stable marker.
func (r Redactor) Redact(s string) string {
	for _, re := range r.patterns {
		s = re.ReplaceAllStringFunc(s, func(match string) string {
			if isSafeSecretReference(match) {
				return match
			}
			if strings.HasPrefix(match, "http://") || strings.HasPrefix(match, "https://") {
				return regexp.MustCompile(`//.*:.*@`).ReplaceAllString(match, "//[REDACTED]@")
			}
			return "[REDACTED]"
		})
	}
	return s
}

// Detect reports whether s contains a concrete secret.
func (r Redactor) Detect(s string) bool {
	return r.Redact(s) != s
}

// ContainsAny reports whether s contains any candidate substring.
func ContainsAny(s string, needles []string) bool {
	for _, n := range needles {
		if n != "" && strings.Contains(s, n) {
			return true
		}
	}
	return false
}

var safeSecretReferenceRE = regexp.MustCompile(`(?i)^\s*(password|passwd|token|secret)\s*[:=]\s*["']\$\{[A-Za-z_][A-Za-z0-9_]*\}["']\s*$`)

func isSafeSecretReference(s string) bool {
	return safeSecretReferenceRE.MatchString(s)
}
