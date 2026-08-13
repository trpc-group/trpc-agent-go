//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package aggregator buffers and merges AG-UI events before they are persisted.
package aggregator

import (
	"context"
	"strings"
	"sync"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
)

// Aggregator buffers and merges AG-UI events before they are persisted.
type Aggregator interface {
	// Append ingests one event and returns zero or more events to enqueue for the next flush.
	Append(ctx context.Context, event aguievents.Event) ([]aguievents.Event, error)
	// Flush emits any buffered events and clears internal state.
	Flush(ctx context.Context) ([]aguievents.Event, error)
}

// Factory creates a new Aggregator instance.
type Factory func(ctx context.Context, opt ...Option) Aggregator

// New creates a new aggregator with the given options.
func New(ctx context.Context, opt ...Option) Aggregator {
	opts := newOptions(opt...)
	return &aggregator{
		enabled: opts.enabled,
	}
}

// aggregator keeps a pending event queue and merges content by stable stream keys before persistence.
type aggregator struct {
	mu      sync.Mutex
	enabled bool                 // enabled indicates whether aggregation is active.
	pending []aguievents.Event   // pending stores events in their first-observed order.
	slots   map[aggregateKey]int // slots maps active aggregate streams to pending positions.
	buffers map[aggregateKey]*strings.Builder
}

type aggregateKind int

const (
	aggregateKindText aggregateKind = iota
	aggregateKindReasoning
	aggregateKindToolArgs
)

type aggregateKey struct {
	kind aggregateKind
	id   string
}

// Append aggregates content events with the same active stream key.
func (a *aggregator) Append(_ context.Context, event aguievents.Event) ([]aguievents.Event, error) {
	if !a.enabled {
		return []aguievents.Event{event}, nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.slots == nil {
		a.slots = make(map[aggregateKey]int)
	}
	if a.buffers == nil {
		a.buffers = make(map[aggregateKey]*strings.Builder)
	}
	switch e := event.(type) {
	case *aguievents.TextMessageContentEvent:
		a.appendContent(aggregateKey{kind: aggregateKindText, id: e.MessageID}, e.Delta, func(delta string) aguievents.Event {
			return aguievents.NewTextMessageContentEvent(e.MessageID, delta)
		})
	case *aguievents.ReasoningMessageContentEvent:
		a.appendContent(aggregateKey{kind: aggregateKindReasoning, id: e.MessageID}, e.Delta, func(delta string) aguievents.Event {
			return aguievents.NewReasoningMessageContentEvent(e.MessageID, delta)
		})
	case *aguievents.ToolCallArgsEvent:
		a.appendContent(aggregateKey{kind: aggregateKindToolArgs, id: e.ToolCallID}, e.Delta, func(delta string) aguievents.Event {
			return aguievents.NewToolCallArgsEvent(e.ToolCallID, delta)
		})
	default:
		a.closeSlotsForBarrier()
		a.pending = append(a.pending, event)
	}
	return nil, nil
}

// Flush emits pending events and clears buffered content.
func (a *aggregator) Flush(context.Context) ([]aguievents.Event, error) {
	if !a.enabled {
		return nil, nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.pending) == 0 {
		return nil, nil
	}
	events := append([]aguievents.Event(nil), a.pending...)
	a.pending = nil
	a.slots = nil
	a.buffers = nil
	return events, nil
}

func (a *aggregator) appendContent(key aggregateKey, delta string, build func(string) aguievents.Event) {
	if idx, ok := a.slots[key]; ok {
		buffer := a.buffers[key]
		buffer.WriteString(delta)
		a.pending[idx] = build(buffer.String())
		return
	}
	buffer := &strings.Builder{}
	buffer.WriteString(delta)
	a.slots[key] = len(a.pending)
	a.buffers[key] = buffer
	a.pending = append(a.pending, build(delta))
}

func (a *aggregator) closeSlotsForBarrier() {
	a.slots = nil
	a.buffers = nil
}
