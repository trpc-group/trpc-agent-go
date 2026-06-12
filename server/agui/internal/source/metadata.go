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
	aguitypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	agentevent "trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/internal/multimodal"
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
	Timestamp          *int64 `json:"timestamp,omitempty"`
}

// SnapshotMetadata indexes source metadata for messages snapshot payloads.
type SnapshotMetadata struct {
	Messages  map[string]Metadata `json:"messages,omitempty"`
	ToolCalls map[string]Metadata `json:"toolCalls,omitempty"`
}

// SnapshotMetadataOption configures how snapshot metadata is built.
type SnapshotMetadataOption func(*snapshotMetadataOptions)

type snapshotMetadataOptions struct {
	includeRunLifecycleEvents bool
}

// WithRunLifecycleEvents controls whether RUN_* lifecycle events are included
// in message snapshot metadata.
func WithRunLifecycleEvents(include bool) SnapshotMetadataOption {
	return func(o *snapshotMetadataOptions) {
		o.includeRunLifecycleEvents = include
	}
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
			Timestamp:          int64FromMap(v, "timestamp"),
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
func BuildSnapshotMetadata(
	events []session.TrackEvent,
	opts ...SnapshotMetadataOption,
) SnapshotMetadata {
	options := snapshotMetadataOptions{}
	for _, opt := range opts {
		opt(&options)
	}
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
		if !options.includeRunLifecycleEvents && isRunLifecycleEvent(evt) {
			continue
		}
		sourceMetadata, ok := FromRawEvent(base.RawEvent)
		if timestamp := base.Timestamp(); timestamp != nil {
			ts := *timestamp
			sourceMetadata.Timestamp = &ts
		} else if !trackEvent.Timestamp.IsZero() {
			ts := trackEvent.Timestamp.UnixMilli()
			sourceMetadata.Timestamp = &ts
		}
		if !ok && sourceMetadata.Timestamp == nil {
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

func isRunLifecycleEvent(evt aguievents.Event) bool {
	switch evt.(type) {
	case *aguievents.RunStartedEvent,
		*aguievents.RunFinishedEvent,
		*aguievents.RunErrorEvent:
		return true
	default:
		return false
	}
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

func int64FromMap(values map[string]any, key string) *int64 {
	value, ok := values[key]
	if !ok {
		return nil
	}
	switch v := value.(type) {
	case int64:
		return &v
	case int:
		ts := int64(v)
		return &ts
	case float64:
		ts := int64(v)
		if float64(ts) != v {
			return nil
		}
		return &ts
	case json.Number:
		ts, err := v.Int64()
		if err != nil {
			return nil
		}
		return &ts
	default:
		return nil
	}
}

func recordSnapshotMetadata(
	messages map[string]Metadata,
	toolCalls map[string]Metadata,
	event aguievents.Event,
	metadata Metadata,
) {
	switch e := event.(type) {
	case *aguievents.TextMessageStartEvent:
		recordMessageMetadata(messages, e.MessageID, metadata)
	case *aguievents.TextMessageContentEvent:
		recordMessageMetadata(messages, e.MessageID, metadata)
	case *aguievents.TextMessageEndEvent:
		recordMessageMetadata(messages, e.MessageID, metadata)
	case *aguievents.TextMessageChunkEvent:
		if e.MessageID != nil && *e.MessageID != "" {
			recordMessageMetadata(messages, *e.MessageID, metadata)
		}
	case *aguievents.ReasoningMessageStartEvent:
		recordMessageMetadata(messages, e.MessageID, metadata)
	case *aguievents.ReasoningMessageContentEvent:
		recordMessageMetadata(messages, e.MessageID, metadata)
	case *aguievents.ReasoningMessageEndEvent:
		recordMessageMetadata(messages, e.MessageID, metadata)
	case *aguievents.ReasoningMessageChunkEvent:
		if e.MessageID != nil && *e.MessageID != "" {
			recordMessageMetadata(messages, *e.MessageID, metadata)
		}
	case *aguievents.ToolCallStartEvent:
		recordToolCallMetadata(toolCalls, e.ToolCallID, metadata)
		if e.ParentMessageID != nil && *e.ParentMessageID != "" {
			recordMessageMetadata(messages, *e.ParentMessageID, metadata)
		}
	case *aguievents.ToolCallArgsEvent:
		recordToolCallMetadata(toolCalls, e.ToolCallID, metadata)
	case *aguievents.ToolCallEndEvent:
		recordToolCallMetadata(toolCalls, e.ToolCallID, metadata)
	case *aguievents.ToolCallResultEvent:
		recordMessageMetadata(messages, e.MessageID, metadata)
		if _, exists := toolCalls[e.ToolCallID]; !exists {
			recordToolCallMetadata(toolCalls, e.ToolCallID, metadata)
		}
	case *aguievents.ActivitySnapshotEvent:
		recordMessageMetadata(messages, e.MessageID, metadata)
	case *aguievents.ActivityDeltaEvent:
		recordMessageMetadata(messages, e.MessageID, metadata)
	case *aguievents.StepStartedEvent:
		recordMessageMetadata(messages, e.ID(), metadata)
	case *aguievents.StepFinishedEvent:
		recordMessageMetadata(messages, e.ID(), metadata)
	case *aguievents.StateSnapshotEvent:
		recordMessageMetadata(messages, e.ID(), metadata)
	case *aguievents.StateDeltaEvent:
		recordMessageMetadata(messages, e.ID(), metadata)
	case *aguievents.MessagesSnapshotEvent:
		recordMessageMetadata(messages, e.ID(), metadata)
	case *aguievents.RunStartedEvent:
		recordMessageMetadata(messages, e.ID(), metadata)
	case *aguievents.RunFinishedEvent:
		recordMessageMetadata(messages, e.ID(), metadata)
	case *aguievents.RunErrorEvent:
		recordMessageMetadata(messages, e.ID(), metadata)
	case *aguievents.CustomEvent:
		if e.Name == multimodal.CustomEventNameUserMessage {
			if messageID := customUserMessageID(e); messageID != "" {
				recordMessageMetadata(messages, messageID, metadata)
				return
			}
		}
		recordMessageMetadata(messages, e.ID(), metadata)
	case *aguievents.RawEvent:
		recordMessageMetadata(messages, e.ID(), metadata)
	default:
	}
}

func recordMessageMetadata(
	messages map[string]Metadata,
	messageID string,
	metadata Metadata,
) {
	if messageID == "" {
		return
	}
	messages[messageID] = mergeMetadata(messages[messageID], metadata)
}

func recordToolCallMetadata(
	toolCalls map[string]Metadata,
	toolCallID string,
	metadata Metadata,
) {
	if toolCallID == "" {
		return
	}
	toolCalls[toolCallID] = mergeMetadata(toolCalls[toolCallID], metadata)
}

func mergeMetadata(existing Metadata, incoming Metadata) Metadata {
	merged := existing
	if incoming.EventID != "" {
		merged.EventID = incoming.EventID
	}
	if incoming.Author != "" {
		merged.Author = incoming.Author
	}
	if incoming.InvocationID != "" {
		merged.InvocationID = incoming.InvocationID
	}
	if incoming.ParentInvocationID != "" {
		merged.ParentInvocationID = incoming.ParentInvocationID
	}
	if incoming.Branch != "" {
		merged.Branch = incoming.Branch
	}
	if incoming.Timestamp != nil &&
		(merged.Timestamp == nil || *incoming.Timestamp < *merged.Timestamp) {
		ts := *incoming.Timestamp
		merged.Timestamp = &ts
	}
	return merged
}

func customUserMessageID(e *aguievents.CustomEvent) string {
	if e == nil || e.Value == nil {
		return ""
	}
	data, err := json.Marshal(e.Value)
	if err != nil {
		return ""
	}
	var message aguitypes.Message
	if err := json.Unmarshal(data, &message); err != nil {
		return ""
	}
	return message.ID
}
