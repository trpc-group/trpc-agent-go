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

func TestExecutionErrorSliceReducer_ClonesInputs(t *testing.T) {
	existing := []ExecutionError{{
		Severity: ExecutionErrorSeverityRecoverable,
		Error: &model.ResponseError{
			Type:    model.ErrorTypeFlowError,
			Message: "first",
		},
	}}
	update := []ExecutionError{{
		Severity: ExecutionErrorSeverityFatal,
		Error: &model.ResponseError{
			Type:    model.ErrorTypeFlowError,
			Message: "second",
		},
	}}

	reduced, ok := ExecutionErrorSliceReducer(
		existing,
		update,
	).([]ExecutionError)
	require.True(t, ok)
	require.Len(t, reduced, 2)

	existing[0].Error.Message = "changed-first"
	update[0].Error.Message = "changed-second"

	require.Equal(t, "first", reduced[0].Error.Message)
	require.Equal(t, "second", reduced[1].Error.Message)
}

func TestDecodeExecutionErrors_ClonesDecodedValues(t *testing.T) {
	raw, err := json.Marshal([]ExecutionError{{
		Severity: ExecutionErrorSeverityFatal,
		Error: &model.ResponseError{
			Type:    model.ErrorTypeFlowError,
			Message: "boom",
		},
	}})
	require.NoError(t, err)

	first, err := DecodeExecutionErrors(raw)
	require.NoError(t, err)
	require.Len(t, first, 1)

	first[0].Error.Message = "mutated"

	second, err := DecodeExecutionErrors(raw)
	require.NoError(t, err)
	require.Len(t, second, 1)
	require.Equal(t, "boom", second[0].Error.Message)
}

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

func TestExecutionErrorCollector_IgnoresRecoveredNodeError(
	t *testing.T,
) {
	collector := NewExecutionErrorCollector()
	callbacks := NewNodeCallbacks().
		RegisterAfterNode(func(
			ctx context.Context,
			callbackCtx *NodeCallbackContext,
			state State,
			result any,
			nodeErr error,
		) (any, error) {
			require.Error(t, nodeErr)
			return State{"ok": true}, nil
		}).
		RegisterAfterNode(collector.afterNode)

	result, err := callbacks.RunAfterNode(
		context.Background(),
		&NodeCallbackContext{NodeID: "step"},
		State{},
		nil,
		errors.New("boom"),
	)
	require.NoError(t, err)

	update, ok := result.(State)
	require.True(t, ok)
	require.Equal(t, true, update["ok"])
	_, exists := update[StateKeyExecutionErrors]
	require.False(t, exists)
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

func TestExecutionErrorCollector_SubgraphStateUpdate_Fallback(
	t *testing.T,
) {
	collector := NewExecutionErrorCollector()
	raw, err := json.Marshal([]ExecutionError{{
		Severity: ExecutionErrorSeverityFatal,
		Error: &model.ResponseError{
			Type:    model.ErrorTypeFlowError,
			Message: "child fallback",
		},
	}})
	require.NoError(t, err)

	update := collector.SubgraphStateUpdate(SubgraphResult{
		FallbackStateDelta: map[string][]byte{
			StateKeyExecutionErrors: raw,
		},
	})
	require.NotNil(t, update)

	executionErrors, ok := update[StateKeyExecutionErrors].([]ExecutionError)
	require.True(t, ok)
	require.Len(t, executionErrors, 1)
	require.Equal(t, "child fallback", executionErrors[0].Error.Message)
}
