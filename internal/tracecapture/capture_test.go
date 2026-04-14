//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tracecapture

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
)

func TestCapture_TracksFanOutJoinAndNestedInvocationPredecessors(t *testing.T) {
	startedAt := time.Date(2026, 3, 24, 10, 0, 0, 0, time.UTC)
	capture := New("assistant", "root-inv", "session-1", startedAt)
	s1 := capture.StartStep(StartStepInput{
		InvocationID: "root-inv",
		AgentName:    "assistant",
		Branch:       "assistant",
		NodeID:       "assistant/start",
		StartedAt:    startedAt,
		Input:        &atrace.Snapshot{Text: "start input"},
	})
	capture.FinishStep(s1, &atrace.Snapshot{Text: "start output"}, "", startedAt.Add(time.Second))
	s2 := capture.StartStep(StartStepInput{
		InvocationID:       "root-inv",
		AgentName:          "assistant",
		Branch:             "assistant",
		NodeID:             "assistant/branch-a",
		StartedAt:          startedAt.Add(2 * time.Second),
		PredecessorStepIDs: []string{s1},
		Input:              &atrace.Snapshot{Text: "branch a"},
	})
	capture.FinishStep(s2, &atrace.Snapshot{Text: "branch a out"}, "", startedAt.Add(3*time.Second))
	s3 := capture.StartStep(StartStepInput{
		InvocationID:       "root-inv",
		AgentName:          "assistant",
		Branch:             "assistant",
		NodeID:             "assistant/branch-b",
		StartedAt:          startedAt.Add(4 * time.Second),
		PredecessorStepIDs: []string{s1},
		Input:              &atrace.Snapshot{Text: "branch b"},
	})
	capture.FinishStep(s3, &atrace.Snapshot{Text: "branch b out"}, "", startedAt.Add(5*time.Second))
	require.Equal(t, []string{s2, s3}, capture.PredecessorsForInvocation("root-inv", nil))
	s4 := capture.StartStep(StartStepInput{
		InvocationID:       "child-inv",
		ParentInvocationID: "root-inv",
		AgentName:          "worker",
		Branch:             "assistant/worker",
		NodeID:             "assistant/worker",
		StartedAt:          startedAt.Add(6 * time.Second),
		PredecessorStepIDs: []string{s2, s3},
		Input:              &atrace.Snapshot{Text: "child input"},
	})
	capture.FinishStep(s4, &atrace.Snapshot{Text: "child out"}, "", startedAt.Add(7*time.Second))
	require.Equal(t, []string{s4}, capture.TerminalStepIDs("child-inv"))
	require.Empty(t, capture.TerminalStepIDs("root-inv"))
	s5 := capture.StartStep(StartStepInput{
		InvocationID:       "root-inv",
		AgentName:          "assistant",
		Branch:             "assistant",
		NodeID:             "assistant/join",
		StartedAt:          startedAt.Add(8 * time.Second),
		PredecessorStepIDs: []string{s4},
		Input:              &atrace.Snapshot{Text: "join input"},
	})
	capture.FinishStep(s5, &atrace.Snapshot{Text: "join out"}, "", startedAt.Add(9*time.Second))
	trace := capture.Build(atrace.TraceStatusCompleted, startedAt.Add(10*time.Second))
	require.NotNil(t, trace)
	assert.Equal(t, "assistant", trace.RootAgentName)
	assert.Equal(t, "root-inv", trace.RootInvocationID)
	assert.Equal(t, "session-1", trace.SessionID)
	assert.Equal(t, atrace.TraceStatusCompleted, trace.Status)
	require.Len(t, trace.Steps, 5)
	assert.Equal(t, []string{s1}, trace.Steps[1].PredecessorStepIDs)
	assert.Equal(t, []string{s1}, trace.Steps[2].PredecessorStepIDs)
	assert.Equal(t, []string{s2, s3}, trace.Steps[3].PredecessorStepIDs)
	assert.Equal(t, []string{s4}, trace.Steps[4].PredecessorStepIDs)
	assert.Equal(t, "start input", trace.Steps[0].Input.Text)
	assert.Equal(t, "join out", trace.Steps[4].Output.Text)
}

func TestCapture_BuildReturnsDetachedCopies(t *testing.T) {
	startedAt := time.Date(2026, 3, 24, 10, 0, 0, 0, time.UTC)
	capture := New("assistant", "root-inv", "session-1", startedAt)
	stepID := capture.StartStep(StartStepInput{
		InvocationID: "root-inv",
		AgentName:    "assistant",
		Branch:       "assistant",
		NodeID:       "assistant",
		StartedAt:    startedAt,
		Input:        &atrace.Snapshot{Text: "input"},
	})
	capture.FinishStep(stepID, &atrace.Snapshot{Text: "output"}, "", startedAt.Add(time.Second))
	trace := capture.Build(atrace.TraceStatusCompleted, startedAt.Add(2*time.Second))
	require.Len(t, trace.Steps, 1)
	trace.Steps[0].Input.Text = "mutated input"
	trace.Steps[0].Output.Text = "mutated output"
	second := capture.Build(atrace.TraceStatusCompleted, startedAt.Add(3*time.Second))
	require.Len(t, second.Steps, 1)
	assert.Equal(t, "input", second.Steps[0].Input.Text)
	assert.Equal(t, "output", second.Steps[0].Output.Text)
}

func TestCapture_AppliedSurfaceIDsAreCopiedAndUpdated(t *testing.T) {
	startedAt := time.Date(2026, 3, 24, 10, 0, 0, 0, time.UTC)
	capture := New("assistant", "root-inv", "session-1", startedAt)
	initialSurfaceIDs := []string{"assistant#instruction"}
	stepID := capture.StartStep(StartStepInput{
		InvocationID:      "root-inv",
		AgentName:         "assistant",
		Branch:            "assistant",
		NodeID:            "assistant",
		StartedAt:         startedAt,
		AppliedSurfaceIDs: initialSurfaceIDs,
	})
	initialSurfaceIDs[0] = "mutated"
	capture.SetStepAppliedSurfaceIDs("missing", []string{"ignored"})
	updatedSurfaceIDs := []string{"assistant#model", "assistant#tool"}
	capture.SetStepAppliedSurfaceIDs(stepID, updatedSurfaceIDs)
	updatedSurfaceIDs[0] = "changed"
	trace := capture.Build(atrace.TraceStatusCompleted, startedAt.Add(time.Second))
	require.Len(t, trace.Steps, 1)
	assert.Equal(t, []string{"assistant#model", "assistant#tool"}, trace.Steps[0].AppliedSurfaceIDs)
	trace.Steps[0].AppliedSurfaceIDs[0] = "trace-mutated"
	second := capture.Build(atrace.TraceStatusCompleted, startedAt.Add(2*time.Second))
	require.Len(t, second.Steps, 1)
	assert.Equal(t, []string{"assistant#model", "assistant#tool"}, second.Steps[0].AppliedSurfaceIDs)
}

func TestCapture_PredecessorsForInvocation_UsesNestedChildTerminals(t *testing.T) {
	startedAt := time.Date(2026, 3, 24, 10, 0, 0, 0, time.UTC)
	capture := New("assistant", "root-inv", "session-1", startedAt)
	workerAStepID := capture.StartStep(StartStepInput{
		InvocationID:       "worker-a-inv",
		ParentInvocationID: "parallel-inv",
		AgentName:          "worker-a",
		Branch:             "workflow/fanout/worker-a",
		NodeID:             "workflow/fanout/worker-a",
		StartedAt:          startedAt,
		Input:              &atrace.Snapshot{Text: "worker-a input"},
	})
	capture.FinishStep(workerAStepID, &atrace.Snapshot{Text: "worker-a output"}, "", startedAt.Add(time.Second))
	workerBStepID := capture.StartStep(StartStepInput{
		InvocationID:       "worker-b-inv",
		ParentInvocationID: "parallel-inv",
		AgentName:          "worker-b",
		Branch:             "workflow/fanout/worker-b",
		NodeID:             "workflow/fanout/worker-b",
		StartedAt:          startedAt.Add(2 * time.Second),
		Input:              &atrace.Snapshot{Text: "worker-b input"},
	})
	capture.FinishStep(workerBStepID, &atrace.Snapshot{Text: "worker-b output"}, "", startedAt.Add(3*time.Second))
	require.Equal(
		t,
		[]string{workerAStepID, workerBStepID},
		capture.PredecessorsForInvocation("parallel-inv", []string{"entry-step"}),
	)
}

func TestCapture_CoversNilGuardsAndMetadataFallbacks(t *testing.T) {
	var nilCapture *Capture
	assert.Nil(t, nilCapture.Build(atrace.TraceStatusCompleted, time.Time{}))
	assert.Nil(t, nilCapture.TerminalStepIDs("inv"))
	assert.Equal(t, []string{"entry"}, nilCapture.PredecessorsForInvocation("inv", []string{"entry"}))
	assert.Empty(t, nilCapture.StartStep(StartStepInput{InvocationID: "inv"}))
	nilCapture.FinishStep("step-1", nil, "", time.Time{})
	nilCapture.SetStepAppliedSurfaceIDs("step-1", []string{"assistant#instruction"})
	nilCapture.SetRootAgentName("assistant")
	nilCapture.SetSessionID("session-1")
	nilCapture.RegisterInvocation("parent", "child")
	capture := New("", "root-inv", "", time.Time{})
	require.False(t, capture.startedAt.IsZero())
	capture.SetRootAgentName("assistant")
	capture.SetRootAgentName("ignored")
	assert.Equal(t, "assistant", capture.rootAgentName)
	capture.SetSessionID("session-1")
	capture.SetSessionID("ignored")
	assert.Equal(t, "session-1", capture.sessionID)
	capture.RegisterInvocation("parent-inv", "child-inv")
	capture.RegisterInvocation("", "ignored")
	require.Contains(t, capture.childInvocationsByParent["parent-inv"], "child-inv")
	stepID := capture.StartStep(StartStepInput{
		InvocationID:       "child-inv",
		ParentInvocationID: "parent-inv",
		AgentName:          "assistant",
		NodeID:             "assistant/node",
	})
	require.NotEmpty(t, stepID)
	capture.SetStepAppliedSurfaceIDs("", []string{"assistant#instruction"})
	capture.FinishStep("missing", nil, "", time.Time{})
	capture.FinishStep(stepID, nil, "boom", time.Time{})
	trace := capture.Build(atrace.TraceStatusFailed, time.Time{})
	require.NotNil(t, trace)
	require.Len(t, trace.Steps, 1)
	assert.Equal(t, "boom", trace.Steps[0].Error)
	assert.NotZero(t, trace.EndedAt)
	assert.Nil(t, capture.TerminalStepIDs("missing"))
	assert.Equal(t, []string{"entry"}, capture.PredecessorsForInvocation("missing", []string{"entry"}))
	assert.Nil(t, capture.effectiveTerminalStepIDsLocked("", map[string]struct{}{}))
	assert.Nil(t, capture.effectiveTerminalStepIDsLocked("child-inv", map[string]struct{}{"child-inv": {}}))
	assert.Nil(t, capture.effectiveTerminalStepIDsLocked("missing", map[string]struct{}{}))
	assert.Nil(t, cloneSnapshot(nil))
}
