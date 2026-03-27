//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package agent

import (
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/internal/surfacepatch"
	"trpc.group/trpc-go/trpc-agent-go/internal/tracecapture"
)

// WithExecutionTraceEnabled toggles execution trace recording for this run.
func WithExecutionTraceEnabled(enabled bool) RunOption {
	return func(opts *RunOptions) {
		opts.ExecutionTraceEnabled = enabled
	}
}

// WithInvocationEntryPredecessorStepIDs sets entry predecessor step ids for a child invocation.
func WithInvocationEntryPredecessorStepIDs(stepIDs []string) InvocationOptions {
	return func(inv *Invocation) {
		inv.entryPredecessorStepIDs = cloneStringSlice(stepIDs)
	}
}

// WithInvocationTraceNodeID sets the static root node id for an invocation.
func WithInvocationTraceNodeID(nodeID string) InvocationOptions {
	return func(inv *Invocation) {
		inv.traceNodeID = nodeID
	}
}

// executionTraceEnabled reports whether this invocation has execution trace enabled.
func executionTraceEnabled(inv *Invocation) bool {
	return inv != nil && inv.traceCapture != nil
}

// InvocationTraceNodeID returns the invocation root node id used by execution trace.
func InvocationTraceNodeID(inv *Invocation) string {
	if inv == nil {
		return ""
	}
	inv.ensureTraceNodeID()
	return inv.traceNodeID
}

// SetInvocationSurfaceRootNodeID stores one invocation's mounted surface root node id.
func SetInvocationSurfaceRootNodeID(inv *Invocation, nodeID string) {
	if inv == nil || nodeID == "" {
		return
	}
	inv.SetState(surfaceRootNodeIDStateKey, nodeID)
}

// ClearInvocationSurfaceRootNodeID removes one invocation's mounted surface root node id.
func ClearInvocationSurfaceRootNodeID(inv *Invocation) {
	if inv == nil {
		return
	}
	inv.DeleteState(surfaceRootNodeIDStateKey)
}

// InvocationSurfaceRootNodeID returns the effective surface root node id for one invocation.
func InvocationSurfaceRootNodeID(inv *Invocation) string {
	if inv == nil {
		return ""
	}
	if nodeID, ok := GetStateValue[string](inv, surfaceRootNodeIDStateKey); ok && nodeID != "" {
		return nodeID
	}
	return surfacepatch.RootNodeID(
		inv.RunOptions.CustomAgentConfigs,
		InvocationTraceNodeID(inv),
	)
}

// SetInvocationTeamMemberTraceRoot stores one invocation's mounted team member trace root.
func SetInvocationTeamMemberTraceRoot(inv *Invocation, rootNodeID string) {
	if inv == nil || rootNodeID == "" {
		return
	}
	inv.SetState(teamMemberTraceRootStateKey, rootNodeID)
}

// ClearInvocationTeamMemberTraceRoot removes one invocation's mounted team member trace root.
func ClearInvocationTeamMemberTraceRoot(inv *Invocation) {
	if inv == nil {
		return
	}
	inv.DeleteState(teamMemberTraceRootStateKey)
}

// InvocationTeamMemberTraceRoot returns one invocation's mounted team member trace root when present.
func InvocationTeamMemberTraceRoot(inv *Invocation) string {
	if inv == nil {
		return ""
	}
	rootNodeID, _ := GetStateValue[string](inv, teamMemberTraceRootStateKey)
	return rootNodeID
}

// StartExecutionTraceStep records a newly started real step.
func StartExecutionTraceStep(
	inv *Invocation,
	nodeID string,
	input *trace.Snapshot,
	predecessors []string,
) string {
	if inv == nil || nodeID == "" {
		return ""
	}
	inv.initializeExecutionTrace()
	if inv.traceCapture == nil {
		return ""
	}
	preds := predecessors
	if len(preds) == 0 {
		preds = inv.traceCapture.PredecessorsForInvocation(
			inv.InvocationID,
			inv.entryPredecessorStepIDs,
		)
	}
	parentInvocationID := ""
	if inv.parent != nil {
		parentInvocationID = inv.parent.InvocationID
	}
	return inv.traceCapture.StartStep(tracecapture.StartStepInput{
		InvocationID:       inv.InvocationID,
		ParentInvocationID: parentInvocationID,
		AgentName:          inv.AgentName,
		Branch:             inv.Branch,
		NodeID:             nodeID,
		StartedAt:          time.Now(),
		PredecessorStepIDs: preds,
		Input:              input,
	})
}

// FinishExecutionTraceStep finalizes a previously started step.
func FinishExecutionTraceStep(
	inv *Invocation,
	stepID string,
	output *trace.Snapshot,
	stepErr error,
) {
	if inv == nil || stepID == "" {
		return
	}
	inv.initializeExecutionTrace()
	if inv.traceCapture == nil {
		return
	}
	errText := ""
	if stepErr != nil {
		errText = stepErr.Error()
	}
	inv.traceCapture.FinishStep(stepID, output, errText, time.Now())
}

// NextExecutionTracePredecessors returns the predecessor set for the next real step or child invocation.
func NextExecutionTracePredecessors(inv *Invocation) []string {
	if inv == nil {
		return nil
	}
	inv.initializeExecutionTrace()
	if inv.traceCapture == nil {
		return nil
	}
	return inv.traceCapture.PredecessorsForInvocation(inv.InvocationID, inv.entryPredecessorStepIDs)
}

// BuildExecutionTrace builds the final trace for the root invocation.
func BuildExecutionTrace(
	inv *Invocation,
	status trace.TraceStatus,
) *trace.Trace {
	if inv == nil {
		return nil
	}
	inv.initializeExecutionTrace()
	if inv.traceCapture == nil {
		return nil
	}
	inv.ensureTraceCaptureMetadata()
	return inv.traceCapture.Build(status, time.Now())
}

func (inv *Invocation) initializeExecutionTrace() {
	if inv == nil || !inv.RunOptions.ExecutionTraceEnabled || inv.traceCapture != nil {
		return
	}
	inv.ensureTraceNodeID()
	sessionID := ""
	if inv.Session != nil {
		sessionID = inv.Session.ID
	}
	inv.traceCapture = tracecapture.New(
		inv.AgentName,
		inv.InvocationID,
		sessionID,
		time.Now(),
	)
}

func (inv *Invocation) ensureTraceCaptureMetadata() {
	if inv == nil || inv.traceCapture == nil {
		return
	}
	inv.ensureTraceNodeID()
	inv.traceCapture.SetRootAgentName(inv.AgentName)
	if inv.Session != nil {
		inv.traceCapture.SetSessionID(inv.Session.ID)
	}
}

func (inv *Invocation) ensureTraceNodeID() {
	if inv == nil || inv.traceNodeID != "" || inv.AgentName == "" {
		return
	}
	inv.traceNodeID = escapeTraceLocalName(inv.AgentName)
}

func cloneStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}

func escapeTraceLocalName(name string) string {
	if name == "" {
		return "_"
	}
	replacer := strings.NewReplacer("~", "~0", "/", "~1")
	escaped := replacer.Replace(name)
	if escaped == "" {
		return "_"
	}
	return escaped
}
