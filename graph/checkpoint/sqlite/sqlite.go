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
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/graph"
)

const (
	// SQLite table names and SQL statements.
	sqliteTableCheckpoints = "checkpoints"
	sqliteTableWrites      = "checkpoint_writes"

	sqliteCreateCheckpoints = "CREATE TABLE IF NOT EXISTS checkpoints (" +
		"thread_id TEXT NOT NULL, " +
		"checkpoint_ns TEXT NOT NULL, " +
		"checkpoint_id TEXT NOT NULL, " +
		"parent_checkpoint_id TEXT, " +
		"ts INTEGER NOT NULL, " +
		"checkpoint_json BLOB NOT NULL, " +
		"metadata_json BLOB NOT NULL, " +
		"PRIMARY KEY (thread_id, checkpoint_ns, checkpoint_id)" +
		")"

	sqliteCreateWrites = "CREATE TABLE IF NOT EXISTS checkpoint_writes (" +
		"thread_id TEXT NOT NULL, " +
		"checkpoint_ns TEXT NOT NULL, " +
		"checkpoint_id TEXT NOT NULL, " +
		"task_id TEXT NOT NULL, " +
		"idx INTEGER NOT NULL, " +
		"channel TEXT NOT NULL, " +
		"value_json BLOB NOT NULL, " +
		"task_path TEXT, " +
		"PRIMARY KEY (thread_id, checkpoint_ns, checkpoint_id, task_id, idx)" +
		")"

	sqliteInsertCheckpoint = "INSERT OR REPLACE INTO checkpoints (" +
		"thread_id, checkpoint_ns, checkpoint_id, parent_checkpoint_id, ts, " +
		"checkpoint_json, metadata_json) VALUES (?, ?, ?, ?, ?, ?, ?)"

	sqliteSelectLatest = "SELECT checkpoint_json, metadata_json, parent_checkpoint_id, checkpoint_id " +
		"FROM checkpoints WHERE thread_id = ? AND checkpoint_ns = ? " +
		"ORDER BY ts DESC LIMIT 1"

	sqliteSelectByID = "SELECT checkpoint_json, metadata_json, parent_checkpoint_id " +
		"FROM checkpoints WHERE thread_id = ? AND checkpoint_ns = ? AND checkpoint_id = ? LIMIT 1"

	sqliteSelectIDsAsc = "SELECT checkpoint_id, ts FROM checkpoints " +
		"WHERE thread_id = ? AND checkpoint_ns = ? ORDER BY ts ASC"

	sqliteInsertWrite = "INSERT OR REPLACE INTO checkpoint_writes (" +
		"thread_id, checkpoint_ns, checkpoint_id, task_id, idx, channel, value_json, task_path) " +
		"VALUES (?, ?, ?, ?, ?, ?, ?, ?)"

	sqliteSelectWrites = "SELECT task_id, idx, channel, value_json, task_path FROM checkpoint_writes " +
		"WHERE thread_id = ? AND checkpoint_ns = ? AND checkpoint_id = ? ORDER BY task_id, idx"

	sqliteDeleteThreadCkpts  = "DELETE FROM checkpoints WHERE thread_id = ?"
	sqliteDeleteThreadWrites = "DELETE FROM checkpoint_writes WHERE thread_id = ?"
)

// Saver is a SQLite-backed implementation of CheckpointSaver.
// It expects an initialized *sql.DB and will create the required schema.
// This saver stores the entire checkpoint and metadata as JSON blobs.
// It is suitable for production usage when paired with a persistent DB.
type Saver struct {
	db *sql.DB
}

// NewSaver creates a new saver using the provided DB.
// The DB must use a SQLite driver. The constructor creates tables if needed.
func NewSaver(db *sql.DB) (*Saver, error) {
	if db == nil {
		return nil, errors.New("db is nil")
	}
	if _, err := db.Exec(sqliteCreateCheckpoints); err != nil {
		return nil, fmt.Errorf("create checkpoints table: %w", err)
	}
	if _, err := db.Exec(sqliteCreateWrites); err != nil {
		return nil, fmt.Errorf("create writes table: %w", err)
	}
	return &Saver{db: db}, nil
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
	threadID := graph.GetThreadID(config)
	checkpointNS := graph.GetNamespace(config)
	checkpointID := graph.GetCheckpointID(config)
	if threadID == "" {
		return nil, errors.New("thread_id is required")
	}
	var row *sql.Row
	if checkpointID == "" {
		row = s.db.QueryRowContext(ctx, sqliteSelectLatest, threadID, checkpointNS)
		var checkpointJSON, metadataJSON []byte
		var parentID, foundID string
		if err := row.Scan(&checkpointJSON, &metadataJSON, &parentID, &foundID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, nil
			}
			return nil, fmt.Errorf("select latest: %w", err)
		}
		var ckpt graph.Checkpoint
		if err := json.Unmarshal(checkpointJSON, &ckpt); err != nil {
			return nil, fmt.Errorf("unmarshal checkpoint: %w", err)
		}
		var meta graph.CheckpointMetadata
		if err := json.Unmarshal(metadataJSON, &meta); err != nil {
			return nil, fmt.Errorf("unmarshal metadata: %w", err)
		}
		cfg := graph.CreateCheckpointConfig(threadID, foundID, checkpointNS)
		writes, err := s.loadWrites(ctx, threadID, checkpointNS, foundID)
		if err != nil {
			return nil, err
		}
		var parentCfg map[string]any
		if parentID != "" {
			parentCfg = graph.CreateCheckpointConfig(threadID, parentID, checkpointNS)
		}
		return &graph.CheckpointTuple{
			Config:        cfg,
			Checkpoint:    &ckpt,
			Metadata:      &meta,
			ParentConfig:  parentCfg,
			PendingWrites: writes,
		}, nil
	}
	row = s.db.QueryRowContext(ctx, sqliteSelectByID, threadID, checkpointNS, checkpointID)
	var checkpointJSON, metadataJSON []byte
	var parentID string
	if err := row.Scan(&checkpointJSON, &metadataJSON, &parentID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("select by id: %w", err)
	}
	var ckpt graph.Checkpoint
	if err := json.Unmarshal(checkpointJSON, &ckpt); err != nil {
		return nil, fmt.Errorf("unmarshal checkpoint: %w", err)
	}
	var meta graph.CheckpointMetadata
	if err := json.Unmarshal(metadataJSON, &meta); err != nil {
		return nil, fmt.Errorf("unmarshal metadata: %w", err)
	}
	cfg := graph.CreateCheckpointConfig(threadID, checkpointID, checkpointNS)
	writes, err := s.loadWrites(ctx, threadID, checkpointNS, checkpointID)
	if err != nil {
		return nil, err
	}
	var parentCfg map[string]any
	if parentID != "" {
		parentCfg = graph.CreateCheckpointConfig(threadID, parentID, checkpointNS)
	}
	return &graph.CheckpointTuple{
		Config:        cfg,
		Checkpoint:    &ckpt,
		Metadata:      &meta,
		ParentConfig:  parentCfg,
		PendingWrites: writes,
	}, nil
}

// List returns checkpoints for the thread/namespace, with optional filters.
func (s *Saver) List(
	ctx context.Context,
	config map[string]any,
	filter *graph.CheckpointFilter,
) ([]*graph.CheckpointTuple, error) {
	threadID := graph.GetThreadID(config)
	checkpointNS := graph.GetNamespace(config)
	if threadID == "" {
		return nil, errors.New("thread_id is required")
	}
	rows, err := s.db.QueryContext(ctx, sqliteSelectIDsAsc, threadID, checkpointNS)
	if err != nil {
		return nil, fmt.Errorf("select checkpoints: %w", err)
	}
	defer rows.Close()
	var tuples []*graph.CheckpointTuple
	for rows.Next() {
		var checkpointID string
		var ts int64
		if err := rows.Scan(&checkpointID, &ts); err != nil {
			return nil, fmt.Errorf("scan checkpoint: %w", err)
		}
		// Apply before filter if specified.
		if filter != nil && filter.Before != nil {
			beforeID := graph.GetCheckpointID(filter.Before)
			if beforeID != "" && checkpointID >= beforeID {
				continue
			}
		}
		// Get full tuple for this checkpoint.
		cfg := graph.CreateCheckpointConfig(threadID, checkpointID, checkpointNS)
		tuple, err := s.GetTuple(ctx, cfg)
		if err != nil {
			return nil, err
		}
		if tuple == nil {
			continue
		}
		// Apply metadata filter if specified.
		if filter != nil && filter.Metadata != nil {
			matches := true
			for key, value := range filter.Metadata {
				if tuple.Metadata == nil || tuple.Metadata.Extra == nil {
					matches = false
					break
				}
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
		// Apply limit if specified.
		if filter != nil && filter.Limit > 0 && len(tuples) >= filter.Limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iter checkpoints: %w", err)
	}
	return tuples, nil
}

// Put stores the checkpoint and returns the updated config with checkpoint ID.
func (s *Saver) Put(ctx context.Context, req graph.PutRequest) (map[string]any, error) {
	if req.Checkpoint == nil {
		return nil, errors.New("checkpoint cannot be nil")
	}
	threadID := graph.GetThreadID(req.Config)
	checkpointNS := graph.GetNamespace(req.Config)
	if threadID == "" {
		return nil, errors.New("thread_id is required")
	}
	parentID := graph.GetCheckpointID(req.Config)
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
	ts := req.Checkpoint.Timestamp.Unix()
	if ts == 0 {
		// Ensure non-zero timestamp for ordering.
		ts = time.Now().UTC().Unix()
	}
	_, err = s.db.ExecContext(
		ctx,
		sqliteInsertCheckpoint,
		threadID,
		checkpointNS,
		req.Checkpoint.ID,
		parentID,
		ts,
		checkpointJSON,
		metadataJSON,
	)
	if err != nil {
		return nil, fmt.Errorf("insert checkpoint: %w", err)
	}
	return graph.CreateCheckpointConfig(threadID, req.Checkpoint.ID, checkpointNS), nil
}

// PutWrites stores write entries for a checkpoint.
func (s *Saver) PutWrites(ctx context.Context, req graph.PutWritesRequest) error {
	threadID := graph.GetThreadID(req.Config)
	checkpointNS := graph.GetNamespace(req.Config)
	checkpointID := graph.GetCheckpointID(req.Config)
	if threadID == "" || checkpointID == "" {
		return errors.New("thread_id and checkpoint_id are required")
	}
	for idx, w := range req.Writes {
		valueJSON, err := json.Marshal(w.Value)
		if err != nil {
			return fmt.Errorf("marshal write: %w", err)
		}
		_, err = s.db.ExecContext(
			ctx,
			sqliteInsertWrite,
			threadID,
			checkpointNS,
			checkpointID,
			req.TaskID,
			idx,
			w.Channel,
			valueJSON,
			req.TaskPath,
		)
		if err != nil {
			return fmt.Errorf("insert write: %w", err)
		}
	}
	return nil
}

// PutFull atomically stores a checkpoint with its pending writes in a single transaction.
func (s *Saver) PutFull(ctx context.Context, req graph.PutFullRequest) (map[string]any, error) {
	threadID := graph.GetThreadID(req.Config)
	checkpointNS := graph.GetNamespace(req.Config)
	if threadID == "" {
		return nil, errors.New("thread_id is required")
	}
	if req.Checkpoint == nil {
		return nil, errors.New("checkpoint cannot be nil")
	}

	// Start transaction
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Marshal checkpoint and metadata
	checkpointJSON, err := json.Marshal(req.Checkpoint)
	if err != nil {
		return nil, fmt.Errorf("marshal checkpoint: %w", err)
	}
	metadataJSON, err := json.Marshal(req.Metadata)
	if err != nil {
		return nil, fmt.Errorf("marshal metadata: %w", err)
	}

	// Insert checkpoint
	parentID := graph.GetCheckpointID(req.Config)
	_, err = tx.ExecContext(
		ctx,
		sqliteInsertCheckpoint,
		threadID,
		checkpointNS,
		req.Checkpoint.ID,
		parentID,
		req.Checkpoint.Timestamp.UnixNano(),
		checkpointJSON,
		metadataJSON,
	)
	if err != nil {
		return nil, fmt.Errorf("insert checkpoint: %w", err)
	}

	// Insert pending writes with sequence numbers
	for idx, w := range req.PendingWrites {
		valueJSON, err := json.Marshal(w.Value)
		if err != nil {
			return nil, fmt.Errorf("marshal write value: %w", err)
		}
		_, err = tx.ExecContext(
			ctx,
			sqliteInsertWrite,
			threadID,
			checkpointNS,
			req.Checkpoint.ID,
			w.TaskID,
			idx, // Use index as sequence number
			w.Channel,
			valueJSON,
			"", // task_path
		)
		if err != nil {
			return nil, fmt.Errorf("insert write: %w", err)
		}
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}

	// Return updated config with the new checkpoint ID
	updatedConfig := graph.CreateCheckpointConfig(threadID, req.Checkpoint.ID, checkpointNS)
	return updatedConfig, nil
}

// DeleteThread deletes all checkpoints and writes for the thread.
func (s *Saver) DeleteThread(ctx context.Context, threadID string) error {
	if threadID == "" {
		return errors.New("thread_id is required")
	}
	if _, err := s.db.ExecContext(ctx, sqliteDeleteThreadCkpts, threadID); err != nil {
		return fmt.Errorf("delete checkpoints: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, sqliteDeleteThreadWrites, threadID); err != nil {
		return fmt.Errorf("delete writes: %w", err)
	}
	return nil
}

// Close releases resources held by the saver.
func (s *Saver) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *Saver) loadWrites(
	ctx context.Context,
	threadID, checkpointNS, checkpointID string,
) ([]graph.PendingWrite, error) {
	rows, err := s.db.QueryContext(
		ctx,
		sqliteSelectWrites,
		threadID,
		checkpointNS,
		checkpointID,
	)
	if err != nil {
		return nil, fmt.Errorf("select writes: %w", err)
	}
	defer rows.Close()
	var writes []graph.PendingWrite
	for rows.Next() {
		var taskID string
		var idx int
		var channel string
		var valueJSON []byte
		var taskPath string
		if err := rows.Scan(&taskID, &idx, &channel, &valueJSON, &taskPath); err != nil {
			return nil, fmt.Errorf("scan write: %w", err)
		}
		var value any
		if err := json.Unmarshal(valueJSON, &value); err != nil {
			return nil, fmt.Errorf("unmarshal write: %w", err)
		}
		writes = append(writes, graph.PendingWrite{Channel: channel, Value: value})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iter writes: %w", err)
	}
	return writes, nil
}
