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
