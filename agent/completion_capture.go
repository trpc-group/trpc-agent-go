//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package agent

import "context"

type graphCompletionCaptureKey struct{}

// WithGraphCompletionCapture keeps terminal graph completion events available
// to internal graph consumers even when caller-visible forwarding is disabled.
func WithGraphCompletionCapture(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, graphCompletionCaptureKey{}, true)
}

// WithoutGraphCompletionCapture clears any inherited capture flag for the
// current visible stream while preserving the rest of the context.
func WithoutGraphCompletionCapture(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, graphCompletionCaptureKey{}, false)
}

// ShouldCaptureGraphCompletion reports whether the current context keeps
// terminal graph completion events available for internal consumers.
func ShouldCaptureGraphCompletion(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	capture, ok := ctx.Value(graphCompletionCaptureKey{}).(bool)
	return ok && capture
}

func graphCompletionCaptureValue(ctx context.Context) (bool, bool) {
	if ctx == nil {
		return false, false
	}
	capture, ok := ctx.Value(graphCompletionCaptureKey{}).(bool)
	return capture, ok
}

// PreserveGraphCompletionCapture copies the graph completion capture setting
// from base into next when next does not provide an explicit override.
func PreserveGraphCompletionCapture(
	base context.Context,
	next context.Context,
) context.Context {
	if next == nil {
		next = context.Background()
	}
	if _, ok := graphCompletionCaptureValue(next); ok {
		return next
	}
	if capture, ok := graphCompletionCaptureValue(base); ok {
		return context.WithValue(next, graphCompletionCaptureKey{}, capture)
	}
	return next
}
