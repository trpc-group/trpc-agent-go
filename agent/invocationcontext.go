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

// GetStateValueFromContext retrieves a typed value from the invocation state
// stored in the context.
//
// Returns the typed value and true if the invocation exists, the key exists,
// and the type matches, or the zero value and false otherwise.
//
// Example:
//
//	if startTime, ok := GetStateValueFromContext[time.Time](ctx, "agent:start_time"); ok {
//	    duration := time.Since(startTime)
//	}
//	if requestID, ok := GetStateValueFromContext[string](ctx, "middleware:request_id"); ok {
//	    log.Printf("Request ID: %s", requestID)
//	}
func GetStateValueFromContext[T any](ctx context.Context, key string) (T, bool) {
	var zero T
	inv, ok := InvocationFromContext(ctx)
	if !ok {
		return zero, false
	}
	return GetStateValue[T](inv, key)
}

// GetRuntimeStateValueFromContext retrieves a typed value from the runtime state
// stored in the invocation's RunOptions within the context.
//
// Returns the typed value and true if the invocation exists, the key exists in
// RuntimeState, and the type matches, or the zero value and false otherwise.
//
// Example:
//
//	if userID, ok := GetRuntimeStateValueFromContext[string](ctx, "user_id"); ok {
//	    log.Printf("User ID: %s", userID)
//	}
//	if roomID, ok := GetRuntimeStateValueFromContext[int](ctx, "room_id"); ok {
//	    log.Printf("Room ID: %d", roomID)
//	}
func GetRuntimeStateValueFromContext[T any](ctx context.Context, key string) (T, bool) {
	var zero T
	inv, ok := InvocationFromContext(ctx)
	if !ok || inv == nil {
		return zero, false
	}
	return GetRuntimeStateValue[T](&inv.RunOptions, key)
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
