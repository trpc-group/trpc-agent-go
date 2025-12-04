//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"strings"
	"sync"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/aggregator"
)

type thinkAggregator struct {
	mu    sync.Mutex
	inner aggregator.Aggregator
	think strings.Builder
}

func newAggregator(ctx context.Context, opt ...aggregator.Option) aggregator.Aggregator {
	return &thinkAggregator{inner: aggregator.New(ctx, opt...)}
}

func (c *thinkAggregator) Append(ctx context.Context, event aguievents.Event) ([]aguievents.Event, error) {
	// Acquire lock to protect the inner aggregator and the think buffer from concurrent access.
	c.mu.Lock()
	defer c.mu.Unlock()

	// Handle custom "think" events by accumulating their content without emitting them immediately.
	// 1. Flush the inner aggregator to emit all previously aggregated non-think events.
	// 2. Append the current think fragment to the buffer only.
	// This ensures think fragments remain contiguous and all prior non-think events are emitted first.
	if custom, ok := event.(*aguievents.CustomEvent); ok && custom.Name == string(thinkEventTypeContent) {
		flushed, err := c.inner.Flush(ctx)
		if err != nil {
			return nil, err
		}
		// Only accumulate string values to avoid corrupting the buffer with unexpected types.
		if v, ok := custom.Value.(string); ok {
			c.think.WriteString(v)
		}
		// Return only previously flushed inner events; the current think fragment stays buffered and invisible downstream.
		return flushed, nil
	}

	// Delegate non-think events to the inner aggregator to preserve its default aggregation behavior.
	events, err := c.inner.Append(ctx, event)
	if err != nil {
		return nil, err
	}

	// If there is no buffered think content, simply return the inner events as-is.
	if c.think.Len() == 0 {
		return events, nil
	}

	// If buffered think content exists, prepend a single aggregated think event to the current batch.
	// 1. Wrap the buffered content as one CustomEvent.
	// 2. Reset the buffer to avoid re-emitting the same content.
	// 3. Place the think event before subsequent events to preserve temporal ordering.
	think := aguievents.NewCustomEvent(string(thinkEventTypeContent), aguievents.WithValue(c.think.String()))
	c.think.Reset()

	out := make([]aguievents.Event, 0, len(events)+1)
	out = append(out, think)
	out = append(out, events...)
	return out, nil
}

func (c *thinkAggregator) Flush(ctx context.Context) ([]aguievents.Event, error) {
	// Serialize Flush to protect both the inner aggregator and the think buffer.
	c.mu.Lock()
	defer c.mu.Unlock()

	// Flush the inner aggregator first so that all non-think events follow their own aggregation semantics.
	events, err := c.inner.Flush(ctx)
	if err != nil {
		return nil, err
	}

	// If residual think content exists, emit it as a single aggregated think event at the front of this batch.
	// This guarantees the final chunk of thinking is not lost and is not interleaved after later events.
	if c.think.Len() > 0 {
		think := aguievents.NewCustomEvent(string(thinkEventTypeContent), aguievents.WithValue(c.think.String()))
		c.think.Reset()
		events = append([]aguievents.Event{think}, events...)
	}
	return events, nil
}
