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
	"sync/atomic"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// callCounter is a shared, goroutine-safe counter for candidate model invocations. It is shared
// across every candidate model instance built during a run (baseline agent + candidate agent) so
// the audit report can attribute a single total call count as the run's cost proxy.
type callCounter struct {
	n int64
}

func (c *callCounter) inc()       { atomic.AddInt64(&c.n, 1) }
func (c *callCounter) count() int { return int(atomic.LoadInt64(&c.n)) }

// countingModel wraps a model.Model and increments a shared counter on every GenerateContent call.
type countingModel struct {
	inner   model.Model
	counter *callCounter
}

func newCountingModel(inner model.Model, counter *callCounter) model.Model {
	if counter == nil {
		return inner
	}
	return &countingModel{inner: inner, counter: counter}
}

func (m *countingModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	m.counter.inc()
	return m.inner.GenerateContent(ctx, req)
}

func (m *countingModel) Info() model.Info {
	return m.inner.Info()
}
