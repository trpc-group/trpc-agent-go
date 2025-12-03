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
	c.mu.Lock()
	defer c.mu.Unlock()
	if custom, ok := event.(*aguievents.CustomEvent); ok && custom.Type() == "think" {
		flushed, err := c.inner.Flush(ctx)
		if err != nil {
			return nil, err
		}
		if v, ok := custom.Value.(string); ok {
			c.think.WriteString(v)
		}
		return flushed, nil
	}
	events, err := c.inner.Append(ctx, event)
	if err != nil {
		return nil, err
	}
	if c.think.Len() == 0 {
		return events, nil
	}
	think := aguievents.NewCustomEvent("think", aguievents.WithValue(c.think.String()))
	c.think.Reset()
	out := make([]aguievents.Event, 0, len(events)+1)
	out = append(out, think)
	out = append(out, events...)
	return out, nil
}

func (c *thinkAggregator) Flush(ctx context.Context) ([]aguievents.Event, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	events, err := c.inner.Flush(ctx)
	if err != nil {
		return nil, err
	}
	if c.think.Len() > 0 {
		think := aguievents.NewCustomEvent("think", aguievents.WithValue(c.think.String()))
		c.think.Reset()
		events = append([]aguievents.Event{think}, events...)
	}
	return events, nil
}
