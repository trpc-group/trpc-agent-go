//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package eventvisibility keeps runner-only event forwarding decisions out of
// event payloads and persisted session data.
package eventvisibility

import (
	"context"
	"sync"
)

type contextKey struct{}
type hiddenEvents struct {
	mu  sync.Mutex
	ids map[string]struct{}
}

// WithCapture creates a run-scoped registry for events that must be processed
// internally without being forwarded to the caller. Derived contexts share it;
// a new call creates an independent registry. Unconsumed entries live only as
// long as the run's context.
func WithCapture(ctx context.Context) context.Context {
	return context.WithValue(ctx, contextKey{}, &hiddenEvents{ids: make(map[string]struct{})})
}

// Hide marks id for internal processing. It reports false if there is no
// runner capture registry or id is empty, so standalone producers can retain
// their normal filtering behavior. Concurrent calls are safe.
func Hide(ctx context.Context, id string) bool {
	state, _ := ctx.Value(contextKey{}).(*hiddenEvents)
	if state == nil || id == "" {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	state.ids[id] = struct{}{}
	return true
}

// TakeHidden consumes the forwarding decision for the original event ID before
// event plugins can replace it. Missing registries and IDs return false.
// Concurrent calls are safe.
func TakeHidden(ctx context.Context, id string) bool {
	state, _ := ctx.Value(contextKey{}).(*hiddenEvents)
	if state == nil {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	_, hidden := state.ids[id]
	delete(state.ids, id)
	return hidden
}
