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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
)

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
	)

	// Test normal progress
	err := emitter.EmitProgress(50.0, "halfway done")
	require.NoError(t, err)

	select {
	case evt := <-eventChan:
		assert.Equal(t, ObjectTypeGraphNodeCustom, evt.Object)
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
	)

	err := emitter.EmitText("Hello, World!")
	require.NoError(t, err)

	select {
	case evt := <-eventChan:
		assert.Equal(t, ObjectTypeGraphNodeCustom, evt.Object)
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
