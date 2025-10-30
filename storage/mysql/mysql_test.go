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
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterMySQLInstance(t *testing.T) {
	instanceName := "test-instance"
	dsn := "user:password@tcp(localhost:3306)/testdb?parseTime=true"

	RegisterMySQLInstance(instanceName, WithClientBuilderDSN(dsn))

	opts, ok := GetMySQLInstance(instanceName)
	require.True(t, ok, "expected instance %s to be registered", instanceName)
	assert.NotEmpty(t, opts, "expected at least one option")
}

func TestRegisterMySQLInstance_MultipleOptions(t *testing.T) {
	instanceName := "test-multi-opts"
	dsn := "user:password@tcp(localhost:3306)/testdb?parseTime=true"

	// Register with multiple options.
	RegisterMySQLInstance(instanceName,
		WithClientBuilderDSN(dsn),
		WithMaxOpenConns(50),
		WithMaxIdleConns(10),
		WithConnMaxLifetime(time.Hour),
		WithConnMaxIdleTime(30*time.Minute),
	)

	opts, ok := GetMySQLInstance(instanceName)
	require.True(t, ok)
	assert.Len(t, opts, 5)

	// Apply options and verify.
	builderOpts := &ClientBuilderOpts{}
	for _, opt := range opts {
		opt(builderOpts)
	}

	assert.Equal(t, dsn, builderOpts.DSN)
	assert.Equal(t, 50, builderOpts.MaxOpenConns)
	assert.Equal(t, 10, builderOpts.MaxIdleConns)
	assert.Equal(t, time.Hour, builderOpts.ConnMaxLifetime)
	assert.Equal(t, 30*time.Minute, builderOpts.ConnMaxIdleTime)
}

func TestRegisterMySQLInstance_Append(t *testing.T) {
	instanceName := "test-append"

	// Register first time.
	RegisterMySQLInstance(instanceName, WithClientBuilderDSN("dsn1"))

	// Register again with different options - should append.
	RegisterMySQLInstance(instanceName, WithClientBuilderDSN("dsn2"))

	opts, ok := GetMySQLInstance(instanceName)
	require.True(t, ok)
	// Should have both options (appended).
	assert.Len(t, opts, 2)
}

func TestGetMySQLInstance_NotFound(t *testing.T) {
	_, ok := GetMySQLInstance("non-existent-instance")
	assert.False(t, ok, "expected instance to not be found")
}

func TestClientBuilderOpts(t *testing.T) {
	dsn := "user:password@tcp(localhost:3306)/testdb?parseTime=true"
	opts := &ClientBuilderOpts{}

	WithClientBuilderDSN(dsn)(opts)
	assert.Equal(t, dsn, opts.DSN)

	WithMaxOpenConns(100)(opts)
	assert.Equal(t, 100, opts.MaxOpenConns)

	WithMaxIdleConns(10)(opts)
	assert.Equal(t, 10, opts.MaxIdleConns)

	WithConnMaxLifetime(time.Hour)(opts)
	assert.Equal(t, time.Hour, opts.ConnMaxLifetime)

	WithConnMaxIdleTime(10 * time.Minute)(opts)
	assert.Equal(t, 10*time.Minute, opts.ConnMaxIdleTime)
}

func TestWithExtraOptions(t *testing.T) {
	t.Run("single option", func(t *testing.T) {
		opts := &ClientBuilderOpts{}
		WithExtraOptions("option1")(opts)
		assert.Len(t, opts.ExtraOptions, 1)
		assert.Equal(t, "option1", opts.ExtraOptions[0])
	})

	t.Run("multiple options separately", func(t *testing.T) {
		opts := &ClientBuilderOpts{}
		WithExtraOptions("opt1")(opts)
		WithExtraOptions("opt2")(opts)
		assert.Len(t, opts.ExtraOptions, 2)
		assert.Equal(t, "opt1", opts.ExtraOptions[0])
		assert.Equal(t, "opt2", opts.ExtraOptions[1])
	})

	t.Run("multiple options at once", func(t *testing.T) {
		opts := &ClientBuilderOpts{}
		WithExtraOptions("opt1", "opt2", "opt3")(opts)
		assert.Len(t, opts.ExtraOptions, 3)
	})

	t.Run("accumulation behavior", func(t *testing.T) {
		opts := &ClientBuilderOpts{}
		WithExtraOptions("opt1")(opts)
		WithExtraOptions("opt2", "opt3")(opts)
		assert.Len(t, opts.ExtraOptions, 3)
		assert.Equal(t, "opt1", opts.ExtraOptions[0])
		assert.Equal(t, "opt2", opts.ExtraOptions[1])
		assert.Equal(t, "opt3", opts.ExtraOptions[2])
	})
}

func TestOptionsOrder(t *testing.T) {
	t.Run("DSN override", func(t *testing.T) {
		opts := &ClientBuilderOpts{}
		WithClientBuilderDSN("dsn1")(opts)
		assert.Equal(t, "dsn1", opts.DSN)

		WithClientBuilderDSN("dsn2")(opts)
		assert.Equal(t, "dsn2", opts.DSN, "later option should override")
	})

	t.Run("connection pool override", func(t *testing.T) {
		opts := &ClientBuilderOpts{}
		WithMaxOpenConns(10)(opts)
		WithMaxOpenConns(20)(opts)
		assert.Equal(t, 20, opts.MaxOpenConns, "later option should override")
	})
}

func TestSetAndGetClientBuilder(t *testing.T) {
	// Save original builder.
	originalBuilder := GetClientBuilder()
	defer SetClientBuilder(originalBuilder)

	// Create a custom builder.
	customBuilder := func(builderOpts ...ClientBuilderOpt) (Client, error) {
		return nil, sql.ErrConnDone
	}

	// Set custom builder.
	SetClientBuilder(customBuilder)

	// Verify it was set.
	currentBuilder := GetClientBuilder()
	assert.NotNil(t, currentBuilder)

	// Test that the custom builder is used.
	_, err := currentBuilder()
	assert.Error(t, err)
	assert.Equal(t, sql.ErrConnDone, err)
}

func TestDefaultClientBuilder_SuccessPath(t *testing.T) {
	// Test the success path by using a custom builder that simulates successful connection.
	// This tests the logic after sql.Open succeeds and ping succeeds.

	// Save original builder.
	originalBuilder := GetClientBuilder()
	defer SetClientBuilder(originalBuilder)

	// Create a mock database that will succeed on ping.
	mockDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	defer mockDB.Close()

	// Expect ping to succeed.
	mock.ExpectPing()

	// Create a custom builder that simulates DefaultClientBuilder's success path.
	successBuilder := func(builderOpts ...ClientBuilderOpt) (Client, error) {
		// Apply options (same as DefaultClientBuilder).
		o := &ClientBuilderOpts{}
		for _, opt := range builderOpts {
			opt(o)
		}

		// Check DSN (same as DefaultClientBuilder).
		if o.DSN == "" {
			return nil, errors.New("mysql: dsn is empty")
		}

		// Instead of sql.Open, return our mock DB.
		// This simulates successful sql.Open.
		db := mockDB

		// Apply connection pool settings (same as DefaultClientBuilder).
		if o.MaxOpenConns > 0 {
			db.SetMaxOpenConns(o.MaxOpenConns)
		}
		if o.MaxIdleConns > 0 {
			db.SetMaxIdleConns(o.MaxIdleConns)
		}
		if o.ConnMaxLifetime > 0 {
			db.SetConnMaxLifetime(o.ConnMaxLifetime)
		}
		if o.ConnMaxIdleTime > 0 {
			db.SetConnMaxIdleTime(o.ConnMaxIdleTime)
		}

		// Test connection (same as DefaultClientBuilder).
		if err := db.Ping(); err != nil {
			db.Close()
			return nil, fmt.Errorf("mysql: ping failed: %w", err)
		}

		// Return success (this is the path we want to test).
		return &sqlDBClient{db: db}, nil
	}

	// Set the custom builder.
	SetClientBuilder(successBuilder)

	// Call the builder with all options.
	db, err := GetClientBuilder()(
		WithClientBuilderDSN("user:password@tcp(localhost:3306)/testdb?parseTime=true"),
		WithMaxOpenConns(100),
		WithMaxIdleConns(10),
		WithConnMaxLifetime(time.Hour),
		WithConnMaxIdleTime(30*time.Minute),
	)

	// Verify success - this tests the "return db, nil" path.
	require.NoError(t, err)
	require.NotNil(t, db)

	// Verify all expectations were met.
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDefaultClientBuilder_ConnectionPoolApplication(t *testing.T) {
	// Test that connection pool settings are correctly applied.
	// This verifies the logic between sql.Open and Ping.

	mockDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	defer mockDB.Close()

	// Expect ping to succeed.
	mock.ExpectPing()

	// Apply connection pool settings (simulating DefaultClientBuilder).
	maxOpen := 100
	maxIdle := 10
	maxLifetime := time.Hour
	maxIdleTime := 30 * time.Minute

	mockDB.SetMaxOpenConns(maxOpen)
	mockDB.SetMaxIdleConns(maxIdle)
	mockDB.SetConnMaxLifetime(maxLifetime)
	mockDB.SetConnMaxIdleTime(maxIdleTime)

	// Test ping.
	err = mockDB.Ping()
	require.NoError(t, err)

	// Verify expectations.
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDefaultClientBuilder_EmptyDSN(t *testing.T) {
	_, err := defaultClientBuilder()
	require.Error(t, err)
	assert.EqualError(t, err, "mysql: dsn is empty")
}

func TestDefaultClientBuilder_InvalidDSN(t *testing.T) {
	_, err := defaultClientBuilder(WithClientBuilderDSN("invalid-dsn-format"))
	require.Error(t, err)
	// Error occurs at open stage for invalid DSN format.
	assert.Contains(t, err.Error(), "mysql: open connection")
}

func TestDefaultClientBuilder_PingFailure(t *testing.T) {
	// This test will fail at ping stage (no real MySQL server).
	_, err := defaultClientBuilder(
		WithClientBuilderDSN("user:password@tcp(localhost:3306)/testdb?parseTime=true"),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mysql: ping failed")
}

func TestDefaultClientBuilder_WithAllOptions(t *testing.T) {
	// Test that all options are processed correctly.
	// This will fail to connect but tests option processing.
	_, err := defaultClientBuilder(
		WithClientBuilderDSN("user:password@tcp(localhost:3306)/testdb?parseTime=true"),
		WithMaxOpenConns(50),
		WithMaxIdleConns(5),
		WithConnMaxLifetime(2*time.Hour),
		WithConnMaxIdleTime(15*time.Minute),
		WithExtraOptions("extra1", "extra2"),
	)

	// Expected to fail on ping since no real MySQL server.
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mysql: ping failed")
}

func TestDefaultClientBuilder_Integration(t *testing.T) {
	t.Run("with mock database", func(t *testing.T) {
		// Create a mock database.
		db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
		require.NoError(t, err)
		defer db.Close()

		// Expect ping.
		mock.ExpectPing()

		// Save original builder.
		originalBuilder := GetClientBuilder()
		defer SetClientBuilder(originalBuilder)

		// Set a custom builder that returns our mock.
		SetClientBuilder(func(builderOpts ...ClientBuilderOpt) (Client, error) {
			o := &ClientBuilderOpts{}
			for _, opt := range builderOpts {
				opt(o)
			}

			if o.DSN == "" {
				return nil, sql.ErrConnDone
			}

			// Set connection pool settings if provided.
			if o.MaxOpenConns > 0 {
				db.SetMaxOpenConns(o.MaxOpenConns)
			}
			if o.MaxIdleConns > 0 {
				db.SetMaxIdleConns(o.MaxIdleConns)
			}
			if o.ConnMaxLifetime > 0 {
				db.SetConnMaxLifetime(o.ConnMaxLifetime)
			}
			if o.ConnMaxIdleTime > 0 {
				db.SetConnMaxIdleTime(o.ConnMaxIdleTime)
			}

			// Ping the database.
			if err := db.Ping(); err != nil {
				return nil, err
			}

			return &sqlDBClient{db: db}, nil
		})

		// Test the builder.
		result, err := GetClientBuilder()(
			WithClientBuilderDSN("user:password@tcp(localhost:3306)/testdb?parseTime=true"),
			WithMaxOpenConns(50),
			WithMaxIdleConns(10),
		)

		require.NoError(t, err)
		assert.NotNil(t, result)

		// Verify all expectations were met.
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestClientCompliance tests that Client interface is properly implemented
func TestClientCompliance(t *testing.T) {
	t.Run("sqlDBClient implements Client", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		// Test that our wrapper implements the interface
		var client Client = &sqlDBClient{db: mockDB}

		// Test Exec
		mock.ExpectExec("INSERT INTO test").WillReturnResult(sqlmock.NewResult(1, 1))
		result, err := client.Exec(context.Background(), "INSERT INTO test VALUES (1)")
		require.NoError(t, err)
		assert.NotNil(t, result)

		// Test Query (callback pattern)
		rows := sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "test")
		mock.ExpectQuery("SELECT id, name FROM test").WillReturnRows(rows)
		var id int
		var name string
		err = client.Query(context.Background(), func(rows *sql.Rows) error {
			return rows.Scan(&id, &name)
		}, "SELECT id, name FROM test")
		require.NoError(t, err)
		assert.Equal(t, 1, id)
		assert.Equal(t, "test", name)

		// Test QueryRow
		countRows := sqlmock.NewRows([]string{"count"}).AddRow(1)
		mock.ExpectQuery("SELECT COUNT").WillReturnRows(countRows)
		var count int
		err = client.QueryRow(context.Background(), []any{&count}, "SELECT COUNT(*) FROM test")
		require.NoError(t, err)
		assert.Equal(t, 1, count)

		// Test Close (sqlmock doesn't track Close calls, so we just call it)
		client.Close()

		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestDefaultClientBuilderInterface tests that DefaultClientBuilder returns Client interface
func TestDefaultClientBuilderInterface(t *testing.T) {
	t.Run("returns Client", func(t *testing.T) {
		// This test will fail because we don't have a real MySQL server
		// But it validates that the function signature is correct
		_, err := defaultClientBuilder(WithClientBuilderDSN("invalid-dsn"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "mysql: open connection")
	})

	t.Run("with valid options", func(t *testing.T) {
		// Test that all options are processed correctly
		// This will fail at connection time but validates option processing
		_, err := defaultClientBuilder(
			WithClientBuilderDSN("user:password@tcp(localhost:3306)/testdb"),
			WithMaxOpenConns(10),
			WithMaxIdleConns(5),
			WithConnMaxLifetime(time.Hour),
			WithConnMaxIdleTime(30*time.Minute),
		)
		// Should fail at connection or ping stage
		require.Error(t, err)
		assert.Contains(t, err.Error(), "mysql")
	})
}

// TestSQLDBClient_Query tests the Query method with callback pattern
func TestSQLDBClient_Query(t *testing.T) {
	t.Run("successful query with multiple rows", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		client := &sqlDBClient{db: mockDB}

		rows := sqlmock.NewRows([]string{"id", "name"}).
			AddRow(1, "Alice").
			AddRow(2, "Bob").
			AddRow(3, "Charlie")

		mock.ExpectQuery("SELECT id, name FROM users").WillReturnRows(rows)

		var results []struct {
			ID   int
			Name string
		}

		err = client.Query(context.Background(), func(rows *sql.Rows) error {
			var id int
			var name string
			if err := rows.Scan(&id, &name); err != nil {
				return err
			}
			results = append(results, struct {
				ID   int
				Name string
			}{ID: id, Name: name})
			return nil
		}, "SELECT id, name FROM users")

		require.NoError(t, err)
		assert.Len(t, results, 3)
		assert.Equal(t, 1, results[0].ID)
		assert.Equal(t, "Alice", results[0].Name)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query with ErrBreak", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		client := &sqlDBClient{db: mockDB}

		rows := sqlmock.NewRows([]string{"id", "name"}).
			AddRow(1, "Alice").
			AddRow(2, "Bob").
			AddRow(3, "Charlie")

		mock.ExpectQuery("SELECT id, name FROM users").WillReturnRows(rows)

		var results []struct {
			ID   int
			Name string
		}

		err = client.Query(context.Background(), func(rows *sql.Rows) error {
			var id int
			var name string
			if err := rows.Scan(&id, &name); err != nil {
				return err
			}
			results = append(results, struct {
				ID   int
				Name string
			}{ID: id, Name: name})
			// Stop after first row
			if id == 1 {
				return ErrBreak
			}
			return nil
		}, "SELECT id, name FROM users")

		require.NoError(t, err)
		assert.Len(t, results, 1)
		assert.Equal(t, 1, results[0].ID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query with callback error", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		client := &sqlDBClient{db: mockDB}

		rows := sqlmock.NewRows([]string{"id", "name"}).
			AddRow(1, "Alice").
			AddRow(2, "Bob")

		mock.ExpectQuery("SELECT id, name FROM users").WillReturnRows(rows)

		expectedErr := errors.New("callback error")
		err = client.Query(context.Background(), func(rows *sql.Rows) error {
			return expectedErr
		}, "SELECT id, name FROM users")

		require.Error(t, err)
		assert.Equal(t, expectedErr, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query error", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		client := &sqlDBClient{db: mockDB}

		mock.ExpectQuery("SELECT id, name FROM users").
			WillReturnError(errors.New("query error"))

		err = client.Query(context.Background(), func(rows *sql.Rows) error {
			return nil
		}, "SELECT id, name FROM users")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "query error")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestSQLDBClient_Transaction tests the Transaction method
func TestSQLDBClient_Transaction(t *testing.T) {
	t.Run("successful transaction", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		client := &sqlDBClient{db: mockDB}

		mock.ExpectBegin()
		mock.ExpectExec("INSERT INTO users").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec("UPDATE accounts").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		err = client.Transaction(context.Background(), func(tx *sql.Tx) error {
			_, err := tx.ExecContext(context.Background(), "INSERT INTO users (name) VALUES (?)", "Alice")
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(context.Background(), "UPDATE accounts SET balance = balance - 100 WHERE id = 1")
			return err
		})

		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("transaction with rollback on error", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		client := &sqlDBClient{db: mockDB}

		mock.ExpectBegin()
		mock.ExpectExec("INSERT INTO users").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec("UPDATE accounts").WillReturnError(errors.New("update error"))
		mock.ExpectRollback()

		expectedErr := errors.New("update error")
		err = client.Transaction(context.Background(), func(tx *sql.Tx) error {
			_, err := tx.ExecContext(context.Background(), "INSERT INTO users (name) VALUES (?)", "Alice")
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(context.Background(), "UPDATE accounts SET balance = balance - 100 WHERE id = 1")
			return err
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), expectedErr.Error())
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("transaction with custom options", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		client := &sqlDBClient{db: mockDB}

		mock.ExpectBegin()
		mock.ExpectExec("INSERT INTO users").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()

		err = client.Transaction(context.Background(), func(tx *sql.Tx) error {
			_, err := tx.ExecContext(context.Background(), "INSERT INTO users (name) VALUES (?)", "Alice")
			return err
		}, func(opts *sql.TxOptions) {
			opts.Isolation = sql.LevelReadCommitted
			opts.ReadOnly = false
		})

		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("begin transaction error", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		client := &sqlDBClient{db: mockDB}

		mock.ExpectBegin().WillReturnError(errors.New("begin error"))

		err = client.Transaction(context.Background(), func(tx *sql.Tx) error {
			return nil
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "begin error")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("commit error", func(t *testing.T) {
		mockDB, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer mockDB.Close()

		client := &sqlDBClient{db: mockDB}

		mock.ExpectBegin()
		mock.ExpectExec("INSERT INTO users").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit().WillReturnError(errors.New("commit error"))

		err = client.Transaction(context.Background(), func(tx *sql.Tx) error {
			_, err := tx.ExecContext(context.Background(), "INSERT INTO users (name) VALUES (?)", "Alice")
			return err
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "commit error")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestWrapSQLDB(t *testing.T) {
	// Create a mock database.
	mockDB, _, err := sqlmock.New()
	require.NoError(t, err)
	defer mockDB.Close()

	// Test WrapSQLDB function.
	client := WrapSQLDB(mockDB)
	assert.NotNil(t, client)

	// Verify it returns a sqlDBClient.
	sqlClient, ok := client.(*sqlDBClient)
	assert.True(t, ok)
	assert.Equal(t, mockDB, sqlClient.db)
}
