//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package agenttoolgraph carries internal bridge data between Graph ToolsNode
// execution and AgentTool-wrapped GraphAgent runs.
package agenttoolgraph

import (
	"context"
	"errors"

	"trpc.group/trpc-go/trpc-agent-go/agent"
)

// RuntimeContext carries parent graph runtime data for one AgentTool call.
type RuntimeContext struct {
	ParentInvocation *agent.Invocation
	State            map[string]any
	ParentNodeID     string
	ToolCallID       string
}

// RuntimeCallable is implemented by internal tools that consume AgentTool graph runtime.
type RuntimeCallable interface {
	CallWithAgentToolGraphRuntime(
		ctx context.Context,
		jsonArgs []byte,
		runtime RuntimeContext,
	) (any, error)
}

type interruptMetadata struct {
	parentNodeID      string
	childAgentName    string
	childCheckpointID string
	childCheckpointNS string
	childLineageID    string
	childTaskID       string
	toolCallID        string
}

type interruptError struct {
	err      error
	metadata interruptMetadata
}

// NewInterruptError creates an interrupt error with child checkpoint data.
func NewInterruptError(
	err error,
	parentNodeID string,
	childAgentName string,
	childCheckpointID string,
	childCheckpointNS string,
	childLineageID string,
	childTaskID string,
	toolCallID string,
) error {
	if err == nil {
		err = errors.New("agent tool graph interrupted")
	}
	return &interruptError{
		err: err,
		metadata: interruptMetadata{
			parentNodeID:      parentNodeID,
			childAgentName:    childAgentName,
			childCheckpointID: childCheckpointID,
			childCheckpointNS: childCheckpointNS,
			childLineageID:    childLineageID,
			childTaskID:       childTaskID,
			toolCallID:        toolCallID,
		},
	}
}

// InterruptMetadataFromError returns child checkpoint data from an interrupt error.
func InterruptMetadataFromError(
	err error,
) (
	parentNodeID string,
	childAgentName string,
	childCheckpointID string,
	childCheckpointNS string,
	childLineageID string,
	childTaskID string,
	toolCallID string,
	ok bool,
) {
	var interruptErr *interruptError
	if !errors.As(err, &interruptErr) {
		return "", "", "", "", "", "", "", false
	}
	meta := interruptErr.metadata
	return meta.parentNodeID,
		meta.childAgentName,
		meta.childCheckpointID,
		meta.childCheckpointNS,
		meta.childLineageID,
		meta.childTaskID,
		meta.toolCallID,
		true
}

// Error returns the wrapped graph interrupt error message.
func (e *interruptError) Error() string {
	if e == nil || e.err == nil {
		return "agent tool graph interrupted"
	}
	return e.err.Error()
}

// Unwrap returns the wrapped graph interrupt.
func (e *interruptError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}
