//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package evalresult provides evaluation result for evaluation set.
package evalresult

type EvalStatus int

const (
	EvalStatusUnknown EvalStatus = iota
	EvalStatusPassed
	EvalStatusFailed
	EvalStatusNotEvaluated
)

func (s EvalStatus) String() string {
	switch s {
	case EvalStatusPassed:
		return "passed"
	case EvalStatusFailed:
		return "failed"
	case EvalStatusNotEvaluated:
		return "not_evaluated"
	default:
		return "unknown"
	}
}
