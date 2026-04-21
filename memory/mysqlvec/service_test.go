//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mysqlvec

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// ---------------------------------------------------------------------------
// Mock embedder
// ---------------------------------------------------------------------------

type mockEmbedder struct {
	embedding  []float64
	err        error
	dimensions int
}

func (m *mockEmbedder) GetEmbedding(_ context.Context, _ string) ([]float64, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.embedding, nil
}

func (m *mockEmbedder) GetEmbeddingWithUsage(_ context.Context, _ string) ([]float64, map[string]any, error) {
	if m.err != nil {
		return nil, nil, m.err
	}
	return m.embedding, nil, nil
}

func (m *mockEmbedder) GetDimensions() int { return m.dimensions }

func newMockEmbedder(dim int) *mockEmbedder {
	emb := make([]float64, dim)
	for i := range emb {
		emb[i] = float64(i) * 0.01
	}
	return &mockEmbedder{embedding: emb, dimensions: dim}
}

func newMockEmbedderWithError(err error) *mockEmbedder {
	return &mockEmbedder{err: err, dimensions: defaultIndexDimension}
}

// ---------------------------------------------------------------------------
// Mock extractor
// ---------------------------------------------------------------------------

type mockMemoryExtractor struct{}

func (m *mockMemoryExtractor) Extract(_ context.Context, _ []model.Message, _ []*memory.Entry) ([]*extractor.Operation, error) {
	return []*extractor.Operation{{Type: extractor.OperationAdd, Memory: "test", Topics: []string{"t"}}}, nil
}
func (m *mockMemoryExtractor) ShouldExtract(_ *extractor.ExtractionContext) bool { return true }
func (m *mockMemoryExtractor) SetPrompt(_ string)                                {}
func (m *mockMemoryExtractor) SetModel(_ model.Model)                            {}
func (m *mockMemoryExtractor) SetEnabledTools(_ map[string]struct{})             {}
func (m *mockMemoryExtractor) Metadata() map[string]any                          { return nil }

// ---------------------------------------------------------------------------
// Test infrastructure: sqlmock + testClient + setupMockService
// ---------------------------------------------------------------------------

func setupMockDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	return db, mock
}

// testClient wraps *sql.DB to implement storage.Client.
type testClient struct{ db *sql.DB }

func (c *testClient) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return c.db.ExecContext(ctx, query, args...)
}

func (c *testClient) Query(ctx context.Context, next storage.NextFunc, query string, args ...any) error {
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := next(rows); err != nil {
			if err == storage.ErrBreak {
				break
			}
			return err
		}
	}
	return rows.Err()
}

func (c *testClient) QueryRow(ctx context.Context, dest []any, query string, args ...any) error {
	return c.db.QueryRowContext(ctx, query, args...).Scan(dest...)
}

func (c *testClient) Transaction(ctx context.Context, fn storage.TxFunc, _ ...storage.TxOption) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (c *testClient) Close() error { return c.db.Close() }

func setupMockService(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock, opts ...ServiceOpt) *Service {
	t.Helper()
	originalBuilder := storage.GetClientBuilder()
	client := &testClient{db: db}
	storage.SetClientBuilder(func(builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return client, nil
	})
	t.Cleanup(func() { storage.SetClientBuilder(originalBuilder) })

	testOpts := defaultOptions.clone()
	for _, opt := range opts {
		opt(&testOpts)
	}
	// Detect vector support mock (CAST returns error = no vector).
	if !testOpts.skipDBInit {
		mock.ExpectQuery("SELECT 1 FROM").WillReturnError(fmt.Errorf("no vector"))
		mock.ExpectExec("CREATE TABLE IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))
		for i := 0; i < 4; i++ {
			mock.ExpectExec("ALTER TABLE").WillReturnResult(sqlmock.NewResult(0, 0))
		}
	} else {
		// Even with skipDBInit, detectVectorSupport is called.
		mock.ExpectQuery("SELECT 1 FROM").WillReturnError(fmt.Errorf("no vector"))
	}

	if testOpts.embedder == nil {
		dim := testOpts.indexDimension
		if dim == 0 {
			dim = defaultIndexDimension
		}
		opts = append(opts, WithEmbedder(newMockEmbedder(dim)))
	}
	if testOpts.dsn == "" && testOpts.instanceName == "" {
		opts = append(opts, WithMySQLClientDSN("mock:mock@tcp(localhost:3306)/test?parseTime=true"))
	}

	svc, err := NewService(opts...)
	require.NoError(t, err)
	return svc
}

// standard mock row columns
var memCols = []string{
	"memory_id", "app_name", "user_id", "memory_content", "topics",
	"memory_kind", "event_time", "participants", "location",
	"created_at", "updated_at",
}

var memColsWithSimilarity = append(append([]string{}, memCols...), "similarity")
var memColsWithEmbedding = append(append([]string{}, memCols...), "embedding")

// setupMockServiceWithVector creates a Service with supportsVector=true.
func setupMockServiceWithVector(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock, opts ...ServiceOpt) *Service {
	t.Helper()
	originalBuilder := storage.GetClientBuilder()
	client := &testClient{db: db}
	storage.SetClientBuilder(func(builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return client, nil
	})
	t.Cleanup(func() { storage.SetClientBuilder(originalBuilder) })

	// detectVectorSupport returns success → supportsVector=true.
	mock.ExpectQuery("SELECT 1 FROM").WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))

	testOpts := defaultOptions.clone()
	for _, opt := range opts {
		opt(&testOpts)
	}
	if testOpts.embedder == nil {
		dim := testOpts.indexDimension
		if dim == 0 {
			dim = defaultIndexDimension
		}
		opts = append(opts, WithEmbedder(newMockEmbedder(dim)))
	}
	if testOpts.dsn == "" && testOpts.instanceName == "" {
		opts = append(opts, WithMySQLClientDSN("mock:mock@tcp(localhost)/test"))
	}
	opts = append(opts, WithSkipDBInit(true))

	svc, err := NewService(opts...)
	require.NoError(t, err)
	require.True(t, svc.supportsVector)
	return svc
}

func memRow(id, content string, similarity ...float64) []any {
	now := time.Now()
	row := []any{id, "app", "u1", content, `["t"]`, "fact", nil, nil, nil, now, now}
	if len(similarity) > 0 {
		row = append(row, similarity[0])
	}
	return row
}

// ---------------------------------------------------------------------------
// Options tests
// ---------------------------------------------------------------------------

func TestOptions_Clone(t *testing.T) {
	opts := defaultOptions.clone()
	opts.enabledTools["test"] = struct{}{}
	_, exists := defaultOptions.enabledTools["test"]
	assert.False(t, exists)
}

func TestOptions_WithTableName(t *testing.T) {
	opts := &ServiceOpts{}
	WithTableName("my_table")(opts)
	assert.Equal(t, "my_table", opts.tableName)

	assert.Panics(t, func() { WithTableName("")(&ServiceOpts{}) })
	assert.Panics(t, func() { WithTableName("invalid-table!")(&ServiceOpts{}) })
}

func TestOptions_WithIndexDimension(t *testing.T) {
	opts := &ServiceOpts{}
	WithIndexDimension(768)(opts)
	assert.Equal(t, 768, opts.indexDimension)
	WithIndexDimension(0)(opts)
	assert.Equal(t, 768, opts.indexDimension)
	WithIndexDimension(-1)(opts)
	assert.Equal(t, 768, opts.indexDimension)
}

func TestOptions_WithMaxResults(t *testing.T) {
	opts := &ServiceOpts{}
	WithMaxResults(20)(opts)
	assert.Equal(t, 20, opts.maxResults)
	WithMaxResults(0)(opts)
	assert.Equal(t, 20, opts.maxResults)
}

func TestOptions_WithSimilarityThreshold(t *testing.T) {
	opts := &ServiceOpts{}
	WithSimilarityThreshold(0.5)(opts)
	assert.Equal(t, 0.5, opts.similarityThreshold)
	WithSimilarityThreshold(1.5)(opts)
	assert.Equal(t, 0.5, opts.similarityThreshold)
	WithSimilarityThreshold(-0.1)(opts)
	assert.Equal(t, 0.5, opts.similarityThreshold)
	WithSimilarityThreshold(0)(opts)
	assert.Equal(t, 0.0, opts.similarityThreshold)
}

func TestOptions_WithToolEnabled(t *testing.T) {
	opts := defaultOptions.clone()
	WithToolEnabled(memory.DeleteToolName, true)(&opts)
	_, exists := opts.enabledTools[memory.DeleteToolName]
	assert.True(t, exists)
	WithToolEnabled(memory.DeleteToolName, false)(&opts)
	_, exists = opts.enabledTools[memory.DeleteToolName]
	assert.False(t, exists)
	WithToolEnabled("invalid_tool", true)(&opts)
	_, exists = opts.enabledTools["invalid_tool"]
	assert.False(t, exists)
}

func TestOptions_WithCustomTool(t *testing.T) {
	opts := defaultOptions.clone()
	creator := func() tool.Tool { return nil }
	WithCustomTool(memory.AddToolName, creator)(&opts)
	assert.NotNil(t, opts.toolCreators[memory.AddToolName])
	WithCustomTool("invalid_tool", nil)(&opts)
	_, exists := opts.toolCreators["invalid_tool"]
	assert.False(t, exists)
}

func TestOptions_WithSoftDelete(t *testing.T) {
	opts := &ServiceOpts{}
	WithSoftDelete(true)(opts)
	assert.True(t, opts.softDelete)
}

func TestOptions_WithMemoryLimit(t *testing.T) {
	opts := &ServiceOpts{}
	WithMemoryLimit(500)(opts)
	assert.Equal(t, 500, opts.memoryLimit)
}

func TestOptions_WithAsyncMemoryNum(t *testing.T) {
	opts := &ServiceOpts{}
	WithAsyncMemoryNum(3)(opts)
	assert.Equal(t, 3, opts.asyncMemoryNum)
	WithAsyncMemoryNum(0)(opts)
	assert.Equal(t, 1, opts.asyncMemoryNum)
}

func TestOptions_WithSkipDBInit(t *testing.T) {
	opts := &ServiceOpts{}
	WithSkipDBInit(true)(opts)
	assert.True(t, opts.skipDBInit)
}

func TestOptions_WithMySQLInstance(t *testing.T) {
	opts := &ServiceOpts{}
	WithMySQLInstance("my-instance")(opts)
	assert.Equal(t, "my-instance", opts.instanceName)
}

func TestOptions_WithEmbedder(t *testing.T) {
	opts := &ServiceOpts{}
	emb := newMockEmbedder(16)
	WithEmbedder(emb)(opts)
	assert.Equal(t, emb, opts.embedder)
}

func TestOptions_WithExtractor(t *testing.T) {
	opts := &ServiceOpts{}
	ext := &mockMemoryExtractor{}
	WithExtractor(ext)(opts)
	assert.Equal(t, ext, opts.extractor)
}

func TestOptions_WithMemoryQueueSize(t *testing.T) {
	opts := &ServiceOpts{}
	WithMemoryQueueSize(50)(opts)
	assert.Equal(t, 50, opts.memoryQueueSize)
	WithMemoryQueueSize(0)(opts)
	assert.Greater(t, opts.memoryQueueSize, 0)
}

func TestOptions_WithMemoryJobTimeout(t *testing.T) {
	opts := &ServiceOpts{}
	WithMemoryJobTimeout(5 * time.Minute)(opts)
	assert.Equal(t, 5*time.Minute, opts.memoryJobTimeout)
}

func TestOptions_WithExtraOptions(t *testing.T) {
	opts := &ServiceOpts{}
	WithExtraOptions("a", "b")(opts)
	assert.Len(t, opts.extraOptions, 2)
}

func TestOptions_WithAutoMemoryExposedTools(t *testing.T) {
	opts := &ServiceOpts{}
	WithAutoMemoryExposedTools(memory.AddToolName)(opts)
	_, exposed := opts.toolExposed[memory.AddToolName]
	assert.True(t, exposed)
	WithAutoMemoryExposedTools("invalid_tool")(opts)
	_, exposed = opts.toolExposed["invalid_tool"]
	assert.False(t, exposed)
}

func TestOptions_WithToolExposed(t *testing.T) {
	opts := &ServiceOpts{}
	WithToolExposed(memory.AddToolName, true)(opts)
	_, exposed := opts.toolExposed[memory.AddToolName]
	assert.True(t, exposed)
	WithToolExposed(memory.AddToolName, false)(opts)
	_, hidden := opts.toolHidden[memory.AddToolName]
	assert.True(t, hidden)
}

func TestValidateTableName(t *testing.T) {
	assert.NoError(t, validateTableName("memories"))
	assert.NoError(t, validateTableName("my_memories"))
	assert.Error(t, validateTableName(""))
	assert.Error(t, validateTableName(string(make([]byte, 65))))
	assert.Error(t, validateTableName("123table"))
	assert.Error(t, validateTableName("my-table"))
}

func TestIsDuplicateColumnError(t *testing.T) {
	assert.False(t, isDuplicateColumnError(nil))
	assert.False(t, isDuplicateColumnError(assert.AnError))
	assert.True(t, isDuplicateColumnError(fmt.Errorf("Error 1060: Duplicate column name")))
}

// ---------------------------------------------------------------------------
// NewService tests
// ---------------------------------------------------------------------------

func TestNewService_EmbedderRequired(t *testing.T) {
	_, err := NewService(
		WithMySQLClientDSN("user:pass@tcp(localhost)/test"),
		WithSkipDBInit(true),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "embedder is required")
}

func TestNewService_InstanceNameNotFound(t *testing.T) {
	_, err := NewService(
		WithMySQLInstance("non-existent"),
		WithEmbedder(newMockEmbedder(16)),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestNewService_InitDB_Success(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock) // no skipDBInit
	defer svc.Close()
	assert.False(t, svc.supportsVector) // mock returns error for detect
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestNewService_InitDB_CreateTableError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	originalBuilder := storage.GetClientBuilder()
	storage.SetClientBuilder(func(opts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return &testClient{db: db}, nil
	})
	defer storage.SetClientBuilder(originalBuilder)

	mock.ExpectQuery("SELECT 1 FROM").WillReturnError(fmt.Errorf("no vector"))
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS").WillReturnError(fmt.Errorf("disk full"))

	_, err := NewService(
		WithMySQLClientDSN("mock:mock@tcp(localhost)/test"),
		WithEmbedder(newMockEmbedder(16)),
		WithIndexDimension(16),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "init database failed")
}

func TestNewService_WithExtractor(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true), WithExtractor(&mockMemoryExtractor{}))
	defer svc.Close()
	assert.NotNil(t, svc.autoMemoryWorker)
}

// ---------------------------------------------------------------------------
// AddMemory tests
// ---------------------------------------------------------------------------

func TestService_AddMemory(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true), WithMemoryLimit(0))
	defer svc.Close()

	mock.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(1, 1))
	err := svc.AddMemory(context.Background(), memory.UserKey{AppName: "app", UserID: "u1"}, "test memory", []string{"t"})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_AddMemory_InvalidKey(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	err := svc.AddMemory(context.Background(), memory.UserKey{AppName: "", UserID: "u"}, "m", nil)
	assert.Equal(t, memory.ErrAppNameRequired, err)
	err = svc.AddMemory(context.Background(), memory.UserKey{AppName: "a", UserID: ""}, "m", nil)
	assert.Equal(t, memory.ErrUserIDRequired, err)
}

func TestService_AddMemory_EmbeddingError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true), WithMemoryLimit(0), WithEmbedder(newMockEmbedderWithError(fmt.Errorf("fail"))))
	defer svc.Close()

	err := svc.AddMemory(context.Background(), memory.UserKey{AppName: "a", UserID: "u"}, "m", nil)
	assert.Contains(t, err.Error(), "generate embedding failed")
}

func TestService_AddMemory_DimensionMismatch(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true), WithMemoryLimit(0), WithIndexDimension(1536), WithEmbedder(newMockEmbedder(768)))
	defer svc.Close()

	err := svc.AddMemory(context.Background(), memory.UserKey{AppName: "a", UserID: "u"}, "m", nil)
	assert.Contains(t, err.Error(), "embedding dimension mismatch")
}

func TestService_AddMemory_MemoryLimit(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true), WithMemoryLimit(1))
	defer svc.Close()

	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(1, 1))

	err := svc.AddMemory(context.Background(), memory.UserKey{AppName: "a", UserID: "u"}, "m", nil)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_AddMemory_MemoryLimitExceeded(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true), WithMemoryLimit(1))
	defer svc.Close()

	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	err := svc.AddMemory(context.Background(), memory.UserKey{AppName: "a", UserID: "u"}, "m", nil)
	assert.Contains(t, err.Error(), "memory limit exceeded")
}

func TestService_AddMemory_MemoryLimit_SoftDelete(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true), WithMemoryLimit(5), WithSoftDelete(true))
	defer svc.Close()

	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(1, 1))

	err := svc.AddMemory(context.Background(), memory.UserKey{AppName: "a", UserID: "u"}, "m", nil)
	require.NoError(t, err)
}

func TestService_AddMemory_SQLError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true), WithMemoryLimit(0))
	defer svc.Close()

	mock.ExpectExec("INSERT INTO").WillReturnError(fmt.Errorf("insert failed"))
	err := svc.AddMemory(context.Background(), memory.UserKey{AppName: "a", UserID: "u"}, "m", nil)
	assert.Contains(t, err.Error(), "store memory entry failed")
}

func TestService_AddMemory_CountQueryError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true), WithMemoryLimit(1))
	defer svc.Close()

	mock.ExpectQuery("SELECT COUNT").WillReturnError(fmt.Errorf("count fail"))
	err := svc.AddMemory(context.Background(), memory.UserKey{AppName: "a", UserID: "u"}, "m", nil)
	assert.Contains(t, err.Error(), "check memory count failed")
}

// ---------------------------------------------------------------------------
// UpdateMemory tests
// ---------------------------------------------------------------------------

func expectUpdateLoad(mock sqlmock.Sqlmock, key memory.Key, softDelete bool) {
	now := time.Now()
	q := "SELECT memory_id, app_name, user_id, memory_content, topics"
	mock.ExpectQuery(q).
		WithArgs(key.MemoryID, key.AppName, key.UserID).
		WillReturnRows(sqlmock.NewRows(memCols).AddRow(
			key.MemoryID, key.AppName, key.UserID, "old memory", `["old"]`,
			"fact", nil, nil, nil, now, now,
		))
}

func TestService_UpdateMemory(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	key := memory.Key{AppName: "app", UserID: "u1", MemoryID: "mem-1"}
	expectUpdateLoad(mock, key, false)
	// Content changes → ID changes → rotateMemory.
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := svc.UpdateMemory(context.Background(), key, "updated", []string{"new"})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_UpdateMemory_IDChanged_RotateMemory(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	key := memory.Key{AppName: "app", UserID: "u1", MemoryID: "mem-original"}
	now := time.Now()
	// Load returns original entry.
	mock.ExpectQuery("SELECT memory_id").
		WithArgs(key.MemoryID, key.AppName, key.UserID).
		WillReturnRows(sqlmock.NewRows(memCols).AddRow(
			key.MemoryID, key.AppName, key.UserID, "old content", `["old"]`,
			"fact", nil, nil, nil, now, now,
		))
	// Content changed → new ID → rotateMemory: BEGIN + DELETE + INSERT + COMMIT.
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := svc.UpdateMemory(context.Background(), key, "completely different content", []string{"new-topic"})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_UpdateMemory_NotFound(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	key := memory.Key{AppName: "app", UserID: "u1", MemoryID: "non-existent"}
	mock.ExpectQuery("SELECT memory_id").
		WithArgs(key.MemoryID, key.AppName, key.UserID).
		WillReturnRows(sqlmock.NewRows(memCols))

	err := svc.UpdateMemory(context.Background(), key, "x", nil)
	assert.Contains(t, err.Error(), "not found")
}

func TestService_UpdateMemory_InvalidKey(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	err := svc.UpdateMemory(context.Background(), memory.Key{AppName: "", UserID: "u", MemoryID: "id"}, "m", nil)
	assert.Equal(t, memory.ErrAppNameRequired, err)
	err = svc.UpdateMemory(context.Background(), memory.Key{AppName: "a", UserID: "u", MemoryID: ""}, "m", nil)
	assert.Equal(t, memory.ErrMemoryIDRequired, err)
}

func TestService_UpdateMemory_EmbeddingError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true), WithEmbedder(newMockEmbedderWithError(fmt.Errorf("fail"))))
	defer svc.Close()

	key := memory.Key{AppName: "app", UserID: "u1", MemoryID: "mem-1"}
	expectUpdateLoad(mock, key, false)
	err := svc.UpdateMemory(context.Background(), key, "x", nil)
	assert.Contains(t, err.Error(), "generate embedding failed")
}

func TestService_UpdateMemory_DimensionMismatch(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true), WithIndexDimension(1536), WithEmbedder(newMockEmbedder(768)))
	defer svc.Close()

	key := memory.Key{AppName: "app", UserID: "u1", MemoryID: "mem-1"}
	expectUpdateLoad(mock, key, false)
	err := svc.UpdateMemory(context.Background(), key, "x", nil)
	assert.Contains(t, err.Error(), "embedding dimension mismatch")
}

func TestService_UpdateMemory_SameID_UpdateInPlace(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	// Pre-compute the ID that GenerateMemoryID will produce for this content+topics+app+user.
	mem := &memory.Memory{Memory: "same content", Topics: []string{"same"}}
	correctID := imemory.GenerateMemoryID(mem, "app", "u1")

	now := time.Now()
	key := memory.Key{AppName: "app", UserID: "u1", MemoryID: correctID}
	mock.ExpectQuery("SELECT memory_id").
		WithArgs(key.MemoryID, key.AppName, key.UserID).
		WillReturnRows(sqlmock.NewRows(memCols).AddRow(
			key.MemoryID, key.AppName, key.UserID, "same content", `["same"]`,
			"fact", nil, nil, nil, now, now,
		))
	mock.ExpectExec("UPDATE").WillReturnResult(sqlmock.NewResult(0, 1))

	err := svc.UpdateMemory(context.Background(), key, "same content", []string{"same"})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_UpdateMemory_SoftDelete(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true), WithSoftDelete(true))
	defer svc.Close()

	key := memory.Key{AppName: "app", UserID: "u1", MemoryID: "mem-1"}
	expectUpdateLoad(mock, key, true)
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := svc.UpdateMemory(context.Background(), key, "updated", []string{"t"})
	require.NoError(t, err)
}

func TestService_UpdateMemory_SQLError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	key := memory.Key{AppName: "app", UserID: "u1", MemoryID: "mem-1"}
	expectUpdateLoad(mock, key, false)
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM").WillReturnError(fmt.Errorf("timeout"))

	err := svc.UpdateMemory(context.Background(), key, "x", nil)
	assert.Error(t, err)
}

func TestService_UpdateMemory_RowsAffectedError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	key := memory.Key{AppName: "app", UserID: "u1", MemoryID: "mem-1"}
	expectUpdateLoad(mock, key, false)
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM").WillReturnResult(sqlmock.NewErrorResult(fmt.Errorf("rows err")))

	err := svc.UpdateMemory(context.Background(), key, "x", nil)
	assert.Error(t, err)
}

func TestService_UpdateMemory_InPlace_SQLError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	mem := &memory.Memory{Memory: "same", Topics: []string{"s"}}
	correctID := imemory.GenerateMemoryID(mem, "app", "u1")
	key := memory.Key{AppName: "app", UserID: "u1", MemoryID: correctID}
	now := time.Now()
	mock.ExpectQuery("SELECT memory_id").
		WithArgs(key.MemoryID, key.AppName, key.UserID).
		WillReturnRows(sqlmock.NewRows(memCols).AddRow(
			key.MemoryID, key.AppName, key.UserID, "same", `["s"]`,
			"fact", nil, nil, nil, now, now,
		))
	mock.ExpectExec("UPDATE").WillReturnError(fmt.Errorf("update fail"))

	err := svc.UpdateMemory(context.Background(), key, "same", []string{"s"})
	assert.Contains(t, err.Error(), "update memory entry failed")
}

func TestService_UpdateMemory_InPlace_RowsAffectedError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	mem := &memory.Memory{Memory: "same", Topics: []string{"s"}}
	correctID := imemory.GenerateMemoryID(mem, "app", "u1")
	key := memory.Key{AppName: "app", UserID: "u1", MemoryID: correctID}
	now := time.Now()
	mock.ExpectQuery("SELECT memory_id").
		WithArgs(key.MemoryID, key.AppName, key.UserID).
		WillReturnRows(sqlmock.NewRows(memCols).AddRow(
			key.MemoryID, key.AppName, key.UserID, "same", `["s"]`,
			"fact", nil, nil, nil, now, now,
		))
	mock.ExpectExec("UPDATE").WillReturnResult(sqlmock.NewErrorResult(fmt.Errorf("rows err")))

	err := svc.UpdateMemory(context.Background(), key, "same", []string{"s"})
	assert.Contains(t, err.Error(), "rows affected")
}

func TestService_UpdateMemory_InPlace_ZeroRowsAffected(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	mem := &memory.Memory{Memory: "same", Topics: []string{"s"}}
	correctID := imemory.GenerateMemoryID(mem, "app", "u1")
	key := memory.Key{AppName: "app", UserID: "u1", MemoryID: correctID}
	now := time.Now()
	mock.ExpectQuery("SELECT memory_id").
		WithArgs(key.MemoryID, key.AppName, key.UserID).
		WillReturnRows(sqlmock.NewRows(memCols).AddRow(
			key.MemoryID, key.AppName, key.UserID, "same", `["s"]`,
			"fact", nil, nil, nil, now, now,
		))
	mock.ExpectExec("UPDATE").WillReturnResult(sqlmock.NewResult(0, 0))

	err := svc.UpdateMemory(context.Background(), key, "same", []string{"s"})
	assert.Contains(t, err.Error(), "not found")
}

// ---------------------------------------------------------------------------
// DeleteMemory tests
// ---------------------------------------------------------------------------

func TestService_DeleteMemory(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	mock.ExpectExec("DELETE FROM").WillReturnResult(sqlmock.NewResult(0, 1))
	err := svc.DeleteMemory(context.Background(), memory.Key{AppName: "a", UserID: "u", MemoryID: "m"})
	require.NoError(t, err)
}

func TestService_DeleteMemory_SoftDelete(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true), WithSoftDelete(true))
	defer svc.Close()

	mock.ExpectExec("UPDATE").WillReturnResult(sqlmock.NewResult(0, 1))
	err := svc.DeleteMemory(context.Background(), memory.Key{AppName: "a", UserID: "u", MemoryID: "m"})
	require.NoError(t, err)
}

func TestService_DeleteMemory_InvalidKey(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	err := svc.DeleteMemory(context.Background(), memory.Key{AppName: "", UserID: "u", MemoryID: "m"})
	assert.Equal(t, memory.ErrAppNameRequired, err)
}

func TestService_DeleteMemory_SQLError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	mock.ExpectExec("DELETE FROM").WillReturnError(fmt.Errorf("db error"))
	err := svc.DeleteMemory(context.Background(), memory.Key{AppName: "a", UserID: "u", MemoryID: "m"})
	assert.Contains(t, err.Error(), "delete memory entry failed")
}

// ---------------------------------------------------------------------------
// ClearMemories tests
// ---------------------------------------------------------------------------

func TestService_ClearMemories(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	mock.ExpectExec("DELETE FROM").WillReturnResult(sqlmock.NewResult(0, 3))
	err := svc.ClearMemories(context.Background(), memory.UserKey{AppName: "a", UserID: "u"})
	require.NoError(t, err)
}

func TestService_ClearMemories_SoftDelete(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true), WithSoftDelete(true))
	defer svc.Close()

	mock.ExpectExec("UPDATE").WillReturnResult(sqlmock.NewResult(0, 3))
	err := svc.ClearMemories(context.Background(), memory.UserKey{AppName: "a", UserID: "u"})
	require.NoError(t, err)
}

func TestService_ClearMemories_InvalidKey(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	err := svc.ClearMemories(context.Background(), memory.UserKey{AppName: "", UserID: "u"})
	assert.Equal(t, memory.ErrAppNameRequired, err)
}

func TestService_ClearMemories_SQLError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	mock.ExpectExec("DELETE FROM").WillReturnError(fmt.Errorf("err"))
	err := svc.ClearMemories(context.Background(), memory.UserKey{AppName: "a", UserID: "u"})
	assert.Contains(t, err.Error(), "clear memories failed")
}

// ---------------------------------------------------------------------------
// ReadMemories tests
// ---------------------------------------------------------------------------

func TestService_ReadMemories(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	now := time.Now()
	mock.ExpectQuery("SELECT memory_id").WillReturnRows(
		sqlmock.NewRows(memCols).
			AddRow("m1", "app", "u1", "memory 1", `["t1"]`, "fact", nil, nil, nil, now, now).
			AddRow("m2", "app", "u1", "memory 2", `["t2"]`, "fact", nil, nil, nil, now, now),
	)

	entries, err := svc.ReadMemories(context.Background(), memory.UserKey{AppName: "app", UserID: "u1"}, 10)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, "memory 1", entries[0].Memory.Memory)
}

func TestService_ReadMemories_Empty(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	mock.ExpectQuery("SELECT memory_id").WillReturnRows(sqlmock.NewRows(memCols))
	entries, err := svc.ReadMemories(context.Background(), memory.UserKey{AppName: "a", UserID: "u"}, 10)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestService_ReadMemories_InvalidKey(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	_, err := svc.ReadMemories(context.Background(), memory.UserKey{AppName: "", UserID: "u"}, 10)
	assert.Equal(t, memory.ErrAppNameRequired, err)
}

func TestService_ReadMemories_SQLError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	mock.ExpectQuery("SELECT memory_id").WillReturnError(fmt.Errorf("timeout"))
	_, err := svc.ReadMemories(context.Background(), memory.UserKey{AppName: "a", UserID: "u"}, 10)
	assert.Contains(t, err.Error(), "list memories failed")
}

func TestService_ReadMemories_SoftDelete(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true), WithSoftDelete(true))
	defer svc.Close()

	mock.ExpectQuery("SELECT memory_id").WillReturnRows(sqlmock.NewRows(memCols))
	entries, err := svc.ReadMemories(context.Background(), memory.UserKey{AppName: "a", UserID: "u"}, 0)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestService_ReadMemories_ScanError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	mock.ExpectQuery("SELECT memory_id").WillReturnRows(
		sqlmock.NewRows(memCols).AddRow("m", "a", "u", "x", nil, "fact", nil, nil, nil, "bad-time", "bad"),
	)
	_, err := svc.ReadMemories(context.Background(), memory.UserKey{AppName: "a", UserID: "u"}, 10)
	assert.Contains(t, err.Error(), "list memories failed")
}

// ---------------------------------------------------------------------------
// SearchMemories tests (brute-force path since supportsVector=false)
// ---------------------------------------------------------------------------

func TestService_SearchMemories(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	now := time.Now()
	emb := serializeVector(svc.opts.embedder.(*mockEmbedder).embedding)
	mock.ExpectQuery("SELECT memory_id").WillReturnRows(
		sqlmock.NewRows(memColsWithEmbedding).
			AddRow("m1", "app", "u1", "coffee tips", `["hobby"]`, "fact", nil, nil, nil, now, now, emb).
			AddRow("m2", "app", "u1", "likes coffee", `["profile"]`, "fact", nil, nil, nil, now, now, emb),
	)

	results, err := svc.SearchMemories(context.Background(), memory.UserKey{AppName: "app", UserID: "u1"}, "coffee")
	require.NoError(t, err)
	require.Len(t, results, 2)
}

func TestService_SearchMemories_EmptyQuery(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	results, err := svc.SearchMemories(context.Background(), memory.UserKey{AppName: "a", UserID: "u"}, "")
	require.NoError(t, err)
	assert.Empty(t, results)
	results, err = svc.SearchMemories(context.Background(), memory.UserKey{AppName: "a", UserID: "u"}, "   ")
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestService_SearchMemories_InvalidKey(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	_, err := svc.SearchMemories(context.Background(), memory.UserKey{AppName: "", UserID: "u"}, "q")
	assert.Equal(t, memory.ErrAppNameRequired, err)
}

func TestService_SearchMemories_EmbeddingError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true), WithEmbedder(newMockEmbedderWithError(fmt.Errorf("fail"))))
	defer svc.Close()

	_, err := svc.SearchMemories(context.Background(), memory.UserKey{AppName: "a", UserID: "u"}, "q")
	assert.Contains(t, err.Error(), "generate query embedding failed")
}

func TestService_SearchMemories_DimensionMismatch(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true), WithIndexDimension(1536), WithEmbedder(newMockEmbedder(768)))
	defer svc.Close()

	_, err := svc.SearchMemories(context.Background(), memory.UserKey{AppName: "a", UserID: "u"}, "q")
	assert.Contains(t, err.Error(), "query embedding dimension mismatch")
}

func TestService_SearchMemories_SQLError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	mock.ExpectQuery("SELECT memory_id").WillReturnError(fmt.Errorf("search failed"))
	_, err := svc.SearchMemories(context.Background(), memory.UserKey{AppName: "a", UserID: "u"}, "q")
	assert.Contains(t, err.Error(), "search memories failed")
}

func TestService_SearchMemories_ThresholdAndDeduplicate(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	dim := defaultIndexDimension
	// Use specific embeddings to control similarity scores.
	emb1 := make([]float64, dim)
	emb2 := make([]float64, dim)
	for i := range emb1 {
		emb1[i] = float64(i) * 0.01
		emb2[i] = float64(i) * 0.001 // very different
	}
	svc := setupMockService(t, db, mock, WithSkipDBInit(true), WithSimilarityThreshold(0))
	defer svc.Close()

	now := time.Now()
	blob1 := serializeVector(emb1)
	blob2 := serializeVector(emb2)
	mock.ExpectQuery("SELECT memory_id").WillReturnRows(
		sqlmock.NewRows(memColsWithEmbedding).
			AddRow("m1", "app", "u1", "Alice hiking", `["t"]`, "fact", nil, nil, nil, now, now, blob1).
			AddRow("m2", "app", "u1", "Alice hiking", `["t"]`, "fact", nil, nil, nil, now, now, blob1). // duplicate content
			AddRow("m3", "app", "u1", "Different", `["t"]`, "fact", nil, nil, nil, now, now, blob2),
	)

	results, err := svc.SearchMemories(context.Background(), memory.UserKey{AppName: "app", UserID: "u1"}, "hiking",
		memory.WithSearchOptions(memory.SearchOptions{
			Query:       "hiking",
			Deduplicate: true,
		}),
	)
	require.NoError(t, err)
	// Should deduplicate m1 and m2 (same content).
	require.Len(t, results, 2)
}

func TestService_SearchMemories_KindFallback(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true), WithSimilarityThreshold(0))
	defer svc.Close()

	now := time.Now()
	emb := serializeVector(svc.opts.embedder.(*mockEmbedder).embedding)
	// First query (filtered by kind) returns 1 result (< minKindFallbackResults).
	mock.ExpectQuery("SELECT memory_id").WillReturnRows(
		sqlmock.NewRows(memColsWithEmbedding).
			AddRow("m1", "app", "u1", "Episode", `["t"]`, "episode", nil, nil, nil, now, now, emb),
	)
	// Fallback query (no kind filter) returns more.
	mock.ExpectQuery("SELECT memory_id").WillReturnRows(
		sqlmock.NewRows(memColsWithEmbedding).
			AddRow("m2", "app", "u1", "Fact", `["t"]`, "fact", nil, nil, nil, now, now, emb).
			AddRow("m3", "app", "u1", "Another fact", `["t"]`, "fact", nil, nil, nil, now, now, emb),
	)

	results, err := svc.SearchMemories(context.Background(), memory.UserKey{AppName: "app", UserID: "u1"}, "q",
		memory.WithSearchOptions(memory.SearchOptions{
			Query:        "q",
			Kind:         memory.KindEpisode,
			KindFallback: true,
		}),
	)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(results), 1)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_SearchMemories_HybridSearch(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true), WithSimilarityThreshold(0))
	defer svc.Close()

	now := time.Now()
	emb := serializeVector(svc.opts.embedder.(*mockEmbedder).embedding)
	// Vector search.
	mock.ExpectQuery("SELECT memory_id").WillReturnRows(
		sqlmock.NewRows(memColsWithEmbedding).
			AddRow("m1", "app", "u1", "Vector result", `["t"]`, "fact", nil, nil, nil, now, now, emb),
	)
	// Keyword search.
	mock.ExpectQuery("SELECT memory_id").WillReturnRows(
		sqlmock.NewRows(memColsWithSimilarity).
			AddRow("m2", "app", "u1", "Keyword result", `["t"]`, "fact", nil, nil, nil, now, now, 0.5),
	)

	results, err := svc.SearchMemories(context.Background(), memory.UserKey{AppName: "app", UserID: "u1"}, "coffee",
		memory.WithSearchOptions(memory.SearchOptions{
			Query:        "coffee",
			HybridSearch: true,
		}),
	)
	require.NoError(t, err)
	require.NotEmpty(t, results)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_SearchMemories_KindFilter(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true), WithSimilarityThreshold(0))
	defer svc.Close()

	now := time.Now()
	emb := serializeVector(svc.opts.embedder.(*mockEmbedder).embedding)
	mock.ExpectQuery("SELECT memory_id").WillReturnRows(
		sqlmock.NewRows(memColsWithEmbedding).
			AddRow("m1", "app", "u1", "Fact", `["t"]`, "fact", nil, nil, nil, now, now, emb),
	)

	results, err := svc.SearchMemories(context.Background(), memory.UserKey{AppName: "app", UserID: "u1"}, "q",
		memory.WithSearchOptions(memory.SearchOptions{
			Query: "q",
			Kind:  memory.KindFact,
		}),
	)
	require.NoError(t, err)
	require.Len(t, results, 1)
}

func TestService_SearchMemories_TimeFilters(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true), WithSimilarityThreshold(0))
	defer svc.Close()

	after := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	before := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)

	now := time.Now()
	emb := serializeVector(svc.opts.embedder.(*mockEmbedder).embedding)
	mock.ExpectQuery("SELECT memory_id").WillReturnRows(
		sqlmock.NewRows(memColsWithEmbedding).
			AddRow("m1", "app", "u1", "result", `["t"]`, "fact", nil, nil, nil, now, now, emb),
	)

	results, err := svc.SearchMemories(context.Background(), memory.UserKey{AppName: "app", UserID: "u1"}, "q",
		memory.WithSearchOptions(memory.SearchOptions{
			Query:      "q",
			TimeAfter:  &after,
			TimeBefore: &before,
		}),
	)
	require.NoError(t, err)
	require.Len(t, results, 1)
}

func TestService_SearchMemories_SoftDelete(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true), WithSoftDelete(true), WithSimilarityThreshold(0))
	defer svc.Close()

	now := time.Now()
	emb := serializeVector(svc.opts.embedder.(*mockEmbedder).embedding)
	mock.ExpectQuery("SELECT memory_id").WillReturnRows(
		sqlmock.NewRows(memColsWithEmbedding).
			AddRow("m1", "app", "u1", "result", `["t"]`, "fact", nil, nil, nil, now, now, emb),
	)

	results, err := svc.SearchMemories(context.Background(), memory.UserKey{AppName: "app", UserID: "u1"}, "q")
	require.NoError(t, err)
	require.Len(t, results, 1)
}

func TestService_SearchMemories_MaxResults(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true), WithSimilarityThreshold(0))
	defer svc.Close()

	now := time.Now()
	emb := serializeVector(svc.opts.embedder.(*mockEmbedder).embedding)
	mock.ExpectQuery("SELECT memory_id").WillReturnRows(
		sqlmock.NewRows(memColsWithEmbedding).
			AddRow("m1", "app", "u1", "r1", `["t"]`, "fact", nil, nil, nil, now, now, emb).
			AddRow("m2", "app", "u1", "r2", `["t"]`, "fact", nil, nil, nil, now, now, emb).
			AddRow("m3", "app", "u1", "r3", `["t"]`, "fact", nil, nil, nil, now, now, emb),
	)

	results, err := svc.SearchMemories(context.Background(), memory.UserKey{AppName: "app", UserID: "u1"}, "q",
		memory.WithSearchOptions(memory.SearchOptions{Query: "q", MaxResults: 2}),
	)
	require.NoError(t, err)
	require.Len(t, results, 2)
}

// ---------------------------------------------------------------------------
// executeKeywordSearch tests
// ---------------------------------------------------------------------------

func TestExecuteKeywordSearch_EmptyQuery(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	results, err := svc.executeKeywordSearch(context.Background(),
		memory.UserKey{AppName: "a", UserID: "u"}, memory.SearchOptions{Query: "   "}, 5)
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestExecuteKeywordSearch_Error(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	mock.ExpectQuery("SELECT memory_id").WillReturnError(fmt.Errorf("fulltext error"))
	results, err := svc.executeKeywordSearch(context.Background(),
		memory.UserKey{AppName: "a", UserID: "u"}, memory.SearchOptions{Query: "coffee"}, 5)
	require.NoError(t, err) // non-fatal
	assert.Empty(t, results)
}

func TestExecuteKeywordSearch_WithKindAndTimeFilters(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true), WithSoftDelete(true))
	defer svc.Close()

	after := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	before := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)
	now := time.Now()
	mock.ExpectQuery("SELECT memory_id").WillReturnRows(
		sqlmock.NewRows(memColsWithSimilarity).
			AddRow("m1", "a", "u", "result", `["t"]`, "episode", now, nil, nil, now, now, 0.8),
	)

	results, err := svc.executeKeywordSearch(context.Background(),
		memory.UserKey{AppName: "a", UserID: "u"},
		memory.SearchOptions{
			Query:      "coffee",
			Kind:       memory.KindEpisode,
			TimeAfter:  &after,
			TimeBefore: &before,
		}, 5)
	require.NoError(t, err)
	require.Len(t, results, 1)
}

// ---------------------------------------------------------------------------
// Tools / Close / EnqueueAutoMemoryJob tests
// ---------------------------------------------------------------------------

func TestService_Tools(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	tools := svc.Tools()
	require.NotEmpty(t, tools)
	names := make(map[string]bool)
	for _, tl := range tools {
		if decl := tl.Declaration(); decl != nil {
			names[decl.Name] = true
		}
	}
	assert.True(t, names[memory.AddToolName])
	assert.True(t, names[memory.SearchToolName])
	assert.False(t, names[memory.DeleteToolName])
}

func TestService_Tools_Disabled(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true), WithToolEnabled(memory.SearchToolName, false))
	defer svc.Close()

	for _, tl := range svc.Tools() {
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
	require.NoError(t, svc.Close())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestService_Close_NilDB(t *testing.T) {
	svc := &Service{db: nil}
	require.NoError(t, svc.Close())
}

func TestService_EnqueueAutoMemoryJob_NoWorker(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true))
	defer svc.Close()

	require.NoError(t, svc.EnqueueAutoMemoryJob(context.Background(), nil))
}

func TestService_EnqueueAutoMemoryJob_WithWorker(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true), WithExtractor(&mockMemoryExtractor{}))
	defer svc.Close()

	require.NoError(t, svc.EnqueueAutoMemoryJob(context.Background(), nil))
}

// ---------------------------------------------------------------------------
// Helper function tests
// ---------------------------------------------------------------------------

func TestParseJSONStringSlice(t *testing.T) {
	assert.Nil(t, parseJSONStringSlice(""))
	assert.Nil(t, parseJSONStringSlice("null"))
	assert.Equal(t, []string{"a", "b"}, parseJSONStringSlice(`["a","b"]`))
	assert.Equal(t, []string{}, parseJSONStringSlice(`[]`))
	assert.Nil(t, parseJSONStringSlice(`not json`))
}

func TestResolveMetadata(t *testing.T) {
	f := resolveMetadata(nil)
	assert.Empty(t, f.kind)
	assert.Nil(t, f.participants)

	f = resolveMetadata(&memory.Memory{
		Kind:         memory.KindEpisode,
		Location:     "Tokyo",
		Participants: []string{"Alice"},
	})
	assert.Equal(t, "episode", f.kind)
	assert.NotNil(t, f.location)
	assert.Equal(t, "Tokyo", *f.location)
	assert.NotNil(t, f.participants)
	assert.Contains(t, *f.participants, "Alice")
}

func TestBuildEntry(t *testing.T) {
	now := time.Now()
	entry := buildEntry("m1", "app", "user", "content",
		sql.NullString{}, "fact",
		sql.NullTime{}, sql.NullString{}, sql.NullString{},
		sql.NullTime{Valid: true, Time: now}, sql.NullTime{Valid: true, Time: now})
	assert.Equal(t, "m1", entry.ID)
	assert.Equal(t, "content", entry.Memory.Memory)
	assert.Equal(t, memory.KindFact, entry.Memory.Kind)
}

func TestBuildEntry_WithEpisodicFields(t *testing.T) {
	now := time.Now()
	entry := buildEntry("m2", "app", "user", "hiking",
		sql.NullString{Valid: true, String: `["travel"]`}, "episode",
		sql.NullTime{Valid: true, Time: now},
		sql.NullString{Valid: true, String: `["Alice"]`},
		sql.NullString{Valid: true, String: "Kyoto"},
		sql.NullTime{Valid: true, Time: now}, sql.NullTime{Valid: true, Time: now})
	assert.Equal(t, memory.KindEpisode, entry.Memory.Kind)
	assert.Equal(t, "Kyoto", entry.Memory.Location)
	assert.NotNil(t, entry.Memory.EventTime)
	assert.Equal(t, []string{"Alice"}, entry.Memory.Participants)
}

// ---------------------------------------------------------------------------
// vectorSearch path tests (supportsVector=true)
// ---------------------------------------------------------------------------

func TestService_VectorSearch_Path(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockServiceWithVector(t, db, mock, WithSimilarityThreshold(0))
	defer svc.Close()

	now := time.Now()
	mock.ExpectQuery("SELECT memory_id").WillReturnRows(
		sqlmock.NewRows(memColsWithSimilarity).
			AddRow("m1", "app", "u1", "result", `["t"]`, "fact", nil, nil, nil, now, now, 0.9),
	)

	results, err := svc.SearchMemories(context.Background(), memory.UserKey{AppName: "app", UserID: "u1"}, "q")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, 0.9, results[0].Score)
}

func TestService_VectorSearch_SQLError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockServiceWithVector(t, db, mock)
	defer svc.Close()

	mock.ExpectQuery("SELECT memory_id").WillReturnError(fmt.Errorf("vector search fail"))
	_, err := svc.SearchMemories(context.Background(), memory.UserKey{AppName: "a", UserID: "u"}, "q")
	assert.Contains(t, err.Error(), "vector search memories failed")
}

func TestService_VectorSearch_WithFilters(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockServiceWithVector(t, db, mock, WithSoftDelete(true), WithSimilarityThreshold(0))
	defer svc.Close()

	after := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	before := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)
	now := time.Now()
	mock.ExpectQuery("SELECT memory_id").WillReturnRows(
		sqlmock.NewRows(memColsWithSimilarity).
			AddRow("m1", "app", "u1", "episode", `["t"]`, "episode", now, nil, nil, now, now, 0.85),
	)

	results, err := svc.SearchMemories(context.Background(), memory.UserKey{AppName: "app", UserID: "u1"}, "q",
		memory.WithSearchOptions(memory.SearchOptions{
			Query:      "q",
			Kind:       memory.KindEpisode,
			TimeAfter:  &after,
			TimeBefore: &before,
		}),
	)
	require.NoError(t, err)
	require.Len(t, results, 1)
}

func TestService_VectorSearch_KindFact(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockServiceWithVector(t, db, mock, WithSimilarityThreshold(0))
	defer svc.Close()

	now := time.Now()
	mock.ExpectQuery("SELECT memory_id").WillReturnRows(
		sqlmock.NewRows(memColsWithSimilarity).
			AddRow("m1", "app", "u1", "fact", `["t"]`, "fact", nil, nil, nil, now, now, 0.8),
	)

	results, err := svc.SearchMemories(context.Background(), memory.UserKey{AppName: "app", UserID: "u1"}, "q",
		memory.WithSearchOptions(memory.SearchOptions{
			Query: "q",
			Kind:  memory.KindFact,
		}),
	)
	require.NoError(t, err)
	require.Len(t, results, 1)
}

func TestService_VectorSearch_ScanError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockServiceWithVector(t, db, mock)
	defer svc.Close()

	mock.ExpectQuery("SELECT memory_id").WillReturnRows(
		sqlmock.NewRows(memColsWithSimilarity).
			AddRow("m1", "a", "u", "x", nil, "fact", nil, nil, nil, "bad-time", "bad", 0.9),
	)
	_, err := svc.SearchMemories(context.Background(), memory.UserKey{AppName: "a", UserID: "u"}, "q")
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// initDB tests (MySQL 9.0+ VECTOR path)
// ---------------------------------------------------------------------------

func TestNewService_InitDB_VectorPath(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	originalBuilder := storage.GetClientBuilder()
	client := &testClient{db: db}
	storage.SetClientBuilder(func(opts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return client, nil
	})
	defer storage.SetClientBuilder(originalBuilder)

	// detectVectorSupport returns success.
	mock.ExpectQuery("SELECT 1 FROM").WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	// initDB with VECTOR type.
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))
	for i := 0; i < 4; i++ {
		mock.ExpectExec("ALTER TABLE").WillReturnResult(sqlmock.NewResult(0, 0))
	}

	svc, err := NewService(
		WithMySQLClientDSN("mock:mock@tcp(localhost)/test"),
		WithEmbedder(newMockEmbedder(16)),
		WithIndexDimension(16),
	)
	require.NoError(t, err)
	assert.True(t, svc.supportsVector)
	svc.Close()
}

func TestNewService_InitDB_AlterTableError(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()

	originalBuilder := storage.GetClientBuilder()
	client := &testClient{db: db}
	storage.SetClientBuilder(func(opts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return client, nil
	})
	defer storage.SetClientBuilder(originalBuilder)

	mock.ExpectQuery("SELECT 1 FROM").WillReturnError(fmt.Errorf("no vector"))
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))
	// First ALTER succeeds, second fails with non-1060 error.
	mock.ExpectExec("ALTER TABLE").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("ALTER TABLE").WillReturnError(fmt.Errorf("syntax error"))

	_, err := NewService(
		WithMySQLClientDSN("mock:mock@tcp(localhost)/test"),
		WithEmbedder(newMockEmbedder(16)),
		WithIndexDimension(16),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "add episodic column")
}

// ---------------------------------------------------------------------------
// AddMemory with vector path
// ---------------------------------------------------------------------------

func TestService_AddMemory_VectorPath(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockServiceWithVector(t, db, mock, WithMemoryLimit(0))
	defer svc.Close()

	mock.ExpectExec("INSERT INTO").WillReturnResult(sqlmock.NewResult(1, 1))
	err := svc.AddMemory(context.Background(), memory.UserKey{AppName: "a", UserID: "u"}, "m", nil)
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Scan error tests (embedding path)
// ---------------------------------------------------------------------------

func TestService_ScanEntryWithEmbedding_Error(t *testing.T) {
	db, mock := setupMockDB(t)
	defer db.Close()
	svc := setupMockService(t, db, mock, WithSkipDBInit(true), WithSimilarityThreshold(0))
	defer svc.Close()

	mock.ExpectQuery("SELECT memory_id").WillReturnRows(
		sqlmock.NewRows(memColsWithEmbedding).
			AddRow("m1", "a", "u", "x", nil, "fact", nil, nil, nil, "bad-time", "bad", []byte{1, 2}),
	)
	_, err := svc.SearchMemories(context.Background(), memory.UserKey{AppName: "a", UserID: "u"}, "q")
	assert.Contains(t, err.Error(), "brute force search memories failed")
}

// ---------------------------------------------------------------------------
// WithCustomTool valid name test
// ---------------------------------------------------------------------------

func TestOptions_WithCustomTool_ValidName(t *testing.T) {
	opts := defaultOptions.clone()
	creator := func() tool.Tool { return nil }
	WithCustomTool(memory.SearchToolName, creator)(&opts)
	assert.NotNil(t, opts.toolCreators[memory.SearchToolName])
	_, exists := opts.enabledTools[memory.SearchToolName]
	assert.True(t, exists)
}
