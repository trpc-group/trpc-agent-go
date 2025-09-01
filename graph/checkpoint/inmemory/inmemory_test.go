//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package inmemory

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/graph"
)

func TestInMemoryCheckpointSaver(t *testing.T) {
	saver := NewSaver()
	ctx := context.Background()

	threadID := "test-thread"
	config := graph.CreateCheckpointConfig(threadID, "", "")

	// Create a checkpoint.
	checkpoint := graph.NewCheckpoint(
		map[string]any{"counter": 1},
		map[string]any{"counter": 1},
		map[string]map[string]any{},
	)
	metadata := graph.NewCheckpointMetadata(graph.CheckpointSourceInput, -1)

	// Store checkpoint.
	req := graph.PutRequest{
		Config:      config,
		Checkpoint:  checkpoint,
		Metadata:    metadata,
		NewVersions: map[string]any{"counter": 1},
	}
	updatedConfig, err := saver.Put(ctx, req)
	require.NoError(t, err)

	// Verify updated config contains checkpoint ID.
	checkpointID := graph.GetCheckpointID(updatedConfig)
	assert.NotEmpty(t, checkpointID)

	// Retrieve checkpoint.
	retrieved, err := saver.Get(ctx, updatedConfig)
	require.NoError(t, err)
	require.NotNil(t, retrieved)

	assert.NotEmpty(t, retrieved.ID)
	// Note: JSON serialization converts int to float64, so we need to compare values individually
	assert.Equal(t, len(checkpoint.ChannelValues), len(retrieved.ChannelValues))
	for key, expectedValue := range checkpoint.ChannelValues {
		actualValue, exists := retrieved.ChannelValues[key]
		assert.True(t, exists, "Key %s should exist", key)
		// Convert both to float64 for comparison since JSON unmarshaling converts int to float64
		expectedFloat := float64(expectedValue.(int))
		actualFloat := actualValue.(float64)
		assert.Equal(t, expectedFloat, actualFloat)
	}

	// Test retrieving tuple.
	tuple, err := saver.GetTuple(ctx, updatedConfig)
	require.NoError(t, err)
	require.NotNil(t, tuple)

	assert.NotEmpty(t, tuple.Checkpoint.ID)
	assert.Equal(t, metadata.Source, tuple.Metadata.Source)
	assert.Equal(t, metadata.Step, tuple.Metadata.Step)
}

func TestInMemoryCheckpointSaverList(t *testing.T) {
	saver := NewSaver()
	ctx := context.Background()

	threadID := "test-thread"
	config := graph.CreateCheckpointConfig(threadID, "", "")

	// Create multiple checkpoints.
	for i := 0; i < 3; i++ {
		checkpoint := graph.NewCheckpoint(
			map[string]any{"step": i},
			map[string]any{"step": i + 1},
			map[string]map[string]any{},
		)
		metadata := graph.NewCheckpointMetadata(graph.CheckpointSourceLoop, i)

		req := graph.PutRequest{
			Config:      config,
			Checkpoint:  checkpoint,
			Metadata:    metadata,
			NewVersions: map[string]any{"step": i + 1},
		}
		_, err := saver.Put(ctx, req)
		require.NoError(t, err)
	}

	// List checkpoints.
	checkpoints, err := saver.List(ctx, config, nil)
	require.NoError(t, err)
	assert.Len(t, checkpoints, 3)

	// Test filtering by limit.
	filter := &graph.CheckpointFilter{Limit: 2}
	limited, err := saver.List(ctx, config, filter)
	require.NoError(t, err)
	assert.Len(t, limited, 2)
}

func TestInMemoryCheckpointSaverWrites(t *testing.T) {
	saver := NewSaver()
	ctx := context.Background()

	threadID := "test-thread"
	config := graph.CreateCheckpointConfig(threadID, "", "")

	// Create a checkpoint first.
	checkpoint := graph.NewCheckpoint(
		map[string]any{"counter": 0},
		map[string]any{"counter": 1},
		map[string]map[string]any{},
	)
	metadata := graph.NewCheckpointMetadata(graph.CheckpointSourceInput, -1)

	req := graph.PutRequest{
		Config:      config,
		Checkpoint:  checkpoint,
		Metadata:    metadata,
		NewVersions: map[string]any{"counter": 1},
	}
	updatedConfig, err := saver.Put(ctx, req)
	require.NoError(t, err)

	// Store writes.
	writes := []graph.PendingWrite{
		{Channel: "counter", Value: 42},
		{Channel: "message", Value: "hello"},
	}

	writeReq := graph.PutWritesRequest{
		Config:   updatedConfig,
		Writes:   writes,
		TaskID:   "task1",
		TaskPath: "",
	}
	err = saver.PutWrites(ctx, writeReq)
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
	saver := NewSaver()
	ctx := context.Background()

	threadID := "test-thread"
	config := graph.CreateCheckpointConfig(threadID, "", "")

	// Create a checkpoint.
	checkpoint := graph.NewCheckpoint(
		map[string]any{"counter": 42},
		map[string]any{"counter": 1},
		map[string]map[string]any{},
	)
	metadata := graph.NewCheckpointMetadata(graph.CheckpointSourceInput, -1)

	req := graph.PutRequest{
		Config:      config,
		Checkpoint:  checkpoint,
		Metadata:    metadata,
		NewVersions: map[string]any{"counter": 1},
	}
	updatedConfig, err := saver.Put(ctx, req)
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

func TestInMemoryCheckpointSaverMaxCheckpoints(t *testing.T) {
	saver := NewSaver().WithMaxCheckpointsPerThread(2)
	ctx := context.Background()

	threadID := "test-thread"
	config := graph.CreateCheckpointConfig(threadID, "", "")

	// Create 3 checkpoints (exceeds limit of 2).
	for i := 0; i < 3; i++ {
		checkpoint := graph.NewCheckpoint(
			map[string]any{"step": i},
			map[string]any{"step": i + 1},
			map[string]map[string]any{},
		)
		metadata := graph.NewCheckpointMetadata(graph.CheckpointSourceLoop, i)

		req := graph.PutRequest{
			Config:      config,
			Checkpoint:  checkpoint,
			Metadata:    metadata,
			NewVersions: map[string]any{"step": i + 1},
		}
		_, err := saver.Put(ctx, req)
		require.NoError(t, err)
	}

	// List checkpoints - should only have 2 (the most recent ones).
	checkpoints, err := saver.List(ctx, config, nil)
	require.NoError(t, err)
	assert.Len(t, checkpoints, 2)
}

func TestInMemoryCheckpointSaverConcurrentAccess(t *testing.T) {
	saver := NewSaver()
	ctx := context.Background()

	threadID := "test-thread"
	config := graph.CreateCheckpointConfig(threadID, "", "")

	// Test concurrent writes.
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(id int) {
			defer func() { done <- true }()

			checkpoint := graph.NewCheckpoint(
				map[string]any{"counter": id},
				map[string]any{"counter": id + 1},
				map[string]map[string]any{},
			)
			metadata := graph.NewCheckpointMetadata(graph.CheckpointSourceLoop, id)

			req := graph.PutRequest{
				Config:      config,
				Checkpoint:  checkpoint,
				Metadata:    metadata,
				NewVersions: map[string]any{"counter": id + 1},
			}
			_, err := saver.Put(ctx, req)
			assert.NoError(t, err)
		}(i)
	}

	// Wait for all goroutines to complete.
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify all checkpoints were created.
	checkpoints, err := saver.List(ctx, config, nil)
	require.NoError(t, err)
	assert.Len(t, checkpoints, 10)
}

func TestInMemoryCheckpointSaverClose(t *testing.T) {
	saver := NewSaver()
	ctx := context.Background()

	threadID := "test-thread"
	config := graph.CreateCheckpointConfig(threadID, "", "")

	// Create a checkpoint.
	checkpoint := graph.NewCheckpoint(
		map[string]any{"counter": 42},
		map[string]any{"counter": 1},
		map[string]map[string]any{},
	)
	metadata := graph.NewCheckpointMetadata(graph.CheckpointSourceInput, -1)

	req := graph.PutRequest{
		Config:      config,
		Checkpoint:  checkpoint,
		Metadata:    metadata,
		NewVersions: map[string]any{"counter": 1},
	}
	updatedConfig, err := saver.Put(ctx, req)
	require.NoError(t, err)

	// Verify checkpoint exists.
	retrieved, err := saver.Get(ctx, updatedConfig)
	require.NoError(t, err)
	assert.NotNil(t, retrieved)

	// Close saver.
	err = saver.Close()
	require.NoError(t, err)

	// Verify checkpoint is gone after close.
	retrieved, err = saver.Get(ctx, updatedConfig)
	require.NoError(t, err)
	assert.Nil(t, retrieved)
}
