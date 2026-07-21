//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package redact provides best-effort removal of common credential shapes
// before framework-owned text is sent to a model.
package redact

import (
	"regexp"
	"strings"
)

// Value replaces detected credential values.
const Value = "[REDACTED]"

var (
	sensitiveNamePattern = regexp.MustCompile(
		`(?i)\b[A-Z0-9_]*(TOKEN|SECRET|PASSWORD|PASSWD|API_KEY|ACCESS_KEY|PRIVATE_KEY)\b[A-Z0-9_]*`,
	)
	assignmentPattern = regexp.MustCompile(
		`(?im)\b([A-Za-z_][A-Za-z0-9_]*)(\s*=\s*)(\"[^\"]*\"|'[^']*'|[^\s,;]+)`,
	)
	colonPattern = regexp.MustCompile(
		`(?im)([\"']?)([A-Za-z_][A-Za-z0-9_]*)([\"']?\s*:\s*)(\"[^\"]*\"|'[^']*'|[^,\s}\]]+)`,
	)
	sensitiveFlagPattern = regexp.MustCompile(
		`(?i)(--(?:api-key|token|secret|password)\s+)(\"[^\"]*\"|'[^']*'|[^\s]+)`,
	)
	authorizationHeaderPattern = regexp.MustCompile(
		`(?i)(authorization\s*:\s*bearer\s+)([^\s,;]+)`,
	)
	authorizationFieldPattern = regexp.MustCompile(
		`(?im)([\"']?authorization[\"']?\s*:\s*)(\"[^\"]*\"|'[^']*'|[^,\s}\]]+)`,
	)
	bearerTokenPattern = regexp.MustCompile(
		`(?i)(\bbearer\s+)([A-Za-z0-9._~+/-]{12,}=*)`,
	)
	openAIKeyPattern = regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{8,}\b`)
	jwtPattern       = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`)
)

// SensitiveText redacts common credential shapes while preserving the
// surrounding text. It is best-effort and does not replace domain-specific
// data classification or sanitization.
func SensitiveText(text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	redacted := authorizationHeaderPattern.ReplaceAllString(text, `${1}`+Value)
	redacted = authorizationFieldPattern.ReplaceAllStringFunc(
		redacted, redactAuthorizationFieldMatch,
	)
	redacted = bearerTokenPattern.ReplaceAllString(redacted, `${1}`+Value)
	redacted = assignmentPattern.ReplaceAllStringFunc(redacted, redactAssignmentMatch)
	redacted = colonPattern.ReplaceAllStringFunc(redacted, redactColonMatch)
	redacted = sensitiveFlagPattern.ReplaceAllString(redacted, `${1}`+Value)
	redacted = openAIKeyPattern.ReplaceAllString(redacted, Value)
	redacted = jwtPattern.ReplaceAllString(redacted, Value)
	return redacted
}

// IsSensitiveName reports whether name commonly identifies a credential.
func IsSensitiveName(name string) bool {
	return sensitiveNamePattern.MatchString(name)
}

// StructuredValue replaces raw while retaining its quoting and trailing
// delimiter shape.
func StructuredValue(raw string) string {
	trimmedRight := strings.TrimRight(raw, " \t")
	suffix := raw[len(trimmedRight):]
	body := trimmedRight
	trailing := ""
	if strings.HasSuffix(body, ",") {
		body = strings.TrimSuffix(body, ",")
		trailing = ","
	}
	switch {
	case HasWrappedQuotes(body, '"'):
		return `"` + Value + `"` + trailing + suffix
	case HasWrappedQuotes(body, '\''):
		return `'` + Value + `'` + trailing + suffix
	default:
		return Value + trailing + suffix
	}
}

// HasWrappedQuotes reports whether value starts and ends with quote.
func HasWrappedQuotes(value string, quote byte) bool {
	return len(value) >= 2 && value[0] == quote && value[len(value)-1] == quote
}

func redactAuthorizationFieldMatch(match string) string {
	parts := authorizationFieldPattern.FindStringSubmatch(match)
	if len(parts) != 3 {
		return match
	}
	return parts[1] + StructuredValue(parts[2])
}

func redactAssignmentMatch(match string) string {
	parts := assignmentPattern.FindStringSubmatch(match)
	if len(parts) != 4 || !IsSensitiveName(parts[1]) {
		return match
	}
	return parts[1] + parts[2] + StructuredValue(parts[3])
}

func redactColonMatch(match string) string {
	parts := colonPattern.FindStringSubmatch(match)
	if len(parts) != 5 || !IsSensitiveName(parts[2]) {
		return match
	}
	return parts[1] + parts[2] + parts[3] + StructuredValue(parts[4])
}
