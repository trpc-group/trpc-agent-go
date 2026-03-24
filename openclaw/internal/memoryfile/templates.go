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

func DefaultTemplate() string {
	return strings.Join([]string{
		"# Memory",
		"",
		"This is a user-owned file for durable memory.",
		"It is user-visible, not hidden internal state.",
		"If the user asks what is remembered here or asks to " +
			"inspect this file, the agent may quote or summarize " +
			"the relevant parts.",
		"If the user explicitly says \"remember this\" or asks " +
			"the agent to remember a durable preference, fact, " +
			"or workflow rule, update this file with a short " +
			"bullet.",
		"",
		"This file stores stable, low-volume memory about the user.",
		"",
		"The agent may update this file only when all conditions hold:",
		"- The information is likely to matter in future sessions.",
		"- The information is stable, not task-local noise.",
		"- The information can be written as a short bullet.",
		"- The information does not contain secrets.",
		"",
		"Do not store:",
		"- Secrets, credentials, or private tokens.",
		"- Large conversation summaries.",
		"- One-off debugging details.",
		"",
		"## Long-term facts",
		"",
		"Use for stable facts such as the user's name or role.",
		"",
		"## Preferences",
		"",
		"Use for durable tone, nickname, format, or persona " +
			"preferences.",
		"",
		"## Repeated working style",
		"",
		"Use for recurring workflow rules such as git, PR, or " +
			"review habits.",
		"",
	}, "\n")
}
