//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package promptiter defines shared domain models used by the PromptIter workflow.
package promptiter

// TraceStatus indicates whether an execution trace can be consumed by the optimizer pipeline.
type TraceStatus string

const (
	// TraceStatusCompleted means every step in the round-DAG was executed.
	TraceStatusCompleted TraceStatus = "completed"
	// TraceStatusIncomplete means execution stopped before DAG expansion finished.
	TraceStatusIncomplete TraceStatus = "incomplete"
	// TraceStatusFailed means an error occurred and downstream step data is incomplete.
	TraceStatusFailed TraceStatus = "failed"
)

// Trace is the concrete step graph for one evaluation case in one run.
type Trace struct {
	// StructureID binds the trace to a specific structure snapshot.
	StructureID string
	// Status reports whether this trace can be used for backwarding.
	Status TraceStatus
	// FinalOutput stores the last non-empty output along the execution path.
	FinalOutput *TraceOutput
	// Steps stores executed nodes in topological order after trace expansion.
	Steps []TraceStep
}

// TraceStep captures one concrete node execution in the expanded trace.
type TraceStep struct {
	// StepID is the unique step key inside this trace.
	StepID string
	// NodeID is the logical node rendered by this step.
	NodeID string
	// PredecessorStepIDs stores direct predecessor steps that produced inputs.
	PredecessorStepIDs []string
	// AppliedSurfaceIDs stores surfaces that effectively changed this step input.
	AppliedSurfaceIDs []string
	// Input is the runtime input actually passed to this step.
	Input *TraceInput
	// Output is the runtime output produced by this step.
	Output *TraceOutput
	// Error stores step failure details, if this step is part of a failed trace.
	Error string
}

// TraceInput is the text input snapshot used by one trace step.
type TraceInput struct {
	// Text is the serialized payload fed to the step.
	Text string
}

// TraceOutput is the text output snapshot returned by one trace step.
type TraceOutput struct {
	// Text is the generated text output content.
	Text string
}
