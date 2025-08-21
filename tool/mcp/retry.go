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
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
)

// isRetryableError determines if an error is retryable based on its characteristics.
// This function uses precise pattern matching to avoid false positives.
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())

	// Network connection errors - use precise matching to avoid false positives
	if strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "connection timeout") ||
		strings.Contains(errStr, "connection lost") ||
		strings.Contains(errStr, "connection aborted") ||
		strings.Contains(errStr, "i/o timeout") ||
		strings.Contains(errStr, "read timeout") ||
		strings.Contains(errStr, "write timeout") ||
		strings.Contains(errStr, "dial timeout") ||
		errStr == "eof" || // Exact match to avoid false positives
		strings.HasSuffix(errStr, ": eof") { // EOF at end of error chain
		return true
	}

	// HTTP status codes - use word boundary matching to avoid false positives
	// Pattern: "status code: 500" or "HTTP 500" or "500 Internal Server Error"
	if isHTTPStatusRetryable(errStr) {
		return true
	}

	// Default to non-retryable for unknown errors to avoid infinite retry loops
	return false
}

// isHTTPStatusRetryable checks if an error contains a retryable HTTP status code.
// Uses precise patterns to avoid false positives (e.g., "port 5001" won't match "501").
func isHTTPStatusRetryable(errStr string) bool {
	// Define retryable status codes: 408, 409, 429, 5xx
	retryableCodes := []string{
		"408", "409", "429",
		"500", "501", "502", "503", "504", "505", "506", "507", "508", "509", "510", "511",
	}

	for _, code := range retryableCodes {
		// Match patterns like "HTTP 500", "status 500", "500 Internal Server Error"
		if strings.Contains(errStr, "http "+code) ||
			strings.Contains(errStr, "status "+code) ||
			strings.Contains(errStr, "status: "+code) ||
			strings.Contains(errStr, "code "+code) ||
			strings.Contains(errStr, "code: "+code) ||
			strings.Contains(errStr, code+" ") { // Status code followed by space (e.g., "500 Internal")
			return true
		}
	}

	return false
}

// executeWithRetry executes a function with exponential backoff retry logic.
// It implements the retry strategy defined in the RetryConfig.
func executeWithRetry(
	ctx context.Context,
	retryConfig *RetryConfig,
	operation func() (any, error),
	operationName string,
) (any, error) {
	if retryConfig == nil || retryConfig.MaxRetries <= 0 {
		// No retry configuration, execute once
		return operation()
	}

	var lastErr error
	backoff := retryConfig.InitialBackoff

	for attempt := 0; attempt <= retryConfig.MaxRetries; attempt++ {
		result, err := operation()
		if err == nil {
			// Success on attempt
			if attempt > 0 {
				log.Debug("Operation succeeded after retry",
					"operation", operationName,
					"attempt", attempt+1,
					"total_attempts", attempt+1)
			}
			return result, nil
		}

		// Check if this error is retryable using default logic
		if !isRetryableError(err) {
			log.Debug("Non-retryable error encountered",
				"operation", operationName,
				"attempt", attempt+1,
				"error", err)
			return nil, err
		}

		lastErr = err

		// If this was the last attempt, don't wait
		if attempt >= retryConfig.MaxRetries {
			break
		}

		log.Debug("Retryable error encountered, will retry",
			"operation", operationName,
			"attempt", attempt+1,
			"max_retries", retryConfig.MaxRetries,
			"backoff", backoff,
			"error", err)

		// Wait for backoff duration
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("operation cancelled during retry backoff: %w", ctx.Err())
		case <-time.After(backoff):
			// Calculate next backoff duration with exponential growth
			backoff = time.Duration(float64(backoff) * retryConfig.BackoffFactor)
			if backoff > retryConfig.MaxBackoff {
				backoff = retryConfig.MaxBackoff
			}
		}
	}

	// All retries exhausted
	log.Error("All retry attempts exhausted",
		"operation", operationName,
		"total_attempts", retryConfig.MaxRetries+1,
		"final_error", lastErr)

	// Return the original error without additional wrapping to avoid deep error chains
	return nil, lastErr
}
