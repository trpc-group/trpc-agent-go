//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evolution

const defaultMinToolCalls = 4

// Policy decides whether a session delta is worth reviewing.
type Policy interface {
	ShouldReview(ctx *ReviewContext) bool
}

// defaultPolicy triggers review when there are enough tool calls, a user
// correction, or a recovered error.
type defaultPolicy struct{}

// ShouldReview implements Policy.
func (defaultPolicy) ShouldReview(ctx *ReviewContext) bool {
	if ctx == nil || len(ctx.Messages) == 0 {
		return false
	}
	if ctx.ToolCallCount >= defaultMinToolCalls {
		return true
	}
	if ctx.HasUserCorrection {
		return true
	}
	if ctx.HasRecoveredError {
		return true
	}
	return false
}
