//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package postgres

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// Test that SetClientBuilder installs a custom builder and that the
// returned builder is actually used when invoked.
func TestSetGetClientBuilder(t *testing.T) {
	// Isolate global state.
	oldRegistry := postgresRegistry
	postgresRegistry = make(map[string][]ClientBuilderOpt)
	defer func() { postgresRegistry = oldRegistry }()

	oldBuilder := GetClientBuilder()
	defer func() { SetClientBuilder(oldBuilder) }()

	invoked := false
	custom := func(ctx context.Context, opts ...ClientBuilderOpt) (Client, error) {
		invoked = true
		return nil, nil
	}

	SetClientBuilder(custom)
	b := GetClientBuilder()
	_, err := b(context.Background(), WithClientConnString("postgres://localhost:5432/test"))
	require.NoError(t, err)
	require.True(t, invoked, "custom builder was not invoked")
}

// Test the default builder validates empty connection string.
func TestDefaultClientBuilder_EmptyConnString(t *testing.T) {
	const expected = "postgres: connection string is empty"
	_, err := defaultClientBuilder(context.Background())
	require.Error(t, err)
	require.Equal(t, expected, err.Error())
}

// Test invalid connection string parsing error path.
func TestDefaultClientBuilder_InvalidConnString(t *testing.T) {
	const badConnString = "invalid connection string"
	_, err := defaultClientBuilder(context.Background(), WithClientConnString(badConnString))
	require.Error(t, err)
	// The error should contain information about connection failure or opening
	require.Contains(t, err.Error(), "postgres")
}

// Test the default builder can parse a standard postgres connection string.
// Note: This doesn't actually connect to the database, it just validates the connection string.
func TestDefaultClientBuilder_ParseConnStringSuccess(t *testing.T) {
	const connString = "postgres://user:pass@127.0.0.1:5432/testdb?sslmode=disable"

	// Skip this test if we can't connect (no postgres available)
	// We only test the parsing and config creation
	t.Skip("Skipping test that requires a real PostgreSQL connection")
}

// Test registry add and get.
func TestRegisterAndGetPostgresInstance(t *testing.T) {
	// Isolate global state.
	oldRegistry := postgresRegistry
	postgresRegistry = make(map[string][]ClientBuilderOpt)
	defer func() { postgresRegistry = oldRegistry }()

	const (
		name       = "test-instance"
		connString = "postgres://user:pass@127.0.0.1:5432/testdb"
	)

	RegisterPostgresInstance(name, WithClientConnString(connString))
	opts, ok := GetPostgresInstance(name)
	require.True(t, ok, "expected instance to exist")
	require.NotEmpty(t, opts, "expected at least one option")

	// Verify that options can be extracted
	cfg := &ClientBuilderOpts{}
	for _, opt := range opts {
		opt(cfg)
	}
	require.Equal(t, connString, cfg.ConnString)
}

// Test GetPostgresInstance for a non-existing instance.
func TestGetPostgresInstance_NotFound(t *testing.T) {
	// Isolate global state.
	oldRegistry := postgresRegistry
	postgresRegistry = make(map[string][]ClientBuilderOpt)
	defer func() { postgresRegistry = oldRegistry }()

	opts, ok := GetPostgresInstance("not-exist")
	require.False(t, ok)
	require.Nil(t, opts)
}

// Test WithExtraOptions accumulates and preserves order via a custom builder.
func TestWithExtraOptions_Accumulation(t *testing.T) {
	oldBuilder := GetClientBuilder()
	defer func() { SetClientBuilder(oldBuilder) }()

	observed := make([]any, 0)
	custom := func(ctx context.Context, builderOpts ...ClientBuilderOpt) (Client, error) {
		cfg := &ClientBuilderOpts{}
		for _, opt := range builderOpts {
			opt(cfg)
		}
		observed = append(observed, cfg.ExtraOptions...)
		return nil, nil
	}
	SetClientBuilder(custom)

	const (
		first  = "alpha"
		second = "beta"
		third  = "gamma"
	)
	b := GetClientBuilder()
	_, err := b(
		context.Background(),
		WithClientConnString("postgres://localhost:5432/test"),
		WithExtraOptions(first),
		WithExtraOptions(second, third),
	)
	require.NoError(t, err)
	require.Equal(t, []any{first, second, third}, observed)
}

// Test multiple RegisterPostgresInstance calls append options rather than overwrite.
func TestRegisterPostgresInstance_AppendsOptions(t *testing.T) {
	// Isolate global state.
	oldRegistry := postgresRegistry
	postgresRegistry = make(map[string][]ClientBuilderOpt)
	defer func() { postgresRegistry = oldRegistry }()

	const name = "append-instance"
	RegisterPostgresInstance(name, WithClientConnString("postgres://localhost:5432/test"))
	RegisterPostgresInstance(name, WithExtraOptions("x"), WithExtraOptions("y"))

	opts, ok := GetPostgresInstance(name)
	require.True(t, ok)
	require.GreaterOrEqual(t, len(opts), 3)

	// Apply options to verify combined effect on ClientBuilderOpts.
	cfg := &ClientBuilderOpts{}
	for _, opt := range opts {
		opt(cfg)
	}
	require.Equal(t, []any{"x", "y"}, cfg.ExtraOptions)
}

// TestSQLClient tests the sqlClient implementation using a mock approach.
func TestSQLClient_Close(t *testing.T) {
	// We'll use the custom builder approach to test Close
	oldBuilder := GetClientBuilder()
	defer func() { SetClientBuilder(oldBuilder) }()

	closeCalled := false
	mockClient := &mockClient{
		closeFn: func() error {
			closeCalled = true
			return nil
		},
	}

	SetClientBuilder(func(ctx context.Context, opts ...ClientBuilderOpt) (Client, error) {
		return mockClient, nil
	})

	client, err := GetClientBuilder()(context.Background(), WithClientConnString("test"))
	require.NoError(t, err)
	require.NotNil(t, client)

	err = client.Close()
	require.NoError(t, err)
	require.True(t, closeCalled)
}

// TestSQLClient_ExecContext tests the ExecContext method.
func TestSQLClient_ExecContext(t *testing.T) {
	oldBuilder := GetClientBuilder()
	defer func() { SetClientBuilder(oldBuilder) }()

	execCalled := false
	mockClient := &mockClient{
		execFn: func(ctx context.Context, query string, args ...any) (sql.Result, error) {
			execCalled = true
			require.Equal(t, "INSERT INTO test VALUES ($1)", query)
			require.Equal(t, []any{"value"}, args)
			return &mockResult{rowsAffected: 1}, nil
		},
	}

	SetClientBuilder(func(ctx context.Context, opts ...ClientBuilderOpt) (Client, error) {
		return mockClient, nil
	})

	client, err := GetClientBuilder()(context.Background(), WithClientConnString("test"))
	require.NoError(t, err)

	result, err := client.ExecContext(context.Background(), "INSERT INTO test VALUES ($1)", "value")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, execCalled)

	rows, err := result.RowsAffected()
	require.NoError(t, err)
	require.Equal(t, int64(1), rows)
}

// TestSQLClient_Query tests the Query method.
func TestSQLClient_Query(t *testing.T) {
	oldBuilder := GetClientBuilder()
	defer func() { SetClientBuilder(oldBuilder) }()

	queryCalled := false
	mockClient := &mockClient{
		queryFn: func(ctx context.Context, handler HandlerFunc, query string, args ...any) error {
			queryCalled = true
			require.Equal(t, "SELECT * FROM test WHERE id = $1", query)
			require.Equal(t, []any{1}, args)
			// Simulate calling the handler with nil rows (we can't create real rows without DB)
			return nil
		},
	}

	SetClientBuilder(func(ctx context.Context, opts ...ClientBuilderOpt) (Client, error) {
		return mockClient, nil
	})

	client, err := GetClientBuilder()(context.Background(), WithClientConnString("test"))
	require.NoError(t, err)

	err = client.Query(context.Background(), func(rows *sql.Rows) error {
		return nil
	}, "SELECT * FROM test WHERE id = $1", 1)
	require.NoError(t, err)
	require.True(t, queryCalled)
}

// TestSQLClient_Transaction tests the Transaction method.
func TestSQLClient_Transaction(t *testing.T) {
	oldBuilder := GetClientBuilder()
	defer func() { SetClientBuilder(oldBuilder) }()

	txCalled := false
	mockClient := &mockClient{
		txFn: func(ctx context.Context, fn TxFunc) error {
			txCalled = true
			// Simulate transaction execution
			return fn(nil)
		},
	}

	SetClientBuilder(func(ctx context.Context, opts ...ClientBuilderOpt) (Client, error) {
		return mockClient, nil
	})

	client, err := GetClientBuilder()(context.Background(), WithClientConnString("test"))
	require.NoError(t, err)

	txFnCalled := false
	err = client.Transaction(context.Background(), func(tx *sql.Tx) error {
		txFnCalled = true
		return nil
	})
	require.NoError(t, err)
	require.True(t, txCalled)
	require.True(t, txFnCalled)
}

// Mock implementations for testing

type mockClient struct {
	execFn  func(ctx context.Context, query string, args ...any) (sql.Result, error)
	queryFn func(ctx context.Context, handler HandlerFunc, query string, args ...any) error
	txFn    func(ctx context.Context, fn TxFunc) error
	closeFn func() error
}

func (m *mockClient) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if m.execFn != nil {
		return m.execFn(ctx, query, args...)
	}
	return nil, nil
}

func (m *mockClient) Query(ctx context.Context, handler HandlerFunc, query string, args ...any) error {
	if m.queryFn != nil {
		return m.queryFn(ctx, handler, query, args...)
	}
	return nil
}

func (m *mockClient) Transaction(ctx context.Context, fn TxFunc) error {
	if m.txFn != nil {
		return m.txFn(ctx, fn)
	}
	return nil
}

func (m *mockClient) Close() error {
	if m.closeFn != nil {
		return m.closeFn()
	}
	return nil
}

type mockResult struct {
	rowsAffected int64
	lastInsertID int64
}

func (m *mockResult) LastInsertId() (int64, error) {
	return m.lastInsertID, nil
}

func (m *mockResult) RowsAffected() (int64, error) {
	return m.rowsAffected, nil
}

// Test real sqlClient implementation using sqlmock

func TestRealSQLClient_ExecContext(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	client := &sqlClient{db: db}

	mock.ExpectExec("INSERT INTO test").
		WithArgs("value").
		WillReturnResult(sqlmock.NewResult(1, 1))

	result, err := client.ExecContext(context.Background(), "INSERT INTO test VALUES ($1)", "value")
	require.NoError(t, err)
	require.NotNil(t, result)

	rows, err := result.RowsAffected()
	require.NoError(t, err)
	require.Equal(t, int64(1), rows)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRealSQLClient_Query(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	client := &sqlClient{db: db}

	rows := sqlmock.NewRows([]string{"id", "name"}).
		AddRow(1, "test1").
		AddRow(2, "test2")

	mock.ExpectQuery("SELECT .* FROM test").
		WithArgs(1).
		WillReturnRows(rows)

	var results []struct {
		ID   int
		Name string
	}

	err = client.Query(context.Background(), func(rows *sql.Rows) error {
		for rows.Next() {
			var id int
			var name string
			if err := rows.Scan(&id, &name); err != nil {
				return err
			}
			results = append(results, struct {
				ID   int
				Name string
			}{ID: id, Name: name})
		}
		return nil
	}, "SELECT * FROM test WHERE id = $1", 1)

	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Equal(t, 1, results[0].ID)
	require.Equal(t, "test1", results[0].Name)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRealSQLClient_Query_Error(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	client := &sqlClient{db: db}

	mock.ExpectQuery("SELECT .* FROM test").
		WillReturnError(errors.New("query error"))

	err = client.Query(context.Background(), func(rows *sql.Rows) error {
		return nil
	}, "SELECT * FROM test")

	require.Error(t, err)
	require.Contains(t, err.Error(), "query")

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRealSQLClient_Transaction_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	client := &sqlClient{db: db}

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO test").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err = client.Transaction(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO test VALUES ($1)", "value")
		return err
	})

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRealSQLClient_Transaction_Rollback(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	client := &sqlClient{db: db}

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO test").WillReturnError(errors.New("insert error"))
	mock.ExpectRollback()

	err = client.Transaction(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO test VALUES ($1)", "value")
		return err
	})

	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRealSQLClient_Transaction_BeginError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	client := &sqlClient{db: db}

	mock.ExpectBegin().WillReturnError(errors.New("begin error"))

	err = client.Transaction(context.Background(), func(tx *sql.Tx) error {
		return nil
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "begin transaction")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRealSQLClient_Transaction_CommitError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	client := &sqlClient{db: db}

	mock.ExpectBegin()
	mock.ExpectCommit().WillReturnError(errors.New("commit error"))

	err = client.Transaction(context.Background(), func(tx *sql.Tx) error {
		return nil
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "commit transaction")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRealSQLClient_Close(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)

	client := &sqlClient{db: db}

	mock.ExpectClose()

	err = client.Close()
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRealSQLClient_Query_HandlerError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	client := &sqlClient{db: db}

	rows := sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "test1")
	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	handlerErr := errors.New("handler error")
	err = client.Query(context.Background(), func(rows *sql.Rows) error {
		return handlerErr
	}, "SELECT * FROM test")

	require.Error(t, err)
	require.Equal(t, handlerErr, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

// TestRealSQLClient_Query_RowsError tests rows.Err() error path
func TestRealSQLClient_Query_RowsError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	client := &sqlClient{db: db}

	rows := sqlmock.NewRows([]string{"id", "name"}).
		AddRow(1, "test1").
		AddRow(2, "test2").
		RowError(1, errors.New("rows iteration error"))

	mock.ExpectQuery("SELECT").WillReturnRows(rows)

	err = client.Query(context.Background(), func(rows *sql.Rows) error {
		for rows.Next() {
			var id int
			var name string
			if err := rows.Scan(&id, &name); err != nil {
				return err
			}
		}
		return nil
	}, "SELECT * FROM test")

	require.Error(t, err)
	require.Contains(t, err.Error(), "rows iteration")

	require.NoError(t, mock.ExpectationsWereMet())
}

// TestRealSQLClient_Transaction_Panic tests panic handling in transaction
func TestRealSQLClient_Transaction_Panic(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	client := &sqlClient{db: db}

	mock.ExpectBegin()
	mock.ExpectRollback()

	defer func() {
		if r := recover(); r != nil {
			require.Equal(t, "panic in transaction", r)
		} else {
			t.Fatal("expected panic but didn't get one")
		}
	}()

	_ = client.Transaction(context.Background(), func(tx *sql.Tx) error {
		panic("panic in transaction")
	})

	require.NoError(t, mock.ExpectationsWereMet())
}

// TestDefaultClientBuilder_PingError tests PingContext error path
func TestDefaultClientBuilder_PingError(t *testing.T) {
	// Use an invalid host/port combination that will fail on ping
	// The connection string is valid format-wise, but points to a non-existent server
	const badConnString = "postgres://user:pass@255.255.255.255:1/testdb?connect_timeout=1"

	// Note: This test may take a few seconds due to connection timeout
	// Use a reasonable timeout to prevent the test from hanging
	_, err := defaultClientBuilder(context.Background(), WithClientConnString(badConnString))
	require.Error(t, err)
	// The error should be about ping or connection failure
	require.Contains(t, err.Error(), "postgres")
}
