//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool

import (
	"context"
	"sync/atomic"
)

// ContextKeyToolCallID is the context key type for tool call ID.
// It's exported so that the framework can inject tool call ID into context.
type ContextKeyToolCallID struct{}

type contextKeyStructuredStreamErrors struct{}
type contextKeyFinalResultChunks struct{}
type contextKeyToolResultAttachmentBudget struct{}

type toolResultAttachmentBudget struct {
	max  int64
	used atomic.Int64
}

// ToolCallIDFromContext retrieves tool call ID from context.
// Returns the tool call ID and true if found, empty string and false
// otherwise.
func ToolCallIDFromContext(ctx context.Context) (string, bool) {
	toolCallID, ok := ctx.Value(ContextKeyToolCallID{}).(string)
	return toolCallID, ok
}

// WithStructuredStreamErrors marks a streamable-tool invocation as expecting
// structured error chunks instead of plain text fallback content.
func WithStructuredStreamErrors(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, contextKeyStructuredStreamErrors{}, true)
}

// StructuredStreamErrorsFromContext reports whether structured stream error
// chunks are enabled for the current streamable-tool invocation.
func StructuredStreamErrorsFromContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	enabled, _ := ctx.Value(contextKeyStructuredStreamErrors{}).(bool)
	return enabled
}

// WithFinalResultChunks marks a streamable-tool invocation as expecting final
// result chunks for framework-managed completion handling.
func WithFinalResultChunks(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, contextKeyFinalResultChunks{}, true)
}

// FinalResultChunksFromContext reports whether final result chunks are enabled
// for the current streamable-tool invocation.
func FinalResultChunksFromContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	enabled, _ := ctx.Value(contextKeyFinalResultChunks{}).(bool)
	return enabled
}

// WithToolResultAttachmentBudget limits callback-managed attachments for one
// tool response processing pass.
func WithToolResultAttachmentBudget(
	ctx context.Context,
	maxAttachments int,
) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	budget := &toolResultAttachmentBudget{
		max: int64(maxAttachments),
	}
	return context.WithValue(
		ctx,
		contextKeyToolResultAttachmentBudget{},
		budget,
	)
}

// EnsureToolResultAttachmentBudget installs a budget only when none exists.
func EnsureToolResultAttachmentBudget(
	ctx context.Context,
	maxAttachments int,
) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if toolResultAttachmentBudgetFromContext(ctx) != nil {
		return ctx
	}
	return WithToolResultAttachmentBudget(ctx, maxAttachments)
}

// WithoutToolResultAttachmentBudget hides any inherited attachment budget.
// This is useful when invoking a nested tool or agent from inside a tool body;
// the outer processor can still use the original context for this tool result.
func WithoutToolResultAttachmentBudget(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(
		ctx,
		contextKeyToolResultAttachmentBudget{},
		(*toolResultAttachmentBudget)(nil),
	)
}

// ReserveToolResultAttachments reserves up to requested attachment slots from
// the current tool result budget. Without a budget it preserves legacy
// behavior and grants the full request.
func ReserveToolResultAttachments(
	ctx context.Context,
	requested int,
) int {
	if requested <= 0 {
		return 0
	}
	if ctx == nil {
		return requested
	}
	budget := toolResultAttachmentBudgetFromContext(ctx)
	if budget == nil {
		return requested
	}
	for {
		used := budget.used.Load()
		available := budget.max - used
		if available <= 0 {
			return 0
		}
		granted := int64(requested)
		if granted > available {
			granted = available
		}
		if budget.used.CompareAndSwap(used, used+granted) {
			return int(granted)
		}
	}
}

func toolResultAttachmentBudgetFromContext(
	ctx context.Context,
) *toolResultAttachmentBudget {
	if ctx == nil {
		return nil
	}
	budget, _ := ctx.Value(
		contextKeyToolResultAttachmentBudget{},
	).(*toolResultAttachmentBudget)
	return budget
}
