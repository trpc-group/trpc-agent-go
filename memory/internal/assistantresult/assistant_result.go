//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package assistantresult defines the internal text contract used to preserve
// and identify concrete results produced by an assistant.
package assistantresult

import "strings"

// Prefix marks a memory as a concrete result produced by the assistant.
const Prefix = "Assistant result: "

var normalizedPrefix = strings.ToLower(strings.TrimSpace(Prefix))

// Is reports whether text follows the assistant-result memory contract.
func Is(text string) bool {
	return strings.HasPrefix(
		strings.ToLower(strings.TrimSpace(text)),
		normalizedPrefix,
	)
}

// Normalize removes duplicate or misplaced markers and returns one canonical
// assistant-result memory.
func Normalize(text string) string {
	parts := make([]string, 0, 2)
	for {
		index := strings.Index(strings.ToLower(text), normalizedPrefix)
		if index < 0 {
			if part := strings.TrimSpace(text); part != "" {
				parts = append(parts, part)
			}
			break
		}
		if part := strings.TrimSpace(text[:index]); part != "" {
			parts = append(parts, part)
		}
		text = text[index+len(normalizedPrefix):]
	}
	return Prefix + strings.Join(parts, " ")
}
