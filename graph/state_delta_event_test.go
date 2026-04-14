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
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
)

func TestEmitCustomStateDelta_EmitsEvent(t *testing.T) {
	eventCh := make(chan *event.Event, 1)
	state := State{
		StateKeyExecContext: &ExecutionContext{
			InvocationID: "inv",
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

	evt := <-eventCh
	require.NotNil(t, evt)
	require.Equal(t, ObjectTypeGraphNodeCustom, evt.Object)
	require.Contains(t, evt.StateDelta, "result")
	require.Contains(t, evt.StateDelta, MetadataKeyNodeCustom)
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
