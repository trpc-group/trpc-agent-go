//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	mysqlerr "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
)

func newMySQLStore(t *testing.T) (*mysqlStore, *sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	assert.NoError(t, err)
	store := &mysqlStore{
		db:        storage.WrapSQLDB(db),
		tableName: sqldb.BuildTableName("test_", tableNameRuns),
	}
	return store, db, mock
}

func TestNew_SkipDBInit(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	assert.NoError(t, err)
	oldBuilder := storage.GetClientBuilder()
	storage.SetClientBuilder(func(builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		options := &storage.ClientBuilderOpts{}
		for _, opt := range builderOpts {
			opt(options)
		}
		assert.Equal(t, "dsn", options.DSN)
		return storage.WrapSQLDB(db), nil
	})
	t.Cleanup(func() { storage.SetClientBuilder(oldBuilder) })
	store, err := New(
		WithMySQLClientDSN("dsn"),
		WithSkipDBInit(true),
		WithTablePrefix("test_"),
		WithInitTimeout(-1),
	)
	assert.NoError(t, err)
	mock.ExpectClose()
	assert.NoError(t, store.Close())
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestNew_BuildClientError(t *testing.T) {
	oldBuilder := storage.GetClientBuilder()
	storage.SetClientBuilder(func(builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return nil, errors.New("boom")
	})
	t.Cleanup(func() { storage.SetClientBuilder(oldBuilder) })
	_, err := New(WithMySQLClientDSN("dsn"), WithSkipDBInit(true))
	assert.Error(t, err)
}

func TestNew_DBInitFailureClosesClient(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	assert.NoError(t, err)
	oldBuilder := storage.GetClientBuilder()
	storage.SetClientBuilder(func(builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return storage.WrapSQLDB(db), nil
	})
	t.Cleanup(func() { storage.SetClientBuilder(oldBuilder) })
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS\\s+" + regexp.QuoteMeta("test_promptiter_runs")).
		WillReturnError(errors.New("boom"))
	mock.ExpectClose()
	_, err = New(WithMySQLClientDSN("dsn"), WithTablePrefix("test_"))
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestOptions(t *testing.T) {
	options := newOptions(
		WithMySQLClientDSN("dsn"),
		WithMySQLInstance("instance"),
		WithExtraOptions("x"),
		WithSkipDBInit(true),
		WithTablePrefix("test_"),
		WithTablePrefix(""),
		WithInitTimeout(time.Second),
		WithInitTimeout(-1),
	)
	assert.Equal(t, "dsn", options.dsn)
	assert.Equal(t, "instance", options.instanceName)
	assert.Equal(t, []any{"x"}, options.extraOptions)
	assert.True(t, options.skipDBInit)
	assert.Equal(t, "", options.tablePrefix)
	assert.Equal(t, time.Second, options.initTimeout)
}

func TestClose_NilClient(t *testing.T) {
	store := &mysqlStore{}
	assert.NoError(t, store.Close())
}

func TestCreateValidationErrors(t *testing.T) {
	ctx := context.Background()
	store := &mysqlStore{}
	err := store.Create(ctx, nil)
	assert.Error(t, err)
	err = store.Create(ctx, &engine.RunResult{})
	assert.Error(t, err)
}

func TestCreateStoresRun(t *testing.T) {
	ctx := context.Background()
	store, db, mock := newMySQLStore(t)
	t.Cleanup(func() { _ = db.Close() })
	run := &engine.RunResult{
		ID:     "run-1",
		Status: engine.RunStatusQueued,
	}
	query := fmt.Sprintf(
		"INSERT INTO %s \\(run_id, status, run_result\\) VALUES \\(\\?, \\?, \\?\\)",
		regexp.QuoteMeta(store.tableName),
	)
	mock.ExpectExec(query).
		WithArgs("run-1", string(engine.RunStatusQueued), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	err := store.Create(ctx, run)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateDuplicateEntryReturnsFriendlyError(t *testing.T) {
	ctx := context.Background()
	store, db, mock := newMySQLStore(t)
	t.Cleanup(func() { _ = db.Close() })
	run := &engine.RunResult{
		ID:     "run-1",
		Status: engine.RunStatusQueued,
	}
	query := fmt.Sprintf(
		"INSERT INTO %s \\(run_id, status, run_result\\) VALUES \\(\\?, \\?, \\?\\)",
		regexp.QuoteMeta(store.tableName),
	)
	mock.ExpectExec(query).
		WithArgs("run-1", string(engine.RunStatusQueued), sqlmock.AnyArg()).
		WillReturnError(&mysqlerr.MySQLError{Number: sqldb.MySQLErrDuplicateEntry})
	err := store.Create(ctx, run)
	assert.EqualError(t, err, `run "run-1" already exists`)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetValidationError(t *testing.T) {
	ctx := context.Background()
	store := &mysqlStore{}
	_, err := store.Get(ctx, "")
	assert.Error(t, err)
}

func TestGetLoadsRun(t *testing.T) {
	ctx := context.Background()
	store, db, mock := newMySQLStore(t)
	t.Cleanup(func() { _ = db.Close() })
	run := &engine.RunResult{
		ID:           "run-1",
		Status:       engine.RunStatusRunning,
		CurrentRound: 2,
		ErrorMessage: "warning",
	}
	payload, err := json.Marshal(run)
	assert.NoError(t, err)
	query := fmt.Sprintf(
		"SELECT run_result FROM %s WHERE run_id = \\?",
		regexp.QuoteMeta(store.tableName),
	)
	rows := sqlmock.NewRows([]string{"run_result"}).AddRow(payload)
	mock.ExpectQuery(query).WithArgs("run-1").WillReturnRows(rows)
	loaded, err := store.Get(ctx, "run-1")
	assert.NoError(t, err)
	assert.Equal(t, run.ID, loaded.ID)
	assert.Equal(t, run.Status, loaded.Status)
	assert.Equal(t, run.CurrentRound, loaded.CurrentRound)
	assert.Equal(t, run.ErrorMessage, loaded.ErrorMessage)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetNotFound(t *testing.T) {
	ctx := context.Background()
	store, db, mock := newMySQLStore(t)
	t.Cleanup(func() { _ = db.Close() })
	query := fmt.Sprintf(
		"SELECT run_result FROM %s WHERE run_id = \\?",
		regexp.QuoteMeta(store.tableName),
	)
	mock.ExpectQuery(query).WithArgs("run-1").WillReturnError(sql.ErrNoRows)
	_, err := store.Get(ctx, "run-1")
	assert.Error(t, err)
	assert.ErrorIs(t, err, os.ErrNotExist)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetUnmarshalError(t *testing.T) {
	ctx := context.Background()
	store, db, mock := newMySQLStore(t)
	t.Cleanup(func() { _ = db.Close() })
	query := fmt.Sprintf(
		"SELECT run_result FROM %s WHERE run_id = \\?",
		regexp.QuoteMeta(store.tableName),
	)
	rows := sqlmock.NewRows([]string{"run_result"}).AddRow([]byte("{"))
	mock.ExpectQuery(query).WithArgs("run-1").WillReturnRows(rows)
	_, err := store.Get(ctx, "run-1")
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateValidationErrors(t *testing.T) {
	ctx := context.Background()
	store := &mysqlStore{}
	err := store.Update(ctx, nil)
	assert.Error(t, err)
	err = store.Update(ctx, &engine.RunResult{})
	assert.Error(t, err)
}

func TestUpdatePersistsRun(t *testing.T) {
	ctx := context.Background()
	store, db, mock := newMySQLStore(t)
	t.Cleanup(func() { _ = db.Close() })
	run := &engine.RunResult{
		ID:     "run-1",
		Status: engine.RunStatusSucceeded,
	}
	query := fmt.Sprintf(
		"UPDATE %s SET status = \\?, run_result = \\?, updated_at = CURRENT_TIMESTAMP\\(6\\) WHERE run_id = \\?",
		regexp.QuoteMeta(store.tableName),
	)
	mock.ExpectExec(query).
		WithArgs(string(engine.RunStatusSucceeded), sqlmock.AnyArg(), "run-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	err := store.Update(ctx, run)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateMissingRunReturnsNotFound(t *testing.T) {
	ctx := context.Background()
	store, db, mock := newMySQLStore(t)
	t.Cleanup(func() { _ = db.Close() })
	run := &engine.RunResult{
		ID:     "run-1",
		Status: engine.RunStatusSucceeded,
	}
	query := fmt.Sprintf(
		"UPDATE %s SET status = \\?, run_result = \\?, updated_at = CURRENT_TIMESTAMP\\(6\\) WHERE run_id = \\?",
		regexp.QuoteMeta(store.tableName),
	)
	mock.ExpectExec(query).
		WithArgs(string(engine.RunStatusSucceeded), sqlmock.AnyArg(), "run-1").
		WillReturnResult(sqlmock.NewResult(0, 0))
	err := store.Update(ctx, run)
	assert.Error(t, err)
	assert.ErrorIs(t, err, os.ErrNotExist)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestEnsureSchemaIgnoresDuplicateIndexName(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	assert.NoError(t, err)
	client := storage.WrapSQLDB(db)
	t.Cleanup(func() { _ = db.Close() })
	tableName := sqldb.BuildTableName("test_", tableNameRuns)
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS\\s+" + regexp.QuoteMeta(tableName)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE UNIQUE INDEX\\s+" + regexp.QuoteMeta(runIDUniqueIndexName) + "\\s+ON\\s+" + regexp.QuoteMeta(tableName)).
		WillReturnError(&mysqlerr.MySQLError{Number: sqldb.MySQLErrDuplicateKeyName})
	err = ensureSchema(ctx, client, tableName)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
