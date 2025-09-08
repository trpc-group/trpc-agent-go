//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sqlite

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3" // Import SQLite driver.
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/graph"
)

func setupTestDB(t *testing.T) (*sql.DB, func()) {
	// Create a temporary database file.
	tmpfile, err := os.CreateTemp("", "test-*.db")
	require.NoError(t, err)

	// Open the database.
	db, err := sql.Open("sqlite3", tmpfile.Name())
	require.NoError(t, err)

	// Return cleanup function.
	cleanup := func() {
		db.Close()
		os.Remove(tmpfile.Name())
	}

	return db, cleanup
}

func TestSQLiteCheckpointSaver(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	saver, err := NewSaver(db)
	require.NoError(t, err)
	defer saver.Close()

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
	// JSON unmarshaling converts integers to float64, so compare values properly.
	assert.Equal(t, len(checkpoint.ChannelValues), len(retrieved.ChannelValues))
	for key, expectedVal := range checkpoint.ChannelValues {
		actualVal, exists := retrieved.ChannelValues[key]
		assert.True(t, exists, "Key %s should exist", key)
		// Compare as float64 since JSON unmarshaling converts numbers to float64.
		assert.Equal(t, float64(expectedVal.(int)), actualVal)
	}

	// Test retrieving tuple.
	tuple, err := saver.GetTuple(ctx, updatedConfig)
	require.NoError(t, err)
	require.NotNil(t, tuple)

	assert.NotEmpty(t, tuple.Checkpoint.ID)
	assert.Equal(t, metadata.Source, tuple.Metadata.Source)
	assert.Equal(t, metadata.Step, tuple.Metadata.Step)
}

func TestSQLiteCheckpointSaverList(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	saver, err := NewSaver(db)
	require.NoError(t, err)
	defer saver.Close()

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

func TestSQLiteCheckpointSaverWrites(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	saver, err := NewSaver(db)
	require.NoError(t, err)
	defer saver.Close()

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
	assert.Equal(t, float64(42), tuple.PendingWrites[0].Value)
	assert.Equal(t, "message", tuple.PendingWrites[1].Channel)
	assert.Equal(t, "hello", tuple.PendingWrites[1].Value)
}

func TestSQLiteCheckpointSaverDeleteLineage(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	saver, err := NewSaver(db)
	require.NoError(t, err)
	defer saver.Close()

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

func TestSQLiteCheckpointSaverLatestCheckpoint(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	saver, err := NewSaver(db)
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	lineageID := "test-lineage"
	config := graph.CreateCheckpointConfig(lineageID, "", "")

	// Create multiple checkpoints.
	var checkpointIDs []string
	for i := 0; i < 3; i++ {
		// Add small delay to ensure different timestamps.
		if i > 0 {
			time.Sleep(10 * time.Millisecond)
		}
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
		updatedConfig, err := saver.Put(ctx, req)
		require.NoError(t, err)

		checkpointID := graph.GetCheckpointID(updatedConfig)
		checkpointIDs = append(checkpointIDs, checkpointID)
	}

	// Get latest checkpoint (should be the last one created).
	latest, err := saver.Get(ctx, config)
	require.NoError(t, err)
	require.NotNil(t, latest)

	// Debug: print what we got
	t.Logf("Expected ID: %s, Got ID: %s", checkpointIDs[2], latest.ID)
	t.Logf("Expected step: 2, Got step: %v", latest.ChannelValues["step"])

	// Verify it's the latest checkpoint.
	assert.Equal(t, checkpointIDs[2], latest.ID)
	assert.Equal(t, float64(2), latest.ChannelValues["step"])
}

func TestSQLiteCheckpointSaverMetadataFilter(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	saver, err := NewSaver(db)
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	lineageID := "test-lineage"
	config := graph.CreateCheckpointConfig(lineageID, "", "")

	// Create checkpoints with different metadata.
	for i := 0; i < 3; i++ {
		checkpoint := graph.NewCheckpoint(
			map[string]any{"step": i},
			map[string]int64{"step": int64(i + 1)},
			map[string]map[string]int64{},
		)
		metadata := graph.NewCheckpointMetadata(graph.CheckpointSourceLoop, i)
		metadata.Extra["type"] = "test"
		if i == 1 {
			metadata.Extra["special"] = "yes"
		}

		req := graph.PutRequest{
			Config:      config,
			Checkpoint:  checkpoint,
			Metadata:    metadata,
			NewVersions: map[string]int64{"step": int64(i + 1)},
		}
		_, err := saver.Put(ctx, req)
		require.NoError(t, err)
	}

	// Filter by metadata.
	filter := &graph.CheckpointFilter{}
	filter.WithMetadata("special", "yes")

	checkpoints, err := saver.List(ctx, config, filter)
	require.NoError(t, err)
	assert.Len(t, checkpoints, 1)
	assert.Equal(t, float64(1), checkpoints[0].Checkpoint.ChannelValues["step"])
}

func TestSQLiteCheckpointSaverNilDB(t *testing.T) {
	_, err := NewSaver(nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "db is nil")
}

func TestSQLiteCheckpointSaverClose(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	saver, err := NewSaver(db)
	require.NoError(t, err)

	// Close should not error.
	err = saver.Close()
	assert.NoError(t, err)

	// Close again should not error.
	err = saver.Close()
	assert.NoError(t, err)
}
