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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
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

func containsCreateForTable(queries []string, table string) bool {
	needle := "CREATE TABLE IF NOT EXISTS " + table
	for _, q := range queries {
		if strings.Contains(q, needle) {
			return true
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
	assert.Len(t, client.queries, 2)
	assert.True(t, containsCreateForTable(client.queries, tables.EvalSets))
	assert.True(t, containsCreateForTable(client.queries, tables.EvalSetResults))
	assert.False(t, containsCreateForTable(client.queries, tables.EvalCases))
	assert.False(t, containsCreateForTable(client.queries, tables.Metrics))
}

func TestEnsureSchema_AllTargets(t *testing.T) {
	ctx := context.Background()
	client := &recordingClient{}
	tables := BuildTables("test_")

	err := EnsureSchema(ctx, client, tables, SchemaAll)
	assert.NoError(t, err)
	assert.Len(t, client.queries, 4)
	assert.True(t, containsCreateForTable(client.queries, tables.EvalSets))
	assert.True(t, containsCreateForTable(client.queries, tables.EvalCases))
	assert.True(t, containsCreateForTable(client.queries, tables.Metrics))
	assert.True(t, containsCreateForTable(client.queries, tables.EvalSetResults))
}

func TestEnsureSchema_NoTarget(t *testing.T) {
	ctx := context.Background()
	client := &recordingClient{}
	tables := BuildTables("test")

	err := EnsureSchema(ctx, client, tables, 0)
	assert.Error(t, err)
}
