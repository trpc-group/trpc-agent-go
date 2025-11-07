//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package graph provides graph-based execution functionality.
package graph

import (
	"context"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
)

// StateKeyNodeCallbacks is the key for storing node callbacks in the state.
const StateKeyNodeCallbacks = "node_callbacks"

// NodeCallbackContext provides context information for node callbacks.
type NodeCallbackContext struct {
	// NodeID is the ID of the node being executed.
	NodeID string
	// NodeName is the name of the node being executed.
	NodeName string
	// NodeType is the type of the node being executed.
	NodeType NodeType
	// StepNumber is the current step number in the graph execution.
	StepNumber int
	// ExecutionStartTime is when the node execution started.
	ExecutionStartTime time.Time
	// InvocationID is the unique identifier for this graph execution.
	InvocationID string
	// SessionID is the session identifier if available.
	SessionID string
}

// BeforeNodeCallback is called before a node is executed.
// Returns (customResult, error).
// - customResult: if not nil, this result will be returned and node execution will be skipped.
// - error: if not nil, node execution will be stopped with this error.
type BeforeNodeCallback func(
	ctx context.Context,
	callbackCtx *NodeCallbackContext,
	state State,
) (any, error)

// AfterNodeCallback is called after a node is executed.
// Returns (customResult, error).
// - customResult: if not nil, this result will be used instead of the actual node result.
// - error: if not nil, this error will be returned.
type AfterNodeCallback func(
	ctx context.Context,
	callbackCtx *NodeCallbackContext,
	state State,
	result any,
	nodeErr error,
) (any, error)

// OnNodeErrorCallback is called when a node execution fails.
// This callback cannot change the error but can be used for logging, monitoring, etc.
type OnNodeErrorCallback func(
	ctx context.Context,
	callbackCtx *NodeCallbackContext,
	state State,
	err error,
)

// AgentEventCallback is called when an agent event is emitted.
type AgentEventCallback func(
	ctx context.Context,
	callbackCtx *NodeCallbackContext,
	state State,
	evt *event.Event,
)

// NodeCallbacks holds callbacks for node operations.
type NodeCallbacks struct {
	// BeforeNode is a list of callbacks that are called before the node is executed.
	BeforeNode []BeforeNodeCallback
	// AfterNode is a list of callbacks that are called after the node is executed.
	AfterNode []AfterNodeCallback
	// OnNodeError is a list of callbacks that are called when a node execution fails.
	OnNodeError []OnNodeErrorCallback
	// AgentEvent is a list of callbacks that are called when an agent event is emitted.
	AgentEvent []AgentEventCallback
}

// NewNodeCallbacks creates a new NodeCallbacks instance.
func NewNodeCallbacks() *NodeCallbacks {
	return &NodeCallbacks{}
}

// RegisterBeforeNode registers a before node callback.
func (c *NodeCallbacks) RegisterBeforeNode(cb BeforeNodeCallback) *NodeCallbacks {
	c.BeforeNode = append(c.BeforeNode, cb)
	return c
}

// RegisterAfterNode registers an after node callback.
func (c *NodeCallbacks) RegisterAfterNode(cb AfterNodeCallback) *NodeCallbacks {
	c.AfterNode = append(c.AfterNode, cb)
	return c
}

// RegisterOnNodeError registers an on node error callback.
func (c *NodeCallbacks) RegisterOnNodeError(cb OnNodeErrorCallback) *NodeCallbacks {
	c.OnNodeError = append(c.OnNodeError, cb)
	return c
}

// RunBeforeNode runs all before node callbacks in order.
// Returns (customResult, error).
// If any callback returns a custom result, stop and return.
func (c *NodeCallbacks) RunBeforeNode(
	ctx context.Context,
	callbackCtx *NodeCallbackContext,
	state State,
) (any, error) {
	for _, cb := range c.BeforeNode {
		if cb == nil {
			// Skip nil callback entries defensively.
			continue
		}
		customResult, err := cb(ctx, callbackCtx, state)
		if err != nil {
			return nil, err
		}
		if customResult != nil {
			return customResult, nil
		}
	}
	return nil, nil
}

// RunAfterNode runs all after node callbacks in order.
// Returns (customResult, error).
// If any callback returns a custom result, stop and return.
func (c *NodeCallbacks) RunAfterNode(
	ctx context.Context,
	callbackCtx *NodeCallbackContext,
	state State,
	result any,
	nodeErr error,
) (any, error) {
	currentResult := result
	for _, cb := range c.AfterNode {
		if cb == nil {
			// Skip nil callback entries defensively.
			continue
		}
		customResult, err := cb(ctx, callbackCtx, state, currentResult, nodeErr)
		if err != nil {
			return nil, err
		}
		if customResult != nil {
			currentResult = customResult
		}
	}
	return currentResult, nil
}

// RunOnNodeError runs all on node error callbacks in order.
// This method does not return any values as error callbacks are for side effects only.
func (c *NodeCallbacks) RunOnNodeError(
	ctx context.Context,
	callbackCtx *NodeCallbackContext,
	state State,
	err error,
) {
	for _, cb := range c.OnNodeError {
		if cb == nil {
			// Skip nil callback entries defensively.
			continue
		}
		cb(ctx, callbackCtx, state, err)
	}
}
