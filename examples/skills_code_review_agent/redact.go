//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"regexp"
	"strings"
)

const redactedValue = "[REDACTED]"

var (
	secretKeyQuotedPattern = regexp.MustCompile(
		`(?i)\b([a-z0-9_]*(?:api[_-]?key|access[_-]?key|client[_-]?secret|secret|token|password|passwd|authorization))\b(\s*(?::=|=|:)\s*)(["'])([^"'\r\n]{4,})(["'])`,
	)
	secretKeyBarePattern = regexp.MustCompile(
		`(?i)\b([a-z0-9_]*(?:api[_-]?key|access[_-]?key|client[_-]?secret|secret|token|password|passwd|authorization))\b(\s*(?:=|:)\s*)([A-Za-z0-9_./+@-]{6,})`,
	)
	bearerPattern = regexp.MustCompile(
		`(?i)\b(Bearer\s+)([A-Za-z0-9._~+/=-]{8,})`,
	)
	githubTokenPattern = regexp.MustCompile(
		`\bgh[pousr]_[A-Za-z0-9]{20,}\b`,
	)
	awsAccessKeyPattern = regexp.MustCompile(
		`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`,
	)
	serviceTokenPattern = regexp.MustCompile(
		`\b(?:sk|rk)[-_](?:live|test|proj)[-_][A-Za-z0-9_-]{12,}\b`,
	)
	providerTokenPattern = regexp.MustCompile(
		`\b(?:AIza[0-9A-Za-z_-]{30,}|xox[baprs]-[0-9A-Za-z-]{10,}|glpat-[0-9A-Za-z_-]{16,}|npm_[0-9A-Za-z]{20,})\b`,
	)
	urlCredentialPattern = regexp.MustCompile(
		`([A-Za-z][A-Za-z0-9+.-]*://[^:/\s]+:)([^@\s/]{4,})(@)`,
	)
	privateKeyPattern = regexp.MustCompile(
		`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`,
	)
)

// Redact removes common credential forms before persistence or reporting.
func Redact(value string) string {
	if value == "" {
		return ""
	}
	value = privateKeyPattern.ReplaceAllString(value, redactedValue)
	value = urlCredentialPattern.ReplaceAllString(value, `${1}`+redactedValue+`${3}`)
	value = bearerPattern.ReplaceAllString(value, `${1}`+redactedValue)
	value = githubTokenPattern.ReplaceAllString(value, redactedValue)
	value = awsAccessKeyPattern.ReplaceAllString(value, redactedValue)
	value = serviceTokenPattern.ReplaceAllString(value, redactedValue)
	value = providerTokenPattern.ReplaceAllString(value, redactedValue)
	value = secretKeyQuotedPattern.ReplaceAllString(
		value, `${1}${2}${3}`+redactedValue+`${5}`,
	)
	value = replaceBareSecret(value)
	return value
}

func replaceBareSecret(value string) string {
	return secretKeyBarePattern.ReplaceAllStringFunc(value, func(match string) string {
		parts := secretKeyBarePattern.FindStringSubmatch(match)
		if len(parts) != 4 {
			return match
		}
		candidate := strings.ToLower(parts[3])
		if candidate == strings.ToLower(redactedValue) ||
			strings.HasPrefix(candidate, "os.getenv") ||
			strings.HasPrefix(candidate, "getenv") {
			return match
		}
		return parts[1] + parts[2] + redactedValue
	})
}

// ContainsSecret reports whether redaction identifies credential material.
func ContainsSecret(value string) bool {
	if value == "" {
		return false
	}
	return Redact(value) != value
}
