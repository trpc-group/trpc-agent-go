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
	"strings"
)

var (
	// outputAssignSecretPattern finds secret-named assignments for output redaction.
	outputAssignSecretPattern = regexp.MustCompile(
		`(?i)(["']?(?:api[_-]?key|token|password|secret)["']?\s*(?::=|=|:)\s*)("[^"]*"|'[^']*'|[^\s"',]+)`,
	)
	// hardcodedLiteralSecretPattern requires a quoted literal for CR-SEC-002.
	hardcodedLiteralSecretPattern = regexp.MustCompile(
		`(?i)(?:api[_-]?key|token|password|secret)\s*(?::=|=|:)\s*(?:"[^"]+"|'[^']+')`,
	)
	skPattern     = regexp.MustCompile(`sk-[A-Za-z0-9]{20,}`)
	akiaPattern   = regexp.MustCompile(`AKIA[0-9A-Z]{16}`)
	bearerPattern = regexp.MustCompile(`(?i)(Bearer\s+)[A-Za-z0-9._~+/=-]{8,}`)
	pemPattern    = regexp.MustCompile(`-----BEGIN (?:RSA |OPENSSH )?PRIVATE KEY-----`)
)

// Redact replaces sensitive secrets with [REDACTED].
// Broad format detectors (sk-/AKIA/Bearer/PEM) always redact. Name-based
// assignments are redacted only when the value looks like a literal, not a
// call or selector (e.g. token = scanner.Text() is left intact).
func Redact(s string) string {
	out := outputAssignSecretPattern.ReplaceAllStringFunc(s, func(m string) string {
		sub := outputAssignSecretPattern.FindStringSubmatch(m)
		if len(sub) < 3 {
			return m
		}
		if isComputedSecretRHS(sub[2]) {
			return m
		}
		return sub[1] + "[REDACTED]"
	})
	out = skPattern.ReplaceAllString(out, "[REDACTED]")
	out = akiaPattern.ReplaceAllString(out, "[REDACTED]")
	out = bearerPattern.ReplaceAllString(out, `${1}[REDACTED]`)
	out = pemPattern.ReplaceAllString(out, "[REDACTED]")
	return out
}

// isComputedSecretRHS reports whether an assignment RHS is a call/selector
// rather than a literal secret value.
func isComputedSecretRHS(val string) bool {
	val = strings.TrimSpace(val)
	if val == "" {
		return true
	}
	if (strings.HasPrefix(val, `"`) && strings.HasSuffix(val, `"`)) ||
		(strings.HasPrefix(val, `'`) && strings.HasSuffix(val, `'`)) {
		return false
	}
	return strings.ContainsAny(val, ".()")
}

// ContainsSecret reports whether s appears to contain a hard-coded secret.
// Computed assignments (scanner results, function calls, field copies) do not
// count; quoted literals and known secret formats do.
func ContainsSecret(s string) bool {
	return ContainsHardcodedSecret(s)
}

// ContainsHardcodedSecret reports hard-coded secrets suitable for CR-SEC-002.
func ContainsHardcodedSecret(s string) bool {
	if skPattern.MatchString(s) ||
		akiaPattern.MatchString(s) ||
		bearerPattern.MatchString(s) ||
		pemPattern.MatchString(s) {
		return true
	}
	return hardcodedLiteralSecretPattern.MatchString(s)
}
