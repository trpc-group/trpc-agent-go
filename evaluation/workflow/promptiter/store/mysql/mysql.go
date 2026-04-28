//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/internal/mysqldb"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/store"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
)

type mysqlStore struct {
	db        storage.Client
	tableName string
}

// New creates a MySQL-backed PromptIter store.
func New(opts ...Option) (store.Store, error) {
	options := newOptions(opts...)
	db, err := mysqldb.BuildClient(options.dsn, options.instanceName, options.extraOptions)
	if err != nil {
		return nil, fmt.Errorf("create mysql client failed: %w", err)
	}
	store := &mysqlStore{
		db:        db,
		tableName: sqldb.BuildTableName(options.tablePrefix, tableNameRuns),
	}
	if !options.skipDBInit {
		ctx, cancel := context.WithTimeout(context.Background(), options.initTimeout)
		defer cancel()
		if err := ensureSchema(ctx, db, store.tableName); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("init database failed: %w", err)
		}
	}
	return store, nil
}

// Create persists one new PromptIter run.
func (s *mysqlStore) Create(ctx context.Context, run *engine.RunResult) error {
	if run == nil {
		return errors.New("promptiter run is nil")
	}
	if run.ID == "" {
		return errors.New("promptiter run id is empty")
	}
	payload, err := json.Marshal(run)
	if err != nil {
		return fmt.Errorf("marshal promptiter run %q: %w", run.ID, err)
	}
	query := fmt.Sprintf(
		"INSERT INTO %s (run_id, status, run_result) VALUES (?, ?, ?)",
		s.tableName,
	)
	if _, err := s.db.Exec(ctx, query, run.ID, string(run.Status), payload); err != nil {
		if mysqldb.IsDuplicateEntry(err) {
			return fmt.Errorf("run %q already exists", run.ID)
		}
		return fmt.Errorf("create run %q: %w", run.ID, err)
	}
	return nil
}

// Get loads one persisted PromptIter run by run ID.
func (s *mysqlStore) Get(ctx context.Context, runID string) (*engine.RunResult, error) {
	if runID == "" {
		return nil, errors.New("run id is empty")
	}
	var payload []byte
	query := fmt.Sprintf(
		"SELECT run_result FROM %s WHERE run_id = ?",
		s.tableName,
	)
	if err := s.db.QueryRow(ctx, []any{&payload}, query, runID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("run %q not found: %w", runID, os.ErrNotExist)
		}
		return nil, fmt.Errorf("load run %q: %w", runID, err)
	}
	var run engine.RunResult
	if err := json.Unmarshal(payload, &run); err != nil {
		return nil, fmt.Errorf("unmarshal run %q: %w", runID, err)
	}
	if run.ID == "" {
		run.ID = runID
	}
	return &run, nil
}

// Update persists changes to one existing PromptIter run.
func (s *mysqlStore) Update(ctx context.Context, run *engine.RunResult) error {
	if run == nil {
		return errors.New("promptiter run is nil")
	}
	if run.ID == "" {
		return errors.New("promptiter run id is empty")
	}
	payload, err := json.Marshal(run)
	if err != nil {
		return fmt.Errorf("marshal promptiter run %q: %w", run.ID, err)
	}
	query := fmt.Sprintf(
		"UPDATE %s SET status = ?, run_result = ?, updated_at = CURRENT_TIMESTAMP(6) WHERE run_id = ?",
		s.tableName,
	)
	result, err := s.db.Exec(ctx, query, string(run.Status), payload, run.ID)
	if err != nil {
		return fmt.Errorf("update run %q: %w", run.ID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected for run %q: %w", run.ID, err)
	}
	if affected == 0 {
		return fmt.Errorf("run %q not found: %w", run.ID, os.ErrNotExist)
	}
	return nil
}

// Close releases the underlying MySQL client.
func (s *mysqlStore) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}
