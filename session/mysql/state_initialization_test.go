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
	"database/sql/driver"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	drivermysql "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqldb"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type sessionStateJSONArgument struct {
	match func(*SessionState) bool
}

type stateInitializationResult struct {
	rows int64
	err  error
}

func (r stateInitializationResult) LastInsertId() (int64, error) {
	return 0, nil
}

func (r stateInitializationResult) RowsAffected() (int64, error) {
	return r.rows, r.err
}

func (a sessionStateJSONArgument) Match(value driver.Value) bool {
	var encoded []byte
	switch typed := value.(type) {
	case string:
		encoded = []byte(typed)
	case []byte:
		encoded = typed
	default:
		return false
	}
	var state SessionState
	if err := json.Unmarshal(encoded, &state); err != nil {
		return false
	}
	return a.match(&state)
}

func stateInitializationTestKey() session.Key {
	return session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "session",
	}
}

func stateInitializationTestGeneration() stateInitializationGeneration {
	return stateInitializationGeneration{
		rowID:     42,
		createdAt: time.Date(2026, 8, 10, 12, 0, 0, 123456000, time.UTC),
	}
}

func marshalStateInitializationSession(
	t *testing.T,
	key session.Key,
	state session.StateMap,
	generation stateInitializationGeneration,
) []byte {
	t.Helper()
	encoded, err := json.Marshal(&SessionState{
		ID:        key.SessionID,
		State:     state,
		CreatedAt: generation.createdAt,
		UpdatedAt: generation.createdAt,
	})
	require.NoError(t, err)
	return encoded
}

func expectStateInitializationLoad(
	mock sqlmock.Sqlmock,
	key session.Key,
	generation stateInitializationGeneration,
	stateBytes []byte,
) {
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, state, created_at FROM session_states")).
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "state", "created_at"}).
			AddRow(generation.rowID, stateBytes, generation.createdAt))
}

func expectStateInitializationWriterLoad(
	mock sqlmock.Sqlmock,
	key session.Key,
	generation stateInitializationGeneration,
	stateBytes []byte,
) {
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(
		`SELECT id, state, created_at FROM session_states
			WHERE app_name = ? AND user_id = ? AND session_id = ?
			AND deleted_at IS NULL
			AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP(6))
			FOR UPDATE`,
	)).
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "state", "created_at"}).
			AddRow(generation.rowID, stateBytes, generation.createdAt))
	mock.ExpectCommit()
}

func newStateInitializationSQLMockService(
	t *testing.T,
) (*Service, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	service := createTestService(t, db)
	service.stateInitializationRenewInterval = time.Hour
	cleanup := func() {
		require.NoError(t, mock.ExpectationsWereMet())
		_ = db.Close()
	}
	return service, mock, cleanup
}

func TestLoadOrInitializeSessionStateReturnsDefensiveExistingValue(t *testing.T) {
	service, mock, cleanup := newStateInitializationSQLMockService(t)
	defer cleanup()
	key := stateInitializationTestKey()
	generation := stateInitializationTestGeneration()
	stateBytes := marshalStateInitializationSession(
		t,
		key,
		session.StateMap{"state": []byte("valid")},
		generation,
	)
	expectStateInitializationLoad(mock, key, generation, stateBytes)
	expectStateInitializationLoad(mock, key, generation, stateBytes)

	var callbackCalls atomic.Int32
	var nilCtx context.Context
	validate := func(value []byte) bool {
		valid := string(value) == "valid"
		value[0] = 'X'
		return valid
	}
	value, didInitialize, err := service.LoadOrInitializeSessionState(
		nilCtx,
		key,
		"state",
		validate,
		func(context.Context) ([]byte, error) {
			callbackCalls.Add(1)
			return []byte("unexpected"), nil
		},
	)
	require.NoError(t, err)
	require.False(t, didInitialize)
	require.Equal(t, "valid", string(value))
	value[0] = 'Y'

	value, didInitialize, err = service.LoadOrInitializeSessionState(
		context.Background(),
		key,
		"state",
		validate,
		func(context.Context) ([]byte, error) {
			callbackCalls.Add(1)
			return []byte("unexpected"), nil
		},
	)
	require.NoError(t, err)
	require.False(t, didInitialize)
	require.Equal(t, "valid", string(value))
	require.Zero(t, callbackCalls.Load())
}

func TestLoadOrInitializeSessionStateMissingSessionDoesNotRunCallback(t *testing.T) {
	service, mock, cleanup := newStateInitializationSQLMockService(t)
	defer cleanup()
	key := stateInitializationTestKey()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, state, created_at FROM session_states")).
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnError(sql.ErrNoRows)
	var callbackCalls atomic.Int32
	_, didInitialize, err := service.LoadOrInitializeSessionState(
		context.Background(),
		key,
		"state",
		func([]byte) bool { return false },
		func(context.Context) ([]byte, error) {
			callbackCalls.Add(1)
			return []byte("value"), nil
		},
	)
	require.ErrorIs(t, err, errStateInitializationSessionNotFound)
	require.False(t, didInitialize)
	require.Zero(t, callbackCalls.Load())
}

func TestLoadOrInitializeSessionStateDisabled(t *testing.T) {
	service, _, cleanup := newStateInitializationSQLMockService(t)
	defer cleanup()
	service.opts.stateInitializationEnabled = false

	var callbackCalls atomic.Int32
	_, didInitialize, err := service.LoadOrInitializeSessionState(
		context.Background(),
		stateInitializationTestKey(),
		"state",
		func([]byte) bool { return false },
		func(context.Context) ([]byte, error) {
			callbackCalls.Add(1)
			return []byte("value"), nil
		},
	)
	require.ErrorIs(t, err, errStateInitializationDisabled)
	require.False(t, didInitialize)
	require.False(t, service.StateInitializationAvailable())
	require.Zero(t, callbackCalls.Load())
}

func TestLoadStateInitializationValueForUpdateErrors(t *testing.T) {
	key := stateInitializationTestKey()
	tests := []struct {
		name       string
		expect     func(sqlmock.Sqlmock)
		wantErr    error
		contains   string
		stateBytes []byte
	}{
		{
			name: "missing session",
			expect: func(mock sqlmock.Sqlmock) {
				mock.ExpectBegin()
				mock.ExpectQuery(regexp.QuoteMeta(
					"SELECT id, state, created_at FROM session_states",
				)).WillReturnError(sql.ErrNoRows)
				mock.ExpectRollback()
			},
			wantErr: errStateInitializationSessionNotFound,
		},
		{
			name: "locking read error",
			expect: func(mock sqlmock.Sqlmock) {
				mock.ExpectBegin()
				mock.ExpectQuery(regexp.QuoteMeta(
					"SELECT id, state, created_at FROM session_states",
				)).WillReturnError(assert.AnError)
				mock.ExpectRollback()
			},
			contains: "lock session for initialization recheck",
		},
		{
			name: "invalid state JSON",
			expect: func(mock sqlmock.Sqlmock) {
				mock.ExpectBegin()
				mock.ExpectQuery(regexp.QuoteMeta(
					"SELECT id, state, created_at FROM session_states",
				)).WillReturnRows(sqlmock.NewRows([]string{
					"id", "state", "created_at",
				}).AddRow(42, []byte("{"), time.Now()))
				mock.ExpectCommit()
			},
			contains: "unmarshal writer session state",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, mock, cleanup := newStateInitializationSQLMockService(t)
			defer cleanup()
			test.expect(mock)
			_, _, _, err := service.loadStateInitializationValueForUpdate(
				context.Background(), key, "state",
			)
			if test.wantErr != nil {
				require.ErrorIs(t, err, test.wantErr)
			}
			if test.contains != "" {
				require.ErrorContains(t, err, test.contains)
			}
		})
	}
}

func TestLoadStateInitializationValueErrors(t *testing.T) {
	key := stateInitializationTestKey()
	for _, test := range []struct {
		name     string
		row      *sqlmock.Rows
		err      error
		contains string
	}{
		{
			name:     "query error",
			err:      assert.AnError,
			contains: "load session",
		},
		{
			name: "invalid state JSON",
			row: sqlmock.NewRows([]string{"id", "state", "created_at"}).
				AddRow(42, []byte("{"), time.Now()),
			contains: "unmarshal session state",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, mock, cleanup := newStateInitializationSQLMockService(t)
			defer cleanup()
			expectation := mock.ExpectQuery(regexp.QuoteMeta(
				"SELECT id, state, created_at FROM session_states",
			)).WithArgs(key.AppName, key.UserID, key.SessionID)
			if test.err != nil {
				expectation.WillReturnError(test.err)
			} else {
				expectation.WillReturnRows(test.row)
			}
			_, _, _, err := service.loadStateInitializationValue(
				context.Background(), key, "state",
			)
			require.ErrorContains(t, err, test.contains)
		})
	}
}

func TestLoadOrInitializeSessionStateCommitsProjectionsAtomically(t *testing.T) {
	service, mock, cleanup := newStateInitializationSQLMockService(t)
	defer cleanup()
	key := stateInitializationTestKey()
	generation := stateInitializationTestGeneration()
	stateBytes := marshalStateInitializationSession(
		t,
		key,
		session.StateMap{"cleared": []byte("stale")},
		generation,
	)
	coordinationKey := stateInitializationCoordinationKey(key, "canonical")
	expectStateInitializationLoad(mock, key, generation, stateBytes)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO state_initialization_leases")).
		WithArgs(
			coordinationKey[:],
			key.UserID,
			sqlmock.AnyArg(),
			generation.rowID,
			generation.createdAt,
			int64(defaultStateInitializationLeaseTTL/time.Microsecond),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))
	expectStateInitializationWriterLoad(mock, key, generation, stateBytes)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE state_initialization_leases")).
		WithArgs(
			int64(defaultStateInitializationLeaseTTL/time.Microsecond),
			coordinationKey[:],
			key.UserID,
			sqlmock.AnyArg(),
			generation.rowID,
			generation.createdAt,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT session_row_id, session_created_at,")).
		WithArgs(coordinationKey[:], key.UserID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{
			"session_row_id", "session_created_at", "active",
		}).AddRow(generation.rowID, generation.createdAt, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, state, created_at,")).
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "state", "created_at", "active",
		}).AddRow(generation.rowID, stateBytes, generation.createdAt, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE session_states")).
		WithArgs(
			sessionStateJSONArgument{match: func(state *SessionState) bool {
				canonical, canonicalPresent := state.State["canonical"]
				legacy, legacyPresent := state.State["legacy"]
				cleared, clearedPresent := state.State["cleared"]
				return canonicalPresent && string(canonical) == "canonical" &&
					legacyPresent && string(legacy) == "legacy" &&
					clearedPresent && cleared == nil
			}},
			sqlmock.AnyArg(),
			nil,
			generation.rowID,
			key.UserID,
			generation.createdAt,
			key.AppName,
			key.SessionID,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM state_initialization_leases")).
		WithArgs(
			coordinationKey[:],
			key.UserID,
			sqlmock.AnyArg(),
			generation.rowID,
			generation.createdAt,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	initializedValue := []byte("canonical")
	projectedValue := []byte("legacy")
	value, didInitialize, err := service.LoadOrInitializeSessionState(
		context.Background(),
		key,
		"canonical",
		func(value []byte) bool { return string(value) == "canonical" },
		func(context.Context) ([]byte, error) { return initializedValue, nil },
		session.StateInitializationProjection{
			StateKey: "legacy",
			Project: func(value []byte) ([]byte, error) {
				value[0] = 'X'
				return projectedValue, nil
			},
		},
		session.StateInitializationProjection{
			StateKey: "cleared",
			Project:  func([]byte) ([]byte, error) { return nil, nil },
		},
	)
	require.NoError(t, err)
	require.True(t, didInitialize)
	require.Equal(t, "canonical", string(value))
	initializedValue[0] = 'I'
	projectedValue[0] = 'P'
	require.Equal(t, "canonical", string(value))
}

func TestLoadOrInitializeSessionStateRecheckReturnsConcurrentValidValue(t *testing.T) {
	service, mock, cleanup := newStateInitializationSQLMockService(t)
	defer cleanup()
	key := stateInitializationTestKey()
	generation := stateInitializationTestGeneration()
	coordinationKey := stateInitializationCoordinationKey(key, "state")
	invalidState := marshalStateInitializationSession(
		t,
		key,
		session.StateMap{"state": []byte("invalid")},
		generation,
	)
	validState := marshalStateInitializationSession(
		t,
		key,
		session.StateMap{"state": []byte("valid")},
		generation,
	)
	expectStateInitializationLoad(mock, key, generation, invalidState)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO state_initialization_leases")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	expectStateInitializationWriterLoad(mock, key, generation, validState)
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM state_initialization_leases")).
		WithArgs(
			coordinationKey[:],
			key.UserID,
			sqlmock.AnyArg(),
			generation.rowID,
			generation.createdAt,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	var callbackCalls atomic.Int32
	value, didInitialize, err := service.LoadOrInitializeSessionState(
		context.Background(),
		key,
		"state",
		func(value []byte) bool { return string(value) == "valid" },
		func(context.Context) ([]byte, error) {
			callbackCalls.Add(1)
			return []byte("unexpected"), nil
		},
	)
	require.NoError(t, err)
	require.False(t, didInitialize)
	require.Equal(t, "valid", string(value))
	require.Zero(t, callbackCalls.Load())
}

func TestLoadOrInitializeSessionStateFailurePathsReleaseLease(t *testing.T) {
	wantCallbackErr := errors.New("callback failed")
	wantProjectionErr := errors.New("projection failed")
	tests := []struct {
		name       string
		initialize func(context.Context) ([]byte, error)
		projection []session.StateInitializationProjection
		wantErr    error
		wantPanic  bool
	}{
		{
			name: "callback error",
			initialize: func(context.Context) ([]byte, error) {
				return nil, wantCallbackErr
			},
			wantErr: wantCallbackErr,
		},
		{
			name: "callback panic",
			initialize: func(context.Context) ([]byte, error) {
				panic("boom")
			},
			wantPanic: true,
		},
		{
			name: "invalid callback result",
			initialize: func(context.Context) ([]byte, error) {
				return []byte("invalid"), nil
			},
		},
		{
			name: "projection error",
			initialize: func(context.Context) ([]byte, error) {
				return []byte("valid"), nil
			},
			projection: []session.StateInitializationProjection{{
				StateKey: "projection",
				Project: func([]byte) ([]byte, error) {
					return nil, wantProjectionErr
				},
			}},
			wantErr: wantProjectionErr,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, mock, cleanup := newStateInitializationSQLMockService(t)
			defer cleanup()
			key := stateInitializationTestKey()
			generation := stateInitializationTestGeneration()
			stateBytes := marshalStateInitializationSession(t, key, nil, generation)
			coordinationKey := stateInitializationCoordinationKey(key, "state")
			expectStateInitializationLoad(mock, key, generation, stateBytes)
			mock.ExpectExec(regexp.QuoteMeta("INSERT INTO state_initialization_leases")).
				WillReturnResult(sqlmock.NewResult(1, 1))
			expectStateInitializationWriterLoad(mock, key, generation, stateBytes)
			mock.ExpectExec(regexp.QuoteMeta("UPDATE state_initialization_leases")).
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec(regexp.QuoteMeta("DELETE FROM state_initialization_leases")).
				WithArgs(
					coordinationKey[:],
					key.UserID,
					sqlmock.AnyArg(),
					generation.rowID,
					generation.createdAt,
				).
				WillReturnResult(sqlmock.NewResult(0, 1))

			call := func() error {
				_, didInitialize, err := service.LoadOrInitializeSessionState(
					context.Background(),
					key,
					"state",
					func(value []byte) bool { return string(value) == "valid" },
					test.initialize,
					test.projection...,
				)
				require.False(t, didInitialize)
				return err
			}
			if test.wantPanic {
				require.Panics(t, func() { _ = call() })
				return
			}
			err := call()
			require.Error(t, err)
			if test.wantErr != nil {
				require.ErrorIs(t, err, test.wantErr)
			}
		})
	}
}

func TestLoadOrInitializeSessionStateWaiterCancellation(t *testing.T) {
	service, mock, cleanup := newStateInitializationSQLMockService(t)
	defer cleanup()
	service.stateInitializationPollMin = time.Second
	key := stateInitializationTestKey()
	generation := stateInitializationTestGeneration()
	stateBytes := marshalStateInitializationSession(t, key, nil, generation)
	coordinationKey := stateInitializationCoordinationKey(key, "state")
	expectStateInitializationLoad(mock, key, generation, stateBytes)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO state_initialization_leases")).
		WillReturnError(&drivermysql.MySQLError{
			Number:  sqldb.MySQLErrDuplicateEntry,
			Message: "duplicate lease",
		})
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT session_row_id, session_created_at,")).
		WithArgs(coordinationKey[:], key.UserID).
		WillReturnRows(sqlmock.NewRows([]string{
			"session_row_id", "session_created_at", "expired",
		}).AddRow(generation.rowID, generation.createdAt, 0))
	mock.ExpectCommit()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, didInitialize, err := service.LoadOrInitializeSessionState(
		ctx,
		key,
		"state",
		func(value []byte) bool { return len(value) > 0 },
		func(context.Context) ([]byte, error) { return []byte("unexpected"), nil },
	)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.False(t, didInitialize)
}

func TestLoadOrInitializeSessionStateWaiterFencesGenerationChange(t *testing.T) {
	service, mock, cleanup := newStateInitializationSQLMockService(t)
	defer cleanup()
	service.stateInitializationPollMin = time.Millisecond
	service.stateInitializationPollMax = time.Millisecond
	key := stateInitializationTestKey()
	oldGeneration := stateInitializationTestGeneration()
	newGeneration := stateInitializationGeneration{
		rowID:     oldGeneration.rowID,
		createdAt: oldGeneration.createdAt.Add(time.Second),
	}
	oldState := marshalStateInitializationSession(t, key, nil, oldGeneration)
	newState := marshalStateInitializationSession(t, key, nil, newGeneration)
	coordinationKey := stateInitializationCoordinationKey(key, "state")
	expectStateInitializationLoad(mock, key, oldGeneration, oldState)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO state_initialization_leases")).
		WillReturnError(&drivermysql.MySQLError{
			Number:  sqldb.MySQLErrDuplicateEntry,
			Message: "duplicate lease",
		})
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT session_row_id, session_created_at,")).
		WithArgs(coordinationKey[:], key.UserID).
		WillReturnRows(sqlmock.NewRows([]string{
			"session_row_id", "session_created_at", "expired",
		}).AddRow(oldGeneration.rowID, oldGeneration.createdAt, 0))
	mock.ExpectCommit()
	expectStateInitializationLoad(mock, key, newGeneration, newState)

	_, didInitialize, err := service.LoadOrInitializeSessionState(
		context.Background(),
		key,
		"state",
		func([]byte) bool { return false },
		func(context.Context) ([]byte, error) { return []byte("unexpected"), nil },
	)
	require.ErrorIs(t, err, errStateInitializationGenerationChanged)
	require.False(t, didInitialize)
}

func TestTryAcquireStateInitializationLeaseTakesOverExpiredOwner(t *testing.T) {
	service, mock, cleanup := newStateInitializationSQLMockService(t)
	defer cleanup()
	service.opts.tdsqlSharding = true
	key := stateInitializationTestKey()
	generation := stateInitializationTestGeneration()
	coordinationKey := stateInitializationCoordinationKey(key, "state")
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO state_initialization_leases")).
		WithArgs(
			coordinationKey[:],
			key.UserID,
			"new-owner",
			generation.rowID,
			generation.createdAt,
			int64(defaultStateInitializationLeaseTTL/time.Microsecond),
		).
		WillReturnError(&drivermysql.MySQLError{
			Number:  sqldb.MySQLErrDuplicateEntry,
			Message: "duplicate lease",
		})
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT session_row_id, session_created_at,")).
		WithArgs(coordinationKey[:], key.UserID).
		WillReturnRows(sqlmock.NewRows([]string{
			"session_row_id", "session_created_at", "expired",
		}).AddRow(generation.rowID, generation.createdAt, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, created_at FROM session_states")).
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).
			AddRow(generation.rowID, generation.createdAt))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE state_initialization_leases")).
		WithArgs(
			"new-owner",
			generation.rowID,
			generation.createdAt,
			int64(defaultStateInitializationLeaseTTL/time.Microsecond),
			coordinationKey[:],
			key.UserID,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	acquired, deadline, err := service.tryAcquireStateInitializationLease(
		context.Background(),
		key,
		coordinationKey,
		"new-owner",
		generation,
	)
	require.NoError(t, err)
	require.True(t, acquired)
	require.True(t, deadline.After(time.Now()))
}

func TestTryAcquireStateInitializationLeaseDoesNotLetStaleGenerationSteal(t *testing.T) {
	service, mock, cleanup := newStateInitializationSQLMockService(t)
	defer cleanup()
	key := stateInitializationTestKey()
	oldGeneration := stateInitializationTestGeneration()
	newGeneration := stateInitializationGeneration{
		rowID:     oldGeneration.rowID,
		createdAt: oldGeneration.createdAt.Add(time.Second),
	}
	coordinationKey := stateInitializationCoordinationKey(key, "state")
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO state_initialization_leases")).
		WillReturnError(&drivermysql.MySQLError{
			Number:  sqldb.MySQLErrDuplicateEntry,
			Message: "duplicate lease",
		})
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT session_row_id, session_created_at,")).
		WillReturnRows(sqlmock.NewRows([]string{
			"session_row_id", "session_created_at", "expired",
		}).AddRow(oldGeneration.rowID, oldGeneration.createdAt, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, created_at FROM session_states")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).
			AddRow(newGeneration.rowID, newGeneration.createdAt))
	mock.ExpectRollback()

	acquired, _, err := service.tryAcquireStateInitializationLease(
		context.Background(),
		key,
		coordinationKey,
		"stale-owner",
		oldGeneration,
	)
	require.ErrorIs(t, err, errStateInitializationGenerationChanged)
	require.False(t, acquired)
}

func TestTryAcquireStateInitializationLeaseErrors(t *testing.T) {
	key := stateInitializationTestKey()
	generation := stateInitializationTestGeneration()
	coordinationKey := stateInitializationCoordinationKey(key, "state")

	t.Run("insert", func(t *testing.T) {
		service, mock, cleanup := newStateInitializationSQLMockService(t)
		defer cleanup()
		mock.ExpectExec(regexp.QuoteMeta(
			"INSERT INTO state_initialization_leases",
		)).WillReturnError(assert.AnError)
		acquired, _, err := service.tryAcquireStateInitializationLease(
			context.Background(), key, coordinationKey, "owner", generation,
		)
		require.False(t, acquired)
		require.ErrorContains(t, err, "acquire session state initialization lease")
	})

	t.Run("lease lock", func(t *testing.T) {
		service, mock, cleanup := newStateInitializationSQLMockService(t)
		defer cleanup()
		mock.ExpectExec(regexp.QuoteMeta(
			"INSERT INTO state_initialization_leases",
		)).WillReturnError(&drivermysql.MySQLError{
			Number: sqldb.MySQLErrDuplicateEntry,
		})
		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(
			"SELECT session_row_id, session_created_at,",
		)).WillReturnError(assert.AnError)
		mock.ExpectRollback()
		acquired, _, err := service.tryAcquireStateInitializationLease(
			context.Background(), key, coordinationKey, "owner", generation,
		)
		require.False(t, acquired)
		require.ErrorContains(t, err, "lock initialization lease")
	})
}

func TestRenewAndAbortStateInitializationLeaseErrors(t *testing.T) {
	key := stateInitializationTestKey()
	generation := stateInitializationTestGeneration()
	coordinationKey := stateInitializationCoordinationKey(key, "state")
	for _, operation := range []struct {
		name      string
		sqlPrefix string
		call      func(*Service) (bool, error)
	}{
		{
			name:      "renew",
			sqlPrefix: "UPDATE state_initialization_leases",
			call: func(service *Service) (bool, error) {
				return service.renewStateInitializationLease(
					context.Background(), key, coordinationKey, "owner", generation,
				)
			},
		},
		{
			name:      "abort",
			sqlPrefix: "DELETE FROM state_initialization_leases",
			call: func(service *Service) (bool, error) {
				return service.abortStateInitializationLease(
					context.Background(), key, coordinationKey, "owner", generation,
				)
			},
		},
	} {
		t.Run(operation.name+" execution", func(t *testing.T) {
			service, mock, cleanup := newStateInitializationSQLMockService(t)
			defer cleanup()
			mock.ExpectExec(regexp.QuoteMeta(operation.sqlPrefix)).
				WillReturnError(assert.AnError)
			owned, err := operation.call(service)
			require.False(t, owned)
			require.Error(t, err)
		})
		t.Run(operation.name+" rows affected", func(t *testing.T) {
			service, mock, cleanup := newStateInitializationSQLMockService(t)
			defer cleanup()
			mock.ExpectExec(regexp.QuoteMeta(operation.sqlPrefix)).
				WillReturnResult(stateInitializationResult{err: assert.AnError})
			owned, err := operation.call(service)
			require.False(t, owned)
			require.Error(t, err)
		})
	}
}

func TestInitializeSessionStateRejectsOwnershipLossBeforeCallback(t *testing.T) {
	service, mock, cleanup := newStateInitializationSQLMockService(t)
	defer cleanup()
	key := stateInitializationTestKey()
	generation := stateInitializationTestGeneration()
	stateBytes := marshalStateInitializationSession(t, key, nil, generation)
	coordinationKey := stateInitializationCoordinationKey(key, "state")
	expectStateInitializationWriterLoad(mock, key, generation, stateBytes)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE state_initialization_leases")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM state_initialization_leases")).
		WillReturnResult(sqlmock.NewResult(0, 0))

	var callbackCalls atomic.Int32
	_, didInitialize, err := service.initializeSessionState(
		context.Background(),
		key,
		"state",
		coordinationKey,
		"owner",
		time.Now().Add(time.Minute),
		generation,
		func(value []byte) bool { return len(value) > 0 },
		func(context.Context) ([]byte, error) {
			callbackCalls.Add(1)
			return []byte("value"), nil
		},
		nil,
	)
	require.ErrorContains(t, err, "ownership lost before callback")
	require.False(t, didInitialize)
	require.Zero(t, callbackCalls.Load())
}

func TestStateInitializationRenewalReportsOwnershipLossAndDeadline(t *testing.T) {
	t.Run("ownership lost", func(t *testing.T) {
		service, mock, cleanup := newStateInitializationSQLMockService(t)
		defer cleanup()
		service.stateInitializationRenewInterval = 5 * time.Millisecond
		key := stateInitializationTestKey()
		generation := stateInitializationTestGeneration()
		coordinationKey := stateInitializationCoordinationKey(key, "state")
		mock.ExpectExec(regexp.QuoteMeta("UPDATE state_initialization_leases")).
			WillReturnResult(sqlmock.NewResult(0, 0))
		initializeCtx, cancelInitialize := context.WithCancel(context.Background())
		defer cancelInitialize()
		renewal := service.startStateInitializationRenewal(
			context.Background(),
			key,
			coordinationKey,
			"owner",
			generation,
			cancelInitialize,
			time.Now().Add(time.Second),
		)
		select {
		case <-initializeCtx.Done():
		case <-time.After(time.Second):
			t.Fatal("renewal did not cancel initialization after ownership loss")
		}
		err := renewal.stop()
		require.ErrorContains(t, err, "ownership lost")
	})

	t.Run("deadline exceeded", func(t *testing.T) {
		service, _, cleanup := newStateInitializationSQLMockService(t)
		defer cleanup()
		key := stateInitializationTestKey()
		generation := stateInitializationTestGeneration()
		coordinationKey := stateInitializationCoordinationKey(key, "state")
		initializeCtx, cancelInitialize := context.WithCancel(context.Background())
		defer cancelInitialize()
		renewal := service.startStateInitializationRenewal(
			context.Background(),
			key,
			coordinationKey,
			"owner",
			generation,
			cancelInitialize,
			time.Now().Add(-time.Millisecond),
		)
		select {
		case <-initializeCtx.Done():
		case <-time.After(time.Second):
			t.Fatal("renewal did not cancel initialization after deadline")
		}
		err := renewal.stop()
		require.ErrorContains(t, err, "deadline exceeded")
	})
}

func TestCommitStateInitializationRejectsStaleOwnerAndGeneration(t *testing.T) {
	key := stateInitializationTestKey()
	generation := stateInitializationTestGeneration()
	coordinationKey := stateInitializationCoordinationKey(key, "state")
	for _, test := range []struct {
		name       string
		expectRows func(sqlmock.Sqlmock)
		wantErr    error
	}{
		{
			name: "ownership lost",
			expectRows: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta("SELECT session_row_id, session_created_at,")).
					WillReturnError(sql.ErrNoRows)
			},
			wantErr: errStateInitializationOwnershipLost,
		},
		{
			name: "session generation changed",
			expectRows: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta("SELECT session_row_id, session_created_at,")).
					WillReturnRows(sqlmock.NewRows([]string{
						"session_row_id", "session_created_at", "active",
					}).AddRow(generation.rowID, generation.createdAt, 1))
				newGeneration := stateInitializationGeneration{
					rowID:     generation.rowID,
					createdAt: generation.createdAt.Add(time.Second),
				}
				stateBytes := marshalStateInitializationSession(t, key, nil, newGeneration)
				mock.ExpectQuery(regexp.QuoteMeta("SELECT id, state, created_at,")).
					WillReturnRows(sqlmock.NewRows([]string{
						"id", "state", "created_at", "active",
					}).AddRow(newGeneration.rowID, stateBytes, newGeneration.createdAt, 1))
			},
			wantErr: errStateInitializationGenerationChanged,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, mock, cleanup := newStateInitializationSQLMockService(t)
			defer cleanup()
			mock.ExpectBegin()
			test.expectRows(mock)
			mock.ExpectRollback()
			err := service.commitStateInitialization(
				context.Background(),
				key,
				"state",
				[]byte("value"),
				nil,
				coordinationKey,
				"owner",
				generation,
			)
			require.ErrorIs(t, err, test.wantErr)
		})
	}
}

func TestCommitStateInitializationRollsBackPrimaryAndProjectionsTogether(t *testing.T) {
	service, mock, cleanup := newStateInitializationSQLMockService(t)
	defer cleanup()
	key := stateInitializationTestKey()
	generation := stateInitializationTestGeneration()
	coordinationKey := stateInitializationCoordinationKey(key, "state")
	stateBytes := marshalStateInitializationSession(t, key, nil, generation)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT session_row_id, session_created_at,")).
		WillReturnRows(sqlmock.NewRows([]string{
			"session_row_id", "session_created_at", "active",
		}).AddRow(generation.rowID, generation.createdAt, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, state, created_at,")).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "state", "created_at", "active",
		}).AddRow(generation.rowID, stateBytes, generation.createdAt, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE session_states")).
		WillReturnError(errors.New("write failed"))
	mock.ExpectRollback()

	err := service.commitStateInitialization(
		context.Background(),
		key,
		"state",
		[]byte("primary"),
		session.StateMap{"projection": []byte("projected")},
		coordinationKey,
		"owner",
		generation,
	)
	require.ErrorContains(t, err, "write failed")
}

func TestStateInitializationArgumentValidation(t *testing.T) {
	validKey := stateInitializationTestKey()
	validate := func([]byte) bool { return true }
	initialize := func(context.Context) ([]byte, error) { return []byte("value"), nil }
	project := func([]byte) ([]byte, error) { return []byte("value"), nil }
	tests := []struct {
		name        string
		key         session.Key
		stateKey    string
		validate    func([]byte) bool
		initialize  func(context.Context) ([]byte, error)
		projections []session.StateInitializationProjection
	}{
		{name: "invalid session key", key: session.Key{}, stateKey: "state", validate: validate, initialize: initialize},
		{name: "empty state key", key: validKey, stateKey: " ", validate: validate, initialize: initialize},
		{name: "app state key", key: validKey, stateKey: session.StateAppPrefix + "state", validate: validate, initialize: initialize},
		{name: "user state key", key: validKey, stateKey: session.StateUserPrefix + "state", validate: validate, initialize: initialize},
		{name: "missing validator", key: validKey, stateKey: "state", initialize: initialize},
		{name: "missing initializer", key: validKey, stateKey: "state", validate: validate},
		{
			name:       "projection reuses primary",
			key:        validKey,
			stateKey:   "state",
			validate:   validate,
			initialize: initialize,
			projections: []session.StateInitializationProjection{{
				StateKey: "state",
				Project:  project,
			}},
		},
		{
			name:       "duplicate projection",
			key:        validKey,
			stateKey:   "state",
			validate:   validate,
			initialize: initialize,
			projections: []session.StateInitializationProjection{
				{StateKey: "projection", Project: project},
				{StateKey: "projection", Project: project},
			},
		},
		{
			name:       "missing projection function",
			key:        validKey,
			stateKey:   "state",
			validate:   validate,
			initialize: initialize,
			projections: []session.StateInitializationProjection{{
				StateKey: "projection",
			}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateStateInitializationArguments(
				test.key,
				test.stateKey,
				test.validate,
				test.initialize,
				test.projections,
			)
			require.Error(t, err)
		})
	}
}

func TestStateInitializationHelpers(t *testing.T) {
	keyA := session.Key{AppName: "a", UserID: "bc", SessionID: "d"}
	keyB := session.Key{AppName: "ab", UserID: "c", SessionID: "d"}
	require.NotEqual(
		t,
		stateInitializationCoordinationKey(keyA, "state"),
		stateInitializationCoordinationKey(keyB, "state"),
	)
	require.Equal(
		t,
		stateInitializationCoordinationKey(keyA, "state"),
		stateInitializationCoordinationKey(keyA, "state"),
	)

	require.True(t, isDuplicateEntryError(&drivermysql.MySQLError{
		Number: sqldb.MySQLErrDuplicateEntry,
	}))
	require.True(t, isDuplicateEntryError(errors.Join(
		errors.New("wrapped"),
		&drivermysql.MySQLError{Number: sqldb.MySQLErrDuplicateEntry},
	)))
	require.False(t, isDuplicateEntryError(nil))
	require.False(t, isDuplicateEntryError(errors.New("other")))
	require.False(t, isDuplicateEntryError(&drivermysql.MySQLError{
		Number: sqldb.MySQLErrDuplicateKeyName,
	}))

	require.Contains(t, tdsqlCreateStateInitializationLeasesTable, "PRIMARY KEY (id, user_id)")
	require.Contains(t, tdsqlCreateStateInitializationLeasesTable, "shardkey=user_id")
	require.Contains(t, sqlCreateStateInitializationLeasesUniqueIndex, "coordination_key, user_id")
	require.Contains(t, sqlCreateStateInitializationLeasesTable, "COLLATE ascii_bin")
	require.Equal(t, "uniq", stateInitializationLeaseIndexUniq)
	require.Equal(t, "exp", stateInitializationLeaseIndexExp)
	require.Equal(t, "state_initialization_active", stateInitializationActiveColumn)
	require.Equal(t, "state_init_active", stateInitializationActiveIndex)
	require.True(t, strings.Contains(
		sqlCreateStateInitializationLeasesTable,
		"session_created_at TIMESTAMP(6) NOT NULL",
	))

	service, _, cleanup := newStateInitializationSQLMockService(t)
	defer cleanup()
	service.stateInitializationLeaseTTL = 0
	require.Equal(t, time.Millisecond, service.effectiveStateInitializationLeaseTTL())
	require.Equal(t, int64(1), stateInitializationLeaseMicros(0))
}
