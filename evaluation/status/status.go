//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package status provides the status of an evaluation.
package status

// EvalStatus represents the status of an evaluation.
type EvalStatus int

const (
	// EvalStatusUnknown represents an unknown evaluation status.
	EvalStatusUnknown EvalStatus = iota
	// EvalStatusPassed represents a passed evaluation status.
	EvalStatusPassed
	// EvalStatusFailed represents a failed evaluation status.
	EvalStatusFailed
	// EvalStatusNotEvaluated represents a not evaluated evaluation status.
	EvalStatusNotEvaluated
)

const (
	statusUnknownStr      = "unknown"
	statusPassedStr       = "passed"
	statusFailedStr       = "failed"
	statusNotEvaluatedStr = "not_evaluated"
)

// String returns the string representation of the evaluation status.
func (s EvalStatus) String() string {
	switch s {
	case EvalStatusPassed:
		return statusPassedStr
	case EvalStatusFailed:
		return statusFailedStr
	case EvalStatusNotEvaluated:
		return statusNotEvaluatedStr
	default:
		return statusUnknownStr
	}
}
