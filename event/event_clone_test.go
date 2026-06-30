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
	"encoding/json"
	"testing"
	"time"

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
		Extensions: map[string]json.RawMessage{
			"ext": json.RawMessage(`{"name":"value"}`),
		},
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
	c.Extensions["ext"] = json.RawMessage(`{"name":"changed"}`)
	require.NotEqual(
		t,
		string(e.Extensions["ext"]),
		string(c.Extensions["ext"]),
	)

	clonedRaw := c.Extensions["ext"]
	clonedRaw[0] = 'x'
	c.Extensions["ext"] = clonedRaw
	require.NotEqual(
		t,
		e.Extensions["ext"][0],
		c.Extensions["ext"][0],
	)
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
			Usage: &model.Usage{
				PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3,
				TimingInfo: &model.TimingInfo{FirstTokenDuration: time.Second},
			},
			Steps: []trace.Step{
				{
					StepID:             "s1",
					NodeID:             "assistant",
					PredecessorStepIDs: []string{"s0"},
					Input:              &trace.Snapshot{Text: "input"},
					Output:             &trace.Snapshot{Text: "output"},
					Usage: &model.Usage{
						PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3,
						TimingInfo: &model.TimingInfo{ReasoningDuration: time.Second},
					},
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
	clone.ExecutionTrace.Steps[0].Usage.TotalTokens = 99
	clone.ExecutionTrace.Steps[0].Usage.TimingInfo.ReasoningDuration = 2 * time.Second
	clone.ExecutionTrace.Usage.TotalTokens = 88
	clone.ExecutionTrace.Usage.TimingInfo.FirstTokenDuration = 3 * time.Second
	require.Equal(t, []string{"s0"}, e.ExecutionTrace.Steps[0].PredecessorStepIDs)
	require.Equal(t, "input", e.ExecutionTrace.Steps[0].Input.Text)
	require.Equal(t, 3, e.ExecutionTrace.Steps[0].Usage.TotalTokens)
	require.Equal(t, time.Second, e.ExecutionTrace.Steps[0].Usage.TimingInfo.ReasoningDuration)
	require.Equal(t, 3, e.ExecutionTrace.Usage.TotalTokens)
	require.Equal(t, time.Second, e.ExecutionTrace.Usage.TimingInfo.FirstTokenDuration)
}

func TestEvent_Clone_ExecutionTraceKeepsNilUsage(t *testing.T) {
	e := &Event{
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Done:   true,
		},
		ExecutionTrace: &trace.Trace{
			Steps: []trace.Step{{StepID: "s1"}},
		},
	}
	clone := e.Clone()
	require.NotNil(t, clone)
	require.NotNil(t, clone.ExecutionTrace)
	require.Nil(t, clone.ExecutionTrace.Usage)
	require.Len(t, clone.ExecutionTrace.Steps, 1)
	require.Nil(t, clone.ExecutionTrace.Steps[0].Usage)
}
