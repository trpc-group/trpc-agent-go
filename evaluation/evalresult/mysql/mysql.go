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

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/epochtime"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/internal/mysqldb"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
)

var _ evalresult.Manager = (*manager)(nil)

type manager struct {
	opts   options
	db     storage.Client
	tables mysqldb.Tables
}

// New creates a MySQL-backed eval result manager.
func New(opts ...Option) (evalresult.Manager, error) {
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
		if err := mysqldb.EnsureSchema(ctx, db, tables, mysqldb.SchemaEvalSetResults); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("init database failed: %w", err)
		}
	}
	return m, nil
}

// Close implements evalresult.Manager.
func (m *manager) Close() error {
	if m.db == nil {
		return nil
	}
	return m.db.Close()
}

// Save upserts an evaluation result into MySQL.
func (m *manager) Save(ctx context.Context, appName string, evalSetResult *evalresult.EvalSetResult) (string, error) {
	if appName == "" {
		return "", errors.New("app name is empty")
	}
	if evalSetResult == nil {
		return "", errors.New("eval set result is nil")
	}
	if evalSetResult.EvalSetID == "" {
		return "", errors.New("the eval set id of eval set result is empty")
	}
	evalSetResultID := evalSetResult.EvalSetResultID
	if evalSetResultID == "" {
		evalSetResultID = fmt.Sprintf("%s_%s_%s", appName, evalSetResult.EvalSetID, uuid.New().String())
	}
	evalSetResultName := evalSetResult.EvalSetResultName
	if evalSetResultName == "" {
		evalSetResultName = evalSetResultID
	}
	caseResults := evalSetResult.EvalCaseResults
	if caseResults == nil {
		caseResults = []*evalresult.EvalCaseResult{}
	}
	casePayload, err := json.Marshal(caseResults)
	if err != nil {
		return "", fmt.Errorf("marshal eval case results: %w", err)
	}
	var summaryPayload any
	if evalSetResult.Summary != nil {
		summaryBytes, err := json.Marshal(evalSetResult.Summary)
		if err != nil {
			return "", fmt.Errorf("marshal summary: %w", err)
		}
		summaryPayload = summaryBytes
	}
	query := fmt.Sprintf(
		`INSERT INTO %s (app_name, eval_set_result_id, eval_set_id, eval_set_result_name, eval_case_results, summary)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE
		   eval_set_id = VALUES(eval_set_id),
		   eval_set_result_name = VALUES(eval_set_result_name),
		   eval_case_results = VALUES(eval_case_results),
		   summary = VALUES(summary),
		   updated_at = CURRENT_TIMESTAMP(6)`,
		m.tables.EvalSetResults,
	)
	if _, err := m.db.Exec(ctx, query, appName, evalSetResultID, evalSetResult.EvalSetID, evalSetResultName, casePayload, summaryPayload); err != nil {
		return "", fmt.Errorf("store eval set result %s.%s: %w", appName, evalSetResultID, err)
	}
	return evalSetResultID, nil
}

// Get loads an evaluation result from MySQL.
func (m *manager) Get(ctx context.Context, appName, evalSetResultID string) (*evalresult.EvalSetResult, error) {
	if appName == "" {
		return nil, errors.New("app name is empty")
	}
	if evalSetResultID == "" {
		return nil, errors.New("eval set result id is empty")
	}
	var (
		evalSetID   string
		name        string
		casePayload []byte
		summary     sql.NullString
		createdAt   time.Time
	)
	query := fmt.Sprintf(
		"SELECT eval_set_id, eval_set_result_name, eval_case_results, summary, created_at FROM %s WHERE app_name = ? AND eval_set_result_id = ?",
		m.tables.EvalSetResults,
	)
	if err := m.db.QueryRow(ctx, []any{&evalSetID, &name, &casePayload, &summary, &createdAt}, query, appName, evalSetResultID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("eval set result %s.%s not found: %w", appName, evalSetResultID, os.ErrNotExist)
		}
		return nil, fmt.Errorf("load eval set result %s.%s: %w", appName, evalSetResultID, err)
	}
	var cases []*evalresult.EvalCaseResult
	if err := json.Unmarshal(casePayload, &cases); err != nil {
		return nil, fmt.Errorf("unmarshal eval case results %s.%s: %w", appName, evalSetResultID, err)
	}
	if cases == nil {
		cases = []*evalresult.EvalCaseResult{}
	}
	var summaryObj *evalresult.EvalSetResultSummary
	if summary.Valid && summary.String != "" {
		var s evalresult.EvalSetResultSummary
		if err := json.Unmarshal([]byte(summary.String), &s); err != nil {
			return nil, fmt.Errorf("unmarshal summary %s.%s: %w", appName, evalSetResultID, err)
		}
		summaryObj = &s
	}
	return &evalresult.EvalSetResult{
		EvalSetResultID:   evalSetResultID,
		EvalSetResultName: name,
		EvalSetID:         evalSetID,
		EvalCaseResults:   cases,
		Summary:           summaryObj,
		CreationTimestamp: &epochtime.EpochTime{Time: createdAt},
	}, nil
}

// List lists evaluation result IDs for the given app from MySQL.
func (m *manager) List(ctx context.Context, appName string) ([]string, error) {
	if appName == "" {
		return nil, errors.New("app name is empty")
	}
	query := fmt.Sprintf(
		"SELECT eval_set_result_id FROM %s WHERE app_name = ? ORDER BY created_at DESC",
		m.tables.EvalSetResults,
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
		return nil, fmt.Errorf("list eval set results for app %s: %w", appName, err)
	}
	if ids == nil {
		ids = []string{}
	}
	return ids, nil
}
