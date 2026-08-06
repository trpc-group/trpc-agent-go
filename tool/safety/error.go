//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import "fmt"

// BlockedError is returned by execution tools when a configured scanner
// blocks or asks for review before starting the process.
type BlockedError struct {
	Report Report
}

// NewBlockedError creates an error carrying the full safety report.
func NewBlockedError(report Report) *BlockedError {
	return &BlockedError{Report: report}
}

// Error summarizes the safety decision for ordinary error handling.
func (e *BlockedError) Error() string {
	if e == nil {
		return "tool safety guard blocked execution"
	}
	rule := e.Report.PrimaryRuleID()
	if rule == "" {
		return fmt.Sprintf("tool safety guard returned %s", e.Report.Decision)
	}
	return fmt.Sprintf("tool safety guard returned %s: %s", e.Report.Decision, rule)
}
