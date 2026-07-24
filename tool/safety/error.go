// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import (
	"errors"
	"fmt"
)

// ErrBlocked marks executions blocked by the tool safety guard.
var ErrBlocked = errors.New("tool safety blocked execution")

// BlockedError carries the safety report that caused an execution block.
type BlockedError struct {
	Report Report
}

// Error returns a compact, stable blocked-execution summary.
func (e *BlockedError) Error() string {
	if e == nil {
		return ErrBlocked.Error()
	}
	return fmt.Sprintf(
		"%s: decision=%s risk=%s rule=%s recommendation=%s",
		ErrBlocked,
		e.Report.Decision,
		e.Report.RiskLevel,
		primaryRuleID(e.Report.RuleIDs),
		e.Report.Recommendation,
	)
}

// Unwrap exposes ErrBlocked for errors.Is checks.
func (e *BlockedError) Unwrap() error {
	return ErrBlocked
}

// NewBlockedError creates a sentinel-wrapped error for a blocked report.
func NewBlockedError(report Report) error {
	return &BlockedError{Report: report}
}
