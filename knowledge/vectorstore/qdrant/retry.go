//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package qdrant

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// retryConfig holds the configuration for retry operations.
type retryConfig struct {
	maxRetries     int
	baseRetryDelay time.Duration
	maxRetryDelay  time.Duration
}

// isTransientError checks if the error is a transient gRPC error that can be retried.
func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	switch st.Code() {
	case codes.Unavailable, codes.ResourceExhausted, codes.Aborted, codes.DeadlineExceeded:
		return true
	default:
		return false
	}
}

// retry executes the operation with exponential backoff for transient errors.
func retry[T any](ctx context.Context, cfg retryConfig, op func() (T, error)) (T, error) {
	var result T
	var lastErr error

	for attempt := 0; attempt <= cfg.maxRetries; attempt++ {
		result, lastErr = op()
		if lastErr == nil {
			return result, nil
		}
		if !isTransientError(lastErr) {
			return result, lastErr
		}
		if attempt == cfg.maxRetries {
			break
		}
		delay := min(cfg.baseRetryDelay<<attempt, cfg.maxRetryDelay)
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-time.After(delay):
		}
	}
	return result, lastErr
}

// retryVoid executes a void operation with exponential backoff for transient errors.
func retryVoid(ctx context.Context, cfg retryConfig, op func() error) error {
	_, err := retry(ctx, cfg, func() (struct{}, error) {
		return struct{}{}, op()
	})
	return err
}
