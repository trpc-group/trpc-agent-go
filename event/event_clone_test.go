//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//
//

package event

import (
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestEvent_Clone_DeepCopy(t *testing.T) {
	e := &Event{
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Done:   true,
		},
		InvocationID:       "inv-1",
		Author:             "tester",
		LongRunningToolIDs: map[string]struct{}{"a": {}, "b": {}},
		StateDelta:         map[string][]byte{"k": []byte("v")},
	}

	c := e.Clone()
	require.NotNil(t, c)
	require.NotSame(t, e, c)
	// Mutate clone and ensure original not affected.
	c.LongRunningToolIDs["c"] = struct{}{}
	c.StateDelta["k"][0] = 'x'
	_, ok := e.LongRunningToolIDs["c"]
	require.False(t, ok)
	require.NotEqual(t, string(e.StateDelta["k"]), string(c.StateDelta["k"]))
}

func TestEvent_Clone_LegacyVersionMigratesFilterKey(t *testing.T) {
	e := &Event{
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Done:   true,
		},
		InvocationID: "inv-legacy",
		Author:       "tester",
		Branch:       "legacy/branch",
		Version:      InitVersion,
	}

	clone := e.Clone()
	require.NotNil(t, clone)
	require.Equal(t, CurrentVersion, clone.Version)
	require.Equal(t, e.Branch, clone.FilterKey)
	require.Equal(t, e.Branch, clone.Branch)
	require.NotEqual(t, clone.ID, e.ID)
}

func TestEvent_Clone_DeepCopiesExecutionTrace(t *testing.T) {
	e := &Event{
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Done:   true,
		},
		ExecutionTrace: &trace.Trace{
			RootAgentName:    "assistant",
			RootInvocationID: "inv-1",
			Steps: []trace.Step{
				{
					StepID:             "s1",
					NodeID:             "assistant",
					PredecessorStepIDs: []string{"s0"},
					Input:              &trace.Snapshot{Text: "input"},
					Output:             &trace.Snapshot{Text: "output"},
				},
			},
		},
	}
	clone := e.Clone()
	require.NotNil(t, clone)
	require.NotNil(t, clone.ExecutionTrace)
	require.NotSame(t, e.ExecutionTrace, clone.ExecutionTrace)
	clone.ExecutionTrace.Steps[0].PredecessorStepIDs[0] = "changed"
	clone.ExecutionTrace.Steps[0].Input.Text = "updated"
	require.Equal(t, []string{"s0"}, e.ExecutionTrace.Steps[0].PredecessorStepIDs)
	require.Equal(t, "input", e.ExecutionTrace.Steps[0].Input.Text)
}
