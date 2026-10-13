//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestStreamModeProgress(t *testing.T) {
	f := NewStreamModeFilter(true, []agent.StreamMode{agent.StreamModeProgress})
	for _, tc := range []struct {
		name, payload string
		allowed       bool
	}{
		{"unknown percent", `{"operation_id":"a","kind":"tool","status":"started"}`, true},
		{"known zero", `{"operation_id":"a","kind":"tool","status":"running","percent":0}`, true},
		{"future status", `{"operation_id":"a","kind":"future","status":"paused"}`, true},
		{"null", `null`, false},
		{"malformed", `{`, false},
		{"missing identity", `{"kind":"tool","status":"running"}`, false},
		{"missing kind", `{"operation_id":"a","status":"running"}`, false},
		{"missing status", `{"operation_id":"a","kind":"tool"}`, false},
		{"negative", `{"operation_id":"a","kind":"tool","status":"running","percent":-1}`, false},
		{"overflow", `{"operation_id":"a","kind":"tool","status":"running","percent":101}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, object := range []string{model.ObjectTypePreprocessingStatus, ObjectTypeGraphNodeCustom, "app.status"} {
				e := eventWithObject(object)
				e.Extensions = map[string]json.RawMessage{event.ProgressExtensionKey: json.RawMessage(tc.payload)}
				require.Equal(t, tc.allowed, f.Allows(e))
				// Progress selection does not depend on a model response envelope.
				e.Response = nil
				require.Equal(t, tc.allowed, f.Allows(e))
			}
		})
	}
	require.False(t, f.Allows(nil))
	require.False(t, f.Allows(eventWithObject(ObjectTypeGraphNodeCustom)))
	require.False(t, f.Allows(eventWithObject(model.ObjectTypePreprocessingStatus)))
	require.True(t, f.Allows(eventWithError()))
}

func TestEmitProgressOptInAndOperationIdentity(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		inv := agent.NewInvocation(agent.WithInvocationRunOptions(agent.RunOptions{ProgressEventsEnabled: enabled}))
		ch := make(chan *event.Event, 8)
		emitter := NewEventEmitter(ch, withEmitterInvocation(inv), WithEmitterNodeID("work"))
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = emitter.EmitProgress(100, "snapshot")
			}()
		}
		wg.Wait()
		var operationID string
		for i := 0; i < 8; i++ {
			e := <-ch
			require.Equal(t, ObjectTypeGraphNodeCustom, e.Object)
			var legacy NodeCustomEventMetadata
			require.NoError(t, json.Unmarshal(e.StateDelta[MetadataKeyNodeCustom], &legacy))
			require.Equal(t, 100.0, legacy.Progress)
			require.Equal(t, "snapshot", legacy.Message)
			p, ok, err := event.GetExtension[event.Progress](e, event.ProgressExtensionKey)
			require.NoError(t, err)
			require.Equal(t, enabled, ok)
			if enabled {
				require.NotEmpty(t, p.OperationID)
				if operationID == "" {
					operationID = p.OperationID
				}
				require.Equal(t, operationID, p.OperationID)
				require.Equal(t, "graph_node", p.Kind)
				require.Equal(t, event.ProgressStatusRunning, p.Status)
				require.NotNil(t, p.Percent)
				require.Equal(t, 100.0, *p.Percent)
			}
		}
		// A second operation in the same invocation gets a separate identity.
		second := NewEventEmitter(ch, withEmitterInvocation(inv), WithEmitterNodeID("work"))
		require.NoError(t, second.EmitProgress(0, "retry"))
		p, ok, err := event.GetExtension[event.Progress](<-ch, event.ProgressExtensionKey)
		require.NoError(t, err)
		if enabled {
			require.True(t, ok)
			require.NotEqual(t, operationID, p.OperationID)
			require.NotNil(t, p.Percent)
			require.Zero(t, *p.Percent)
		}
	}
}
