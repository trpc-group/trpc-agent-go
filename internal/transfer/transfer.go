//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package transfer contains internal transfer extension contracts.
package transfer

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/agent"
)

type transferMessageContextKey struct{}

// ContextWithTransferMessage returns a context carrying the raw transfer message.
func ContextWithTransferMessage(ctx context.Context, message string) context.Context {
	return context.WithValue(ctx, transferMessageContextKey{}, message)
}

// TransferMessageFromContext returns the raw transfer message carried by ctx.
func TransferMessageFromContext(ctx context.Context) (string, bool) {
	message, ok := ctx.Value(transferMessageContextKey{}).(string)
	return message, ok
}

// InvocationCustomizer customizes a transfer target invocation before it runs.
type InvocationCustomizer interface {
	CustomizeTransferInvocation(
		ctx context.Context,
		source *agent.Invocation,
		target *agent.Invocation,
	) error
}
