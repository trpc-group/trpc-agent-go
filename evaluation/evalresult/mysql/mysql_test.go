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
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/internal/mysqldb"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
)

func newEvalResultManager(t *testing.T) (*manager, *sql.DB, sqlmock.Sqlmock) {
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

	mock.ExpectExec("CREATE TABLE IF NOT EXISTS\\s+" + regexp.QuoteMeta("test_evaluation_eval_set_results")).
		WillReturnError(errors.New("boom"))
	mock.ExpectClose()

	_, err = New(WithMySQLClientDSN("dsn"), WithTablePrefix("test_"))
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
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

func TestClose_NilClient(t *testing.T) {
	m := &manager{}
	assert.NoError(t, m.Close())
}

func TestValidationErrors(t *testing.T) {
	ctx := context.Background()
	m := &manager{}

	_, err := m.Save(ctx, "", &evalresult.EvalSetResult{EvalSetID: "set"})
	assert.Error(t, err)

	_, err = m.Save(ctx, "app", nil)
	assert.Error(t, err)

	_, err = m.Save(ctx, "app", &evalresult.EvalSetResult{})
	assert.Error(t, err)

	_, err = m.Get(ctx, "", "rid")
	assert.Error(t, err)

	_, err = m.Get(ctx, "app", "")
	assert.Error(t, err)

	_, err = m.List(ctx, "")
	assert.Error(t, err)
}

func TestSave_GeneratesDefaultsAndStores(t *testing.T) {
	ctx := context.Background()
	m, db, mock := newEvalResultManager(t)
	t.Cleanup(func() { _ = db.Close() })

	pattern := fmt.Sprintf(`(?s)INSERT INTO %s.*ON DUPLICATE KEY UPDATE`, regexp.QuoteMeta(m.tables.EvalSetResults))
	mock.ExpectExec(pattern).
		WithArgs("app", sqlmock.AnyArg(), "set", sqlmock.AnyArg(), sqlmock.AnyArg(), nil).
		WillReturnResult(sqlmock.NewResult(1, 1))

	id, err := m.Save(ctx, "app", &evalresult.EvalSetResult{EvalSetID: "set"})
	assert.NoError(t, err)
	assert.True(t, strings.HasPrefix(id, "app_set_"))

	mock.ExpectExec(pattern).
		WithArgs("app", "rid", "set", "rname", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	id, err = m.Save(ctx, "app", &evalresult.EvalSetResult{
		EvalSetResultID:   "rid",
		EvalSetResultName: "rname",
		EvalSetID:         "set",
		EvalCaseResults:   []*evalresult.EvalCaseResult{},
		Summary:           &evalresult.EvalSetResultSummary{NumRuns: 1},
	})
	assert.NoError(t, err)
	assert.Equal(t, "rid", id)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGet_ParsesPayloadAndSummary(t *testing.T) {
	ctx := context.Background()
	m, db, mock := newEvalResultManager(t)
	t.Cleanup(func() { _ = db.Close() })

	payload, err := json.Marshal([]*evalresult.EvalCaseResult{{EvalSetID: "set", EvalID: "case"}})
	assert.NoError(t, err)

	createdAt := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	query := fmt.Sprintf(
		"SELECT eval_set_id, eval_set_result_name, eval_case_results, summary, created_at FROM %s WHERE app_name = ? AND eval_set_result_id = ?",
		m.tables.EvalSetResults,
	)
	rows := sqlmock.NewRows([]string{"eval_set_id", "eval_set_result_name", "eval_case_results", "summary", "created_at"}).
		AddRow("set", "name", payload, `{"numRuns":1}`, createdAt)

	mock.ExpectQuery(regexp.QuoteMeta(query)).
		WithArgs("app", "rid").
		WillReturnRows(rows)

	res, err := m.Get(ctx, "app", "rid")
	assert.NoError(t, err)
	assert.Equal(t, "set", res.EvalSetID)
	assert.Equal(t, "name", res.EvalSetResultName)
	assert.Len(t, res.EvalCaseResults, 1)
	assert.NotNil(t, res.Summary)
	assert.Equal(t, 1, res.Summary.NumRuns)
	assert.NotNil(t, res.CreationTimestamp)
	assert.Equal(t, createdAt, res.CreationTimestamp.Time)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGet_NullCaseResultsBecomesEmptySlice(t *testing.T) {
	ctx := context.Background()
	m, db, mock := newEvalResultManager(t)
	t.Cleanup(func() { _ = db.Close() })

	createdAt := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	query := fmt.Sprintf(
		"SELECT eval_set_id, eval_set_result_name, eval_case_results, summary, created_at FROM %s WHERE app_name = ? AND eval_set_result_id = ?",
		m.tables.EvalSetResults,
	)
	rows := sqlmock.NewRows([]string{"eval_set_id", "eval_set_result_name", "eval_case_results", "summary", "created_at"}).
		AddRow("set", "name", []byte("null"), nil, createdAt)

	mock.ExpectQuery(regexp.QuoteMeta(query)).
		WithArgs("app", "rid").
		WillReturnRows(rows)

	res, err := m.Get(ctx, "app", "rid")
	assert.NoError(t, err)
	assert.Equal(t, [](*evalresult.EvalCaseResult){}, res.EvalCaseResults)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGet_NotFound(t *testing.T) {
	ctx := context.Background()
	m, db, mock := newEvalResultManager(t)
	t.Cleanup(func() { _ = db.Close() })

	query := fmt.Sprintf(
		"SELECT eval_set_id, eval_set_result_name, eval_case_results, summary, created_at FROM %s WHERE app_name = ? AND eval_set_result_id = ?",
		m.tables.EvalSetResults,
	)
	mock.ExpectQuery(regexp.QuoteMeta(query)).
		WithArgs("app", "rid").
		WillReturnError(sql.ErrNoRows)

	_, err := m.Get(ctx, "app", "rid")
	assert.Error(t, err)
	assert.True(t, errors.Is(err, os.ErrNotExist))

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestList_ReturnsIDs(t *testing.T) {
	ctx := context.Background()
	m, db, mock := newEvalResultManager(t)
	t.Cleanup(func() { _ = db.Close() })

	query := fmt.Sprintf(
		"SELECT eval_set_result_id FROM %s WHERE app_name = ? ORDER BY created_at DESC",
		m.tables.EvalSetResults,
	)
	rows := sqlmock.NewRows([]string{"eval_set_result_id"}).
		AddRow("id-1").
		AddRow("id-2")
	mock.ExpectQuery(regexp.QuoteMeta(query)).
		WithArgs("app").
		WillReturnRows(rows)

	ids, err := m.List(ctx, "app")
	assert.NoError(t, err)
	assert.Equal(t, []string{"id-1", "id-2"}, ids)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestList_EmptyReturnsEmptySlice(t *testing.T) {
	ctx := context.Background()
	m, db, mock := newEvalResultManager(t)
	t.Cleanup(func() { _ = db.Close() })

	query := fmt.Sprintf(
		"SELECT eval_set_result_id FROM %s WHERE app_name = ? ORDER BY created_at DESC",
		m.tables.EvalSetResults,
	)
	rows := sqlmock.NewRows([]string{"eval_set_result_id"})
	mock.ExpectQuery(regexp.QuoteMeta(query)).
		WithArgs("app").
		WillReturnRows(rows)

	ids, err := m.List(ctx, "app")
	assert.NoError(t, err)
	assert.Equal(t, []string{}, ids)

	assert.NoError(t, mock.ExpectationsWereMet())
}
