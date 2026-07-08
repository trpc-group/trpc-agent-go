//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package redaction

import (
	"regexp"
)

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(sk-[a-zA-Z0-9_-]{20,})`),
	regexp.MustCompile(`(?i)(eyJ[a-zA-Z0-9_-]*\.[a-zA-Z0-9_-]*\.[a-zA-Z0-9_-]*)`),
	regexp.MustCompile(`(?i)(ghp_[a-zA-Z0-9]{36})`),
	regexp.MustCompile(`(?i)(AKIA[0-9A-Z]{16})`),
	regexp.MustCompile(`(?i)(api[_-]?key|secret[_-]?key|access[_-]?token|password|secret)\s*=\s*["']([^"']+)["']`),
	regexp.MustCompile(`(?i)(AWS_SECRET_ACCESS_KEY|AWS_ACCESS_KEY_ID)\s*=\s*["']([^"']+)["']`),
	regexp.MustCompile(`(?i)(password|secret|token|key)\s*[:=]\s*["']([^"']+)["']`),
}

func RedactSecrets(input string) string {
	result := input

	for _, pattern := range secretPatterns {
		result = pattern.ReplaceAllStringFunc(result, func(match string) string {
			indices := pattern.FindStringSubmatchIndex(match)
			if len(indices) >= 4 {
				start := indices[len(indices)-2]
				end := indices[len(indices)-1]
				secretValue := match[start:end]
				if len(secretValue) <= 8 {
					return match[:start] + "******" + match[end:]
				}
				redacted := secretValue[:4] + "******" + secretValue[len(secretValue)-4:]
				return match[:start] + redacted + match[end:]
			}
			if len(match) <= 8 {
				return "******"
			}
			return match[:4] + "******" + match[len(match)-4:]
		})
	}

	return result
}

func RedactFindingContent(finding string) string {
	return RedactSecrets(finding)
}

func RedactDiffCode(code string) string {
	return RedactSecrets(code)
}

func RedactAllStrings(strs []string) []string {
	result := make([]string, len(strs))
	for i, s := range strs {
		result[i] = RedactSecrets(s)
	}
	return result
}

func ContainsSecret(input string) bool {
	for _, pattern := range secretPatterns {
		if pattern.MatchString(input) {
			return true
		}
	}
	return false
}
