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
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
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

// SQLiteCheckpointSaver is a SQLite-backed implementation of CheckpointSaver.
// It expects an initialized *sql.DB and will create the required schema.
// This saver stores the entire checkpoint and metadata as JSON blobs.
// It is suitable for production usage when paired with a persistent DB.
type SQLiteCheckpointSaver struct {
	db *sql.DB
}

// NewSQLiteCheckpointSaverFromDB creates a new saver using the provided DB.
// The DB must use a SQLite driver. The constructor creates tables if needed.
func NewSQLiteCheckpointSaverFromDB(db *sql.DB) (*SQLiteCheckpointSaver, error) {
	if db == nil {
		return nil, errors.New("db is nil")
	}
	if _, err := db.Exec(sqliteCreateCheckpoints); err != nil {
		return nil, fmt.Errorf("create checkpoints table: %w", err)
	}
	if _, err := db.Exec(sqliteCreateWrites); err != nil {
		return nil, fmt.Errorf("create writes table: %w", err)
	}
	return &SQLiteCheckpointSaver{db: db}, nil
}

// Get returns the checkpoint for the given config.
func (s *SQLiteCheckpointSaver) Get(ctx context.Context, config map[string]any) (*Checkpoint, error) {
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
func (s *SQLiteCheckpointSaver) GetTuple(ctx context.Context, config map[string]any) (*CheckpointTuple, error) {
	threadID := GetThreadID(config)
	checkpointNS := GetNamespace(config)
	checkpointID := GetCheckpointID(config)
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
		var ckpt Checkpoint
		if err := json.Unmarshal(checkpointJSON, &ckpt); err != nil {
			return nil, fmt.Errorf("unmarshal checkpoint: %w", err)
		}
		var meta CheckpointMetadata
		if err := json.Unmarshal(metadataJSON, &meta); err != nil {
			return nil, fmt.Errorf("unmarshal metadata: %w", err)
		}
		cfg := CreateCheckpointConfig(threadID, foundID, checkpointNS)
		writes, err := s.loadWrites(ctx, threadID, checkpointNS, foundID)
		if err != nil {
			return nil, err
		}
		var parentCfg map[string]any
		if parentID != "" {
			parentCfg = CreateCheckpointConfig(threadID, parentID, checkpointNS)
		}
		return &CheckpointTuple{
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
	var ckpt Checkpoint
	if err := json.Unmarshal(checkpointJSON, &ckpt); err != nil {
		return nil, fmt.Errorf("unmarshal checkpoint: %w", err)
	}
	var meta CheckpointMetadata
	if err := json.Unmarshal(metadataJSON, &meta); err != nil {
		return nil, fmt.Errorf("unmarshal metadata: %w", err)
	}
	cfg := CreateCheckpointConfig(threadID, checkpointID, checkpointNS)
	writes, err := s.loadWrites(ctx, threadID, checkpointNS, checkpointID)
	if err != nil {
		return nil, err
	}
	var parentCfg map[string]any
	if parentID != "" {
		parentCfg = CreateCheckpointConfig(threadID, parentID, checkpointNS)
	}
	return &CheckpointTuple{
		Config:        cfg,
		Checkpoint:    &ckpt,
		Metadata:      &meta,
		ParentConfig:  parentCfg,
		PendingWrites: writes,
	}, nil
}

// List returns checkpoints for the thread/namespace, with optional filters.
func (s *SQLiteCheckpointSaver) List(
	ctx context.Context,
	config map[string]any,
	filter *CheckpointFilter,
) ([]*CheckpointTuple, error) {
	threadID := GetThreadID(config)
	checkpointNS := GetNamespace(config)
	if threadID == "" {
		return nil, errors.New("thread_id is required")
	}
	rows, err := s.db.QueryContext(ctx, sqliteSelectIDsAsc, threadID, checkpointNS)
	if err != nil {
		return nil, fmt.Errorf("select ids: %w", err)
	}
	defer rows.Close()
	var tuples []*CheckpointTuple
	var beforeID string
	var limit int
	if filter != nil {
		if filter.Before != nil {
			beforeID = GetCheckpointID(filter.Before)
		}
		limit = filter.Limit
	}
	for rows.Next() {
		var id string
		var ts int64
		if err := rows.Scan(&id, &ts); err != nil {
			return nil, fmt.Errorf("scan id: %w", err)
		}
		if beforeID != "" && id >= beforeID {
			continue
		}
		cfg := CreateCheckpointConfig(threadID, id, checkpointNS)
		t, err := s.GetTuple(ctx, cfg)
		if err != nil {
			return nil, err
		}
		if t == nil {
			continue
		}
		if filter != nil && filter.Metadata != nil {
			match := true
			for k, v := range filter.Metadata {
				if t.Metadata == nil || t.Metadata.Extra == nil || t.Metadata.Extra[k] != v {
					match = false
					break
				}
			}
			if !match {
				continue
			}
		}
		tuples = append(tuples, t)
		if limit > 0 && len(tuples) >= limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iter ids: %w", err)
	}
	return tuples, nil
}

// Put stores the checkpoint and returns the updated config with checkpoint ID.
func (s *SQLiteCheckpointSaver) Put(
	ctx context.Context,
	config map[string]any,
	checkpoint *Checkpoint,
	metadata *CheckpointMetadata,
	newVersions map[string]any,
) (map[string]any, error) {
	if checkpoint == nil {
		return nil, errors.New("checkpoint cannot be nil")
	}
	threadID := GetThreadID(config)
	checkpointNS := GetNamespace(config)
	if threadID == "" {
		return nil, errors.New("thread_id is required")
	}
	parentID := GetCheckpointID(config)
	checkpointJSON, err := json.Marshal(checkpoint)
	if err != nil {
		return nil, fmt.Errorf("marshal checkpoint: %w", err)
	}
	if metadata == nil {
		metadata = &CheckpointMetadata{Source: CheckpointSourceUpdate, Step: 0}
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("marshal metadata: %w", err)
	}
	ts := checkpoint.Timestamp.Unix()
	if ts == 0 {
		// Ensure non-zero timestamp for ordering.
		ts = time.Now().UTC().Unix()
	}
	_, err = s.db.ExecContext(
		ctx,
		sqliteInsertCheckpoint,
		threadID,
		checkpointNS,
		checkpoint.ID,
		parentID,
		ts,
		checkpointJSON,
		metadataJSON,
	)
	if err != nil {
		return nil, fmt.Errorf("insert checkpoint: %w", err)
	}
	return CreateCheckpointConfig(threadID, checkpoint.ID, checkpointNS), nil
}

// PutWrites stores write entries for a checkpoint.
func (s *SQLiteCheckpointSaver) PutWrites(
	ctx context.Context,
	config map[string]any,
	writes []PendingWrite,
	taskID string,
	taskPath string,
) error {
	threadID := GetThreadID(config)
	checkpointNS := GetNamespace(config)
	checkpointID := GetCheckpointID(config)
	if threadID == "" || checkpointID == "" {
		return errors.New("thread_id and checkpoint_id are required")
	}
	for idx, w := range writes {
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
			taskID,
			idx,
			w.Channel,
			valueJSON,
			taskPath,
		)
		if err != nil {
			return fmt.Errorf("insert write: %w", err)
		}
	}
	return nil
}

// DeleteThread deletes all checkpoints and writes for the thread.
func (s *SQLiteCheckpointSaver) DeleteThread(ctx context.Context, threadID string) error {
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

func (s *SQLiteCheckpointSaver) loadWrites(
	ctx context.Context,
	threadID, checkpointNS, checkpointID string,
) ([]PendingWrite, error) {
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
	var writes []PendingWrite
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
		writes = append(writes, PendingWrite{Channel: channel, Value: value})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iter writes: %w", err)
	}
	return writes, nil
}
