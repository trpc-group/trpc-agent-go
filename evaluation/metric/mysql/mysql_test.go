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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/internal/mysqldb"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
)

func newMetricManager(t *testing.T) (*manager, *sql.DB, sqlmock.Sqlmock) {
	t.Helper()

	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	assert.NoError(t, err)

	m := &manager{
		db:     storage.WrapSQLDB(db),
		tables: mysqldb.BuildTables("test_"),
	}
	return m, db, mock
}

func TestNew_SkipDBInit(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	assert.NoError(t, err)

	oldBuilder := storage.GetClientBuilder()
	storage.SetClientBuilder(func(builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		o := &storage.ClientBuilderOpts{}
		for _, opt := range builderOpts {
			opt(o)
		}
		assert.Equal(t, "dsn", o.DSN)
		return storage.WrapSQLDB(db), nil
	})
	t.Cleanup(func() { storage.SetClientBuilder(oldBuilder) })

	m, err := New(
		WithMySQLClientDSN("dsn"),
		WithSkipDBInit(true),
		WithTablePrefix("test_"),
		WithInitTimeout(-1),
	)
	assert.NoError(t, err)
	mock.ExpectClose()
	assert.NoError(t, m.Close())
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

	mock.ExpectExec("CREATE TABLE IF NOT EXISTS\\s+" + regexp.QuoteMeta("test_evaluation_metrics")).
		WillReturnError(errors.New("boom"))
	mock.ExpectClose()

	_, err = New(WithMySQLClientDSN("dsn"), WithTablePrefix("test_"))
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestClose_NilClient(t *testing.T) {
	m := &manager{}
	assert.NoError(t, m.Close())
}

func TestOptions(t *testing.T) {
	opts := newOptions(
		WithMySQLClientDSN("dsn"),
		WithMySQLInstance("instance"),
		WithExtraOptions("x"),
		WithSkipDBInit(true),
		WithTablePrefix("test_"),
		WithTablePrefix(""),
		WithInitTimeout(time.Second),
		WithInitTimeout(-1),
	)
	assert.Equal(t, "dsn", opts.dsn)
	assert.Equal(t, "instance", opts.instanceName)
	assert.Equal(t, []any{"x"}, opts.extraOptions)
	assert.True(t, opts.skipDBInit)
	assert.Equal(t, "", opts.tablePrefix)
	assert.Equal(t, time.Second, opts.initTimeout)
}

func TestValidationErrors(t *testing.T) {
	ctx := context.Background()
	m := &manager{}

	_, err := m.List(ctx, "", "set")
	assert.Error(t, err)

	_, err = m.List(ctx, "app", "")
	assert.Error(t, err)

	_, err = m.Get(ctx, "", "set", "m1")
	assert.Error(t, err)

	_, err = m.Get(ctx, "app", "", "m1")
	assert.Error(t, err)

	_, err = m.Get(ctx, "app", "set", "")
	assert.Error(t, err)

	err = m.Add(ctx, "", "set", &metric.EvalMetric{MetricName: "m1"})
	assert.Error(t, err)

	err = m.Add(ctx, "app", "", &metric.EvalMetric{MetricName: "m1"})
	assert.Error(t, err)

	err = m.Add(ctx, "app", "set", nil)
	assert.Error(t, err)

	err = m.Add(ctx, "app", "set", &metric.EvalMetric{})
	assert.Error(t, err)

	err = m.Update(ctx, "", "set", &metric.EvalMetric{MetricName: "m1"})
	assert.Error(t, err)

	err = m.Update(ctx, "app", "", &metric.EvalMetric{MetricName: "m1"})
	assert.Error(t, err)

	err = m.Update(ctx, "app", "set", nil)
	assert.Error(t, err)

	err = m.Update(ctx, "app", "set", &metric.EvalMetric{})
	assert.Error(t, err)

	err = m.Delete(ctx, "", "set", "m1")
	assert.Error(t, err)

	err = m.Delete(ctx, "app", "", "m1")
	assert.Error(t, err)

	err = m.Delete(ctx, "app", "set", "")
	assert.Error(t, err)
}

func TestList_ReturnsMetricNames(t *testing.T) {
	ctx := context.Background()
	m, db, mock := newMetricManager(t)
	t.Cleanup(func() { _ = db.Close() })

	query := fmt.Sprintf(
		"SELECT metric_name FROM %s WHERE app_name = ? AND eval_set_id = ? ORDER BY metric_name ASC",
		m.tables.Metrics,
	)
	rows := sqlmock.NewRows([]string{"metric_name"}).
		AddRow("m1").
		AddRow("m2")
	mock.ExpectQuery(regexp.QuoteMeta(query)).
		WithArgs("app", "set").
		WillReturnRows(rows)

	names, err := m.List(ctx, "app", "set")
	assert.NoError(t, err)
	assert.Equal(t, []string{"m1", "m2"}, names)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestList_EmptyReturnsEmptySlice(t *testing.T) {
	ctx := context.Background()
	m, db, mock := newMetricManager(t)
	t.Cleanup(func() { _ = db.Close() })

	query := fmt.Sprintf(
		"SELECT metric_name FROM %s WHERE app_name = ? AND eval_set_id = ? ORDER BY metric_name ASC",
		m.tables.Metrics,
	)
	rows := sqlmock.NewRows([]string{"metric_name"})
	mock.ExpectQuery(regexp.QuoteMeta(query)).
		WithArgs("app", "set").
		WillReturnRows(rows)

	names, err := m.List(ctx, "app", "set")
	assert.NoError(t, err)
	assert.Equal(t, []string{}, names)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGet_Add_Update_Delete(t *testing.T) {
	ctx := context.Background()
	m, db, mock := newMetricManager(t)
	t.Cleanup(func() { _ = db.Close() })

	getSQL := fmt.Sprintf(
		"SELECT metric FROM %s WHERE app_name = ? AND eval_set_id = ? AND metric_name = ?",
		m.tables.Metrics,
	)
	payload, err := json.Marshal(&metric.EvalMetric{MetricName: "m1", Threshold: 0.5})
	assert.NoError(t, err)
	mock.ExpectQuery(regexp.QuoteMeta(getSQL)).
		WithArgs("app", "set", "m1").
		WillReturnRows(sqlmock.NewRows([]string{"metric"}).AddRow(payload))

	got, err := m.Get(ctx, "app", "set", "m1")
	assert.NoError(t, err)
	assert.Equal(t, "m1", got.MetricName)

	addSQL := fmt.Sprintf(
		"INSERT INTO %s (app_name, eval_set_id, metric_name, metric) VALUES (?, ?, ?, ?)",
		m.tables.Metrics,
	)
	mock.ExpectExec(regexp.QuoteMeta(addSQL)).
		WithArgs("app", "set", "m1", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = m.Add(ctx, "app", "set", &metric.EvalMetric{MetricName: "m1", Threshold: 0.5})
	assert.NoError(t, err)

	updateSQL := fmt.Sprintf(
		"UPDATE %s SET metric = ?, updated_at = CURRENT_TIMESTAMP(6) WHERE app_name = ? AND eval_set_id = ? AND metric_name = ?",
		m.tables.Metrics,
	)
	mock.ExpectExec(regexp.QuoteMeta(updateSQL)).
		WithArgs(sqlmock.AnyArg(), "app", "set", "m1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = m.Update(ctx, "app", "set", &metric.EvalMetric{MetricName: "m1", Threshold: 0.7})
	assert.NoError(t, err)

	deleteSQL := fmt.Sprintf(
		"DELETE FROM %s WHERE app_name = ? AND eval_set_id = ? AND metric_name = ?",
		m.tables.Metrics,
	)
	mock.ExpectExec(regexp.QuoteMeta(deleteSQL)).
		WithArgs("app", "set", "m1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = m.Delete(ctx, "app", "set", "m1")
	assert.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGet_NotFound(t *testing.T) {
	ctx := context.Background()
	m, db, mock := newMetricManager(t)
	t.Cleanup(func() { _ = db.Close() })

	getSQL := fmt.Sprintf(
		"SELECT metric FROM %s WHERE app_name = ? AND eval_set_id = ? AND metric_name = ?",
		m.tables.Metrics,
	)
	mock.ExpectQuery(regexp.QuoteMeta(getSQL)).
		WithArgs("app", "set", "missing").
		WillReturnError(sql.ErrNoRows)

	_, err := m.Get(ctx, "app", "set", "missing")
	assert.Error(t, err)
	assert.True(t, errors.Is(err, os.ErrNotExist))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateAndDelete_NotFound(t *testing.T) {
	ctx := context.Background()
	m, db, mock := newMetricManager(t)
	t.Cleanup(func() { _ = db.Close() })

	updateSQL := fmt.Sprintf(
		"UPDATE %s SET metric = ?, updated_at = CURRENT_TIMESTAMP(6) WHERE app_name = ? AND eval_set_id = ? AND metric_name = ?",
		m.tables.Metrics,
	)
	mock.ExpectExec(regexp.QuoteMeta(updateSQL)).
		WithArgs(sqlmock.AnyArg(), "app", "set", "missing").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := m.Update(ctx, "app", "set", &metric.EvalMetric{MetricName: "missing"})
	assert.Error(t, err)
	assert.True(t, errors.Is(err, os.ErrNotExist))

	deleteSQL := fmt.Sprintf(
		"DELETE FROM %s WHERE app_name = ? AND eval_set_id = ? AND metric_name = ?",
		m.tables.Metrics,
	)
	mock.ExpectExec(regexp.QuoteMeta(deleteSQL)).
		WithArgs("app", "set", "missing").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = m.Delete(ctx, "app", "set", "missing")
	assert.Error(t, err)
	assert.True(t, errors.Is(err, os.ErrNotExist))

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAdd_DuplicateEntry(t *testing.T) {
	ctx := context.Background()
	m, db, mock := newMetricManager(t)
	t.Cleanup(func() { _ = db.Close() })

	addSQL := fmt.Sprintf(
		"INSERT INTO %s (app_name, eval_set_id, metric_name, metric) VALUES (?, ?, ?, ?)",
		m.tables.Metrics,
	)
	mock.ExpectExec(regexp.QuoteMeta(addSQL)).
		WithArgs("app", "set", "m1", sqlmock.AnyArg()).
		WillReturnError(&mysql.MySQLError{Number: sqldb.MySQLErrDuplicateEntry, Message: "Duplicate entry"})

	err := m.Add(ctx, "app", "set", &metric.EvalMetric{MetricName: "m1"})
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
