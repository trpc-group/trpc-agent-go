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
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/graph"
)

func TestInMemoryCheckpointSaver(t *testing.T) {
	saver := NewSaver()
	ctx := context.Background()

	lineageID := "test-lineage"
	config := graph.CreateCheckpointConfig(lineageID, "", "")

	// Create a checkpoint.
	checkpoint := graph.NewCheckpoint(
		map[string]any{"counter": 1},
		map[string]int64{"counter": 1},
		map[string]map[string]int64{},
	)
	metadata := graph.NewCheckpointMetadata(graph.CheckpointSourceInput, -1)

	// Store checkpoint.
	req := graph.PutRequest{
		Config:      config,
		Checkpoint:  checkpoint,
		Metadata:    metadata,
		NewVersions: map[string]int64{"counter": 1},
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
		// Handle both float64 and json.Number types for comparison
		expectedFloat := float64(expectedValue.(int))
		var actualFloat float64
		switch v := actualValue.(type) {
		case float64:
			actualFloat = v
		case json.Number:
			if f, err := v.Float64(); err == nil {
				actualFloat = f
			} else {
				t.Fatalf("Failed to convert json.Number to float64: %v", err)
			}
		default:
			t.Fatalf("Unexpected type for actualValue: %T", actualValue)
		}
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

	lineageID := "test-lineage"
	config := graph.CreateCheckpointConfig(lineageID, "", "")

	// Create multiple checkpoints.
	for i := 0; i < 3; i++ {
		checkpoint := graph.NewCheckpoint(
			map[string]any{"step": i},
			map[string]int64{"step": int64(i + 1)},
			map[string]map[string]int64{},
		)
		metadata := graph.NewCheckpointMetadata(graph.CheckpointSourceLoop, i)

		req := graph.PutRequest{
			Config:      config,
			Checkpoint:  checkpoint,
			Metadata:    metadata,
			NewVersions: map[string]int64{"step": int64(i + 1)},
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

	lineageID := "test-lineage"
	config := graph.CreateCheckpointConfig(lineageID, "", "")

	// Create a checkpoint first.
	checkpoint := graph.NewCheckpoint(
		map[string]any{"counter": 0},
		map[string]int64{"counter": 1},
		map[string]map[string]int64{},
	)
	metadata := graph.NewCheckpointMetadata(graph.CheckpointSourceInput, -1)

	req := graph.PutRequest{
		Config:      config,
		Checkpoint:  checkpoint,
		Metadata:    metadata,
		NewVersions: map[string]int64{"counter": 1},
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

func TestInMemoryCheckpointSaverDeleteLineage(t *testing.T) {
	saver := NewSaver()
	ctx := context.Background()

	lineageID := "test-lineage"
	config := graph.CreateCheckpointConfig(lineageID, "", "")

	// Create a checkpoint.
	checkpoint := graph.NewCheckpoint(
		map[string]any{"counter": 42},
		map[string]int64{"counter": 1},
		map[string]map[string]int64{},
	)
	metadata := graph.NewCheckpointMetadata(graph.CheckpointSourceInput, -1)

	req := graph.PutRequest{
		Config:      config,
		Checkpoint:  checkpoint,
		Metadata:    metadata,
		NewVersions: map[string]int64{"counter": 1},
	}
	updatedConfig, err := saver.Put(ctx, req)
	require.NoError(t, err)

	// Verify checkpoint exists.
	retrieved, err := saver.Get(ctx, updatedConfig)
	require.NoError(t, err)
	assert.NotNil(t, retrieved)

	// Delete lineage.
	err = saver.DeleteLineage(ctx, lineageID)
	require.NoError(t, err)

	// Verify checkpoint is gone.
	retrieved, err = saver.Get(ctx, updatedConfig)
	require.NoError(t, err)
	assert.Nil(t, retrieved)
}

func TestInMemoryCheckpointSaverMaxCheckpoints(t *testing.T) {
	saver := NewSaver().WithMaxCheckpointsPerLineage(2)
	ctx := context.Background()

	lineageID := "test-lineage"
	config := graph.CreateCheckpointConfig(lineageID, "", "")

	// Create 3 checkpoints (exceeds limit of 2).
	for i := 0; i < 3; i++ {
		checkpoint := graph.NewCheckpoint(
			map[string]any{"step": i},
			map[string]int64{"step": int64(i + 1)},
			map[string]map[string]int64{},
		)
		metadata := graph.NewCheckpointMetadata(graph.CheckpointSourceLoop, i)

		req := graph.PutRequest{
			Config:      config,
			Checkpoint:  checkpoint,
			Metadata:    metadata,
			NewVersions: map[string]int64{"step": int64(i + 1)},
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

	lineageID := "test-lineage"
	config := graph.CreateCheckpointConfig(lineageID, "", "")

	// Test concurrent writes.
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(id int) {
			defer func() { done <- true }()

			checkpoint := graph.NewCheckpoint(
				map[string]any{"counter": id},
				map[string]int64{"counter": int64(id + 1)},
				map[string]map[string]int64{},
			)
			metadata := graph.NewCheckpointMetadata(graph.CheckpointSourceLoop, id)

			req := graph.PutRequest{
				Config:      config,
				Checkpoint:  checkpoint,
				Metadata:    metadata,
				NewVersions: map[string]int64{"counter": int64(id + 1)},
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

	lineageID := "test-lineage"
	config := graph.CreateCheckpointConfig(lineageID, "", "")

	// Create a checkpoint.
	checkpoint := graph.NewCheckpoint(
		map[string]any{"counter": 42},
		map[string]int64{"counter": 1},
		map[string]map[string]int64{},
	)
	metadata := graph.NewCheckpointMetadata(graph.CheckpointSourceInput, -1)

	req := graph.PutRequest{
		Config:      config,
		Checkpoint:  checkpoint,
		Metadata:    metadata,
		NewVersions: map[string]int64{"counter": 1},
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

func TestInMemoryCheckpointSaverPutFull(t *testing.T) {
	saver := NewSaver()
	ctx := context.Background()

	lineageID := "test-lineage"
	namespace := "test-namespace"
	config := graph.CreateCheckpointConfig(lineageID, "", namespace)

	// Create a checkpoint with parent ID.
	parentCheckpoint := graph.NewCheckpoint(
		map[string]any{"counter": 0},
		map[string]int64{"counter": 1},
		map[string]map[string]int64{},
	)
	parentCheckpoint.ID = "parent-checkpoint-id"

	// First store the parent checkpoint.
	parentReq := graph.PutRequest{
		Config:     config,
		Checkpoint: parentCheckpoint,
		Metadata:   graph.NewCheckpointMetadata(graph.CheckpointSourceInput, -1),
	}
	_, err := saver.Put(ctx, parentReq)
	require.NoError(t, err)

	// Create child checkpoint with parent reference.
	childCheckpoint := graph.NewCheckpoint(
		map[string]any{"counter": 1},
		map[string]int64{"counter": 2},
		map[string]map[string]int64{},
	)
	childCheckpoint.ParentCheckpointID = parentCheckpoint.ID
	metadata := graph.NewCheckpointMetadata(graph.CheckpointSourceLoop, 1)

	// Create pending writes.
	pendingWrites := []graph.PendingWrite{
		{
			Channel:  "counter",
			Value:    42,
			TaskID:   "task1",
			Sequence: 1,
		},
		{
			Channel:  "message",
			Value:    "test message",
			TaskID:   "task2",
			Sequence: 2,
		},
	}

	// Use PutFull to store checkpoint and writes atomically.
	fullReq := graph.PutFullRequest{
		Config:        config,
		Checkpoint:    childCheckpoint,
		Metadata:      metadata,
		NewVersions:   map[string]int64{"counter": 2},
		PendingWrites: pendingWrites,
	}
	updatedConfig, err := saver.PutFull(ctx, fullReq)
	require.NoError(t, err)
	require.NotNil(t, updatedConfig)

	// Verify checkpoint was stored.
	checkpointID := graph.GetCheckpointID(updatedConfig)
	assert.NotEmpty(t, checkpointID)

	// Retrieve tuple and verify everything.
	tuple, err := saver.GetTuple(ctx, updatedConfig)
	require.NoError(t, err)
	require.NotNil(t, tuple)

	// Verify checkpoint.
	assert.Equal(t, childCheckpoint.ID, tuple.Checkpoint.ID)
	assert.Equal(t, parentCheckpoint.ID, tuple.Checkpoint.ParentCheckpointID)

	// Verify metadata.
	assert.Equal(t, metadata.Source, tuple.Metadata.Source)
	assert.Equal(t, metadata.Step, tuple.Metadata.Step)

	// Verify parent config.
	assert.NotNil(t, tuple.ParentConfig)
	parentID := graph.GetCheckpointID(tuple.ParentConfig)
	assert.Equal(t, parentCheckpoint.ID, parentID)

	// Verify pending writes.
	assert.Len(t, tuple.PendingWrites, 2)
	assert.Equal(t, "counter", tuple.PendingWrites[0].Channel)
	assert.Equal(t, 42, tuple.PendingWrites[0].Value)
	assert.Equal(t, "task1", tuple.PendingWrites[0].TaskID)
	assert.Equal(t, "message", tuple.PendingWrites[1].Channel)
	assert.Equal(t, "test message", tuple.PendingWrites[1].Value)
	assert.Equal(t, "task2", tuple.PendingWrites[1].TaskID)
}

func TestInMemoryCheckpointSaverGetLatest(t *testing.T) {
	saver := NewSaver()
	ctx := context.Background()

	lineageID := "test-lineage"
	namespace := "test-namespace"

	// Test getting latest when no checkpoint exists.
	emptyConfig := graph.CreateCheckpointConfig(lineageID, "", namespace)
	retrieved, err := saver.Get(ctx, emptyConfig)
	require.NoError(t, err)
	assert.Nil(t, retrieved)

	// Create multiple checkpoints.
	var lastConfig map[string]any
	for i := 0; i < 3; i++ {
		checkpoint := graph.NewCheckpoint(
			map[string]any{"step": i},
			map[string]int64{"step": int64(i + 1)},
			map[string]map[string]int64{},
		)
		metadata := graph.NewCheckpointMetadata(graph.CheckpointSourceLoop, i)

		req := graph.PutRequest{
			Config:      graph.CreateCheckpointConfig(lineageID, "", namespace),
			Checkpoint:  checkpoint,
			Metadata:    metadata,
			NewVersions: map[string]int64{"step": int64(i + 1)},
		}
		lastConfig, err = saver.Put(ctx, req)
		require.NoError(t, err)
	}

	// Get latest checkpoint (should be the last one created).
	latestConfig := graph.CreateCheckpointConfig(lineageID, "", namespace)
	latestTuple, err := saver.GetTuple(ctx, latestConfig)
	require.NoError(t, err)
	require.NotNil(t, latestTuple)

	// Verify it's the latest.
	lastCheckpointID := graph.GetCheckpointID(lastConfig)
	assert.Equal(t, lastCheckpointID, latestTuple.Checkpoint.ID)

	// Test cross-namespace search (empty namespace).
	crossNamespaceConfig := graph.CreateCheckpointConfig(lineageID, "", "")
	crossNamespaceTuple, err := saver.GetTuple(ctx, crossNamespaceConfig)
	require.NoError(t, err)
	require.Nil(t, crossNamespaceTuple)
}

func TestInMemoryCheckpointSaverFilters(t *testing.T) {
	saver := NewSaver()
	ctx := context.Background()

	lineageID := "test-lineage"
	namespace := "test-namespace"
	config := graph.CreateCheckpointConfig(lineageID, "", namespace)

	// Create checkpoints with different metadata.
	var checkpointConfigs []map[string]any
	for i := 0; i < 5; i++ {
		checkpoint := graph.NewCheckpoint(
			map[string]any{"step": i},
			map[string]int64{"step": int64(i + 1)},
			map[string]map[string]int64{},
		)

		// Add extra metadata to some checkpoints.
		var metadata *graph.CheckpointMetadata
		if i%2 == 0 {
			metadata = graph.NewCheckpointMetadata(graph.CheckpointSourceLoop, i)
			metadata.Extra = map[string]any{
				"type":     "even",
				"priority": "high",
			}
		} else {
			metadata = graph.NewCheckpointMetadata(graph.CheckpointSourceInput, i)
			metadata.Extra = map[string]any{
				"type":     "odd",
				"priority": "low",
			}
		}

		req := graph.PutRequest{
			Config:      config,
			Checkpoint:  checkpoint,
			Metadata:    metadata,
			NewVersions: map[string]int64{"step": int64(i + 1)},
		}
		cfg, err := saver.Put(ctx, req)
		require.NoError(t, err)
		checkpointConfigs = append(checkpointConfigs, cfg)
	}

	// Test metadata filter - get only "even" type checkpoints.
	metadataFilter := &graph.CheckpointFilter{
		Metadata: map[string]any{"type": "even"},
	}
	evenCheckpoints, err := saver.List(ctx, config, metadataFilter)
	require.NoError(t, err)
	assert.Len(t, evenCheckpoints, 3) // 0, 2, 4 are even

	// Verify all returned checkpoints have "even" type.
	for _, tuple := range evenCheckpoints {
		assert.Equal(t, "even", tuple.Metadata.Extra["type"])
	}

	// Test Before filter - get checkpoints before the 3rd one.
	beforeFilter := &graph.CheckpointFilter{
		Before: checkpointConfigs[2], // Before index 2
	}
	beforeCheckpoints, err := saver.List(ctx, config, beforeFilter)
	require.NoError(t, err)
	assert.Len(t, beforeCheckpoints, 2) // Should have 0 and 1

	// Test combined filters.
	combinedFilter := &graph.CheckpointFilter{
		Metadata: map[string]any{"priority": "high"},
		Limit:    2,
	}
	combinedResult, err := saver.List(ctx, config, combinedFilter)
	require.NoError(t, err)
	assert.Len(t, combinedResult, 2)
	for _, tuple := range combinedResult {
		assert.Equal(t, "high", tuple.Metadata.Extra["priority"])
	}

	// Test cross-namespace List (empty namespace).
	crossNamespaceConfig := graph.CreateCheckpointConfig(lineageID, "", "")
	allCheckpoints, err := saver.List(ctx, crossNamespaceConfig, nil)
	require.NoError(t, err)
	assert.Len(t, allCheckpoints, 5)
}

func TestInMemoryCheckpointSaverErrorCases(t *testing.T) {
	saver := NewSaver()
	ctx := context.Background()

	// Test Get with empty lineage ID.
	invalidConfig := map[string]any{
		graph.CfgKeyConfigurable: map[string]any{},
	}
	_, err := saver.Get(ctx, invalidConfig)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "lineage_id is required")

	// Test List with empty lineage ID.
	_, err = saver.List(ctx, invalidConfig, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "lineage_id is required")

	// Test Put with nil checkpoint.
	config := graph.CreateCheckpointConfig("test-lineage", "", "")
	req := graph.PutRequest{
		Config:     config,
		Checkpoint: nil,
		Metadata:   graph.NewCheckpointMetadata(graph.CheckpointSourceInput, -1),
	}
	_, err = saver.Put(ctx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "checkpoint cannot be nil")

	// Test PutFull with nil checkpoint.
	fullReq := graph.PutFullRequest{
		Config:     config,
		Checkpoint: nil,
		Metadata:   graph.NewCheckpointMetadata(graph.CheckpointSourceInput, -1),
	}
	_, err = saver.PutFull(ctx, fullReq)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "checkpoint cannot be nil")

	// Test PutWrites with missing checkpoint ID.
	writeReq := graph.PutWritesRequest{
		Config: map[string]any{
			graph.CfgKeyConfigurable: map[string]any{
				graph.CfgKeyLineageID: "test-lineage",
			},
		},
		Writes: []graph.PendingWrite{{Channel: "test", Value: 1}},
	}
	err = saver.PutWrites(ctx, writeReq)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "checkpoint_id are required")
}
