//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"context"
	"reflect"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckpointBasics(t *testing.T) {
	// Test checkpoint creation.
	channelValues := map[string]any{
		"counter": 42,
		"message": "hello",
	}
	channelVersions := map[string]any{
		"counter": 1,
		"message": 1,
	}
	versionsSeen := map[string]map[string]any{
		"node1": {
			"counter": 1,
			"message": 1,
		},
	}

	checkpoint := NewCheckpoint(channelValues, channelVersions, versionsSeen)

	assert.Equal(t, CheckpointVersion, checkpoint.Version)
	assert.NotEmpty(t, checkpoint.ID)
	assert.WithinDuration(t, time.Now().UTC(), checkpoint.Timestamp, 2*time.Second)
	assert.Equal(t, channelValues, checkpoint.ChannelValues)
	assert.Equal(t, channelVersions, checkpoint.ChannelVersions)
	assert.Equal(t, versionsSeen, checkpoint.VersionsSeen)
}

func TestCheckpointMetadata(t *testing.T) {
	metadata := NewCheckpointMetadata(CheckpointSourceInput, -1)

	assert.Equal(t, CheckpointSourceInput, metadata.Source)
	assert.Equal(t, -1, metadata.Step)
	assert.NotNil(t, metadata.Parents)
	assert.NotNil(t, metadata.Extra)
}

func TestCheckpointCopy(t *testing.T) {
	original := NewCheckpoint(
		map[string]any{"key": "value"},
		map[string]any{"key": 1},
		map[string]map[string]any{"node": {"key": 1}},
	)

	copied := original.Copy()

	assert.NotEqual(t, original.ID, copied.ID) // Should have new ID
	assert.Equal(t, original.ChannelValues, copied.ChannelValues)
	assert.Equal(t, original.ChannelVersions, copied.ChannelVersions)
	assert.Equal(t, original.VersionsSeen, copied.VersionsSeen)

	// Test that modifying copied doesn't affect original.
	copied.ChannelValues["new_key"] = "new_value"
	assert.NotEqual(t, original.ChannelValues, copied.ChannelValues)
}

func TestInMemoryCheckpointSaver(t *testing.T) {
	saver := NewInMemoryCheckpointSaver()
	ctx := context.Background()

	// Test storing and retrieving a checkpoint.
	threadID := "test-thread"
	config := CreateCheckpointConfig(threadID, "", "")

	checkpoint := NewCheckpoint(
		map[string]any{"counter": 42},
		map[string]any{"counter": 1},
		map[string]map[string]any{},
	)
	metadata := NewCheckpointMetadata(CheckpointSourceInput, -1)

	// Store checkpoint.
	updatedConfig, err := saver.Put(ctx, config, checkpoint, metadata, map[string]any{"counter": 1})
	require.NoError(t, err)

	// Verify updated config contains checkpoint ID.
	checkpointID := GetCheckpointID(updatedConfig)
	assert.NotEmpty(t, checkpointID)

	// Retrieve checkpoint.
	retrieved, err := saver.Get(ctx, updatedConfig)
	require.NoError(t, err)
	require.NotNil(t, retrieved)

	assert.Equal(t, checkpoint.ID, retrieved.ID)
	assert.Equal(t, checkpoint.ChannelValues, retrieved.ChannelValues)

	// Test retrieving tuple.
	tuple, err := saver.GetTuple(ctx, updatedConfig)
	require.NoError(t, err)
	require.NotNil(t, tuple)

	assert.Equal(t, checkpoint.ID, tuple.Checkpoint.ID)
	assert.Equal(t, metadata.Source, tuple.Metadata.Source)
	assert.Equal(t, metadata.Step, tuple.Metadata.Step)
}

func TestInMemoryCheckpointSaverList(t *testing.T) {
	saver := NewInMemoryCheckpointSaver()
	ctx := context.Background()

	threadID := "test-thread"
	config := CreateCheckpointConfig(threadID, "", "")

	// Create multiple checkpoints.
	for i := 0; i < 3; i++ {
		checkpoint := NewCheckpoint(
			map[string]any{"step": i},
			map[string]any{"step": i + 1},
			map[string]map[string]any{},
		)
		metadata := NewCheckpointMetadata(CheckpointSourceLoop, i)

		_, err := saver.Put(ctx, config, checkpoint, metadata, map[string]any{"step": i + 1})
		require.NoError(t, err)
	}

	// List checkpoints.
	checkpoints, err := saver.List(ctx, config, nil)
	require.NoError(t, err)
	assert.Len(t, checkpoints, 3)

	// Test filtering by limit.
	filter := &CheckpointFilter{Limit: 2}
	limited, err := saver.List(ctx, config, filter)
	require.NoError(t, err)
	assert.Len(t, limited, 2)
}

func TestInMemoryCheckpointSaverWrites(t *testing.T) {
	saver := NewInMemoryCheckpointSaver()
	ctx := context.Background()

	threadID := "test-thread"
	config := CreateCheckpointConfig(threadID, "", "")

	// Create a checkpoint first.
	checkpoint := NewCheckpoint(
		map[string]any{"counter": 0},
		map[string]any{"counter": 1},
		map[string]map[string]any{},
	)
	metadata := NewCheckpointMetadata(CheckpointSourceInput, -1)

	updatedConfig, err := saver.Put(ctx, config, checkpoint, metadata, map[string]any{"counter": 1})
	require.NoError(t, err)

	// Store writes.
	writes := []PendingWrite{
		{Channel: "counter", Value: 42},
		{Channel: "message", Value: "hello"},
	}

	err = saver.PutWrites(ctx, updatedConfig, writes, "task1", "")
	require.NoError(t, err)

	// Retrieve tuple and verify writes.
	tuple, err := saver.GetTuple(ctx, updatedConfig)
	require.NoError(t, err)
	require.NotNil(t, tuple)

	assert.Len(t, tuple.PendingWrites, 2)
	assert.Equal(t, "counter", tuple.PendingWrites[0].Channel)
	assert.Equal(t, 42, tuple.PendingWrites[0].Value)
	assert.Equal(t, "message", tuple.PendingWrites[1].Channel)
	assert.Equal(t, "hello", tuple.PendingWrites[1].Value)
}

func TestInMemoryCheckpointSaverDeleteThread(t *testing.T) {
	saver := NewInMemoryCheckpointSaver()
	ctx := context.Background()

	threadID := "test-thread"
	config := CreateCheckpointConfig(threadID, "", "")

	// Create a checkpoint.
	checkpoint := NewCheckpoint(
		map[string]any{"counter": 42},
		map[string]any{"counter": 1},
		map[string]map[string]any{},
	)
	metadata := NewCheckpointMetadata(CheckpointSourceInput, -1)

	updatedConfig, err := saver.Put(ctx, config, checkpoint, metadata, map[string]any{"counter": 1})
	require.NoError(t, err)

	// Verify checkpoint exists.
	retrieved, err := saver.Get(ctx, updatedConfig)
	require.NoError(t, err)
	assert.NotNil(t, retrieved)

	// Delete thread.
	err = saver.DeleteThread(ctx, threadID)
	require.NoError(t, err)

	// Verify checkpoint is gone.
	retrieved, err = saver.Get(ctx, updatedConfig)
	require.NoError(t, err)
	assert.Nil(t, retrieved)
}

func TestCheckpointManager(t *testing.T) {
	saver := NewInMemoryCheckpointSaver()
	manager := NewCheckpointManager(saver)
	ctx := context.Background()

	threadID := "test-thread"
	config := CreateCheckpointConfig(threadID, "", "")

	// Create state.
	state := State{
		"counter": 42,
		"message": "hello",
	}

	// Create checkpoint.
	checkpoint, err := manager.CreateCheckpoint(ctx, config, state, CheckpointSourceInput, -1)
	require.NoError(t, err)
	assert.NotNil(t, checkpoint)

	// Resume from checkpoint.
	resumedState, err := manager.ResumeFromCheckpoint(ctx, config)
	require.NoError(t, err)
	assert.NotNil(t, resumedState)

	assert.Equal(t, state["counter"], resumedState["counter"])
	assert.Equal(t, state["message"], resumedState["message"])
}

func TestCheckpointConfigHelpers(t *testing.T) {
	// Test config creation.
	threadID := "test-thread"
	checkpointID := "test-checkpoint"
	namespace := "test-namespace"

	config := CreateCheckpointConfig(threadID, checkpointID, namespace)

	// Test extraction functions.
	assert.Equal(t, threadID, GetThreadID(config))
	assert.Equal(t, checkpointID, GetCheckpointID(config))
	assert.Equal(t, namespace, GetNamespace(config))

	// Test with empty values.
	emptyConfig := CreateCheckpointConfig("", "", "")
	assert.Equal(t, "", GetThreadID(emptyConfig))
	assert.Equal(t, "", GetCheckpointID(emptyConfig))
	assert.Equal(t, DefaultCheckpointNamespace, GetNamespace(emptyConfig))
}

func TestCheckpointWithExecutor(t *testing.T) {
	// Create a simple graph.
	schema := NewStateSchema()
	schema.AddField("counter", StateField{
		Type:    reflect.TypeOf(0),
		Reducer: DefaultReducer,
		Default: func() any { return 0 },
	})

	graph := NewStateGraph(schema)
	graph.AddNode("increment", func(ctx context.Context, state State) (any, error) {
		counter := state["counter"].(int)
		return State{"counter": counter + 1}, nil
	})
	graph.SetEntryPoint("increment")
	graph.SetFinishPoint("increment")

	compiledGraph, err := graph.Compile()
	require.NoError(t, err)

	// Create executor with checkpoint saver.
	saver := NewInMemoryCheckpointSaver()
	executor, err := NewExecutor(compiledGraph, WithCheckpointSaver(saver))
	require.NoError(t, err)

	// Execute graph.
	ctx := context.Background()
	initialState := State{"counter": 0}
	invocation := &agent.Invocation{
		InvocationID: "test-invocation",
	}

	eventChan, err := executor.Execute(ctx, initialState, invocation)
	require.NoError(t, err)

	// Collect events.
	var events []*event.Event
	for event := range eventChan {
		events = append(events, event)
	}

	// Verify execution completed.
	assert.NotEmpty(t, events)

	// Verify checkpoint was created.
	config := CreateCheckpointConfig("test-invocation", "", "")
	tuple, err := saver.GetTuple(ctx, config)
	require.NoError(t, err)
	assert.NotNil(t, tuple)

	// Verify state was persisted.
	finalCounter := tuple.Checkpoint.ChannelValues["counter"]
	assert.Equal(t, 1, finalCounter)
}
