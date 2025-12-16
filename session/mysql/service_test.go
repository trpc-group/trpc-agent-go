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
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/mysql"
)

// mockMySQLClient implements storage.Client for testing with sqlmock
type mockMySQLClient struct {
	db *sql.DB
}

func (m *mockMySQLClient) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return m.db.ExecContext(ctx, query, args...)
}

func (m *mockMySQLClient) Query(ctx context.Context, next storage.NextFunc, query string, args ...any) error {
	rows, err := m.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	// Iterate all rows, calling the callback function
	// This matches the behavior of the real MySQL client
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

func (m *mockMySQLClient) QueryRow(ctx context.Context, dest []any, query string, args ...any) error {
	return m.db.QueryRowContext(ctx, query, args...).Scan(dest...)
}

func (m *mockMySQLClient) Transaction(ctx context.Context, fn storage.TxFunc, opts ...storage.TxOption) error {
	txOptions := &sql.TxOptions{}
	for _, opt := range opts {
		opt(txOptions)
	}

	tx, err := m.db.BeginTx(ctx, txOptions)
	if err != nil {
		return err
	}

	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}

	return tx.Commit()
}

func (m *mockMySQLClient) Close() error {
	// Do nothing - we manage db lifecycle in tests
	return nil
}

// createTestService creates a Service with sqlmock for testing
func createTestService(t *testing.T, db *sql.DB, opts ...ServiceOpt) *Service {
	t.Helper()

	// Apply default options
	serviceOpts := ServiceOpts{
		sessionEventLimit: defaultSessionEventLimit,
		asyncPersisterNum: defaultAsyncPersisterNum,
		softDelete:        true,
	}
	for _, opt := range opts {
		opt(&serviceOpts)
	}

	return &Service{
		opts:                  serviceOpts,
		mysqlClient:           &mockMySQLClient{db: db},
		tableSessionStates:    "session_states",
		tableSessionEvents:    "session_events",
		tableSessionSummaries: "session_summaries",
		tableAppStates:        "app_states",
		tableUserStates:       "user_states",
	}
}

type sessionStateJSONMatcher struct {
	t             *testing.T
	expectedID    string
	expectedState session.StateMap
	createdAt     time.Time
	previousAt    time.Time
}

func (m *sessionStateJSONMatcher) Match(v driver.Value) bool {
	m.t.Helper()
	stateBytes, ok := v.([]byte)
	if !ok {
		m.t.Errorf("expected []byte for state, got %T", v)
		return false
	}

	var stored SessionState
	if err := json.Unmarshal(stateBytes, &stored); err != nil {
		m.t.Errorf("unmarshal state failed: %v", err)
		return false
	}

	if stored.ID != m.expectedID {
		m.t.Errorf("unexpected session id, got %s", stored.ID)
		return false
	}
	if !stored.CreatedAt.Equal(m.createdAt) {
		m.t.Errorf("createdAt changed, expected %v got %v", m.createdAt, stored.CreatedAt)
		return false
	}
	if stored.UpdatedAt.IsZero() {
		m.t.Errorf("updatedAt should be set")
		return false
	}
	if !m.previousAt.IsZero() && !stored.UpdatedAt.After(m.previousAt) {
		m.t.Errorf("updatedAt should be refreshed")
		return false
	}

	for key, expected := range m.expectedState {
		got, ok := stored.State[key]
		if !ok {
			m.t.Errorf("state missing key %s", key)
			return false
		}
		if string(got) != string(expected) {
			m.t.Errorf("state mismatch for key %s", key)
			return false
		}
	}
	return true
}

func TestCreateSession_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db, WithSessionTTL(1*time.Hour))
	ctx := context.Background()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user-123",
		SessionID: "session-456",
	}

	state := session.StateMap{
		"key1": []byte(`"value1"`),
		"key2": []byte("42"),
	}

	// Mock: Check if session exists (should return no rows)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT expires_at FROM session_states")).
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(sqlmock.NewRows([]string{"expires_at"}))

	// Mock: Insert new session
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO session_states")).
		WithArgs(
			key.AppName,
			key.UserID,
			key.SessionID,
			sqlmock.AnyArg(), // state (JSON)
			sqlmock.AnyArg(), // created_at
			sqlmock.AnyArg(), // updated_at
			sqlmock.AnyArg(), // expires_at
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// Mock: List app states (empty)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM app_states")).
		WithArgs(key.AppName, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

	// Mock: List user states (empty)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM user_states")).
		WithArgs(key.AppName, key.UserID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

	sess, err := s.CreateSession(ctx, key, state)
	require.NoError(t, err)
	assert.NotNil(t, sess)
	assert.Equal(t, key.SessionID, sess.ID)
	assert.Equal(t, state, sess.State)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateSession_AlreadyExists(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user-123",
		SessionID: "session-456",
	}

	// Mock: Session exists and not expired
	futureTime := time.Now().Add(1 * time.Hour)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT expires_at FROM session_states")).
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(sqlmock.NewRows([]string{"expires_at"}).AddRow(futureTime))

	sess, err := s.CreateSession(ctx, key, session.StateMap{})
	assert.Error(t, err)
	assert.Nil(t, sess)
	assert.Contains(t, err.Error(), "session already exists")

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateAppState_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db, WithAppStateTTL(24*time.Hour))
	ctx := context.Background()

	appName := "test-app"
	state := session.StateMap{
		"key1": []byte(`"value1"`),
		"key2": []byte(`"value2"`),
	}

	// Mock: UPSERT for each state key
	for range state {
		mock.ExpectExec(regexp.QuoteMeta("REPLACE INTO app_states")).
			WithArgs(
				appName,
				sqlmock.AnyArg(), // key
				sqlmock.AnyArg(), // value
				sqlmock.AnyArg(), // updated_at
				sqlmock.AnyArg(), // expires_at
			).
			WillReturnResult(sqlmock.NewResult(1, 1))
	}

	err = s.UpdateAppState(ctx, appName, state)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListAppStates_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	appName := "test-app"

	// Mock: Query returns expected values
	rows := sqlmock.NewRows([]string{"key", "value"}).
		AddRow("key1", []byte(`"value1"`)).
		AddRow("key2", []byte(`"value2"`))

	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM app_states")).
		WithArgs(appName, sqlmock.AnyArg()).
		WillReturnRows(rows)

	states, err := s.ListAppStates(ctx, appName)
	require.NoError(t, err)
	assert.Len(t, states, 2)

	// Check both keys exist
	assert.Contains(t, states, "key1")
	assert.Contains(t, states, "key2")

	// Unmarshal and verify values
	var val1, val2 string
	_ = json.Unmarshal(states["key1"], &val1)
	_ = json.Unmarshal(states["key2"], &val2)
	assert.Equal(t, "value1", val1)
	assert.Equal(t, "value2", val2)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteAppState(t *testing.T) {
	tests := []struct {
		name       string
		softDelete bool
	}{
		{"SoftDelete", true},
		{"HardDelete", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()

			s := createTestService(t, db, WithSoftDelete(tt.softDelete))
			ctx := context.Background()
			appName, key := "test-app", "test-key"

			if tt.softDelete {
				mock.ExpectExec(regexp.QuoteMeta("UPDATE app_states SET deleted_at = ?")).
					WithArgs(sqlmock.AnyArg(), appName, key).
					WillReturnResult(sqlmock.NewResult(0, 1))
			} else {
				mock.ExpectExec(regexp.QuoteMeta("DELETE FROM app_states")).
					WithArgs(appName, key).
					WillReturnResult(sqlmock.NewResult(0, 1))
			}

			err = s.DeleteAppState(ctx, appName, key)
			assert.NoError(t, err)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestAppendEvent_Sync(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db, WithSessionTTL(1*time.Hour))
	ctx := context.Background()

	sess := &session.Session{
		ID:      "sess-123",
		AppName: "test-app",
		UserID:  "user-456",
		State:   session.StateMap{"existing": []byte(`"state"`)},
		Events:  []event.Event{},
	}

	// Create test event using New helper
	evt := event.New("inv-123", "test-author")
	evt.Response = &model.Response{
		Object: model.ObjectTypeChatCompletion,
		Done:   true,
		Choices: []model.Choice{
			{
				Index: 0,
				Message: model.Message{
					Content: "test response content",
				},
			},
		},
	}
	// Set IsPartial=false
	evt.IsPartial = false

	// Mock: Get current session state
	sessState := SessionState{
		ID:    sess.ID,
		State: sess.State,
	}
	stateBytes, _ := json.Marshal(sessState)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT state, expires_at FROM session_states")).
		WithArgs(sess.AppName, sess.UserID, sess.ID).
		WillReturnRows(sqlmock.NewRows([]string{"state", "expires_at"}).
			AddRow(stateBytes, nil))

	// Mock: Transaction (update session + insert event + enforce limit)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE session_states SET state = ?, updated_at = ?, expires_at = ?")).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sess.AppName, sess.UserID, sess.ID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	// Only insert if event passes the filter (Response != nil && !IsPartial && IsValidContent())
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO session_events")).
		WithArgs(
			sess.AppName,
			sess.UserID,
			sess.ID,
			sqlmock.AnyArg(), // event (JSON)
			sqlmock.AnyArg(), // created_at
			sqlmock.AnyArg(), // updated_at
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	mock.ExpectCommit()

	err = s.AppendEvent(ctx, sess, evt)
	assert.NoError(t, err)
	// Note: sess.Events update logic is handled by isession.UpdateUserSession
	// which may have specific conditions for adding events to the list.
	// We focus on testing SQL operations here.
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteSession_SoftDelete(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db, WithSoftDelete(true))
	ctx := context.Background()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user-123",
		SessionID: "session-456",
	}

	// Mock: Transaction for soft delete
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE session_states SET deleted_at = ?")).
		WithArgs(sqlmock.AnyArg(), key.AppName, key.UserID, key.SessionID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE session_summaries SET deleted_at = ?")).
		WithArgs(sqlmock.AnyArg(), key.AppName, key.UserID, key.SessionID).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE session_events SET deleted_at = ?")).
		WithArgs(sqlmock.AnyArg(), key.AppName, key.UserID, key.SessionID).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	err = s.DeleteSession(ctx, key)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCleanupExpiredSessions(t *testing.T) {
	tests := []struct {
		name       string
		softDelete bool
	}{
		{"SoftDelete", true},
		{"HardDelete", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()

			s := createTestService(t, db, WithSoftDelete(tt.softDelete), WithSessionTTL(1*time.Hour))
			ctx := context.Background()
			now := time.Now()

			mock.ExpectQuery(regexp.QuoteMeta("SELECT app_name, user_id, session_id, MAX(updated_at) as updated_at FROM session_events")).
				WillReturnRows(sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "updated_at"}).
					AddRow("session-1", "app-1", "user-1", now.Add(-48*time.Hour)))

			if tt.softDelete {
				mock.ExpectBegin()
				mock.ExpectExec(regexp.QuoteMeta("UPDATE session_states SET deleted_at = ?")).
					WillReturnResult(sqlmock.NewResult(0, 5))
				mock.ExpectExec(regexp.QuoteMeta("UPDATE session_summaries SET deleted_at = ?")).
					WillReturnResult(sqlmock.NewResult(0, 3))
				mock.ExpectExec(regexp.QuoteMeta("UPDATE session_events SET deleted_at = ?")).
					WillReturnResult(sqlmock.NewResult(0, 10))

				mock.ExpectCommit()
			} else {
				mock.ExpectBegin()
				mock.ExpectExec(regexp.QuoteMeta("DELETE FROM session_states")).
					WillReturnResult(sqlmock.NewResult(0, 5))
				mock.ExpectExec(regexp.QuoteMeta("DELETE FROM session_summaries")).
					WillReturnResult(sqlmock.NewResult(0, 3))
				mock.ExpectExec(regexp.QuoteMeta("DELETE FROM session_events")).
					WillReturnResult(sqlmock.NewResult(0, 10))
				mock.ExpectCommit()
			}

			s.cleanupExpiredSessions(ctx, now)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// Mock summarizer for testing
type mockSummarizer struct {
	summarizeFunc       func(ctx context.Context, sess *session.Session) (string, error)
	shouldSummarizeFunc func(sess *session.Session) bool
}

func (m *mockSummarizer) Summarize(ctx context.Context, sess *session.Session) (string, error) {
	if m.summarizeFunc != nil {
		return m.summarizeFunc(ctx, sess)
	}
	return "test summary", nil
}

func (m *mockSummarizer) ShouldSummarize(sess *session.Session) bool {
	if m.shouldSummarizeFunc != nil {
		return m.shouldSummarizeFunc(sess)
	}
	return true
}

func (m *mockSummarizer) SetPrompt(prompt string) {}

func (m *mockSummarizer) SetModel(mdl model.Model) {}

func (m *mockSummarizer) Metadata() map[string]any {
	return map[string]any{"type": "mock"}
}

func TestUpdateUserState_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db, WithUserStateTTL(24*time.Hour))
	ctx := context.Background()

	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "user-123",
	}

	state := session.StateMap{
		"pref1": []byte(`"value1"`),
		"pref2": []byte(`"value2"`),
	}

	// Mock: UPSERT for each state key
	for range state {
		mock.ExpectExec(regexp.QuoteMeta("REPLACE INTO user_states")).
			WithArgs(
				userKey.AppName,
				userKey.UserID,
				sqlmock.AnyArg(), // key
				sqlmock.AnyArg(), // value
				sqlmock.AnyArg(), // updated_at
				sqlmock.AnyArg(), // expires_at
			).
			WillReturnResult(sqlmock.NewResult(1, 1))
	}

	err = s.UpdateUserState(ctx, userKey, state)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListUserStates_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "user-123",
	}

	// Mock: Query returns user states
	rows := sqlmock.NewRows([]string{"key", "value"}).
		AddRow("pref1", []byte(`"value1"`)).
		AddRow("pref2", []byte(`"value2"`))

	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM user_states")).
		WithArgs(userKey.AppName, userKey.UserID, sqlmock.AnyArg()).
		WillReturnRows(rows)

	states, err := s.ListUserStates(ctx, userKey)
	require.NoError(t, err)
	assert.Len(t, states, 2)
	assert.Contains(t, states, "pref1")
	assert.Contains(t, states, "pref2")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteUserState(t *testing.T) {
	tests := []struct {
		name       string
		softDelete bool
	}{
		{"SoftDelete", true},
		{"HardDelete", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()

			s := createTestService(t, db, WithSoftDelete(tt.softDelete))
			ctx := context.Background()
			userKey := session.UserKey{AppName: "test-app", UserID: "user-123"}
			key := "pref1"

			if tt.softDelete {
				mock.ExpectExec(regexp.QuoteMeta("UPDATE user_states SET deleted_at = ?")).
					WithArgs(sqlmock.AnyArg(), userKey.AppName, userKey.UserID, key).
					WillReturnResult(sqlmock.NewResult(0, 1))
			} else {
				mock.ExpectExec(regexp.QuoteMeta("DELETE FROM user_states")).
					WithArgs(userKey.AppName, userKey.UserID, key).
					WillReturnResult(sqlmock.NewResult(0, 1))
			}

			err = s.DeleteUserState(ctx, userKey, key)
			assert.NoError(t, err)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestCleanupExpiredSessions_QueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db, WithSoftDelete(true), WithSessionTTL(time.Hour))

	ctx := context.Background()
	now := time.Now()

	mock.ExpectQuery(regexp.QuoteMeta(
		"SELECT app_name, user_id, session_id, " +
			"MAX(updated_at) as updated_at FROM session_events",
	)).
		WillReturnError(assert.AnError)

	s.cleanupExpiredSessions(ctx, now)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCleanupExpiredStates(t *testing.T) {
	tests := []struct {
		name       string
		stateType  string
		softDelete bool
		ttl        time.Duration
	}{
		{"AppStates_SoftDelete", "app", true, 24 * time.Hour},
		{"AppStates_HardDelete", "app", false, 24 * time.Hour},
		{"UserStates_SoftDelete", "user", true, 24 * time.Hour},
		{"UserStates_HardDelete", "user", false, 24 * time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()

			opts := []ServiceOpt{WithSoftDelete(tt.softDelete)}
			if tt.stateType == "app" {
				opts = append(opts, WithAppStateTTL(tt.ttl))
			} else {
				opts = append(opts, WithUserStateTTL(tt.ttl))
			}

			s := createTestService(t, db, opts...)
			ctx := context.Background()
			now := time.Now()

			table := tt.stateType + "_states"
			if tt.softDelete {
				mock.ExpectExec(regexp.QuoteMeta("UPDATE " + table + " SET deleted_at = ?")).
					WillReturnResult(sqlmock.NewResult(0, 2))
			} else {
				mock.ExpectExec(regexp.QuoteMeta("DELETE FROM " + table)).
					WillReturnResult(sqlmock.NewResult(0, 2))
			}

			if tt.stateType == "app" {
				s.cleanupExpiredAppStates(ctx, now)
			} else {
				s.cleanupExpiredUserStates(ctx, now)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestCleanupExpiredForUser(t *testing.T) {
	tests := []struct {
		name       string
		softDelete bool
	}{
		{"SoftDelete", true},
		{"HardDelete", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()

			s := createTestService(t, db, WithSoftDelete(tt.softDelete))
			ctx := context.Background()
			userKey := session.UserKey{AppName: "test-app", UserID: "user-123"}

			if tt.softDelete {
				mock.ExpectExec(
					regexp.QuoteMeta(
						"UPDATE session_states SET deleted_at = ?",
					),
				).
					WithArgs(
						sqlmock.AnyArg(),
						userKey.AppName,
						userKey.UserID,
						sqlmock.AnyArg(),
					).
					WillReturnResult(sqlmock.NewResult(0, 1))
			} else {
				mock.ExpectExec(
					regexp.QuoteMeta("DELETE FROM session_states"),
				).
					WithArgs(
						userKey.AppName,
						userKey.UserID,
						sqlmock.AnyArg(),
					).
					WillReturnResult(sqlmock.NewResult(0, 1))
			}

			s.cleanupExpiredForUser(ctx, userKey)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestCleanupExpiredForUser_ExecError(t *testing.T) {
	tests := []struct {
		name       string
		softDelete bool
	}{
		{"SoftDeleteExecError", true},
		{"HardDeleteExecError", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()

			s := createTestService(t, db, WithSoftDelete(tt.softDelete))
			ctx := context.Background()
			userKey := session.UserKey{
				AppName: "test-app",
				UserID:  "user-123",
			}

			if tt.softDelete {
				mock.ExpectExec(
					regexp.QuoteMeta(
						"UPDATE session_states SET deleted_at = ?",
					),
				).
					WithArgs(
						sqlmock.AnyArg(),
						userKey.AppName,
						userKey.UserID,
						sqlmock.AnyArg(),
					).
					WillReturnError(assert.AnError)
			} else {
				mock.ExpectExec(
					regexp.QuoteMeta("DELETE FROM session_states"),
				).
					WithArgs(
						userKey.AppName,
						userKey.UserID,
						sqlmock.AnyArg(),
					).
					WillReturnError(assert.AnError)
			}

			s.cleanupExpiredForUser(ctx, userKey)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestAppendEvent_Async(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	// Enable async persist
	s := createTestService(t, db, WithSessionTTL(1*time.Hour), WithEnableAsyncPersist(true), WithAsyncPersisterNum(1))

	// Start async workers
	s.startAsyncPersistWorker()
	defer func() {
		for _, ch := range s.eventPairChans {
			close(ch)
		}
	}()

	ctx := context.Background()

	sess := &session.Session{
		ID:      "sess-123",
		AppName: "test-app",
		UserID:  "user-456",
		State:   session.StateMap{"existing": []byte(`"state"`)},
		Events:  []event.Event{},
	}

	// Create test event
	evt := event.New("inv-123", "test-author")
	evt.Response = &model.Response{
		Object: model.ObjectTypeChatCompletion,
		Done:   true,
		Choices: []model.Choice{
			{
				Index: 0,
				Message: model.Message{
					Content: "test response",
				},
			},
		},
	}
	evt.IsPartial = false

	// Mock: Get current session state (will be called by async worker)
	sessState := SessionState{
		ID:    sess.ID,
		State: sess.State,
	}
	stateBytes, _ := json.Marshal(sessState)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT state, expires_at FROM session_states")).
		WithArgs(sess.AppName, sess.UserID, sess.ID).
		WillReturnRows(sqlmock.NewRows([]string{"state", "expires_at"}).
			AddRow(stateBytes, nil))

	// Mock: Transaction for async worker
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE session_states SET state = ?, updated_at = ?, expires_at = ?")).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sess.AppName, sess.UserID, sess.ID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO session_events")).
		WithArgs(
			sess.AppName,
			sess.UserID,
			sess.ID,
			sqlmock.AnyArg(), // event (JSON)
			sqlmock.AnyArg(), // created_at
			sqlmock.AnyArg(), // updated_at
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = s.AppendEvent(ctx, sess, evt)
	assert.NoError(t, err)

	// Wait for async processing
	time.Sleep(50 * time.Millisecond)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateAppState_Error(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db, WithAppStateTTL(24*time.Hour))
	ctx := context.Background()

	appName := "test-app"
	state := session.StateMap{
		"key1": []byte(`"value1"`),
	}

	// Mock: UPSERT fails
	mock.ExpectExec(regexp.QuoteMeta("REPLACE INTO app_states")).
		WithArgs(
			appName,
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnError(fmt.Errorf("database error"))

	err = s.UpdateAppState(ctx, appName, state)
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteSession_Error(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db, WithSoftDelete(true))
	ctx := context.Background()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user-123",
		SessionID: "session-456",
	}

	// Mock: Transaction for soft delete fails
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE session_states SET deleted_at = ?")).
		WithArgs(sqlmock.AnyArg(), key.AppName, key.UserID, key.SessionID).
		WillReturnError(fmt.Errorf("database error"))
	mock.ExpectRollback()

	err = s.DeleteSession(ctx, key)
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteUserState_InvalidKey(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	userKey := session.UserKey{
		AppName: "", // Invalid: empty app name
		UserID:  "user-123",
	}
	key := "test-key"

	err = s.DeleteUserState(ctx, userKey, key)
	assert.Error(t, err)
}

func TestListUserStates_InvalidKey(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "", // Invalid: empty user ID
	}

	states, err := s.ListUserStates(ctx, userKey)
	assert.Error(t, err)
	assert.Nil(t, states)
}

func TestCreateSession_WithAutoGeneratedID(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db, WithSessionTTL(1*time.Hour))
	ctx := context.Background()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user-123",
		SessionID: "", // Empty session ID, should auto-generate
	}

	state := session.StateMap{"key1": []byte(`"value1"`)}

	// Mock: Check if session exists (will use auto-generated ID)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT expires_at FROM session_states")).
		WithArgs("test-app", "user-123", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"expires_at"}))

	// Mock: Insert new session
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO session_states")).
		WithArgs(
			"test-app",
			"user-123",
			sqlmock.AnyArg(), // session_id (auto-generated UUID)
			sqlmock.AnyArg(), // state (JSON)
			sqlmock.AnyArg(), // created_at
			sqlmock.AnyArg(), // updated_at
			sqlmock.AnyArg(), // expires_at
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// Mock: List app states
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM app_states")).
		WithArgs("test-app", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

	// Mock: List user states
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM user_states")).
		WithArgs("test-app", "user-123", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

	sess, err := s.CreateSession(ctx, key, state)
	require.NoError(t, err)
	assert.NotNil(t, sess)
	assert.NotEmpty(t, sess.ID) // Should have auto-generated ID
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateSessionState_UnmarshalSessionEnvelope(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "test-session",
	}
	createdAt := time.Date(2024, 12, 31, 12, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(time.Minute)

	existing := &SessionState{
		ID:        key.SessionID,
		State:     session.StateMap{"existing": []byte("old")},
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}
	stateBytes, err := json.Marshal(existing)
	require.NoError(t, err)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT state FROM session_states WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL")).
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(sqlmock.NewRows([]string{"state"}).AddRow(stateBytes))

	expectedState := session.StateMap{
		"existing": []byte("old"),
		"new":      []byte("fresh"),
	}
	mock.ExpectExec(regexp.QuoteMeta("UPDATE session_states SET state = ?, updated_at = ?, expires_at = ?")).
		WithArgs(
			&sessionStateJSONMatcher{
				t:             t,
				expectedID:    key.SessionID,
				expectedState: expectedState,
				createdAt:     createdAt,
				previousAt:    updatedAt,
			},
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			key.AppName,
			key.UserID,
			key.SessionID,
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = s.UpdateSessionState(ctx, key, session.StateMap{
		"new": []byte("fresh"),
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateSessionState_UnmarshalNilState(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "test-session",
	}
	createdAt := time.Date(2024, 11, 11, 11, 11, 11, 0, time.UTC)
	updatedAt := createdAt.Add(time.Minute)

	existing := &SessionState{
		ID:        key.SessionID,
		State:     nil,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}
	stateBytes, err := json.Marshal(existing)
	require.NoError(t, err)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT state FROM session_states WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL")).
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(sqlmock.NewRows([]string{"state"}).AddRow(stateBytes))

	expectedState := session.StateMap{
		"only": []byte("value"),
	}
	mock.ExpectExec(regexp.QuoteMeta("UPDATE session_states SET state = ?, updated_at = ?, expires_at = ?")).
		WithArgs(
			&sessionStateJSONMatcher{
				t:             t,
				expectedID:    key.SessionID,
				expectedState: expectedState,
				createdAt:     createdAt,
				previousAt:    updatedAt,
			},
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			key.AppName,
			key.UserID,
			key.SessionID,
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = s.UpdateSessionState(ctx, key, session.StateMap{
		"only": []byte("value"),
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateSessionState_UnmarshalError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "test-session",
	}

	mock.ExpectQuery(regexp.QuoteMeta("SELECT state FROM session_states WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL")).
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(sqlmock.NewRows([]string{"state"}).AddRow([]byte("{")))

	err = s.UpdateSessionState(ctx, key, session.StateMap{
		"k": []byte("v"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal state")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateSessionState_InvalidKey(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	err = s.UpdateSessionState(ctx, session.Key{}, session.StateMap{})
	require.Error(t, err)
}

func TestUpdateSessionState_InvalidPrefix(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	err = s.UpdateSessionState(ctx, key, session.StateMap{
		session.StateAppPrefix + "k": []byte("v"),
	})
	require.Error(t, err)

	err = s.UpdateSessionState(ctx, key, session.StateMap{
		session.StateUserPrefix + "k": []byte("v"),
	})
	require.Error(t, err)
}

func TestUpdateSessionState_SessionNotFound_ErrNoRows(
	t *testing.T,
) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}

	mock.ExpectQuery(regexp.QuoteMeta("SELECT state FROM session_states WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL")).
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(sqlmock.NewRows([]string{"state"}))

	err = s.UpdateSessionState(ctx, key, session.StateMap{"k": []byte("v")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session not found")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateSessionState_QueryError_Propagates(
	t *testing.T,
) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}

	mock.ExpectQuery(regexp.QuoteMeta("SELECT state FROM session_states WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL")).
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnError(fmt.Errorf("db error"))

	err = s.UpdateSessionState(ctx, key, session.StateMap{"k": []byte("v")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db error")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateSessionState_UpdateError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}

	stateBytes, err := json.Marshal(&SessionState{
		ID:        key.SessionID,
		State:     session.StateMap{"a": []byte("b")},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	})
	require.NoError(t, err)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT state FROM session_states WHERE app_name = ? AND user_id = ? AND session_id = ? AND deleted_at IS NULL")).
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(sqlmock.NewRows([]string{"state"}).AddRow(stateBytes))

	mock.ExpectExec(regexp.QuoteMeta("UPDATE session_states SET state = ?, updated_at = ?, expires_at = ?")).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), key.AppName, key.UserID, key.SessionID).
		WillReturnError(fmt.Errorf("update error"))

	err = s.UpdateSessionState(ctx, key, session.StateMap{"k": []byte("v")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update session state failed")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListSessions_WithEvents(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "user-123",
	}

	// Mock: List app states
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM app_states")).
		WithArgs(userKey.AppName, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

	// Mock: List user states
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM user_states")).
		WithArgs(userKey.AppName, userKey.UserID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

	// Prepare session state
	sess1 := SessionState{ID: "session-1", State: session.StateMap{}}
	state1Bytes, _ := json.Marshal(sess1)

	// Mock: Query session states
	mock.ExpectQuery(regexp.QuoteMeta("SELECT session_id, state, created_at, updated_at FROM session_states")).
		WithArgs(userKey.AppName, userKey.UserID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"session_id", "state", "created_at", "updated_at"}).
			AddRow("session-1", state1Bytes, time.Now(), time.Now()))

	// Prepare mock event
	evt := event.New("inv-1", "author")
	evt.Response = &model.Response{
		Object: model.ObjectTypeChatCompletion,
		Choices: []model.Choice{
			{Index: 0, Message: model.Message{Role: model.RoleUser, Content: "response"}},
		},
	}
	eventBytes, _ := json.Marshal(evt)

	// Mock: Batch load events with data
	mock.ExpectQuery(regexp.QuoteMeta("SELECT app_name, user_id, session_id, event FROM session_events")).
		WillReturnRows(sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "event"}).
			AddRow(userKey.AppName, userKey.UserID, "session-1", eventBytes))

	// Prepare mock summary
	summary := session.Summary{Summary: "test summary", Topics: []string{}}
	summaryBytes, _ := json.Marshal(summary)

	// Mock: Batch load summaries with data
	mock.ExpectQuery(regexp.QuoteMeta("SELECT app_name, user_id, session_id, filter_key, summary FROM session_summaries")).
		WillReturnRows(sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "filter_key", "summary"}).
			AddRow(userKey.AppName, userKey.UserID, "session-1", "", summaryBytes))

	sessions, err := s.ListSessions(ctx, userKey)
	require.NoError(t, err)
	assert.Len(t, sessions, 1)
	assert.Len(t, sessions[0].Events, 1)
	assert.Len(t, sessions[0].Summaries, 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSession_WithEvents(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user-123",
		SessionID: "session-456",
	}

	// Prepare mock session state
	sessState := SessionState{
		ID:        key.SessionID,
		State:     session.StateMap{},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	stateBytes, _ := json.Marshal(sessState)

	// Mock: Query session
	mock.ExpectQuery(regexp.QuoteMeta("SELECT state, created_at, updated_at FROM session_states")).
		WithArgs(key.AppName, key.UserID, key.SessionID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"state", "created_at", "updated_at"}).
			AddRow(stateBytes, sessState.CreatedAt, sessState.UpdatedAt))

	// Mock: List app states
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM app_states")).
		WithArgs(key.AppName, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

	// Mock: List user states
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM user_states")).
		WithArgs(key.AppName, key.UserID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

	// Prepare multiple mock events
	evt1 := event.New("inv-1", "author")
	evt1.Response = &model.Response{
		Object: model.ObjectTypeChatCompletion,
		Choices: []model.Choice{
			{Index: 0, Message: model.Message{Role: model.RoleUser, Content: "response1"}},
		},
	}
	event1Bytes, _ := json.Marshal(evt1)

	evt2 := event.New("inv-2", "author")
	evt2.Response = &model.Response{Object: model.ObjectTypeChatCompletion}
	event2Bytes, _ := json.Marshal(evt2)

	// Mock: Query events (multiple rows)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT app_name, user_id, session_id, event FROM session_events")).
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "event"}).
			AddRow(key.AppName, key.UserID, key.SessionID, event1Bytes).
			AddRow(key.AppName, key.UserID, key.SessionID, event2Bytes))

	// Prepare multiple summaries
	sum1 := session.Summary{Summary: "summary1", Topics: []string{}}
	sum1Bytes, _ := json.Marshal(sum1)
	sum2 := session.Summary{Summary: "summary2", Topics: []string{}}
	sum2Bytes, _ := json.Marshal(sum2)

	// Mock: Query summaries (multiple rows)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT app_name, user_id, session_id, filter_key, summary FROM session_summaries")).
		WithArgs(key.AppName, key.UserID, key.SessionID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "filter_key", "summary"}).
			AddRow(key.AppName, key.UserID, key.SessionID, "filter1", sum1Bytes).
			AddRow(key.AppName, key.UserID, key.SessionID, "filter2", sum2Bytes))

	sess, err := s.GetSession(ctx, key)
	require.NoError(t, err)
	assert.NotNil(t, sess)
	assert.Len(t, sess.Events, 2)
	assert.Len(t, sess.Summaries, 2)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateSession_ExistingExpired(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db, WithSessionTTL(1*time.Hour))
	ctx := context.Background()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user-123",
		SessionID: "session-456",
	}

	state := session.StateMap{"key1": []byte(`"value1"`)}

	// Mock: Session exists but expired
	expiredTime := time.Now().Add(-2 * time.Hour)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT expires_at FROM session_states")).
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(sqlmock.NewRows([]string{"expires_at"}).AddRow(expiredTime))

	// Mock: Cleanup expired sessions for user (soft delete)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE session_states SET deleted_at = ?")).
		WithArgs(sqlmock.AnyArg(), key.AppName, key.UserID, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	// Mock: Insert new session
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO session_states")).
		WithArgs(
			key.AppName,
			key.UserID,
			key.SessionID,
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// Mock: List app states
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM app_states")).
		WithArgs(key.AppName, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

	// Mock: List user states
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM user_states")).
		WithArgs(key.AppName, key.UserID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

	sess, err := s.CreateSession(ctx, key, state)
	require.NoError(t, err)
	assert.NotNil(t, sess)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateSession_ExistingNotExpired(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user-123",
		SessionID: "session-456",
	}

	// Mock: Session exists with NULL expires_at (never expires)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT expires_at FROM session_states")).
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(sqlmock.NewRows([]string{"expires_at"}).AddRow(nil))

	sess, err := s.CreateSession(ctx, key, session.StateMap{})
	assert.Error(t, err)
	assert.Nil(t, sess)
	assert.Contains(t, err.Error(), "session already exists")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateUserState_Error(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "user-123",
	}

	state := session.StateMap{
		"key1": []byte(`"value1"`),
	}

	// Mock: UPSERT fails
	mock.ExpectExec(regexp.QuoteMeta("REPLACE INTO user_states")).
		WithArgs(
			userKey.AppName,
			userKey.UserID,
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnError(fmt.Errorf("database error"))

	err = s.UpdateUserState(ctx, userKey, state)
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListUserStates_Error(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "user-123",
	}

	// Mock: Query fails
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM user_states")).
		WithArgs(userKey.AppName, userKey.UserID, sqlmock.AnyArg()).
		WillReturnError(fmt.Errorf("database error"))

	states, err := s.ListUserStates(ctx, userKey)
	assert.Error(t, err)
	assert.Nil(t, states)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteUserState_Error(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db, WithSoftDelete(true))
	ctx := context.Background()

	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "user-123",
	}
	key := "test-key"

	// Mock: Soft delete fails
	mock.ExpectExec(regexp.QuoteMeta("UPDATE user_states SET deleted_at = ?")).
		WithArgs(sqlmock.AnyArg(), userKey.AppName, userKey.UserID, key).
		WillReturnError(fmt.Errorf("database error"))

	err = s.DeleteUserState(ctx, userKey, key)
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestOptions_Coverage(t *testing.T) {
	// Test option functions that aren't covered elsewhere
	opts := ServiceOpts{}

	WithMySQLClientDSN("test-dsn")(&opts)
	assert.Equal(t, "test-dsn", opts.dsn)

	WithMySQLInstance("test-instance")(&opts)
	assert.Equal(t, "test-instance", opts.instanceName)

	WithExtraOptions()(&opts)
	assert.Empty(t, opts.extraOptions)

	WithEnableAsyncPersist(true)(&opts)
	assert.True(t, opts.enableAsyncPersist)

	WithAsyncPersisterNum(5)(&opts)
	assert.Equal(t, 5, opts.asyncPersisterNum)

	WithAsyncSummaryNum(3)(&opts)
	assert.Equal(t, 3, opts.asyncSummaryNum)

	WithSummaryQueueSize(100)(&opts)
	assert.Equal(t, 100, opts.summaryQueueSize)

	WithSummaryJobTimeout(5 * time.Second)(&opts)
	assert.Equal(t, 5*time.Second, opts.summaryJobTimeout)

	WithCleanupInterval(10 * time.Minute)(&opts)
	assert.Equal(t, 10*time.Minute, opts.cleanupInterval)

	WithSkipDBInit(true)(&opts)
	assert.True(t, opts.skipDBInit)

	WithTablePrefix("test_")(&opts)
	assert.Equal(t, "test_", opts.tablePrefix)
}

func TestApplyOptions(t *testing.T) {
	// Test applyOptions function
	opt := applyOptions(
		session.WithEventNum(10),
		session.WithEventTime(time.Now().Add(-1*time.Hour)),
	)

	assert.Equal(t, 10, opt.EventNum)
	assert.True(t, opt.EventTime.Before(time.Now()))
}

func TestClose(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)

	// Initialize channels
	s.eventPairChans = []chan *sessionEventPair{make(chan *sessionEventPair)}
	s.summaryJobChans = []chan *summaryJob{make(chan *summaryJob)}
	s.cleanupDone = make(chan struct{})

	// Call Close (no mock expectations needed since mockMySQLClient.Close() does nothing)
	err = s.Close()
	assert.NoError(t, err)

	// Verify channels are closed by checking if they're nil or closed
	// We can't directly check if a channel is closed, but we can verify the cleanup was called
}

func TestGetSession_InvalidKey(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user-123",
		SessionID: "", // Invalid: empty session ID
	}

	sess, err := s.GetSession(ctx, key)
	assert.Error(t, err)
	assert.Nil(t, sess)
}

func TestGetSession_WithAfterTime(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user-123",
		SessionID: "session-456",
	}

	// Prepare mock session state
	sessState := SessionState{
		ID:        key.SessionID,
		State:     session.StateMap{},
		CreatedAt: time.Now().Add(-2 * time.Hour),
		UpdatedAt: time.Now(),
	}
	stateBytes, _ := json.Marshal(sessState)

	// Mock: Query session
	mock.ExpectQuery(regexp.QuoteMeta("SELECT state, created_at, updated_at FROM session_states")).
		WithArgs(key.AppName, key.UserID, key.SessionID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"state", "created_at", "updated_at"}).
			AddRow(stateBytes, sessState.CreatedAt, sessState.UpdatedAt))

	// Mock: List app states
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM app_states")).
		WithArgs(key.AppName, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

	// Mock: List user states
	mock.ExpectQuery(regexp.QuoteMeta("SELECT `key`, value FROM user_states")).
		WithArgs(key.AppName, key.UserID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

	// Mock: Query events with afterTime filter (should filter events)
	afterTime := time.Now().Add(-1 * time.Hour)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT app_name, user_id, session_id, event FROM session_events")).
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "event"}))

	sess, err := s.GetSession(ctx, key, session.WithEventTime(afterTime))
	require.NoError(t, err)
	assert.NotNil(t, sess)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestAppendEvent_AsyncPath tests async persist path
func TestAppendEvent_AsyncPath(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db, WithEnableAsyncPersist(true), WithAsyncPersisterNum(2))
	s.startAsyncPersistWorker()
	defer s.Close()

	ctx := context.Background()
	sess := &session.Session{
		AppName: "test-app",
		UserID:  "user-123",
		ID:      "session-456",
		Events:  make([]event.Event, 0),
		State:   make(session.StateMap),
	}

	evt := event.New("inv-1", "test-author")
	evt.Response = &model.Response{
		Object: model.ObjectTypeChatCompletion,
		Done:   true,
		Choices: []model.Choice{
			{
				Index: 0,
				Message: model.Message{
					Content: "test response",
				},
			},
		},
	}

	// Should not block - async path
	err = s.AppendEvent(ctx, sess, evt)
	assert.NoError(t, err)

	// Wait a bit for async processing
	time.Sleep(50 * time.Millisecond)
}

// TestCleanupExpiredData tests cleanup of all expired data
func TestCleanupExpiredData(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db,
		WithSessionTTL(1*time.Hour),
		WithAppStateTTL(2*time.Hour),
		WithUserStateTTL(3*time.Hour),
		WithSoftDelete(true),
	)

	ctx := context.Background()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT app_name, user_id, session_id, MAX(updated_at) as updated_at FROM session_events")).
		WillReturnRows(sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "updated_at"}).
			AddRow("session-1", "app-1", "user-1", time.Now().Add(-48*time.Hour)))

	// Mock cleanup sessions in transaction (including related events and summaries)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE session_states SET deleted_at = ?")).
		WillReturnResult(sqlmock.NewResult(0, 5))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE session_summaries SET deleted_at = ?")).
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE session_events SET deleted_at = ?")).
		WillReturnResult(sqlmock.NewResult(0, 10))
	mock.ExpectCommit()

	// Mock cleanup app states
	mock.ExpectExec(regexp.QuoteMeta("UPDATE app_states SET deleted_at = ?")).
		WillReturnResult(sqlmock.NewResult(0, 3))

	// Mock cleanup user states
	mock.ExpectExec(regexp.QuoteMeta("UPDATE user_states SET deleted_at = ?")).
		WillReturnResult(sqlmock.NewResult(0, 2))

	s.cleanupExpiredData(ctx)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestCleanupExpiredSessions_HardDelete tests hard delete of sessions
func TestCleanupExpiredSessions_HardDelete(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db, WithSessionTTL(1*time.Hour), WithSoftDelete(false))
	ctx := context.Background()
	now := time.Now()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT app_name, user_id, session_id, MAX(updated_at) as updated_at FROM session_events")).
		WillReturnRows(sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "updated_at"}).
			AddRow("session-1", "app-1", "user-1", time.Now().Add(-48*time.Hour)))

	// Mock hard delete sessions, events, and summaries in transaction
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM session_states WHERE expires_at IS NOT NULL AND expires_at <= ?")).
		WithArgs(now).
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM session_summaries WHERE expires_at IS NOT NULL AND expires_at <= ?")).
		WithArgs(now).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM session_events WHERE")).
		WillReturnResult(sqlmock.NewResult(0, 5))
	mock.ExpectCommit()

	s.cleanupExpiredSessions(ctx, now)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestDeleteAppState_HardDelete tests hard delete of app state
func TestDeleteAppState_HardDelete(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db, WithSoftDelete(false))
	ctx := context.Background()

	// Mock hard delete
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM app_states WHERE app_name = ? AND `key` = ?")).
		WithArgs("test-app", "test-key").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = s.DeleteAppState(ctx, "test-app", "test-key")
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestDeleteAppState_EmptyKey tests error handling for empty key
func TestDeleteAppState_EmptyKey(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	err = s.DeleteAppState(ctx, "test-app", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "state key is required")
}

// TestDeleteSession_HardDelete tests hard delete of session
func TestDeleteSession_HardDelete(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db, WithSoftDelete(false))
	ctx := context.Background()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user-123",
		SessionID: "session-456",
	}

	// Mock transaction
	mock.ExpectBegin()

	// Mock hard delete session state
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM session_states WHERE app_name = ? AND user_id = ? AND session_id = ?")).
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	// Mock hard delete session summaries
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM session_summaries WHERE app_name = ? AND user_id = ? AND session_id = ?")).
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnResult(sqlmock.NewResult(0, 0))

	// Mock hard delete session events
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM session_events WHERE app_name = ? AND user_id = ? AND session_id = ?")).
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnResult(sqlmock.NewResult(0, 0))

	mock.ExpectCommit()

	err = s.DeleteSession(ctx, key)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestCleanupExpiredAppStates_HardDelete tests hard delete of app states
func TestCleanupExpiredAppStates_HardDelete(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db, WithAppStateTTL(1*time.Hour), WithSoftDelete(false))
	ctx := context.Background()
	now := time.Now()

	// Mock hard delete app states
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM app_states WHERE expires_at IS NOT NULL AND expires_at <= ?")).
		WithArgs(now).
		WillReturnResult(sqlmock.NewResult(0, 2))

	s.cleanupExpiredAppStates(ctx, now)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestCleanupExpiredUserStates_HardDelete tests hard delete of user states
func TestCleanupExpiredUserStates_HardDelete(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db, WithUserStateTTL(1*time.Hour), WithSoftDelete(false))
	ctx := context.Background()
	now := time.Now()

	// Mock hard delete user states
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM user_states WHERE expires_at IS NOT NULL AND expires_at <= ?")).
		WithArgs(now).
		WillReturnResult(sqlmock.NewResult(0, 2))

	s.cleanupExpiredUserStates(ctx, now)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestNewService_WithDSN_Success(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	defer db.Close()

	// Save original builder
	originalBuilder := storage.GetClientBuilder()
	defer storage.SetClientBuilder(originalBuilder)

	// Set custom builder that returns our mock
	storage.SetClientBuilder(func(builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return &mockMySQLClient{db: db}, nil
	})

	// Mock database initialization
	mockDBInit(mock)

	svc, err := NewService(
		WithMySQLClientDSN("test:test@tcp(localhost:3306)/testdb"),
		WithSessionTTL(1*time.Hour),
	)
	require.NoError(t, err)
	require.NotNil(t, svc)

	// Verify service configuration
	assert.Equal(t, 1*time.Hour, svc.opts.sessionTTL)
	assert.Equal(t, defaultSessionEventLimit, svc.opts.sessionEventLimit)
	assert.Equal(t, defaultAsyncPersisterNum, svc.opts.asyncPersisterNum)
	assert.True(t, svc.opts.softDelete)

	// Verify table names
	assert.Equal(t, "session_states", svc.tableSessionStates)
	assert.Equal(t, "session_events", svc.tableSessionEvents)
	assert.Equal(t, "session_summaries", svc.tableSessionSummaries)
	assert.Equal(t, "app_states", svc.tableAppStates)
	assert.Equal(t, "user_states", svc.tableUserStates)

	// Clean up
	err = svc.Close()
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestNewService_WithTablePrefix(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	defer db.Close()

	originalBuilder := storage.GetClientBuilder()
	defer storage.SetClientBuilder(originalBuilder)

	storage.SetClientBuilder(func(builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return &mockMySQLClient{db: db}, nil
	})

	mockDBInit(mock)

	svc, err := NewService(
		WithMySQLClientDSN("test:test@tcp(localhost:3306)/testdb"),
		WithTablePrefix("test_"),
	)
	require.NoError(t, err)
	require.NotNil(t, svc)

	// Verify table names with prefix
	assert.Equal(t, "test_session_states", svc.tableSessionStates)
	assert.Equal(t, "test_session_events", svc.tableSessionEvents)
	assert.Equal(t, "test_session_summaries", svc.tableSessionSummaries)
	assert.Equal(t, "test_app_states", svc.tableAppStates)
	assert.Equal(t, "test_user_states", svc.tableUserStates)

	err = svc.Close()
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestNewService_WithSkipDBInit(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	defer db.Close()

	originalBuilder := storage.GetClientBuilder()
	defer storage.SetClientBuilder(originalBuilder)

	storage.SetClientBuilder(func(builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return &mockMySQLClient{db: db}, nil
	})

	// No other mock expectations because DB init should be skipped

	svc, err := NewService(
		WithMySQLClientDSN("test:test@tcp(localhost:3306)/testdb"),
		WithSkipDBInit(true),
	)
	require.NoError(t, err)
	require.NotNil(t, svc)

	err = svc.Close()
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestNewService_MissingDSNAndInstance(t *testing.T) {
	svc, err := NewService()
	assert.Error(t, err)
	assert.Nil(t, svc)
	assert.Contains(t, err.Error(), "create mysql client failed")
}

func TestNewService_WithInstance_Success(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	defer db.Close()

	originalBuilder := storage.GetClientBuilder()
	defer storage.SetClientBuilder(originalBuilder)

	storage.SetClientBuilder(func(builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return &mockMySQLClient{db: db}, nil
	})

	// Register instance
	instanceName := "test-instance-success"
	storage.RegisterMySQLInstance(instanceName,
		storage.WithClientBuilderDSN("test:test@tcp(localhost:3306)/testdb"),
	)

	mockDBInit(mock)

	svc, err := NewService(
		WithMySQLInstance(instanceName),
	)
	require.NoError(t, err)
	require.NotNil(t, svc)

	err = svc.Close()
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestNewService_InstanceNotFound(t *testing.T) {
	svc, err := NewService(
		WithMySQLInstance("non-existent-instance"),
	)
	assert.Error(t, err)
	assert.Nil(t, svc)
	assert.Contains(t, err.Error(), "mysql instance")
	assert.Contains(t, err.Error(), "not found")
}

func TestNewService_ClientBuilderError(t *testing.T) {
	originalBuilder := storage.GetClientBuilder()
	defer storage.SetClientBuilder(originalBuilder)

	// Set a builder that always returns error
	storage.SetClientBuilder(func(builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return nil, assert.AnError
	})

	// Test with DSN
	svc, err := NewService(
		WithMySQLClientDSN("test:test@tcp(localhost:3306)/testdb"),
	)
	assert.Error(t, err)
	assert.Nil(t, svc)
	assert.Contains(t, err.Error(), "create mysql client failed")

	// Test with instance name
	storage.RegisterMySQLInstance("test-error-instance",
		storage.WithClientBuilderDSN("test:test@tcp(localhost:3306)/testdb"),
	)

	svc, err = NewService(
		WithMySQLInstance("test-error-instance"),
	)
	assert.Error(t, err)
	assert.Nil(t, svc)
	assert.Contains(t, err.Error(), "create mysql client failed")
}

func TestNewService_DBInitFailure(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	defer db.Close()

	originalBuilder := storage.GetClientBuilder()
	defer storage.SetClientBuilder(originalBuilder)

	storage.SetClientBuilder(func(builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return &mockMySQLClient{db: db}, nil
	})

	// Mock DB init failure
	mock.ExpectExec("CREATE TABLE").WillReturnError(assert.AnError)

	svc, err := NewService(
		WithMySQLClientDSN("test:test@tcp(localhost:3306)/testdb"),
	)
	assert.Error(t, err)
	assert.Nil(t, svc)
	assert.Contains(t, err.Error(), "init database failed")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestNewService_WithAsyncPersist(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	defer db.Close()

	originalBuilder := storage.GetClientBuilder()
	defer storage.SetClientBuilder(originalBuilder)

	storage.SetClientBuilder(func(builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return &mockMySQLClient{db: db}, nil
	})

	mockDBInit(mock)

	svc, err := NewService(
		WithMySQLClientDSN("test:test@tcp(localhost:3306)/testdb"),
		WithEnableAsyncPersist(true),
		WithAsyncPersisterNum(5),
	)
	require.NoError(t, err)
	require.NotNil(t, svc)

	// Verify async persist is enabled
	assert.True(t, svc.opts.enableAsyncPersist)
	assert.Equal(t, 5, svc.opts.asyncPersisterNum)
	assert.NotNil(t, svc.eventPairChans)
	assert.Len(t, svc.eventPairChans, 5)

	err = svc.Close()
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestNewService_WithCleanupRoutine(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	defer db.Close()

	originalBuilder := storage.GetClientBuilder()
	defer storage.SetClientBuilder(originalBuilder)

	storage.SetClientBuilder(func(builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return &mockMySQLClient{db: db}, nil
	})

	mockDBInit(mock)

	svc, err := NewService(
		WithMySQLClientDSN("test:test@tcp(localhost:3306)/testdb"),
		WithSessionTTL(1*time.Hour),
		WithAppStateTTL(2*time.Hour),
		WithUserStateTTL(3*time.Hour),
		WithCleanupInterval(10*time.Minute),
	)
	require.NoError(t, err)
	require.NotNil(t, svc)

	// Verify cleanup routine is started
	assert.NotNil(t, svc.cleanupTicker)
	assert.NotNil(t, svc.cleanupDone)

	err = svc.Close()
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestNewService_WithAllOptions(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	defer db.Close()

	originalBuilder := storage.GetClientBuilder()
	defer storage.SetClientBuilder(originalBuilder)

	storage.SetClientBuilder(func(builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return &mockMySQLClient{db: db}, nil
	})

	mockDBInit(mock)

	svc, err := NewService(
		WithMySQLClientDSN("test:test@tcp(localhost:3306)/testdb"),
		WithSessionEventLimit(500),
		WithSessionTTL(1*time.Hour),
		WithAppStateTTL(2*time.Hour),
		WithUserStateTTL(3*time.Hour),
		WithEnableAsyncPersist(true),
		WithAsyncPersisterNum(3),
		WithAsyncSummaryNum(2),
		WithSummaryQueueSize(128),
		WithSummaryJobTimeout(30*time.Second),
		WithSoftDelete(false),
		WithCleanupInterval(5*time.Minute),
		WithTablePrefix("myapp_"),
	)
	require.NoError(t, err)
	require.NotNil(t, svc)

	// Verify all options
	assert.Equal(t, 500, svc.opts.sessionEventLimit)
	assert.Equal(t, 1*time.Hour, svc.opts.sessionTTL)
	assert.Equal(t, 2*time.Hour, svc.opts.appStateTTL)
	assert.Equal(t, 3*time.Hour, svc.opts.userStateTTL)
	assert.True(t, svc.opts.enableAsyncPersist)
	assert.Equal(t, 3, svc.opts.asyncPersisterNum)
	assert.Equal(t, 2, svc.opts.asyncSummaryNum)
	assert.Equal(t, 128, svc.opts.summaryQueueSize)
	assert.Equal(t, 30*time.Second, svc.opts.summaryJobTimeout)
	assert.False(t, svc.opts.softDelete)
	assert.Equal(t, 5*time.Minute, svc.opts.cleanupInterval)

	// Verify table names with prefix
	assert.Equal(t, "myapp_session_states", svc.tableSessionStates)

	err = svc.Close()
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// mockDBInit mocks the database initialization process
func mockDBInit(mock sqlmock.Sqlmock) {
	// Mock: Create 5 tables
	for i := 0; i < 5; i++ {
		mock.ExpectExec("CREATE TABLE").WillReturnResult(sqlmock.NewResult(0, 0))
	}

	// Mock: Create 10 indexes
	for i := 0; i < 10; i++ {
		mock.ExpectExec("CREATE").WillReturnResult(sqlmock.NewResult(0, 0))
	}
}

// TestUpdateUserState_InvalidKey tests update user state with invalid key
func TestUpdateUserState_InvalidKey(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	userKey := session.UserKey{
		AppName: "", // Invalid: empty app name
		UserID:  "user-123",
	}
	state := session.StateMap{"key1": []byte("value1")}

	err = s.UpdateUserState(ctx, userKey, state)
	assert.Error(t, err)
}

// TestCreateSession_InvalidAppName tests creating session with invalid app name
func TestCreateSession_InvalidAppName(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	key := session.Key{
		AppName:   "", // Invalid: empty app name
		UserID:    "user-123",
		SessionID: "session-456",
	}

	_, err = s.CreateSession(ctx, key, session.StateMap{})
	assert.Error(t, err)
}

func TestAppendEventHook(t *testing.T) {
	t.Run("hook modifies event before storage (skip db)", func(t *testing.T) {
		db, _, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		hookCalled := false
		s := createTestService(t, db,
			WithAppendEventHook(func(ctx *session.AppendEventContext, next func() error) error {
				hookCalled = true
				ctx.Event.Tag = "hook_processed"
				return nil // abort before DB
			}),
		)

		ctx := context.Background()
		key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
		sess := session.NewSession(key.AppName, key.UserID, key.SessionID)

		evt := event.New("inv1", "assistant")
		evt.Response = &model.Response{
			Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "Hello"}}},
		}

		err = s.AppendEvent(ctx, sess, evt)
		require.NoError(t, err)
		assert.True(t, hookCalled)
		assert.Equal(t, "hook_processed", evt.Tag)
	})

	t.Run("hook can abort event storage", func(t *testing.T) {
		db, _, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		s := createTestService(t, db,
			WithAppendEventHook(func(ctx *session.AppendEventContext, next func() error) error {
				return nil // skip next()
			}),
		)

		ctx := context.Background()
		key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
		sess := session.NewSession(key.AppName, key.UserID, key.SessionID)

		evt := event.New("inv1", "assistant")
		evt.Response = &model.Response{
			Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "Hello"}}},
		}

		// No DB expectations - hook aborts before DB call
		err = s.AppendEvent(ctx, sess, evt)
		require.NoError(t, err)
	})

	t.Run("multiple hooks execute in order", func(t *testing.T) {
		db, _, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		order := []string{}
		s := createTestService(t, db,
			WithAppendEventHook(func(ctx *session.AppendEventContext, next func() error) error {
				order = append(order, "hook1_before")
				err := next()
				order = append(order, "hook1_after")
				return err
			}),
			WithAppendEventHook(func(ctx *session.AppendEventContext, next func() error) error {
				order = append(order, "hook2_before")
				err := next()
				order = append(order, "hook2_after")
				return err
			}),
		)

		ctx := context.Background()
		key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
		sess := session.NewSession(key.AppName, key.UserID, key.SessionID)

		evt := event.New("inv1", "assistant")
		evt.Response = &model.Response{
			Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "Hello"}}},
		}

		// Hook2 aborts, so no DB call
		_ = s.AppendEvent(ctx, sess, evt)
		assert.Equal(t, []string{"hook1_before", "hook2_before", "hook2_after", "hook1_after"}, order)
	})
}

func TestGetSessionHook(t *testing.T) {
	t.Run("hook returns custom session without db", func(t *testing.T) {
		db, _, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		hookCalled := false
		s := createTestService(t, db,
			WithGetSessionHook(func(ctx *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
				hookCalled = true
				custom := session.NewSession(ctx.Key.AppName, ctx.Key.UserID, ctx.Key.SessionID)
				custom.State["hook_added"] = []byte("true")
				return custom, nil
			}),
		)

		ctx := context.Background()
		key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}

		sess, err := s.GetSession(ctx, key)
		require.NoError(t, err)
		assert.True(t, hookCalled)
		require.NotNil(t, sess)
		assert.Equal(t, []byte("true"), sess.State["hook_added"])
	})

	t.Run("hook can return nil session", func(t *testing.T) {
		db, _, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		s := createTestService(t, db,
			WithGetSessionHook(func(ctx *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
				return nil, nil // skip next()
			}),
		)

		ctx := context.Background()
		key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}

		// No DB expectations - hook aborts before DB call
		sess, err := s.GetSession(ctx, key)
		require.NoError(t, err)
		assert.Nil(t, sess)
	})

	t.Run("multiple hooks execute in order", func(t *testing.T) {
		db, _, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		order := []string{}
		s := createTestService(t, db,
			WithGetSessionHook(func(ctx *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
				order = append(order, "hook1_before")
				sess, err := next()
				order = append(order, "hook1_after")
				return sess, err
			}),
			WithGetSessionHook(func(ctx *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
				order = append(order, "hook2_before")
				sess, err := next()
				order = append(order, "hook2_after")
				return sess, err
			}),
		)

		ctx := context.Background()
		key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}

		// Hook2 returns nil, so no DB call
		s.opts.getSessionHooks[1] = func(ctx *session.GetSessionContext, next func() (*session.Session, error)) (*session.Session, error) {
			order = append(order, "hook2_before")
			order = append(order, "hook2_after")
			return nil, nil
		}

		_, _ = s.GetSession(ctx, key)
		assert.Equal(t, []string{"hook1_before", "hook2_before", "hook2_after", "hook1_after"}, order)
	})
}

func TestUpdateSessionState_DisallowPrefixedKeys(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user-123",
		SessionID: "session-456",
	}

	stateWithAppPrefix := session.StateMap{
		session.StateAppPrefix + "foo": []byte("bar"),
	}
	err = s.UpdateSessionState(ctx, key, stateWithAppPrefix)
	require.Error(t, err)

	stateWithUserPrefix := session.StateMap{
		session.StateUserPrefix + "foo": []byte("bar"),
	}
	err = s.UpdateSessionState(ctx, key, stateWithUserPrefix)
	require.Error(t, err)
}

func TestUpdateSessionState_SessionNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user-123",
		SessionID: "session-456",
	}

	mock.ExpectQuery(
		regexp.QuoteMeta("SELECT state FROM session_states"),
	).
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnError(sql.ErrNoRows)

	err = s.UpdateSessionState(ctx, key, session.StateMap{
		"key1": []byte("value1"),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "session not found")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateSessionState_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db, WithSessionTTL(1*time.Hour))
	ctx := context.Background()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user-123",
		SessionID: "session-456",
	}

	currentState := session.StateMap{
		"existing": []byte(`"old"`),
	}
	currentBytes, err := json.Marshal(currentState)
	require.NoError(t, err)

	mock.ExpectQuery(
		regexp.QuoteMeta("SELECT state FROM session_states"),
	).
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(
			sqlmock.NewRows([]string{"state"}).AddRow(currentBytes),
		)

	mock.ExpectExec(
		regexp.QuoteMeta("UPDATE session_states SET state = ?, "+
			"updated_at = ?, expires_at = ?"),
	).
		WithArgs(
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			key.AppName,
			key.UserID,
			key.SessionID,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = s.UpdateSessionState(ctx, key, session.StateMap{
		"newKey": []byte(`"newValue"`),
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateSessionState_QueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user-123",
		SessionID: "session-456",
	}

	mock.ExpectQuery(
		regexp.QuoteMeta("SELECT state FROM session_states"),
	).
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnError(fmt.Errorf("query failed"))

	err = s.UpdateSessionState(ctx, key, session.StateMap{
		"key1": []byte("value1"),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "query failed")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateSessionState_UnmarshalError_InvalidJSON(
	t *testing.T,
) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "user-123",
		SessionID: "session-456",
	}

	mock.ExpectQuery(
		regexp.QuoteMeta("SELECT state FROM session_states"),
	).
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(
			sqlmock.NewRows([]string{"state"}).AddRow([]byte("not-json")),
		)

	err = s.UpdateSessionState(ctx, key, session.StateMap{
		"key1": []byte("value1"),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unmarshal state")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAppendEventInternal_AsyncPersistEnqueue(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db, WithEnableAsyncPersist(true))
	s.eventPairChans = []chan *sessionEventPair{
		make(chan *sessionEventPair, 1),
	}

	ctx := context.Background()
	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess",
	}
	sess := session.NewSession(key.AppName, key.UserID, key.SessionID)
	sess.Hash = 0

	evt := event.New("inv-1", "author")

	err = s.appendEventInternal(ctx, sess, evt, key)
	require.NoError(t, err)

	select {
	case pair := <-s.eventPairChans[0]:
		require.NotNil(t, pair)
		assert.Equal(t, key, pair.key)
		assert.Equal(t, evt, pair.event)
	default:
		t.Fatal("expected event to be enqueued")
	}
}

func TestAppendEventInternal_SendOnClosedChannel(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db, WithEnableAsyncPersist(true))
	s.eventPairChans = []chan *sessionEventPair{
		make(chan *sessionEventPair, 1),
	}
	close(s.eventPairChans[0])

	ctx := context.Background()
	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess",
	}
	sess := session.NewSession(key.AppName, key.UserID, key.SessionID)
	sess.Hash = 0

	evt := event.New("inv-1", "author")

	// Should not panic even though channel is closed.
	err = s.appendEventInternal(ctx, sess, evt, key)
	require.NoError(t, err)
}
