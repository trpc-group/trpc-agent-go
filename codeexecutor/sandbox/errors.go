//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

import (
	"errors"
	"fmt"
)

// ErrorKind identifies a typed sandbox failure.
type ErrorKind string

const (
	// ErrDenied indicates a generic policy denial.
	ErrDenied ErrorKind = "Denied"
	// ErrTimeout indicates that execution exceeded its deadline.
	ErrTimeout ErrorKind = "Timeout"
	// ErrUnsupportedBackend indicates that the requested sandbox backend is
	// not available for the host platform or policy.
	ErrUnsupportedBackend ErrorKind = "UnsupportedBackend"
	// ErrSetupFailed indicates that sandbox setup or preflight failed.
	ErrSetupFailed ErrorKind = "SetupFailed"
	// ErrPolicyViolation indicates that a request is internally inconsistent
	// with the configured policy.
	ErrPolicyViolation ErrorKind = "PolicyViolation"
	// ErrNetworkDenied indicates that network access was denied by policy.
	ErrNetworkDenied ErrorKind = "NetworkDenied"
	// ErrPathDenied indicates that filesystem access was denied by policy.
	ErrPathDenied ErrorKind = "PathDenied"
)

// SandboxError is returned when sandbox policy, setup, or execution control
// rejects a request. It is intentionally small so callers can safely log it.
type SandboxError struct {
	Kind    ErrorKind
	Op      string
	Path    string
	Backend string
	Err     error
}

func (e *SandboxError) Error() string {
	if e == nil {
		return ""
	}
	msg := string(e.Kind)
	if e.Op != "" {
		msg += " " + e.Op
	}
	if e.Path != "" {
		msg += " " + e.Path
	}
	if e.Backend != "" {
		msg += " backend=" + e.Backend
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

// Unwrap returns the underlying cause.
func (e *SandboxError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func sandboxError(kind ErrorKind, op string, path string, err error) error {
	return &SandboxError{Kind: kind, Op: op, Path: path, Err: err}
}

func backendError(kind ErrorKind, backend string, err error) error {
	return &SandboxError{Kind: kind, Backend: backend, Err: err}
}

// IsKind reports whether err contains a SandboxError with the requested kind.
func IsKind(err error, kind ErrorKind) bool {
	var se *SandboxError
	if !errors.As(err, &se) {
		return false
	}
	return se.Kind == kind
}

func deniedf(kind ErrorKind, op string, path string, format string, args ...any) error {
	return sandboxError(kind, op, path, fmt.Errorf(format, args...))
}
