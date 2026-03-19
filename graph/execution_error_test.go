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
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type recoverableTestError struct {
	message     string
	recoverable bool
}

func (e recoverableTestError) Error() string {
	return e.message
}

func (e recoverableTestError) Recoverable() bool {
	return e.recoverable
}

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

func TestExecutionErrorCollector_DefaultRecoverablePolicy(
	t *testing.T,
) {
	collector := NewExecutionErrorCollector()

	result, err := collector.NodeCallbacks().RunAfterNode(
		context.Background(),
		&NodeCallbackContext{NodeID: "step"},
		State{},
		nil,
		recoverableTestError{
			message:     "recoverable boom",
			recoverable: true,
		},
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
}

func TestExecutionErrorCollector_DefaultRecoverableWrapper(
	t *testing.T,
) {
	collector := NewExecutionErrorCollector()

	result, err := collector.NodeCallbacks().RunAfterNode(
		context.Background(),
		&NodeCallbackContext{NodeID: "step"},
		State{},
		nil,
		MarkRecoverable(errors.New("wrapped boom")),
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
	require.Equal(t, "wrapped boom", executionErrors[0].Error.Message)
}

func TestWithRecoverableExecutionErrors_ExtendsDefaultPolicy(
	t *testing.T,
) {
	collector := NewExecutionErrorCollector(
		WithRecoverableExecutionErrors(func(error) bool {
			return false
		}),
	)

	result, err := collector.afterNode(
		context.Background(),
		&NodeCallbackContext{NodeID: "step"},
		State{},
		nil,
		recoverableTestError{
			message:     "recoverable boom",
			recoverable: true,
		},
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
}

func TestExecutionErrorCollector_RecoveryKeepsOriginalStateResult(
	t *testing.T,
) {
	collector := NewExecutionErrorCollector()

	result, err := collector.afterNode(
		context.Background(),
		&NodeCallbackContext{NodeID: "step"},
		State{},
		State{"partial": true},
		recoverableTestError{
			message:     "recoverable boom",
			recoverable: true,
		},
	)
	require.NoError(t, err)

	update, ok := result.(State)
	require.True(t, ok)
	require.Equal(t, true, update["partial"])

	executionErrors, ok := update[StateKeyExecutionErrors].([]ExecutionError)
	require.True(t, ok)
	require.Len(t, executionErrors, 1)
}

func TestExecutionErrorCollector_RecoveryKeepsOriginalCommandResult(
	t *testing.T,
) {
	collector := NewExecutionErrorCollector()

	result, err := collector.afterNode(
		context.Background(),
		&NodeCallbackContext{NodeID: "step"},
		State{},
		&Command{
			GoTo: "next",
			Update: State{
				"partial": true,
			},
		},
		recoverableTestError{
			message:     "recoverable boom",
			recoverable: true,
		},
	)
	require.NoError(t, err)

	command, ok := result.(*Command)
	require.True(t, ok)
	require.Equal(t, "next", command.GoTo)
	require.Equal(t, true, command.Update["partial"])

	executionErrorsValue := command.Update[StateKeyExecutionErrors]
	executionErrors, ok := executionErrorsValue.([]ExecutionError)
	require.True(t, ok)
	require.Len(t, executionErrors, 1)
}

func TestExecutionErrorCollector_RecoveryCommandMergesUpdate(t *testing.T) {
	collector := NewExecutionErrorCollector(
		WithExecutionErrorPolicy(func(
			ctx context.Context,
			callbackCtx *NodeCallbackContext,
			state State,
			result any,
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

	executionErrorsValue := command.Update[StateKeyExecutionErrors]
	executionErrors, ok := executionErrorsValue.([]ExecutionError)
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

func TestExecutionErrorCollector_ConfigHelpers(t *testing.T) {
	const eventType = "biz_error"
	const stateKey = "biz_errors"

	collector := NewExecutionErrorCollector(
		WithExecutionErrorStateKey(stateKey),
		WithExecutionErrorEventType(eventType),
	)

	require.Equal(t, stateKey, collector.StateKey())
	require.Equal(t, eventType, collector.eventType)

	field := collector.StateField()
	require.Equal(t, reflect.TypeOf([]ExecutionError{}), field.Type)

	defaultValue, ok := field.Default().([]ExecutionError)
	require.True(t, ok)
	require.Empty(t, defaultValue)

	schema := collector.AddField(nil)
	require.NotNil(t, schema)

	added, ok := schema.Fields[stateKey]
	require.True(t, ok)
	require.Equal(t, field.Type, added.Type)
	require.NotNil(t, added.Reducer)

	raw, err := json.Marshal([]ExecutionError{{
		Severity: ExecutionErrorSeverityFatal,
	}})
	require.NoError(t, err)

	mapper := collector.SubgraphOutputMapper()
	update := mapper(nil, SubgraphResult{
		RawStateDelta: map[string][]byte{
			stateKey: raw,
		},
	})
	require.NotNil(t, update)

	executionErrors, ok := update[stateKey].([]ExecutionError)
	require.True(t, ok)
	require.Len(t, executionErrors, 1)
}

func TestExecutionErrorCollector_EmptyOptionsKeepDefaults(t *testing.T) {
	collector := NewExecutionErrorCollector(
		WithExecutionErrorStateKey(""),
		WithExecutionErrorEventType(""),
		WithRecoverableExecutionErrors(nil),
	)

	require.Equal(t, StateKeyExecutionErrors, collector.StateKey())
	require.Equal(t, ExecutionErrorEventType, collector.eventType)

	policy := collector.policy(
		context.Background(),
		nil,
		nil,
		nil,
		errors.New("boom"),
	)
	require.False(t, policy.Recover)
}

func TestNewExecutionError_NilCallbackContext(t *testing.T) {
	record := NewExecutionError(
		nil,
		errors.New("boom"),
		ExecutionErrorSeverityFatal,
	)

	require.Equal(t, ExecutionErrorSeverityFatal, record.Severity)
	require.NotNil(t, record.Error)
	require.Equal(t, "boom", record.Error.Message)
	require.Empty(t, record.NodeID)
	require.Empty(t, record.NodeName)
}

func TestExecutionErrorHelpers_EdgeCases(t *testing.T) {
	update := []ExecutionError{{
		Severity: ExecutionErrorSeverityRecoverable,
	}}

	reduced, ok := ExecutionErrorSliceReducer(
		nil,
		update,
	).([]ExecutionError)
	require.True(t, ok)
	require.Len(t, reduced, 1)

	update[0].Severity = ExecutionErrorSeverityFatal
	require.Equal(
		t,
		ExecutionErrorSeverityRecoverable,
		reduced[0].Severity,
	)

	require.Equal(
		t,
		"update",
		ExecutionErrorSliceReducer("existing", "update"),
	)

	decoded, err := DecodeExecutionErrors(nil)
	require.NoError(t, err)
	require.Nil(t, decoded)

	decoded, err = DecodeExecutionErrors([]byte("{"))
	require.Error(t, err)
	require.Nil(t, decoded)

	decoded, err = ExecutionErrorsFromStateDelta(
		nil,
		StateKeyExecutionErrors,
	)
	require.NoError(t, err)
	require.Nil(t, decoded)

	decoded, err = ExecutionErrorsFromStateDelta(
		map[string][]byte{"other": []byte("[]")},
		StateKeyExecutionErrors,
	)
	require.NoError(t, err)
	require.Nil(t, decoded)

	require.Nil(t, cloneExecutionErrors(nil))
}

func TestExecutionErrorCollector_CustomResponseError(t *testing.T) {
	const codeValue = "BIZ_001"
	const overrideMessage = "override"

	code := codeValue
	collector := NewExecutionErrorCollector(
		WithExecutionErrorPolicy(func(
			ctx context.Context,
			callbackCtx *NodeCallbackContext,
			state State,
			result any,
			err error,
		) ExecutionErrorPolicy {
			return ExecutionErrorPolicy{
				Recover: true,
				ResponseError: &model.ResponseError{
					Type:    model.ErrorTypeFlowError,
					Message: overrideMessage,
					Code:    &code,
				},
			}
		}),
	)

	result, err := collector.afterNode(
		context.Background(),
		&NodeCallbackContext{NodeID: "step"},
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

	code = "changed"
	require.Equal(
		t,
		overrideMessage,
		executionErrors[0].Error.Message,
	)
	require.NotNil(t, executionErrors[0].Error.Code)
	require.Equal(t, codeValue, *executionErrors[0].Error.Code)
}

func TestExecutionErrorCollector_PolicyReceivesResult(
	t *testing.T,
) {
	const stateKey = "partial"

	var seenResult any
	collector := NewExecutionErrorCollector(
		WithExecutionErrorPolicy(func(
			ctx context.Context,
			callbackCtx *NodeCallbackContext,
			state State,
			result any,
			err error,
		) ExecutionErrorPolicy {
			seenResult = result
			return ExecutionErrorPolicy{
				Recover: true,
			}
		}),
	)

	original := State{stateKey: true}
	result, err := collector.afterNode(
		context.Background(),
		&NodeCallbackContext{NodeID: "step"},
		State{},
		original,
		errors.New("boom"),
	)
	require.NoError(t, err)
	require.Equal(t, original, seenResult)

	update, ok := result.(State)
	require.True(t, ok)
	require.Equal(t, true, update[stateKey])
}

func TestExecutionErrorCollector_FatalEmitErrorIsIgnored(t *testing.T) {
	collector := NewExecutionErrorCollector()
	eventCh := make(chan *event.Event)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	state := State{
		StateKeyExecContext: &ExecutionContext{
			InvocationID: "inv",
			EventChan:    eventCh,
		},
		StateKeyCurrentNodeID: "fatal-node",
	}

	result, err := collector.afterNode(
		ctx,
		&NodeCallbackContext{NodeID: "fatal-node"},
		state,
		nil,
		errors.New("fatal boom"),
	)
	require.NoError(t, err)
	require.Nil(t, result)

	select {
	case evt := <-eventCh:
		t.Fatalf("unexpected event emitted: %+v", evt)
	default:
	}
}

func TestExecutionErrorCollector_SubgraphStateUpdate_InvalidData(
	t *testing.T,
) {
	collector := NewExecutionErrorCollector()

	update := collector.SubgraphStateUpdate(SubgraphResult{
		RawStateDelta: map[string][]byte{
			StateKeyExecutionErrors: []byte("{"),
		},
	})
	require.Nil(t, update)
}

func TestMergeExecutionErrorReplacement(t *testing.T) {
	update := State{
		StateKeyExecutionErrors: []ExecutionError{{
			Severity: ExecutionErrorSeverityFatal,
		}},
	}

	require.Equal(t, update, mergeExecutionErrorReplacement(nil, update))

	stateResult, ok := mergeExecutionErrorReplacement(
		State{"done": true},
		update,
	).(State)
	require.True(t, ok)
	require.Equal(t, true, stateResult["done"])
	_, ok = stateResult[StateKeyExecutionErrors]
	require.True(t, ok)

	commandValue, ok := mergeExecutionErrorReplacement(
		Command{
			GoTo: "next",
			Update: State{
				"done": true,
			},
		},
		update,
	).(Command)
	require.True(t, ok)
	require.Equal(t, "next", commandValue.GoTo)
	require.Equal(t, true, commandValue.Update["done"])
	_, ok = commandValue.Update[StateKeyExecutionErrors]
	require.True(t, ok)

	command, ok := mergeExecutionErrorReplacement(
		(*Command)(nil),
		update,
	).(*Command)
	require.True(t, ok)
	require.Equal(t, update, command.Update)

	command, ok = mergeExecutionErrorReplacement(
		&Command{
			GoTo: "next",
			Update: State{
				"done": true,
			},
		},
		update,
	).(*Command)
	require.True(t, ok)
	require.Equal(t, "next", command.GoTo)
	require.Equal(t, true, command.Update["done"])
	_, ok = command.Update[StateKeyExecutionErrors]
	require.True(t, ok)

	require.Equal(
		t,
		"raw",
		mergeExecutionErrorReplacement("raw", update),
	)

	merged := mergeStateForExecutionError(nil, update)
	_, ok = merged[StateKeyExecutionErrors]
	require.True(t, ok)
}

func TestCloneResponseError_ClonesOptionalFields(t *testing.T) {
	const codeValue = "BIZ_001"
	const paramValue = "field"

	code := codeValue
	param := paramValue
	cloned := cloneResponseError(&model.ResponseError{
		Type:    model.ErrorTypeFlowError,
		Message: "boom",
		Code:    &code,
		Param:   &param,
	})
	require.NotNil(t, cloned)

	code = "changed-code"
	param = "changed-param"

	require.NotNil(t, cloned.Code)
	require.Equal(t, codeValue, *cloned.Code)
	require.NotNil(t, cloned.Param)
	require.Equal(t, paramValue, *cloned.Param)
	require.Nil(t, cloneResponseError(nil))
}

func TestExecutionErrorMessage_EmptyWithoutResponseError(t *testing.T) {
	require.Empty(t, executionErrorMessage(ExecutionError{}))
}
