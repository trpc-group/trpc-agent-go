//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package codeexecutor

import "context"

type executionIntentContextKey struct{}

// WithExecutionIntent annotates a context with the sandbox execution intent.
func WithExecutionIntent(
	ctx context.Context,
	intent ExecutionIntent,
) context.Context {
	if ctx == nil || intent == "" {
		return ctx
	}
	return context.WithValue(ctx, executionIntentContextKey{}, intent)
}

// ExecutionIntentFromContext returns the sandbox execution intent carried by
// the context, if any.
func ExecutionIntentFromContext(
	ctx context.Context,
) (ExecutionIntent, bool) {
	if ctx == nil {
		return "", false
	}
	intent, ok := ctx.Value(executionIntentContextKey{}).(ExecutionIntent)
	if !ok || intent == "" {
		return "", false
	}
	return intent, true
}
