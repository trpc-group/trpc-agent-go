//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package redact detects and redacts sensitive information in strings.
package redact

import (
	"regexp"
	"strings"
)

const redactedTag = "[REDACTED:"

var patterns = []struct {
	name    string
	re      *regexp.Regexp
	replace string
}{
	// API keys
	{name: "openai_key", re: regexp.MustCompile(`(?i)(sk-[A-Za-z0-9-_]{20,})`), replace: redactedTag + "OPENAI_KEY]"},
	{name: "github_token", re: regexp.MustCompile(`(?i)(gh[pousr]_[A-Za-z0-9_]{20,})`), replace: redactedTag + "GITHUB_TOKEN]"},
	{name: "aws_access_key", re: regexp.MustCompile(`(?i)(AKIA[0-9A-Z]{16})`), replace: redactedTag + "AWS_ACCESS_KEY]"},
	{name: "aws_secret_key", re: regexp.MustCompile(`(?i)(aws.{0,5}secret][^=]*=\s*[A-Za-z0-9/+=]{20,})`), replace: redactedTag + "AWS_SECRET_KEY]"},
	// General secrets
	{name: "generic_api_key", re: regexp.MustCompile(`(?i)(api[_-]?key|apikey)[:=\s]+['"]?[A-Za-z0-9_\-.]{16,}`), replace: redactedTag + "API_KEY]"},
	{name: "password_assignment", re: regexp.MustCompile(`(?i)(password|passwd|pwd)[:=\s]+['"]?[^\s'"]+`), replace: "${1} " + redactedTag + "PASSWORD]"},
	{name: "token_assignment", re: regexp.MustCompile(`(?i)(token|secret_key|private_key|access_key)[:=\s]+['"]?[^\s'"]+`), replace: "${1} " + redactedTag + "TOKEN]"},
	// JWT tokens
	{name: "jwt", re: regexp.MustCompile(`(?i)(eyJ[A-Za-z0-9_-]+\.eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)`), replace: redactedTag + "JWT]"},
	// Connection strings
	{name: "conn_string", re: regexp.MustCompile(`(?i)([:@]//)[^:]+:[^@]+@`), replace: "${1}" + redactedTag + "CREDENTIALS]@"},
}

// String redacts all detected sensitive patterns in s.
// It is idempotent: String(String(s)) == String(s).
func String(s string) string {
	if s == "" {
		return s
	}
	// If already redacted, do not re-process.
	if strings.Contains(s, redactedTag) {
		return s
	}
	result := s
	for _, p := range patterns {
		result = p.re.ReplaceAllString(result, p.replace)
	}
	return result
}

// ContainsSecret reports whether the value contains any sensitive pattern.
// It compares String(value) with value using plain inequality.
func ContainsSecret(value string) bool {
	return String(value) != value
}
