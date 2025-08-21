//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mcp

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "connection refused",
			err:      errors.New("connection refused"),
			expected: true,
		},
		{
			name:     "connection timeout",
			err:      errors.New("connection timeout"),
			expected: true,
		},
		{
			name:     "exact EOF error",
			err:      errors.New("EOF"),
			expected: true,
		},
		{
			name:     "EOF at end of error chain",
			err:      errors.New("read error: EOF"),
			expected: true,
		},
		{
			name:     "i/o timeout",
			err:      errors.New("i/o timeout"),
			expected: true,
		},
		{
			name:     "HTTP 500 error",
			err:      errors.New("HTTP 500 internal server error"),
			expected: true,
		},
		{
			name:     "status 503 error",
			err:      errors.New("status 503 service unavailable"),
			expected: true,
		},
		{
			name:     "505 followed by space",
			err:      errors.New("505 HTTP Version Not Supported"),
			expected: true,
		},
		{
			name:     "status code 511",
			err:      errors.New("status code: 511 authentication required"),
			expected: true,
		},
		{
			name:     "HTTP 408 timeout",
			err:      errors.New("408 Request Timeout"),
			expected: true,
		},
		{
			name:     "HTTP 409 conflict",
			err:      errors.New("409 Conflict"),
			expected: true,
		},
		{
			name:     "HTTP 429 rate limit",
			err:      errors.New("429 Too Many Requests"),
			expected: true,
		},

		{
			name:     "HTTP 400 error (non-retryable)",
			err:      errors.New("bad request: 400"),
			expected: false,
		},
		{
			name:     "HTTP 404 error (non-retryable)",
			err:      errors.New("not found: 404"),
			expected: false,
		},
		{
			name:     "authentication error (non-retryable)",
			err:      errors.New("authentication failed"),
			expected: false,
		},
		{
			name:     "MCP session error (non-retryable without auto-reconnect)",
			err:      errors.New("MCP session not connected"),
			expected: false,
		},
		{
			name:     "MCP transport error (non-retryable without auto-reconnect)",
			err:      errors.New("transport error"),
			expected: false,
		},
		{
			name:     "false positive: port 5001 (should not match 501)",
			err:      errors.New("port 5001 unavailable"),
			expected: false,
		},
		{
			name:     "false positive: expected 5000 items (should not match 500)",
			err:      errors.New("expected 5000 items but got 100"),
			expected: false,
		},
		{
			name:     "false positive: EOF expected (should not match EOF)",
			err:      errors.New("EOF expected at line 10"),
			expected: false,
		},
		{
			name:     "false positive: connection established (should not match connection)",
			err:      errors.New("connection established successfully"),
			expected: false,
		},
		{
			name:     "unknown error",
			err:      errors.New("some unknown error"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isRetryableError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExecuteWithRetry_Success(t *testing.T) {
	ctx := context.Background()
	retryConfig := &RetryConfig{
		MaxRetries:     3,
		InitialBackoff: 10 * time.Millisecond,
		BackoffFactor:  2.0,
		MaxBackoff:     100 * time.Millisecond,
	}

	callCount := 0
	operation := func() (any, error) {
		callCount++
		return "success", nil
	}

	result, err := executeWithRetry(ctx, retryConfig, operation, "test_operation")

	require.NoError(t, err)
	assert.Equal(t, "success", result)
	assert.Equal(t, 1, callCount, "Should succeed on first attempt")
}

func TestExecuteWithRetry_SuccessAfterRetries(t *testing.T) {
	ctx := context.Background()
	retryConfig := &RetryConfig{
		MaxRetries:     3,
		InitialBackoff: 10 * time.Millisecond,
		BackoffFactor:  2.0,
		MaxBackoff:     100 * time.Millisecond,
	}

	callCount := 0
	operation := func() (any, error) {
		callCount++
		if callCount < 3 {
			return nil, errors.New("connection timeout") // retryable error
		}
		return "success_after_retries", nil
	}

	result, err := executeWithRetry(ctx, retryConfig, operation, "test_operation")

	require.NoError(t, err)
	assert.Equal(t, "success_after_retries", result)
	assert.Equal(t, 3, callCount, "Should succeed on third attempt")
}

func TestExecuteWithRetry_NonRetryableError(t *testing.T) {
	ctx := context.Background()
	retryConfig := &RetryConfig{
		MaxRetries:     3,
		InitialBackoff: 10 * time.Millisecond,
		BackoffFactor:  2.0,
		MaxBackoff:     100 * time.Millisecond,
	}

	callCount := 0
	operation := func() (any, error) {
		callCount++
		return nil, errors.New("authentication failed") // non-retryable error
	}

	result, err := executeWithRetry(ctx, retryConfig, operation, "test_operation")

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Equal(t, 1, callCount, "Should not retry for non-retryable error")
	assert.Contains(t, err.Error(), "authentication failed")
}

func TestExecuteWithRetry_ExhaustRetries(t *testing.T) {
	ctx := context.Background()
	retryConfig := &RetryConfig{
		MaxRetries:     2,
		InitialBackoff: 10 * time.Millisecond,
		BackoffFactor:  2.0,
		MaxBackoff:     100 * time.Millisecond,
	}

	callCount := 0
	operation := func() (any, error) {
		callCount++
		return nil, errors.New("connection timeout") // retryable error
	}

	result, err := executeWithRetry(ctx, retryConfig, operation, "test_operation")

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Equal(t, 3, callCount, "Should try 3 times (initial + 2 retries)")
	assert.Contains(t, err.Error(), "connection timeout")
}

func TestExecuteWithRetry_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	retryConfig := &RetryConfig{
		MaxRetries:     3,
		InitialBackoff: 100 * time.Millisecond, // Longer backoff to allow cancellation
		BackoffFactor:  2.0,
		MaxBackoff:     1 * time.Second,
	}

	callCount := 0
	operation := func() (any, error) {
		callCount++
		return nil, errors.New("connection timeout") // retryable error
	}

	// Cancel context after a short delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	result, err := executeWithRetry(ctx, retryConfig, operation, "test_operation")

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Equal(t, 1, callCount, "Should be interrupted after first attempt")
	assert.Contains(t, err.Error(), "operation cancelled during retry backoff")
}

func TestExecuteWithRetry_NoRetryConfig(t *testing.T) {
	ctx := context.Background()

	callCount := 0
	operation := func() (any, error) {
		callCount++
		return "no_retry_result", nil
	}

	result, err := executeWithRetry(ctx, nil, operation, "test_operation")

	require.NoError(t, err)
	assert.Equal(t, "no_retry_result", result)
	assert.Equal(t, 1, callCount, "Should execute once without retry config")
}

func TestExecuteWithRetry_ZeroMaxRetries(t *testing.T) {
	ctx := context.Background()
	retryConfig := &RetryConfig{
		MaxRetries:     0, // No retries
		InitialBackoff: 10 * time.Millisecond,
		BackoffFactor:  2.0,
		MaxBackoff:     100 * time.Millisecond,
	}

	callCount := 0
	operation := func() (any, error) {
		callCount++
		return "zero_retry_result", nil
	}

	result, err := executeWithRetry(ctx, retryConfig, operation, "test_operation")

	require.NoError(t, err)
	assert.Equal(t, "zero_retry_result", result)
	assert.Equal(t, 1, callCount, "Should execute once with zero max retries")
}

func TestRetryConfig_DefaultValues(t *testing.T) {
	config := defaultRetryConfig

	assert.Equal(t, 2, config.MaxRetries)
	assert.Equal(t, 500*time.Millisecond, config.InitialBackoff)
	assert.Equal(t, 2.0, config.BackoffFactor)
	assert.Equal(t, 8*time.Second, config.MaxBackoff)
}

func TestWithSimpleRetry(t *testing.T) {
	config := &toolSetConfig{}
	option := WithSimpleRetry(5)
	option(config)

	require.NotNil(t, config.retryConfig)
	assert.Equal(t, 5, config.retryConfig.MaxRetries)
	assert.Equal(t, defaultRetryConfig.InitialBackoff, config.retryConfig.InitialBackoff)
	assert.Equal(t, defaultRetryConfig.BackoffFactor, config.retryConfig.BackoffFactor)
	assert.Equal(t, defaultRetryConfig.MaxBackoff, config.retryConfig.MaxBackoff)
}

func TestWithRetry(t *testing.T) {
	customConfig := RetryConfig{
		MaxRetries:     10,
		InitialBackoff: 200 * time.Millisecond,
		BackoffFactor:  1.5,
		MaxBackoff:     30 * time.Second,
	}

	config := &toolSetConfig{}
	option := WithRetry(customConfig)
	option(config)

	require.NotNil(t, config.retryConfig)
	assert.Equal(t, 10, config.retryConfig.MaxRetries)
	assert.Equal(t, 200*time.Millisecond, config.retryConfig.InitialBackoff)
	assert.Equal(t, 1.5, config.retryConfig.BackoffFactor)
	assert.Equal(t, 30*time.Second, config.retryConfig.MaxBackoff)
}

func TestWithSimpleRetry_Validation(t *testing.T) {
	tests := []struct {
		name           string
		input          int
		expectedOutput int
	}{
		{"negative input", -5, 0},
		{"zero input", 0, 0},
		{"normal input", 3, 3},
		{"max boundary", 10, 10},
		{"over max", 15, 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &toolSetConfig{}
			option := WithSimpleRetry(tt.input)
			option(config)

			require.NotNil(t, config.retryConfig)
			assert.Equal(t, tt.expectedOutput, config.retryConfig.MaxRetries)
		})
	}
}

func TestWithRetry_Validation(t *testing.T) {
	invalidConfig := RetryConfig{
		MaxRetries:     -1,                   // Invalid: negative
		InitialBackoff: 0,                    // Invalid: zero
		BackoffFactor:  0.5,                  // Invalid: less than 1.0
		MaxBackoff:     time.Millisecond / 2, // Invalid: less than InitialBackoff
	}

	config := &toolSetConfig{}
	option := WithRetry(invalidConfig)
	option(config)

	require.NotNil(t, config.retryConfig)
	assert.Equal(t, 0, config.retryConfig.MaxRetries)                    // Clamped to 0
	assert.Equal(t, time.Millisecond, config.retryConfig.InitialBackoff) // Clamped to minimum
	assert.Equal(t, 1.0, config.retryConfig.BackoffFactor)               // Clamped to minimum
	assert.Equal(t, time.Millisecond, config.retryConfig.MaxBackoff)     // Clamped to InitialBackoff
}

// Benchmark retry performance
func BenchmarkExecuteWithRetry_Success(b *testing.B) {
	ctx := context.Background()
	retryConfig := &RetryConfig{
		MaxRetries:     3,
		InitialBackoff: 1 * time.Millisecond,
		BackoffFactor:  2.0,
		MaxBackoff:     10 * time.Millisecond,
	}

	operation := func() (any, error) {
		return "benchmark_result", nil
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := executeWithRetry(ctx, retryConfig, operation, "benchmark_operation")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkIsRetryableError(b *testing.B) {
	testErrors := []error{
		errors.New("connection timeout"),
		errors.New("authentication failed"),
		errors.New("HTTP 500 error"),
		errors.New("not found: 404"),
		fmt.Errorf("wrapped: %w", errors.New("connection refused")),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := testErrors[i%len(testErrors)]
		_ = isRetryableError(err)
	}
}
