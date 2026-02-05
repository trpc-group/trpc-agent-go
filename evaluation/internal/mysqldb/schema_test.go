//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mysqldb

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
)

type dummyResult struct{}

func (dummyResult) LastInsertId() (int64, error) { return 0, nil }

func (dummyResult) RowsAffected() (int64, error) { return 0, nil }

type recordingClient struct {
	queries []string
}

func (c *recordingClient) Exec(_ context.Context, query string, _ ...any) (sql.Result, error) {
	c.queries = append(c.queries, query)
	return dummyResult{}, nil
}

func (c *recordingClient) Query(_ context.Context, _ storage.NextFunc, _ string, _ ...any) error {
	return nil
}

func (c *recordingClient) QueryRow(_ context.Context, _ []any, _ string, _ ...any) error {
	return nil
}

func (c *recordingClient) Transaction(_ context.Context, _ storage.TxFunc, _ ...storage.TxOption) error {
	return nil
}

func (c *recordingClient) Close() error { return nil }

type scriptedClient struct {
	queries []string
	execFn  func(query string) error
}

func (c *scriptedClient) Exec(_ context.Context, query string, _ ...any) (sql.Result, error) {
	c.queries = append(c.queries, query)
	if c.execFn != nil {
		if err := c.execFn(query); err != nil {
			return dummyResult{}, err
		}
	}
	return dummyResult{}, nil
}

func (c *scriptedClient) Query(_ context.Context, _ storage.NextFunc, _ string, _ ...any) error {
	return nil
}

func (c *scriptedClient) QueryRow(_ context.Context, _ []any, _ string, _ ...any) error {
	return nil
}

func (c *scriptedClient) Transaction(_ context.Context, _ storage.TxFunc, _ ...storage.TxOption) error {
	return nil
}

func (c *scriptedClient) Close() error { return nil }

func containsCreateForTable(queries []string, table string) bool {
	needle := "CREATE TABLE IF NOT EXISTS " + table
	for _, q := range queries {
		if strings.Contains(q, needle) {
			return true
		}
	}
	return false
}

func containsCreateIndexForTable(queries []string, indexName string, table string) bool {
	needles := []string{
		"CREATE UNIQUE INDEX " + indexName + " ON " + table,
		"CREATE INDEX " + indexName + " ON " + table,
	}
	for _, q := range queries {
		for _, needle := range needles {
			if strings.Contains(q, needle) {
				return true
			}
		}
	}
	return false
}

func TestEnsureSchema_TargetSelection(t *testing.T) {
	ctx := context.Background()
	client := &recordingClient{}
	tables := BuildTables("test")

	err := EnsureSchema(ctx, client, tables, SchemaEvalSets|SchemaEvalSetResults)
	assert.NoError(t, err)
	assert.Len(t, client.queries, 7)
	assert.True(t, containsCreateForTable(client.queries, tables.EvalSets))
	assert.True(t, containsCreateForTable(client.queries, tables.EvalSetResults))
	assert.True(t, containsCreateIndexForTable(client.queries, "uniq_eval_sets_app_eval_set", tables.EvalSets))
	assert.True(t, containsCreateIndexForTable(client.queries, "idx_eval_sets_app_created", tables.EvalSets))
	assert.True(t, containsCreateIndexForTable(client.queries, "uniq_results_app_result_id", tables.EvalSetResults))
	assert.True(t, containsCreateIndexForTable(client.queries, "idx_results_app_created", tables.EvalSetResults))
	assert.True(t, containsCreateIndexForTable(client.queries, "idx_results_app_set_created", tables.EvalSetResults))
	assert.False(t, containsCreateForTable(client.queries, tables.EvalCases))
	assert.False(t, containsCreateForTable(client.queries, tables.Metrics))
	assert.False(t, containsCreateIndexForTable(client.queries, "uniq_eval_cases_app_set_case", tables.EvalCases))
	assert.False(t, containsCreateIndexForTable(client.queries, "idx_eval_cases_app_set_order", tables.EvalCases))
	assert.False(t, containsCreateIndexForTable(client.queries, "uniq_metrics_app_set_name", tables.Metrics))
	assert.False(t, containsCreateIndexForTable(client.queries, "idx_metrics_app_set", tables.Metrics))
}

func TestEnsureSchema_AllTargets(t *testing.T) {
	ctx := context.Background()
	client := &recordingClient{}
	tables := BuildTables("test_")

	err := EnsureSchema(ctx, client, tables, SchemaAll)
	assert.NoError(t, err)
	assert.Len(t, client.queries, 13)
	assert.True(t, containsCreateForTable(client.queries, tables.EvalSets))
	assert.True(t, containsCreateForTable(client.queries, tables.EvalCases))
	assert.True(t, containsCreateForTable(client.queries, tables.Metrics))
	assert.True(t, containsCreateForTable(client.queries, tables.EvalSetResults))
	assert.True(t, containsCreateIndexForTable(client.queries, "uniq_eval_sets_app_eval_set", tables.EvalSets))
	assert.True(t, containsCreateIndexForTable(client.queries, "idx_eval_sets_app_created", tables.EvalSets))
	assert.True(t, containsCreateIndexForTable(client.queries, "uniq_eval_cases_app_set_case", tables.EvalCases))
	assert.True(t, containsCreateIndexForTable(client.queries, "idx_eval_cases_app_set_order", tables.EvalCases))
	assert.True(t, containsCreateIndexForTable(client.queries, "uniq_metrics_app_set_name", tables.Metrics))
	assert.True(t, containsCreateIndexForTable(client.queries, "idx_metrics_app_set", tables.Metrics))
	assert.True(t, containsCreateIndexForTable(client.queries, "uniq_results_app_result_id", tables.EvalSetResults))
	assert.True(t, containsCreateIndexForTable(client.queries, "idx_results_app_created", tables.EvalSetResults))
	assert.True(t, containsCreateIndexForTable(client.queries, "idx_results_app_set_created", tables.EvalSetResults))
}

func TestEnsureSchema_NoTarget(t *testing.T) {
	ctx := context.Background()
	client := &recordingClient{}
	tables := BuildTables("test")

	err := EnsureSchema(ctx, client, tables, 0)
	assert.Error(t, err)
}

func TestEnsureSchema_IgnoresDuplicateIndexName(t *testing.T) {
	ctx := context.Background()
	client := &scriptedClient{
		execFn: func(query string) error {
			if strings.Contains(query, "CREATE INDEX") || strings.Contains(query, "CREATE UNIQUE INDEX") {
				return &mysql.MySQLError{Number: sqldb.MySQLErrDuplicateKeyName, Message: "Duplicate key name 'idx_test'"}
			}
			return nil
		},
	}
	tables := BuildTables("test")

	err := EnsureSchema(ctx, client, tables, SchemaEvalSets)
	assert.NoError(t, err)
}

func TestEnsureSchema_IndexError(t *testing.T) {
	ctx := context.Background()
	client := &scriptedClient{
		execFn: func(query string) error {
			if strings.Contains(query, "CREATE INDEX") || strings.Contains(query, "CREATE UNIQUE INDEX") {
				return errors.New("boom")
			}
			return nil
		},
	}
	tables := BuildTables("test")

	err := EnsureSchema(ctx, client, tables, SchemaEvalSets)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "create index")
}

func TestIsDuplicateKeyName(t *testing.T) {
	assert.False(t, IsDuplicateKeyName(errors.New("boom")))
	assert.False(t, IsDuplicateKeyName(&mysql.MySQLError{Number: sqldb.MySQLErrDuplicateEntry}))
	assert.True(t, IsDuplicateKeyName(&mysql.MySQLError{Number: sqldb.MySQLErrDuplicateKeyName}))
}
