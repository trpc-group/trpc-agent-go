//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package pgvector

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/storage/postgres"
)

// testClient wraps a postgres.Client for testing
type testClient struct {
	client postgres.Client
	mock   sqlmock.Sqlmock
	db     *sql.DB
}

// newTestClient creates a new test client with sqlmock
func newTestClient(t *testing.T) *testClient {
	// Use QueryMatcherOption to allow partial SQL matching
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)

	// Create a test postgres client that wraps the mock DB
	// Since we can't directly create postgres.Client, we use a wrapper
	client := &testPostgresClient{db: db}

	return &testClient{
		client: client,
		mock:   mock,
		db:     db,
	}
}

// testPostgresClient wraps sql.DB to implement postgres.Client for testing
type testPostgresClient struct {
	db *sql.DB
}

func (c *testPostgresClient) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return c.db.ExecContext(ctx, query, args...)
}

func (c *testPostgresClient) Query(ctx context.Context, handler postgres.HandlerFunc, query string, args ...any) error {
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	if err := handler(rows); err != nil {
		return err
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows iteration: %w", err)
	}

	return nil
}

func (c *testPostgresClient) Transaction(ctx context.Context, fn postgres.TxFunc) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		} else if err != nil {
			_ = tx.Rollback()
		}
	}()

	err = fn(tx)
	if err != nil {
		return err
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

func (c *testPostgresClient) Close() error {
	return c.db.Close()
}

// Close closes the test client
func (tc *testClient) Close() {
	tc.db.Close()
}

// ExpectExec adds an expectation for ExecContext
func (tc *testClient) ExpectExec(query string) *sqlmock.ExpectedExec {
	return tc.mock.ExpectExec(query)
}

// ExpectQuery adds an expectation for Query
func (tc *testClient) ExpectQuery(query string) *sqlmock.ExpectedQuery {
	return tc.mock.ExpectQuery(query)
}

// ExpectBegin adds an expectation for transaction begin
func (tc *testClient) ExpectBegin() *sqlmock.ExpectedBegin {
	return tc.mock.ExpectBegin()
}

// ExpectCommit adds an expectation for transaction commit
func (tc *testClient) ExpectCommit() *sqlmock.ExpectedCommit {
	return tc.mock.ExpectCommit()
}

// ExpectRollback adds an expectation for transaction rollback
func (tc *testClient) ExpectRollback() *sqlmock.ExpectedRollback {
	return tc.mock.ExpectRollback()
}

// AssertExpectations verifies all expectations were met
func (tc *testClient) AssertExpectations(t *testing.T) {
	require.NoError(t, tc.mock.ExpectationsWereMet())
}

// newTestVectorStore creates a VectorStore with test client for testing
func newTestVectorStore(t *testing.T, opts ...Option) (*VectorStore, *testClient) {
	tc := newTestClient(t)

	option := defaultOptions
	// Disable TSVector by default in tests
	option.enableTSVector = false

	for _, opt := range opts {
		opt(&option)
	}

	// Mock CREATE EXTENSION and table creation
	tc.ExpectExec("CREATE EXTENSION IF NOT EXISTS vector").
		WillReturnResult(sqlmock.NewResult(0, 0))

	tc.ExpectExec("CREATE TABLE IF NOT EXISTS").
		WillReturnResult(sqlmock.NewResult(0, 0))

	tc.ExpectExec("CREATE INDEX IF NOT EXISTS (.+)_embedding_idx").
		WillReturnResult(sqlmock.NewResult(0, 0))

	vs := &VectorStore{
		client:          tc.client,
		option:          option,
		filterConverter: &pgVectorConverter{},
	}

	// Initialize DB (will use our mocked expectations)
	err := vs.initDB(context.Background())
	require.NoError(t, err)

	return vs, tc
}

// Helper functions for creating common mock responses

// mockDocumentRow creates a mock row for a document
func mockDocumentRow(id, name, content string, vector []float64, metadata map[string]any) *sqlmock.Rows {
	vectorJSON := "["
	for i, v := range vector {
		if i > 0 {
			vectorJSON += ","
		}
		vectorJSON += fmt.Sprintf("%f", v)
	}
	vectorJSON += "]"

	metadataJSON := mapToJSON(metadata)

	return sqlmock.NewRows([]string{
		"id", "name", "content", "embedding", "metadata", "created_at", "updated_at", "vector_score", "text_score", "score",
	}).AddRow(id, name, content, vectorJSON, metadataJSON, 1000000, 2000000, 0.0, 0.0, 0.0)
}

// mockSearchResultRow creates a mock row for search results
func mockSearchResultRow(id, name, content string, vector []float64, metadata map[string]any, score float64) *sqlmock.Rows {
	vectorJSON := "["
	for i, v := range vector {
		if i > 0 {
			vectorJSON += ","
		}
		vectorJSON += fmt.Sprintf("%f", v)
	}
	vectorJSON += "]"

	metadataJSON := mapToJSON(metadata)
	vectorScore := score
	textScore := 0.0

	return sqlmock.NewRows([]string{
		"id", "name", "content", "embedding", "metadata", "created_at", "updated_at", "vector_score", "text_score", "score",
	}).AddRow(id, name, content, vectorJSON, metadataJSON, 1000000, 2000000, vectorScore, textScore, score)
}

// mockCountRow creates a mock row for count query
func mockCountRow(count int) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"count"}).AddRow(count)
}

// mockExistsRow creates a mock row for exists check
func mockExistsRow(exists bool) *sqlmock.Rows {
	val := 0
	if exists {
		val = 1
	}
	return sqlmock.NewRows([]string{"exists"}).AddRow(val)
}

// newTestVectorStoreWithTSVector creates a VectorStore with TSVector enabled for testing
func newTestVectorStoreWithTSVector(t *testing.T, opts ...Option) (*VectorStore, *testClient) {
	tc := newTestClient(t)

	option := defaultOptions
	option.enableTSVector = true

	for _, opt := range opts {
		opt(&option)
	}

	// Mock CREATE EXTENSION, table creation, and both indexes
	tc.ExpectExec("CREATE EXTENSION IF NOT EXISTS vector").
		WillReturnResult(sqlmock.NewResult(0, 0))

	tc.ExpectExec("CREATE TABLE IF NOT EXISTS").
		WillReturnResult(sqlmock.NewResult(0, 0))

	tc.ExpectExec("CREATE INDEX IF NOT EXISTS (.+)_embedding_idx").
		WillReturnResult(sqlmock.NewResult(0, 0))

	// Expect text search index creation
	tc.ExpectExec("CREATE INDEX IF NOT EXISTS (.+)_content_fts_idx").
		WillReturnResult(sqlmock.NewResult(0, 0))

	vs := &VectorStore{
		client:          tc.client,
		option:          option,
		filterConverter: &pgVectorConverter{},
	}

	// Initialize DB (will use our mocked expectations)
	err := vs.initDB(context.Background())
	require.NoError(t, err)

	return vs, tc
}
