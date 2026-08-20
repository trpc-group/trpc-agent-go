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
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
)

func requireEmitterEvent(t *testing.T, eventChan <-chan *event.Event) *event.Event {
	t.Helper()
	select {
	case evt := <-eventChan:
		return evt
	case <-time.After(time.Second):
		require.FailNow(t, "timeout waiting for event")
	}
	return nil
}

func TestEventEmitter_EmitCustom(t *testing.T) {
	eventChan := make(chan *event.Event, 10)
	emitter := NewEventEmitter(
		eventChan,
		WithEmitterNodeID("test-node"),
		WithEmitterInvocationID("test-invocation"),
		WithEmitterStepNumber(1),
	)

	err := emitter.EmitCustom("test-event", map[string]any{"key": "value"})
	require.NoError(t, err)

	select {
	case evt := <-eventChan:
		assert.Equal(t, "test-invocation", evt.InvocationID)
		assert.Equal(t, "test-node", evt.Author)
		assert.Equal(t, ObjectTypeGraphNodeCustom, evt.Object)
		assert.NotNil(t, evt.StateDelta)
		assert.Contains(t, evt.StateDelta, MetadataKeyNodeCustom)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestEventEmitter_EmitProgress(t *testing.T) {
	eventChan := make(chan *event.Event, 10)
	emitter := NewEventEmitter(
		eventChan,
		WithEmitterNodeID("test-node"),
		WithEmitterInvocationID("test-invocation"),
		WithEmitterBranch("test-branch"),
	)

	// Test normal progress
	err := emitter.EmitProgress(50.0, "halfway done")
	require.NoError(t, err)

	select {
	case evt := <-eventChan:
		assert.Equal(t, ObjectTypeGraphNodeCustom, evt.Object)
		assert.Equal(t, "test-branch", evt.Branch)
		assert.Contains(t, evt.StateDelta, MetadataKeyNodeCustom)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}

	// Test progress clamping (should clamp to 0-100)
	err = emitter.EmitProgress(-10.0, "negative")
	require.NoError(t, err)

	err = emitter.EmitProgress(150.0, "over 100")
	require.NoError(t, err)
}

func TestEventEmitter_EmitText(t *testing.T) {
	eventChan := make(chan *event.Event, 10)
	emitter := NewEventEmitter(
		eventChan,
		WithEmitterNodeID("test-node"),
		WithEmitterInvocationID("test-invocation"),
		WithEmitterBranch("test-branch"),
	)

	err := emitter.EmitText("Hello, World!")
	require.NoError(t, err)

	select {
	case evt := <-eventChan:
		assert.Equal(t, ObjectTypeGraphNodeCustom, evt.Object)
		assert.Equal(t, "test-branch", evt.Branch)
		assert.Contains(t, evt.StateDelta, MetadataKeyNodeCustom)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestEventEmitter_Emit(t *testing.T) {
	eventChan := make(chan *event.Event, 10)
	emitter := NewEventEmitter(
		eventChan,
		WithEmitterNodeID("test-node"),
		WithEmitterInvocationID("test-invocation"),
		WithEmitterBranch("test-branch"),
	)

	evt := event.New("", "", event.WithObject("test-object"))
	err := emitter.Emit(evt)
	require.NoError(t, err)

	select {
	case received := <-eventChan:
		assert.Equal(t, "test-invocation", received.InvocationID)
		assert.Equal(t, "test-node", received.Author)
		assert.Equal(t, "test-branch", received.Branch)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestEventEmitter_EmitPreservesExplicitEventFields(t *testing.T) {
	eventChan := make(chan *event.Event, 10)
	inv := agent.NewInvocation(
		agent.WithInvocationID("injected-invocation"),
		agent.WithInvocationBranch("injected-branch"),
		agent.WithInvocationEventFilterKey("injected-filter"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			RequestID: "injected-request",
		}),
	)
	emitter := NewEventEmitter(
		eventChan,
		WithEmitterNodeID("test-node"),
		WithEmitterInvocationID("fallback-invocation"),
		WithEmitterBranch("fallback-branch"),
		withEmitterInvocation(inv),
	)
	evt := event.New("explicit-invocation", "", event.WithBranch("explicit-branch"))
	evt.RequestID = "explicit-request"
	evt.ParentInvocationID = "explicit-parent"
	evt.FilterKey = "explicit-filter"
	err := emitter.Emit(evt)
	require.NoError(t, err)
	received := requireEmitterEvent(t, eventChan)
	require.Equal(t, "explicit-request", received.RequestID)
	require.Equal(t, "explicit-parent", received.ParentInvocationID)
	require.Equal(t, "explicit-invocation", received.InvocationID)
	require.Equal(t, "explicit-branch", received.Branch)
	require.Equal(t, "explicit-filter", received.FilterKey)
	require.Equal(t, "test-node", received.Author)
}

func TestEventEmitter_EmitDoesNotMixDifferentInvocationFields(t *testing.T) {
	eventChan := make(chan *event.Event, 10)
	inv := agent.NewInvocation(
		agent.WithInvocationID("current-invocation"),
		agent.WithInvocationBranch("current-branch"),
		agent.WithInvocationEventFilterKey("current-filter"),
		agent.WithInvocationParentMetadata(&event.ParentInvocationMetadata{
			TriggerType: event.TriggerTypeToolCall,
			TriggerID:   "current-trigger",
			TriggerName: "current-tool",
		}),
		agent.WithInvocationRunOptions(agent.RunOptions{
			RequestID: "current-request",
		}),
	)
	emitter := NewEventEmitter(
		eventChan,
		WithEmitterNodeID("test-node"),
		withEmitterInvocation(inv),
	)
	err := emitter.Emit(event.New("other-invocation", ""))
	require.NoError(t, err)
	received := requireEmitterEvent(t, eventChan)
	require.Equal(t, "other-invocation", received.InvocationID)
	require.Empty(t, received.RequestID)
	require.Empty(t, received.Branch)
	require.Empty(t, received.FilterKey)
	require.Nil(t, received.ParentMetadata)
	require.Equal(t, "test-node", received.Author)
}

func TestEventEmitter_EmitCustomInheritsParentFields(t *testing.T) {
	eventChan := make(chan *event.Event, 10)
	parent := agent.NewInvocation(agent.WithInvocationID("parent-invocation"))
	parentMetadata := &event.ParentInvocationMetadata{
		TriggerType: event.TriggerTypeToolCall,
		TriggerID:   "tool-call",
		TriggerName: "tool-name",
	}
	child := parent.Clone(
		agent.WithInvocationID("child-invocation"),
		agent.WithInvocationParentMetadata(parentMetadata),
	)
	emitter := NewEventEmitter(
		eventChan,
		WithEmitterNodeID("test-node"),
		withEmitterInvocation(child),
	)
	err := emitter.EmitCustom("test-type", nil)
	require.NoError(t, err)
	received := requireEmitterEvent(t, eventChan)
	require.Equal(t, "parent-invocation", received.ParentInvocationID)
	require.Same(t, parentMetadata, received.ParentMetadata)
}

func TestEventEmitter_NilEvent(t *testing.T) {
	eventChan := make(chan *event.Event, 10)
	emitter := NewEventEmitter(eventChan)

	// Emit nil event should not panic or error
	err := emitter.Emit(nil)
	assert.NoError(t, err)
}

func TestNoopEmitter(t *testing.T) {
	emitter := NewEventEmitter(nil) // nil channel returns noopEmitter

	// All methods should return nil without panic
	assert.NoError(t, emitter.Emit(&event.Event{}))
	assert.NoError(t, emitter.EmitCustom("type", nil))
	assert.NoError(t, emitter.EmitProgress(50, "msg"))
	assert.NoError(t, emitter.EmitText("text"))
	assert.NotNil(t, emitter.Context())
}

func TestGetEventEmitter_NilState(t *testing.T) {
	emitter := GetEventEmitter(nil)

	// Should return noopEmitter
	assert.NoError(t, emitter.EmitCustom("type", nil))
}

func TestGetEventEmitter_NoExecutionContext(t *testing.T) {
	state := State{
		"some_key": "some_value",
	}

	emitter := GetEventEmitter(state)

	// Should return noopEmitter
	assert.NoError(t, emitter.EmitCustom("type", nil))
}

func TestGetEventEmitter_NilEventChan(t *testing.T) {
	execCtx := &ExecutionContext{
		InvocationID: "test-invocation",
		EventChan:    nil,
	}
	state := State{
		StateKeyExecContext: execCtx,
	}

	emitter := GetEventEmitter(state)

	// Should return noopEmitter
	assert.NoError(t, emitter.EmitCustom("type", nil))
}

func TestGetEventEmitter_WithValidContext(t *testing.T) {
	eventChan := make(chan *event.Event, 10)
	execCtx := &ExecutionContext{
		InvocationID: "test-invocation",
		EventChan:    eventChan,
	}
	state := State{
		StateKeyExecContext:   execCtx,
		StateKeyCurrentNodeID: "test-node",
	}

	emitter := GetEventEmitter(state)

	err := emitter.EmitCustom("test-type", map[string]any{"foo": "bar"})
	require.NoError(t, err)

	select {
	case evt := <-eventChan:
		assert.Equal(t, "test-invocation", evt.InvocationID)
		assert.Equal(t, "test-node", evt.Author)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestGetEventEmitterWithContext_InjectsInvocation(t *testing.T) {
	eventChan := make(chan *event.Event, 10)
	inv := agent.NewInvocation(
		agent.WithInvocationID("test-invocation"),
		agent.WithInvocationBranch("test-branch"),
		agent.WithInvocationEventFilterKey("test-filter"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			RequestID: "test-request",
		}),
	)
	execCtx := &ExecutionContext{
		InvocationID: "stale-invocation",
		Invocation:   inv,
		EventChan:    eventChan,
	}
	state := State{
		StateKeyExecContext:   execCtx,
		StateKeyCurrentNodeID: "test-node",
	}

	emitter := GetEventEmitterWithContext(context.Background(), state)
	err := emitter.EmitCustom("test-type", map[string]any{"foo": "bar"})
	require.NoError(t, err)

	evt := requireEmitterEvent(t, eventChan)
	require.Equal(t, "test-request", evt.RequestID)
	require.Equal(t, "test-invocation", evt.InvocationID)
	require.Equal(t, "test-branch", evt.Branch)
	require.Equal(t, "test-filter", evt.FilterKey)
	require.Contains(t, evt.StateDelta, MetadataKeyNodeCustom)
	var metadata NodeCustomEventMetadata
	require.NoError(t, json.Unmarshal(evt.StateDelta[MetadataKeyNodeCustom], &metadata))
	require.Equal(t, "test-invocation", metadata.InvocationID)
	require.Equal(t, "test-node", metadata.NodeID)
}

func TestGetEventEmitterWithContext_InjectsContextInvocation(t *testing.T) {
	eventChan := make(chan *event.Event, 10)
	inv := agent.NewInvocation(
		agent.WithInvocationID("ctx-invocation"),
		agent.WithInvocationBranch("ctx-branch"),
		agent.WithInvocationEventFilterKey("ctx-filter"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			RequestID: "ctx-request",
		}),
	)
	execCtx := &ExecutionContext{
		InvocationID: "stale-invocation",
		EventChan:    eventChan,
	}
	state := State{
		StateKeyExecContext:   execCtx,
		StateKeyCurrentNodeID: "test-node",
	}
	ctx := agent.NewInvocationContext(context.Background(), inv)
	emitter := GetEventEmitterWithContext(ctx, state)
	err := emitter.EmitCustom("test-type", map[string]any{"foo": "bar"})
	require.NoError(t, err)
	evt := requireEmitterEvent(t, eventChan)
	require.Equal(t, "ctx-request", evt.RequestID)
	require.Equal(t, "ctx-invocation", evt.InvocationID)
	require.Equal(t, "ctx-branch", evt.Branch)
	require.Equal(t, "ctx-filter", evt.FilterKey)
	require.Contains(t, evt.StateDelta, MetadataKeyNodeCustom)
	var metadata NodeCustomEventMetadata
	require.NoError(t, json.Unmarshal(evt.StateDelta[MetadataKeyNodeCustom], &metadata))
	require.Equal(t, "ctx-invocation", metadata.InvocationID)
	require.Equal(t, "test-node", metadata.NodeID)
}

func TestGetEventEmitterWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventChan := make(chan *event.Event, 10)
	execCtx := &ExecutionContext{
		InvocationID: "test-invocation",
		EventChan:    eventChan,
	}
	state := State{
		StateKeyExecContext: execCtx,
	}

	emitter := GetEventEmitterWithContext(ctx, state)

	assert.Equal(t, ctx, emitter.Context())
}

func TestEventEmitter_WithTimeout(t *testing.T) {
	eventChan := make(chan *event.Event, 1)
	emitter := NewEventEmitter(
		eventChan,
		WithEmitterTimeout(100*time.Millisecond),
	)

	// First emit should succeed
	err := emitter.EmitCustom("test", nil)
	assert.NoError(t, err)
}

func TestEventEmitter_RecoverFromPanic(t *testing.T) {
	// Create a closed channel to simulate panic scenario
	eventChan := make(chan *event.Event, 1)
	close(eventChan)

	emitter := &eventEmitter{
		ctx:          context.Background(),
		eventChan:    eventChan,
		nodeID:       "test-node",
		invocationID: "test-invocation",
	}

	// This should recover from panic and not propagate error
	err := emitter.EmitCustom("test", nil)
	assert.NoError(t, err)
}
