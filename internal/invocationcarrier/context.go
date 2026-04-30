//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package invocationcarrier

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/agent"
)

type stateCarrierContextKey struct{}

// WithInvocationStateCarrier stores a stable invocation carrier in ctx for
// runtimes that need one shared invocation-scoped state holder across multiple
// task-local invocation clones.
func WithInvocationStateCarrier(
	ctx context.Context,
	inv *agent.Invocation,
) context.Context {
	if ctx == nil || inv == nil {
		return ctx
	}
	return context.WithValue(ctx, stateCarrierContextKey{}, inv)
}

// InvocationStateCarrierFromContext returns the stable invocation carrier
// stored in ctx, if any.
func InvocationStateCarrierFromContext(
	ctx context.Context,
) (*agent.Invocation, bool) {
	if ctx == nil {
		return nil, false
	}
	inv, ok := ctx.Value(stateCarrierContextKey{}).(*agent.Invocation)
	return inv, ok && inv != nil
}
