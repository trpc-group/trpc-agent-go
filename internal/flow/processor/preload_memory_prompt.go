//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

const defaultPreloadMemoryPlaybook = `## Memory

You have access to stored memories about the user. These memories can save time,
avoid repeated questions, and help keep responses consistent across sessions.

Decision boundary: should you use memory for this request?

- Skip memory only when the request is clearly self-contained and does not need
  user preferences, profile details, durable user facts, or prior decisions.
- Use memory by default when the request asks for personalization, continuity,
  preferences, recurring projects, or anything that may depend on prior sessions.
- If unsure, do a quick memory pass instead of asking the user to repeat context.

Quick memory pass:

1. Start from the preloaded memories below; they are already in this prompt.
2. If memory_search is available and the answer needs more detail, use it with a
   focused query before using broader searches.
3. Use memory_load only when it is available and you need to inspect stored
   memories beyond the preloaded set, or when memory_search points to entries
   that need expansion.
4. For exact prior-conversation details, wording, or tool traces, rely only on
   session-history context or separate session-history tool guidance when it is
   explicitly provided. Use preloaded memories for durable user facts and
   preferences.
5. Keep lookup lightweight. Stop once the relevant memory is found or when
   searches do not return useful matches.

Verification:

- Treat memories as helpful context, not as guaranteed-current truth.
- If a memory-derived fact may have changed and is cheap to verify, verify it
  before relying on it.
- If you answer from potentially stale memory without verification, mention that
  the detail comes from stored memory when it matters.`

const preloadMemoryHeader = "========= PRELOADED_USER_MEMORIES BEGINS ========="
const preloadMemoryFooter = "========= PRELOADED_USER_MEMORIES ENDS ========="

func buildPreloadMemoryPrompt(
	playbookOverride string,
	memories []*memory.Entry,
) string {
	playbook := strings.TrimSpace(playbookOverride)
	if playbook == "" {
		playbook = defaultPreloadMemoryPlaybook
	}

	sections := []string{
		strings.TrimSpace(playbook),
		preloadMemoryHeader,
		strings.TrimSpace(formatMemoryEntries(memories)),
		preloadMemoryFooter,
	}
	return strings.Join(sections, "\n\n")
}

// formatMemoryContent formats memories for preload context injection.
func formatMemoryContent(memories []*memory.Entry) string {
	return buildPreloadMemoryPrompt("", memories)
}

// formatMemoryEntries formats stored memories while preserving the legacy entry
// shape models already see in preload prompts.
func formatMemoryEntries(memories []*memory.Entry) string {
	var sb strings.Builder
	sb.WriteString("## User Memories\n\n")
	sb.WriteString("The following are stored memories about the user. ")
	sb.WriteString("Use these to answer questions. Episodic memories include ")
	sb.WriteString("event details (time, participants, location).\n\n")
	for _, mem := range memories {
		if mem == nil || mem.Memory == nil {
			continue
		}
		fmt.Fprintf(&sb, "- [%s] %s", mem.ID, mem.Memory.Memory)
		// Append metadata inline for richer context.
		var meta []string
		if mem.Memory.Kind != "" {
			meta = append(meta, fmt.Sprintf("kind=%s", mem.Memory.Kind))
		}
		if mem.Memory.EventTime != nil {
			meta = append(
				meta,
				fmt.Sprintf(
					"date=%s",
					mem.Memory.EventTime.Format("2006-01-02"),
				),
			)
		}
		if len(mem.Memory.Participants) > 0 {
			meta = append(
				meta,
				fmt.Sprintf(
					"with=%s",
					strings.Join(mem.Memory.Participants, ", "),
				),
			)
		}
		if mem.Memory.Location != "" {
			meta = append(meta, fmt.Sprintf("at=%s", mem.Memory.Location))
		}
		// Do not render topic labels in the preload prompt. The memory_add
		// tool expects topics as []string, and showing inline
		// "topics=foo, bar" text can lead models to copy a scalar value into
		// tool arguments.
		if len(meta) > 0 {
			fmt.Fprintf(&sb, " (%s)", strings.Join(meta, "; "))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}
