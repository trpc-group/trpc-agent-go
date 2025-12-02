//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package sqlite provides SQLite-based checkpoint storage implementation
// for graph execution state persistence and recovery.
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
		"lineage_id TEXT NOT NULL, " +
		"checkpoint_ns TEXT NOT NULL, " +
		"checkpoint_id TEXT NOT NULL, " +
		"parent_checkpoint_id TEXT, " +
		"ts INTEGER NOT NULL, " +
		"checkpoint_json BLOB NOT NULL, " +
		"metadata_json BLOB NOT NULL, " +
		"PRIMARY KEY (lineage_id, checkpoint_ns, checkpoint_id)" +
		")"

	sqliteCreateWrites = "CREATE TABLE IF NOT EXISTS checkpoint_writes (" +
		"lineage_id TEXT NOT NULL, " +
		"checkpoint_ns TEXT NOT NULL, " +
		"checkpoint_id TEXT NOT NULL, " +
		"task_id TEXT NOT NULL, " +
		"idx INTEGER NOT NULL, " +
		"channel TEXT NOT NULL, " +
		"value_json BLOB NOT NULL, " +
		"task_path TEXT, " +
		"seq INTEGER NOT NULL, " +
		"PRIMARY KEY (lineage_id, checkpoint_ns, checkpoint_id, task_id, idx)" +
		")"

	sqliteInsertCheckpoint = "INSERT OR REPLACE INTO checkpoints (" +
		"lineage_id, checkpoint_ns, checkpoint_id, parent_checkpoint_id, ts, " +
		"checkpoint_json, metadata_json) VALUES (?, ?, ?, ?, ?, ?, ?)"

	sqliteSelectLatest = "SELECT checkpoint_json, metadata_json, parent_checkpoint_id, checkpoint_ns, checkpoint_id " +
		"FROM checkpoints WHERE lineage_id = ? AND checkpoint_ns = ? " +
		"ORDER BY ts DESC LIMIT 1"

	sqliteSelectByID = "SELECT checkpoint_json, metadata_json, parent_checkpoint_id, checkpoint_ns, checkpoint_id " +
		"FROM checkpoints WHERE lineage_id = ? AND checkpoint_ns = ? AND checkpoint_id = ? LIMIT 1"

	sqliteSelectIDsAsc = "SELECT checkpoint_id, ts FROM checkpoints " +
		"WHERE lineage_id = ? AND checkpoint_ns = ? ORDER BY ts ASC"

	sqliteInsertWrite = "INSERT OR REPLACE INTO checkpoint_writes (" +
		"lineage_id, checkpoint_ns, checkpoint_id, task_id, idx, channel, value_json, task_path, seq) " +
		"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)"

	sqliteSelectWrites = "SELECT task_id, idx, channel, value_json, task_path, seq FROM checkpoint_writes " +
		"WHERE lineage_id = ? AND checkpoint_ns = ? AND checkpoint_id = ? ORDER BY seq"

	sqliteDeleteLineageCkpts  = "DELETE FROM checkpoints WHERE lineage_id = ?"
	sqliteDeleteLineageWrites = "DELETE FROM checkpoint_writes WHERE lineage_id = ?"
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
	lineageID := graph.GetLineageID(config)
	checkpointNS := graph.GetNamespace(config)
	checkpointID := graph.GetCheckpointID(config)
	if lineageID == "" {
		return nil, errors.New("lineage_id is required")
	}

	// Query checkpoint data (supports cross-namespace when checkpointNS is empty).
	row, err := s.queryCheckpointData(ctx, lineageID, checkpointNS, checkpointID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	// Determine the namespace to use when searching across namespaces.
	nsForTuple := checkpointNS
	if checkpointNS == "" {
		nsForTuple = row.namespace
	}

	// Build the tuple from retrieved data.
	return s.buildTuple(ctx, lineageID, nsForTuple, row.checkpointID, row.parentID,
		row.checkpointJSON, row.metadataJSON)
}

// queryCheckpointData retrieves checkpoint data from database.
type checkpointRow struct {
	checkpointJSON []byte
	metadataJSON   []byte
	parentID       string
	checkpointID   string
	namespace      string
}

func (s *Saver) queryCheckpointData(ctx context.Context, lineageID, checkpointNS,
	checkpointID string) (*checkpointRow, error) {
	var row *sql.Row
	if checkpointID == "" {
		row = s.db.QueryRowContext(ctx, sqliteSelectLatest, lineageID, checkpointNS)
	} else {
		row = s.db.QueryRowContext(ctx, sqliteSelectByID, lineageID, checkpointNS, checkpointID)
	}

	var r checkpointRow
	if err := row.Scan(&r.checkpointJSON, &r.metadataJSON, &r.parentID, &r.namespace, &r.checkpointID); err != nil {
		return nil, fmt.Errorf("select checkpoint failed: %w", err)
	}
	return &r, nil
}

// buildTuple constructs a CheckpointTuple from raw data.
func (s *Saver) buildTuple(ctx context.Context, lineageID, checkpointNS, checkpointID,
	parentID string, checkpointJSON, metadataJSON []byte) (*graph.CheckpointTuple, error) {

	var ckpt graph.Checkpoint
	if err := json.Unmarshal(checkpointJSON, &ckpt); err != nil {
		return nil, fmt.Errorf("unmarshal checkpoint: %w", err)
	}

	var meta graph.CheckpointMetadata
	if err := json.Unmarshal(metadataJSON, &meta); err != nil {
		return nil, fmt.Errorf("unmarshal metadata: %w", err)
	}

	cfg := graph.CreateCheckpointConfig(lineageID, checkpointID, checkpointNS)
	writes, err := s.loadWrites(ctx, lineageID, checkpointNS, checkpointID)
	if err != nil {
		return nil, err
	}

	var parentCfg map[string]any
	if parentID != "" {
		// Look up the parent's actual namespace. If not found, use empty namespace
		// to allow cross-namespace lookup by ID.
		parentNS, err := s.findCheckpointNamespace(ctx, lineageID, parentID)
		if err != nil {
			return nil, err
		}
		parentCfg = graph.CreateCheckpointConfig(lineageID, parentID, parentNS)
	}
	return &graph.CheckpointTuple{
		Config:        cfg,
		Checkpoint:    &ckpt,
		Metadata:      &meta,
		ParentConfig:  parentCfg,
		PendingWrites: writes,
	}, nil
}

// findCheckpointNamespace returns the namespace of the given checkpoint ID within a lineage.
// If not found, returns an empty string to indicate cross-namespace lookup should be used.
func (s *Saver) findCheckpointNamespace(ctx context.Context, lineageID, checkpointID string) (string, error) {
	if checkpointID == "" || lineageID == "" {
		return "", nil
	}
	row := s.db.QueryRowContext(
		ctx,
		"SELECT checkpoint_ns FROM checkpoints WHERE lineage_id = ? AND checkpoint_id = ? LIMIT 1",
		lineageID,
		checkpointID,
	)
	var ns string
	if err := row.Scan(&ns); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("lookup parent namespace: %w", err)
	}
	return ns, nil
}

// List returns checkpoints for the lineage/namespace, with optional filters.
func (s *Saver) List(
	ctx context.Context,
	config map[string]any,
	filter *graph.CheckpointFilter,
) ([]*graph.CheckpointTuple, error) {
	lineageID := graph.GetLineageID(config)
	checkpointNS := graph.GetNamespace(config)
	if lineageID == "" {
		return nil, errors.New("lineage_id is required")
	}

	// Query beforeTs if Before filter is specified.
	beforeTs, err := s.getBeforeTimestamp(ctx, lineageID, checkpointNS, filter)
	if err != nil {
		return nil, err
	}

	// Build and execute query.
	rows, err := s.executeListQuery(ctx, lineageID, checkpointNS, beforeTs, filter)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Process results.
	return s.processListResults(ctx, rows, lineageID, checkpointNS, filter)
}

// getBeforeTimestamp retrieves the timestamp for the Before filter.
func (s *Saver) getBeforeTimestamp(ctx context.Context, lineageID, checkpointNS string,
	filter *graph.CheckpointFilter) (*int64, error) {

	if filter == nil || filter.Before == nil {
		return nil, nil
	}

	beforeID := graph.GetCheckpointID(filter.Before)
	if beforeID == "" {
		return nil, nil
	}

	var row *sql.Row
	if checkpointNS == "" {
		// Cross-namespace lookup for before timestamp
		row = s.db.QueryRowContext(ctx,
			"SELECT ts FROM checkpoints WHERE lineage_id=? AND checkpoint_id=? ORDER BY ts DESC LIMIT 1",
			lineageID, beforeID,
		)
	} else {
		row = s.db.QueryRowContext(ctx,
			"SELECT ts FROM checkpoints WHERE lineage_id=? AND checkpoint_ns=? AND checkpoint_id=? LIMIT 1",
			lineageID, checkpointNS, beforeID,
		)
	}
	var ts int64
	if err := row.Scan(&ts); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get before timestamp: %w", err)
	}
	return &ts, nil
}

// executeListQuery builds and executes the list query.
func (s *Saver) executeListQuery(ctx context.Context, lineageID, checkpointNS string,
	beforeTs *int64, filter *graph.CheckpointFilter) (*sql.Rows, error) {

	var q string
	var args []any

	if checkpointNS == "" {
		// Cross-namespace listing
		q = "SELECT checkpoint_id, checkpoint_ns, ts FROM checkpoints WHERE lineage_id=?"
		args = []any{lineageID}
	} else {
		q = "SELECT checkpoint_id, ts FROM checkpoints WHERE lineage_id=? AND checkpoint_ns=?"
		args = []any{lineageID, checkpointNS}
	}

	if beforeTs != nil {
		q += " AND ts < ?"
		args = append(args, *beforeTs)
	}

	q += " ORDER BY ts DESC"

	if filter != nil && filter.Limit > 0 {
		q += " LIMIT ?"
		args = append(args, filter.Limit)
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("select checkpoints: %w", err)
	}
	return rows, nil
}

// processListResults processes the query results and applies filters.
func (s *Saver) processListResults(ctx context.Context, rows *sql.Rows,
	lineageID, checkpointNS string, filter *graph.CheckpointFilter) ([]*graph.CheckpointTuple, error) {

	var tuples []*graph.CheckpointTuple

	for rows.Next() {
		tuple, err := s.processSingleRow(ctx, rows, lineageID, checkpointNS)
		if err != nil {
			return nil, err
		}
		if tuple == nil {
			continue
		}

		// Apply metadata filter.
		if !s.matchesMetadataFilter(tuple, filter) {
			continue
		}

		tuples = append(tuples, tuple)

		// Check limit.
		if filter != nil && filter.Limit > 0 && len(tuples) >= filter.Limit {
			break
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iter checkpoints: %w", err)
	}
	return tuples, nil
}

// processSingleRow processes a single row from the query result.
func (s *Saver) processSingleRow(ctx context.Context, rows *sql.Rows,
	lineageID, checkpointNS string) (*graph.CheckpointTuple, error) {

	var checkpointID string
	var ts int64
	if checkpointNS == "" {
		var ns string
		if err := rows.Scan(&checkpointID, &ns, &ts); err != nil {
			return nil, fmt.Errorf("scan checkpoint (cross-ns): %w", err)
		}
		cfg := graph.CreateCheckpointConfig(lineageID, checkpointID, ns)
		tuple, err := s.GetTuple(ctx, cfg)
		if err != nil {
			return nil, err
		}
		return tuple, nil
	}

	if err := rows.Scan(&checkpointID, &ts); err != nil {
		return nil, fmt.Errorf("scan checkpoint: %w", err)
	}

	cfg := graph.CreateCheckpointConfig(lineageID, checkpointID, checkpointNS)
	tuple, err := s.GetTuple(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return tuple, nil
}

// matchesMetadataFilter checks if a tuple matches the metadata filter.
func (s *Saver) matchesMetadataFilter(tuple *graph.CheckpointTuple, filter *graph.CheckpointFilter) bool {
	// Treat nil or empty metadata map as "no metadata filter".
	if filter == nil || len(filter.Metadata) == 0 {
		return true
	}

	if tuple.Metadata == nil || tuple.Metadata.Extra == nil {
		return false
	}

	for key, value := range filter.Metadata {
		if tuple.Metadata.Extra[key] != value {
			return false
		}
	}
	return true
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
	// Use the ParentCheckpointID from the checkpoint itself, not from config.
	parentID := req.Checkpoint.ParentCheckpointID
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
	// Use UnixNano for better precision in ordering.
	ts := req.Checkpoint.Timestamp.UnixNano()
	if ts == 0 {
		// Ensure non-zero timestamp for ordering.
		ts = time.Now().UTC().UnixNano()
	}
	_, err = s.db.ExecContext(
		ctx,
		sqliteInsertCheckpoint,
		lineageID,
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
	return graph.CreateCheckpointConfig(lineageID, req.Checkpoint.ID, checkpointNS), nil
}

// PutWrites stores write entries for a checkpoint.
func (s *Saver) PutWrites(ctx context.Context, req graph.PutWritesRequest) error {
	lineageID := graph.GetLineageID(req.Config)
	checkpointNS := graph.GetNamespace(req.Config)
	checkpointID := graph.GetCheckpointID(req.Config)
	if lineageID == "" || checkpointID == "" {
		return errors.New("lineage_id and checkpoint_id are required")
	}
	for idx, w := range req.Writes {
		valueJSON, err := json.Marshal(w.Value)
		if err != nil {
			return fmt.Errorf("marshal write: %w", err)
		}
		// Use Sequence if available in the write, otherwise use index.
		seq := w.Sequence
		if seq == 0 {
			seq = int64(idx)
		}
		_, err = s.db.ExecContext(
			ctx,
			sqliteInsertWrite,
			lineageID,
			checkpointNS,
			checkpointID,
			req.TaskID,
			idx,
			w.Channel,
			valueJSON,
			req.TaskPath,
			seq,
		)
		if err != nil {
			return fmt.Errorf("insert write: %w", err)
		}
	}
	return nil
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

	// Start transaction.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Marshal checkpoint and metadata.
	checkpointJSON, err := json.Marshal(req.Checkpoint)
	if err != nil {
		return nil, fmt.Errorf("marshal checkpoint: %w", err)
	}
	metadataJSON, err := json.Marshal(req.Metadata)
	if err != nil {
		return nil, fmt.Errorf("marshal metadata: %w", err)
	}

	// Insert checkpoint.
	// Use the ParentCheckpointID from the checkpoint itself, not from config.
	parentID := req.Checkpoint.ParentCheckpointID
	_, err = tx.ExecContext(
		ctx,
		sqliteInsertCheckpoint,
		lineageID,
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

	// Insert pending writes with sequence numbers.
	for idx, w := range req.PendingWrites {
		valueJSON, err := json.Marshal(w.Value)
		if err != nil {
			return nil, fmt.Errorf("marshal write value: %w", err)
		}

		// Use Sequence if available, otherwise fallback to timestamp.
		seq := w.Sequence
		if seq == 0 {
			seq = time.Now().UnixNano()
		}

		_, err = tx.ExecContext(
			ctx,
			sqliteInsertWrite,
			lineageID,
			checkpointNS,
			req.Checkpoint.ID,
			w.TaskID,
			idx, // Use index as sequence number.
			w.Channel,
			valueJSON,
			"", // task_path.
			seq,
		)
		if err != nil {
			return nil, fmt.Errorf("insert write: %w", err)
		}
	}

	// Commit transaction.
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}

	// Return updated config with the new checkpoint ID.
	updatedConfig := graph.CreateCheckpointConfig(lineageID, req.Checkpoint.ID, checkpointNS)
	return updatedConfig, nil
}

// DeleteLineage deletes all checkpoints and writes for the lineage.
func (s *Saver) DeleteLineage(ctx context.Context, lineageID string) error {
	if lineageID == "" {
		return errors.New("lineage_id is required")
	}
	if _, err := s.db.ExecContext(ctx, sqliteDeleteLineageCkpts, lineageID); err != nil {
		return fmt.Errorf("delete checkpoints: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, sqliteDeleteLineageWrites, lineageID); err != nil {
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
	lineageID, checkpointNS, checkpointID string,
) ([]graph.PendingWrite, error) {
	rows, err := s.db.QueryContext(
		ctx,
		sqliteSelectWrites,
		lineageID,
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
		var seq int64
		if err := rows.Scan(&taskID, &idx, &channel, &valueJSON, &taskPath, &seq); err != nil {
			return nil, fmt.Errorf("scan write: %w", err)
		}
		var value any
		if err := json.Unmarshal(valueJSON, &value); err != nil {
			return nil, fmt.Errorf("unmarshal write: %w", err)
		}
		writes = append(writes, graph.PendingWrite{
			Channel:  channel,
			Value:    value,
			TaskID:   taskID,
			Sequence: seq,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iter writes: %w", err)
	}
	return writes, nil
}
