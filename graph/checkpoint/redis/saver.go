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
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/log"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/redis"
)

const (
	keyPrefixCheckpoint   = "ckpt:"
	keyPrefixCheckpointTS = "ckpt_ts:"
	keyPrefixWrites       = "writes:"
	keyPrefixLineageNS    = "lineage_ns:"
)

const (
	lingeageIDKey         = "lineage_id"
	checkpointIDKey       = "checkpoint_id"
	checkpointNSKey       = "checkpoint_ns"
	parentCheckpointIDKey = "parent_checkpoint_id"
	tsKey                 = "ts"
	checkpointJSONKey     = "checkpoint_json"
	metadataJSONKey       = "metadata_json"
)

func checkpointKey(lineageID, checkpointNS, checkpointID string) string {
	return fmt.Sprintf("%s%s:%s:%s", keyPrefixCheckpoint, lineageID, checkpointNS, checkpointID)
}

func checkpointTSKey(lineageID, checkpointNS string) string {
	if checkpointNS == "" {
		return fmt.Sprintf("%s%s", keyPrefixCheckpointTS, lineageID)
	}
	return fmt.Sprintf("%s%s:%s", keyPrefixCheckpointTS, lineageID, checkpointNS)
}

func writesKey(lineageID, checkpointNS, checkpointID string) string {
	return fmt.Sprintf("%s%s:%s:%s", keyPrefixWrites, lineageID, checkpointNS, checkpointID)
}

func lineageNSKey(lineageID string) string {
	return fmt.Sprintf("%s%s", keyPrefixLineageNS, lineageID)
}

type writeData struct {
	TaskID    string `json:"task_id"`
	Idx       int    `json:"idx"`
	Channel   string `json:"channel"`
	ValueJSON []byte `json:"value_json"`
	TaskPath  string `json:"task_path"`
	Seq       int64  `json:"seq"`
}

// Saver is the redis checkpoint service.
type Saver struct {
	opts   Options
	client redis.UniversalClient
	once   sync.Once // ensure Close is called only once
}

// NewSaver creates a new saver.
func NewSaver(options ...Option) (*Saver, error) {
	opts := defaultOptions
	for _, option := range options {
		option(&opts)
	}

	builderOpts := []storage.ClientBuilderOpt{
		storage.WithClientBuilderURL(opts.url),
		storage.WithExtraOptions(opts.extraOptions...),
	}

	// if instance name set, and url not set, use instance name to create redis client
	if opts.url == "" && opts.instanceName != "" {
		var ok bool
		if builderOpts, ok = storage.GetRedisInstance(opts.instanceName); !ok {
			return nil, fmt.Errorf("redis instance %s not found", opts.instanceName)
		}
	}

	redisClient, err := storage.GetClientBuilder()(builderOpts...)
	if err != nil {
		return nil, fmt.Errorf("create redis client from url failed: %w", err)
	}

	s := &Saver{
		opts:   opts,
		client: redisClient,
	}
	return s, nil
}

// Get returns the checkpoint for the given config.
func (s *Saver) Get(ctx context.Context, config map[string]any) (*graph.Checkpoint, error) {
	t, err := s.GetTuple(ctx, config)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, nil
	}
	return t.Checkpoint, nil
}

// GetTuple returns the checkpoint tuple for the given config.
func (s *Saver) GetTuple(ctx context.Context, config map[string]any) (*graph.CheckpointTuple, error) {
	lineageID := graph.GetLineageID(config)
	checkpointNS := graph.GetNamespace(config)
	checkpointID := graph.GetCheckpointID(config)

	if lineageID == "" {
		return nil, errors.New("lineage_id is required")
	}

	checkpointID, err := s.findCheckpointID(ctx, lineageID, checkpointNS, checkpointID)
	if err != nil {
		return nil, err
	}
	if checkpointID == "" {
		return nil, nil
	}

	checkpointData, err := s.client.HGetAll(ctx, checkpointKey(lineageID, checkpointNS, checkpointID)).Result()
	if err != nil {
		return nil, fmt.Errorf("get checkpoint data: %w", err)
	}
	if len(checkpointData) == 0 {
		return nil, nil
	}

	var ckpt graph.Checkpoint
	if err := json.Unmarshal([]byte(checkpointData["checkpoint_json"]), &ckpt); err != nil {
		return nil, fmt.Errorf("unmarshal checkpoint: %w", err)
	}

	var meta graph.CheckpointMetadata
	if err := json.Unmarshal([]byte(checkpointData["metadata_json"]), &meta); err != nil {
		return nil, fmt.Errorf("unmarshal metadata: %w", err)
	}

	parentID := checkpointData[parentCheckpointIDKey]
	ts, err := strconv.ParseInt(checkpointData["ts"], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse timestamp: %w", err)
	}

	writes, err := s.loadWrites(ctx, lineageID, checkpointNS, checkpointID)
	if err != nil {
		return nil, err
	}

	var parentCfg map[string]any
	if parentID != "" {
		parentNS, err := s.findCheckpointNamespace(ctx, lineageID, parentID)
		if err != nil {
			return nil, err
		}
		parentCfg = graph.CreateCheckpointConfig(lineageID, parentID, parentNS)
	}

	returnCfg := graph.CreateCheckpointConfig(lineageID, checkpointID, checkpointNS)
	if ts > 0 {
		ckpt.Timestamp = time.Unix(0, ts)
	}

	return &graph.CheckpointTuple{
		Config:        returnCfg,
		Checkpoint:    &ckpt,
		Metadata:      &meta,
		ParentConfig:  parentCfg,
		PendingWrites: writes,
	}, nil
}

func (s *Saver) findCheckpointID(ctx context.Context, lineageID, checkpointNS, checkpointID string) (string, error) {
	if checkpointID != "" {
		return checkpointID, nil
	}
	// Find a latest checkpoint in the namespace.
	key := checkpointTSKey(lineageID, checkpointNS)
	members, err := s.client.ZRevRange(ctx, key, 0, 0).Result()
	if err != nil {
		return "", err
	}
	if len(members) == 0 {
		return "", nil
	}
	return members[0], nil
}

// List returns checkpoints for the lineage/namespace, with optional filters.
func (s *Saver) List(ctx context.Context, config map[string]any, filter *graph.CheckpointFilter) ([]*graph.CheckpointTuple, error) {
	lineageID := graph.GetLineageID(config)
	checkpointNS := graph.GetNamespace(config)
	if lineageID == "" {
		return nil, errors.New("lineage_id is required")
	}

	checkpointIDs, err := s.getCheckpointIDs(ctx, lineageID, checkpointNS, filter)
	if err != nil {
		return nil, err
	}

	var tuples []*graph.CheckpointTuple
	for _, checkpointID := range checkpointIDs {
		cfg := graph.CreateCheckpointConfig(lineageID, checkpointID, checkpointNS)
		tuple, err := s.GetTuple(ctx, cfg)
		if err != nil {
			return nil, err
		}
		if tuple == nil {
			continue
		}

		if filter != nil && len(filter.Metadata) > 0 {
			if tuple.Metadata == nil || tuple.Metadata.Extra == nil {
				continue
			}
			matches := true
			for key, value := range filter.Metadata {
				if tuple.Metadata.Extra[key] != value {
					matches = false
					break
				}
			}
			if !matches {
				continue
			}
		}
		tuples = append(tuples, tuple)
		if filter != nil && filter.Limit > 0 && len(tuples) >= filter.Limit {
			break
		}
	}

	return tuples, nil
}

func (s *Saver) getCheckpointIDs(ctx context.Context, lineageID, checkpointNS string, filter *graph.CheckpointFilter) ([]string, error) {
	key := checkpointTSKey(lineageID, checkpointNS)
	var members []string
	var err error

	if filter != nil && filter.Before != nil {
		beforeID := graph.GetCheckpointID(filter.Before)
		if beforeID != "" {
			beforeScore, err := s.getCheckpointScore(ctx, lineageID, checkpointNS, beforeID)
			if err != nil {
				return nil, err
			}
			if beforeScore > 0 {
				members, err = s.client.ZRangeByScore(ctx, key, &redis.ZRangeBy{
					Min: "0",
					Max: fmt.Sprintf("(%d", beforeScore),
				}).Result()
			}
		}
	}

	if members == nil {
		members, err = s.client.ZRevRange(ctx, key, 0, -1).Result()
	}
	if err != nil {
		return nil, err
	}

	var checkpointIDs []string
	for _, id := range members {
		if id == "" {
			log.WarnfContext(
				ctx,
				"invalid checkpoint id format: %s",
				id,
			)
			continue
		}
		checkpointIDs = append(checkpointIDs, id)
	}

	return checkpointIDs, nil
}

func (s *Saver) getCheckpointScore(ctx context.Context, lineageID, checkpointNS, checkpointID string) (int64, error) {
	key := checkpointTSKey(lineageID, checkpointNS)
	score, err := s.client.ZScore(ctx, key, checkpointID).Result()
	if err != nil {
		return 0, err
	}
	return int64(score), nil
}

// Put stores the checkpoint and returns the updated config with checkpoint ID.
func (s *Saver) Put(ctx context.Context, req graph.PutRequest) (map[string]any, error) {
	if req.Checkpoint == nil {
		return nil, errors.New("checkpoint cannot be nil")
	}

	lineageID := graph.GetLineageID(req.Config)
	checkpointNS := graph.GetNamespace(req.Config)
	if lineageID == "" {
		return nil, errors.New("lineage_id is required")
	}

	checkpointJSON, err := json.Marshal(req.Checkpoint)
	if err != nil {
		return nil, fmt.Errorf("marshal checkpoint: %w", err)
	}

	if req.Metadata == nil {
		req.Metadata = &graph.CheckpointMetadata{Source: graph.CheckpointSourceUpdate, Step: 0}
	}
	metadataJSON, err := json.Marshal(req.Metadata)
	if err != nil {
		return nil, fmt.Errorf("marshal metadata: %w", err)
	}

	pipe := s.client.TxPipeline()

	checkpointID := req.Checkpoint.ID
	ts := req.Checkpoint.Timestamp.UnixNano()
	if ts <= 0 {
		ts = time.Now().UTC().UnixNano()
	}

	checkpointKey := checkpointKey(lineageID, checkpointNS, checkpointID)
	pipe.HSet(ctx, checkpointKey,
		lingeageIDKey, lineageID,
		checkpointNSKey, checkpointNS,
		checkpointIDKey, checkpointID,
		parentCheckpointIDKey, req.Checkpoint.ParentCheckpointID,
		tsKey, ts,
		checkpointJSONKey, checkpointJSON,
		metadataJSONKey, metadataJSON,
	)
	pipe.Expire(ctx, checkpointKey, s.opts.ttl)

	tsKey := checkpointTSKey(lineageID, checkpointNS)
	pipe.ZAdd(ctx, tsKey, redis.Z{
		Score:  float64(ts),
		Member: checkpointID,
	})
	pipe.Expire(ctx, tsKey, s.opts.ttl)

	nsKey := lineageNSKey(lineageID)
	pipe.SAdd(ctx, nsKey, checkpointNS)
	pipe.Expire(ctx, nsKey, s.opts.ttl)

	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("redis transaction failed: %w", err)
	}

	return graph.CreateCheckpointConfig(lineageID, checkpointID, checkpointNS), nil
}

func (s *Saver) PutWrites(ctx context.Context, req graph.PutWritesRequest) error {
	lineageID := graph.GetLineageID(req.Config)
	checkpointNS := graph.GetNamespace(req.Config)
	checkpointID := graph.GetCheckpointID(req.Config)
	if lineageID == "" || checkpointID == "" {
		return errors.New("lineage_id and checkpoint_id are required")
	}

	pipe := s.client.Pipeline()

	writeKey := writesKey(lineageID, checkpointNS, checkpointID)

	for idx, w := range req.Writes {
		valueJSON, err := json.Marshal(w.Value)
		if err != nil {
			return fmt.Errorf("marshal write: %w", err)
		}

		seq := w.Sequence
		if seq == 0 {
			seq = int64(idx)
		}

		writeData := writeData{
			TaskID:    req.TaskID,
			Idx:       idx,
			Channel:   w.Channel,
			ValueJSON: valueJSON,
			TaskPath:  req.TaskPath,
			Seq:       seq,
		}

		field := fmt.Sprintf("%s:%d", req.TaskID, idx)
		writeJSON, _ := json.Marshal(writeData)
		pipe.HSet(ctx, writeKey, field, writeJSON)
	}
	pipe.Expire(ctx, writeKey, s.opts.ttl)

	_, err := pipe.Exec(ctx)
	return err
}

// PutFull atomically stores a checkpoint with its pending writes in a single transaction.
func (s *Saver) PutFull(ctx context.Context, req graph.PutFullRequest) (map[string]any, error) {
	lineageID := graph.GetLineageID(req.Config)
	checkpointNS := graph.GetNamespace(req.Config)
	if lineageID == "" {
		return nil, errors.New("lineage_id is required")
	}
	if req.Checkpoint == nil {
		return nil, errors.New("checkpoint cannot be nil")
	}

	checkpointJSON, err := json.Marshal(req.Checkpoint)
	if err != nil {
		return nil, fmt.Errorf("marshal checkpoint: %w", err)
	}

	metadataJSON, err := json.Marshal(req.Metadata)
	if err != nil {
		return nil, fmt.Errorf("marshal metadata: %w", err)
	}

	pipe := s.client.TxPipeline()

	checkpointID := req.Checkpoint.ID
	ts := req.Checkpoint.Timestamp.UnixNano()
	if ts <= 0 {
		ts = time.Now().UTC().UnixNano()
	}

	checkpointKey := checkpointKey(lineageID, checkpointNS, checkpointID)
	pipe.HSet(ctx, checkpointKey,
		lingeageIDKey, lineageID,
		checkpointNSKey, checkpointNS,
		checkpointIDKey, checkpointID,
		parentCheckpointIDKey, req.Checkpoint.ParentCheckpointID,
		tsKey, ts,
		checkpointJSONKey, checkpointJSON,
		metadataJSONKey, metadataJSON,
	)
	pipe.Expire(ctx, checkpointKey, s.opts.ttl)

	tsKey := checkpointTSKey(lineageID, checkpointNS)
	pipe.ZAdd(ctx, tsKey, redis.Z{
		Score:  float64(ts),
		Member: checkpointID,
	})
	pipe.Expire(ctx, tsKey, s.opts.ttl)

	nsKey := lineageNSKey(lineageID)
	pipe.SAdd(ctx, nsKey, checkpointNS)
	pipe.Expire(ctx, nsKey, s.opts.ttl)

	writeKey := writesKey(lineageID, checkpointNS, checkpointID)
	for idx, w := range req.PendingWrites {
		valueJSON, err := json.Marshal(w.Value)
		if err != nil {
			return nil, fmt.Errorf("marshal write value: %w", err)
		}

		seq := w.Sequence
		if seq == 0 {
			seq = time.Now().UnixNano()
		}

		writeData := writeData{
			TaskID:    w.TaskID,
			Idx:       idx,
			Channel:   w.Channel,
			ValueJSON: valueJSON,
			TaskPath:  "",
			Seq:       seq,
		}

		field := fmt.Sprintf("%s:%d", w.TaskID, idx)
		writeJSON, err := json.Marshal(writeData)
		if err != nil {
			return nil, fmt.Errorf("marshal write data: %w", err)
		}
		pipe.HSet(ctx, writeKey, field, writeJSON)
	}
	pipe.Expire(ctx, writeKey, s.opts.ttl)

	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("redis transaction failed: %w", err)
	}

	return graph.CreateCheckpointConfig(lineageID, checkpointID, checkpointNS), nil
}

// DeleteLineage deletes all checkpoints and writes for the lineage.
func (s *Saver) DeleteLineage(ctx context.Context, lineageID string) error {
	if lineageID == "" {
		return errors.New("lineage_id is required")
	}

	nsKey := lineageNSKey(lineageID)
	namespaces, err := s.client.SMembers(ctx, nsKey).Result()
	if err != nil {
		return err
	}
	pipe := s.client.Pipeline()

	for _, ns := range namespaces {
		tsKey := checkpointTSKey(lineageID, ns)
		members, err := s.client.ZRange(ctx, tsKey, 0, -1).Result()
		if err != nil {
			continue
		}

		for _, member := range members {
			checkpointID := member

			ckptKey := checkpointKey(lineageID, ns, checkpointID)
			pipe.Del(ctx, ckptKey)

			writeKey := writesKey(lineageID, ns, checkpointID)
			pipe.Del(ctx, writeKey)
		}

		pipe.Del(ctx, tsKey)
	}

	pipe.Del(ctx, nsKey)

	_, err = pipe.Exec(ctx)
	return err
}

func (s *Saver) loadWrites(ctx context.Context, lineageID, checkpointNS, checkpointID string) ([]graph.PendingWrite, error) {
	writeKey := writesKey(lineageID, checkpointNS, checkpointID)
	writeMap, err := s.client.HGetAll(ctx, writeKey).Result()
	if err != nil {
		return nil, fmt.Errorf("get writes: %w", err)
	}

	var writes []graph.PendingWrite
	for _, writeJSON := range writeMap {
		var writeData writeData
		if err := json.Unmarshal([]byte(writeJSON), &writeData); err != nil {
			continue
		}
		var value any
		if err := json.Unmarshal(writeData.ValueJSON, &value); err != nil {
			continue
		}

		writes = append(writes, graph.PendingWrite{
			TaskID:   writeData.TaskID,
			Channel:  writeData.Channel,
			Value:    value,
			Sequence: writeData.Seq,
		})
	}

	sort.Slice(writes, func(i, j int) bool {
		return writes[i].Sequence < writes[j].Sequence
	})

	return writes, nil
}

func (s *Saver) findCheckpointNamespace(ctx context.Context, lineageID, checkpointID string) (string, error) {
	if checkpointID == "" || lineageID == "" {
		return "", nil
	}

	nsKey := lineageNSKey(lineageID)
	namespaces, err := s.client.SMembers(ctx, nsKey).Result()
	if err != nil {
		return "", err
	}

	for _, ns := range namespaces {
		exists, err := s.client.Exists(ctx, checkpointKey(lineageID, ns, checkpointID)).Result()
		if err != nil {
			continue
		}
		if exists > 0 {
			return ns, nil
		}
	}

	return "", nil
}

// Close closes the service.
func (s *Saver) Close() error {
	s.once.Do(func() {
		// Close redis connection.
		if s.client != nil {
			s.client.Close()
		}
	})

	return nil
}
