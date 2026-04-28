//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package source provides shared helpers for AG-UI source metadata.
package source

import (
	"encoding/json"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	agentevent "trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	// ExtensionKey stores an AG-UI source metadata override on an agent event.
	ExtensionKey = "server.agui.source_metadata.v1"
)

// Metadata is the compact source metadata exposed to AG-UI consumers.
type Metadata struct {
	EventID            string `json:"eventId,omitempty"`
	Author             string `json:"author,omitempty"`
	InvocationID       string `json:"invocationId,omitempty"`
	ParentInvocationID string `json:"parentInvocationId,omitempty"`
	Branch             string `json:"branch,omitempty"`
}

// SnapshotMetadata indexes source metadata for messages snapshot payloads.
type SnapshotMetadata struct {
	Messages  map[string]Metadata `json:"messages,omitempty"`
	ToolCalls map[string]Metadata `json:"toolCalls,omitempty"`
}

// IsZero reports whether the metadata is empty.
func (m Metadata) IsZero() bool {
	return m == (Metadata{})
}

// IsZero reports whether the snapshot metadata is empty.
func (m SnapshotMetadata) IsZero() bool {
	return len(m.Messages) == 0 && len(m.ToolCalls) == 0
}

// FromEvent resolves source metadata from an agent event.
//
// If the event carries an explicit override in Extensions, the override takes
// precedence. A zero-value override intentionally suppresses metadata export.
func FromEvent(ev *agentevent.Event) (Metadata, bool) {
	if ev == nil {
		return Metadata{}, false
	}
	if metadata, ok := fromEventOverride(ev); ok {
		return metadata, !metadata.IsZero()
	}
	metadata := Metadata{
		EventID:            ev.ID,
		Author:             ev.Author,
		InvocationID:       ev.InvocationID,
		ParentInvocationID: ev.ParentInvocationID,
		Branch:             ev.Branch,
	}
	return metadata, !metadata.IsZero()
}

// SetEventOverride stores an explicit source metadata override on the event.
func SetEventOverride(
	ev *agentevent.Event,
	metadata Metadata,
) error {
	if ev == nil {
		return nil
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	if ev.Extensions == nil {
		ev.Extensions = make(map[string]json.RawMessage)
	}
	ev.Extensions[ExtensionKey] = raw
	return nil
}

// FromRawEvent extracts source metadata from an AG-UI rawEvent payload.
func FromRawEvent(raw any) (Metadata, bool) {
	switch v := raw.(type) {
	case nil:
		return Metadata{}, false
	case Metadata:
		return v, !v.IsZero()
	case *Metadata:
		if v == nil {
			return Metadata{}, false
		}
		return *v, !v.IsZero()
	case map[string]any:
		metadata := Metadata{
			EventID:            stringFromMap(v, "eventId"),
			Author:             stringFromMap(v, "author"),
			InvocationID:       stringFromMap(v, "invocationId"),
			ParentInvocationID: stringFromMap(v, "parentInvocationId"),
			Branch:             stringFromMap(v, "branch"),
		}
		return metadata, !metadata.IsZero()
	default:
		rawJSON, err := json.Marshal(raw)
		if err != nil {
			return Metadata{}, false
		}
		var metadata Metadata
		if err := json.Unmarshal(rawJSON, &metadata); err != nil {
			return Metadata{}, false
		}
		return metadata, !metadata.IsZero()
	}
}

// BuildSnapshotMetadata derives message and tool-call source indexes from
// persisted AG-UI track events.
func BuildSnapshotMetadata(events []session.TrackEvent) SnapshotMetadata {
	metadata := SnapshotMetadata{
		Messages:  make(map[string]Metadata),
		ToolCalls: make(map[string]Metadata),
	}
	for _, trackEvent := range events {
		if len(trackEvent.Payload) == 0 {
			continue
		}
		evt, err := aguievents.EventFromJSON(trackEvent.Payload)
		if err != nil || evt == nil {
			continue
		}
		base := evt.GetBaseEvent()
		if base == nil {
			continue
		}
		sourceMetadata, ok := FromRawEvent(base.RawEvent)
		if !ok {
			continue
		}
		recordSnapshotMetadata(metadata.Messages, metadata.ToolCalls,
			evt, sourceMetadata)
	}
	if len(metadata.Messages) == 0 {
		metadata.Messages = nil
	}
	if len(metadata.ToolCalls) == 0 {
		metadata.ToolCalls = nil
	}
	return metadata
}

func fromEventOverride(ev *agentevent.Event) (Metadata, bool) {
	if ev == nil || len(ev.Extensions) == 0 {
		return Metadata{}, false
	}
	raw, ok := ev.Extensions[ExtensionKey]
	if !ok {
		return Metadata{}, false
	}
	var metadata Metadata
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return Metadata{}, false
	}
	return metadata, true
}

func stringFromMap(values map[string]any, key string) string {
	value, ok := values[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return text
}

func recordSnapshotMetadata(
	messages map[string]Metadata,
	toolCalls map[string]Metadata,
	event aguievents.Event,
	metadata Metadata,
) {
	switch e := event.(type) {
	case *aguievents.TextMessageStartEvent:
		messages[e.MessageID] = metadata
	case *aguievents.TextMessageContentEvent:
		messages[e.MessageID] = metadata
	case *aguievents.TextMessageEndEvent:
		messages[e.MessageID] = metadata
	case *aguievents.TextMessageChunkEvent:
		if e.MessageID != nil && *e.MessageID != "" {
			messages[*e.MessageID] = metadata
		}
	case *aguievents.ReasoningMessageStartEvent:
		messages[e.MessageID] = metadata
	case *aguievents.ReasoningMessageContentEvent:
		messages[e.MessageID] = metadata
	case *aguievents.ReasoningMessageEndEvent:
		messages[e.MessageID] = metadata
	case *aguievents.ReasoningMessageChunkEvent:
		if e.MessageID != nil && *e.MessageID != "" {
			messages[*e.MessageID] = metadata
		}
	case *aguievents.ToolCallStartEvent:
		toolCalls[e.ToolCallID] = metadata
		if e.ParentMessageID != nil && *e.ParentMessageID != "" {
			messages[*e.ParentMessageID] = metadata
		}
	case *aguievents.ToolCallArgsEvent:
		toolCalls[e.ToolCallID] = metadata
	case *aguievents.ToolCallEndEvent:
		toolCalls[e.ToolCallID] = metadata
	case *aguievents.ToolCallResultEvent:
		messages[e.MessageID] = metadata
		if _, exists := toolCalls[e.ToolCallID]; !exists {
			toolCalls[e.ToolCallID] = metadata
		}
	case *aguievents.ActivitySnapshotEvent:
		messages[e.MessageID] = metadata
	case *aguievents.ActivityDeltaEvent:
		messages[e.MessageID] = metadata
	case *aguievents.StepStartedEvent:
		messages[e.ID()] = metadata
	case *aguievents.StepFinishedEvent:
		messages[e.ID()] = metadata
	case *aguievents.StateSnapshotEvent:
		messages[e.ID()] = metadata
	case *aguievents.StateDeltaEvent:
		messages[e.ID()] = metadata
	case *aguievents.MessagesSnapshotEvent:
		messages[e.ID()] = metadata
	case *aguievents.RunStartedEvent:
		messages[e.ID()] = metadata
	case *aguievents.RunFinishedEvent:
		messages[e.ID()] = metadata
	case *aguievents.RunErrorEvent:
		messages[e.ID()] = metadata
	case *aguievents.CustomEvent:
		messages[e.ID()] = metadata
	case *aguievents.RawEvent:
		messages[e.ID()] = metadata
	default:
	}
}
