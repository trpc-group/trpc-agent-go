package contextutil

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/agent"
)

type InvocationContext struct {
	context.Context
	*agent.Invocation
}
type invocationKey struct{}

func NewInvocationContext(ctx context.Context, invocation *agent.Invocation) *InvocationContext {
	return &InvocationContext{
		Context: context.WithValue(ctx, invocationKey{}, invocation),
	}
}

// InvocationFromContext returns the invocation from the context.
func InvocationFromContext(ctx context.Context) (*agent.Invocation, bool) {
	invocation, ok := ctx.Value(invocationKey{}).(*agent.Invocation)
	return invocation, ok
}
