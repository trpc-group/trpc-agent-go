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
type EvalStatus string

const (
	// EvalStatusUnknown represents an unknown evaluation status.
	EvalStatusUnknown EvalStatus = "unknown"
	// EvalStatusPassed represents a passed evaluation status.
	EvalStatusPassed EvalStatus = "passed"
	// EvalStatusFailed represents a failed evaluation status.
	EvalStatusFailed EvalStatus = "failed"
	// EvalStatusNotEvaluated represents a not evaluated evaluation status.
	EvalStatusNotEvaluated EvalStatus = "not_evaluated"
)
