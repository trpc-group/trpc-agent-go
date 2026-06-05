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
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
)

// RuntimeContext carries parent graph runtime data for one AgentTool call.
type RuntimeContext struct {
	ParentInvocation *agent.Invocation
	State            map[string]any
	ParentNodeID     string
	ToolCallID       string
	ToolCallKey      string
}

// RuntimeCallable is implemented by internal tools that consume AgentTool graph runtime.
type RuntimeCallable interface {
	CallWithAgentToolGraphRuntime(
		ctx context.Context,
		jsonArgs []byte,
		runtime RuntimeContext,
	) (any, error)
}

// InterruptMetadata carries child graph checkpoint data for one interrupted AgentTool call.
type InterruptMetadata struct {
	ParentNodeID      string
	ChildAgentName    string
	ChildCheckpointID string
	ChildCheckpointNS string
	ChildLineageID    string
	ChildTaskID       string
	ToolCallID        string
	ToolCallKey       string
}

type interruptError struct {
	err      error
	metadata InterruptMetadata
}

// NewInterruptError creates an interrupt error with child checkpoint data.
func NewInterruptError(err error, metadata InterruptMetadata) error {
	if err := validateInterruptMetadata(metadata); err != nil {
		return err
	}
	if err == nil {
		err = errors.New("agent tool graph interrupted")
	}
	return &interruptError{
		err:      err,
		metadata: metadata,
	}
}

// InterruptMetadataFromError returns child checkpoint data from an interrupt error.
func InterruptMetadataFromError(err error) (InterruptMetadata, bool) {
	var interruptErr *interruptError
	if !errors.As(err, &interruptErr) {
		return InterruptMetadata{}, false
	}
	if validateInterruptMetadata(interruptErr.metadata) != nil {
		return InterruptMetadata{}, false
	}
	return interruptErr.metadata, true
}

func validateInterruptMetadata(metadata InterruptMetadata) error {
	switch {
	case metadata.ParentNodeID == "":
		return fmt.Errorf("agent tool graph interrupt missing parent node id")
	case metadata.ChildAgentName == "":
		return fmt.Errorf("agent tool graph interrupt missing child agent name")
	case metadata.ChildCheckpointID == "":
		return fmt.Errorf("agent tool graph interrupt missing child checkpoint id")
	case metadata.ChildCheckpointNS == "":
		return fmt.Errorf("agent tool graph interrupt missing child checkpoint namespace")
	case metadata.ChildLineageID == "":
		return fmt.Errorf("agent tool graph interrupt missing child lineage id")
	case metadata.ChildTaskID == "":
		return fmt.Errorf("agent tool graph interrupt missing child task id")
	case metadata.ToolCallID == "":
		return fmt.Errorf("agent tool graph interrupt missing tool call id")
	case metadata.ToolCallKey == "":
		return fmt.Errorf("agent tool graph interrupt missing tool call key")
	default:
		return nil
	}
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
