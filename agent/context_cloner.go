//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"context"
	"sync/atomic"
)

// GoroutineContextCloner clones a context before it is used by a new
// goroutine.
//
// This hook lets integrations (for example, Tencent Remote Procedure Call
// (tRPC))
// isolate per-goroutine state stored inside context values (such as message
// metadata) while still preserving cancellation and deadlines.
type GoroutineContextCloner func(context.Context) context.Context

var goroutineContextCloner atomic.Value

func init() {
	goroutineContextCloner.Store(GoroutineContextCloner(identityContext))
}

// SetGoroutineContextCloner configures how contexts are cloned for
// goroutines.
//
// Passing nil resets the cloner to the identity function.
func SetGoroutineContextCloner(cloner GoroutineContextCloner) {
	if cloner == nil {
		cloner = identityContext
	}
	goroutineContextCloner.Store(cloner)
}

// CloneContext returns a context safe to use inside a new goroutine.
//
// By default, it returns the input context unchanged.
func CloneContext(ctx context.Context) context.Context {
	if ctx == nil {
		return nil
	}
	cloner, ok := goroutineContextCloner.Load().(GoroutineContextCloner)
	if !ok || cloner == nil {
		return ctx
	}
	return cloner(ctx)
}

// CloneContextForGoroutine returns a context safe to use inside a new
// goroutine.
//
// Deprecated: use CloneContext.
func CloneContextForGoroutine(ctx context.Context) context.Context {
	return CloneContext(ctx)
}

func identityContext(ctx context.Context) context.Context { return ctx }
