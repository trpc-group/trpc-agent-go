//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package trace defines the public execution trace model.
package trace

import "time"

// TraceStatus describes the overall status of a single runner.Run execution trace.
type TraceStatus string

const (
	// TraceStatusCompleted indicates the run completed successfully.
	TraceStatusCompleted TraceStatus = "completed"
	// TraceStatusIncomplete indicates the run stopped before full completion without a terminal error.
	TraceStatusIncomplete TraceStatus = "incomplete"
	// TraceStatusFailed indicates the run ended with a terminal error.
	TraceStatusFailed TraceStatus = "failed"
)

// Trace is the root execution trace artifact for a single runner.Run.
type Trace struct {
	RootAgentName    string
	RootInvocationID string
	SessionID        string
	StartedAt        time.Time
	EndedAt          time.Time
	Status           TraceStatus
	Steps            []Step
}

// Step is a single recorded execution step.
type Step struct {
	StepID             string
	InvocationID       string
	ParentInvocationID string
	AgentName          string
	Branch             string
	NodeID             string
	StartedAt          time.Time
	EndedAt            time.Time
	PredecessorStepIDs []string
	AppliedSurfaceIDs  []string
	Input              *Snapshot
	Output             *Snapshot
	Error              string
}

// Snapshot stores a stable text snapshot for a step input or output.
type Snapshot struct {
	Text string
}
