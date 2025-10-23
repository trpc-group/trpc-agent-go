//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package event

import (
	"strings"
)

// Standard event tags used by the framework.
//
// These tags are attached to streaming model events so clients can
// distinguish between different kinds of internal reasoning.
const (
	// TagReasoningTool marks reasoning that belongs to a model call
	// that is planning or initiating tool/function usage.
	// Clients can filter this tag to hide pre‑tool thought.
	TagReasoningTool = "reasoning.tool"

	// TagReasoningFinal marks reasoning that belongs to the final model
	// answer of the turn (either there were no tools, or tools already
	// ran and the model is producing the concluding answer).
	TagReasoningFinal = "reasoning.final"

	// TagReasoningUnknown marks reasoning where the framework cannot yet
	// determine whether it will lead to tool usage. This only appears
	// during the very beginning of a model call before any tool intent is
	// observable. Most UIs can ignore it or treat it as pre‑tool.
	TagReasoningUnknown = "reasoning.unknown"
)

// AppendTagString appends a tag to an existing tag string using TagDelimiter.
// It avoids duplicates and preserves any existing business tags.
func AppendTagString(existing, tag string) string {
	if tag == "" {
		return existing
	}
	if existing == "" {
		return tag
	}
	// Split and check for duplicates.
	// Tags are treated case-sensitively for now to keep semantics simple.
	if ContainsTagString(existing, tag) {
		return existing
	}
	return existing + TagDelimiter + tag
}

// AddTag appends a tag to the given Event.Tag field without overwriting
// existing tags and avoiding duplicates.
func AddTag(e *Event, tag string) {
	if e == nil {
		return
	}
	e.Tag = AppendTagString(e.Tag, tag)
}

// ContainsTagString reports whether the delimited tag string contains the given tag.
// It performs an exact match on segments split by TagDelimiter. Tags are case-sensitive.
func ContainsTagString(existing, tag string) bool {
	if existing == "" || tag == "" {
		return false
	}
	parts := strings.Split(existing, TagDelimiter)
	for _, p := range parts {
		if p == tag {
			return true
		}
	}
	return false
}

// HasTag reports whether the event currently contains the provided tag.
// It returns false for nil events or empty tag input.
func (e *Event) HasTag(tag string) bool {
	if e == nil || tag == "" {
		return false
	}
	return ContainsTagString(e.Tag, tag)
}
