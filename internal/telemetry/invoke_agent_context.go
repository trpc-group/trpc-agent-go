//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package telemetry

import "context"

// invokeAgentActiveCtxKey is an unexported context key type used to mark a
// context as already scoped by an active invoke_agent span/metrics recorder.
//
// It enables idempotent invoke_agent instrumentation: wrappers (e.g. the
// one inside agent.RunWithPlugins) check for this marker before creating a
// new recorder, and self-instrumenting agent implementations check for it
// to skip their own span and metrics when an ancestor has already taken
// responsibility for this invocation's invoke_agent telemetry.
type invokeAgentActiveCtxKey struct{}

// WithInvokeAgentActive returns a new context that is marked as being inside
// an active invoke_agent instrumentation scope.
//
// Callers that start an invoke_agent span or InvokeAgentTracker for an
// invocation should wrap the derived context with this helper so downstream
// agent implementations and framework wrappers can avoid producing a
// duplicate invoke_agent span / metric for the same invocation.
func WithInvokeAgentActive(ctx context.Context) context.Context {
	if ctx == nil {
		return ctx
	}
	if IsInvokeAgentActive(ctx) {
		return ctx
	}
	return context.WithValue(ctx, invokeAgentActiveCtxKey{}, true)
}

// IsInvokeAgentActive reports whether ctx is already inside an active
// invoke_agent instrumentation scope.
//
// A true return value means some ancestor (typically the framework wrapper
// in agent.RunWithPlugins) has already started an invoke_agent span and
// metrics tracker for the current invocation. In that case, descendant
// instrumentation MUST skip creating another invoke_agent span to avoid
// duplicate telemetry.
func IsInvokeAgentActive(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	active, _ := ctx.Value(invokeAgentActiveCtxKey{}).(bool)
	return active
}
