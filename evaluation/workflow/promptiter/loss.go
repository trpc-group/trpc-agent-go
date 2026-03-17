//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package promptiter defines shared domain models used by the PromptIter workflow.
package promptiter

// LossSeverity classifies terminal failures by priority and remediation urgency.
type LossSeverity string

const (
	// LossSeverityP0 is highest priority and must be fixed before lower severities.
	LossSeverityP0 LossSeverity = "P0"
	// LossSeverityP1 is critical failure requiring strong optimization response.
	LossSeverityP1 LossSeverity = "P1"
	// LossSeverityP2 is warning-level failure with moderate business impact.
	LossSeverityP2 LossSeverity = "P2"
	// LossSeverityP3 is low-priority issue for later optimization iterations.
	LossSeverityP3 LossSeverity = "P3"
)

// TerminalLoss represents one terminal grading result from evaluation.
type TerminalLoss struct {
	// EvalSetID ties this loss to one evaluation set.
	EvalSetID string
	// EvalCaseID ties this loss to one evaluation case.
	EvalCaseID string
	// MetricName identifies which metric produced this loss value.
	MetricName string
	// Severity indicates how urgently this loss should influence optimization.
	Severity LossSeverity
	// StepID links this loss back to the trace step that triggered it.
	StepID string
	// Loss is the serialized loss payload used by gradient computation.
	Loss string
}

// CaseLoss aggregates all terminal losses for one evaluation case.
type CaseLoss struct {
	// EvalSetID links this case-level record to its evaluation set.
	EvalSetID string
	// EvalCaseID links this case-level record to one sample.
	EvalCaseID string
	// TerminalLosses stores every terminal loss collected for the case.
	TerminalLosses []TerminalLoss
}
