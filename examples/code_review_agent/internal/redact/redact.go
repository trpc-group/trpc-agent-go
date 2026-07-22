//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package redact removes secrets at every persistence boundary.
package redact

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"
)

type pattern struct {
	kind string
	re   *regexp.Regexp
}

var patterns = []pattern{
	{"pem", regexp.MustCompile(`(?s)-----BEGIN (?:RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----.*?-----END (?:RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----`)},
	{"jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{6,}\.[A-Za-z0-9_-]{6,}\b`)},
	{"bearer", regexp.MustCompile(`(?i)\bBearer[ \t]+[A-Za-z0-9._~+/=-]{12,}`)},
	{"openai_key", regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}\b`)},
	{"github_token", regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}\b`)},
	{"google_key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{20,}\b`)},
	{"aws_key", regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`)},
	{"url_credential", regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s/:@]+:[^\s/@]+@[^\s]+`)},
	{"dsn", regexp.MustCompile(`(?i)\b(?:postgres|postgresql|mysql|mongodb(?:\+srv)?|redis)://[^\s]+`)},
	{"named_secret", regexp.MustCompile(`(?i)(?:"|')?(?:api[_-]?key|access[_-]?token|auth[_-]?token|token|password|passwd|secret)(?:"|')?[ \t]*[:=][ \t]*(?:"[^"\r\n]{4,}"|'[^'\r\n]{4,}'|[^\s,;}]{4,})`)},
}

var redactedTag = regexp.MustCompile(`\[REDACTED:[a-z_]+:[0-9a-f]{8}\]`)

const protectedTagFormat = "\x00CR_REDACTED_%d\x00"

// String replaces recognized secrets with typed, stable, non-reversible tags.
func String(value string) string {
	var tags []string
	result := redactedTag.ReplaceAllStringFunc(value, func(tag string) string {
		placeholder := fmt.Sprintf(protectedTagFormat, len(tags))
		tags = append(tags, tag)
		return placeholder
	})
	for _, candidate := range patterns {
		result = candidate.re.ReplaceAllStringFunc(result, func(secret string) string {
			digest := sha256.Sum256([]byte(secret))
			return fmt.Sprintf("[REDACTED:%s:%x]", candidate.kind, digest[:4])
		})
	}
	for index, tag := range tags {
		result = strings.ReplaceAll(result, fmt.Sprintf(protectedTagFormat, index), tag)
	}
	return result
}

// ContainsSecret reports whether redaction changes the input.
func ContainsSecret(value string) bool {
	return !strings.EqualFold(String(value), value)
}
