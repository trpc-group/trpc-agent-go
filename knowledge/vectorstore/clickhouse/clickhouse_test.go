//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package clickhouse

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"

	storage "trpc.group/trpc-go/trpc-agent-go/storage/clickhouse"
)

// vsWithClient builds a VectorStore with a mock client and default options
// (table "docs", dimension 3), plus any additional options.
func vsWithClient(client storage.Client, opts ...Option) *VectorStore {
	o := defaultOptions
	o.tableName = "docs"
	o.vectorDimension = 3
	for _, opt := range opts {
		opt(&o)
	}
	return &VectorStore{client: client, option: o}
}

func testDoc() *document.Document {
	return &document.Document{
		ID:       "doc1",
		Name:     "Doc One",
		Content:  "hello world",
		Metadata: map[string]any{"category": "news"},
	}
}

func TestNewWithDSN(t *testing.T) {
	c := &mockClient{}
	original := storage.GetClientBuilder()
	storage.SetClientBuilder(func(opts ...storage.ClientBuilderOpt) (storage.Client, error) { return c, nil })
	defer storage.SetClientBuilder(original)

	vs, err := New(WithDSN("clickhouse://mock:9000/db"), WithTableName("docs"), WithVectorDimension(3))
	require.NoError(t, err)
	require.NotNil(t, vs)
	defer vs.Close()

	// autoCreateTable ran the CREATE TABLE statement.
	require.Len(t, c.execCalls, 1)
	assert.Contains(t, c.execCalls[0].query, "CREATE TABLE IF NOT EXISTS docs")
}

func TestNewWithInstance(t *testing.T) {
	c := &mockClient{}
	storage.RegisterClickHouseInstance("test-new-instance", storage.WithClientBuilderDSN("clickhouse://mock:9000/db"))
	original := storage.GetClientBuilder()
	storage.SetClientBuilder(func(opts ...storage.ClientBuilderOpt) (storage.Client, error) { return c, nil })
	defer storage.SetClientBuilder(original)

	vs, err := New(WithInstanceName("test-new-instance"), WithTableName("docs"), WithVectorDimension(3))
	require.NoError(t, err)
	require.NotNil(t, vs)
	defer vs.Close()
}

// TestNewInstanceHonorsExtraOptions asserts that WithExtraOptions reaches the
// client builder on the named-instance path, not only on the DSN path.
func TestNewInstanceHonorsExtraOptions(t *testing.T) {
	c := &mockClient{}
	registered := []storage.ClientBuilderOpt{storage.WithClientBuilderDSN("clickhouse://mock:9000/db")}
	storage.RegisterClickHouseInstance("test-extra-options", registered...)
	original := storage.GetClientBuilder()
	var seen storage.ClientBuilderOpts
	storage.SetClientBuilder(func(opts ...storage.ClientBuilderOpt) (storage.Client, error) {
		for _, o := range opts {
			o(&seen)
		}
		return c, nil
	})
	defer storage.SetClientBuilder(original)

	vs, err := New(
		WithInstanceName("test-extra-options"),
		WithTableName("docs"),
		WithVectorDimension(3),
		WithExtraOptions("extra-1"),
	)
	require.NoError(t, err)
	defer vs.Close()

	assert.Equal(t, "clickhouse://mock:9000/db", seen.DSN)
	assert.Contains(t, seen.ExtraOptions, "extra-1")
	// The registered instance options must not be mutated by appending.
	assert.Len(t, registered, 1)
}

func TestNewErrors(t *testing.T) {
	// No DSN / instance.
	_, err := New(WithTableName("docs"), WithVectorDimension(3))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must specify one of")

	// Missing instance.
	_, err = New(WithInstanceName("test-missing-instance"), WithTableName("docs"), WithVectorDimension(3))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not registered")

	// Validation error.
	_, err = New(WithDSN("clickhouse://x"), WithVectorDimension(3))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "table name is required")

	// autoCreateTable=false skips table creation.
	c := &mockClient{}
	original := storage.GetClientBuilder()
	storage.SetClientBuilder(func(opts ...storage.ClientBuilderOpt) (storage.Client, error) { return c, nil })
	defer storage.SetClientBuilder(original)
	vs, err := New(WithDSN("clickhouse://x"), WithTableName("docs"), WithVectorDimension(3), WithAutoCreateTable(false))
	require.NoError(t, err)
	defer vs.Close()
	assert.Empty(t, c.execCalls)
}

func TestAdd(t *testing.T) {
	c := &mockClient{}
	vs := vsWithClient(c)
	err := vs.Add(context.Background(), testDoc(), []float64{1, 2, 3})
	require.NoError(t, err)
	require.Len(t, c.execCalls, 1)
	assert.Contains(t, c.execCalls[0].query, "INSERT INTO docs")
	require.Len(t, c.execCalls[0].args, 7)
	assert.Equal(t, "doc1", c.execCalls[0].args[0])
	assert.Equal(t, "Doc One", c.execCalls[0].args[1])
	assert.Equal(t, []float64{1, 2, 3}, c.execCalls[0].args[3])
}

func TestAddErrors(t *testing.T) {
	c := &mockClient{}
	vs := vsWithClient(c)

	assert.ErrorIs(t, vs.Add(context.Background(), nil, nil), errDocumentRequired)
	assert.ErrorIs(t, vs.Add(context.Background(), &document.Document{}, nil), errDocumentIDRequired)
	err := vs.Add(context.Background(), testDoc(), []float64{1, 2})
	assert.ErrorIs(t, err, errVectorDimMismatch)
	assert.Empty(t, c.execCalls)

	// Exec error propagates.
	c.execFunc = func(ctx context.Context, q string, a ...any) error { return errors.New("boom") }
	err = vs.Add(context.Background(), testDoc(), []float64{1, 2, 3})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insert")
}

func TestGet(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	c := &mockClient{}
	c.queryFunc = func(ctx context.Context, q string, a ...any) (driver.Rows, error) {
		return newMockRows([][]any{{"doc1", "Doc One", "hello world", []float64{1, 2, 3}, `{"category":"news"}`, now, now}}), nil
	}
	vs := vsWithClient(c)
	doc, emb, err := vs.Get(context.Background(), "doc1")
	require.NoError(t, err)
	assert.Equal(t, "doc1", doc.ID)
	assert.Equal(t, "Doc One", doc.Name)
	assert.Equal(t, "news", doc.Metadata["category"])
	assert.Equal(t, []float64{1, 2, 3}, emb)
	assert.Equal(t, now, doc.CreatedAt)
}

func TestGetErrors(t *testing.T) {
	c := &mockClient{}
	vs := vsWithClient(c)

	_, _, err := vs.Get(context.Background(), "")
	assert.ErrorIs(t, err, errDocumentIDRequired)

	// Not found (no rows).
	c.queryFunc = func(ctx context.Context, q string, a ...any) (driver.Rows, error) {
		return newMockRows(nil), nil
	}
	_, _, err = vs.Get(context.Background(), "missing")
	assert.ErrorIs(t, err, errNotFound)

	// Query error.
	c.queryFunc = func(ctx context.Context, q string, a ...any) (driver.Rows, error) {
		return nil, errors.New("boom")
	}
	_, _, err = vs.Get(context.Background(), "doc1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get")
}

func TestGetRowsErr(t *testing.T) {
	// A row-stream iteration error must be reported as an error, not as
	// not-found.
	c := &mockClient{}
	c.queryFunc = func(ctx context.Context, q string, a ...any) (driver.Rows, error) {
		return &mockRows{current: -1, err: errors.New("iteration failed")}, nil
	}
	vs := vsWithClient(c)
	_, _, err := vs.Get(context.Background(), "doc1")
	require.Error(t, err)
	assert.NotErrorIs(t, err, errNotFound)
	assert.Contains(t, err.Error(), "iteration failed")
}

func TestUpdate(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	c := &mockClient{}
	c.queryFunc = func(ctx context.Context, q string, a ...any) (driver.Rows, error) {
		return newMockRows([][]any{{"doc1", "old", "old content", []float64{9, 9, 9}, "{}", now, now}}), nil
	}
	vs := vsWithClient(c)
	doc := testDoc()
	doc.Content = "new content"
	err := vs.Update(context.Background(), doc, []float64{1, 2, 3})
	require.NoError(t, err)
	// One SELECT (Get) + one INSERT (update).
	require.Len(t, c.queryCalls, 1)
	require.Len(t, c.execCalls, 1)
	assert.Contains(t, c.execCalls[0].query, "INSERT INTO docs")
	// created_at preserved from existing doc.
	require.Len(t, c.execCalls[0].args, 7)
	assert.Equal(t, now, c.execCalls[0].args[5])
}

func TestUpdatePreservesEmbeddingWhenEmpty(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	c := &mockClient{}
	c.queryFunc = func(ctx context.Context, q string, a ...any) (driver.Rows, error) {
		return newMockRows([][]any{{"doc1", "old", "old content", []float64{9, 9, 9}, "{}", now, now}}), nil
	}
	vs := vsWithClient(c)
	err := vs.Update(context.Background(), testDoc(), nil)
	require.NoError(t, err)
	require.Len(t, c.execCalls, 1)
	assert.Equal(t, []float64{9, 9, 9}, c.execCalls[0].args[3])
}

func TestUpdateErrors(t *testing.T) {
	c := &mockClient{}
	vs := vsWithClient(c)
	assert.ErrorIs(t, vs.Update(context.Background(), nil, nil), errDocumentRequired)
	assert.ErrorIs(t, vs.Update(context.Background(), &document.Document{}, nil), errDocumentIDRequired)
	err := vs.Update(context.Background(), testDoc(), []float64{1, 2})
	assert.ErrorIs(t, err, errVectorDimMismatch)

	// Not found.
	c.queryFunc = func(ctx context.Context, q string, a ...any) (driver.Rows, error) {
		return newMockRows(nil), nil
	}
	err = vs.Update(context.Background(), testDoc(), nil)
	assert.ErrorIs(t, err, errNotFound)
}

func TestDelete(t *testing.T) {
	c := &mockClient{}
	vs := vsWithClient(c)
	err := vs.Delete(context.Background(), "doc1")
	require.NoError(t, err)
	require.Len(t, c.execCalls, 1)
	assert.Contains(t, c.execCalls[0].query, "ALTER TABLE docs DELETE WHERE id = ?")
	assert.Equal(t, []any{"doc1"}, c.execCalls[0].args)

	assert.ErrorIs(t, vs.Delete(context.Background(), ""), errDocumentIDRequired)

	// Exec error propagates.
	c.execFunc = func(ctx context.Context, q string, a ...any) error { return errors.New("delete failed") }
	err = vs.Delete(context.Background(), "doc1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete")
}

func TestNewInitTableError(t *testing.T) {
	c := &mockClient{}
	c.execFunc = func(ctx context.Context, q string, a ...any) error { return errors.New("create failed") }
	original := storage.GetClientBuilder()
	storage.SetClientBuilder(func(opts ...storage.ClientBuilderOpt) (storage.Client, error) { return c, nil })
	defer storage.SetClientBuilder(original)

	_, err := New(WithDSN("clickhouse://x"), WithTableName("docs"), WithVectorDimension(3))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create table")
}

func TestInsertArgsTypeError(t *testing.T) {
	vs := vsWithClient(&mockClient{}, WithFilterFields(
		FilterFieldSpec{Name: "count", Type: FilterFieldInt64},
	))
	r := &row{
		id:        "doc1",
		name:      "n",
		content:   "c",
		embedding: []float64{1, 2, 3},
		metadata:  map[string]any{"count": "not-an-int"},
	}
	_, err := vs.insertArgs(r)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "count")
}

func TestClose(t *testing.T) {
	// nil client.
	vs := &VectorStore{}
	assert.NoError(t, vs.Close())

	// Close error propagates.
	c := &mockClient{closeFunc: func() error { return errors.New("boom") }}
	vs = vsWithClient(c)
	assert.Error(t, vs.Close())
}

func TestSQLBuilders(t *testing.T) {
	vs := vsWithClient(&mockClient{}, WithFilterFields(
		FilterFieldSpec{Name: "category", Type: FilterFieldString},
		FilterFieldSpec{Name: "count", Type: FilterFieldInt64},
	))

	create := vs.buildCreateTableSQL()
	assert.Contains(t, create, "CREATE TABLE IF NOT EXISTS docs")
	assert.Contains(t, create, "category String")
	assert.Contains(t, create, "count Int64")
	assert.Contains(t, create, "ENGINE = ReplacingMergeTree(updated_at)")
	assert.Contains(t, create, "ORDER BY id")

	insert := vs.buildInsertSQL()
	assert.Contains(t, insert, "INSERT INTO docs (")
	assert.Contains(t, insert, "category, count")
	assert.Equal(t, 9, strings.Count(insert, "?"))

	sel := vs.buildSelectSQL()
	assert.Contains(t, sel, "SELECT id, name, content, embedding, metadata, created_at, updated_at, category, count FROM docs FINAL")

	cols := vs.selectColumns()
	assert.Len(t, cols, 9)
	assert.Equal(t, "category", cols[7])
	assert.Equal(t, "count", cols[8])
}

func TestInsertArgs(t *testing.T) {
	vs := vsWithClient(&mockClient{}, WithFilterFields(
		FilterFieldSpec{Name: "category", Type: FilterFieldString},
	))
	r := &row{
		id:        "doc1",
		name:      "n",
		content:   "c",
		embedding: []float64{1, 2, 3},
		metadata:  map[string]any{"category": "news"},
		createdAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		updatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	args, err := vs.insertArgs(r)
	require.NoError(t, err)
	require.Len(t, args, 8)
	assert.Equal(t, "doc1", args[0])
	assert.Equal(t, "news", args[7])
	// metadata is JSON-encoded at index 4.
	assert.Contains(t, args[4].(string), `"category":"news"`)
}

func TestScanRow(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	vs := vsWithClient(&mockClient{}, WithFilterFields(
		FilterFieldSpec{Name: "category", Type: FilterFieldString},
	))
	// The metadata JSON always carries the declared filter fields, because
	// insertArgs writes them into both the JSON blob and the typed column.
	rows := newMockRows([][]any{{"doc1", "n", "c", []float64{1, 2, 3}, `{"x":1,"category":"news"}`, now, now, "news"}})
	require.True(t, rows.Next())
	r, err := vs.scanRow(rows, nil)
	require.NoError(t, err)
	assert.Equal(t, "doc1", r.id)
	assert.Equal(t, float64(1), r.metadata["x"])
	// filter field restored from the typed column.
	assert.Equal(t, "news", r.metadata["category"])
	assert.Equal(t, now, r.createdAt)

	// A filter field absent from the metadata is not invented from the column
	// zero value.
	rowsNoKey := newMockRows([][]any{{"doc2", "n", "c", []float64{1, 2, 3}, `{"x":1}`, now, now, ""}})
	require.True(t, rowsNoKey.Next())
	rNoKey, err := vs.scanRow(rowsNoKey, nil)
	require.NoError(t, err)
	assert.NotContains(t, rNoKey.metadata, "category")

	// With score pointer.
	rows = newMockRows([][]any{{"doc1", "n", "c", []float64{1, 2, 3}, "{}", now, now, "news", 0.25}})
	require.True(t, rows.Next())
	var score float64
	_, err = vs.scanRow(rows, &score)
	require.NoError(t, err)
	assert.Equal(t, 0.25, score)
}
