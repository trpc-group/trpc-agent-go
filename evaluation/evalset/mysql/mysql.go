//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/epochtime"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/internal/clone"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/internal/mysqldb"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
)

var _ evalset.Manager = (*manager)(nil)

type manager struct {
	opts   options
	db     storage.Client
	tables mysqldb.Tables
}

// New creates a MySQL-backed eval set manager.
func New(opts ...Option) (evalset.Manager, error) {
	options := newOptions(opts...)
	db, err := mysqldb.BuildClient(options.dsn, options.instanceName, options.extraOptions)
	if err != nil {
		return nil, fmt.Errorf("create mysql client failed: %w", err)
	}
	tables := mysqldb.BuildTables(options.tablePrefix)
	m := &manager{
		opts:   *options,
		db:     db,
		tables: tables,
	}
	if !options.skipDBInit {
		ctx, cancel := context.WithTimeout(context.Background(), options.initTimeout)
		defer cancel()
		if err := mysqldb.EnsureSchema(ctx, db, tables, mysqldb.SchemaEvalSets|mysqldb.SchemaEvalCases); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("init database failed: %w", err)
		}
	}
	return m, nil
}

// Close implements evalset.Manager.
func (m *manager) Close() error {
	if m.db == nil {
		return nil
	}
	return m.db.Close()
}

// ensureEvalSetExists checks whether the specified eval set exists in MySQL.
func (m *manager) ensureEvalSetExists(ctx context.Context, appName, evalSetID string) error {
	var one int
	err := m.db.QueryRow(ctx, []any{&one},
		fmt.Sprintf("SELECT 1 FROM %s WHERE app_name = ? AND eval_set_id = ?", m.tables.EvalSets),
		appName, evalSetID,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("eval set %s.%s not found: %w", appName, evalSetID, os.ErrNotExist)
		}
		return err
	}
	return nil
}

// Get retrieves an evaluation set and its cases from MySQL.
func (m *manager) Get(ctx context.Context, appName, evalSetID string) (*evalset.EvalSet, error) {
	if appName == "" {
		return nil, errors.New("app name is empty")
	}
	if evalSetID == "" {
		return nil, errors.New("eval set id is empty")
	}
	var (
		name       string
		desc       sql.NullString
		createdAt  time.Time
		evalCases  []*evalset.EvalCase
		evalSetSQL = fmt.Sprintf(
			"SELECT name, description, created_at FROM %s WHERE app_name = ? AND eval_set_id = ?",
			m.tables.EvalSets,
		)
	)
	if err := m.db.QueryRow(ctx, []any{&name, &desc, &createdAt}, evalSetSQL, appName, evalSetID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("eval set %s.%s not found: %w", appName, evalSetID, os.ErrNotExist)
		}
		return nil, fmt.Errorf("get eval set %s.%s: %w", appName, evalSetID, err)
	}
	casesSQL := fmt.Sprintf(
		"SELECT eval_case FROM %s WHERE app_name = ? AND eval_set_id = ? ORDER BY id ASC",
		m.tables.EvalCases,
	)
	if err := m.db.Query(ctx, func(rows *sql.Rows) error {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return err
		}
		var c evalset.EvalCase
		if err := json.Unmarshal(payload, &c); err != nil {
			return fmt.Errorf("unmarshal eval case: %w", err)
		}
		evalCases = append(evalCases, &c)
		return nil
	}, casesSQL, appName, evalSetID); err != nil {
		return nil, fmt.Errorf("list eval cases for eval set %s.%s: %w", appName, evalSetID, err)
	}
	if evalCases == nil {
		evalCases = []*evalset.EvalCase{}
	}
	result := &evalset.EvalSet{
		EvalSetID:         evalSetID,
		Name:              name,
		Description:       desc.String,
		EvalCases:         evalCases,
		CreationTimestamp: &epochtime.EpochTime{Time: createdAt},
	}
	return result, nil
}

// Create creates a new evaluation set in MySQL.
func (m *manager) Create(ctx context.Context, appName, evalSetID string) (*evalset.EvalSet, error) {
	if appName == "" {
		return nil, errors.New("app name is empty")
	}
	if evalSetID == "" {
		return nil, errors.New("eval set id is empty")
	}
	query := fmt.Sprintf(
		"INSERT INTO %s (app_name, eval_set_id, name, description) VALUES (?, ?, ?, ?)",
		m.tables.EvalSets,
	)
	if _, err := m.db.Exec(ctx, query, appName, evalSetID, evalSetID, ""); err != nil {
		if mysqldb.IsDuplicateEntry(err) {
			return nil, fmt.Errorf("eval set %s.%s already exists", appName, evalSetID)
		}
		return nil, fmt.Errorf("create eval set %s.%s: %w", appName, evalSetID, err)
	}
	now := time.Now()
	return &evalset.EvalSet{
		EvalSetID:         evalSetID,
		Name:              evalSetID,
		EvalCases:         []*evalset.EvalCase{},
		CreationTimestamp: &epochtime.EpochTime{Time: now},
	}, nil
}

// List lists evaluation set IDs for the given app from MySQL.
func (m *manager) List(ctx context.Context, appName string) ([]string, error) {
	if appName == "" {
		return nil, errors.New("app name is empty")
	}
	query := fmt.Sprintf(
		"SELECT eval_set_id FROM %s WHERE app_name = ? ORDER BY eval_set_id ASC",
		m.tables.EvalSets,
	)
	var ids []string
	if err := m.db.Query(ctx, func(rows *sql.Rows) error {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
		return nil
	}, query, appName); err != nil {
		return nil, fmt.Errorf("list eval sets for app %s: %w", appName, err)
	}
	if ids == nil {
		ids = []string{}
	}
	return ids, nil
}

// Delete deletes an evaluation set and its cases from MySQL.
func (m *manager) Delete(ctx context.Context, appName, evalSetID string) error {
	if appName == "" {
		return errors.New("app name is empty")
	}
	if evalSetID == "" {
		return errors.New("eval set id is empty")
	}
	return m.db.Transaction(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			fmt.Sprintf("DELETE FROM %s WHERE app_name = ? AND eval_set_id = ?", m.tables.EvalCases),
			appName, evalSetID,
		)
		if err != nil {
			return fmt.Errorf("delete eval cases failed: %w", err)
		}
		res, err := tx.ExecContext(ctx,
			fmt.Sprintf("DELETE FROM %s WHERE app_name = ? AND eval_set_id = ?", m.tables.EvalSets),
			appName, evalSetID,
		)
		if err != nil {
			return fmt.Errorf("delete eval set failed: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("get rows affected failed: %w", err)
		}
		if affected == 0 {
			return fmt.Errorf("eval set %s.%s not found: %w", appName, evalSetID, os.ErrNotExist)
		}
		return nil
	})
}

// GetCase retrieves an evaluation case from MySQL.
func (m *manager) GetCase(ctx context.Context, appName, evalSetID, evalCaseID string) (*evalset.EvalCase, error) {
	if appName == "" {
		return nil, errors.New("app name is empty")
	}
	if evalSetID == "" {
		return nil, errors.New("eval set id is empty")
	}
	if evalCaseID == "" {
		return nil, errors.New("eval case id is empty")
	}
	if err := m.ensureEvalSetExists(ctx, appName, evalSetID); err != nil {
		return nil, err
	}
	var payload []byte
	query := fmt.Sprintf(
		"SELECT eval_case FROM %s WHERE app_name = ? AND eval_set_id = ? AND eval_id = ?",
		m.tables.EvalCases,
	)
	if err := m.db.QueryRow(ctx, []any{&payload}, query, appName, evalSetID, evalCaseID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("eval case %s.%s.%s not found: %w", appName, evalSetID, evalCaseID, os.ErrNotExist)
		}
		return nil, fmt.Errorf("get eval case %s.%s.%s: %w", appName, evalSetID, evalCaseID, err)
	}
	var c evalset.EvalCase
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, fmt.Errorf("unmarshal eval case %s.%s.%s: %w", appName, evalSetID, evalCaseID, err)
	}
	return &c, nil
}

// AddCase adds a new evaluation case to MySQL.
func (m *manager) AddCase(ctx context.Context, appName, evalSetID string, evalCase *evalset.EvalCase) error {
	if appName == "" {
		return errors.New("app name is empty")
	}
	if evalSetID == "" {
		return errors.New("eval set id is empty")
	}
	if evalCase == nil {
		return errors.New("evalCase is nil")
	}
	if evalCase.EvalID == "" {
		return errors.New("evalCase.EvalID is empty")
	}
	if err := m.ensureEvalSetExists(ctx, appName, evalSetID); err != nil {
		return err
	}
	cloned, err := clone.Clone(evalCase)
	if err != nil {
		return fmt.Errorf("clone evalcase: %w", err)
	}
	now := time.Now()
	if cloned.CreationTimestamp == nil {
		cloned.CreationTimestamp = &epochtime.EpochTime{Time: now}
	}
	for _, invocation := range cloned.Conversation {
		if invocation == nil {
			continue
		}
		if invocation.CreationTimestamp == nil {
			invocation.CreationTimestamp = &epochtime.EpochTime{Time: now}
		}
	}
	for _, invocation := range cloned.ActualConversation {
		if invocation == nil {
			continue
		}
		if invocation.CreationTimestamp == nil {
			invocation.CreationTimestamp = &epochtime.EpochTime{Time: now}
		}
	}
	payload, err := json.Marshal(cloned)
	if err != nil {
		return fmt.Errorf("marshal evalcase: %w", err)
	}
	query := fmt.Sprintf(
		"INSERT INTO %s (app_name, eval_set_id, eval_id, eval_mode, eval_case) VALUES (?, ?, ?, ?, ?)",
		m.tables.EvalCases,
	)
	if _, err := m.db.Exec(ctx, query, appName, evalSetID, cloned.EvalID, cloned.EvalMode, payload); err != nil {
		if mysqldb.IsDuplicateEntry(err) {
			return fmt.Errorf("eval case %s.%s.%s already exists", appName, evalSetID, cloned.EvalID)
		}
		return fmt.Errorf("add eval case %s.%s.%s: %w", appName, evalSetID, cloned.EvalID, err)
	}
	return nil
}

// UpdateCase updates an existing evaluation case in MySQL.
func (m *manager) UpdateCase(ctx context.Context, appName, evalSetID string, evalCase *evalset.EvalCase) error {
	if appName == "" {
		return errors.New("app name is empty")
	}
	if evalSetID == "" {
		return errors.New("eval set id is empty")
	}
	if evalCase == nil {
		return errors.New("evalCase is nil")
	}
	if evalCase.EvalID == "" {
		return errors.New("evalCase.EvalID is empty")
	}
	if err := m.ensureEvalSetExists(ctx, appName, evalSetID); err != nil {
		return err
	}
	payload, err := json.Marshal(evalCase)
	if err != nil {
		return fmt.Errorf("marshal evalcase: %w", err)
	}
	query := fmt.Sprintf(
		"UPDATE %s SET eval_mode = ?, eval_case = ?, updated_at = CURRENT_TIMESTAMP(6) WHERE app_name = ? AND eval_set_id = ? AND eval_id = ?",
		m.tables.EvalCases,
	)
	res, err := m.db.Exec(ctx, query, evalCase.EvalMode, payload, appName, evalSetID, evalCase.EvalID)
	if err != nil {
		return fmt.Errorf("update eval case %s.%s.%s: %w", appName, evalSetID, evalCase.EvalID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("get rows affected failed: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("eval case %s.%s.%s not found: %w", appName, evalSetID, evalCase.EvalID, os.ErrNotExist)
	}
	return nil
}

// DeleteCase deletes an evaluation case from MySQL.
func (m *manager) DeleteCase(ctx context.Context, appName, evalSetID, evalCaseID string) error {
	if appName == "" {
		return errors.New("app name is empty")
	}
	if evalSetID == "" {
		return errors.New("eval set id is empty")
	}
	if evalCaseID == "" {
		return errors.New("eval case id is empty")
	}
	if err := m.ensureEvalSetExists(ctx, appName, evalSetID); err != nil {
		return err
	}
	query := fmt.Sprintf(
		"DELETE FROM %s WHERE app_name = ? AND eval_set_id = ? AND eval_id = ?",
		m.tables.EvalCases,
	)
	res, err := m.db.Exec(ctx, query, appName, evalSetID, evalCaseID)
	if err != nil {
		return fmt.Errorf("delete eval case %s.%s.%s: %w", appName, evalSetID, evalCaseID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("get rows affected failed: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("eval case %s.%s.%s not found: %w", appName, evalSetID, evalCaseID, os.ErrNotExist)
	}
	return nil
}
