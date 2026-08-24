//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package graph

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
)

func TestEmitCustomStateDelta_EmitsEvent(t *testing.T) {
	eventCh := make(chan *event.Event, 1)
	inv := agent.NewInvocation(
		agent.WithInvocationID("inv-from-invocation"),
		agent.WithInvocationBranch("branch"),
		agent.WithInvocationEventFilterKey("filter"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			RequestID: "req",
		}),
	)
	state := State{
		StateKeyExecContext: &ExecutionContext{
			InvocationID: "stale-invocation",
			Invocation:   inv,
			EventChan:    eventCh,
		},
		StateKeyCurrentNodeID: "step",
	}

	err := EmitCustomStateDelta(
		context.Background(),
		state,
		State{"result": map[string]any{"ok": true}},
		WithStateDeltaEventType("custom_state"),
		WithStateDeltaEventMessage("carry state"),
	)
	require.NoError(t, err)

	evt := requireEmitterEvent(t, eventCh)
	require.NotNil(t, evt)
	require.Equal(t, "req", evt.RequestID)
	require.Equal(t, "inv-from-invocation", evt.InvocationID)
	require.Equal(t, "branch", evt.Branch)
	require.Equal(t, "filter", evt.FilterKey)
	require.Equal(t, ObjectTypeGraphNodeCustom, evt.Object)
	require.Contains(t, evt.StateDelta, "result")
	require.Contains(t, evt.StateDelta, MetadataKeyNodeCustom)
	var metadata NodeCustomEventMetadata
	require.NoError(t, json.Unmarshal(evt.StateDelta[MetadataKeyNodeCustom], &metadata))
	require.Equal(t, "inv-from-invocation", metadata.InvocationID)
}

func TestEmitCustomStateDelta_PreservesFallbackInvocationID(t *testing.T) {
	eventCh := make(chan *event.Event, 1)
	inv := agent.NewInvocation(
		agent.WithInvocationID(""),
		agent.WithInvocationBranch("branch"),
		agent.WithInvocationEventFilterKey("filter"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			RequestID: "req",
		}),
	)
	state := State{
		StateKeyExecContext: &ExecutionContext{
			InvocationID: "fallback-invocation",
			Invocation:   inv,
			EventChan:    eventCh,
		},
		StateKeyCurrentNodeID: "step",
	}

	err := EmitCustomStateDelta(
		context.Background(),
		state,
		State{"result": true},
	)
	require.NoError(t, err)

	evt := requireEmitterEvent(t, eventCh)
	require.Equal(t, "fallback-invocation", evt.InvocationID)
	require.Equal(t, "req", evt.RequestID)
	require.Equal(t, "branch", evt.Branch)
	require.Equal(t, "filter", evt.FilterKey)
	var metadata NodeCustomEventMetadata
	require.NoError(t, json.Unmarshal(evt.StateDelta[MetadataKeyNodeCustom], &metadata))
	require.Equal(t, "fallback-invocation", metadata.InvocationID)
}

func TestEmitCustomStateDelta_NoExecutionContextIsNoop(t *testing.T) {
	err := EmitCustomStateDelta(
		context.Background(),
		State{},
		State{"result": true},
	)
	require.NoError(t, err)
}

func TestEmitCustomStateDelta_EmptyDeltaIsNoop(t *testing.T) {
	eventCh := make(chan *event.Event, 1)
	state := State{
		StateKeyExecContext: &ExecutionContext{
			InvocationID: "inv",
			EventChan:    eventCh,
		},
	}

	err := EmitCustomStateDelta(context.Background(), state, nil)
	require.NoError(t, err)
	require.Len(t, eventCh, 0)
}

func TestEmitCustomStateDelta_MarshalError(t *testing.T) {
	err := EmitCustomStateDelta(
		context.Background(),
		State{
			StateKeyExecContext: &ExecutionContext{
				InvocationID: "inv",
				EventChan:    make(chan *event.Event, 1),
			},
		},
		State{"bad": func() {}},
	)
	require.Error(t, err)
}

func TestMergeStateDeltaMaps_ClonesBytes(t *testing.T) {
	src := map[string][]byte{
		"result": []byte(`{"ok":true}`),
	}

	merged := mergeStateDeltaMaps(nil, src)
	require.NotNil(t, merged["result"])

	src["result"][0] = 'x'
	require.Equal(t, byte('{'), merged["result"][0])
}
