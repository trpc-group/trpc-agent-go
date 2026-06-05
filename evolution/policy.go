//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evolution

import "context"

const defaultMinToolCalls = 4

// ReviewPolicy decides whether a session delta is worth reviewing.
type ReviewPolicy interface {
	ShouldReview(ctx context.Context, input *ReviewPolicyInput) (bool, error)
}

// DefaultReviewPolicy is the built-in review trigger policy.
//
// The zero value is usable: it triggers review when the delta has at least
// 4 tool calls, a user correction, or a recovered tool error.
type DefaultReviewPolicy struct {
	// MinToolCalls is the tool-call threshold that triggers review. Zero uses
	// the built-in default. A negative value disables the tool-call trigger.
	MinToolCalls int

	// DisableUserCorrectionTrigger disables review when the delta contains a
	// user correction after an assistant turn.
	DisableUserCorrectionTrigger bool

	// DisableRecoveredErrorTrigger disables review when the delta shows the
	// assistant recovering after a tool error.
	DisableRecoveredErrorTrigger bool
}

// ShouldReview implements ReviewPolicy.
func (p DefaultReviewPolicy) ShouldReview(ctx context.Context, input *ReviewPolicyInput) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if input == nil || input.ReviewContext == nil {
		return false, nil
	}
	reviewCtx := input.ReviewContext
	if len(reviewCtx.Messages) == 0 {
		return false, nil
	}

	minToolCalls := p.MinToolCalls
	if minToolCalls == 0 {
		minToolCalls = defaultMinToolCalls
	}
	if minToolCalls > 0 && reviewCtx.ToolCallCount >= minToolCalls {
		return true, nil
	}
	if !p.DisableUserCorrectionTrigger && reviewCtx.HasUserCorrection {
		return true, nil
	}
	if !p.DisableRecoveredErrorTrigger && reviewCtx.HasRecoveredError {
		return true, nil
	}
	return false, nil
}
