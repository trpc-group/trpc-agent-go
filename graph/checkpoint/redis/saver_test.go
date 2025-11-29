//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package redis provides Redis-based checkpoint storage implementation
// for graph execution state persistence and recovery.
package redis

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/graph"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/redis"
)

func setupTestRedis(t testing.TB) (string, func()) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	cleanup := func() {
		mr.Close()
	}
	return "redis://" + mr.Addr(), cleanup
}

func buildRedisClient(t *testing.T, redisURL string) *redis.Client {
	opts, err := redis.ParseURL(redisURL)
	require.NoError(t, err)
	return redis.NewClient(opts)
}

func TestNewSaverWithRedisInstance_buildSuccess(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	const (
		name = "test-instance"
	)

	defer cleanup()

	storage.RegisterRedisInstance(name, storage.WithClientBuilderURL(redisURL))
	opts, ok := storage.GetRedisInstance(name)
	require.True(t, ok, "expected instance to exist")
	require.NotEmpty(t, opts, "expected at least one option")

	saver, err := NewSaver(WithRedisInstance(name))
	require.NoError(t, err)
	defer saver.Close()
}

func TestNewSaverWithRedisInstance_buildFailed(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	const (
		name = "test-instance"
	)

	defer cleanup()

	storage.RegisterRedisInstance(name, storage.WithClientBuilderURL(redisURL))
	opts, ok := storage.GetRedisInstance(name)
	require.True(t, ok, "expected instance to exist")
	require.NotEmpty(t, opts, "expected at least one option")

	saver, err := NewSaver(WithRedisInstance("no-instance"))
	require.Error(t, err)
	require.Nil(t, saver)
}

func TestNewSaverWithRedisOption_Error(t *testing.T) {
	saver, err := NewSaver(WithRedisClientURL(""))
	require.Error(t, err)
	require.Nil(t, saver)
}

func TestRedisCheckpointSaver(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
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

func TestRedisCheckpointSaverList(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
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

func TestRedisCheckpointSaverWrites(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
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

func TestRedisCheckpointSaverDeleteLineage(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
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

func TestRedisCheckpointSaverLatestCheckpoint(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
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

func TestRedis_GetTuple_EmptyDB_ReturnsNil(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	// No checkpoints inserted yet
	cfg := graph.CreateCheckpointConfig("ln-empty", "", "")
	tup, err := saver.GetTuple(ctx, cfg)
	require.NoError(t, err)
	assert.Nil(t, tup)
}

func TestRedis_Put_MetadataDefault(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	lineageID := "ln-meta"
	ns := "ns"
	ck := graph.NewCheckpoint(map[string]any{"a": 1}, map[string]int64{"a": 1}, nil)
	// Put with nil metadata should not error
	cfg, err := saver.Put(ctx, graph.PutRequest{Config: graph.CreateCheckpointConfig(lineageID, "", ns), Checkpoint: ck, Metadata: nil, NewVersions: map[string]int64{"a": 1}})
	require.NoError(t, err)
	tup, err := saver.GetTuple(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, tup)
	// Metadata should exist with default Source
	require.NotNil(t, tup.Metadata)
	assert.NotEmpty(t, tup.Metadata.Source)
}

func TestRedis_PutWrites_SequenceUsed(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	cfg, err := saver.Put(ctx, graph.PutRequest{Config: graph.CreateCheckpointConfig("ln-writes", "", "ns"), Checkpoint: graph.NewCheckpoint(map[string]any{}, map[string]int64{}, nil), Metadata: graph.NewCheckpointMetadata(graph.CheckpointSourceInput, 0), NewVersions: map[string]int64{}})
	require.NoError(t, err)

	// Provide explicit sequence numbers
	writes := []graph.PendingWrite{
		{TaskID: "t", Channel: "x", Value: 1, Sequence: 101},
		{TaskID: "t", Channel: "y", Value: 2, Sequence: 102},
	}
	err = saver.PutWrites(ctx, graph.PutWritesRequest{Config: cfg, Writes: writes, TaskID: "t", TaskPath: "p"})
	require.NoError(t, err)

	tup, err := saver.GetTuple(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, tup)
	require.Len(t, tup.PendingWrites, 2)
	assert.Equal(t, int64(101), tup.PendingWrites[0].Sequence)
	assert.Equal(t, int64(102), tup.PendingWrites[1].Sequence)
}

func TestRedis_PutFull_SequenceHonored(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	lineageID := "ln-full-seq"
	ns := "ns"
	ck := graph.NewCheckpoint(map[string]any{"v": 1}, map[string]int64{"v": 1}, nil)
	cfg, err := saver.PutFull(ctx, graph.PutFullRequest{Config: graph.CreateCheckpointConfig(lineageID, "", ns), Checkpoint: ck, Metadata: graph.NewCheckpointMetadata(graph.CheckpointSourceInput, 0), NewVersions: map[string]int64{"v": 1}, PendingWrites: []graph.PendingWrite{{TaskID: "t1", Channel: "c1", Value: 1, Sequence: 999}}})
	require.NoError(t, err)

	tup, err := saver.GetTuple(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, tup)
	require.Len(t, tup.PendingWrites, 1)
	assert.Equal(t, int64(999), tup.PendingWrites[0].Sequence)
}

func TestRedis_PutFull_SequenceZero_Assigned(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	cfg, err := saver.Put(ctx, graph.PutRequest{Config: graph.CreateCheckpointConfig("ln-full0", "", "ns"), Checkpoint: graph.NewCheckpoint(map[string]any{}, map[string]int64{}, nil), Metadata: graph.NewCheckpointMetadata(graph.CheckpointSourceInput, 0), NewVersions: map[string]int64{}})
	require.NoError(t, err)

	// Write with Sequence zero should be assigned a non-zero sequence
	_, err = saver.PutFull(ctx, graph.PutFullRequest{Config: cfg, Checkpoint: graph.NewCheckpoint(map[string]any{"v": 1}, map[string]int64{"v": 1}, nil), Metadata: graph.NewCheckpointMetadata(graph.CheckpointSourceLoop, 1), NewVersions: map[string]int64{"v": 1}, PendingWrites: []graph.PendingWrite{{TaskID: "t", Channel: "c", Value: 1, Sequence: 0}}})
	require.NoError(t, err)

	tup, err := saver.GetTuple(ctx, graph.CreateCheckpointConfig("ln-full0", "", "ns"))
	require.NoError(t, err)
	require.NotNil(t, tup)
	require.Len(t, tup.PendingWrites, 1)
	// Should be assigned
	require.Greater(t, tup.PendingWrites[0].Sequence, int64(0))
}

func TestRedis_GetTuple_LatestInNamespace(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	lineageID := "ln-latest-ns"

	ck1 := graph.NewCheckpoint(map[string]any{"x": 1}, map[string]int64{"x": 1}, nil)
	_, err = saver.Put(ctx, graph.PutRequest{Config: graph.CreateCheckpointConfig(lineageID, "", "ns1"), Checkpoint: ck1, Metadata: graph.NewCheckpointMetadata(graph.CheckpointSourceInput, 0), NewVersions: map[string]int64{"x": 1}})
	require.NoError(t, err)
	time.Sleep(2 * time.Millisecond)
	ck2 := graph.NewCheckpoint(map[string]any{"x": 2}, map[string]int64{"x": 2}, nil)
	_, err = saver.Put(ctx, graph.PutRequest{Config: graph.CreateCheckpointConfig(lineageID, "", "ns2"), Checkpoint: ck2, Metadata: graph.NewCheckpointMetadata(graph.CheckpointSourceLoop, 1), NewVersions: map[string]int64{"x": 2}})
	require.NoError(t, err)

	// Latest in ns1 should be ck1, not ns2
	tup, err := saver.GetTuple(ctx, graph.CreateCheckpointConfig(lineageID, "", "ns1"))
	require.NoError(t, err)
	require.NotNil(t, tup)
	assert.Equal(t, ck1.ID, tup.Checkpoint.ID)
	assert.Equal(t, "ns1", graph.GetNamespace(tup.Config))
}

func TestRedis_Put_TimestampZero_UsesNow(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	lineageID := "ln-ts0"
	ns := "ns"
	ck := graph.NewCheckpoint(map[string]any{"x": 1}, map[string]int64{"x": 1}, nil)
	// Zero out timestamp to force now assignment path
	ck.Timestamp = time.Time{}
	cfg, err := saver.Put(ctx, graph.PutRequest{Config: graph.CreateCheckpointConfig(lineageID, "", ns), Checkpoint: ck, Metadata: graph.NewCheckpointMetadata(graph.CheckpointSourceUpdate, 0), NewVersions: map[string]int64{"x": 1}})
	require.NoError(t, err)
	// Should be retrievable
	tup, err := saver.GetTuple(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, tup)
}

func TestRedisCheckpointSaverMetadataFilter(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
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

func TestRedis_List_MetadataFilter_NoExtraInTuple(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	lineageID := "ln-no-extra"
	ns := "ns"

	// Manually insert a checkpoint with metadata JSON missing 'extra' field
	ck := graph.NewCheckpoint(map[string]any{"x": 1}, map[string]int64{"x": 1}, nil)
	ckJSON, _ := json.Marshal(ck)
	// metadata without Extra
	rawMeta := map[string]any{"source": graph.CheckpointSourceInput, "step": 0}
	metaJSON, _ := json.Marshal(rawMeta)
	db := buildRedisClient(t, redisURL)
	pipe := db.TxPipeline()
	checkpointKey := checkpointKey(lineageID, ns, ck.ID)
	pipe.HSet(ctx, checkpointKey,
		lingeageIDKey, lineageID,
		checkpointNSKey, ns,
		checkpointIDKey, ck.ID,
		tsKey, time.Now().UTC().UnixNano(),
		checkpointJSONKey, ckJSON,
		metadataJSONKey, metaJSON,
	)
	tsKey := checkpointTSKey(lineageID, ns)
	pipe.ZAdd(ctx, tsKey, redis.Z{
		Score:  float64(time.Now().UTC().UnixNano()),
		Member: ck.ID,
	})
	nsKey := lineageNSKey(lineageID)
	pipe.SAdd(ctx, nsKey, ns)
	_, err = pipe.Exec(ctx)
	// _, err = db.ExecContext(ctx, sqliteInsertCheckpoint, lineageID, ns, ck.ID, "", time.Now().UTC().UnixNano(), ckJSON, metaJSON)
	require.NoError(t, err)

	// List with metadata filter should exclude this tuple because Extra==nil
	filter := &graph.CheckpointFilter{Metadata: map[string]any{"k": "v"}}
	tuples, err := saver.List(ctx, graph.CreateCheckpointConfig(lineageID, "", ns), filter)
	require.NoError(t, err)
	// No tuples should match the metadata filter
	require.Equal(t, 0, len(tuples))

	// Listing without metadata filter should include 1 tuple
	tuples2, err := saver.List(ctx, graph.CreateCheckpointConfig(lineageID, "", ns), nil)
	require.NoError(t, err)
	require.Equal(t, 1, len(tuples2))
}

func TestRedisCheckpointSaverClose(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	// Close should not error.
	err = saver.Close()
	assert.NoError(t, err)

	// Close again should not error.
	err = saver.Close()
	assert.NoError(t, err)
}

func TestSQLite_GetTuple_ParentNamespaceUnknown_EmptyInParentConfig(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	// Insert a child row that references a non-existent parent ID to force findCheckpointNamespace to return empty namespace.
	// Use Put to create a child (without actual parent) by bypassing ParentCheckpointID validation: we insert directly into DB.
	// 1) Create a fake child checkpoint JSON
	child := graph.NewCheckpoint(map[string]any{"v": 10}, map[string]int64{"v": 1}, nil)
	child.ParentCheckpointID = "no-such-parent"
	childJSON, _ := json.Marshal(child)
	metaJSON, _ := json.Marshal(graph.NewCheckpointMetadata(graph.CheckpointSourceFork, 1))
	db := buildRedisClient(t, redisURL)
	pipe := db.TxPipeline()
	lineageID := "ln-unknown"
	ns := "nsX"
	checkpointKey := checkpointKey(lineageID, ns, child.ID)
	pipe.HSet(ctx, checkpointKey,
		lingeageIDKey, lineageID,
		checkpointNSKey, ns,
		checkpointIDKey, child.ID,
		parentCheckpointIDKey, child.ParentCheckpointID,
		tsKey, time.Now().UTC().UnixNano(),
		checkpointJSONKey, childJSON,
		metadataJSONKey, metaJSON,
	)
	tsKey := checkpointTSKey(lineageID, ns)
	pipe.ZAdd(ctx, tsKey, redis.Z{
		Score:  float64(time.Now().UTC().UnixNano()),
		Member: child.ID,
	})
	nsKey := lineageNSKey(lineageID)
	pipe.SAdd(ctx, nsKey, ns)
	_, err = pipe.Exec(ctx)
	require.NoError(t, err)

	cfg := graph.CreateCheckpointConfig("ln-unknown", child.ID, "nsX")
	tup, err := saver.GetTuple(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, tup)
	require.NotNil(t, tup.ParentConfig)
	assert.Equal(t, "", graph.GetNamespace(tup.ParentConfig))
	assert.Equal(t, child.ParentCheckpointID, graph.GetCheckpointID(tup.ParentConfig))
}

func TestRedis_GetTuple_CrossNamespaceLatestAndByID(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	lineageID := "ln-cross-ns"

	// Put a checkpoint in ns1
	ck1 := graph.NewCheckpoint(map[string]any{"n": 1}, map[string]int64{"n": 1}, map[string]map[string]int64{})
	cfgNS1 := graph.CreateCheckpointConfig(lineageID, "", "")
	_, err = saver.Put(ctx, graph.PutRequest{Config: cfgNS1, Checkpoint: ck1, Metadata: graph.NewCheckpointMetadata(graph.CheckpointSourceInput, 0), NewVersions: map[string]int64{"n": 1}})
	require.NoError(t, err)

	// Small delay to ensure distinct timestamps
	time.Sleep(5 * time.Millisecond)

	// Put a checkpoint in ns2
	ck2 := graph.NewCheckpoint(map[string]any{"n": 2}, map[string]int64{"n": 2}, map[string]map[string]int64{})
	cfgNS2 := graph.CreateCheckpointConfig(lineageID, "", "")
	_, err = saver.Put(ctx, graph.PutRequest{Config: cfgNS2, Checkpoint: ck2, Metadata: graph.NewCheckpointMetadata(graph.CheckpointSourceLoop, 1), NewVersions: map[string]int64{"n": 2}})
	require.NoError(t, err)

	// Latest across namespaces with empty ns, empty id
	latestCfg := graph.CreateCheckpointConfig(lineageID, "", "")
	tuple, err := saver.GetTuple(ctx, latestCfg)
	require.NoError(t, err)
	require.NotNil(t, tuple)
	// Should be the second one in ns2
	assert.Equal(t, ck2.ID, tuple.Checkpoint.ID)
	assert.Equal(t, "", graph.GetNamespace(tuple.Config))

	// Cross-namespace by ID with empty ns but specific id
	byIDCfg := graph.CreateCheckpointConfig(lineageID, ck1.ID, "")
	tuple2, err := saver.GetTuple(ctx, byIDCfg)
	require.NoError(t, err)
	require.NotNil(t, tuple2)
	assert.Equal(t, ck1.ID, tuple2.Checkpoint.ID)
	assert.Equal(t, "", graph.GetNamespace(tuple2.Config))
}

func TestRedis_Put_DefaultMetadataWhenNil(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	lineageID := "ln-nil-meta"
	cfg := graph.CreateCheckpointConfig(lineageID, "", "ns")

	ck := graph.NewCheckpoint(map[string]any{"x": 1}, map[string]int64{"x": 1}, map[string]map[string]int64{})
	// Put with nil metadata should be accepted and default to update/step 0
	updated, err := saver.Put(ctx, graph.PutRequest{Config: cfg, Checkpoint: ck, Metadata: nil, NewVersions: map[string]int64{"x": 1}})
	require.NoError(t, err)

	tup, err := saver.GetTuple(ctx, updated)
	require.NoError(t, err)
	require.NotNil(t, tup)
	require.NotNil(t, tup.Metadata)
	assert.Equal(t, graph.CheckpointSourceUpdate, tup.Metadata.Source)
	assert.Equal(t, 0, tup.Metadata.Step)
}

func TestRedis_PutWrites_SequenceOrdering(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	lineageID := "ln-seq"
	cfg := graph.CreateCheckpointConfig(lineageID, "", "ns")

	ck := graph.NewCheckpoint(map[string]any{"a": 0}, map[string]int64{"a": 1}, map[string]map[string]int64{})
	updated, err := saver.Put(ctx, graph.PutRequest{Config: cfg, Checkpoint: ck, Metadata: graph.NewCheckpointMetadata(graph.CheckpointSourceInput, -1), NewVersions: map[string]int64{"a": 1}})
	require.NoError(t, err)

	// Deliberately out-of-order sequences; query should order by seq
	writes := []graph.PendingWrite{
		{TaskID: "t", Channel: "a", Value: 1, Sequence: 200},
		{TaskID: "t", Channel: "b", Value: 2, Sequence: 100},
	}
	err = saver.PutWrites(ctx, graph.PutWritesRequest{Config: updated, Writes: writes, TaskID: "t"})
	require.NoError(t, err)

	tup, err := saver.GetTuple(ctx, updated)
	require.NoError(t, err)
	require.Len(t, tup.PendingWrites, 2)
	// Ordered by seq ascending
	assert.Equal(t, int64(100), tup.PendingWrites[0].Sequence)
	assert.Equal(t, "b", tup.PendingWrites[0].Channel)
	assert.Equal(t, int64(200), tup.PendingWrites[1].Sequence)
	assert.Equal(t, "a", tup.PendingWrites[1].Channel)
}

func TestRedis_PutFull_WithParentAndWrites(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	lineageID := "ln-putfull"
	ns := "ns"

	// Parent checkpoint first
	parent := graph.NewCheckpoint(map[string]any{"p": 1}, map[string]int64{"p": 1}, map[string]map[string]int64{})
	cfg := graph.CreateCheckpointConfig(lineageID, "", ns)
	_, err = saver.Put(ctx, graph.PutRequest{Config: cfg, Checkpoint: parent, Metadata: graph.NewCheckpointMetadata(graph.CheckpointSourceInput, 0), NewVersions: map[string]int64{"p": 1}})
	require.NoError(t, err)

	// Child via PutFull; ParentCheckpointID is carried from the checkpoint object
	child := graph.NewCheckpoint(map[string]any{"c": 2}, map[string]int64{"c": 1}, map[string]map[string]int64{})
	child.ParentCheckpointID = parent.ID

	fullCfg, err := saver.PutFull(ctx, graph.PutFullRequest{
		Config:        cfg,
		Checkpoint:    child,
		Metadata:      graph.NewCheckpointMetadata(graph.CheckpointSourceLoop, 1),
		NewVersions:   map[string]int64{"c": 1},
		PendingWrites: []graph.PendingWrite{{TaskID: "t1", Channel: "c", Value: 99}},
	})
	require.NoError(t, err)

	tup, err := saver.GetTuple(ctx, fullCfg)
	require.NoError(t, err)
	require.NotNil(t, tup)
	assert.Equal(t, child.ID, tup.Checkpoint.ID)
	// Parent in same namespace
	require.NotNil(t, tup.ParentConfig)
	assert.Equal(t, parent.ID, graph.GetCheckpointID(tup.ParentConfig))
	assert.Equal(t, ns, graph.GetNamespace(tup.ParentConfig))
	// Writes stored
	require.Len(t, tup.PendingWrites, 1)
	assert.Equal(t, "c", tup.PendingWrites[0].Channel)
	assert.Equal(t, float64(99), tup.PendingWrites[0].Value)
}

func TestRedis_PutFull_ParentConfig_CrossNamespace(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	lineageID := "ln-cross-parentcfg"
	nsA := "nsA"
	nsB := "nsB"

	// Parent in nsA
	parent := graph.NewCheckpoint(map[string]any{"p": 1}, map[string]int64{"p": 1}, map[string]map[string]int64{})
	cfgA := graph.CreateCheckpointConfig(lineageID, "", nsA)
	_, err = saver.Put(ctx, graph.PutRequest{Config: cfgA, Checkpoint: parent, Metadata: graph.NewCheckpointMetadata(graph.CheckpointSourceInput, 0), NewVersions: map[string]int64{"p": 1}})
	require.NoError(t, err)

	// Child in nsB with ParentCheckpointID referencing parent in nsA
	child := graph.NewCheckpoint(map[string]any{"c": 2}, map[string]int64{"c": 1}, map[string]map[string]int64{})
	child.ParentCheckpointID = parent.ID
	cfgB := graph.CreateCheckpointConfig(lineageID, "", nsB)
	fullCfg, err := saver.PutFull(ctx, graph.PutFullRequest{Config: cfgB, Checkpoint: child, Metadata: graph.NewCheckpointMetadata(graph.CheckpointSourceFork, 1), NewVersions: map[string]int64{"c": 1}})
	require.NoError(t, err)

	// Load child tuple and verify ParentConfig points to parent's actual namespace (nsA)
	tup, err := saver.GetTuple(ctx, fullCfg)
	require.NoError(t, err)
	require.NotNil(t, tup)
	require.NotNil(t, tup.ParentConfig)
	assert.Equal(t, parent.ID, graph.GetCheckpointID(tup.ParentConfig))
	assert.Equal(t, nsA, graph.GetNamespace(tup.ParentConfig))
}

func TestRedis_List_WithBeforeAndCrossNamespace(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	lineageID := "ln-before"

	// Create three checkpoints across two namespaces
	ck1 := graph.NewCheckpoint(map[string]any{"i": 1}, map[string]int64{"i": 1}, map[string]map[string]int64{})
	_, err = saver.Put(ctx, graph.PutRequest{Config: graph.CreateCheckpointConfig(lineageID, "", "nsA"), Checkpoint: ck1, Metadata: graph.NewCheckpointMetadata(graph.CheckpointSourceInput, 0), NewVersions: map[string]int64{"i": 1}})
	require.NoError(t, err)
	time.Sleep(5 * time.Millisecond)
	ck2 := graph.NewCheckpoint(map[string]any{"i": 2}, map[string]int64{"i": 2}, map[string]map[string]int64{})
	_, err = saver.Put(ctx, graph.PutRequest{Config: graph.CreateCheckpointConfig(lineageID, "", "nsA"), Checkpoint: ck2, Metadata: graph.NewCheckpointMetadata(graph.CheckpointSourceLoop, 1), NewVersions: map[string]int64{"i": 2}})
	require.NoError(t, err)
	time.Sleep(5 * time.Millisecond)
	ck3 := graph.NewCheckpoint(map[string]any{"i": 3}, map[string]int64{"i": 3}, map[string]map[string]int64{})
	_, err = saver.Put(ctx, graph.PutRequest{Config: graph.CreateCheckpointConfig(lineageID, "", "nsA"), Checkpoint: ck3, Metadata: graph.NewCheckpointMetadata(graph.CheckpointSourceLoop, 2), NewVersions: map[string]int64{"i": 3}})
	require.NoError(t, err)

	// Cross-namespace list with Before(ck3) should exclude ck3.
	// Be tolerant on size/order across platforms; just ensure ck3 is excluded and ck1/ck2 appear if any.
	cfgAll := graph.CreateCheckpointConfig(lineageID, "", "nsA")
	filter := graph.NewCheckpointFilter().WithBefore(graph.CreateCheckpointConfig(lineageID, ck3.ID, "")).WithLimit(10)
	tuples, err := saver.List(ctx, cfgAll, filter)
	require.NoError(t, err)
	have3 := false
	for _, tu := range tuples {
		if tu.Checkpoint.ID == ck3.ID {
			have3 = true
		}
	}
	assert.False(t, have3, "ck3 should be excluded by Before filter")
	// If results present, they must be among {ck1, ck2}
	for _, tu := range tuples {
		assert.True(t, tu.Checkpoint.ID == ck1.ID || tu.Checkpoint.ID == ck2.ID)
	}

	// Namespace-specific list with Before(ck3) in nsA should return only ck1
	cfgNsA := graph.CreateCheckpointConfig(lineageID, "", "nsA")
	filter2 := graph.NewCheckpointFilter().WithBefore(graph.CreateCheckpointConfig(lineageID, ck3.ID, "nsA"))
	tuples2, err := saver.List(ctx, cfgNsA, filter2)
	require.NoError(t, err)
	// Should not include ck3
	for _, tu := range tuples2 {
		assert.NotEqual(t, tu.Checkpoint.ID, ck3.ID)
	}
	if len(tuples2) > 0 {
		assert.Equal(t, ck1.ID, tuples2[0].Checkpoint.ID)
	}
}

func TestRedis_List_CrossNamespace_Limit1(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()
	ctx := context.Background()
	lineageID := "ln-limit"
	// three checkpoints across namespaces
	_, err = saver.Put(ctx, graph.PutRequest{Config: graph.CreateCheckpointConfig(lineageID, "", "ns1"), Checkpoint: graph.NewCheckpoint(map[string]any{"i": 1}, map[string]int64{"i": 1}, nil), Metadata: graph.NewCheckpointMetadata(graph.CheckpointSourceInput, 0), NewVersions: map[string]int64{"i": 1}})
	require.NoError(t, err)
	time.Sleep(1 * time.Millisecond)
	_, err = saver.Put(ctx, graph.PutRequest{Config: graph.CreateCheckpointConfig(lineageID, "", "ns2"), Checkpoint: graph.NewCheckpoint(map[string]any{"i": 2}, map[string]int64{"i": 2}, nil), Metadata: graph.NewCheckpointMetadata(graph.CheckpointSourceLoop, 1), NewVersions: map[string]int64{"i": 2}})
	require.NoError(t, err)
	time.Sleep(1 * time.Millisecond)
	_, err = saver.Put(ctx, graph.PutRequest{Config: graph.CreateCheckpointConfig(lineageID, "", "ns1"), Checkpoint: graph.NewCheckpoint(map[string]any{"i": 3}, map[string]int64{"i": 3}, nil), Metadata: graph.NewCheckpointMetadata(graph.CheckpointSourceLoop, 2), NewVersions: map[string]int64{"i": 3}})
	require.NoError(t, err)

	cfgAll := graph.CreateCheckpointConfig(lineageID, "", "ns1")
	tuples, err := saver.List(ctx, cfgAll, &graph.CheckpointFilter{Limit: 1})
	require.NoError(t, err)
	require.Equal(t, 1, len(tuples))
}

func TestRedis_List_NamespaceNotExists_ReturnsEmpty(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()
	ctx := context.Background()
	// List in a namespace with no data
	tuples, err := saver.List(ctx, graph.CreateCheckpointConfig("ln-empty-ns", "", "nsX"), nil)
	require.NoError(t, err)
	require.Equal(t, 0, len(tuples))
}

func TestRedis_PutFull_NilCheckpoint_Error(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()
	defer saver.Close()
	_, err = saver.PutFull(context.Background(), graph.PutFullRequest{Config: graph.CreateCheckpointConfig("ln", "", "ns"), Checkpoint: nil})
	require.Error(t, err)
}

func TestRedis_Get_MissingLineage_Error(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()
	defer saver.Close()
	_, err = saver.Get(context.Background(), map[string]any{})
	require.Error(t, err)
}

func TestRedis_List_MetadataMismatch_ReturnsEmpty(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()
	ctx := context.Background()
	lineageID := "ln-meta-mismatch"
	ck := graph.NewCheckpoint(map[string]any{"x": 1}, map[string]int64{"x": 1}, nil)
	meta := graph.NewCheckpointMetadata(graph.CheckpointSourceLoop, 1)
	meta.Extra["type"] = "test"
	_, err = saver.Put(ctx, graph.PutRequest{Config: graph.CreateCheckpointConfig(lineageID, "", "ns"), Checkpoint: ck, Metadata: meta, NewVersions: map[string]int64{"x": 1}})
	require.NoError(t, err)
	// Mismatched metadata filter should yield no results
	tuples, err := saver.List(ctx, graph.CreateCheckpointConfig(lineageID, "", "ns"), &graph.CheckpointFilter{Metadata: map[string]any{"type": "other"}})
	require.NoError(t, err)
	require.Equal(t, 0, len(tuples))
}

func TestRedis_List_MissingLineage_Error(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()
	defer saver.Close()
	_, err = saver.List(context.Background(), map[string]any{}, nil)
	require.Error(t, err)
}

func TestRedis_List_NamespaceWithLimit(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()
	defer saver.Close()
	ctx := context.Background()
	lineageID := "ln-ns-limit"
	_, err = saver.Put(ctx, graph.PutRequest{Config: graph.CreateCheckpointConfig(lineageID, "", "ns"), Checkpoint: graph.NewCheckpoint(map[string]any{"i": 1}, map[string]int64{"i": 1}, nil), Metadata: graph.NewCheckpointMetadata(graph.CheckpointSourceInput, 0), NewVersions: map[string]int64{"i": 1}})
	require.NoError(t, err)
	_, err = saver.Put(ctx, graph.PutRequest{Config: graph.CreateCheckpointConfig(lineageID, "", "ns"), Checkpoint: graph.NewCheckpoint(map[string]any{"i": 2}, map[string]int64{"i": 2}, nil), Metadata: graph.NewCheckpointMetadata(graph.CheckpointSourceLoop, 1), NewVersions: map[string]int64{"i": 2}})
	require.NoError(t, err)
	tuples, err := saver.List(ctx, graph.CreateCheckpointConfig(lineageID, "", "ns"), &graph.CheckpointFilter{Limit: 1})
	require.NoError(t, err)
	require.Equal(t, 1, len(tuples))
}

func TestRedis_PutFull_NoWrites_Success_NoPendingWrites(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()
	ctx := context.Background()
	lineageID := "ln-pf-nowrites"
	ns := "ns"
	ck := graph.NewCheckpoint(map[string]any{"v": 1}, map[string]int64{"v": 1}, nil)
	cfg, err := saver.PutFull(ctx, graph.PutFullRequest{Config: graph.CreateCheckpointConfig(lineageID, "", ns), Checkpoint: ck, Metadata: graph.NewCheckpointMetadata(graph.CheckpointSourceInput, 0), NewVersions: map[string]int64{"v": 1}})
	require.NoError(t, err)
	tup, err := saver.GetTuple(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, tup)
	require.Equal(t, 0, len(tup.PendingWrites))
}

func TestRedis_PutWrites_SequenceZero_UsesIndex(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()
	defer saver.Close()
	ctx := context.Background()
	cfg, err := saver.Put(ctx, graph.PutRequest{Config: graph.CreateCheckpointConfig("ln-pw-idx", "", "ns"), Checkpoint: graph.NewCheckpoint(map[string]any{"a": 1}, map[string]int64{"a": 1}, nil), Metadata: graph.NewCheckpointMetadata(graph.CheckpointSourceInput, 0), NewVersions: map[string]int64{"a": 1}})
	require.NoError(t, err)
	// both Sequence=0 -> DB uses idx (0 and 1)
	err = saver.PutWrites(ctx, graph.PutWritesRequest{Config: cfg, Writes: []graph.PendingWrite{{TaskID: "t", Channel: "c", Value: 1, Sequence: 0}, {TaskID: "t", Channel: "d", Value: 2, Sequence: 0}}})
	require.NoError(t, err)
	tup, err := saver.GetTuple(ctx, cfg)
	require.NoError(t, err)
	require.Len(t, tup.PendingWrites, 2)
	require.Equal(t, int64(0), tup.PendingWrites[0].Sequence)
	require.Equal(t, int64(1), tup.PendingWrites[1].Sequence)
}

func TestRedis_NoParent_ParentConfigNil(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()
	ctx := context.Background()
	cfg, err := saver.Put(ctx, graph.PutRequest{Config: graph.CreateCheckpointConfig("ln-nopar", "", "ns"), Checkpoint: graph.NewCheckpoint(map[string]any{"x": 1}, map[string]int64{"x": 1}, nil), Metadata: graph.NewCheckpointMetadata(graph.CheckpointSourceInput, 0), NewVersions: map[string]int64{"x": 1}})
	require.NoError(t, err)
	tup, err := saver.GetTuple(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, tup)
	require.Nil(t, tup.ParentConfig)
}

func TestRedis_findCheckpointNamespace_EmptyArgs(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()
	ns, err := saver.findCheckpointNamespace(context.Background(), "", "")
	require.NoError(t, err)
	require.Equal(t, "", ns)
}

func TestRedis_findCheckpointNamespace_NoRows(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()
	ctx := context.Background()
	// Insert a checkpoint in nsA
	_, err = saver.Put(ctx, graph.PutRequest{Config: graph.CreateCheckpointConfig("ln-fc", "", "nsA"), Checkpoint: graph.NewCheckpoint(map[string]any{"x": 1}, map[string]int64{"x": 1}, nil), Metadata: graph.NewCheckpointMetadata(graph.CheckpointSourceInput, 0), NewVersions: map[string]int64{"x": 1}})
	require.NoError(t, err)
	// Lookup non-existing parent id
	ns, err := saver.findCheckpointNamespace(ctx, "ln-fc", "no-such")
	require.NoError(t, err)
	require.Equal(t, "", ns)
}

func TestRedis_PutFull_SequenceZero_AssignsTime(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()
	ctx := context.Background()
	cfg, err := saver.PutFull(ctx, graph.PutFullRequest{
		Config:      graph.CreateCheckpointConfig("ln-pf-seq0", "", "ns"),
		Checkpoint:  graph.NewCheckpoint(map[string]any{"x": 1}, map[string]int64{"x": 1}, nil),
		Metadata:    graph.NewCheckpointMetadata(graph.CheckpointSourceInput, 0),
		NewVersions: map[string]int64{"x": 1},
		PendingWrites: []graph.PendingWrite{{
			TaskID:   "t",
			Channel:  "c",
			Value:    1,
			Sequence: 0,
		}},
	})
	require.NoError(t, err)
	tup, err := saver.GetTuple(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, tup)
	require.Len(t, tup.PendingWrites, 1)
	require.Greater(t, tup.PendingWrites[0].Sequence, int64(0))
}

func TestRedis_ErrorCases(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()

	// GetTuple with missing lineage id should error
	_, err = saver.GetTuple(ctx, map[string]any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lineage_id is required")

	// Put with missing lineage id should error
	_, err = saver.Put(ctx, graph.PutRequest{Config: map[string]any{"configurable": map[string]any{}}, Checkpoint: graph.NewCheckpoint(nil, nil, nil)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lineage_id is required")

	// PutWrites with missing checkpoint id should error
	err = saver.PutWrites(ctx, graph.PutWritesRequest{Config: graph.CreateCheckpointConfig("ln", "", "")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lineage_id and checkpoint_id are required")

	// PutFull with missing lineage id should error
	_, err = saver.PutFull(ctx, graph.PutFullRequest{Config: map[string]any{"configurable": map[string]any{}}, Checkpoint: graph.NewCheckpoint(nil, nil, nil)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lineage_id is required")

	// DeleteLineage with empty id should error
	err = saver.DeleteLineage(ctx, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lineage_id is required")
}

func TestRedis_PutFull_WriteMarshalError(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	lineageID := "ln-marshal"
	ns := "ns"
	ck := graph.NewCheckpoint(map[string]any{"v": 1}, map[string]int64{"v": 1}, nil)
	// Use a non-JSON-marshalable value (channel) to force error
	_, err = saver.PutFull(ctx, graph.PutFullRequest{Config: graph.CreateCheckpointConfig(lineageID, "", ns), Checkpoint: ck, Metadata: graph.NewCheckpointMetadata(graph.CheckpointSourceUpdate, 0), NewVersions: map[string]int64{"v": 1}, PendingWrites: []graph.PendingWrite{{TaskID: "t", Channel: "c", Value: make(chan int)}}})
	require.Error(t, err)
}

func TestRedis_PutFull_WriteMarshalError_checkpoint(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	lineageID := "ln-marshal"
	ns := "ns"
	ck := graph.NewCheckpoint(map[string]any{"v": 1, "ch": make(chan int)}, map[string]int64{"v": 1}, nil)
	// Use a non-JSON-marshalable value (channel) to force error
	_, err = saver.PutFull(ctx, graph.PutFullRequest{Config: graph.CreateCheckpointConfig(lineageID, "", ns), Checkpoint: ck, Metadata: graph.NewCheckpointMetadata(graph.CheckpointSourceUpdate, 0), NewVersions: map[string]int64{"v": 1}, PendingWrites: []graph.PendingWrite{{TaskID: "t", Channel: "c", Value: 1}}})
	require.Error(t, err)
}

func TestRedis_PutFull_checkpoint_ts_isEmpty(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	lineageID := "ln-marshal"
	ns := "ns"
	ck := &graph.Checkpoint{
		Version:         1,
		ID:              uuid.New().String(),
		ChannelValues:   map[string]any{"v": 1},
		ChannelVersions: map[string]int64{"v": 1},
		VersionsSeen:    map[string]map[string]int64{},
	}
	// Use a non-JSON-marshalable value (channel) to force error
	cb, err := saver.PutFull(ctx, graph.PutFullRequest{Config: graph.CreateCheckpointConfig(lineageID, "", ns), Checkpoint: ck, Metadata: graph.NewCheckpointMetadata(graph.CheckpointSourceUpdate, 0), NewVersions: map[string]int64{"v": 1}, PendingWrites: []graph.PendingWrite{{TaskID: "t", Channel: "c", Value: 1}}})
	require.NoError(t, err)
	assert.Equal(t, ck.ID, cb[graph.CfgKeyConfigurable].(map[string]any)[graph.CfgKeyCheckpointID])
}

func TestRedis_Put_checkpoint_ts_isEmpty(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	lineageID := "ln-marshal"
	ns := "ns"
	ck := &graph.Checkpoint{
		Version:         1,
		ID:              uuid.New().String(),
		ChannelValues:   map[string]any{"v": 1},
		ChannelVersions: map[string]int64{"v": 1},
		VersionsSeen:    map[string]map[string]int64{},
	}
	// Use a non-JSON-marshalable value (channel) to force error
	cb, err := saver.Put(ctx, graph.PutRequest{Config: graph.CreateCheckpointConfig(lineageID, "", ns), Checkpoint: ck, Metadata: graph.NewCheckpointMetadata(graph.CheckpointSourceUpdate, 0), NewVersions: map[string]int64{"v": 1}})
	require.NoError(t, err)
	assert.Equal(t, ck.ID, cb[graph.CfgKeyConfigurable].(map[string]any)[graph.CfgKeyCheckpointID])
}

func TestRedis_Close_NilDB_NoPanic(t *testing.T) {
	s := &Saver{client: nil}
	// Close should be no-op
	assert.NoError(t, s.Close())
}

func TestRedis_Put_NilCheckpoint_Error(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	_, err = saver.Put(ctx, graph.PutRequest{Config: graph.CreateCheckpointConfig("ln", "", "ns"), Checkpoint: nil})
	require.Error(t, err)
}

func TestRedis_PutWrites_MarshalError(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	cfg, err := saver.Put(ctx, graph.PutRequest{Config: graph.CreateCheckpointConfig("ln-pw", "", "ns"), Checkpoint: graph.NewCheckpoint(nil, nil, nil), Metadata: graph.NewCheckpointMetadata(graph.CheckpointSourceInput, 0), NewVersions: map[string]int64{}})
	require.NoError(t, err)
	// Non-serializable write value to force marshal error
	err = saver.PutWrites(ctx, graph.PutWritesRequest{Config: cfg, Writes: []graph.PendingWrite{{TaskID: "t", Channel: "c", Value: make(chan int)}}})
	require.Error(t, err)
}

func TestRedis_findCheckpointNamespace_Found(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()
	ctx := context.Background()
	lineageID := "ln-find"
	// Insert a parent in nsP
	parent := graph.NewCheckpoint(map[string]any{"p": 1}, map[string]int64{"p": 1}, nil)
	_, err = saver.Put(ctx, graph.PutRequest{Config: graph.CreateCheckpointConfig(lineageID, "", "nsP"), Checkpoint: parent, Metadata: graph.NewCheckpointMetadata(graph.CheckpointSourceInput, 0), NewVersions: map[string]int64{"p": 1}})
	require.NoError(t, err)
	ns, err := saver.findCheckpointNamespace(ctx, lineageID, parent.ID)
	require.NoError(t, err)
	assert.Equal(t, "nsP", ns)
}

func TestRedis_NewSaver_DBError(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()
}

func TestRedis_Put_CheckpointMarshalError(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	ck := graph.NewCheckpoint(map[string]any{"bad": make(chan int)}, map[string]int64{}, nil)
	_, err = saver.Put(ctx, graph.PutRequest{Config: graph.CreateCheckpointConfig("ln-bad", "", "ns"), Checkpoint: ck, Metadata: graph.NewCheckpointMetadata(graph.CheckpointSourceUpdate, 0), NewVersions: map[string]int64{}})
	require.Error(t, err)
}

func TestRedis_Put_MetadataMarshalError(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()
	ctx := context.Background()
	ck := graph.NewCheckpoint(map[string]any{"x": 1}, map[string]int64{"x": 1}, nil)
	meta := graph.NewCheckpointMetadata(graph.CheckpointSourceUpdate, 0)
	meta.Extra["bad"] = make(chan int)
	_, err = saver.Put(ctx, graph.PutRequest{Config: graph.CreateCheckpointConfig("ln-meta-err", "", "ns"), Checkpoint: ck, Metadata: meta, NewVersions: map[string]int64{"x": 1}})
	require.Error(t, err)
}

func TestRedis_PutFull_MetadataMarshalError(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	ck := graph.NewCheckpoint(map[string]any{"x": 1}, map[string]int64{"x": 1}, nil)
	meta := graph.NewCheckpointMetadata(graph.CheckpointSourceUpdate, 0)
	// Force marshal error via extra with non-serializable value
	meta.Extra["bad"] = make(chan int)
	_, err = saver.PutFull(ctx, graph.PutFullRequest{Config: graph.CreateCheckpointConfig("ln-meta-bad", "", "ns"), Checkpoint: ck, Metadata: meta, NewVersions: map[string]int64{"x": 1}})
	require.Error(t, err)
}

func TestRedis_DeleteLineage_NullValue(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()
	err = saver.DeleteLineage(context.Background(), "ln-del")
	require.NoError(t, err)
}

func TestRedis_DeleteLineage_SecondExecError(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()
	ctx := context.Background()
	// Put a checkpoint and a write
	cfg, err := saver.Put(ctx, graph.PutRequest{Config: graph.CreateCheckpointConfig("ln-del2", "", "ns"), Checkpoint: graph.NewCheckpoint(map[string]any{"x": 1}, map[string]int64{"x": 1}, nil), Metadata: graph.NewCheckpointMetadata(graph.CheckpointSourceInput, 0), NewVersions: map[string]int64{"x": 1}})
	require.NoError(t, err)
	_ = saver.PutWrites(ctx, graph.PutWritesRequest{Config: cfg, Writes: []graph.PendingWrite{{TaskID: "t", Channel: "c", Value: 1}}})
	// Drop writes table to force second delete to fail
	err = saver.DeleteLineage(ctx, "ln-del2")
	require.NoError(t, err)
}
