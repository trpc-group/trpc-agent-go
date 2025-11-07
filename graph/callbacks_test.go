//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
)

func TestNodeCallbacks_BasicFunctionality(t *testing.T) {
	callbacks := NewNodeCallbacks()
	assert.NotNil(t, callbacks)
	assert.Empty(t, callbacks.BeforeNode)
	assert.Empty(t, callbacks.AfterNode)
	assert.Empty(t, callbacks.OnNodeError)
}

func TestNodeCallbacks_RegisterCallbacks(t *testing.T) {
	callbacks := NewNodeCallbacks()

	beforeCallback := func(ctx context.Context, callbackCtx *NodeCallbackContext, state State) (any, error) {
		return nil, nil
	}

	afterCallback := func(ctx context.Context, callbackCtx *NodeCallbackContext, state State, result any, nodeErr error) (any, error) {
		return nil, nil
	}

	errorCallback := func(ctx context.Context, callbackCtx *NodeCallbackContext, state State, err error) {
	}

	callbacks.RegisterBeforeNode(beforeCallback).
		RegisterAfterNode(afterCallback).
		RegisterOnNodeError(errorCallback)

	assert.Len(t, callbacks.BeforeNode, 1)
	assert.Len(t, callbacks.AfterNode, 1)
	assert.Len(t, callbacks.OnNodeError, 1)
}

func TestNodeCallbacks_RunBeforeNode(t *testing.T) {
	callbacks := NewNodeCallbacks()

	executionOrder := make([]string, 0)
	expectedOrder := []string{"before1", "before2"}

	before1 := func(ctx context.Context, callbackCtx *NodeCallbackContext, state State) (any, error) {
		executionOrder = append(executionOrder, "before1")
		return nil, nil
	}

	before2 := func(ctx context.Context, callbackCtx *NodeCallbackContext, state State) (any, error) {
		executionOrder = append(executionOrder, "before2")
		return nil, nil
	}

	callbacks.RegisterBeforeNode(before1).RegisterBeforeNode(before2)

	callbackCtx := &NodeCallbackContext{
		NodeID:   "test-node",
		NodeName: "Test Node",
		NodeType: NodeTypeFunction,
	}

	state := State{"key": "value"}

	result, err := callbacks.RunBeforeNode(context.Background(), callbackCtx, state)
	assert.NoError(t, err)
	assert.Nil(t, result)
	assert.Equal(t, expectedOrder, executionOrder)
}

func TestNodeCallbacks_RunBeforeNode_WithCustomResult(t *testing.T) {
	callbacks := NewNodeCallbacks()

	executionOrder := make([]string, 0)

	before1 := func(ctx context.Context, callbackCtx *NodeCallbackContext, state State) (any, error) {
		executionOrder = append(executionOrder, "before1")
		return nil, nil
	}

	before2 := func(ctx context.Context, callbackCtx *NodeCallbackContext, state State) (any, error) {
		executionOrder = append(executionOrder, "before2")
		return "custom-result", nil
	}

	before3 := func(ctx context.Context, callbackCtx *NodeCallbackContext, state State) (any, error) {
		executionOrder = append(executionOrder, "before3")
		return nil, nil
	}

	callbacks.RegisterBeforeNode(before1).
		RegisterBeforeNode(before2).
		RegisterBeforeNode(before3)

	callbackCtx := &NodeCallbackContext{
		NodeID:   "test-node",
		NodeName: "Test Node",
		NodeType: NodeTypeFunction,
	}

	state := State{"key": "value"}

	result, err := callbacks.RunBeforeNode(context.Background(), callbackCtx, state)
	assert.NoError(t, err)
	assert.Equal(t, "custom-result", result)
	assert.Equal(t, []string{"before1", "before2"}, executionOrder)
	// before3 should not be called because before2 returned a custom result
}

func TestNodeCallbacks_RunBeforeNode_WithError(t *testing.T) {
	callbacks := NewNodeCallbacks()

	executionOrder := make([]string, 0)
	expectedError := errors.New("callback error")

	before1 := func(ctx context.Context, callbackCtx *NodeCallbackContext, state State) (any, error) {
		executionOrder = append(executionOrder, "before1")
		return nil, nil
	}

	before2 := func(ctx context.Context, callbackCtx *NodeCallbackContext, state State) (any, error) {
		executionOrder = append(executionOrder, "before2")
		return nil, expectedError
	}

	before3 := func(ctx context.Context, callbackCtx *NodeCallbackContext, state State) (any, error) {
		executionOrder = append(executionOrder, "before3")
		return nil, nil
	}

	callbacks.RegisterBeforeNode(before1).
		RegisterBeforeNode(before2).
		RegisterBeforeNode(before3)

	callbackCtx := &NodeCallbackContext{
		NodeID:   "test-node",
		NodeName: "Test Node",
		NodeType: NodeTypeFunction,
	}

	state := State{"key": "value"}

	result, err := callbacks.RunBeforeNode(context.Background(), callbackCtx, state)
	assert.Error(t, err)
	assert.Equal(t, expectedError, err)
	assert.Nil(t, result)
	assert.Equal(t, []string{"before1", "before2"}, executionOrder)
	// before3 should not be called because before2 returned an error
}

func TestNodeCallbacks_RunAfterNode(t *testing.T) {
	callbacks := NewNodeCallbacks()

	executionOrder := make([]string, 0)
	expectedOrder := []string{"after1", "after2"}

	after1 := func(ctx context.Context, callbackCtx *NodeCallbackContext, state State, result any, nodeErr error) (any, error) {
		executionOrder = append(executionOrder, "after1")
		return result, nil
	}

	after2 := func(ctx context.Context, callbackCtx *NodeCallbackContext, state State, result any, nodeErr error) (any, error) {
		executionOrder = append(executionOrder, "after2")
		return result, nil
	}

	callbacks.RegisterAfterNode(after1).RegisterAfterNode(after2)

	callbackCtx := &NodeCallbackContext{
		NodeID:   "test-node",
		NodeName: "Test Node",
		NodeType: NodeTypeFunction,
	}

	state := State{"key": "value"}
	originalResult := "original-result"

	result, err := callbacks.RunAfterNode(context.Background(), callbackCtx, state, originalResult, nil)
	assert.NoError(t, err)
	assert.Equal(t, originalResult, result)
	assert.Equal(t, expectedOrder, executionOrder)
}

func TestNodeCallbacks_RunAfterNode_WithCustomResult(t *testing.T) {
	callbacks := NewNodeCallbacks()

	executionOrder := make([]string, 0)

	after1 := func(ctx context.Context, callbackCtx *NodeCallbackContext, state State, result any, nodeErr error) (any, error) {
		executionOrder = append(executionOrder, "after1")
		return nil, nil
	}

	after2 := func(ctx context.Context, callbackCtx *NodeCallbackContext, state State, result any, nodeErr error) (any, error) {
		executionOrder = append(executionOrder, "after2")
		return "custom-result", nil
	}

	after3 := func(ctx context.Context, callbackCtx *NodeCallbackContext, state State, result any, nodeErr error) (any, error) {
		executionOrder = append(executionOrder, "after3")
		return nil, nil
	}

	callbacks.RegisterAfterNode(after1).
		RegisterAfterNode(after2).
		RegisterAfterNode(after3)

	callbackCtx := &NodeCallbackContext{
		NodeID:   "test-node",
		NodeName: "Test Node",
		NodeType: NodeTypeFunction,
	}

	state := State{"key": "value"}
	originalResult := "original-result"

	result, err := callbacks.RunAfterNode(context.Background(), callbackCtx, state, originalResult, nil)
	assert.NoError(t, err)
	assert.Equal(t, "custom-result", result)
	assert.Equal(t, []string{"after1", "after2", "after3"}, executionOrder)
	// All callbacks should be called, but the final result should be from after2
}

func TestNodeCallbacks_RunOnNodeError(t *testing.T) {
	callbacks := NewNodeCallbacks()

	executionOrder := make([]string, 0)
	expectedOrder := []string{"error1", "error2"}
	expectedError := errors.New("node execution error")

	error1 := func(ctx context.Context, callbackCtx *NodeCallbackContext, state State, err error) {
		executionOrder = append(executionOrder, "error1")
		assert.Equal(t, expectedError, err)
	}

	error2 := func(ctx context.Context, callbackCtx *NodeCallbackContext, state State, err error) {
		executionOrder = append(executionOrder, "error2")
		assert.Equal(t, expectedError, err)
	}

	callbacks.RegisterOnNodeError(error1).RegisterOnNodeError(error2)

	callbackCtx := &NodeCallbackContext{
		NodeID:   "test-node",
		NodeName: "Test Node",
		NodeType: NodeTypeFunction,
	}

	state := State{"key": "value"}

	callbacks.RunOnNodeError(context.Background(), callbackCtx, state, expectedError)
	assert.Equal(t, expectedOrder, executionOrder)
}

// Defensive behavior: nil callbacks should be skipped without panic.
func TestNodeCallbacks_SkipNilEntries(t *testing.T) {
	c := NewNodeCallbacks()
	// Manually inject nil callbacks to simulate corrupted state.
	c.BeforeNode = append(c.BeforeNode, nil)
	c.AfterNode = append(c.AfterNode, nil)
	c.OnNodeError = append(c.OnNodeError, nil)

	ctx := context.Background()
	cbCtx := &NodeCallbackContext{NodeID: "n"}
	st := State{}

	// Expect no panic and nil result / no error for before.
	res, err := c.RunBeforeNode(ctx, cbCtx, st)
	require.NoError(t, err)
	require.Nil(t, res)

	// Expect no panic and passthrough for after.
	out, err := c.RunAfterNode(ctx, cbCtx, st, "x", nil)
	require.NoError(t, err)
	require.Equal(t, "x", out)

	// Expect no panic for error callbacks.
	c.RunOnNodeError(ctx, cbCtx, st, errors.New("boom"))
}

func TestNodeCallbackContext_Complete(t *testing.T) {
	now := time.Now()
	callbackCtx := &NodeCallbackContext{
		NodeID:             "test-node",
		NodeName:           "Test Node",
		NodeType:           NodeTypeLLM,
		StepNumber:         5,
		ExecutionStartTime: now,
		InvocationID:       "inv-123",
		SessionID:          "session-456",
	}

	assert.Equal(t, "test-node", callbackCtx.NodeID)
	assert.Equal(t, "Test Node", callbackCtx.NodeName)
	assert.Equal(t, NodeTypeLLM, callbackCtx.NodeType)
	assert.Equal(t, 5, callbackCtx.StepNumber)
	assert.Equal(t, now, callbackCtx.ExecutionStartTime)
	assert.Equal(t, "inv-123", callbackCtx.InvocationID)
	assert.Equal(t, "session-456", callbackCtx.SessionID)
}

func TestStateGraph_WithNodeCallbacks(t *testing.T) {
	schema := NewStateSchema()
	graph := NewStateGraph(schema)

	callbacks := NewNodeCallbacks()
	beforeCalled := false

	beforeCallback := func(ctx context.Context, callbackCtx *NodeCallbackContext, state State) (any, error) {
		beforeCalled = true
		assert.Equal(t, "test-node", callbackCtx.NodeID)
		assert.Equal(t, NodeTypeFunction, callbackCtx.NodeType)
		return nil, nil
	}

	callbacks.RegisterBeforeNode(beforeCallback)

	// Add node callbacks to the graph
	graph.WithNodeCallbacks(callbacks)

	// Add a test node
	testNodeFunc := func(ctx context.Context, state State) (any, error) {
		return State{"result": "success"}, nil
	}

	graph.AddNode("test-node", testNodeFunc).
		SetEntryPoint("test-node").
		SetFinishPoint("test-node")

	// Compile the graph
	compiledGraph, err := graph.Compile()
	require.NoError(t, err)

	// Create executor
	executor, err := NewExecutor(compiledGraph)
	require.NoError(t, err)

	// Execute the graph
	initialState := State{"input": "test"}
	invocation := &agent.Invocation{
		InvocationID: "test-invocation",
	}
	eventChan, err := executor.Execute(context.Background(), initialState, invocation)
	require.NoError(t, err)

	// Consume events to verify execution completed
	for evt := range eventChan {
		if evt.Object == ObjectTypeGraphExecution {
			break
		}
	}

	// Verify callback was called
	assert.True(t, beforeCalled)
}

// Additional after-node callback coverage: error short-circuit and custom result propagation.
func TestRunAfterNode_ErrorAndCustomResult(t *testing.T) {
	cbs := NewNodeCallbacks()
	ctx := context.Background()
	cbCtx := &NodeCallbackContext{NodeID: "n"}
	st := State{}

	// First callback yields custom result
	cbs.RegisterAfterNode(func(ctx context.Context, c *NodeCallbackContext, s State, result any, nodeErr error) (any, error) {
		return State{"a": 1}, nil
	})
	// Second callback returns error; should short-circuit and return error
	cbs.RegisterAfterNode(func(ctx context.Context, c *NodeCallbackContext, s State, result any, nodeErr error) (any, error) {
		return nil, errors.New("boom")
	})

	_, err := cbs.RunAfterNode(ctx, cbCtx, st, State{"orig": true}, nil)
	if err == nil {
		t.Fatalf("expected error from second callback")
	}

	// Now test no error path: single after callback transforming the result
	cbs2 := NewNodeCallbacks()
	cbs2.RegisterAfterNode(func(ctx context.Context, c *NodeCallbackContext, s State, result any, nodeErr error) (any, error) {
		m, _ := result.(State)
		m["z"] = 9
		return m, nil
	})
	out, err := cbs2.RunAfterNode(ctx, cbCtx, st, State{"k": 7}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m, ok := out.(State); !ok || m["z"].(int) != 9 {
		t.Fatalf("unexpected result: %#v", out)
	}
}
