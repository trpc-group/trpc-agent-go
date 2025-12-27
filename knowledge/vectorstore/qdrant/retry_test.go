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
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var testRetryCfg = retryConfig{
	maxRetries:     3,
	baseRetryDelay: 1 * time.Millisecond, // Fast for tests
	maxRetryDelay:  10 * time.Millisecond,
}

func TestIsTransientError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"non-gRPC error", errors.New("generic error"), false},
		{"unavailable", status.Error(codes.Unavailable, "unavailable"), true},
		{"resource exhausted", status.Error(codes.ResourceExhausted, "rate limited"), true},
		{"aborted", status.Error(codes.Aborted, "aborted"), true},
		{"not found", status.Error(codes.NotFound, "not found"), false},
		{"internal", status.Error(codes.Internal, "internal"), false},
		{"invalid argument", status.Error(codes.InvalidArgument, "invalid"), false},
		{"permission denied", status.Error(codes.PermissionDenied, "denied"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, isTransientError(tt.err))
		})
	}
}

func TestRetry_Success(t *testing.T) {
	t.Parallel()
	attempts := 0
	result, err := retry(context.Background(), testRetryCfg, func() (string, error) {
		attempts++
		return "success", nil
	})

	assert.NoError(t, err)
	assert.Equal(t, "success", result)
	assert.Equal(t, 1, attempts)
}

func TestRetry_SuccessAfterTransientError(t *testing.T) {
	t.Parallel()
	attempts := 0
	result, err := retry(context.Background(), testRetryCfg, func() (string, error) {
		attempts++
		if attempts < 3 {
			return "", status.Error(codes.Unavailable, "unavailable")
		}
		return "success", nil
	})

	assert.NoError(t, err)
	assert.Equal(t, "success", result)
	assert.Equal(t, 3, attempts)
}

func TestRetry_NonTransientError(t *testing.T) {
	t.Parallel()
	attempts := 0
	_, err := retry(context.Background(), testRetryCfg, func() (string, error) {
		attempts++
		return "", status.Error(codes.NotFound, "not found")
	})

	assert.Error(t, err)
	assert.Equal(t, 1, attempts)
}

func TestRetry_ExhaustedRetries(t *testing.T) {
	t.Parallel()
	cfg := retryConfig{
		maxRetries:     2,
		baseRetryDelay: 1 * time.Millisecond,
		maxRetryDelay:  10 * time.Millisecond,
	}
	attempts := 0
	_, err := retry(context.Background(), cfg, func() (string, error) {
		attempts++
		return "", status.Error(codes.Unavailable, "unavailable")
	})

	assert.Error(t, err)
	assert.Equal(t, 3, attempts) // initial + 2 retries
}

func TestRetry_ContextCanceled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	attempts := 0
	_, err := retry(ctx, testRetryCfg, func() (string, error) {
		attempts++
		return "", status.Error(codes.Unavailable, "unavailable")
	})

	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)
	assert.Equal(t, 1, attempts)
}

func TestRetryVoid_Success(t *testing.T) {
	t.Parallel()
	attempts := 0
	err := retryVoid(context.Background(), testRetryCfg, func() error {
		attempts++
		return nil
	})

	assert.NoError(t, err)
	assert.Equal(t, 1, attempts)
}

func TestRetryVoid_SuccessAfterTransientError(t *testing.T) {
	t.Parallel()
	attempts := 0
	err := retryVoid(context.Background(), testRetryCfg, func() error {
		attempts++
		if attempts < 2 {
			return status.Error(codes.ResourceExhausted, "rate limited")
		}
		return nil
	})

	assert.NoError(t, err)
	assert.Equal(t, 2, attempts)
}

func TestRetry_ZeroRetries(t *testing.T) {
	t.Parallel()
	cfg := retryConfig{
		maxRetries:     0,
		baseRetryDelay: 1 * time.Millisecond,
		maxRetryDelay:  10 * time.Millisecond,
	}
	attempts := 0
	_, err := retry(context.Background(), cfg, func() (string, error) {
		attempts++
		return "", status.Error(codes.Unavailable, "unavailable")
	})

	assert.Error(t, err)
	assert.Equal(t, 1, attempts)
}

func TestRetry_CustomDelays(t *testing.T) {
	t.Parallel()
	cfg := retryConfig{
		maxRetries:     2,
		baseRetryDelay: 50 * time.Millisecond,
		maxRetryDelay:  100 * time.Millisecond,
	}

	start := time.Now()
	attempts := 0
	_, _ = retry(context.Background(), cfg, func() (string, error) {
		attempts++
		return "", status.Error(codes.Unavailable, "unavailable")
	})

	elapsed := time.Since(start)
	// Should have delays: 50ms + 100ms (capped) = 150ms minimum
	assert.GreaterOrEqual(t, elapsed.Milliseconds(), int64(100))
	assert.Equal(t, 3, attempts)
}
