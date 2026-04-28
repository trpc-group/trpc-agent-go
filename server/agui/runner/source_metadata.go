//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner

import (
	"context"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	agentevent "trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/internal/source"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func (r *runner) attachToolResultInputSourceMetadata(
	ctx context.Context,
	key session.Key,
	event *agentevent.Event,
	toolCallID string,
) {
	if !r.eventSourceMetadataEnabled || event == nil {
		return
	}
	metadata, ok, err := r.lookupToolCallSourceMetadata(ctx, key, toolCallID)
	if err != nil {
		log.WarnfContext(
			ctx,
			"agui source metadata: lookup tool call %s: %v",
			toolCallID,
			err,
		)
	}
	if !ok {
		metadata = source.Metadata{}
	}
	if err := source.SetEventOverride(event, metadata); err != nil {
		log.WarnfContext(
			ctx,
			"agui source metadata: override tool call %s: %v",
			toolCallID,
			err,
		)
	}
}

func (r *runner) lookupToolCallSourceMetadata(
	ctx context.Context,
	key session.Key,
	toolCallID string,
) (source.Metadata, bool, error) {
	if r == nil ||
		r.tracker == nil ||
		toolCallID == "" {
		return source.Metadata{}, false, nil
	}
	trackEvents, err := r.tracker.GetEvents(ctx, key)
	if err != nil {
		return source.Metadata{}, false, err
	}
	if trackEvents == nil || len(trackEvents.Events) == 0 {
		return source.Metadata{}, false, nil
	}
	for i := len(trackEvents.Events) - 1; i >= 0; i-- {
		metadata, ok := toolCallSourceMetadata(
			trackEvents.Events[i].Payload,
			toolCallID,
		)
		if ok {
			return metadata, true, nil
		}
	}
	return source.Metadata{}, false, nil
}

func toolCallSourceMetadata(
	payload []byte,
	toolCallID string,
) (source.Metadata, bool) {
	if len(payload) == 0 || toolCallID == "" {
		return source.Metadata{}, false
	}
	event, err := aguievents.EventFromJSON(payload)
	if err != nil || event == nil {
		return source.Metadata{}, false
	}
	base := event.GetBaseEvent()
	if base == nil {
		return source.Metadata{}, false
	}
	metadata, ok := source.FromRawEvent(base.RawEvent)
	if !ok || !matchesToolCallID(event, toolCallID) {
		return source.Metadata{}, false
	}
	return metadata, true
}

func matchesToolCallID(
	event aguievents.Event,
	toolCallID string,
) bool {
	switch e := event.(type) {
	case *aguievents.ToolCallStartEvent:
		return e.ToolCallID == toolCallID
	case *aguievents.ToolCallArgsEvent:
		return e.ToolCallID == toolCallID
	case *aguievents.ToolCallEndEvent:
		return e.ToolCallID == toolCallID
	case *aguievents.ToolCallResultEvent:
		return e.ToolCallID == toolCallID
	default:
		return false
	}
}
