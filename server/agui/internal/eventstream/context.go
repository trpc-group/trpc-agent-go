//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package eventstream coordinates internal ownership of AG-UI event streams.
package eventstream

import (
	"context"
	"sync"
)

type consumerDoneKey struct{}

// WithConsumer returns a context carrying an event-consumer lifetime signal
// and an idempotent function that ends that lifetime.
func WithConsumer(ctx context.Context) (context.Context, func()) {
	if ctx == nil {
		ctx = context.Background()
	}
	done := make(chan struct{})
	var once sync.Once
	return context.WithValue(ctx, consumerDoneKey{}, (<-chan struct{})(done)), func() {
		once.Do(func() {
			close(done)
		})
	}
}

// ConsumerDone returns the event-consumer lifetime signal carried by ctx.
func ConsumerDone(ctx context.Context) <-chan struct{} {
	if ctx == nil {
		return nil
	}
	done, _ := ctx.Value(consumerDoneKey{}).(<-chan struct{})
	return done
}
