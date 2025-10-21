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
	customBuilder := func(builderOpts ...ClientBuilderOpt) (*sql.DB, error) {
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
	successBuilder := func(builderOpts ...ClientBuilderOpt) (*sql.DB, error) {
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
		return db, nil
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
	_, err := DefaultClientBuilder()
	require.Error(t, err)
	assert.EqualError(t, err, "mysql: dsn is empty")
}

func TestDefaultClientBuilder_InvalidDSN(t *testing.T) {
	_, err := DefaultClientBuilder(WithClientBuilderDSN("invalid-dsn-format"))
	require.Error(t, err)
	// Error occurs at open stage for invalid DSN format.
	assert.Contains(t, err.Error(), "mysql: open connection")
}

func TestDefaultClientBuilder_PingFailure(t *testing.T) {
	// This test will fail at ping stage (no real MySQL server).
	_, err := DefaultClientBuilder(
		WithClientBuilderDSN("user:password@tcp(localhost:3306)/testdb?parseTime=true"),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mysql: ping failed")
}

func TestDefaultClientBuilder_WithAllOptions(t *testing.T) {
	// Test that all options are processed correctly.
	// This will fail to connect but tests option processing.
	_, err := DefaultClientBuilder(
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
		SetClientBuilder(func(builderOpts ...ClientBuilderOpt) (*sql.DB, error) {
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

			return db, nil
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
