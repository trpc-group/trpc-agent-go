//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summarycontext

import "context"

// ModelCall is the built-in summarizer's published call mode for one summary
// attempt. It carries the stable mode string only, never prompt, usage, or
// content. An empty Mode means this attempt did not publish a call.
type ModelCall struct {
	Mode string
}

type modelCallKey struct{}

// WithModelCallRecorder attaches a recorder that the built-in summarizer
// fills when it records a summary model call or custom response. A nil
// recorder makes RecordModelCall a no-op.
func WithModelCallRecorder(ctx context.Context, call *ModelCall) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if call == nil {
		return ctx
	}
	return context.WithValue(ctx, modelCallKey{}, call)
}

// RecordModelCall publishes the built-in summary call mode for the current
// attempt, replacing any mode recorded earlier in the same attempt. It is a
// no-op when no recorder is attached to ctx.
func RecordModelCall(ctx context.Context, mode string) {
	if ctx == nil {
		return
	}
	recorder, ok := ctx.Value(modelCallKey{}).(*ModelCall)
	if !ok || recorder == nil {
		return
	}
	recorder.Mode = mode
}
