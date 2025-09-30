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
)

// InvocationContext carries the invocation information.
type InvocationContext struct {
	context.Context
}
type invocationKey struct{}

// NewInvocationContext creates a new InvocationContext.
func NewInvocationContext(ctx context.Context, invocation *Invocation) *InvocationContext {
	return &InvocationContext{
		Context: context.WithValue(ctx, invocationKey{}, invocation),
	}
}

// InvocationFromContext returns the invocation from the context.
func InvocationFromContext(ctx context.Context) (*Invocation, bool) {
	invocation, ok := ctx.Value(invocationKey{}).(*Invocation)
	return invocation, ok
}

// CheckContextCancelled check context cancelled
func CheckContextCancelled(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
