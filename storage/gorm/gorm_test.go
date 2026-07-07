//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package gorm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	gormio "gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

type failingDialector struct{}

func (failingDialector) Name() string { return "failing" }

func (failingDialector) Initialize(*gormio.DB) error {
	return errors.New("dialector initialize failed")
}

func (failingDialector) Migrator(*gormio.DB) gormio.Migrator { return nil }

func (failingDialector) DataTypeOf(*schema.Field) string { return "" }

func (failingDialector) DefaultValueOf(*schema.Field) clause.Expression { return nil }

func (failingDialector) BindVarTo(clause.Writer, *gormio.Statement, any) {}

func (failingDialector) QuoteTo(clause.Writer, string) {}

func (failingDialector) Explain(string, ...any) string { return "" }

type poolOnlyDialector struct {
	pool gormio.ConnPool
}

func (d poolOnlyDialector) Name() string { return "pool-only" }

func (d poolOnlyDialector) Initialize(db *gormio.DB) error {
	db.ConnPool = d.pool
	return nil
}

func (d poolOnlyDialector) Migrator(*gormio.DB) gormio.Migrator { return nil }

func (d poolOnlyDialector) DataTypeOf(*schema.Field) string { return "" }

func (d poolOnlyDialector) DefaultValueOf(*schema.Field) clause.Expression { return nil }

func (d poolOnlyDialector) BindVarTo(clause.Writer, *gormio.Statement, any) {}

func (d poolOnlyDialector) QuoteTo(clause.Writer, string) {}

func (d poolOnlyDialector) Explain(string, ...any) string { return "" }

type badConnPool struct{}

func (badConnPool) PrepareContext(context.Context, string) (*sql.Stmt, error) {
	return nil, errors.New("not implemented")
}

func (badConnPool) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, errors.New("not implemented")
}

func (badConnPool) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errors.New("not implemented")
}

func (badConnPool) QueryRowContext(context.Context, string, ...any) *sql.Row {
	return &sql.Row{}
}

func TestRegisterGormInstance(t *testing.T) {
	instanceName := "test-instance"
	RegisterGormInstance(instanceName, WithDialector(sqlite.Open(":memory:")))

	opts, ok := GetGormInstance(instanceName)
	require.True(t, ok, "expected instance %s to be registered", instanceName)
	assert.NotEmpty(t, opts)
}

func TestRegisterGormInstance_Append(t *testing.T) {
	instanceName := "test-append"
	RegisterGormInstance(instanceName, WithDialector(sqlite.Open("file:one?mode=memory")))
	RegisterGormInstance(instanceName, WithInstanceName("one"))

	opts, ok := GetGormInstance(instanceName)
	require.True(t, ok)
	assert.Len(t, opts, 2)
}

func TestSetAndGetClientBuilder(t *testing.T) {
	originalBuilder := GetClientBuilder()
	defer SetClientBuilder(originalBuilder)

	customErr := errors.New("custom builder")
	SetClientBuilder(func(ctx context.Context, builderOpts ...ClientBuilderOpt) (Client, error) {
		return nil, customErr
	})

	_, err := GetClientBuilder()(context.Background())
	assert.ErrorIs(t, err, customErr)
}

func TestDefaultClientBuilder_WithDB(t *testing.T) {
	ctx := context.Background()
	db, err := gormio.Open(sqlite.Open(":memory:"), &gormio.Config{})
	require.NoError(t, err)

	client, err := GetClientBuilder()(ctx, WithDB(db))
	require.NoError(t, err)
	require.NotNil(t, client)
	assert.Same(t, db, client.DB())

	require.NoError(t, client.Close())

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.PingContext(ctx), "injected db must remain open after client close")
}

func TestDefaultClientBuilder_WithDialector(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "storage_gorm_test.db")

	client, err := GetClientBuilder()(ctx, WithDialector(sqlite.Open(path)))
	require.NoError(t, err)
	require.NotNil(t, client)
	require.NotNil(t, client.DB())

	require.NoError(t, client.Close())

	_, err = GetClientBuilder()(ctx, WithDialector(sqlite.Open(path)))
	require.NoError(t, err)
}

func TestDefaultClientBuilder_WithDialector_ExternalConnPool(t *testing.T) {
	ctx := context.Background()
	sqlDB, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	client, err := GetClientBuilder()(ctx,
		WithDialector(sqlite.Dialector{Conn: sqlDB}),
		WithOwnsConnection(false),
	)
	require.NoError(t, err)
	require.NotNil(t, client)

	require.NoError(t, client.Close())
	require.NoError(t, sqlDB.PingContext(ctx), "caller-owned pool must remain open after client close")
}

func TestDefaultClientBuilder_MissingDialector(t *testing.T) {
	_, err := GetClientBuilder()(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dialector is required")
}

func TestGetGormInstance_ReturnsCopy(t *testing.T) {
	instanceName := "test-copy"
	RegisterGormInstance(instanceName, WithDialector(sqlite.Open(":memory:")))

	opts, ok := GetGormInstance(instanceName)
	require.True(t, ok)
	require.Len(t, opts, 1)

	opts = append(opts, WithInstanceName("mutated"))

	stored, ok := GetGormInstance(instanceName)
	require.True(t, ok)
	assert.Len(t, stored, 1, "caller mutations must not affect registry state")
}

func TestRegisterGormInstance_Concurrent(t *testing.T) {
	const workers = 16
	done := make(chan struct{}, workers)
	for i := 0; i < workers; i++ {
		go func(n int) {
			name := fmt.Sprintf("concurrent-%d", n%4)
			RegisterGormInstance(name, WithInstanceName(name))
			_, _ = GetGormInstance(name)
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < workers; i++ {
		<-done
	}
}

func TestClientBuilderOpts_WithExtraOptions(t *testing.T) {
	opts := &ClientBuilderOpts{}
	WithExtraOptions("alpha", 42)(opts)
	assert.Equal(t, []any{"alpha", 42}, opts.ExtraOptions)
}

func TestClientBuilderOpts_WithConfig(t *testing.T) {
	cfg := &gormio.Config{SkipDefaultTransaction: true}
	opts := &ClientBuilderOpts{}
	WithConfig(cfg)(opts)
	assert.Same(t, cfg, opts.Config)
}

func TestClientBuilderOpts_WithInstanceName(t *testing.T) {
	opts := &ClientBuilderOpts{}
	WithInstanceName("primary")(opts)
	assert.Equal(t, "primary", opts.InstanceName)
}

func TestGetGormInstance_NotFound(t *testing.T) {
	opts, ok := GetGormInstance("missing-instance-name")
	assert.False(t, ok)
	assert.Nil(t, opts)
}

func TestDefaultClientBuilder_WithConfig(t *testing.T) {
	ctx := context.Background()
	cfg := &gormio.Config{SkipDefaultTransaction: true}

	client, err := GetClientBuilder()(ctx,
		WithDialector(sqlite.Open(":memory:")),
		WithConfig(cfg),
	)
	require.NoError(t, err)
	require.NoError(t, client.Close())
}

func TestDefaultClientBuilder_DialectorOpenFailure(t *testing.T) {
	_, err := GetClientBuilder()(context.Background(), WithDialector(failingDialector{}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "open connection")
}

func TestDefaultClientBuilder_OpenFailure_ClosedConn(t *testing.T) {
	ctx := context.Background()
	sqlDB, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	_, err = GetClientBuilder()(ctx, WithDialector(sqlite.Dialector{Conn: sqlDB}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "open connection")
}

func TestDefaultClientBuilder_PingFailure_OwnsConnection(t *testing.T) {
	ctx := context.Background()
	mockDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)

	mock.ExpectPing()
	mock.ExpectPing().WillReturnError(errors.New("ping failed"))

	_, err = GetClientBuilder()(ctx, WithDialector(poolOnlyDialector{pool: mockDB}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ping database")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDefaultClientBuilder_PingFailure_NoClose(t *testing.T) {
	ctx := context.Background()
	mockDB, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)

	mock.ExpectPing()
	mock.ExpectPing().WillReturnError(errors.New("ping failed"))

	_, err = GetClientBuilder()(ctx,
		WithDialector(poolOnlyDialector{pool: mockDB}),
		WithOwnsConnection(false),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ping database")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDefaultClientBuilder_GetDBFailure(t *testing.T) {
	_, err := GetClientBuilder()(context.Background(), WithDialector(poolOnlyDialector{pool: badConnPool{}}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get sql db")
}

func TestGormClient_Close_NilDB(t *testing.T) {
	require.NoError(t, (&gormClient{}).Close())
}

func TestGormClient_Close_SkipsWhenNotOwner(t *testing.T) {
	db, err := gormio.Open(sqlite.Open(":memory:"), &gormio.Config{})
	require.NoError(t, err)

	client := &gormClient{db: db, ownsConnection: false}
	require.NoError(t, client.Close())
}

func TestGormClient_Close_GetDBError(t *testing.T) {
	db, err := gormio.Open(poolOnlyDialector{pool: badConnPool{}}, &gormio.Config{})
	require.NoError(t, err)

	client := &gormClient{db: db, ownsConnection: true}
	err = client.Close()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get sql db")
}
