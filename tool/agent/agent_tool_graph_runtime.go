//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	coreagent "trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/internal/agenttoolgraph"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type parentInvocationGraphRuntime struct {
	state        graph.State
	parentNodeID string
	toolCallID   string
	toolCallKey  string
	childKey     string
}

const graphRuntimeSuppressSessionEventsStateKey = "agenttool:graph_runtime_suppress_session_events"

// CallWithAgentToolGraphRuntime executes the tool with explicit parent graph runtime.
// It is called by graph ToolsNode through an internal interface.
func (at *Tool) CallWithAgentToolGraphRuntime(
	ctx context.Context,
	jsonArgs []byte,
	runtime agenttoolgraph.RuntimeContext,
) (any, error) {
	if at.dynamic {
		return at.callDynamic(ctx, jsonArgs)
	}
	graphRuntime, err := parentInvocationGraphRuntimeFromContext(runtime)
	if err != nil {
		return nil, err
	}
	message := model.NewUserMessage(string(jsonArgs))
	return at.callWithParentInvocation(ctx, runtime.ParentInvocation, message, graphRuntime)
}

func parentInvocationGraphRuntimeFromContext(
	runtime agenttoolgraph.RuntimeContext,
) (*parentInvocationGraphRuntime, error) {
	if runtime.ParentInvocation == nil {
		return nil, fmt.Errorf("agent tool graph parent invocation is nil")
	}
	if runtime.State == nil {
		return nil, fmt.Errorf("agent tool graph runtime state is nil")
	}
	if runtime.ParentNodeID == "" {
		return nil, fmt.Errorf("agent tool graph parent node id is empty")
	}
	if runtime.ToolCallKey == "" {
		return nil, fmt.Errorf("agent tool graph tool call key is empty")
	}
	return &parentInvocationGraphRuntime{
		state:        graph.State(runtime.State),
		parentNodeID: runtime.ParentNodeID,
		toolCallID:   runtime.ToolCallID,
		toolCallKey:  runtime.ToolCallKey,
		childKey:     runtime.ChildFilterKey,
	}, nil
}

type graphToolInterruptCapture struct {
	parentNodeID         string
	toolCallID           string
	toolCallKey          string
	childKey             string
	childAgentName       string
	expectedLineageID    string
	expectedCheckpointNS string
	interrupt            *graph.InterruptError
	lineageID            string
	checkpointID         string
	checkpointNS         string
	conflictErr          error
}

func (at *Tool) newGraphToolInterruptCapture(
	runtimeState graph.State,
	parentNodeID string,
	toolCallID string,
	toolCallKey string,
	childKey string,
	enabled bool,
) *graphToolInterruptCapture {
	if !enabled {
		return nil
	}
	childAgentName := at.agent.Info().Name
	expectedLineageID, _ := runtimeState[graph.CfgKeyLineageID].(string)
	expectedCheckpointNS, _ := runtimeState[graph.CfgKeyCheckpointNS].(string)
	return &graphToolInterruptCapture{
		parentNodeID:         parentNodeID,
		toolCallID:           toolCallID,
		toolCallKey:          toolCallKey,
		childKey:             childKey,
		childAgentName:       childAgentName,
		expectedLineageID:    expectedLineageID,
		expectedCheckpointNS: expectedCheckpointNS,
	}
}

func (at *Tool) wrapGraphToolInterruptCapture(
	src <-chan *event.Event,
	capture *graphToolInterruptCapture,
) <-chan *event.Event {
	if capture == nil {
		return src
	}
	out := make(chan *event.Event)
	go func() {
		defer close(out)
		for evt := range src {
			capture.observe(evt)
			out <- evt
		}
	}()
	return out
}

func shouldSuppressGraphRuntimeSessionEvent(
	inv *coreagent.Invocation,
	evt *event.Event,
) bool {
	if inv == nil || evt == nil {
		return false
	}
	suppressed, _ := coreagent.GetStateValue[bool](
		inv,
		graphRuntimeSuppressSessionEventsStateKey,
	)
	return suppressed && strings.HasPrefix(evt.Object, "graph.")
}

func (c *graphToolInterruptCapture) observe(evt *event.Event) {
	if c == nil || evt == nil || evt.Object != graph.ObjectTypeGraphPregelStep {
		return
	}
	raw, ok := evt.StateDelta[graph.MetadataKeyPregel]
	if !ok || len(raw) == 0 {
		return
	}
	var meta graph.PregelStepMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return
	}
	if meta.NodeID == "" || meta.InterruptValue == nil {
		return
	}
	if c.expectedLineageID != "" && meta.LineageID != c.expectedLineageID {
		return
	}
	if c.expectedCheckpointNS != "" && meta.CheckpointNS != c.expectedCheckpointNS {
		return
	}
	interrupt := graph.NewInterruptError(meta.InterruptValue)
	interrupt.NodeID = meta.NodeID
	interruptKey := meta.InterruptKey
	if interruptKey == "" {
		interruptKey = meta.NodeID
	}
	interrupt.Key = interruptKey
	interrupt.TaskID = interruptKey
	if c.interrupt != nil && !c.sameInterrupt(interrupt, meta) {
		c.conflictErr = fmt.Errorf("agent tool graph captured multiple interrupt checkpoints")
		return
	}
	c.interrupt = interrupt
	c.lineageID = meta.LineageID
	c.checkpointID = meta.CheckpointID
	c.checkpointNS = meta.CheckpointNS
}

func (c *graphToolInterruptCapture) sameInterrupt(
	interrupt *graph.InterruptError,
	meta graph.PregelStepMetadata,
) bool {
	return c.interrupt.NodeID == interrupt.NodeID &&
		c.interrupt.TaskID == interrupt.TaskID &&
		c.lineageID == meta.LineageID &&
		c.checkpointID == meta.CheckpointID &&
		c.checkpointNS == meta.CheckpointNS
}

func (c *graphToolInterruptCapture) finish() error {
	if c == nil {
		return nil
	}
	if c.conflictErr != nil {
		return c.conflictErr
	}
	if c.interrupt == nil {
		return nil
	}
	if c.lineageID == "" || c.checkpointID == "" || c.checkpointNS == "" {
		return fmt.Errorf(
			"agent tool graph interrupt missing checkpoint metadata: lineage_id=%q checkpoint_id=%q checkpoint_ns=%q",
			c.lineageID,
			c.checkpointID,
			c.checkpointNS,
		)
	}
	if c.interrupt.TaskID == "" {
		return fmt.Errorf("agent tool graph interrupt missing task id")
	}
	if c.toolCallID == "" {
		return fmt.Errorf("agent tool graph interrupt missing tool call id")
	}
	if c.toolCallKey == "" {
		return fmt.Errorf("agent tool graph interrupt missing tool call key")
	}
	if c.childKey == "" {
		return fmt.Errorf("agent tool graph interrupt missing child filter key")
	}
	return agenttoolgraph.NewInterruptError(c.interrupt, agenttoolgraph.InterruptMetadata{
		ParentNodeID:      c.parentNodeID,
		ChildAgentName:    c.childAgentName,
		ChildCheckpointID: c.checkpointID,
		ChildCheckpointNS: c.checkpointNS,
		ChildLineageID:    c.lineageID,
		ChildTaskID:       c.interrupt.TaskID,
		ToolCallID:        c.toolCallID,
		ToolCallKey:       c.toolCallKey,
		ChildFilterKey:    c.childKey,
	})
}
