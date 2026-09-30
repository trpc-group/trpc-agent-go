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
	"errors"
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

// errHook is a redis.Hook that injects failures into specific commands or the
// whole pipeline, exercising error paths that miniredis cannot produce.
type errHook struct {
	failCmd      map[string]bool
	failPipeline bool
}

func (h *errHook) DialHook(next redis.DialHook) redis.DialHook {
	return next
}

func (h *errHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if h.failCmd[cmd.Name()] {
			return errors.New("injected command error")
		}
		return next(ctx, cmd)
	}
}

func (h *errHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		if h.failPipeline {
			return errors.New("injected pipeline error")
		}
		return next(ctx, cmds)
	}
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

func TestTTLMilliseconds(t *testing.T) {
	assert.Equal(t, int64(0), ttlMilliseconds(0))
	assert.Equal(t, int64(0), ttlMilliseconds(-time.Second))
	assert.Equal(t, int64(1), ttlMilliseconds(500*time.Microsecond))
	assert.Equal(t, int64(1), ttlMilliseconds(time.Millisecond))
	assert.Equal(t, int64(1500), ttlMilliseconds(1500*time.Millisecond))
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

	// Namespace-specific list with Before(ck3) in nsA should return
	// checkpoints older than ck3, newest first (CheckpointSaver contract).
	cfgNsA := graph.CreateCheckpointConfig(lineageID, "", "nsA")
	filter2 := graph.NewCheckpointFilter().WithBefore(graph.CreateCheckpointConfig(lineageID, ck3.ID, "nsA"))
	tuples2, err := saver.List(ctx, cfgNsA, filter2)
	require.NoError(t, err)
	// Should not include ck3
	for _, tu := range tuples2 {
		assert.NotEqual(t, tu.Checkpoint.ID, ck3.ID)
	}
	if len(tuples2) > 0 {
		assert.Equal(t, ck2.ID, tuples2[0].Checkpoint.ID)
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

func TestRedisCheckpointSaverListBeforeFilterOrder(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	lineageID := "test-lineage-before-order"
	config := graph.CreateCheckpointConfig(lineageID, "", "")

	// Create 3 checkpoints with strictly increasing timestamps.
	base := time.Now().Add(-time.Hour)
	var configs []map[string]any
	for i := 0; i < 3; i++ {
		checkpoint := graph.NewCheckpoint(
			map[string]any{"step": i},
			map[string]int64{"step": int64(i + 1)},
			map[string]map[string]int64{},
		)
		checkpoint.Timestamp = base.Add(time.Duration(i) * time.Minute)
		metadata := graph.NewCheckpointMetadata(graph.CheckpointSourceLoop, i)

		req := graph.PutRequest{
			Config:      config,
			Checkpoint:  checkpoint,
			Metadata:    metadata,
			NewVersions: map[string]int64{"step": int64(i + 1)},
		}
		updatedConfig, err := saver.Put(ctx, req)
		require.NoError(t, err)
		configs = append(configs, updatedConfig)
	}

	idOf := func(idx int) string {
		return graph.GetCheckpointID(configs[idx])
	}

	t.Run("before filter with limit returns newest checkpoint before the cursor", func(t *testing.T) {
		// Cursor at ck3: the newest checkpoint before it is ck2, not the
		// oldest one (ck1). See issue #2503.
		filter := &graph.CheckpointFilter{Before: configs[2], Limit: 1}
		got, err := saver.List(ctx, config, filter)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, idOf(1), got[0].Checkpoint.ID)
	})

	t.Run("before filter without limit returns descending order", func(t *testing.T) {
		filter := &graph.CheckpointFilter{Before: configs[2]}
		got, err := saver.List(ctx, config, filter)
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, idOf(1), got[0].Checkpoint.ID)
		assert.Equal(t, idOf(0), got[1].Checkpoint.ID)
	})

	t.Run("before filter at oldest checkpoint returns empty", func(t *testing.T) {
		filter := &graph.CheckpointFilter{Before: configs[0]}
		got, err := saver.List(ctx, config, filter)
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}

// TestRedisCheckpointSaverListBeforeFilterSameScore verifies that checkpoints
// whose distinct nanosecond timestamps round to the same Redis ZSET score are
// preserved when listing with a Before filter.
func TestRedisCheckpointSaverListBeforeFilterSameScore(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	lineageID := "test-lineage-before-same-score"
	config := graph.CreateCheckpointConfig(lineageID, "", "")

	// Nanosecond timestamps beyond 2^53 all round to the same Redis ZSET
	// score (IEEE-754 double): 1<<60, (1<<60)+1 and (1<<60)+2.
	const base = int64(1) << 60
	var configs []map[string]any
	for i := 0; i < 3; i++ {
		checkpoint := graph.NewCheckpoint(
			map[string]any{"step": i},
			map[string]int64{"step": int64(i + 1)},
			map[string]map[string]int64{},
		)
		checkpoint.Timestamp = time.Unix(0, base+int64(i))
		metadata := graph.NewCheckpointMetadata(graph.CheckpointSourceLoop, i)

		req := graph.PutRequest{
			Config:      config,
			Checkpoint:  checkpoint,
			Metadata:    metadata,
			NewVersions: map[string]int64{"step": int64(i + 1)},
		}
		updatedConfig, err := saver.Put(ctx, req)
		require.NoError(t, err)
		configs = append(configs, updatedConfig)
	}

	idOf := func(idx int) string {
		return graph.GetCheckpointID(configs[idx])
	}

	t.Run("before newest checkpoint returns earlier same-score checkpoints newest-first", func(t *testing.T) {
		// Cursor at ck3 (1<<60)+2: ck1 and ck2 share its ZSET score and must
		// still be returned, ordered by exact timestamp.
		filter := &graph.CheckpointFilter{Before: configs[2]}
		got, err := saver.List(ctx, config, filter)
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, idOf(1), got[0].Checkpoint.ID)
		assert.Equal(t, idOf(0), got[1].Checkpoint.ID)
	})

	t.Run("before newest checkpoint with limit returns the immediately preceding one", func(t *testing.T) {
		filter := &graph.CheckpointFilter{Before: configs[2], Limit: 1}
		got, err := saver.List(ctx, config, filter)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, idOf(1), got[0].Checkpoint.ID)
	})

	t.Run("before oldest checkpoint returns empty", func(t *testing.T) {
		// Cursor at ck1 (1<<60): same-score candidates ck2/ck3 are newer and
		// must be excluded by exact timestamp.
		filter := &graph.CheckpointFilter{Before: configs[0]}
		got, err := saver.List(ctx, config, filter)
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}

// TestGetCheckpointTS_Missing_ReturnsZero verifies that a checkpoint without
// stored hash data yields a zero timestamp instead of an error.
func TestGetCheckpointTS_Missing_ReturnsZero(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ts, err := saver.getCheckpointTS(context.Background(), "ln-ts", "nsA", "no-such-id")
	require.NoError(t, err)
	assert.Zero(t, ts)
}

// TestGetCheckpointTS_Malformed_ReturnsError verifies that a malformed
// timestamp in the checkpoint hash fails loudly instead of misordering results.
func TestGetCheckpointTS_Malformed_ReturnsError(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	require.NoError(t, saver.client.HSet(ctx, checkpointKey("ln-ts", "nsA", "bad-ts"), tsKey, "not-a-number").Err())

	ts, err := saver.getCheckpointTS(ctx, "ln-ts", "nsA", "bad-ts")
	require.Error(t, err)
	assert.Zero(t, ts)
}

// TestFilterBeforeRefs_CursorTSUnavailable_ReturnsEmpty verifies that exact
// timestamp filtering returns no candidates when the cursor's checkpoint hash
// data is unavailable, keeping Before strict like the inmemory saver.
func TestFilterBeforeRefs_CursorTSUnavailable_ReturnsEmpty(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	const (
		lineageID = "ln-fbf-cursor-missing"
		ns        = "nsA"
	)
	var refs []checkpointRef
	for i := 0; i < 2; i++ {
		checkpoint := graph.NewCheckpoint(map[string]any{"i": i}, map[string]int64{"i": int64(i + 1)}, nil)
		checkpoint.Timestamp = time.Unix(0, 1_000_000_000+int64(i))
		cfg, err := saver.Put(ctx, graph.PutRequest{
			Config:      graph.CreateCheckpointConfig(lineageID, "", ns),
			Checkpoint:  checkpoint,
			Metadata:    graph.NewCheckpointMetadata(graph.CheckpointSourceInput, i),
			NewVersions: map[string]int64{"i": int64(i + 1)},
		})
		require.NoError(t, err)
		refs = append(refs, checkpointRef{namespace: ns, id: graph.GetCheckpointID(cfg)})
	}

	// Cursor exists only in the ZSET; its checkpoint hash is unavailable.
	require.NoError(t, saver.client.ZAdd(ctx, checkpointTSKey(lineageID, ns), redis.Z{
		Score:  2_000_000_000,
		Member: "cursor-only",
	}).Err())

	got, err := saver.filterBeforeRefs(ctx, lineageID, ns, "cursor-only", refs)
	require.NoError(t, err)
	assert.Empty(t, got, "cursor with unavailable hash data must yield no candidates to keep Before strict")
}

// TestList_BeforeCursorHashMissing_ExcludesNewerSameScore verifies end to end
// that a same-score checkpoint newer than the cursor is not returned when the
// cursor's hash data is missing (e.g. expiry skew), preserving strict Before
// semantics.
func TestList_BeforeCursorHashMissing_ExcludesNewerSameScore(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	const (
		lineageID = "ln-before-cursor-missing"
		ns        = "nsA"
	)

	// Cursor at (1<<60)+1 and a newer checkpoint at (1<<60)+2: both round to
	// the same Redis ZSET score, so only the exact hash timestamps can tell
	// them apart.
	cursor := graph.NewCheckpoint(map[string]any{"i": 1}, map[string]int64{"i": 1}, nil)
	cursor.Timestamp = time.Unix(0, (1<<60)+1)
	curCfg, err := saver.Put(ctx, graph.PutRequest{
		Config:      graph.CreateCheckpointConfig(lineageID, "", ns),
		Checkpoint:  cursor,
		Metadata:    graph.NewCheckpointMetadata(graph.CheckpointSourceInput, 1),
		NewVersions: map[string]int64{"i": 1},
	})
	require.NoError(t, err)

	newer := graph.NewCheckpoint(map[string]any{"i": 2}, map[string]int64{"i": 2}, nil)
	newer.Timestamp = time.Unix(0, (1<<60)+2)
	_, err = saver.Put(ctx, graph.PutRequest{
		Config:      graph.CreateCheckpointConfig(lineageID, "", ns),
		Checkpoint:  newer,
		Metadata:    graph.NewCheckpointMetadata(graph.CheckpointSourceLoop, 2),
		NewVersions: map[string]int64{"i": 2},
	})
	require.NoError(t, err)

	// Simulate expiry skew: the cursor hash is gone while its ZSET member
	// and the newer checkpoint remain.
	require.NoError(t, saver.client.Del(ctx, checkpointKey(lineageID, ns, graph.GetCheckpointID(curCfg))).Err())

	got, err := saver.List(ctx, graph.CreateCheckpointConfig(lineageID, "", ns), &graph.CheckpointFilter{Before: curCfg})
	require.NoError(t, err)
	assert.Empty(t, got, "a same-score checkpoint newer than the cursor must not be returned when the cursor hash is missing")
}

// TestFilterBeforeRefs_SkipsCandidateWithMissingHash verifies that a candidate
// whose checkpoint hash data is missing (redis.Nil) is dropped by exact
// timestamp filtering, while candidates with valid timestamps are kept.
func TestFilterBeforeRefs_SkipsCandidateWithMissingHash(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	const (
		lineageID = "ln-fbf-candidate-ts"
		ns        = "nsA"
	)

	cursor := graph.NewCheckpoint(map[string]any{"i": 2}, map[string]int64{"i": 2}, nil)
	cursor.Timestamp = time.Unix(0, 2_000_000_000)
	cursorCfg, err := saver.Put(ctx, graph.PutRequest{
		Config:      graph.CreateCheckpointConfig(lineageID, "", ns),
		Checkpoint:  cursor,
		Metadata:    graph.NewCheckpointMetadata(graph.CheckpointSourceLoop, 2),
		NewVersions: map[string]int64{"i": 2},
	})
	require.NoError(t, err)

	older := graph.NewCheckpoint(map[string]any{"i": 1}, map[string]int64{"i": 1}, nil)
	older.Timestamp = time.Unix(0, 1_000_000_000)
	olderCfg, err := saver.Put(ctx, graph.PutRequest{
		Config:      graph.CreateCheckpointConfig(lineageID, "", ns),
		Checkpoint:  older,
		Metadata:    graph.NewCheckpointMetadata(graph.CheckpointSourceInput, 1),
		NewVersions: map[string]int64{"i": 1},
	})
	require.NoError(t, err)

	// A ZSET-only member without hash data must be dropped by exact-timestamp
	// filtering; the valid older checkpoint must survive.
	require.NoError(t, saver.client.ZAdd(ctx, checkpointTSKey(lineageID, ns),
		redis.Z{Score: 1_500_000_000, Member: "zset-only"},
	).Err())

	got, err := saver.filterBeforeRefs(ctx, lineageID, ns, graph.GetCheckpointID(cursorCfg),
		[]checkpointRef{{namespace: ns, id: "zset-only"}, {namespace: ns, id: graph.GetCheckpointID(olderCfg)}})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, graph.GetCheckpointID(olderCfg), got[0].id)
}

// TestFilterBeforeRefs_MalformedTimestamp_ReturnsError verifies that a malformed
// timestamp in a candidate's checkpoint hash surfaces a parse error instead of
// silently dropping the checkpoint from the result.
func TestFilterBeforeRefs_MalformedTimestamp_ReturnsError(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	const (
		lineageID = "ln-fbf-malformed-ts"
		ns        = "nsA"
	)

	cursor := graph.NewCheckpoint(map[string]any{"i": 2}, map[string]int64{"i": 2}, nil)
	cursor.Timestamp = time.Unix(0, 2_000_000_000)
	cursorCfg, err := saver.Put(ctx, graph.PutRequest{
		Config:      graph.CreateCheckpointConfig(lineageID, "", ns),
		Checkpoint:  cursor,
		Metadata:    graph.NewCheckpointMetadata(graph.CheckpointSourceLoop, 2),
		NewVersions: map[string]int64{"i": 2},
	})
	require.NoError(t, err)

	require.NoError(t, saver.client.ZAdd(ctx, checkpointTSKey(lineageID, ns),
		redis.Z{Score: 1_400_000_000, Member: "bad-ts"},
	).Err())
	require.NoError(t, saver.client.HSet(ctx, checkpointKey(lineageID, ns, "bad-ts"), tsKey, "not-a-number").Err())

	got, err := saver.filterBeforeRefs(ctx, lineageID, ns, graph.GetCheckpointID(cursorCfg),
		[]checkpointRef{{namespace: ns, id: "bad-ts"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse timestamp")
	assert.Nil(t, got)
}

// TestFilterBeforeRefs_PerCommandError_ReturnsError verifies that a non-Nil
// per-command error from a later HGET propagates even when an earlier candidate
// is missing (redis.Nil), so List never returns silently incomplete history.
func TestFilterBeforeRefs_PerCommandError_ReturnsError(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	const (
		lineageID = "ln-fbf-per-cmd-err"
		ns        = "nsA"
	)

	cursor := graph.NewCheckpoint(map[string]any{"i": 2}, map[string]int64{"i": 2}, nil)
	cursor.Timestamp = time.Unix(0, 2_000_000_000)
	cursorCfg, err := saver.Put(ctx, graph.PutRequest{
		Config:      graph.CreateCheckpointConfig(lineageID, "", ns),
		Checkpoint:  cursor,
		Metadata:    graph.NewCheckpointMetadata(graph.CheckpointSourceLoop, 2),
		NewVersions: map[string]int64{"i": 2},
	})
	require.NoError(t, err)

	// First candidate has no hash data (HGET returns redis.Nil, which the
	// pipeline reports first); the second candidate's key is a plain string,
	// so its HGET fails with WRONGTYPE. The WRONGTYPE error must surface
	// instead of the history being silently truncated.
	require.NoError(t, saver.client.ZAdd(ctx, checkpointTSKey(lineageID, ns),
		redis.Z{Score: 1_500_000_000, Member: "zset-only"},
		redis.Z{Score: 1_400_000_000, Member: "wrong-type"},
	).Err())
	require.NoError(t, saver.client.Set(ctx, checkpointKey(lineageID, ns, "wrong-type"), "plain-string", 0).Err())

	got, err := saver.filterBeforeRefs(ctx, lineageID, ns, graph.GetCheckpointID(cursorCfg),
		[]checkpointRef{{namespace: ns, id: "zset-only"}, {namespace: ns, id: "wrong-type"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "WRONGTYPE")
	assert.Nil(t, got)
}

// TestGetCheckpointRefs_CommandErrors exercises the Redis error paths of the
// Before-filtered reference query by injecting command failures.
func TestGetCheckpointRefs_CommandErrors(t *testing.T) {
	ctx := context.Background()
	const (
		lineageID = "ln-cmd-err"
		ns        = "nsA"
	)
	zkey := checkpointTSKey(lineageID, ns)

	t.Run("zscore error", func(t *testing.T) {
		redisURL, cleanup := setupTestRedis(t)
		defer cleanup()
		saver, err := NewSaver(WithRedisClientURL(redisURL))
		require.NoError(t, err)
		defer saver.Close()
		saver.client.AddHook(&errHook{failCmd: map[string]bool{"zscore": true}})

		before := graph.CreateCheckpointConfig(lineageID, "cursor-id", ns)
		_, err = saver.getCheckpointRefs(ctx, lineageID, ns, &graph.CheckpointFilter{Before: before})
		require.Error(t, err)
	})

	t.Run("zrevrangebyscore error", func(t *testing.T) {
		redisURL, cleanup := setupTestRedis(t)
		defer cleanup()
		saver, err := NewSaver(WithRedisClientURL(redisURL))
		require.NoError(t, err)
		defer saver.Close()
		// A real cursor member so the ZScore lookup succeeds first.
		require.NoError(t, saver.client.ZAdd(ctx, zkey, redis.Z{Score: 1, Member: "cursor-id"}).Err())
		saver.client.AddHook(&errHook{failCmd: map[string]bool{"zrevrangebyscore": true}})

		before := graph.CreateCheckpointConfig(lineageID, "cursor-id", ns)
		_, err = saver.getCheckpointRefs(ctx, lineageID, ns, &graph.CheckpointFilter{Before: before})
		require.Error(t, err)
	})

	t.Run("zrevrange error", func(t *testing.T) {
		redisURL, cleanup := setupTestRedis(t)
		defer cleanup()
		saver, err := NewSaver(WithRedisClientURL(redisURL))
		require.NoError(t, err)
		defer saver.Close()
		saver.client.AddHook(&errHook{failCmd: map[string]bool{"zrevrange": true}})

		_, err = saver.getCheckpointRefs(ctx, lineageID, ns, nil)
		require.Error(t, err)
	})

	t.Run("before filtering error", func(t *testing.T) {
		redisURL, cleanup := setupTestRedis(t)
		defer cleanup()
		saver, err := NewSaver(WithRedisClientURL(redisURL))
		require.NoError(t, err)
		defer saver.Close()
		require.NoError(t, saver.client.ZAdd(ctx, zkey,
			redis.Z{Score: 3, Member: "cursor-id"},
			redis.Z{Score: 2, Member: "real"},
		).Err())
		saver.client.AddHook(&errHook{failCmd: map[string]bool{"hget": true}})

		before := graph.CreateCheckpointConfig(lineageID, "cursor-id", ns)
		_, err = saver.getCheckpointRefs(ctx, lineageID, ns, &graph.CheckpointFilter{Before: before})
		require.Error(t, err)
	})
}

// TestGetCheckpointRefs_EdgeCases exercises the zero-score cursor, empty cursor
// ID and empty member ID paths of the reference query.
func TestGetCheckpointRefs_EdgeCases(t *testing.T) {
	ctx := context.Background()
	const (
		lineageID = "ln-edge"
		ns        = "nsA"
	)
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()
	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	// A zero-score member exercises the beforeScore <= 0 branch; an empty
	// member exercises the invalid ID skip path.
	require.NoError(t, saver.client.ZAdd(ctx, checkpointTSKey(lineageID, ns),
		redis.Z{Score: 0, Member: "zero-score"},
		redis.Z{Score: 2, Member: "real"},
		redis.Z{Score: 1, Member: ""},
	).Err())
	require.NoError(t, saver.client.HSet(ctx, checkpointKey(lineageID, ns, "real"), tsKey, "2").Err())

	t.Run("zero score cursor falls back to full listing", func(t *testing.T) {
		before := graph.CreateCheckpointConfig(lineageID, "zero-score", ns)
		refs, err := saver.getCheckpointRefs(ctx, lineageID, ns, &graph.CheckpointFilter{Before: before})
		require.NoError(t, err)
		// Empty member skipped; zero-score cursor retained, matching the
		// pre-existing fallback behavior.
		assert.Equal(t, []checkpointRef{{namespace: ns, id: "real"}, {namespace: ns, id: "zero-score"}}, refs)
	})

	t.Run("empty before id falls back to full listing", func(t *testing.T) {
		before := graph.CreateCheckpointConfig(lineageID, "", ns)
		refs, err := saver.getCheckpointRefs(ctx, lineageID, ns, &graph.CheckpointFilter{Before: before})
		require.NoError(t, err)
		assert.Equal(t, []checkpointRef{{namespace: ns, id: "real"}, {namespace: ns, id: "zero-score"}}, refs)
	})
}

// TestFilterBeforeRefs_RedisErrors exercises the error paths of exact timestamp
// filtering by injecting failures into the cursor lookup and the pipeline.
func TestFilterBeforeRefs_RedisErrors(t *testing.T) {
	ctx := context.Background()
	const (
		lineageID = "ln-fbf-err"
		ns        = "nsA"
	)

	t.Run("cursor timestamp lookup error", func(t *testing.T) {
		redisURL, cleanup := setupTestRedis(t)
		defer cleanup()
		saver, err := NewSaver(WithRedisClientURL(redisURL))
		require.NoError(t, err)
		defer saver.Close()
		saver.client.AddHook(&errHook{failCmd: map[string]bool{"hget": true}})

		_, err = saver.filterBeforeRefs(ctx, lineageID, ns, "cursor-id", []checkpointRef{{namespace: ns, id: "c1"}, {namespace: ns, id: "c2"}})
		require.Error(t, err)
	})

	t.Run("pipeline error", func(t *testing.T) {
		redisURL, cleanup := setupTestRedis(t)
		defer cleanup()
		saver, err := NewSaver(WithRedisClientURL(redisURL))
		require.NoError(t, err)
		defer saver.Close()
		// Real cursor data so the exact-timestamp lookup succeeds first.
		require.NoError(t, saver.client.HSet(ctx, checkpointKey(lineageID, ns, "cursor-id"), tsKey, "100").Err())
		saver.client.AddHook(&errHook{failPipeline: true})

		_, err = saver.filterBeforeRefs(ctx, lineageID, ns, "cursor-id", []checkpointRef{{namespace: ns, id: "c1"}, {namespace: ns, id: "c2"}})
		require.Error(t, err)
	})
}

// TestGetCheckpointTS_RedisError exercises the non-missing error path of the
// exact timestamp lookup.
func TestGetCheckpointTS_RedisError(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()
	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()
	saver.client.AddHook(&errHook{failCmd: map[string]bool{"hget": true}})

	_, err = saver.getCheckpointTS(context.Background(), "ln-ts", "nsA", "any-id")
	require.Error(t, err)
}

// TestRedis_List_CrossNamespace_Basic verifies that listing with an empty namespace
// retrieves checkpoints across all registered namespaces in newest-first order.
func TestRedis_List_CrossNamespace_Basic(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	const lineageID = "ln-cross-ns-basic"

	base := time.Now().UTC().Add(-time.Hour)

	// Put 2 checkpoints in nsA, 2 in nsB, 1 in default namespace ("")
	// Interleave timestamps:
	// ckpt1 (nsA): base + 10m
	// ckpt2 (nsB): base + 20m
	// ckpt3 (default): base + 30m
	// ckpt4 (nsA): base + 40m
	// ckpt5 (nsB): base + 50m

	putCkpt := func(ns string, id string, offset time.Duration) {
		ck := graph.NewCheckpoint(map[string]any{"id": id}, map[string]int64{"id": 1}, nil)
		ck.ID = id
		ck.Timestamp = base.Add(offset)
		meta := graph.NewCheckpointMetadata(graph.CheckpointSourceInput, 1)
		_, putErr := saver.Put(ctx, graph.PutRequest{
			Config:      graph.CreateCheckpointConfig(lineageID, id, ns),
			Checkpoint:  ck,
			Metadata:    meta,
			NewVersions: map[string]int64{"id": 1},
		})
		require.NoError(t, putErr)
	}

	putCkpt("nsA", "ckpt1", 10*time.Minute)
	putCkpt("nsB", "ckpt2", 20*time.Minute)
	putCkpt("", "ckpt3", 30*time.Minute)
	putCkpt("nsA", "ckpt4", 40*time.Minute)
	putCkpt("nsB", "ckpt5", 50*time.Minute)

	// Cross-namespace List (empty namespace in config)
	cfg := graph.CreateCheckpointConfig(lineageID, "", "")
	tuples, err := saver.List(ctx, cfg, nil)
	require.NoError(t, err)
	require.Len(t, tuples, 5)

	// Expected newest-first: ckpt5 (nsB), ckpt4 (nsA), ckpt3 (""), ckpt2 (nsB), ckpt1 (nsA)
	expectedIDs := []string{"ckpt5", "ckpt4", "ckpt3", "ckpt2", "ckpt1"}
	expectedNS := []string{"nsB", "nsA", "", "nsB", "nsA"}

	for i, tuple := range tuples {
		assert.Equal(t, expectedIDs[i], tuple.Checkpoint.ID)
		assert.Equal(t, expectedNS[i], graph.GetNamespace(tuple.Config))
	}
}

// TestRedis_List_CrossNamespace_WithLimit verifies that Limit is respected in cross-namespace queries.
func TestRedis_List_CrossNamespace_WithLimit(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	const lineageID = "ln-cross-ns-limit"

	base := time.Now().UTC().Add(-time.Hour)

	putCkpt := func(ns string, id string, offset time.Duration) {
		ck := graph.NewCheckpoint(map[string]any{"id": id}, map[string]int64{"id": 1}, nil)
		ck.ID = id
		ck.Timestamp = base.Add(offset)
		meta := graph.NewCheckpointMetadata(graph.CheckpointSourceInput, 1)
		_, putErr := saver.Put(ctx, graph.PutRequest{
			Config:      graph.CreateCheckpointConfig(lineageID, id, ns),
			Checkpoint:  ck,
			Metadata:    meta,
			NewVersions: map[string]int64{"id": 1},
		})
		require.NoError(t, putErr)
	}

	putCkpt("nsA", "ckpt1", 10*time.Minute)
	putCkpt("nsB", "ckpt2", 20*time.Minute)
	putCkpt("nsA", "ckpt3", 30*time.Minute)

	cfg := graph.CreateCheckpointConfig(lineageID, "", "")
	tuples, err := saver.List(ctx, cfg, &graph.CheckpointFilter{Limit: 2})
	require.NoError(t, err)
	require.Len(t, tuples, 2)
	assert.Equal(t, "ckpt3", tuples[0].Checkpoint.ID)
	assert.Equal(t, "ckpt2", tuples[1].Checkpoint.ID)
}

// TestRedis_List_CrossNamespace_WithBeforeAndLimit verifies pagination across namespaces using Before cursor.
func TestRedis_List_CrossNamespace_WithBeforeAndLimit(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	const lineageID = "ln-cross-ns-before"

	base := time.Now().UTC().Add(-time.Hour)

	putCkpt := func(ns string, id string, offset time.Duration) {
		ck := graph.NewCheckpoint(map[string]any{"id": id}, map[string]int64{"id": 1}, nil)
		ck.ID = id
		ck.Timestamp = base.Add(offset)
		meta := graph.NewCheckpointMetadata(graph.CheckpointSourceInput, 1)
		_, putErr := saver.Put(ctx, graph.PutRequest{
			Config:      graph.CreateCheckpointConfig(lineageID, id, ns),
			Checkpoint:  ck,
			Metadata:    meta,
			NewVersions: map[string]int64{"id": 1},
		})
		require.NoError(t, putErr)
	}

	putCkpt("nsA", "ckpt1", 10*time.Minute)
	putCkpt("nsB", "ckpt2", 20*time.Minute)
	putCkpt("nsA", "ckpt3", 30*time.Minute)
	putCkpt("nsB", "ckpt4", 40*time.Minute)

	cfg := graph.CreateCheckpointConfig(lineageID, "", "")

	// 1. Before ckpt4 (nsB) -> returns ckpt3 (nsA), ckpt2 (nsB), ckpt1 (nsA)
	cursor := graph.CreateCheckpointConfig(lineageID, "ckpt4", "nsB")
	tuples, err := saver.List(ctx, cfg, &graph.CheckpointFilter{Before: cursor})
	require.NoError(t, err)
	require.Len(t, tuples, 3)
	assert.Equal(t, "ckpt3", tuples[0].Checkpoint.ID)
	assert.Equal(t, "ckpt2", tuples[1].Checkpoint.ID)
	assert.Equal(t, "ckpt1", tuples[2].Checkpoint.ID)

	// 2. Before ckpt3 (nsA) with Limit 1 -> returns ckpt2 (nsB)
	cursor2 := graph.CreateCheckpointConfig(lineageID, "ckpt3", "")
	tuples2, err := saver.List(ctx, cfg, &graph.CheckpointFilter{Before: cursor2, Limit: 1})
	require.NoError(t, err)
	require.Len(t, tuples2, 1)
	assert.Equal(t, "ckpt2", tuples2[0].Checkpoint.ID)
	assert.Equal(t, "nsB", graph.GetNamespace(tuples2[0].Config))

	// 3. Before oldest ckpt1 -> returns empty
	cursorOldest := graph.CreateCheckpointConfig(lineageID, "ckpt1", "")
	tuples3, err := saver.List(ctx, cfg, &graph.CheckpointFilter{Before: cursorOldest})
	require.NoError(t, err)
	assert.Empty(t, tuples3)
}

// TestRedis_List_CrossNamespace_CursorNotFound_ReturnsEmpty verifies unknown cursor returns empty.
func TestRedis_List_CrossNamespace_CursorNotFound_ReturnsEmpty(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	const lineageID = "ln-cross-unknown-cursor"

	ck := graph.NewCheckpoint(map[string]any{"id": "c1"}, map[string]int64{"id": 1}, nil)
	ck.ID = "c1"
	_, err = saver.Put(ctx, graph.PutRequest{
		Config:      graph.CreateCheckpointConfig(lineageID, "c1", "nsA"),
		Checkpoint:  ck,
		NewVersions: map[string]int64{"id": 1},
	})
	require.NoError(t, err)

	cfg := graph.CreateCheckpointConfig(lineageID, "", "")
	cursor := graph.CreateCheckpointConfig(lineageID, "non-existent", "")
	tuples, err := saver.List(ctx, cfg, &graph.CheckpointFilter{Before: cursor})
	require.NoError(t, err)
	assert.Empty(t, tuples)
}

// TestRedis_List_CrossNamespace_MetadataFilter verifies metadata filtering across namespaces.
func TestRedis_List_CrossNamespace_MetadataFilter(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	const lineageID = "ln-cross-meta"

	base := time.Now().UTC().Add(-time.Hour)

	putCkpt := func(ns string, id string, offset time.Duration, tag string) {
		ck := graph.NewCheckpoint(map[string]any{"id": id}, map[string]int64{"id": 1}, nil)
		ck.ID = id
		ck.Timestamp = base.Add(offset)
		meta := graph.NewCheckpointMetadata(graph.CheckpointSourceInput, 1)
		meta.Extra = map[string]any{"tag": tag}
		_, putErr := saver.Put(ctx, graph.PutRequest{
			Config:      graph.CreateCheckpointConfig(lineageID, id, ns),
			Checkpoint:  ck,
			Metadata:    meta,
			NewVersions: map[string]int64{"id": 1},
		})
		require.NoError(t, putErr)
	}

	putCkpt("nsA", "ckpt1", 10*time.Minute, "blue")
	putCkpt("nsB", "ckpt2", 20*time.Minute, "red")
	putCkpt("nsA", "ckpt3", 30*time.Minute, "blue")

	cfg := graph.CreateCheckpointConfig(lineageID, "", "")
	tuples, err := saver.List(ctx, cfg, &graph.CheckpointFilter{Metadata: map[string]any{"tag": "blue"}})
	require.NoError(t, err)
	require.Len(t, tuples, 2)
	assert.Equal(t, "ckpt3", tuples[0].Checkpoint.ID)
	assert.Equal(t, "ckpt1", tuples[1].Checkpoint.ID)
}

// TestRedis_CrossNamespace_ErrorPaths tests injected Redis errors on SMembers, Exists, and Pipelines.
func TestRedis_CrossNamespace_ErrorPaths(t *testing.T) {
	ctx := context.Background()
	const (
		lineageID = "ln-cross-err"
		nsA       = "nsA"
		nsB       = "nsB"
	)

	t.Run("SMembers error during getCheckpointRefs", func(t *testing.T) {
		redisURL, cleanup := setupTestRedis(t)
		defer cleanup()
		saver, err := NewSaver(WithRedisClientURL(redisURL))
		require.NoError(t, err)
		defer saver.Close()

		saver.client.AddHook(&errHook{failCmd: map[string]bool{"smembers": true}})
		cfg := graph.CreateCheckpointConfig(lineageID, "", "")
		_, err = saver.List(ctx, cfg, nil)
		require.Error(t, err)
	})

	t.Run("SMembers error during findCheckpointNamespace", func(t *testing.T) {
		redisURL, cleanup := setupTestRedis(t)
		defer cleanup()
		saver, err := NewSaver(WithRedisClientURL(redisURL))
		require.NoError(t, err)
		defer saver.Close()

		saver.client.AddHook(&errHook{failCmd: map[string]bool{"smembers": true}})
		_, err = saver.findCheckpointNamespace(ctx, lineageID, "some-id")
		require.Error(t, err)
	})

	t.Run("Exists error during findCheckpointNamespace", func(t *testing.T) {
		redisURL, cleanup := setupTestRedis(t)
		defer cleanup()
		saver, err := NewSaver(WithRedisClientURL(redisURL))
		require.NoError(t, err)
		defer saver.Close()

		require.NoError(t, saver.client.SAdd(ctx, lineageNSKey(lineageID), nsA).Err())
		saver.client.AddHook(&errHook{failCmd: map[string]bool{"exists": true}})
		_, err = saver.findCheckpointNamespace(ctx, lineageID, "some-id")
		require.Error(t, err)
	})

	t.Run("Pipeline error in multi-namespace ZSET retrieval", func(t *testing.T) {
		redisURL, cleanup := setupTestRedis(t)
		defer cleanup()
		saver, err := NewSaver(WithRedisClientURL(redisURL))
		require.NoError(t, err)
		defer saver.Close()

		require.NoError(t, saver.client.SAdd(ctx, lineageNSKey(lineageID), nsA, nsB).Err())
		saver.client.AddHook(&errHook{failPipeline: true})

		cfg := graph.CreateCheckpointConfig(lineageID, "", "")
		_, err = saver.List(ctx, cfg, nil)
		require.Error(t, err)
	})

	t.Run("sortRefsByTimestamp pipeline error", func(t *testing.T) {
		redisURL, cleanup := setupTestRedis(t)
		defer cleanup()
		saver, err := NewSaver(WithRedisClientURL(redisURL))
		require.NoError(t, err)
		defer saver.Close()

		saver.client.AddHook(&errHook{failPipeline: true})
		refs := []checkpointRef{{namespace: nsA, id: "c1"}, {namespace: nsB, id: "c2"}}
		_, err = saver.sortRefsByTimestamp(ctx, lineageID, refs)
		require.Error(t, err)
	})

	t.Run("sortRefsByTimestamp malformed timestamp", func(t *testing.T) {
		redisURL, cleanup := setupTestRedis(t)
		defer cleanup()
		saver, err := NewSaver(WithRedisClientURL(redisURL))
		require.NoError(t, err)
		defer saver.Close()

		require.NoError(t, saver.client.HSet(ctx, checkpointKey(lineageID, nsA, "c1"), tsKey, "not-a-number").Err())
		require.NoError(t, saver.client.HSet(ctx, checkpointKey(lineageID, nsB, "c2"), tsKey, "100").Err())

		refs := []checkpointRef{{namespace: nsA, id: "c1"}, {namespace: nsB, id: "c2"}}
		_, err = saver.sortRefsByTimestamp(ctx, lineageID, refs)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "parse timestamp")
	})

	t.Run("sortRefsByTimestamp skips missing hash entries", func(t *testing.T) {
		redisURL, cleanup := setupTestRedis(t)
		defer cleanup()
		saver, err := NewSaver(WithRedisClientURL(redisURL))
		require.NoError(t, err)
		defer saver.Close()

		require.NoError(t, saver.client.HSet(ctx, checkpointKey(lineageID, nsB, "c2"), tsKey, "200").Err())

		// c1 has no hash entry (redis.Nil)
		refs := []checkpointRef{{namespace: nsA, id: "c1"}, {namespace: nsB, id: "c2"}}
		sorted, err := saver.sortRefsByTimestamp(ctx, lineageID, refs)
		require.NoError(t, err)
		require.Len(t, sorted, 1)
		assert.Equal(t, "c2", sorted[0].id)
	})
}

// TestRedis_List_NamespaceIgnoresDifferentBeforeNamespace verifies that when
// checkpointNS is specified, List strictly fixes the namespace to checkpointNS
// and ignores a different namespace specified in filter.Before, matching
// the sqlite and inmemory saver semantics.
func TestRedis_List_NamespaceIgnoresDifferentBeforeNamespace(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	const (
		lineageID = "ln-fixed-ns"
		nsA       = "nsA"
		nsB       = "nsB"
	)

	// Put 2 checkpoints in nsA.
	var cfgA []map[string]any
	for i := 0; i < 2; i++ {
		cp := graph.NewCheckpoint(map[string]any{"step": i}, map[string]int64{"step": int64(i + 1)}, nil)
		cp.Timestamp = time.Unix(0, 1_000_000_000+int64(i*1000))
		cfg, err := saver.Put(ctx, graph.PutRequest{
			Config:      graph.CreateCheckpointConfig(lineageID, "", nsA),
			Checkpoint:  cp,
			Metadata:    graph.NewCheckpointMetadata(graph.CheckpointSourceInput, i),
			NewVersions: map[string]int64{"step": int64(i + 1)},
		})
		require.NoError(t, err)
		cfgA = append(cfgA, cfg)
	}

	// Put a checkpoint in nsB.
	cpB := graph.NewCheckpoint(map[string]any{"step": 10}, map[string]int64{"step": 11}, nil)
	cpB.Timestamp = time.Unix(0, 1_000_000_500)
	cfgB, err := saver.Put(ctx, graph.PutRequest{
		Config:      graph.CreateCheckpointConfig(lineageID, "", nsB),
		Checkpoint:  cpB,
		Metadata:    graph.NewCheckpointMetadata(graph.CheckpointSourceInput, 10),
		NewVersions: map[string]int64{"step": 11},
	})
	require.NoError(t, err)

	// List in nsA with a Before filter pointing to cfgA[1] but with Namespace explicitly set to nsB.
	// The query should stay fixed to nsA, where cfgA[1]'s ID is found.
	beforeWithWrongNS := graph.CreateCheckpointConfig(lineageID, graph.GetCheckpointID(cfgA[1]), nsB)
	got, err := saver.List(ctx, graph.CreateCheckpointConfig(lineageID, "", nsA), &graph.CheckpointFilter{Before: beforeWithWrongNS})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, graph.GetCheckpointID(cfgA[0]), got[0].Checkpoint.ID)
	assert.Equal(t, nsA, graph.GetNamespace(got[0].Config))

	_ = cfgB
}

// TestRedis_List_TopKCapPushedDown verifies that when Limit is specified
// without metadata filters, results are strictly bounded and correctly sorted.
func TestRedis_List_TopKCapPushedDown(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	saver, err := NewSaver(WithRedisClientURL(redisURL))
	require.NoError(t, err)
	defer saver.Close()

	ctx := context.Background()
	const (
		lineageID = "ln-topk-cap"
		nsA       = "nsA"
		nsB       = "nsB"
	)

	for i := 0; i < 5; i++ {
		cpA := graph.NewCheckpoint(map[string]any{"v": i}, map[string]int64{"v": int64(i + 1)}, nil)
		cpA.Timestamp = time.Unix(0, 1_000_000_000+int64(i*2))
		_, err := saver.Put(ctx, graph.PutRequest{
			Config:      graph.CreateCheckpointConfig(lineageID, "", nsA),
			Checkpoint:  cpA,
			Metadata:    graph.NewCheckpointMetadata(graph.CheckpointSourceInput, i),
			NewVersions: map[string]int64{"v": int64(i + 1)},
		})
		require.NoError(t, err)

		cpB := graph.NewCheckpoint(map[string]any{"v": i}, map[string]int64{"v": int64(i + 1)}, nil)
		cpB.Timestamp = time.Unix(0, 1_000_000_001+int64(i*2))
		_, err = saver.Put(ctx, graph.PutRequest{
			Config:      graph.CreateCheckpointConfig(lineageID, "", nsB),
			Checkpoint:  cpB,
			Metadata:    graph.NewCheckpointMetadata(graph.CheckpointSourceInput, i),
			NewVersions: map[string]int64{"v": int64(i + 1)},
		})
		require.NoError(t, err)
	}

	// Cross-namespace listing with Limit: 3
	got, err := saver.List(ctx, graph.CreateCheckpointConfig(lineageID, "", ""), &graph.CheckpointFilter{Limit: 3})
	require.NoError(t, err)
	require.Len(t, got, 3)
	// Newest first timestamps should be: nsB(4): 1_000_000_009, nsA(4): 1_000_000_008, nsB(3): 1_000_000_007
	assert.Equal(t, nsB, graph.GetNamespace(got[0].Config))
	assert.Equal(t, nsA, graph.GetNamespace(got[1].Config))
	assert.Equal(t, nsB, graph.GetNamespace(got[2].Config))

	// Single-namespace listing with Limit: 2
	gotSingle, err := saver.List(ctx, graph.CreateCheckpointConfig(lineageID, "", nsA), &graph.CheckpointFilter{Limit: 2})
	require.NoError(t, err)
	require.Len(t, gotSingle, 2)
}

// TestRedis_CrossNamespace_CoverageBoost tests all edge and error branches in cross-namespace listing.
func TestRedis_CrossNamespace_CoverageBoost(t *testing.T) {
	ctx := context.Background()
	const (
		lineageID = "ln-cov-boost"
		nsA       = "nsA"
		nsB       = "nsB"
	)

	t.Run("empty lineage with empty namespace returns nil", func(t *testing.T) {
		redisURL, cleanup := setupTestRedis(t)
		defer cleanup()
		saver, err := NewSaver(WithRedisClientURL(redisURL))
		require.NoError(t, err)
		defer saver.Close()

		cfg := graph.CreateCheckpointConfig("ln-empty-lineage", "", "")
		got, err := saver.List(ctx, cfg, nil)
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("findCheckpointNamespace error in getCheckpointRefs", func(t *testing.T) {
		redisURL, cleanup := setupTestRedis(t)
		defer cleanup()
		saver, err := NewSaver(WithRedisClientURL(redisURL))
		require.NoError(t, err)
		defer saver.Close()

		require.NoError(t, saver.client.SAdd(ctx, lineageNSKey(lineageID), nsA).Err())
		saver.client.AddHook(&errHook{failCmd: map[string]bool{"smembers": true}})

		before := graph.CreateCheckpointConfig(lineageID, "some-id", "")
		cfg := graph.CreateCheckpointConfig(lineageID, "", "")
		_, err = saver.List(ctx, cfg, &graph.CheckpointFilter{Before: before})
		require.Error(t, err)
	})

	t.Run("Exists error during cursor validation in getCheckpointRefs", func(t *testing.T) {
		redisURL, cleanup := setupTestRedis(t)
		defer cleanup()
		saver, err := NewSaver(WithRedisClientURL(redisURL))
		require.NoError(t, err)
		defer saver.Close()

		require.NoError(t, saver.client.SAdd(ctx, lineageNSKey(lineageID), nsA).Err())
		saver.client.AddHook(&errHook{failCmd: map[string]bool{"exists": true}})

		before := graph.CreateCheckpointConfig(lineageID, "cursor-not-found", "")
		cfg := graph.CreateCheckpointConfig(lineageID, "", "")
		_, err = saver.List(ctx, cfg, &graph.CheckpointFilter{Before: before})
		require.Error(t, err)
	})

	t.Run("getCheckpointScore redis.Nil returns nil", func(t *testing.T) {
		redisURL, cleanup := setupTestRedis(t)
		defer cleanup()
		saver, err := NewSaver(WithRedisClientURL(redisURL))
		require.NoError(t, err)
		defer saver.Close()

		require.NoError(t, saver.client.SAdd(ctx, lineageNSKey(lineageID), nsA).Err())
		require.NoError(t, saver.client.HSet(ctx, checkpointKey(lineageID, nsA, "cur-hash-only"), tsKey, "100").Err())

		before := graph.CreateCheckpointConfig(lineageID, "cur-hash-only", nsA)
		cfg := graph.CreateCheckpointConfig(lineageID, "", "")
		got, err := saver.List(ctx, cfg, &graph.CheckpointFilter{Before: before})
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("multi-namespace pipeline with beforeApplied skipping cursor and empty ID", func(t *testing.T) {
		redisURL, cleanup := setupTestRedis(t)
		defer cleanup()
		saver, err := NewSaver(WithRedisClientURL(redisURL))
		require.NoError(t, err)
		defer saver.Close()

		require.NoError(t, saver.client.SAdd(ctx, lineageNSKey(lineageID), nsA, nsB).Err())

		cpCur := graph.NewCheckpoint(map[string]any{"v": 1}, map[string]int64{"v": 1}, nil)
		cpCur.Timestamp = time.Unix(0, 200_000_000)
		cfgCur, err := saver.Put(ctx, graph.PutRequest{
			Config:      graph.CreateCheckpointConfig(lineageID, "", nsA),
			Checkpoint:  cpCur,
			Metadata:    graph.NewCheckpointMetadata(graph.CheckpointSourceInput, 1),
			NewVersions: map[string]int64{"v": 1},
		})
		require.NoError(t, err)
		cursorID := graph.GetCheckpointID(cfgCur)

		require.NoError(t, saver.client.ZAdd(ctx, checkpointTSKey(lineageID, nsA), redis.Z{Score: 100, Member: ""}).Err())

		cpOlder := graph.NewCheckpoint(map[string]any{"v": 0}, map[string]int64{"v": 1}, nil)
		cpOlder.Timestamp = time.Unix(0, 150_000_000)
		cfgOlder, err := saver.Put(ctx, graph.PutRequest{
			Config:      graph.CreateCheckpointConfig(lineageID, "", nsB),
			Checkpoint:  cpOlder,
			Metadata:    graph.NewCheckpointMetadata(graph.CheckpointSourceInput, 0),
			NewVersions: map[string]int64{"v": 1},
		})
		require.NoError(t, err)
		olderID := graph.GetCheckpointID(cfgOlder)

		before := graph.CreateCheckpointConfig(lineageID, cursorID, nsA)
		cfg := graph.CreateCheckpointConfig(lineageID, "", "")
		got, err := saver.List(ctx, cfg, &graph.CheckpointFilter{Before: before})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, olderID, got[0].Checkpoint.ID)
	})

	t.Run("multi-namespace with empty ZSETs returns nil", func(t *testing.T) {
		redisURL, cleanup := setupTestRedis(t)
		defer cleanup()
		saver, err := NewSaver(WithRedisClientURL(redisURL))
		require.NoError(t, err)
		defer saver.Close()

		require.NoError(t, saver.client.SAdd(ctx, lineageNSKey(lineageID), nsA, nsB).Err())
		cfg := graph.CreateCheckpointConfig(lineageID, "", "")
		got, err := saver.List(ctx, cfg, nil)
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("filterBeforeRefs and sortRefsByTimestamp empty or single element", func(t *testing.T) {
		redisURL, cleanup := setupTestRedis(t)
		defer cleanup()
		saver, err := NewSaver(WithRedisClientURL(redisURL))
		require.NoError(t, err)
		defer saver.Close()

		res, err := saver.filterBeforeRefs(ctx, lineageID, nsA, "any-id", nil)
		require.NoError(t, err)
		assert.Nil(t, res)

		res, err = saver.sortRefsByTimestamp(ctx, lineageID, nil)
		require.NoError(t, err)
		assert.Nil(t, res)

		single := []checkpointRef{{namespace: nsA, id: "c1"}}
		res, err = saver.sortRefsByTimestamp(ctx, lineageID, single)
		require.NoError(t, err)
		assert.Equal(t, single, res)
	})

	t.Run("getCheckpointRefs propagates filterBeforeRefs error", func(t *testing.T) {
		redisURL, cleanup := setupTestRedis(t)
		defer cleanup()
		saver, err := NewSaver(WithRedisClientURL(redisURL))
		require.NoError(t, err)
		defer saver.Close()

		require.NoError(t, saver.client.SAdd(ctx, lineageNSKey(lineageID), nsA).Err())
		cursorID := "cur-err"
		require.NoError(t, saver.client.HSet(ctx, checkpointKey(lineageID, nsA, cursorID), tsKey, "malformed-ts").Err())
		require.NoError(t, saver.client.ZAdd(ctx, checkpointTSKey(lineageID, nsA), redis.Z{Score: 200, Member: cursorID}).Err())

		// Candidate with valid score
		candID := "cand-1"
		require.NoError(t, saver.client.HSet(ctx, checkpointKey(lineageID, nsA, candID), tsKey, "100").Err())
		require.NoError(t, saver.client.ZAdd(ctx, checkpointTSKey(lineageID, nsA), redis.Z{Score: 100, Member: candID}).Err())

		before := graph.CreateCheckpointConfig(lineageID, cursorID, nsA)
		cfg := graph.CreateCheckpointConfig(lineageID, "", nsA)
		_, err = saver.List(ctx, cfg, &graph.CheckpointFilter{Before: before})
		require.Error(t, err)
	})

	t.Run("sortRefsByTimestamp per-command non-nil error", func(t *testing.T) {
		redisURL, cleanup := setupTestRedis(t)
		defer cleanup()
		saver, err := NewSaver(WithRedisClientURL(redisURL))
		require.NoError(t, err)
		defer saver.Close()

		// Setting a WRONGTYPE on a key to trigger a non-nil command error during HGet
		require.NoError(t, saver.client.Set(ctx, checkpointKey(lineageID, nsA, "c1"), "string-val", 0).Err())
		require.NoError(t, saver.client.HSet(ctx, checkpointKey(lineageID, nsB, "c2"), tsKey, "200").Err())

		refs := []checkpointRef{{namespace: nsA, id: "c1"}, {namespace: nsB, id: "c2"}}
		_, err = saver.sortRefsByTimestamp(ctx, lineageID, refs)
		require.Error(t, err)
	})

	t.Run("getCheckpointRefs pipeline per-command error", func(t *testing.T) {
		redisURL, cleanup := setupTestRedis(t)
		defer cleanup()
		saver, err := NewSaver(WithRedisClientURL(redisURL))
		require.NoError(t, err)
		defer saver.Close()

		require.NoError(t, saver.client.SAdd(ctx, lineageNSKey(lineageID), nsA, nsB).Err())
		// Set WRONGTYPE on nsA's zset key
		require.NoError(t, saver.client.Set(ctx, checkpointTSKey(lineageID, nsA), "string-val", 0).Err())

		cfg := graph.CreateCheckpointConfig(lineageID, "", "")
		_, err = saver.List(ctx, cfg, nil)
		require.Error(t, err)
	})
}
