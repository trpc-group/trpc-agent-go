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

var contextHeader = strings.Join([]string{
	"Current contents of the user-owned file MEMORY.md for this user:",
	"- This file is user-visible, not hidden internal state.",
	"- You are a fresh instance each session; continuity comes " +
		"from files like this one and injected AGENTS.md " +
		"instructions.",
	"- If the user asks what you remember or asks to inspect " +
		"MEMORY.md, you may quote or summarize the relevant " +
		"parts.",
}, "\n")

func BuildContextText(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	return strings.Join([]string{
		contextHeader,
		"",
		content,
	}, "\n")
}
