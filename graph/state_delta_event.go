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
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
)

const defaultStateDeltaEventType = "state_delta"

// StateDeltaEventOptions configures custom state-delta events.
type StateDeltaEventOptions struct {
	EventType string
	Message   string
	Payload   any
}

// StateDeltaEventOption configures a custom state-delta event.
type StateDeltaEventOption func(*StateDeltaEventOptions)

// WithStateDeltaEventType sets the custom event type.
func WithStateDeltaEventType(
	eventType string,
) StateDeltaEventOption {
	return func(opts *StateDeltaEventOptions) {
		if eventType != "" {
			opts.EventType = eventType
		}
	}
}

// WithStateDeltaEventMessage sets the custom event message.
func WithStateDeltaEventMessage(
	message string,
) StateDeltaEventOption {
	return func(opts *StateDeltaEventOptions) {
		opts.Message = message
	}
}

// WithStateDeltaEventPayload sets the custom event payload.
func WithStateDeltaEventPayload(
	payload any,
) StateDeltaEventOption {
	return func(opts *StateDeltaEventOptions) {
		opts.Payload = payload
	}
}

// EmitCustomStateDelta emits a node custom event carrying the given delta.
//
// This is useful when a callback or node needs to expose business state on an
// error path before the graph can emit its final graph.execution snapshot.
func EmitCustomStateDelta(
	ctx context.Context,
	state State,
	delta State,
	opts ...StateDeltaEventOption,
) error {
	if len(delta) == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	execCtx, ok := GetStateValue[*ExecutionContext](state, StateKeyExecContext)
	if !ok || execCtx == nil || execCtx.EventChan == nil {
		return nil
	}

	stateDelta, err := marshalStateDelta(delta)
	if err != nil {
		return err
	}

	options := &StateDeltaEventOptions{
		EventType: defaultStateDeltaEventType,
	}
	for _, opt := range opts {
		opt(options)
	}

	nodeID, _ := GetStateValue[string](state, StateKeyCurrentNodeID)
	metadata := NodeCustomEventMetadata{
		EventType:    options.EventType,
		Category:     NodeCustomEventCategoryCustom,
		NodeID:       nodeID,
		InvocationID: execCtx.InvocationID,
		Timestamp:    time.Now(),
		Payload:      options.Payload,
		Message:      options.Message,
	}

	evt := NewGraphEvent(
		execCtx.InvocationID,
		formatNodeAuthor(nodeID, AuthorGraphNode),
		ObjectTypeGraphNodeCustom,
		WithNodeCustomMetadata(metadata),
	)
	evt.StateDelta = mergeStateDeltaMaps(evt.StateDelta, stateDelta)
	return event.EmitEvent(ctx, execCtx.EventChan, evt)
}

func marshalStateDelta(
	delta State,
) (map[string][]byte, error) {
	stateDelta := make(map[string][]byte, len(delta))
	for key, value := range delta {
		if value == nil {
			stateDelta[key] = nil
			continue
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf(
				"marshal state delta key %q: %w",
				key,
				err,
			)
		}
		stateDelta[key] = raw
	}
	return stateDelta, nil
}

func mergeStateDeltaMaps(
	dst map[string][]byte,
	src map[string][]byte,
) map[string][]byte {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		dst = make(map[string][]byte, len(src))
	}
	for key, value := range src {
		if value == nil {
			dst[key] = nil
			continue
		}
		cloned := make([]byte, len(value))
		copy(cloned, value)
		dst[key] = cloned
	}
	return dst
}
