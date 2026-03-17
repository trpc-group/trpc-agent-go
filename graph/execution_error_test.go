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
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestExecutionErrorCollector_Recoverable(t *testing.T) {
	collector := NewExecutionErrorCollector(
		WithRecoverableExecutionErrors(func(err error) bool {
			return err != nil
		}),
	)

	result, err := collector.NodeCallbacks().RunAfterNode(
		context.Background(),
		&NodeCallbackContext{
			NodeID:   "step",
			NodeName: "Step",
			NodeType: NodeTypeFunction,
		},
		State{},
		nil,
		errors.New("boom"),
	)
	require.NoError(t, err)

	update, ok := result.(State)
	require.True(t, ok)
	executionErrors, ok := update[StateKeyExecutionErrors].([]ExecutionError)
	require.True(t, ok)
	require.Len(t, executionErrors, 1)
	require.Equal(
		t,
		ExecutionErrorSeverityRecoverable,
		executionErrors[0].Severity,
	)
	require.NotNil(t, executionErrors[0].Error)
	require.Equal(t, "boom", executionErrors[0].Error.Message)
}

func TestExecutionErrorCollector_RecoveryCommandMergesUpdate(t *testing.T) {
	collector := NewExecutionErrorCollector(
		WithExecutionErrorPolicy(func(
			ctx context.Context,
			callbackCtx *NodeCallbackContext,
			state State,
			err error,
		) ExecutionErrorPolicy {
			return ExecutionErrorPolicy{
				Recover: true,
				Replacement: &Command{
					GoTo: "next",
					Update: State{
						"done": true,
					},
				},
			}
		}),
	)

	result, err := collector.NodeCallbacks().RunAfterNode(
		context.Background(),
		&NodeCallbackContext{NodeID: "step"},
		State{},
		nil,
		errors.New("boom"),
	)
	require.NoError(t, err)

	command, ok := result.(*Command)
	require.True(t, ok)
	require.Equal(t, "next", command.GoTo)
	require.Equal(t, true, command.Update["done"])

	executionErrors, ok := command.Update[StateKeyExecutionErrors].([]ExecutionError)
	require.True(t, ok)
	require.Len(t, executionErrors, 1)
}

func TestExecutionErrorCollector_FatalEmitsFallbackState(t *testing.T) {
	collector := NewExecutionErrorCollector()
	eventCh := make(chan *event.Event, 1)
	state := State{
		StateKeyExecContext: &ExecutionContext{
			InvocationID: "inv",
			EventChan:    eventCh,
		},
		StateKeyCurrentNodeID: "fatal-node",
	}

	result, err := collector.NodeCallbacks().RunAfterNode(
		context.Background(),
		&NodeCallbackContext{NodeID: "fatal-node"},
		state,
		nil,
		errors.New("fatal boom"),
	)
	require.NoError(t, err)
	require.Nil(t, result)

	evt := <-eventCh
	require.NotNil(t, evt)
	require.Contains(t, evt.StateDelta, StateKeyExecutionErrors)

	executionErrors, err := ExecutionErrorsFromStateDelta(
		evt.StateDelta,
		StateKeyExecutionErrors,
	)
	require.NoError(t, err)
	require.Len(t, executionErrors, 1)
	require.Equal(
		t,
		ExecutionErrorSeverityFatal,
		executionErrors[0].Severity,
	)
}

func TestExecutionErrorCollector_SubgraphStateUpdate(t *testing.T) {
	collector := NewExecutionErrorCollector()
	raw, err := json.Marshal([]ExecutionError{{
		Severity: ExecutionErrorSeverityFatal,
		Error: &model.ResponseError{
			Type:    model.ErrorTypeFlowError,
			Message: "child boom",
		},
	}})
	require.NoError(t, err)

	update := collector.SubgraphStateUpdate(SubgraphResult{
		RawStateDelta: map[string][]byte{
			StateKeyExecutionErrors: raw,
		},
	})
	require.NotNil(t, update)
	executionErrors, ok := update[StateKeyExecutionErrors].([]ExecutionError)
	require.True(t, ok)
	require.Len(t, executionErrors, 1)
	require.Equal(t, "child boom", executionErrors[0].Error.Message)
}
