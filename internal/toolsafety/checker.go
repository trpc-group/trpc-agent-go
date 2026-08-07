// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package toolsafety

import "context"

// Checker checks a single risk dimension during a safety scan.
type Checker interface {
	// ID returns the unique identifier for this checker.
	ID() string
	// Check runs the check and returns any findings.
	Check(ctx context.Context, req *ScanRequest) ([]RiskFinding, error)
	// IsEnabled reports whether this checker is active under the given policy.
	IsEnabled(policy *SafetyPolicy) bool
}

// CheckerFunc adapts a function into a Checker.
type CheckerFunc struct {
	fn      func(ctx context.Context, req *ScanRequest) ([]RiskFinding, error)
	id      string
	enabled func(policy *SafetyPolicy) bool
}

// NewCheckerFunc creates a Checker from a function.
func NewCheckerFunc(id string, fn func(ctx context.Context, req *ScanRequest) ([]RiskFinding, error), enabled func(policy *SafetyPolicy) bool) *CheckerFunc {
	if enabled == nil {
		enabled = func(*SafetyPolicy) bool { return true }
	}
	return &CheckerFunc{id: id, fn: fn, enabled: enabled}
}

// ID returns the checker identifier.
func (c *CheckerFunc) ID() string { return c.id }

// Check runs the check function and returns findings.
func (c *CheckerFunc) Check(ctx context.Context, req *ScanRequest) ([]RiskFinding, error) {
	return c.fn(ctx, req)
}

// IsEnabled reports whether this checker is active under the given policy.
func (c *CheckerFunc) IsEnabled(policy *SafetyPolicy) bool { return c.enabled(policy) }
