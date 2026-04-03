//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package agent

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/internal/surfacepatch"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type surfaceReportingTestAgent struct {
	mockAgent
	surfaceIDs []string
}

func (a *surfaceReportingTestAgent) ExecutionTraceAppliedSurfaceIDs(_ *Invocation) []string {
	return append([]string(nil), a.surfaceIDs...)
}

func TestNewInvocation_InitializesExecutionTraceMetadata(t *testing.T) {
	inv := NewInvocation(
		WithInvocationAgent(&mockAgent{name: "assistant/root~"}),
		WithInvocationSession(&session.Session{ID: "session-1"}),
		WithInvocationRunOptions(RunOptions{ExecutionTraceEnabled: true}),
		WithInvocationMessage(model.NewUserMessage("hello")),
	)
	require.True(t, executionTraceEnabled(inv))
	assert.Equal(t, "assistant~1root~0", InvocationTraceNodeID(inv))
	executionTrace := BuildExecutionTrace(inv, atrace.TraceStatusCompleted)
	require.NotNil(t, executionTrace)
	assert.Equal(t, "assistant/root~", executionTrace.RootAgentName)
	assert.Equal(t, inv.InvocationID, executionTrace.RootInvocationID)
	assert.Equal(t, "session-1", executionTrace.SessionID)
	assert.Equal(t, atrace.TraceStatusCompleted, executionTrace.Status)
	assert.Empty(t, executionTrace.Steps)
}

func TestClone_PreservesExecutionTraceCaptureAndEntryPredecessors(t *testing.T) {
	root := NewInvocation(
		WithInvocationAgent(&mockAgent{name: "assistant"}),
		WithInvocationRunOptions(RunOptions{ExecutionTraceEnabled: true}),
		WithInvocationMessage(model.NewUserMessage("hello")),
	)
	rootStepID := StartExecutionTraceStep(
		root,
		InvocationTraceNodeID(root),
		&atrace.Snapshot{Text: "root input"},
		nil,
	)
	FinishExecutionTraceStep(root, rootStepID, &atrace.Snapshot{Text: "root output"}, nil)
	child := root.Clone(
		WithInvocationAgent(&mockAgent{name: "worker"}),
		WithInvocationTraceNodeID("assistant/worker"),
		WithInvocationEntryPredecessorStepIDs([]string{rootStepID}),
	)
	require.True(t, executionTraceEnabled(child))
	childStepID := StartExecutionTraceStep(
		child,
		InvocationTraceNodeID(child),
		&atrace.Snapshot{Text: "child input"},
		nil,
	)
	FinishExecutionTraceStep(child, childStepID, &atrace.Snapshot{Text: "child output"}, nil)
	executionTrace := BuildExecutionTrace(root, atrace.TraceStatusCompleted)
	require.NotNil(t, executionTrace)
	require.Len(t, executionTrace.Steps, 2)
	assert.Equal(t, rootStepID, executionTrace.Steps[0].StepID)
	assert.Equal(t, child.InvocationID, executionTrace.Steps[1].InvocationID)
	assert.Equal(t, []string{rootStepID}, executionTrace.Steps[1].PredecessorStepIDs)
	assert.Equal(t, "assistant/worker", executionTrace.Steps[1].NodeID)
}

func TestExecutionTrace_LazilyInitializesForDirectInvocationLiteral(t *testing.T) {
	inv := &Invocation{
		InvocationID: "inv-1",
		AgentName:    "assistant",
		RunOptions:   RunOptions{ExecutionTraceEnabled: true},
	}
	stepID := StartExecutionTraceStep(
		inv,
		InvocationTraceNodeID(inv),
		&atrace.Snapshot{Text: "input"},
		nil,
	)
	require.NotEmpty(t, stepID)
	FinishExecutionTraceStep(inv, stepID, &atrace.Snapshot{Text: "output"}, nil)
	executionTrace := BuildExecutionTrace(inv, atrace.TraceStatusCompleted)
	require.NotNil(t, executionTrace)
	require.Len(t, executionTrace.Steps, 1)
	assert.Equal(t, "assistant", executionTrace.RootAgentName)
	assert.Equal(t, "assistant", executionTrace.Steps[0].NodeID)
}

func TestNextExecutionTracePredecessors_UsesNestedChildInvocationTerminals(t *testing.T) {
	root := NewInvocation(
		WithInvocationAgent(&mockAgent{name: "workflow"}),
		WithInvocationRunOptions(RunOptions{ExecutionTraceEnabled: true}),
		WithInvocationMessage(model.NewUserMessage("hello")),
	)
	rootStepID := StartExecutionTraceStep(
		root,
		InvocationTraceNodeID(root),
		&atrace.Snapshot{Text: "root input"},
		nil,
	)
	FinishExecutionTraceStep(root, rootStepID, &atrace.Snapshot{Text: "root output"}, nil)
	middle := root.Clone(
		WithInvocationAgent(&mockAgent{name: "fanout"}),
		WithInvocationTraceNodeID("workflow/fanout"),
		WithInvocationEntryPredecessorStepIDs([]string{rootStepID}),
	)
	leaf := middle.Clone(
		WithInvocationAgent(&mockAgent{name: "worker"}),
		WithInvocationTraceNodeID("workflow/fanout/worker"),
		WithInvocationEntryPredecessorStepIDs([]string{rootStepID}),
	)
	leafStepID := StartExecutionTraceStep(
		leaf,
		InvocationTraceNodeID(leaf),
		&atrace.Snapshot{Text: "leaf input"},
		nil,
	)
	FinishExecutionTraceStep(leaf, leafStepID, &atrace.Snapshot{Text: "leaf output"}, nil)
	assert.Equal(t, []string{leafStepID}, NextExecutionTracePredecessors(middle))
}

func TestWithExecutionTraceEnabled_SetsRunOptions(t *testing.T) {
	var opts RunOptions
	WithExecutionTraceEnabled(true)(&opts)
	assert.True(t, opts.ExecutionTraceEnabled)
	WithExecutionTraceEnabled(false)(&opts)
	assert.False(t, opts.ExecutionTraceEnabled)
}

func TestExecutionTraceHelpers_HandleNilAndDisabledInvocation(t *testing.T) {
	var nilInv *Invocation
	assert.Empty(t, InvocationTraceNodeID(nilInv))
	assert.Empty(t, StartExecutionTraceStep(nilInv, "assistant", nil, nil))
	FinishExecutionTraceStep(nilInv, "step-1", nil, nil)
	SetExecutionTraceStepAppliedSurfaceIDs(nilInv, "step-1")
	assert.Nil(t, NextExecutionTracePredecessors(nilInv))
	assert.Nil(t, BuildExecutionTrace(nilInv, atrace.TraceStatusCompleted))
	nilInv.ensureTraceCaptureMetadata()
	disabled := NewInvocation(
		WithInvocationAgent(&mockAgent{name: "assistant"}),
		WithInvocationMessage(model.NewUserMessage("hello")),
	)
	assert.False(t, executionTraceEnabled(disabled))
	assert.Empty(t, StartExecutionTraceStep(disabled, "assistant", nil, nil))
	assert.Empty(t, StartExecutionTraceStep(disabled, "", nil, nil))
	FinishExecutionTraceStep(disabled, "step-1", nil, nil)
	FinishExecutionTraceStep(disabled, "", nil, nil)
	SetExecutionTraceStepAppliedSurfaceIDs(disabled, "step-1")
	assert.Nil(t, NextExecutionTracePredecessors(disabled))
	assert.Nil(t, BuildExecutionTrace(disabled, atrace.TraceStatusCompleted))
}

func TestExecutionTraceHelpers_RecordStepErrorAndUtilityBranches(t *testing.T) {
	inv := &Invocation{
		InvocationID: "inv-1",
		AgentName:    "assistant",
		RunOptions:   RunOptions{ExecutionTraceEnabled: true},
	}
	stepID := StartExecutionTraceStep(inv, InvocationTraceNodeID(inv), &atrace.Snapshot{Text: "input"}, []string{"entry"})
	require.NotEmpty(t, stepID)
	FinishExecutionTraceStep(inv, stepID, &atrace.Snapshot{Text: "output"}, errors.New("boom"))
	executionTrace := BuildExecutionTrace(inv, atrace.TraceStatusFailed)
	require.NotNil(t, executionTrace)
	require.Len(t, executionTrace.Steps, 1)
	assert.Equal(t, "boom", executionTrace.Steps[0].Error)
	assert.Equal(t, []string{"entry"}, executionTrace.Steps[0].PredecessorStepIDs)
	assert.Nil(t, cloneStringSlice(nil))
	assert.Equal(t, "_", escapeTraceLocalName(""))
	assert.Equal(t, "team~1worker~0", escapeTraceLocalName("team/worker~"))
}

func TestExecutionTraceHelpers_RecordAppliedSurfaceIDs(t *testing.T) {
	inv := NewInvocation(
		WithInvocationAgent(&surfaceReportingTestAgent{
			mockAgent:  mockAgent{name: "assistant"},
			surfaceIDs: []string{"assistant#instruction", "assistant#model"},
		}),
		WithInvocationRunOptions(RunOptions{ExecutionTraceEnabled: true}),
		WithInvocationMessage(model.NewUserMessage("hello")),
	)
	stepID := StartExecutionTraceStep(
		inv,
		InvocationTraceNodeID(inv),
		&atrace.Snapshot{Text: "input"},
		nil,
	)
	require.NotEmpty(t, stepID)
	SetExecutionTraceStepAppliedSurfaceIDs(inv, stepID)
	FinishExecutionTraceStep(inv, stepID, &atrace.Snapshot{Text: "output"}, nil)
	executionTrace := BuildExecutionTrace(inv, atrace.TraceStatusCompleted)
	require.NotNil(t, executionTrace)
	require.Len(t, executionTrace.Steps, 1)
	assert.Equal(t, []string{"assistant#instruction", "assistant#model"}, executionTrace.Steps[0].AppliedSurfaceIDs)
}

func TestExecutionTraceHelpers_SetAppliedSurfaceIDs_IgnoresNilAgent(t *testing.T) {
	inv := NewInvocation(
		WithInvocationAgent(&mockAgent{name: "assistant"}),
		WithInvocationRunOptions(RunOptions{ExecutionTraceEnabled: true}),
		WithInvocationMessage(model.NewUserMessage("hello")),
	)
	stepID := StartExecutionTraceStep(
		inv,
		InvocationTraceNodeID(inv),
		&atrace.Snapshot{Text: "input"},
		nil,
	)
	require.NotEmpty(t, stepID)
	inv.Agent = nil
	SetExecutionTraceStepAppliedSurfaceIDs(inv, stepID)
	FinishExecutionTraceStep(inv, stepID, &atrace.Snapshot{Text: "output"}, nil)
	executionTrace := BuildExecutionTrace(inv, atrace.TraceStatusCompleted)
	require.NotNil(t, executionTrace)
	require.Len(t, executionTrace.Steps, 1)
	assert.Empty(t, executionTrace.Steps[0].AppliedSurfaceIDs)
}

func TestExecutionTraceHelpers_SetAppliedSurfaceIDs_IgnoresEmptyStepID(t *testing.T) {
	inv := NewInvocation(
		WithInvocationAgent(&surfaceReportingTestAgent{
			mockAgent:  mockAgent{name: "assistant"},
			surfaceIDs: []string{"assistant#instruction"},
		}),
		WithInvocationRunOptions(RunOptions{ExecutionTraceEnabled: true}),
		WithInvocationMessage(model.NewUserMessage("hello")),
	)
	SetExecutionTraceStepAppliedSurfaceIDs(inv, "")
	executionTrace := BuildExecutionTrace(inv, atrace.TraceStatusCompleted)
	require.NotNil(t, executionTrace)
	assert.Empty(t, executionTrace.Steps)
}

func TestInvocationSurfaceRootNodeID_LifecycleAndFallback(t *testing.T) {
	var nilInv *Invocation
	SetInvocationSurfaceRootNodeID(nilInv, "workflow/team/coordinator")
	ClearInvocationSurfaceRootNodeID(nilInv)
	require.Empty(t, InvocationSurfaceRootNodeID(nilInv))

	inv := NewInvocation(
		WithInvocationTraceNodeID("trace/team"),
		WithInvocationRunOptions(RunOptions{
			CustomAgentConfigs: surfacepatch.WithRootNodeID(
				nil,
				"workflow/team",
			),
		}),
	)
	require.Equal(t, "workflow/team", InvocationSurfaceRootNodeID(inv))

	SetInvocationSurfaceRootNodeID(inv, "workflow/team/coordinator")
	require.Equal(t, "workflow/team/coordinator", InvocationSurfaceRootNodeID(inv))

	SetInvocationSurfaceRootNodeID(inv, "")
	require.Equal(t, "workflow/team/coordinator", InvocationSurfaceRootNodeID(inv))

	ClearInvocationSurfaceRootNodeID(inv)
	require.Equal(t, "workflow/team", InvocationSurfaceRootNodeID(inv))
}

func TestInvocationTeamMemberTraceRoot_LifecycleAndNilGuards(t *testing.T) {
	var nilInv *Invocation
	SetInvocationTeamMemberTraceRoot(nilInv, "workflow/team")
	ClearInvocationTeamMemberTraceRoot(nilInv)
	require.Empty(t, InvocationTeamMemberTraceRoot(nilInv))

	inv := NewInvocation()
	require.Empty(t, InvocationTeamMemberTraceRoot(inv))

	SetInvocationTeamMemberTraceRoot(inv, "workflow/team")
	require.Equal(t, "workflow/team", InvocationTeamMemberTraceRoot(inv))

	SetInvocationTeamMemberTraceRoot(inv, "")
	require.Equal(t, "workflow/team", InvocationTeamMemberTraceRoot(inv))

	ClearInvocationTeamMemberTraceRoot(inv)
	require.Empty(t, InvocationTeamMemberTraceRoot(inv))
}
