//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package redact removes sensitive values from review text.
package redact

import "regexp"

var (
	openAIKeyRE = regexp.MustCompile(`sk-[a-zA-Z0-9]{20,}`)
	secretRE    = regexp.MustCompile(`(?i)(api[_-]?key|token|password|secret)\s*[:=]\s*['"]?[\w.-]+`)
	bearerRE    = regexp.MustCompile(`Bearer\s+[A-Za-z0-9._-]+`)
)

const replacement = "<redacted>"

// RedactString masks sensitive values in text.
// 打码
func RedactString(s string) string {
	if s == "" {
		return s
	}
	s = openAIKeyRE.ReplaceAllString(s, replacement)
	s = secretRE.ReplaceAllString(s, replacement)
	s = bearerRE.ReplaceAllString(s, "Bearer "+replacement)
	return s
}
