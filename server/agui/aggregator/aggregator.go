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
	// Append ingests one event and returns zero or more aggregated events ready to persist.
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

// aggregator merges adjacent text, reasoning, and tool-call argument events before persistence.
type aggregator struct {
	mu       sync.Mutex
	enabled  bool            // enabled indicates whether aggregation is active.
	lastID   string          // lastID tracks the buffered message or tool call.
	lastType bufferType      // lastType tracks the event type being buffered.
	buffer   strings.Builder // buffer stores concatenated deltas for the buffered entity.
}

type bufferType int

const (
	bufferTypeUnknown bufferType = iota
	bufferTypeText
	bufferTypeReasoning
	bufferTypeToolArgs
)

// Append aggregates adjacent content events with the same message or tool call ID.
func (a *aggregator) Append(_ context.Context, event aguievents.Event) ([]aguievents.Event, error) {
	if !a.enabled {
		return []aguievents.Event{event}, nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	switch e := event.(type) {
	case *aguievents.TextMessageContentEvent:
		return a.handleTextContent(e), nil
	case *aguievents.ReasoningMessageContentEvent:
		return a.handleReasoningContent(e), nil
	case *aguievents.ToolCallArgsEvent:
		return a.handleToolArgs(e), nil
	default:
		events := a.flush()
		events = append(events, event)
		return events, nil
	}
}

// Flush flushes any buffered text and reasoning content.
func (a *aggregator) Flush(context.Context) ([]aguievents.Event, error) {
	if !a.enabled {
		return nil, nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.flush(), nil
}

// handleTextContent merges content when message ID matches the buffer; otherwise flushes first.
func (a *aggregator) handleTextContent(event *aguievents.TextMessageContentEvent) []aguievents.Event {
	if a.lastID == event.MessageID && a.lastType == bufferTypeText {
		a.buffer.WriteString(event.Delta)
		return nil
	}
	events := a.flush()
	a.lastID = event.MessageID
	a.lastType = bufferTypeText
	a.buffer.Reset()
	a.buffer.WriteString(event.Delta)
	return events
}

func (a *aggregator) handleReasoningContent(event *aguievents.ReasoningMessageContentEvent) []aguievents.Event {
	if a.lastID == event.MessageID && a.lastType == bufferTypeReasoning {
		a.buffer.WriteString(event.Delta)
		return nil
	}
	events := a.flush()
	a.lastID = event.MessageID
	a.lastType = bufferTypeReasoning
	a.buffer.Reset()
	a.buffer.WriteString(event.Delta)
	return events
}

func (a *aggregator) handleToolArgs(event *aguievents.ToolCallArgsEvent) []aguievents.Event {
	if a.lastID == event.ToolCallID && a.lastType == bufferTypeToolArgs {
		a.buffer.WriteString(event.Delta)
		return nil
	}
	events := a.flush()
	a.lastID = event.ToolCallID
	a.lastType = bufferTypeToolArgs
	a.buffer.Reset()
	a.buffer.WriteString(event.Delta)
	return events
}

// flush emits the buffered content as one event and clears internal state.
func (a *aggregator) flush() []aguievents.Event {
	if a.buffer.Len() == 0 {
		return nil
	}
	content := a.buffer.String()
	var event aguievents.Event
	switch a.lastType {
	case bufferTypeText:
		event = aguievents.NewTextMessageContentEvent(a.lastID, content)
	case bufferTypeReasoning:
		event = aguievents.NewReasoningMessageContentEvent(a.lastID, content)
	case bufferTypeToolArgs:
		event = aguievents.NewToolCallArgsEvent(a.lastID, content)
	default:
		a.buffer.Reset()
		return nil
	}
	a.buffer.Reset()
	a.lastID = ""
	a.lastType = bufferTypeUnknown
	return []aguievents.Event{event}
}
