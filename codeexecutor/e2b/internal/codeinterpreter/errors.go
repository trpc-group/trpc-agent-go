//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package codeinterpreter

import (
	"errors"
	"fmt"
)

// SandboxError is the generic error returned by the SDK for unexpected
// server responses.
type SandboxError struct {
	Message    string
	StatusCode int
}

func (e *SandboxError) Error() string {
	if e.StatusCode != 0 {
		return fmt.Sprintf("sandbox error (%d): %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("sandbox error: %s", e.Message)
}

// NotFoundError is returned when a resource (context, sandbox, file) is missing.
type NotFoundError struct {
	Message string
}

func (e *NotFoundError) Error() string { return "not found: " + e.Message }

// TimeoutError is returned when a RunCode header or body deadline fires, and
// for some other HTTP failures, including a non-stream request timeout and a
// gateway timeout.
//
// For a RunCode deadline, Message identifies which one fired. A request timeout means the
// client stopped waiting for response headers. An execution timeout means the
// client stopped reading the response body after headers arrived, including
// an HTTP error body. Neither means the remote code or sandbox was killed.
// An http.Client.Timeout or other transport deadline is not a TimeoutError.
// RunCode keeps that error and records whether headers had already arrived.
// Increasing RequestTimeout or Timeout does not extend the client deadline.
//
// Caller cancellation and a caller context deadline are returned as that
// context error, not as TimeoutError. Unwrap returns the RunCode deadline
// cause when one was recorded, and nil for TimeoutError values created
// without a cause, including HTTP status errors. The cause chain identifies
// the header or execution deadline and does not match context.Canceled or
// context.DeadlineExceeded. A transport timeout stays outside this chain, so
// its own net/http error can still match context.DeadlineExceeded.
type TimeoutError struct {
	Message string
	cause   error
}

func (e *TimeoutError) Error() string { return "timeout: " + e.Message }

// Unwrap returns the cause recorded for this timeout, or nil when none was recorded.
func (e *TimeoutError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// InvalidArgumentError is returned when input parameters are invalid
// (e.g. providing both `context` and `language`).
type InvalidArgumentError struct {
	Message string
}

func (e *InvalidArgumentError) Error() string { return "invalid argument: " + e.Message }

// AuthenticationError is returned when the supplied API key is invalid or
// missing.
type AuthenticationError struct {
	Message string
}

func (e *AuthenticationError) Error() string { return "authentication error: " + e.Message }

// RateLimitError is returned when the caller has exceeded the API's rate limit.
type RateLimitError struct {
	Message string
}

func (e *RateLimitError) Error() string { return "rate limit: " + e.Message }

const (
	requestTimeoutMessage = "Request timed out — the 'RequestTimeout' option can be " +
		"used to increase this timeout"
	executionTimeoutMessage = "Execution timed out — the 'Timeout' option can be " +
		"used to increase this timeout"
)

// errRequestTimeout is the cause of a RunCode HTTP header deadline.
// It is not a caller cancellation or a caller deadline.
var errRequestTimeout = errors.New("code interpreter: request timeout")

// errExecutionTimeout is the cause of a RunCode body-read deadline.
// It is not a caller cancellation or a caller deadline.
var errExecutionTimeout = errors.New("code interpreter: execution timeout")

// formatRequestTimeoutError reports an HTTP request timeout without a cause.
func formatRequestTimeoutError() error {
	return &TimeoutError{Message: requestTimeoutMessage}
}

func runCodeRequestTimeout(trigger error) error {
	return &TimeoutError{
		Message: requestTimeoutMessage,
		cause:   timeoutCauseOf(errRequestTimeout, trigger),
	}
}

func runCodeExecutionTimeout(trigger error) error {
	return &TimeoutError{
		Message: executionTimeoutMessage,
		cause:   timeoutCauseOf(errExecutionTimeout, trigger),
	}
}

// timeoutCause keeps the transport or context error that fired a RunCode
// deadline without making that error visible to errors.Is. Caller cancellation
// and caller deadlines therefore cannot be confused with this deadline.
type timeoutCause struct {
	sentinel error
	trigger  error
}

func timeoutCauseOf(sentinel, trigger error) error {
	if trigger == nil || trigger == sentinel {
		return sentinel
	}
	return &timeoutCause{sentinel: sentinel, trigger: trigger}
}

func (e *timeoutCause) Error() string {
	return e.sentinel.Error() + ": " + e.trigger.Error()
}

func (e *timeoutCause) Unwrap() error { return e.sentinel }
