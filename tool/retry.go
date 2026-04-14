//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package tool provides tool interfaces and implementations for the agent system.
package tool

import (
	"context"
	"errors"
	"io"
	"net"
	"time"
)

// RetryInfo contains the current tool-call attempt information exposed to retry policies.
type RetryInfo struct {
	// ToolName is the resolved tool name for the current invocation.
	ToolName string
	// ToolCallID is the model-issued identifier for the current tool call.
	ToolCallID string
	// Arguments contains the final JSON arguments passed into the tool call.
	Arguments []byte
	// Attempt is the current attempt number, starting from one.
	Attempt int
	// MaxAttempts is the configured total attempt budget, including the first try.
	MaxAttempts int
	// Result is the raw result returned by the current attempt.
	Result any
	// Error is the raw error returned by the current attempt.
	Error error
	// ResultError reports whether the raw result was classified as a result-level failure.
	ResultError bool
}

// RetryPolicy defines retry behavior for a single callable tool call.
type RetryPolicy struct {
	// MaxAttempts is the total number of attempts, including the first try.
	MaxAttempts int
	// InitialInterval is the delay applied before the second attempt.
	InitialInterval time.Duration
	// BackoffFactor controls how the delay grows after each failed attempt.
	BackoffFactor float64
	// MaxInterval caps the computed delay when it is greater than zero.
	MaxInterval time.Duration
	// Jitter enables additive random jitter on the computed delay.
	Jitter bool
	// RetryOn decides whether the framework should retry the current attempt.
	RetryOn func(context.Context, *RetryInfo) (bool, error)
}

// DefaultRetryOn applies the framework's default retry decision for tool calls.
func DefaultRetryOn(ctx context.Context, info *RetryInfo) (bool, error) {
	if ctx != nil && ctx.Err() != nil {
		return false, nil
	}
	if info == nil || info.Error == nil || info.ResultError {
		return false, nil
	}
	if errors.Is(info.Error, context.Canceled) || errors.Is(info.Error, context.DeadlineExceeded) {
		return false, nil
	}
	if errors.Is(info.Error, io.EOF) || errors.Is(info.Error, io.ErrUnexpectedEOF) {
		return true, nil
	}
	var netErr net.Error
	if errors.As(info.Error, &netErr) {
		if netErr.Timeout() {
			return true, nil
		}
		// Temporary is deprecated but still widely implemented by network stacks.
		if netErr.Temporary() {
			return true, nil
		}
	}
	return false, nil
}
