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
	"database/sql/driver"
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
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/internal/mysqldb"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
)

type evalCasePayloadMatcher struct {
	t *testing.T
}

func (m evalCasePayloadMatcher) Match(v driver.Value) bool {
	var payload []byte
	switch typed := v.(type) {
	case []byte:
		payload = typed
	case string:
		payload = []byte(typed)
	default:
		return false
	}
	var c evalset.EvalCase
	if err := json.Unmarshal(payload, &c); err != nil {
		return false
	}
	if c.EvalID != "case" {
		return false
	}
	if c.CreationTimestamp == nil {
		return false
	}
	if len(c.Conversation) != 1 || c.Conversation[0] == nil || c.Conversation[0].CreationTimestamp == nil {
		return false
	}
	if len(c.ActualConversation) != 1 || c.ActualConversation[0] == nil || c.ActualConversation[0].CreationTimestamp == nil {
		return false
	}
	return true
}

func newEvalSetManager(t *testing.T) (*manager, *sql.DB, sqlmock.Sqlmock) {
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

	mock.ExpectExec("CREATE TABLE IF NOT EXISTS\\s+" + regexp.QuoteMeta("test_evaluation_eval_sets")).
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

	_, err := m.Get(ctx, "", "set")
	assert.Error(t, err)

	_, err = m.Get(ctx, "app", "")
	assert.Error(t, err)

	_, err = m.Create(ctx, "", "set")
	assert.Error(t, err)

	_, err = m.Create(ctx, "app", "")
	assert.Error(t, err)

	_, err = m.List(ctx, "")
	assert.Error(t, err)

	err = m.Delete(ctx, "", "set")
	assert.Error(t, err)

	err = m.Delete(ctx, "app", "")
	assert.Error(t, err)

	_, err = m.GetCase(ctx, "", "set", "case")
	assert.Error(t, err)

	err = m.AddCase(ctx, "", "set", &evalset.EvalCase{EvalID: "case"})
	assert.Error(t, err)

	err = m.AddCase(ctx, "app", "", &evalset.EvalCase{EvalID: "case"})
	assert.Error(t, err)

	err = m.AddCase(ctx, "app", "set", nil)
	assert.Error(t, err)

	err = m.AddCase(ctx, "app", "set", &evalset.EvalCase{})
	assert.Error(t, err)

	err = m.UpdateCase(ctx, "", "set", &evalset.EvalCase{EvalID: "case"})
	assert.Error(t, err)

	err = m.UpdateCase(ctx, "app", "", &evalset.EvalCase{EvalID: "case"})
	assert.Error(t, err)

	err = m.UpdateCase(ctx, "app", "set", nil)
	assert.Error(t, err)

	err = m.UpdateCase(ctx, "app", "set", &evalset.EvalCase{})
	assert.Error(t, err)

	err = m.DeleteCase(ctx, "", "set", "case")
	assert.Error(t, err)

	err = m.DeleteCase(ctx, "app", "", "case")
	assert.Error(t, err)

	err = m.DeleteCase(ctx, "app", "set", "")
	assert.Error(t, err)
}

func TestCreateAndList(t *testing.T) {
	ctx := context.Background()
	m, db, mock := newEvalSetManager(t)
	t.Cleanup(func() { _ = db.Close() })

	createSQL := fmt.Sprintf(
		"INSERT INTO %s (app_name, eval_set_id, name, description) VALUES (?, ?, ?, ?)",
		m.tables.EvalSets,
	)
	mock.ExpectExec(regexp.QuoteMeta(createSQL)).
		WithArgs("app", "set", "set", "").
		WillReturnResult(sqlmock.NewResult(1, 1))

	created, err := m.Create(ctx, "app", "set")
	assert.NoError(t, err)
	assert.Equal(t, "set", created.EvalSetID)

	listSQL := fmt.Sprintf(
		"SELECT eval_set_id FROM %s WHERE app_name = ? ORDER BY eval_set_id ASC",
		m.tables.EvalSets,
	)
	listRows := sqlmock.NewRows([]string{"eval_set_id"}).
		AddRow("set")
	mock.ExpectQuery(regexp.QuoteMeta(listSQL)).
		WithArgs("app").
		WillReturnRows(listRows)

	ids, err := m.List(ctx, "app")
	assert.NoError(t, err)
	assert.Equal(t, []string{"set"}, ids)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreate_DuplicateEntry(t *testing.T) {
	ctx := context.Background()
	m, db, mock := newEvalSetManager(t)
	t.Cleanup(func() { _ = db.Close() })

	createSQL := fmt.Sprintf(
		"INSERT INTO %s (app_name, eval_set_id, name, description) VALUES (?, ?, ?, ?)",
		m.tables.EvalSets,
	)
	mock.ExpectExec(regexp.QuoteMeta(createSQL)).
		WithArgs("app", "set", "set", "").
		WillReturnError(&mysql.MySQLError{Number: sqldb.MySQLErrDuplicateEntry, Message: "Duplicate entry"})

	_, err := m.Create(ctx, "app", "set")
	assert.Error(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestList_EmptyReturnsEmptySlice(t *testing.T) {
	ctx := context.Background()
	m, db, mock := newEvalSetManager(t)
	t.Cleanup(func() { _ = db.Close() })

	listSQL := fmt.Sprintf(
		"SELECT eval_set_id FROM %s WHERE app_name = ? ORDER BY eval_set_id ASC",
		m.tables.EvalSets,
	)
	listRows := sqlmock.NewRows([]string{"eval_set_id"})
	mock.ExpectQuery(regexp.QuoteMeta(listSQL)).
		WithArgs("app").
		WillReturnRows(listRows)

	ids, err := m.List(ctx, "app")
	assert.NoError(t, err)
	assert.Equal(t, []string{}, ids)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGet_ReturnsEvalSetAndCases(t *testing.T) {
	ctx := context.Background()
	m, db, mock := newEvalSetManager(t)
	t.Cleanup(func() { _ = db.Close() })

	getSQL := fmt.Sprintf(
		"SELECT name, description, created_at FROM %s WHERE app_name = ? AND eval_set_id = ?",
		m.tables.EvalSets,
	)
	createdAt := time.Date(2025, 2, 3, 4, 5, 6, 0, time.UTC)
	setRows := sqlmock.NewRows([]string{"name", "description", "created_at"}).
		AddRow("set-name", "desc", createdAt)
	mock.ExpectQuery(regexp.QuoteMeta(getSQL)).
		WithArgs("app", "set").
		WillReturnRows(setRows)

	casePayload, err := json.Marshal(&evalset.EvalCase{EvalID: "case", EvalMode: evalset.EvalModeTrace})
	assert.NoError(t, err)
	casesSQL := fmt.Sprintf(
		"SELECT eval_case FROM %s WHERE app_name = ? AND eval_set_id = ? ORDER BY id ASC",
		m.tables.EvalCases,
	)
	caseRows := sqlmock.NewRows([]string{"eval_case"}).
		AddRow(casePayload)
	mock.ExpectQuery(regexp.QuoteMeta(casesSQL)).
		WithArgs("app", "set").
		WillReturnRows(caseRows)

	got, err := m.Get(ctx, "app", "set")
	assert.NoError(t, err)
	assert.Equal(t, "set", got.EvalSetID)
	assert.Equal(t, "set-name", got.Name)
	assert.Equal(t, "desc", got.Description)
	assert.Len(t, got.EvalCases, 1)
	assert.Equal(t, "case", got.EvalCases[0].EvalID)
	assert.NotNil(t, got.CreationTimestamp)
	assert.Equal(t, createdAt, got.CreationTimestamp.Time)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestEnsureEvalSetExists_NotFound(t *testing.T) {
	ctx := context.Background()
	m, db, mock := newEvalSetManager(t)
	t.Cleanup(func() { _ = db.Close() })

	existsSQL := fmt.Sprintf("SELECT 1 FROM %s WHERE app_name = ? AND eval_set_id = ?", m.tables.EvalSets)
	mock.ExpectQuery(regexp.QuoteMeta(existsSQL)).
		WithArgs("app", "set").
		WillReturnError(sql.ErrNoRows)

	err := m.ensureEvalSetExists(ctx, "app", "set")
	assert.Error(t, err)
	assert.True(t, errors.Is(err, os.ErrNotExist))

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDelete_SuccessCommits(t *testing.T) {
	ctx := context.Background()
	m, db, mock := newEvalSetManager(t)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(fmt.Sprintf("DELETE FROM %s WHERE app_name = ? AND eval_set_id = ?", m.tables.EvalCases))).
		WithArgs("app", "set").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(fmt.Sprintf("DELETE FROM %s WHERE app_name = ? AND eval_set_id = ?", m.tables.EvalSets))).
		WithArgs("app", "set").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := m.Delete(ctx, "app", "set")
	assert.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDelete_NotFoundRollsBack(t *testing.T) {
	ctx := context.Background()
	m, db, mock := newEvalSetManager(t)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(fmt.Sprintf("DELETE FROM %s WHERE app_name = ? AND eval_set_id = ?", m.tables.EvalCases))).
		WithArgs("app", "set").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(fmt.Sprintf("DELETE FROM %s WHERE app_name = ? AND eval_set_id = ?", m.tables.EvalSets))).
		WithArgs("app", "set").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	err := m.Delete(ctx, "app", "set")
	assert.Error(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCaseCRUD(t *testing.T) {
	ctx := context.Background()
	m, db, mock := newEvalSetManager(t)
	t.Cleanup(func() { _ = db.Close() })

	existsSQL := fmt.Sprintf("SELECT 1 FROM %s WHERE app_name = ? AND eval_set_id = ?", m.tables.EvalSets)
	mock.ExpectQuery(regexp.QuoteMeta(existsSQL)).
		WithArgs("app", "set").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))

	getCaseSQL := fmt.Sprintf(
		"SELECT eval_case FROM %s WHERE app_name = ? AND eval_set_id = ? AND eval_id = ?",
		m.tables.EvalCases,
	)
	casePayload, err := json.Marshal(&evalset.EvalCase{EvalID: "case"})
	assert.NoError(t, err)
	mock.ExpectQuery(regexp.QuoteMeta(getCaseSQL)).
		WithArgs("app", "set", "case").
		WillReturnRows(sqlmock.NewRows([]string{"eval_case"}).AddRow(casePayload))

	c, err := m.GetCase(ctx, "app", "set", "case")
	assert.NoError(t, err)
	assert.Equal(t, "case", c.EvalID)

	mock.ExpectQuery(regexp.QuoteMeta(existsSQL)).
		WithArgs("app", "set").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))

	addSQL := fmt.Sprintf(
		"INSERT INTO %s (app_name, eval_set_id, eval_id, eval_mode, eval_case) VALUES (?, ?, ?, ?, ?)",
		m.tables.EvalCases,
	)
	mock.ExpectExec(regexp.QuoteMeta(addSQL)).
		WithArgs("app", "set", "case", "", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = m.AddCase(ctx, "app", "set", &evalset.EvalCase{
		EvalID:          "case",
		Conversation:    []*evalset.Invocation{{InvocationID: "inv"}},
		EvalMode:        evalset.EvalModeDefault,
		SessionInput:    &evalset.SessionInput{AppName: "app", UserID: "user", State: map[string]any{"k": "v"}},
		ContextMessages: nil,
	})
	assert.NoError(t, err)

	mock.ExpectQuery(regexp.QuoteMeta(existsSQL)).
		WithArgs("app", "set").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))

	updateSQL := fmt.Sprintf(
		"UPDATE %s SET eval_mode = ?, eval_case = ?, updated_at = CURRENT_TIMESTAMP(6) WHERE app_name = ? AND eval_set_id = ? AND eval_id = ?",
		m.tables.EvalCases,
	)
	mock.ExpectExec(regexp.QuoteMeta(updateSQL)).
		WithArgs("", sqlmock.AnyArg(), "app", "set", "case").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = m.UpdateCase(ctx, "app", "set", &evalset.EvalCase{EvalID: "case"})
	assert.NoError(t, err)

	mock.ExpectQuery(regexp.QuoteMeta(existsSQL)).
		WithArgs("app", "set").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))

	deleteSQL := fmt.Sprintf(
		"DELETE FROM %s WHERE app_name = ? AND eval_set_id = ? AND eval_id = ?",
		m.tables.EvalCases,
	)
	mock.ExpectExec(regexp.QuoteMeta(deleteSQL)).
		WithArgs("app", "set", "case").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = m.DeleteCase(ctx, "app", "set", "case")
	assert.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetCase_NotFoundAndBadJSON(t *testing.T) {
	ctx := context.Background()
	m, db, mock := newEvalSetManager(t)
	t.Cleanup(func() { _ = db.Close() })

	existsSQL := fmt.Sprintf("SELECT 1 FROM %s WHERE app_name = ? AND eval_set_id = ?", m.tables.EvalSets)
	getCaseSQL := fmt.Sprintf(
		"SELECT eval_case FROM %s WHERE app_name = ? AND eval_set_id = ? AND eval_id = ?",
		m.tables.EvalCases,
	)

	mock.ExpectQuery(regexp.QuoteMeta(existsSQL)).
		WithArgs("app", "set").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	mock.ExpectQuery(regexp.QuoteMeta(getCaseSQL)).
		WithArgs("app", "set", "missing").
		WillReturnError(sql.ErrNoRows)

	_, err := m.GetCase(ctx, "app", "set", "missing")
	assert.Error(t, err)
	assert.True(t, errors.Is(err, os.ErrNotExist))

	mock.ExpectQuery(regexp.QuoteMeta(existsSQL)).
		WithArgs("app", "set").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	mock.ExpectQuery(regexp.QuoteMeta(getCaseSQL)).
		WithArgs("app", "set", "bad").
		WillReturnRows(sqlmock.NewRows([]string{"eval_case"}).AddRow([]byte("{not-json")))

	_, err = m.GetCase(ctx, "app", "set", "bad")
	assert.Error(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAddCase_SetsCreationTimestampForActualConversation(t *testing.T) {
	ctx := context.Background()
	m, db, mock := newEvalSetManager(t)
	t.Cleanup(func() { _ = db.Close() })

	existsSQL := fmt.Sprintf("SELECT 1 FROM %s WHERE app_name = ? AND eval_set_id = ?", m.tables.EvalSets)
	mock.ExpectQuery(regexp.QuoteMeta(existsSQL)).
		WithArgs("app", "set").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))

	addSQL := fmt.Sprintf(
		"INSERT INTO %s (app_name, eval_set_id, eval_id, eval_mode, eval_case) VALUES (?, ?, ?, ?, ?)",
		m.tables.EvalCases,
	)
	mock.ExpectExec(regexp.QuoteMeta(addSQL)).
		WithArgs("app", "set", "case", "", evalCasePayloadMatcher{t: t}).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := m.AddCase(ctx, "app", "set", &evalset.EvalCase{
		EvalID: "case",
		Conversation: []*evalset.Invocation{
			{InvocationID: "conv"},
		},
		ActualConversation: []*evalset.Invocation{
			{InvocationID: "actual"},
		},
	})
	assert.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAddCase_DuplicateEntry(t *testing.T) {
	ctx := context.Background()
	m, db, mock := newEvalSetManager(t)
	t.Cleanup(func() { _ = db.Close() })

	existsSQL := fmt.Sprintf("SELECT 1 FROM %s WHERE app_name = ? AND eval_set_id = ?", m.tables.EvalSets)
	mock.ExpectQuery(regexp.QuoteMeta(existsSQL)).
		WithArgs("app", "set").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))

	addSQL := fmt.Sprintf(
		"INSERT INTO %s (app_name, eval_set_id, eval_id, eval_mode, eval_case) VALUES (?, ?, ?, ?, ?)",
		m.tables.EvalCases,
	)
	mock.ExpectExec(regexp.QuoteMeta(addSQL)).
		WithArgs("app", "set", "case", "", sqlmock.AnyArg()).
		WillReturnError(&mysql.MySQLError{Number: sqldb.MySQLErrDuplicateEntry, Message: "Duplicate entry"})

	err := m.AddCase(ctx, "app", "set", &evalset.EvalCase{EvalID: "case"})
	assert.Error(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateAndDeleteCase_NotFound(t *testing.T) {
	ctx := context.Background()
	m, db, mock := newEvalSetManager(t)
	t.Cleanup(func() { _ = db.Close() })

	existsSQL := fmt.Sprintf("SELECT 1 FROM %s WHERE app_name = ? AND eval_set_id = ?", m.tables.EvalSets)

	mock.ExpectQuery(regexp.QuoteMeta(existsSQL)).
		WithArgs("app", "set").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))

	updateSQL := fmt.Sprintf(
		"UPDATE %s SET eval_mode = ?, eval_case = ?, updated_at = CURRENT_TIMESTAMP(6) WHERE app_name = ? AND eval_set_id = ? AND eval_id = ?",
		m.tables.EvalCases,
	)
	mock.ExpectExec(regexp.QuoteMeta(updateSQL)).
		WithArgs("", sqlmock.AnyArg(), "app", "set", "missing").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := m.UpdateCase(ctx, "app", "set", &evalset.EvalCase{EvalID: "missing"})
	assert.Error(t, err)
	assert.True(t, errors.Is(err, os.ErrNotExist))

	mock.ExpectQuery(regexp.QuoteMeta(existsSQL)).
		WithArgs("app", "set").
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))

	deleteSQL := fmt.Sprintf(
		"DELETE FROM %s WHERE app_name = ? AND eval_set_id = ? AND eval_id = ?",
		m.tables.EvalCases,
	)
	mock.ExpectExec(regexp.QuoteMeta(deleteSQL)).
		WithArgs("app", "set", "missing").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = m.DeleteCase(ctx, "app", "set", "missing")
	assert.Error(t, err)
	assert.True(t, errors.Is(err, os.ErrNotExist))

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAddCase_PropagatesEnsureError(t *testing.T) {
	ctx := context.Background()
	m, db, mock := newEvalSetManager(t)
	t.Cleanup(func() { _ = db.Close() })

	existsSQL := fmt.Sprintf("SELECT 1 FROM %s WHERE app_name = ? AND eval_set_id = ?", m.tables.EvalSets)
	mock.ExpectQuery(regexp.QuoteMeta(existsSQL)).
		WithArgs("app", "set").
		WillReturnError(sql.ErrNoRows)

	err := m.AddCase(ctx, "app", "set", &evalset.EvalCase{EvalID: "case"})
	assert.Error(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}
