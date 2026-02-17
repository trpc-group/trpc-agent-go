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
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/postgres"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// mockEmbedder is a mock implementation of the embedder.Embedder interface.
type mockEmbedder struct {
	dimension int
	err       error
}

func (m *mockEmbedder) GetEmbedding(ctx context.Context, text string) ([]float64, error) {
	if m.err != nil {
		return nil, m.err
	}
	// Return a deterministic embedding based on text hash for testing.
	embedding := make([]float64, m.dimension)
	for i := range embedding {
		embedding[i] = float64(i) * 0.01
	}
	return embedding, nil
}

func (m *mockEmbedder) GetEmbeddingWithUsage(
	ctx context.Context,
	text string,
) ([]float64, map[string]any, error) {
	emb, err := m.GetEmbedding(ctx, text)
	return emb, nil, err
}

func (m *mockEmbedder) GetDimensions() int {
	return m.dimension
}

func newMockEmbedder(dimension int) *mockEmbedder {
	return &mockEmbedder{dimension: dimension}
}

func newMockEmbedderWithError(err error) *mockEmbedder {
	return &mockEmbedder{err: err}
}

// Options tests.

func TestServiceOpts_Defaults(t *testing.T) {
	opts := defaultOptions.clone()

	assert.Equal(t, defaultTableName, opts.tableName)
	assert.Equal(t, defaultIndexDimension, opts.indexDimension)
	assert.Equal(t, defaultMaxResults, opts.maxResults)
	assert.Equal(t, imemory.DefaultMemoryLimit, opts.memoryLimit)
	assert.Equal(t, imemory.DefaultAsyncMemoryNum, opts.asyncMemoryNum)
	assert.False(t, opts.softDelete)
	assert.False(t, opts.skipDBInit)
	assert.Empty(t, opts.schema)

	assert.Empty(t, opts.dsn)
	assert.Empty(t, opts.host)
	assert.Zero(t, opts.port)
	assert.Empty(t, opts.instanceName)

	require.NotNil(t, opts.hnswParams)
	assert.Equal(t, defaultHNSWM, opts.hnswParams.M)
	assert.Equal(t, defaultHNSWEfConstruction, opts.hnswParams.EfConstruction)

	require.NotEmpty(t, opts.toolCreators)
	require.NotEmpty(t, opts.enabledTools)
	_, hasSearch := opts.enabledTools[memory.SearchToolName]
	assert.True(t, hasSearch)

	// These are applied by AutoMemoryWorker when needed.
	assert.Zero(t, opts.memoryQueueSize)
	assert.Zero(t, opts.memoryJobTimeout)
}

func TestServiceOpts_WithMemoryLimit(t *testing.T) {
	opts := ServiceOpts{}
	limit := 500

	WithMemoryLimit(limit)(&opts)

	assert.Equal(t, limit, opts.memoryLimit)
}

func TestServiceOpts_WithHost(t *testing.T) {
	opts := ServiceOpts{}
	host := "localhost"

	WithHost(host)(&opts)

	assert.Equal(t, host, opts.host)
}

func TestServiceOpts_WithPort(t *testing.T) {
	opts := ServiceOpts{}
	port := 5432

	WithPort(port)(&opts)

	assert.Equal(t, port, opts.port)
}

func TestServiceOpts_WithUser(t *testing.T) {
	opts := ServiceOpts{}
	user := "testuser"

	WithUser(user)(&opts)

	assert.Equal(t, user, opts.user)
}

func TestServiceOpts_WithPassword(t *testing.T) {
	opts := ServiceOpts{}
	password := "testpass"

	WithPassword(password)(&opts)

	assert.Equal(t, password, opts.password)
}

func TestServiceOpts_WithDatabase(t *testing.T) {
	opts := ServiceOpts{}
	database := "testdb"

	WithDatabase(database)(&opts)

	assert.Equal(t, database, opts.database)
}

func TestServiceOpts_WithSSLMode(t *testing.T) {
	opts := ServiceOpts{}
	sslMode := "require"

	WithSSLMode(sslMode)(&opts)

	assert.Equal(t, sslMode, opts.sslMode)
}

func TestServiceOpts_WithSkipDBInit(t *testing.T) {
	opts := ServiceOpts{}

	WithSkipDBInit(true)(&opts)
	assert.True(t, opts.skipDBInit)

	WithSkipDBInit(false)(&opts)
	assert.False(t, opts.skipDBInit)
}

func TestServiceOpts_WithSchema(t *testing.T) {
	opts := ServiceOpts{}
	schema := "test_schema"

	WithSchema(schema)(&opts)

	assert.Equal(t, schema, opts.schema)
}

func TestServiceOpts_WithSchema_Invalid(t *testing.T) {
	defer func() {
		r := recover()
		require.NotNil(t, r, "expected panic for invalid schema name")
		assert.Contains(t, fmt.Sprintf("%v", r), "invalid table name")
	}()

	opts := ServiceOpts{}
	WithSchema("invalid-schema-name")(&opts)
}

func TestServiceOpts_WithPGVectorClientDSN(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
	}{
		{
			name: "URL format",
			dsn:  "postgres://user:password@localhost:5432/mydb?sslmode=disable",
		},
		{
			name: "Key-Value format",
			dsn:  "host=localhost port=5432 user=postgres password=secret dbname=mydb sslmode=disable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := ServiceOpts{}
			WithPGVectorClientDSN(tt.dsn)(&opts)
			assert.Equal(t, tt.dsn, opts.dsn)
		})
	}
}

func TestServiceOpts_WithPostgresInstance(t *testing.T) {
	opts := ServiceOpts{}
	instanceName := "test-instance"

	WithPostgresInstance(instanceName)(&opts)

	assert.Equal(t, instanceName, opts.instanceName)
}

func TestServiceOpts_WithTableName(t *testing.T) {
	opts := ServiceOpts{}
	tableName := "custom_memories"

	WithTableName(tableName)(&opts)

	assert.Equal(t, tableName, opts.tableName)
}

func TestServiceOpts_WithTableName_Invalid(t *testing.T) {
	defer func() {
		r := recover()
		require.NotNil(t, r, "expected panic for invalid table name")
		assert.Contains(t, fmt.Sprintf("%v", r), "invalid table name")
	}()

	opts := ServiceOpts{}
	WithTableName("invalid-table-name")(&opts)
}

func TestServiceOpts_WithIndexDimension(t *testing.T) {
	opts := ServiceOpts{}

	WithIndexDimension(768)(&opts)
	assert.Equal(t, 768, opts.indexDimension)

	// Zero or negative should not change.
	WithIndexDimension(0)(&opts)
	assert.Equal(t, 768, opts.indexDimension)

	WithIndexDimension(-1)(&opts)
	assert.Equal(t, 768, opts.indexDimension)
}

func TestServiceOpts_WithMaxResults(t *testing.T) {
	opts := ServiceOpts{}

	WithMaxResults(20)(&opts)
	assert.Equal(t, 20, opts.maxResults)

	// Zero or negative should not change.
	WithMaxResults(0)(&opts)
	assert.Equal(t, 20, opts.maxResults)
}

func TestServiceOpts_WithSoftDelete(t *testing.T) {
	opts := ServiceOpts{}

	WithSoftDelete(true)(&opts)
	assert.True(t, opts.softDelete)

	WithSoftDelete(false)(&opts)
	assert.False(t, opts.softDelete)
}

func TestServiceOpts_WithEmbedder(t *testing.T) {
	opts := ServiceOpts{}
	emb := newMockEmbedder(1536)

	WithEmbedder(emb)(&opts)

	assert.Equal(t, emb, opts.embedder)
}

func TestServiceOpts_WithCustomTool(t *testing.T) {
	opts := ServiceOpts{
		toolCreators: make(map[string]memory.ToolCreator),
		enabledTools: make(map[string]struct{}),
	}

	toolName := memory.AddToolName
	creator := func() tool.Tool { return nil }

	WithCustomTool(toolName, creator)(&opts)

	assert.NotNil(t, opts.toolCreators[toolName])
	_, hasAdd := opts.enabledTools[toolName]
	assert.True(t, hasAdd)

	// Test with nil creator (should do nothing).
	WithCustomTool(memory.SearchToolName, nil)(&opts)

	assert.Nil(t, opts.toolCreators[memory.SearchToolName])
	_, hasSearch := opts.enabledTools[memory.SearchToolName]
	assert.False(t, hasSearch)
}

func TestServiceOpts_WithToolEnabled(t *testing.T) {
	opts := ServiceOpts{
		enabledTools: make(map[string]struct{}),
	}

	toolName := memory.SearchToolName

	WithToolEnabled(toolName, true)(&opts)

	_, hasSearch := opts.enabledTools[toolName]
	assert.True(t, hasSearch, "Expected tool to be enabled")

	// Test disabling.
	WithToolEnabled(toolName, false)(&opts)

	_, hasSearch = opts.enabledTools[toolName]
	assert.False(t, hasSearch, "Expected tool to be disabled")
}

func TestServiceOpts_InvalidToolName(t *testing.T) {
	opts := ServiceOpts{
		toolCreators: make(map[string]memory.ToolCreator),
		enabledTools: make(map[string]struct{}),
	}

	invalidToolName := "invalid_tool"
	creator := func() tool.Tool { return nil }

	// Test WithCustomTool with invalid name.
	WithCustomTool(invalidToolName, creator)(&opts)

	assert.Nil(t, opts.toolCreators[invalidToolName])
	_, hasInvalid := opts.enabledTools[invalidToolName]
	assert.False(t, hasInvalid)

	// Test WithToolEnabled with invalid name.
	WithToolEnabled(invalidToolName, true)(&opts)

	_, hasInvalid = opts.enabledTools[invalidToolName]
	assert.False(t, hasInvalid)
}

func TestServiceOpts_WithHNSWIndexParams(t *testing.T) {
	opts := ServiceOpts{}

	params := &HNSWIndexParams{M: 32, EfConstruction: 128}
	WithHNSWIndexParams(params)(&opts)

	assert.Equal(t, 32, opts.hnswParams.M)
	assert.Equal(t, 128, opts.hnswParams.EfConstruction)

	// Nil should not change.
	WithHNSWIndexParams(nil)(&opts)
	assert.Equal(t, 32, opts.hnswParams.M)
}

func TestServiceOpts_CombinedOptions(t *testing.T) {
	opts := ServiceOpts{}

	// Apply multiple options.
	WithHost("localhost")(&opts)
	WithPort(5432)(&opts)
	WithUser("testuser")(&opts)
	WithPassword("testpass")(&opts)
	WithDatabase("testdb")(&opts)
	WithMemoryLimit(1000)(&opts)
	WithIndexDimension(768)(&opts)
	WithMaxResults(20)(&opts)

	// Verify all options are set correctly.
	assert.Equal(t, "localhost", opts.host)
	assert.Equal(t, 5432, opts.port)
	assert.Equal(t, "testuser", opts.user)
	assert.Equal(t, "testpass", opts.password)
	assert.Equal(t, "testdb", opts.database)
	assert.Equal(t, 1000, opts.memoryLimit)
	assert.Equal(t, 768, opts.indexDimension)
	assert.Equal(t, 20, opts.maxResults)
}

func TestWithExtraOptions(t *testing.T) {
	opts := ServiceOpts{}
	opt1 := "option1"
	opt2 := "option2"

	WithExtraOptions(opt1, opt2)(&opts)

	assert.Len(t, opts.extraOptions, 2)
	assert.Equal(t, opt1, opts.extraOptions[0])
	assert.Equal(t, opt2, opts.extraOptions[1])

	// Test appending more options.
	opt3 := "option3"
	WithExtraOptions(opt3)(&opts)

	assert.Len(t, opts.extraOptions, 3)
	assert.Equal(t, opt3, opts.extraOptions[2])
}

func TestWithAsyncMemoryNum(t *testing.T) {
	opts := ServiceOpts{}

	WithAsyncMemoryNum(5)(&opts)
	assert.Equal(t, 5, opts.asyncMemoryNum)

	// Zero should use default.
	WithAsyncMemoryNum(0)(&opts)
	assert.Greater(t, opts.asyncMemoryNum, 0)
}

func TestWithMemoryQueueSize(t *testing.T) {
	opts := ServiceOpts{}

	WithMemoryQueueSize(100)(&opts)
	assert.Equal(t, 100, opts.memoryQueueSize)

	// Zero should use default.
	WithMemoryQueueSize(0)(&opts)
	assert.Greater(t, opts.memoryQueueSize, 0)
}

func TestWithMemoryJobTimeout(t *testing.T) {
	opts := ServiceOpts{}

	timeout := 5 * time.Minute
	WithMemoryJobTimeout(timeout)(&opts)
	assert.Equal(t, timeout, opts.memoryJobTimeout)
}

// NewService tests.

func TestNewService_EmbedderRequired(t *testing.T) {
	_, err := NewService(
		WithHost("localhost"),
		WithSkipDBInit(true),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "embedder is required")
}

func TestNewService_InstanceNameNotFound(t *testing.T) {
	_, err := NewService(
		WithPostgresInstance("non-existent-instance"),
		WithEmbedder(newMockEmbedder(1536)),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "postgres instance")
}

// Unit tests with sqlmock.

func setupMockDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	return db, mock
}

// testClient wraps sql.DB to implement storage.Client interface.
type testClient struct {
	db *sql.DB
}

func (c *testClient) ExecContext(
	ctx context.Context,
	query string,
	args ...any,
) (sql.Result, error) {
	return c.db.ExecContext(ctx, query, args...)
}

func (c *testClient) Query(
	ctx context.Context,
	handler storage.HandlerFunc,
	query string,
	args ...any,
) error {
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	if err := handler(rows); err != nil {
		return err
	}
	return rows.Err()
}

func (c *testClient) Transaction(ctx context.Context, fn storage.TxFunc) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (c *testClient) Close() error {
	return c.db.Close()
}

func setupMockService(
	t *testing.T,
	db *sql.DB,
	mock sqlmock.Sqlmock,
	opts ...ServiceOpt,
) *Service {
	originalBuilder := storage.GetClientBuilder()

	// Create a test client that wraps sql.DB.
	client := &testClient{db: db}

	// Set up builder to return our test client.
	storage.SetClientBuilder(
		func(ctx context.Context, builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
			return client, nil
		},
	)
	t.Cleanup(func() {
		storage.SetClientBuilder(originalBuilder)
	})

	// Determine schema and table name from opts.
	testOpts := defaultOptions.clone()
	for _, opt := range opts {
		opt(&testOpts)
	}
	skipDBInit := testOpts.skipDBInit
	schema := testOpts.schema
	if schema == "" {
		schema = "public"
	}

	if !skipDBInit {
		// Mock extension creation.
		mock.ExpectExec("CREATE EXTENSION IF NOT EXISTS vector").
			WillReturnResult(sqlmock.NewResult(0, 0))

		// Mock DDL privilege check.
		mock.ExpectQuery("SELECT has_schema_privilege\\(\\$1, 'CREATE'\\)").
			WithArgs(schema).
			WillReturnRows(sqlmock.NewRows([]string{"has_schema_privilege"}).AddRow(true))

		// Mock table creation.
		mock.ExpectExec("CREATE TABLE IF NOT EXISTS").
			WillReturnResult(sqlmock.NewResult(0, 0))

		// Mock index creation (3 regular indexes + 1 HNSW index).
		mock.ExpectExec("CREATE INDEX IF NOT EXISTS").
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("CREATE INDEX IF NOT EXISTS").
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("CREATE INDEX IF NOT EXISTS").
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("CREATE INDEX IF NOT EXISTS").
			WillReturnResult(sqlmock.NewResult(0, 0))
	}

	// Ensure host and embedder are set.
	hasHost := testOpts.host != ""
	if !hasHost {
		opts = append(opts, WithHost("localhost"))
	}
	if testOpts.embedder == nil {
		opts = append(opts, WithEmbedder(newMockEmbedder(testOpts.indexDimension)))
	}

	svc, err := NewService(opts...)
	require.NoError(t, err)
	return svc
}

func TestService_AddMemory(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	// Disable memory limit to skip COUNT query.
	svc := setupMockService(t, db, mock, WithSkipDBInit(true), WithMemoryLimit(0))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	// Mock the INSERT query.
	mock.ExpectExec("INSERT INTO").
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := svc.AddMemory(ctx, userKey, "test memory", []string{"topic1"})
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_AddMemory_InvalidKey(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	ctx := context.Background()

	// Test with empty app name.
	err := svc.AddMemory(ctx, memory.UserKey{AppName: "", UserID: "u"}, "m", nil)
	require.Error(t, err)
	assert.Equal(t, memory.ErrAppNameRequired, err)

	// Test with empty user id.
	err = svc.AddMemory(ctx, memory.UserKey{AppName: "app", UserID: ""}, "m", nil)
	require.Error(t, err)
	assert.Equal(t, memory.ErrUserIDRequired, err)
}

func TestService_AddMemory_MemoryLimit(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true), WithMemoryLimit(1))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	// Atomic insert will affect 0 rows when at limit and memory does not exist.
	mock.ExpectExec("WITH existing AS").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := svc.AddMemory(ctx, userKey, "test memory", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "memory limit exceeded")

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_AddMemory_EmbeddingError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	errEmb := fmt.Errorf("embedding service unavailable")
	svc := setupMockService(t, db, mock,
		WithSkipDBInit(true),
		WithMemoryLimit(0), // Disable memory limit to skip COUNT query.
		WithEmbedder(newMockEmbedderWithError(errEmb)),
	)
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	err := svc.AddMemory(ctx, userKey, "test memory", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "generate embedding failed")
}

func TestService_AddMemory_DimensionMismatch(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	// Create embedder with wrong dimension.
	svc := setupMockService(t, db, mock,
		WithSkipDBInit(true),
		WithMemoryLimit(0), // Disable memory limit to skip COUNT query.
		WithIndexDimension(1536),
		WithEmbedder(newMockEmbedder(768)), // Wrong dimension.
	)
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	err := svc.AddMemory(ctx, userKey, "test memory", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "embedding dimension mismatch")
}

func TestService_UpdateMemory(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	ctx := context.Background()
	memKey := memory.Key{AppName: "test-app", UserID: "u1", MemoryID: "mem-123"}

	// Mock UPDATE query.
	mock.ExpectExec("UPDATE").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := svc.UpdateMemory(ctx, memKey, "updated memory", []string{"new-topic"})
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_UpdateMemory_NotFound(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	ctx := context.Background()
	memKey := memory.Key{AppName: "test-app", UserID: "u1", MemoryID: "non-existent"}

	// Mock UPDATE query affecting 0 rows (not found).
	mock.ExpectExec("UPDATE").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := svc.UpdateMemory(ctx, memKey, "updated memory", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_UpdateMemory_InvalidKey(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	ctx := context.Background()

	// Test with empty app name.
	err := svc.UpdateMemory(ctx, memory.Key{AppName: "", UserID: "u", MemoryID: "id"}, "m", nil)
	require.Error(t, err)
	assert.Equal(t, memory.ErrAppNameRequired, err)

	// Test with empty memory id.
	err = svc.UpdateMemory(ctx, memory.Key{AppName: "a", UserID: "u", MemoryID: ""}, "m", nil)
	require.Error(t, err)
	assert.Equal(t, memory.ErrMemoryIDRequired, err)
}

func TestService_DeleteMemory(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	ctx := context.Background()
	memKey := memory.Key{AppName: "test-app", UserID: "u1", MemoryID: "mem-123"}

	// Mock DELETE query.
	mock.ExpectExec("DELETE FROM").
		WithArgs(memKey.MemoryID, memKey.AppName, memKey.UserID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := svc.DeleteMemory(ctx, memKey)
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_DeleteMemory_SoftDelete(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true), WithSoftDelete(true))
	defer svc.Close()

	ctx := context.Background()
	memKey := memory.Key{AppName: "test-app", UserID: "u1", MemoryID: "mem-123"}

	// Mock UPDATE query for soft delete.
	mock.ExpectExec("UPDATE").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := svc.DeleteMemory(ctx, memKey)
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_DeleteMemory_InvalidKey(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	ctx := context.Background()

	// Test with empty app name.
	err := svc.DeleteMemory(ctx, memory.Key{AppName: "", UserID: "u", MemoryID: "id"})
	require.Error(t, err)
	assert.Equal(t, memory.ErrAppNameRequired, err)

	// Test with empty memory id.
	err = svc.DeleteMemory(ctx, memory.Key{AppName: "a", UserID: "u", MemoryID: ""})
	require.Error(t, err)
	assert.Equal(t, memory.ErrMemoryIDRequired, err)
}

func TestService_ClearMemories(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	// Mock DELETE query.
	mock.ExpectExec("DELETE FROM").
		WithArgs(userKey.AppName, userKey.UserID).
		WillReturnResult(sqlmock.NewResult(0, 5))

	err := svc.ClearMemories(ctx, userKey)
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_ClearMemories_SoftDelete(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true), WithSoftDelete(true))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	// Mock UPDATE query for soft delete.
	mock.ExpectExec("UPDATE").
		WillReturnResult(sqlmock.NewResult(0, 5))

	err := svc.ClearMemories(ctx, userKey)
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_ClearMemories_InvalidKey(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	ctx := context.Background()

	// Test with empty app name.
	err := svc.ClearMemories(ctx, memory.UserKey{AppName: "", UserID: "u"})
	require.Error(t, err)
	assert.Equal(t, memory.ErrAppNameRequired, err)

	// Test with empty user id.
	err = svc.ClearMemories(ctx, memory.UserKey{AppName: "a", UserID: ""})
	require.Error(t, err)
	assert.Equal(t, memory.ErrUserIDRequired, err)
}

func TestService_ReadMemories(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	now := time.Now()
	// Mock SELECT query.
	mock.ExpectQuery("SELECT memory_id, app_name, user_id, memory_content, topics").
		WithArgs(userKey.AppName, userKey.UserID).
		WillReturnRows(sqlmock.NewRows(
			[]string{"memory_id", "app_name", "user_id", "memory_content", "topics",
				"created_at", "updated_at"},
		).
			AddRow("mem-1", "test-app", "u1", "memory 1", pq.Array([]string{"topic1"}),
				now, now).
			AddRow("mem-2", "test-app", "u1", "memory 2", pq.Array([]string{"topic2"}),
				now, now))

	entries, err := svc.ReadMemories(ctx, userKey, 10)
	require.NoError(t, err)
	require.Len(t, entries, 2)

	assert.Equal(t, "mem-1", entries[0].ID)
	assert.Equal(t, "memory 1", entries[0].Memory.Memory)
	assert.Equal(t, []string{"topic1"}, entries[0].Memory.Topics)

	assert.Equal(t, "mem-2", entries[1].ID)
	assert.Equal(t, "memory 2", entries[1].Memory.Memory)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_ReadMemories_InvalidKey(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	ctx := context.Background()

	// Test with empty app name.
	_, err := svc.ReadMemories(ctx, memory.UserKey{AppName: "", UserID: "u"}, 10)
	require.Error(t, err)
	assert.Equal(t, memory.ErrAppNameRequired, err)

	// Test with empty user id.
	_, err = svc.ReadMemories(ctx, memory.UserKey{AppName: "a", UserID: ""}, 10)
	require.Error(t, err)
	assert.Equal(t, memory.ErrUserIDRequired, err)
}

func TestService_ReadMemories_Empty(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "non-existent"}

	// Mock SELECT query returning no rows.
	mock.ExpectQuery("SELECT memory_id, app_name, user_id, memory_content, topics").
		WithArgs(userKey.AppName, userKey.UserID).
		WillReturnRows(sqlmock.NewRows(
			[]string{"memory_id", "app_name", "user_id", "memory_content", "topics",
				"created_at", "updated_at"},
		))

	entries, err := svc.ReadMemories(ctx, userKey, 10)
	require.NoError(t, err)
	assert.Len(t, entries, 0)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_SearchMemories(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	now := time.Now()
	// Mock SELECT query with vector similarity.
	mock.ExpectQuery("SELECT memory_id, app_name, user_id, memory_content, topics").
		WillReturnRows(sqlmock.NewRows(
			[]string{"memory_id", "app_name", "user_id", "memory_content", "topics",
				"created_at", "updated_at", "similarity"},
		).
			AddRow("mem-1", "test-app", "u1", "coffee brewing tips", pq.Array([]string{"hobby"}),
				now, now, 0.95).
			AddRow("mem-2", "test-app", "u1", "Alice likes coffee", pq.Array([]string{"profile"}),
				now, now, 0.85))

	results, err := svc.SearchMemories(ctx, userKey, "coffee")
	require.NoError(t, err)
	require.Len(t, results, 2)

	// Results should be in order of similarity (most similar first).
	assert.Equal(t, "coffee brewing tips", results[0].Memory.Memory)
	assert.Equal(t, "Alice likes coffee", results[1].Memory.Memory)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_SearchMemories_EmptyQuery(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	// Empty query should return empty results without calling embedder or database.
	results, err := svc.SearchMemories(ctx, userKey, "")
	require.NoError(t, err)
	assert.Len(t, results, 0)

	// Whitespace-only query should also return empty.
	results, err = svc.SearchMemories(ctx, userKey, "   ")
	require.NoError(t, err)
	assert.Len(t, results, 0)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_SearchMemories_InvalidKey(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	ctx := context.Background()

	// Test with empty app name.
	_, err := svc.SearchMemories(ctx, memory.UserKey{AppName: "", UserID: "u"}, "query")
	require.Error(t, err)
	assert.Equal(t, memory.ErrAppNameRequired, err)

	// Test with empty user id.
	_, err = svc.SearchMemories(ctx, memory.UserKey{AppName: "a", UserID: ""}, "query")
	require.Error(t, err)
	assert.Equal(t, memory.ErrUserIDRequired, err)
}

func TestService_SearchMemories_EmbeddingError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	errEmb := fmt.Errorf("embedding service unavailable")
	svc := setupMockService(t, db, mock,
		WithSkipDBInit(true),
		WithEmbedder(newMockEmbedderWithError(errEmb)),
	)
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	_, err := svc.SearchMemories(ctx, userKey, "coffee")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "generate query embedding failed")
}

func TestService_Tools(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	tools := svc.Tools()
	require.NotEmpty(t, tools)

	// Collect tool names and verify defaults include add/update/search/load.
	names := make(map[string]bool)
	for _, tl := range tools {
		if decl := tl.Declaration(); decl != nil {
			names[decl.Name] = true
		}
	}
	assert.True(t, names[memory.AddToolName])
	assert.True(t, names[memory.UpdateToolName])
	assert.True(t, names[memory.SearchToolName])
	assert.True(t, names[memory.LoadToolName])
	assert.False(t, names[memory.DeleteToolName])
	assert.False(t, names[memory.ClearToolName])
}

func TestService_Tools_DisabledTools(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock,
		WithSkipDBInit(true),
		WithToolEnabled(memory.SearchToolName, false),
	)
	defer svc.Close()

	tools := svc.Tools()

	// Search tool should not be in the list.
	for _, tl := range tools {
		if decl := tl.Declaration(); decl != nil {
			assert.NotEqual(t, memory.SearchToolName, decl.Name)
		}
	}
}

func TestService_Close(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true))

	mock.ExpectClose()

	err := svc.Close()
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_EnqueueAutoMemoryJob_NoWorker(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	ctx := context.Background()

	// Should return nil when no worker is configured.
	err := svc.EnqueueAutoMemoryJob(ctx, nil)
	require.NoError(t, err)
}

// Helper function tests.

func TestConvertToFloat32(t *testing.T) {
	input := []float64{1.0, 2.5, 3.7}
	result := convertToFloat32(input)

	require.Len(t, result, 3)
	assert.Equal(t, float32(1.0), result[0])
	assert.Equal(t, float32(2.5), result[1])
	assert.Equal(t, float32(3.7), result[2])
}

func TestConvertToFloat32_Empty(t *testing.T) {
	input := []float64{}
	result := convertToFloat32(input)

	assert.Len(t, result, 0)
}

func TestBuildConnString(t *testing.T) {
	opts := ServiceOpts{
		host:     "myhost",
		port:     5433,
		database: "mydb",
		sslMode:  "require",
		user:     "myuser",
		password: "mypass",
	}

	connStr := buildConnString(opts)

	assert.Contains(t, connStr, "host=myhost")
	assert.Contains(t, connStr, "port=5433")
	assert.Contains(t, connStr, "dbname=mydb")
	assert.Contains(t, connStr, "sslmode=require")
	assert.Contains(t, connStr, "user=myuser")
	assert.Contains(t, connStr, "password=mypass")
}

func TestBuildConnString_Defaults(t *testing.T) {
	opts := ServiceOpts{}

	connStr := buildConnString(opts)

	assert.Contains(t, connStr, "host=localhost")
	assert.Contains(t, connStr, "port=5432")
	assert.Contains(t, connStr, "dbname=trpc-agent-go-pgmemory")
	assert.Contains(t, connStr, "sslmode=disable")
}

// initDB helper tests.

func TestBuildCreateTableSQL(t *testing.T) {
	sql := buildCreateTableSQL("", "test_table", 1536)
	assert.Contains(t, sql, "CREATE TABLE IF NOT EXISTS")
	assert.Contains(t, sql, "test_table")
	assert.Contains(t, sql, "vector(1536)")
}

func TestBuildCreateTableSQL_WithSchema(t *testing.T) {
	sql := buildCreateTableSQL("myschema", "test_table", 768)
	assert.Contains(t, sql, "myschema.test_table")
	assert.Contains(t, sql, "vector(768)")
}

func TestBuildCreateHNSWIndexSQL(t *testing.T) {
	params := &HNSWIndexParams{M: 32, EfConstruction: 128}
	sql := buildCreateHNSWIndexSQL("", "test_table", params)

	assert.Contains(t, sql, "CREATE INDEX IF NOT EXISTS")
	assert.Contains(t, sql, "USING hnsw")
	assert.Contains(t, sql, "vector_cosine_ops")
	assert.Contains(t, sql, "m = 32")
	assert.Contains(t, sql, "ef_construction = 128")
}

func TestBuildCreateHNSWIndexSQL_Defaults(t *testing.T) {
	sql := buildCreateHNSWIndexSQL("", "test_table", nil)

	assert.Contains(t, sql, fmt.Sprintf("m = %d", defaultHNSWM))
	assert.Contains(t, sql, fmt.Sprintf("ef_construction = %d", defaultHNSWEfConstruction))
}

func TestBuildCreateHNSWIndexSQL_PartialParams(t *testing.T) {
	params := &HNSWIndexParams{M: 0, EfConstruction: 256}
	sql := buildCreateHNSWIndexSQL("", "test_table", params)

	// M should use default when <= 0.
	assert.Contains(t, sql, fmt.Sprintf("m = %d", defaultHNSWM))
	assert.Contains(t, sql, "ef_construction = 256")
}

func TestService_InitDB_ExtensionError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	// Mock extension creation failure.
	mock.ExpectExec("CREATE EXTENSION IF NOT EXISTS vector").
		WillReturnError(fmt.Errorf("insufficient privilege"))

	originalBuilder := storage.GetClientBuilder()
	client := &testClient{db: db}
	storage.SetClientBuilder(
		func(ctx context.Context, builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
			return client, nil
		},
	)
	t.Cleanup(func() {
		storage.SetClientBuilder(originalBuilder)
	})

	_, err := NewService(
		WithHost("localhost"),
		WithEmbedder(newMockEmbedder(1536)),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "enable pgvector extension failed")
}

func TestService_InitDB_PrivilegeCheckError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	// Mock extension creation success.
	mock.ExpectExec("CREATE EXTENSION IF NOT EXISTS vector").
		WillReturnResult(sqlmock.NewResult(0, 0))

	// Mock DDL privilege check failure.
	mock.ExpectQuery("SELECT has_schema_privilege").
		WillReturnError(fmt.Errorf("connection lost"))

	originalBuilder := storage.GetClientBuilder()
	client := &testClient{db: db}
	storage.SetClientBuilder(
		func(ctx context.Context, builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
			return client, nil
		},
	)
	t.Cleanup(func() {
		storage.SetClientBuilder(originalBuilder)
	})

	_, err := NewService(
		WithHost("localhost"),
		WithEmbedder(newMockEmbedder(1536)),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "check DDL privilege")
}

func TestService_InitDB_NoPrivilege(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	// Mock extension creation success.
	mock.ExpectExec("CREATE EXTENSION IF NOT EXISTS vector").
		WillReturnResult(sqlmock.NewResult(0, 0))

	// Mock DDL privilege check returns false.
	mock.ExpectQuery("SELECT has_schema_privilege").
		WithArgs("public").
		WillReturnRows(sqlmock.NewRows([]string{"has_schema_privilege"}).AddRow(false))

	originalBuilder := storage.GetClientBuilder()
	client := &testClient{db: db}
	storage.SetClientBuilder(
		func(ctx context.Context, builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
			return client, nil
		},
	)
	t.Cleanup(func() {
		storage.SetClientBuilder(originalBuilder)
	})

	svc, err := NewService(
		WithHost("localhost"),
		WithEmbedder(newMockEmbedder(1536)),
	)
	require.NoError(t, err)
	defer svc.Close()

	// Service should be created without error (skips DDL operations).
	assert.NotNil(t, svc)
}

func TestService_InitDB_TableCreateError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	// Mock extension creation success.
	mock.ExpectExec("CREATE EXTENSION IF NOT EXISTS vector").
		WillReturnResult(sqlmock.NewResult(0, 0))

	// Mock DDL privilege check success.
	mock.ExpectQuery("SELECT has_schema_privilege").
		WithArgs("public").
		WillReturnRows(sqlmock.NewRows([]string{"has_schema_privilege"}).AddRow(true))

	// Mock table creation failure.
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS").
		WillReturnError(fmt.Errorf("disk full"))

	originalBuilder := storage.GetClientBuilder()
	client := &testClient{db: db}
	storage.SetClientBuilder(
		func(ctx context.Context, builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
			return client, nil
		},
	)
	t.Cleanup(func() {
		storage.SetClientBuilder(originalBuilder)
	})

	_, err := NewService(
		WithHost("localhost"),
		WithEmbedder(newMockEmbedder(1536)),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create table")
}

func TestService_InitDB_IndexCreateError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	// Mock extension creation success.
	mock.ExpectExec("CREATE EXTENSION IF NOT EXISTS vector").
		WillReturnResult(sqlmock.NewResult(0, 0))

	// Mock DDL privilege check success.
	mock.ExpectQuery("SELECT has_schema_privilege").
		WithArgs("public").
		WillReturnRows(sqlmock.NewRows([]string{"has_schema_privilege"}).AddRow(true))

	// Mock table creation success.
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS").
		WillReturnResult(sqlmock.NewResult(0, 0))

	// Mock first index creation failure.
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS").
		WillReturnError(fmt.Errorf("index creation failed"))

	originalBuilder := storage.GetClientBuilder()
	client := &testClient{db: db}
	storage.SetClientBuilder(
		func(ctx context.Context, builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
			return client, nil
		},
	)
	t.Cleanup(func() {
		storage.SetClientBuilder(originalBuilder)
	})

	_, err := NewService(
		WithHost("localhost"),
		WithEmbedder(newMockEmbedder(1536)),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create index")
}

func TestService_InitDB_HNSWIndexError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	// Mock extension creation success.
	mock.ExpectExec("CREATE EXTENSION IF NOT EXISTS vector").
		WillReturnResult(sqlmock.NewResult(0, 0))

	// Mock DDL privilege check success.
	mock.ExpectQuery("SELECT has_schema_privilege").
		WithArgs("public").
		WillReturnRows(sqlmock.NewRows([]string{"has_schema_privilege"}).AddRow(true))

	// Mock table creation success.
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS").
		WillReturnResult(sqlmock.NewResult(0, 0))

	// Mock 3 regular index creations.
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS").
		WillReturnResult(sqlmock.NewResult(0, 0))

	// Mock HNSW index creation failure.
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS").
		WillReturnError(fmt.Errorf("HNSW index creation failed"))

	originalBuilder := storage.GetClientBuilder()
	client := &testClient{db: db}
	storage.SetClientBuilder(
		func(ctx context.Context, builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
			return client, nil
		},
	)
	t.Cleanup(func() {
		storage.SetClientBuilder(originalBuilder)
	})

	_, err := NewService(
		WithHost("localhost"),
		WithEmbedder(newMockEmbedder(1536)),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create HNSW index")
}

func TestService_UpdateMemory_EmbeddingError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	errEmb := fmt.Errorf("embedding service down")
	svc := setupMockService(t, db, mock,
		WithSkipDBInit(true),
		WithEmbedder(newMockEmbedderWithError(errEmb)),
	)
	defer svc.Close()

	ctx := context.Background()
	memKey := memory.Key{AppName: "test-app", UserID: "u1", MemoryID: "mem-123"}

	err := svc.UpdateMemory(ctx, memKey, "updated", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "generate embedding failed")
}

func TestService_UpdateMemory_DimensionMismatch(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock,
		WithSkipDBInit(true),
		WithIndexDimension(1536),
		WithEmbedder(newMockEmbedder(768)),
	)
	defer svc.Close()

	ctx := context.Background()
	memKey := memory.Key{AppName: "test-app", UserID: "u1", MemoryID: "mem-123"}

	err := svc.UpdateMemory(ctx, memKey, "updated", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "embedding dimension mismatch")
}

func TestService_UpdateMemory_SoftDelete(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true), WithSoftDelete(true))
	defer svc.Close()

	ctx := context.Background()
	memKey := memory.Key{AppName: "test-app", UserID: "u1", MemoryID: "mem-123"}

	// Mock UPDATE with soft delete filter.
	mock.ExpectExec("UPDATE").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := svc.UpdateMemory(ctx, memKey, "updated", []string{"topic"})
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_UpdateMemory_SQLError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	ctx := context.Background()
	memKey := memory.Key{AppName: "test-app", UserID: "u1", MemoryID: "mem-123"}

	// Mock UPDATE query with error.
	mock.ExpectExec("UPDATE").
		WillReturnError(fmt.Errorf("connection timeout"))

	err := svc.UpdateMemory(ctx, memKey, "updated", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update memory entry failed")
}

func TestService_UpdateMemory_RowsAffectedError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	ctx := context.Background()
	memKey := memory.Key{AppName: "test-app", UserID: "u1", MemoryID: "mem-123"}

	// Mock UPDATE query with RowsAffected error.
	mock.ExpectExec("UPDATE").
		WillReturnResult(sqlmock.NewErrorResult(fmt.Errorf("rows affected failed")))

	err := svc.UpdateMemory(ctx, memKey, "updated", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update memory entry rows affected failed")
}

func TestService_DeleteMemory_SQLError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	ctx := context.Background()
	memKey := memory.Key{AppName: "test-app", UserID: "u1", MemoryID: "mem-123"}

	// Mock DELETE with error.
	mock.ExpectExec("DELETE FROM").
		WillReturnError(fmt.Errorf("database locked"))

	err := svc.DeleteMemory(ctx, memKey)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete memory entry failed")
}

func TestService_ClearMemories_SQLError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	// Mock DELETE with error.
	mock.ExpectExec("DELETE FROM").
		WillReturnError(fmt.Errorf("database error"))

	err := svc.ClearMemories(ctx, userKey)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "clear memories failed")
}

func TestService_ClearMemories_SoftDelete_SQLError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true), WithSoftDelete(true))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	// Mock UPDATE with error.
	mock.ExpectExec("UPDATE").
		WillReturnError(fmt.Errorf("update failed"))

	err := svc.ClearMemories(ctx, userKey)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "clear memories failed")
}

func TestService_ReadMemories_SQLError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	// Mock SELECT with error.
	mock.ExpectQuery("SELECT memory_id").
		WillReturnError(fmt.Errorf("query timeout"))

	_, err := svc.ReadMemories(ctx, userKey, 10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list memories failed")
}

func TestService_ReadMemories_SoftDelete(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true), WithSoftDelete(true))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	// Mock SELECT query with soft delete filter.
	mock.ExpectQuery("SELECT memory_id").
		WillReturnRows(sqlmock.NewRows(
			[]string{"memory_id", "app_name", "user_id", "memory_content", "topics",
				"created_at", "updated_at"},
		))

	entries, err := svc.ReadMemories(ctx, userKey, 0)
	require.NoError(t, err)
	assert.Len(t, entries, 0)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_SearchMemories_DimensionMismatch(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock,
		WithSkipDBInit(true),
		WithIndexDimension(1536),
		WithEmbedder(newMockEmbedder(768)),
	)
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	_, err := svc.SearchMemories(ctx, userKey, "test query")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "query embedding dimension mismatch")
}

func TestService_SearchMemories_SQLError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	// Mock SELECT with error.
	mock.ExpectQuery("SELECT memory_id").
		WillReturnError(fmt.Errorf("search failed"))

	_, err := svc.SearchMemories(ctx, userKey, "query")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "search memories failed")
}

func TestService_SearchMemories_SoftDelete(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true), WithSoftDelete(true))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	now := time.Now()
	// Mock query with soft delete filter.
	mock.ExpectQuery("SELECT memory_id").
		WillReturnRows(sqlmock.NewRows(
			[]string{"memory_id", "app_name", "user_id", "memory_content", "topics",
				"created_at", "updated_at", "similarity"},
		).
			AddRow("mem-1", "test-app", "u1", "test", pq.Array([]string{"t"}),
				now, now, 0.9))

	results, err := svc.SearchMemories(ctx, userKey, "query")
	require.NoError(t, err)
	assert.Len(t, results, 1)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_AddMemory_SoftDelete(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock,
		WithSkipDBInit(true),
		WithMemoryLimit(0),
		WithSoftDelete(true),
	)
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	// Mock INSERT with soft delete (deleted_at = NULL on conflict).
	mock.ExpectExec("INSERT INTO").
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := svc.AddMemory(ctx, userKey, "test", nil)
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_AddMemory_MemoryLimit_SoftDelete(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock,
		WithSkipDBInit(true),
		WithMemoryLimit(5),
		WithSoftDelete(true),
	)
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	// Mock atomic insert with limit and soft delete filter.
	mock.ExpectExec("WITH existing AS").
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := svc.AddMemory(ctx, userKey, "test", nil)
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_AddMemory_SQLError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true), WithMemoryLimit(0))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	// Mock INSERT with error.
	mock.ExpectExec("INSERT INTO").
		WillReturnError(fmt.Errorf("insert failed"))

	err := svc.AddMemory(ctx, userKey, "test", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "store memory entry failed")
}

func TestService_AddMemory_RowsAffectedError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock,
		WithSkipDBInit(true),
		WithMemoryLimit(1),
	)
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	// Mock with RowsAffected error.
	mock.ExpectExec("WITH existing AS").
		WillReturnResult(sqlmock.NewErrorResult(fmt.Errorf("rows affected err")))

	err := svc.AddMemory(ctx, userKey, "test", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "store memory entry rows affected failed")
}

func TestServiceOpts_Clone_Nil(t *testing.T) {
	opts := ServiceOpts{
		hnswParams: nil,
	}

	cloned := opts.clone()
	assert.Nil(t, cloned.hnswParams)
}

func TestBuildCreateIndexSQL(t *testing.T) {
	sql := buildCreateIndexSQL("", "test_table", "test_idx",
		"CREATE INDEX IF NOT EXISTS %s ON %s(col)")
	assert.Contains(t, sql, "CREATE INDEX IF NOT EXISTS")
	assert.Contains(t, sql, "test_table_test_idx")
	assert.Contains(t, sql, "test_table")
}

func TestBuildFullTableName(t *testing.T) {
	assert.Equal(t, "test_table", buildFullTableName("", "test_table"))
	assert.Equal(t, "myschema.test_table",
		buildFullTableName("myschema", "test_table"))
}

func TestBuildIndexName(t *testing.T) {
	name := buildIndexName("test_table", "idx_suffix")
	assert.Contains(t, name, "test_table")
	assert.Contains(t, name, "idx_suffix")
}

func TestService_EnqueueAutoMemoryJob_WithWorker(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	// Mock extractor to enable auto memory worker.
	mockExtractor := &mockMemoryExtractor{}
	svc := setupMockService(t, db, mock,
		WithSkipDBInit(true),
		WithExtractor(mockExtractor),
	)
	defer svc.Close()

	ctx := context.Background()
	// Should not return error when worker is configured.
	err := svc.EnqueueAutoMemoryJob(ctx, nil)
	require.NoError(t, err)
}

// mockMemoryExtractor implements extractor.MemoryExtractor interface.
type mockMemoryExtractor struct{}

func (m *mockMemoryExtractor) Extract(ctx context.Context, messages []model.Message, existing []*memory.Entry) ([]*extractor.Operation, error) {
	return []*extractor.Operation{
		{Type: extractor.OperationAdd, Memory: "test memory", Topics: []string{"test"}},
	}, nil
}

func (m *mockMemoryExtractor) ShouldExtract(ctx *extractor.ExtractionContext) bool {
	return true
}

func (m *mockMemoryExtractor) SetPrompt(prompt string) {}

func (m *mockMemoryExtractor) SetModel(md model.Model) {}

func (m *mockMemoryExtractor) SetEnabledTools(enabled map[string]struct{}) {}

func (m *mockMemoryExtractor) Metadata() map[string]any {
	return map[string]any{"test": "mock"}
}

func TestWithExtractor(t *testing.T) {
	opts := ServiceOpts{}
	mockExt := &mockMemoryExtractor{}

	WithExtractor(mockExt)(&opts)

	assert.Equal(t, mockExt, opts.extractor)
}

func TestService_ScanMemoryEntry_Error(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	// Mock SELECT returning rows with invalid data (trigger scan error).
	mock.ExpectQuery("SELECT memory_id").
		WithArgs(userKey.AppName, userKey.UserID).
		WillReturnRows(sqlmock.NewRows(
			[]string{"memory_id", "app_name", "user_id", "memory_content", "topics",
				"created_at", "updated_at"},
		).AddRow("mem-1", "test-app", "u1", "memory", nil, "invalid-time", "invalid"))

	_, err := svc.ReadMemories(ctx, userKey, 10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list memories failed")
}

func TestService_ScanMemoryEntryWithSimilarity_Error(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	ctx := context.Background()
	userKey := memory.UserKey{AppName: "test-app", UserID: "u1"}

	// Mock SELECT returning rows with invalid similarity data.
	mock.ExpectQuery("SELECT memory_id").
		WillReturnRows(sqlmock.NewRows(
			[]string{"memory_id", "app_name", "user_id", "memory_content", "topics",
				"created_at", "updated_at", "similarity"},
		).AddRow("mem-1", "test-app", "u1", "memory", nil, "invalid-time", "invalid", 0.9))

	_, err := svc.SearchMemories(ctx, userKey, "query")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "search memories failed")
}

func TestService_Close_NilDB(t *testing.T) {
	svc := &Service{
		db: nil,
	}

	err := svc.Close()
	require.NoError(t, err)
}

func TestService_CheckDDLPrivilege_NoRows(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	// Mock extension creation success.
	mock.ExpectExec("CREATE EXTENSION IF NOT EXISTS vector").
		WillReturnResult(sqlmock.NewResult(0, 0))

	// Mock DDL privilege check returns no rows (hasPrivilege remains false).
	mock.ExpectQuery("SELECT has_schema_privilege").
		WithArgs("public").
		WillReturnRows(sqlmock.NewRows([]string{"has_schema_privilege"}))

	originalBuilder := storage.GetClientBuilder()
	client := &testClient{db: db}
	storage.SetClientBuilder(
		func(ctx context.Context, builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
			return client, nil
		},
	)
	t.Cleanup(func() {
		storage.SetClientBuilder(originalBuilder)
	})

	svc, err := NewService(
		WithHost("localhost"),
		WithEmbedder(newMockEmbedder(1536)),
	)
	require.NoError(t, err)
	defer svc.Close()

	// Service should be created (no rows means no privilege, skips DDL).
	assert.NotNil(t, svc)
}
