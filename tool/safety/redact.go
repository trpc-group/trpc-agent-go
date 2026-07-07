//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"regexp"
	"strings"
)

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(api[_-]?key|token|password|passwd|secret)\s*[:=]\s*["']?[^"'\s]+`),
	regexp.MustCompile(`(?i)(sk-[A-Za-z0-9_-]{16,})`),
	regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`),
}

func redactString(s string) (string, bool) {
	redacted := false
	out := s
	for _, re := range secretPatterns {
		next := re.ReplaceAllString(out, "<redacted>")
		if next != out {
			redacted = true
			out = next
		}
	}
	return out, redacted
}

func containsSecret(s string) bool {
	_, ok := redactString(s)
	return ok
}

func redactEnv(env map[string]string) (map[string]string, bool) {
	if len(env) == 0 {
		return nil, false
	}
	out := make(map[string]string, len(env))
	redacted := false
	for k, v := range env {
		if looksSecretName(k) || containsSecret(v) {
			out[k] = "<redacted>"
			redacted = true
			continue
		}
		out[k] = v
	}
	return out, redacted
}

func looksSecretName(s string) bool {
	name := strings.ToLower(s)
	return strings.Contains(name, "token") ||
		strings.Contains(name, "password") ||
		strings.Contains(name, "passwd") ||
		strings.Contains(name, "secret") ||
		strings.Contains(name, "api_key") ||
		strings.Contains(name, "apikey") ||
		strings.Contains(name, "private_key")
}
