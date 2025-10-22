//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package eventtag provides helpers for annotating events with reasoning-phase tags.
package eventtag

import (
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// DecideReasoningTag determines the appropriate reasoning tag for a streaming
// event based on context and observed tool intent. It may update toolPlanSeen
// to true when tool intent is detected on the current chunk.
// Returns one of event.TagReasoningFinal, event.TagReasoningTool, or event.TagReasoningUnknown.
func DecideReasoningTag(e *event.Event, afterTool bool, toolPlanSeen *bool) string {
	if e == nil || e.Response == nil {
		return ""
	}
	if e.Object != model.ObjectTypeChatCompletion && e.Object != model.ObjectTypeChatCompletionChunk {
		return ""
	}
	// Detect tool intent from this chunk.
	hasToolDelta := false
	if len(e.Response.Choices) > 0 {
		ch := e.Response.Choices[0]
		if len(ch.Message.ToolCalls) > 0 || len(ch.Delta.ToolCalls) > 0 {
			hasToolDelta = true
		}
	}
	if toolPlanSeen != nil && hasToolDelta {
		*toolPlanSeen = true
	}
	switch {
	case afterTool:
		return event.TagReasoningFinal
	case (toolPlanSeen != nil && *toolPlanSeen) || hasToolDelta:
		return event.TagReasoningTool
	default:
		return event.TagReasoningUnknown
	}
}
