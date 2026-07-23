//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package safety provides permission, redaction, and execution limits.
package safety

import (
	"regexp"
)

var (
	assignSecretPattern = regexp.MustCompile(
		`(?i)(["']?(?:api[_-]?key|token|password|secret)["']?\s*[:=]\s*["']?)([^"',\s]+)(["']?)`,
	)
	skPattern     = regexp.MustCompile(`sk-[A-Za-z0-9]{20,}`)
	akiaPattern   = regexp.MustCompile(`AKIA[0-9A-Z]{16}`)
	bearerPattern = regexp.MustCompile(`(?i)(Bearer\s+)[A-Za-z0-9._~+/=-]{8,}`)
	pemPattern    = regexp.MustCompile(`-----BEGIN (?:RSA |OPENSSH )?PRIVATE KEY-----`)
)

// Redact replaces sensitive secrets with [REDACTED].
func Redact(s string) string {
	out := s
	out = assignSecretPattern.ReplaceAllString(out, `${1}[REDACTED]${3}`)
	out = skPattern.ReplaceAllString(out, "[REDACTED]")
	out = akiaPattern.ReplaceAllString(out, "[REDACTED]")
	out = bearerPattern.ReplaceAllString(out, `${1}[REDACTED]`)
	out = pemPattern.ReplaceAllString(out, "[REDACTED]")
	return out
}

// ContainsSecret reports whether s appears to contain a secret pattern.
func ContainsSecret(s string) bool {
	return assignSecretPattern.MatchString(s) ||
		skPattern.MatchString(s) ||
		akiaPattern.MatchString(s) ||
		bearerPattern.MatchString(s) ||
		pemPattern.MatchString(s)
}
