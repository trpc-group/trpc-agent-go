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
	regexp.MustCompile(`(?i)(eyJ[a-zA-Z0-9_-]*\.[a-zA-Z0-9_-]*(\.[a-zA-Z0-9_-]*)?)`),
	regexp.MustCompile(`(?i)(ghp_[a-zA-Z0-9]{36})`),
	regexp.MustCompile(`(?i)(AKIA[0-9A-Z]{16})`),
	regexp.MustCompile(`(?i)(api[_-]?key|secret[_-]?key|access[_-]?token|password|secret)\s*=\s*["']([^"']+)["']`),
	regexp.MustCompile(`(?i)(AWS_SECRET_ACCESS_KEY|AWS_ACCESS_KEY_ID)\s*=\s*["']([^"']+)["']`),
	regexp.MustCompile(`(?i)(password|secret|token|key)\s*[:=]\s*["']([^"']+)["']`),
	regexp.MustCompile(`(?i)(dckr_pat_[a-zA-Z0-9_-]{20,})`),
	regexp.MustCompile(`(?i)(gho_[a-zA-Z0-9]{36})`),
	regexp.MustCompile(`(?i)(github_pat_[a-zA-Z0-9]{22}_[a-zA-Z0-9]{59})`),
	regexp.MustCompile(`(?i)(sqldb[_-]?user|sqldb[_-]?password)\s*=\s*["']([^"']+)["']`),
	regexp.MustCompile(`(?i)(connection[_-]?string|conn[_-]?str)\s*=\s*["']([^"']+)["']`),
	regexp.MustCompile(`(?i)(ftp|sftp|http|https)://[^@]+@`),
	regexp.MustCompile(`(?i)([a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,})\s*:\s*["']([^"']+)["']`),
}

func redactToken(token string) string {
	if len(token) <= 8 {
		return "******"
	}
	return token[:4] + "******" + token[len(token)-4:]
}

func RedactSecrets(input string) string {
	result := input

	for _, pattern := range secretPatterns {
		result = pattern.ReplaceAllStringFunc(result, func(match string) string {
			indices := pattern.FindStringSubmatchIndex(match)
			if len(indices) < 4 {
				return redactToken(match)
			}

			valueStart := -1
			valueEnd := -1

			if names := pattern.SubexpNames(); len(names) > 0 {
				for i, name := range names {
					if name == "value" || name == "secret" {
						valueStart = indices[i*2]
						valueEnd = indices[i*2+1]
						break
					}
				}
			}

			if valueStart == -1 {
				lastGroupIdx := len(indices) / 2
				if lastGroupIdx >= 2 {
					valueStart = indices[(lastGroupIdx-1)*2]
					valueEnd = indices[(lastGroupIdx-1)*2+1]
				} else {
					valueStart = indices[2]
					valueEnd = indices[3]
				}
			}

			if valueStart == -1 || valueEnd == -1 || valueStart >= valueEnd {
				return redactToken(match)
			}

			secretValue := match[valueStart:valueEnd]
			if len(secretValue) <= 8 {
				return match[:valueStart] + "******" + match[valueEnd:]
			}
			redacted := secretValue[:4] + "******" + secretValue[len(secretValue)-4:]
			return match[:valueStart] + redacted + match[valueEnd:]
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
