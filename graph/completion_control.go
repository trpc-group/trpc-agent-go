//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package graph

import (
	"context"
	"encoding/json"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// WithGraphCompletionCapture keeps terminal graph completion events available
// to internal graph consumers even when caller-visible forwarding is disabled.
func WithGraphCompletionCapture(ctx context.Context) context.Context {
	return agent.WithGraphCompletionCapture(ctx)
}

// WithoutGraphCompletionCapture clears any inherited capture flag for the
// current visible stream while preserving the rest of the context.
func WithoutGraphCompletionCapture(ctx context.Context) context.Context {
	return agent.WithoutGraphCompletionCapture(ctx)
}

// ShouldCaptureGraphCompletion reports whether the current context keeps
// terminal graph completion events available for internal consumers.
func ShouldCaptureGraphCompletion(ctx context.Context) bool {
	return shouldCaptureGraphCompletion(ctx)
}

func shouldCaptureGraphCompletion(ctx context.Context) bool {
	return agent.ShouldCaptureGraphCompletion(ctx)
}

// IsGraphCompletionEvent reports whether the event is a terminal
// graph.execution event.
func IsGraphCompletionEvent(evt *event.Event) bool {
	return isGraphCompletionEvent(evt)
}

func isGraphCompletionEvent(evt *event.Event) bool {
	return evt != nil &&
		evt.Response != nil &&
		evt.Response.Done &&
		evt.Response.Object == ObjectTypeGraphExecution
}

// IsVisibleGraphCompletionEvent reports whether the event is a caller-visible
// response rewritten from a terminal graph completion event.
func IsVisibleGraphCompletionEvent(evt *event.Event) bool {
	return isVisibleGraphCompletionEvent(evt)
}

func isVisibleGraphCompletionEvent(evt *event.Event) bool {
	if evt == nil ||
		evt.Response == nil ||
		!evt.Response.Done ||
		evt.Response.Object != model.ObjectTypeChatCompletion {
		return false
	}
	metadata, ok := evt.StateDelta[MetadataKeyCompletion]
	return ok && len(metadata) > 0
}

// VisibleGraphCompletionEvent rewrites a terminal graph completion event into a
// caller-visible response event while preserving the final state delta.
func VisibleGraphCompletionEvent(evt *event.Event) (*event.Event, bool) {
	return VisibleGraphCompletionEventForAuthor(evt, "")
}

// VisibleGraphCompletionEventForAuthor rewrites a terminal graph completion
// event into a caller-visible response event while restoring the caller-visible
// author when one is provided.
func VisibleGraphCompletionEventForAuthor(
	evt *event.Event,
	author string,
) (*event.Event, bool) {
	if !IsGraphCompletionEvent(evt) {
		return nil, false
	}
	visible := evt.Clone()
	if visible.StateDelta == nil {
		visible.StateDelta = make(map[string][]byte)
	}
	if len(visible.StateDelta[MetadataKeyCompletion]) == 0 {
		visible.StateDelta[MetadataKeyCompletion] = []byte("{}")
	}
	if visible.Response == nil {
		visible.Response = &model.Response{}
	}
	visible.Object = model.ObjectTypeChatCompletion
	visible.Response.Object = model.ObjectTypeChatCompletion
	if author != "" {
		visible.Author = author
	}
	return visible, true
}

// RecordAssistantResponseID stores stable dedup identifiers when the event
// contains a non-partial assistant message so visible completion snapshots can
// avoid re-emitting the same final answer text.
func RecordAssistantResponseID(
	emitted map[string]struct{},
	evt *event.Event,
) map[string]struct{} {
	if evt == nil || evt.Response == nil || evt.IsPartial {
		return emitted
	}
	for _, choice := range evt.Response.Choices {
		msg := choice.Message
		if msg.Role != model.RoleAssistant || msg.Content == "" {
			continue
		}
		if emitted == nil {
			emitted = make(map[string]struct{})
		}
		if evt.Response.ID != "" {
			emitted["id:"+evt.Response.ID] = struct{}{}
		}
		if signature := assistantChoiceSignature(evt.Response.Choices); signature != "" {
			emitted["sig:"+signature] = struct{}{}
		}
		return emitted
	}
	return emitted
}

// VisibleGraphCompletionEventWithDedup rewrites a terminal graph completion
// event into a caller-visible response event and clears duplicated final
// choices when the corresponding assistant response was already emitted.
func VisibleGraphCompletionEventWithDedup(
	evt *event.Event,
	emittedAssistantResponseIDs map[string]struct{},
) (*event.Event, bool) {
	return VisibleGraphCompletionEventWithDedupForAuthor(
		evt,
		emittedAssistantResponseIDs,
		"",
	)
}

// VisibleGraphCompletionEventWithDedupForAuthor rewrites a terminal graph
// completion event into a caller-visible response event and clears duplicated
// final choices when the corresponding assistant response was already emitted.
func VisibleGraphCompletionEventWithDedupForAuthor(
	evt *event.Event,
	emittedAssistantResponseIDs map[string]struct{},
	author string,
) (*event.Event, bool) {
	visible, ok := VisibleGraphCompletionEventForAuthor(evt, author)
	if !ok {
		return nil, false
	}
	if shouldClearVisibleGraphCompletionChoices(
		visible,
		emittedAssistantResponseIDs,
	) {
		visible.Response = visible.Response.Clone()
		visible.Response.Choices = nil
	}
	return visible, true
}

// VisibleGraphCompletionEventsForForwarding returns the caller-visible event to
// emit and the full completion snapshot to preserve for callbacks.
func VisibleGraphCompletionEventsForForwarding(
	evt *event.Event,
	emittedAssistantResponseIDs map[string]struct{},
) (*event.Event, *event.Event, bool) {
	return VisibleGraphCompletionEventsForForwardingWithAuthor(
		evt,
		emittedAssistantResponseIDs,
		"",
	)
}

// VisibleGraphCompletionEventsForForwardingWithAuthor returns the caller-visible
// event to emit and the full completion snapshot to preserve for callbacks.
func VisibleGraphCompletionEventsForForwardingWithAuthor(
	evt *event.Event,
	emittedAssistantResponseIDs map[string]struct{},
	author string,
) (*event.Event, *event.Event, bool) {
	visible, ok := VisibleGraphCompletionEventWithDedupForAuthor(
		evt,
		emittedAssistantResponseIDs,
		author,
	)
	if !ok {
		return nil, nil, false
	}
	fullRespEvent := visible
	if visibleGraphCompletionNeedsFullResponseSnapshot(evt, visible) {
		fullRespEvent, _ = VisibleGraphCompletionEventForAuthor(evt, author)
	}
	return visible, fullRespEvent, true
}

// ShouldSuppressGraphCompletionEvent reports whether the caller-visible stream
// should hide the terminal graph completion event for this invocation.
func ShouldSuppressGraphCompletionEvent(
	ctx context.Context,
	invocation *agent.Invocation,
	evt *event.Event,
) bool {
	if invocation == nil || !agent.IsGraphCompletionEventDisabled(invocation) {
		return false
	}
	if ShouldCaptureGraphCompletion(ctx) {
		return false
	}
	return IsGraphCompletionEvent(evt)
}

func shouldClearVisibleGraphCompletionChoices(
	evt *event.Event,
	emittedAssistantResponseIDs map[string]struct{},
) bool {
	if evt == nil || evt.Response == nil || len(evt.Response.Choices) == 0 {
		return false
	}
	responseID := completionResponseIDFromStateDelta(evt.StateDelta)
	if responseID != "" {
		_, ok := emittedAssistantResponseIDs["id:"+responseID]
		return ok
	}
	signature := assistantChoiceSignature(evt.Response.Choices)
	if signature == "" {
		return false
	}
	_, ok := emittedAssistantResponseIDs["sig:"+signature]
	return ok
}

func visibleGraphCompletionNeedsFullResponseSnapshot(
	raw *event.Event,
	visible *event.Event,
) bool {
	if raw == nil || raw.Response == nil || len(raw.Response.Choices) == 0 {
		return false
	}
	if visible == nil || visible.Response == nil || visible.Response.IsPartial {
		return false
	}
	return len(visible.Response.Choices) == 0
}

func completionResponseIDFromStateDelta(stateDelta map[string][]byte) string {
	if stateDelta == nil {
		return ""
	}
	raw, ok := stateDelta[StateKeyLastResponseID]
	if !ok || len(raw) == 0 {
		return ""
	}
	var responseID string
	if err := json.Unmarshal(raw, &responseID); err != nil {
		return ""
	}
	return responseID
}

func assistantChoiceSignature(choices []model.Choice) string {
	if len(choices) == 0 {
		return ""
	}
	type signatureChoice struct {
		Role    model.Role `json:"role"`
		Content string     `json:"content"`
	}
	var signatureChoices []signatureChoice
	for _, choice := range choices {
		if choice.Message.Role != model.RoleAssistant ||
			choice.Message.Content == "" {
			continue
		}
		signatureChoices = append(signatureChoices, signatureChoice{
			Role:    choice.Message.Role,
			Content: choice.Message.Content,
		})
	}
	if len(signatureChoices) == 0 {
		return ""
	}
	b, err := json.Marshal(signatureChoices)
	if err != nil {
		return ""
	}
	return string(b)
}
