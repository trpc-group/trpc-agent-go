//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package memoryfile

import "strings"

const ReadLimit = 8 * 1024

func BuildContextText(content string) string {
	return BuildContextTextForScope("the current scope", content)
}

func BuildContextTextForScope(scope string, content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = "the current scope"
	}
	contextHeader := strings.Join([]string{
		"Current contents of the visible MEMORY.md file for " +
			scope + ":",
		"- This file is user-visible, not hidden internal state.",
		"- You are a fresh instance each session; continuity comes " +
			"from files like this one and injected AGENTS.md " +
			"instructions.",
		"- If the user asks what you remember or asks to inspect " +
			"MEMORY.md, you may quote or summarize the relevant " +
			"parts.",
	}, "\n")
	return strings.Join([]string{
		contextHeader,
		"",
		content,
	}, "\n")
}
