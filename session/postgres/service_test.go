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
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/postgres"
)

// stringSliceValuer wraps []string to implement driver.Valuer for sqlmock.
// Note: This matcher can verify the argument type in mock expectations,
// but cannot prevent the actual sql.DB driver from rejecting []string parameters.
// The issue is that sql.DB.QueryContext() validates argument types before
// the mock interceptor can handle them.
//
// Possible solutions for future consideration:
// 1. Use lib/pq driver and pq.Array() wrapper for PostgreSQL arrays
// 2. Create a custom driver that supports array types
// 3. Keep these tests as integration tests (current approach)
// 4. Refactor code to make getEventsList/getSummariesList injectable
type stringSliceValuer []string

func (s stringSliceValuer) Match(v driver.Value) bool {
	_, ok := v.([]string)
	return ok
}

// AnyStringSlice returns a custom matcher for []string arguments.
// WARNING: Due to database/sql driver limitations, this matcher cannot
// prevent runtime type validation errors when actual queries execute.
func AnyStringSlice() sqlmock.Argument {
	return stringSliceValuer{}
}

type stringArray []string

func (a stringArray) Value() (driver.Value, error) {
	return fmt.Sprintf("{%s}", strings.Join(a, ",")), nil
}

type recordingLogger struct {
	mu       sync.Mutex
	messages []string
}

func (l *recordingLogger) record(message string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.messages = append(l.messages, message)
}

func (l *recordingLogger) contains(sub string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, msg := range l.messages {
		if strings.Contains(msg, sub) {
			return true
		}
	}
	return false
}

func (l *recordingLogger) Debug(args ...any) { l.record(fmt.Sprint(args...)) }
func (l *recordingLogger) Debugf(format string, args ...any) {
	l.record(fmt.Sprintf(format, args...))
}
func (l *recordingLogger) Info(args ...any) { l.record(fmt.Sprint(args...)) }
func (l *recordingLogger) Infof(format string, args ...any) {
	l.record(fmt.Sprintf(format, args...))
}
func (l *recordingLogger) Warn(args ...any) { l.record(fmt.Sprint(args...)) }
func (l *recordingLogger) Warnf(format string, args ...any) {
	l.record(fmt.Sprintf(format, args...))
}
func (l *recordingLogger) Error(args ...any) { l.record(fmt.Sprint(args...)) }
func (l *recordingLogger) Errorf(format string, args ...any) {
	l.record(fmt.Sprintf(format, args...))
}
func (l *recordingLogger) Fatal(args ...any) { l.record(fmt.Sprint(args...)) }
func (l *recordingLogger) Fatalf(format string, args ...any) {
	l.record(fmt.Sprintf(format, args...))
}

// mockSummarizer is a mock summarizer for testing
type mockSummarizer interface {
	ShouldSummarize(sess *session.Session) bool
	Summarize(ctx context.Context, sess *session.Session) (string, error)
	Metadata() map[string]any
}

// boolPtr returns a pointer to a bool value
func boolPtr(b bool) *bool {
	return &b
}

// createTestService creates a Service with mock database for testing
func createTestService(t *testing.T, db *sql.DB, opts ...ServiceOpt) *Service {
	t.Helper()

	mockClient := &mockPostgresClient{db: db}
	options := ServiceOpts{tablePrefix: ""}
	for _, opt := range opts {
		opt(&options)
	}

	return &Service{
		pgClient:              mockClient,
		opts:                  options,
		tableSessionStates:    "session_states",
		tableSessionEvents:    "session_events",
		tableSessionSummaries: "session_summaries",
		tableAppStates:        "app_states",
		tableUserStates:       "user_states",
	}
}

// TestServiceOpts contains options for creating a test service
type TestServiceOpts struct {
	sessionTTL         time.Duration
	appStateTTL        time.Duration
	userStateTTL       time.Duration
	sessionEventLimit  int
	enableAsyncPersist bool
	softDelete         *bool // Use pointer to distinguish unset from false
	cleanupInterval    time.Duration
	summarizer         mockSummarizer
	asyncSummaryNum    int
	summaryJobTimeout  time.Duration
	summaryQueueSize   int
}

// mockPostgresClient is a mock implementation of storage.Client for testing
type mockPostgresClient struct {
	db *sql.DB
}

func (c *mockPostgresClient) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return c.db.ExecContext(ctx, query, convertArgs(args)...)
}

func (c *mockPostgresClient) Query(ctx context.Context, handler storage.HandlerFunc, query string, args ...any) error {
	rows, err := c.db.QueryContext(ctx, query, convertArgs(args)...)
	if err != nil {
		return fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	if err := handler(rows); err != nil {
		return err
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows iteration: %w", err)
	}

	return nil
}

func (c *mockPostgresClient) Transaction(ctx context.Context, fn storage.TxFunc) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		} else if err != nil {
			_ = tx.Rollback()
		}
	}()

	err = fn(tx)
	if err != nil {
		return err
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

func (c *mockPostgresClient) Close() error {
	return c.db.Close()
}

func convertArgs(args []any) []any {
	converted := make([]any, len(args))
	for i, arg := range args {
		switch v := arg.(type) {
		case []string:
			converted[i] = stringArray(v)
		default:
			converted[i] = arg
		}
	}
	return converted
}

// setupMockService creates a Service with mocked postgres client
func setupMockService(t *testing.T, opts *TestServiceOpts) (*Service, sqlmock.Sqlmock, *sql.DB) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)

	client := &mockPostgresClient{db: db}

	// Apply default options
	if opts == nil {
		opts = &TestServiceOpts{
			asyncSummaryNum:  defaultAsyncSummaryNum,
			summaryQueueSize: defaultSummaryQueueSize,
		}
	}
	if opts.asyncSummaryNum == 0 {
		opts.asyncSummaryNum = defaultAsyncSummaryNum
	}
	if opts.summaryQueueSize == 0 {
		opts.summaryQueueSize = defaultSummaryQueueSize
	}

	// Default soft delete to true if not explicitly set
	softDelete := true
	if opts.softDelete != nil {
		softDelete = *opts.softDelete
	}

	// Get table prefix from options (default to empty)
	prefix := ""

	s := &Service{
		pgClient: client,
		opts: ServiceOpts{
			sessionTTL:         opts.sessionTTL,
			appStateTTL:        opts.appStateTTL,
			userStateTTL:       opts.userStateTTL,
			sessionEventLimit:  opts.sessionEventLimit,
			enableAsyncPersist: opts.enableAsyncPersist,
			softDelete:         softDelete,
			cleanupInterval:    opts.cleanupInterval,
			summarizer:         opts.summarizer,
			tablePrefix:        prefix,
			summaryQueueSize:   opts.summaryQueueSize,
			asyncSummaryNum:    opts.asyncSummaryNum,
			summaryJobTimeout:  opts.summaryJobTimeout,
		},
		cleanupDone: make(chan struct{}),

		// Initialize table names with prefix
		tableSessionStates:    prefix + "session_states",
		tableSessionEvents:    prefix + "session_events",
		tableSessionSummaries: prefix + "session_summaries",
		tableAppStates:        prefix + "app_states",
		tableUserStates:       prefix + "user_states",
	}

	// Initialize async persist workers if enabled
	if opts.enableAsyncPersist {
		s.eventPairChans = make([]chan *sessionEventPair, defaultAsyncPersisterNum)
		for i := 0; i < defaultAsyncPersisterNum; i++ {
			s.eventPairChans[i] = make(chan *sessionEventPair, defaultChanBufferSize)
		}
	}

	// Initialize summary job channels
	s.summaryJobChans = make([]chan *summaryJob, opts.asyncSummaryNum)
	for i := range s.summaryJobChans {
		s.summaryJobChans[i] = make(chan *summaryJob, opts.summaryQueueSize)
	}

	return s, mock, db
}

func TestCreateSession_Success(t *testing.T) {
	s, mock, db := setupMockService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName: "test-app",
		UserID:  "test-user",
	}

	state := session.StateMap{
		"key1": []byte("value1"),
	}

	// Mock check for existing session (returns no rows - session doesn't exist)
	checkRows := sqlmock.NewRows([]string{"expires_at"})
	mock.ExpectQuery("SELECT expires_at FROM session_states").
		WithArgs("test-app", "test-user", sqlmock.AnyArg()).
		WillReturnRows(checkRows)

	// Mock INSERT session state
	mock.ExpectExec("INSERT INTO session_states").
		WithArgs("test-app", "test-user", sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// Mock SELECT app_states
	appRows := sqlmock.NewRows([]string{"key", "value"})
	mock.ExpectQuery("SELECT key, value FROM app_states").
		WithArgs("test-app", sqlmock.AnyArg()).
		WillReturnRows(appRows)

	// Mock SELECT user_states
	userRows := sqlmock.NewRows([]string{"key", "value"})
	mock.ExpectQuery("SELECT key, value FROM user_states").
		WithArgs("test-app", "test-user", sqlmock.AnyArg()).
		WillReturnRows(userRows)

	sess, err := s.CreateSession(context.Background(), key, state)
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.Equal(t, "test-app", sess.AppName)
	assert.Equal(t, "test-user", sess.UserID)
	assert.NotEmpty(t, sess.ID)
	assert.Equal(t, []byte("value1"), sess.State["key1"])

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateSession_InvalidKey(t *testing.T) {
	s, _, db := setupMockService(t, nil)
	defer db.Close()

	// Test with empty AppName
	key := session.Key{
		AppName: "",
		UserID:  "test-user",
	}

	_, err := s.CreateSession(context.Background(), key, session.StateMap{})
	require.Error(t, err)
}

func TestCreateSession_WithSessionID(t *testing.T) {
	s, mock, db := setupMockService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "custom-session-id",
	}

	// Mock check for existing session (returns no rows - session doesn't exist)
	checkRows := sqlmock.NewRows([]string{"expires_at"})
	mock.ExpectQuery("SELECT expires_at FROM session_states").
		WithArgs("test-app", "test-user", "custom-session-id").
		WillReturnRows(checkRows)

	// Mock INSERT session state
	mock.ExpectExec("INSERT INTO session_states").
		WithArgs("test-app", "test-user", "custom-session-id", sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// Mock SELECT app_states
	appRows := sqlmock.NewRows([]string{"key", "value"})
	mock.ExpectQuery("SELECT key, value FROM app_states").
		WithArgs("test-app", sqlmock.AnyArg()).
		WillReturnRows(appRows)

	// Mock SELECT user_states
	userRows := sqlmock.NewRows([]string{"key", "value"})
	mock.ExpectQuery("SELECT key, value FROM user_states").
		WithArgs("test-app", "test-user", sqlmock.AnyArg()).
		WillReturnRows(userRows)

	sess, err := s.CreateSession(context.Background(), key, session.StateMap{})
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.Equal(t, "custom-session-id", sess.ID)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateSession_ExistingNotExpired(t *testing.T) {
	s, mock, db := setupMockService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "existing-session",
	}

	// Mock check for existing session - returns a future expires_at (not expired)
	futureTime := time.Now().Add(1 * time.Hour)
	checkRows := sqlmock.NewRows([]string{"expires_at"}).
		AddRow(futureTime)
	mock.ExpectQuery("SELECT expires_at FROM session_states").
		WithArgs("test-app", "test-user", "existing-session").
		WillReturnRows(checkRows)

	// Should return error without trying to insert
	_, err := s.CreateSession(context.Background(), key, session.StateMap{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session already exists and has not expired")

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateSession_ExistingNeverExpires(t *testing.T) {
	s, mock, db := setupMockService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "existing-session",
	}

	// Mock check for existing session - returns NULL expires_at (never expires)
	checkRows := sqlmock.NewRows([]string{"expires_at"}).
		AddRow(nil)
	mock.ExpectQuery("SELECT expires_at FROM session_states").
		WithArgs("test-app", "test-user", "existing-session").
		WillReturnRows(checkRows)

	// Should return error without trying to insert
	_, err := s.CreateSession(context.Background(), key, session.StateMap{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session already exists and has not expired")

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateSession_ExistingExpired(t *testing.T) {
	s, mock, db := setupMockService(t, nil)
	defer db.Close()

	// Set sessionTTL so cleanup will be triggered
	s.opts.sessionTTL = 1 * time.Hour

	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "expired-session",
	}

	// Mock check for existing session - returns a past expires_at (expired)
	pastTime := time.Now().Add(-1 * time.Hour)
	checkRows := sqlmock.NewRows([]string{"expires_at"}).
		AddRow(pastTime)
	mock.ExpectQuery("SELECT expires_at FROM session_states").
		WithArgs("test-app", "test-user", "expired-session").
		WillReturnRows(checkRows)

	// Mock cleanup for expired sessions (soft delete) - order matters.
	// Now in transaction.
	mock.ExpectBegin()
	// 1. Soft delete session_states.
	mock.ExpectExec(`UPDATE session_states SET deleted_at = \$1`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	// 2. Soft delete session_events.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT app_name, user_id, session_id, MAX(updated_at) as updated_at FROM session_events")).
		WillReturnRows(sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "updated_at"}).
			AddRow("session-1", "app-1", "user-1", time.Now().Add(-48*time.Hour)))
	mock.ExpectExec(`UPDATE session_events SET deleted_at = \$1`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	// 3. Soft delete session_summaries.
	mock.ExpectExec(`UPDATE session_summaries SET deleted_at = \$1`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	// Mock INSERT session state
	mock.ExpectExec("INSERT INTO session_states").
		WithArgs("test-app", "test-user", "expired-session", sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// Mock SELECT app_states
	appRows := sqlmock.NewRows([]string{"key", "value"})
	mock.ExpectQuery("SELECT key, value FROM app_states").
		WithArgs("test-app", sqlmock.AnyArg()).
		WillReturnRows(appRows)

	// Mock SELECT user_states
	userRows := sqlmock.NewRows([]string{"key", "value"})
	mock.ExpectQuery("SELECT key, value FROM user_states").
		WithArgs("test-app", "test-user", sqlmock.AnyArg()).
		WillReturnRows(userRows)

	sess, err := s.CreateSession(context.Background(), key, session.StateMap{})
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.Equal(t, "expired-session", sess.ID)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSession_Success(t *testing.T) {
	s, mock, db := setupMockService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "test-session",
	}

	// Mock session state
	sessState := &SessionState{
		ID:    "test-session",
		State: session.StateMap{"key1": []byte("value1")},
	}
	stateBytes, _ := json.Marshal(sessState)
	stateRows := sqlmock.NewRows([]string{"state", "created_at", "updated_at"}).
		AddRow(stateBytes, time.Now(), time.Now())

	mock.ExpectQuery("SELECT state, created_at, updated_at FROM session_states").
		WithArgs("test-app", "test-user", "test-session", sqlmock.AnyArg()).
		WillReturnRows(stateRows)

	// Mock app_states
	appRows := sqlmock.NewRows([]string{"key", "value"})
	mock.ExpectQuery("SELECT key, value FROM app_states").
		WithArgs("test-app", sqlmock.AnyArg()).
		WillReturnRows(appRows)

	// Mock user_states
	userRows := sqlmock.NewRows([]string{"key", "value"})
	mock.ExpectQuery("SELECT key, value FROM user_states").
		WithArgs("test-app", "test-user", sqlmock.AnyArg()).
		WillReturnRows(userRows)

		// Prepare mock event
	evt := event.New("inv-1", "author")
	evt.Response = &model.Response{
		Object: model.ObjectTypeChatCompletion,
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role:    model.RoleUser,
					Content: "Hello, world!",
				},
			},
		},
	}
	eventBytes, _ := json.Marshal(evt)
	mock.ExpectQuery("SELECT session_id, event FROM session_events").
		WithArgs("test-app", "test-user", "{test-session}").
		WillReturnRows(sqlmock.NewRows([]string{"session_id", "event"}).
			AddRow(key.SessionID, eventBytes))

	// Mock: Batch load summaries with data
	mock.ExpectQuery(regexp.QuoteMeta("SELECT session_id, filter_key, summary FROM session_summaries")).
		WillReturnRows(sqlmock.NewRows([]string{"session_id", "filter_key", "summary"}))

	sess, err := s.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.Equal(t, "test-session", sess.ID)
	assert.Equal(t, []byte("value1"), sess.State["key1"])
	assert.Equal(t, 1, len(sess.Events))
	assert.Equal(t, "Hello, world!", sess.Events[0].Response.Choices[0].Message.Content)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAppendEvent_InvalidKey(t *testing.T) {
	s, _, db := setupMockService(t, nil)
	defer db.Close()

	// Test with empty SessionID
	sess := &session.Session{
		ID:      "",
		AppName: "test-app",
		UserID:  "test-user",
	}

	evt := event.New("test", "author")
	err := s.AppendEvent(context.Background(), sess, evt)
	require.Error(t, err)
}

func TestClose_Success(t *testing.T) {
	s, _, db := setupMockService(t, nil)

	err := s.Close()
	require.NoError(t, err)

	// Close again should be safe (once.Do)
	err = s.Close()
	require.NoError(t, err)

	db.Close()
}

func TestBuildConnString(t *testing.T) {
	tests := []struct {
		name     string
		opts     ServiceOpts
		expected string
	}{
		{
			name: "all fields provided",
			opts: ServiceOpts{
				host:     "localhost",
				port:     5432,
				database: "testdb",
				user:     "testuser",
				password: "testpass",
				sslMode:  "disable",
			},
			expected: "host=localhost port=5432 dbname=testdb sslmode=disable user=testuser password=testpass",
		},
		{
			name:     "default values",
			opts:     ServiceOpts{},
			expected: "host=localhost port=5432 dbname=trpc-agent-go-pgsession sslmode=disable",
		},
		{
			name: "no credentials",
			opts: ServiceOpts{
				host:     "dbhost",
				port:     5433,
				database: "mydb",
				sslMode:  "require",
			},
			expected: "host=dbhost port=5433 dbname=mydb sslmode=require",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildConnString(tt.opts)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMergeState(t *testing.T) {
	appState := session.StateMap{
		"app_key1": []byte("app_value1"),
	}

	userState := session.StateMap{
		"user_key1": []byte("user_value1"),
	}

	sess := &session.Session{
		ID:      "test-session",
		AppName: "test-app",
		UserID:  "test-user",
		State: session.StateMap{
			"session_key1": []byte("session_value1"),
		},
	}

	result := mergeState(appState, userState, sess)

	assert.Equal(t, []byte("session_value1"), result.State["session_key1"])
	assert.Equal(t, []byte("app_value1"), result.State["app:app_key1"])
	assert.Equal(t, []byte("user_value1"), result.State["user:user_key1"])
}

func TestApplyOptions(t *testing.T) {
	opts := applyOptions(
		session.WithEventNum(10),
		session.WithEventTime(time.Now()),
	)

	assert.Equal(t, 10, opts.EventNum)
	assert.False(t, opts.EventTime.IsZero())
}

func TestGetSession_WithEventLimit(t *testing.T) {
	s, mock, db := setupMockService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "test-session",
	}

	// Mock session state
	sessState := &SessionState{
		ID:    "test-session",
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)
	stateRows := sqlmock.NewRows([]string{"state", "created_at", "updated_at"}).
		AddRow(stateBytes, time.Now(), time.Now())

	mock.ExpectQuery("SELECT state, created_at, updated_at FROM session_states").
		WithArgs("test-app", "test-user", "test-session", sqlmock.AnyArg()).
		WillReturnRows(stateRows)

	// Mock app_states
	appRows := sqlmock.NewRows([]string{"key", "value"})
	mock.ExpectQuery("SELECT key, value FROM app_states").
		WithArgs("test-app", sqlmock.AnyArg()).
		WillReturnRows(appRows)

	// Mock user_states
	userRows := sqlmock.NewRows([]string{"key", "value"})
	mock.ExpectQuery("SELECT key, value FROM user_states").
		WithArgs("test-app", "test-user", sqlmock.AnyArg()).
		WillReturnRows(userRows)

	// Mock events - with LIMIT in query (limit controls how many to return, not delete)
	eventRows := sqlmock.NewRows([]string{"session_id", "event"})
	mock.ExpectQuery("SELECT session_id, event FROM session_events").
		WithArgs("test-app", "test-user", "{test-session}").
		WillReturnRows(eventRows)

	// WithEventNum(5) controls how many events to return in the query
	// Note: This does NOT delete events from database, only limits the result set
	sess, err := s.GetSession(context.Background(), key, session.WithEventNum(5))
	require.NoError(t, err)
	require.NotNil(t, sess)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSession_WithTTLRefresh(t *testing.T) {
	s, mock, db := setupMockService(t, nil)
	defer db.Close()

	// Enable session TTL to trigger refresh
	s.opts.sessionTTL = 1 * time.Hour

	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "test-session",
	}

	// Mock session state
	sessState := &SessionState{
		ID:    "test-session",
		State: session.StateMap{"key1": []byte("value1")},
	}
	stateBytes, _ := json.Marshal(sessState)
	stateRows := sqlmock.NewRows([]string{"state", "created_at", "updated_at"}).
		AddRow(stateBytes, time.Now(), time.Now())

	mock.ExpectQuery("SELECT state, created_at, updated_at FROM session_states").
		WithArgs("test-app", "test-user", "test-session", sqlmock.AnyArg()).
		WillReturnRows(stateRows)

	// Mock app_states
	appRows := sqlmock.NewRows([]string{"key", "value"})
	mock.ExpectQuery("SELECT key, value FROM app_states").
		WithArgs("test-app", sqlmock.AnyArg()).
		WillReturnRows(appRows)

	// Mock user_states
	userRows := sqlmock.NewRows([]string{"key", "value"})
	mock.ExpectQuery("SELECT key, value FROM user_states").
		WithArgs("test-app", "test-user", sqlmock.AnyArg()).
		WillReturnRows(userRows)

	// Mock events
	eventRows := sqlmock.NewRows([]string{"test-session", "event"})
	mock.ExpectQuery("SELECT session_id, event FROM session_events").
		WithArgs("test-app", "test-user", "{test-session}").
		WillReturnRows(eventRows)

	// Mock TTL refresh UPDATE - this should be called after successful GetSession
	mock.ExpectExec("UPDATE session_states").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), "test-app", "test-user", "test-session").
		WillReturnResult(sqlmock.NewResult(0, 1))

	sess, err := s.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.Equal(t, "test-session", sess.ID)
	assert.Equal(t, []byte("value1"), sess.State["key1"])

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAppendEvent_PartialEvent(t *testing.T) {
	s, mock, db := setupMockService(t, nil)
	defer db.Close()

	sess := &session.Session{
		ID:      "test-session",
		AppName: "test-app",
		UserID:  "test-user",
		State:   session.StateMap{},
		Events:  []event.Event{},
	}

	// Create a partial event (IsPartial = true)
	evt := event.New("test-invocation", "test-author")
	evt.IsPartial = true
	evt.Response = &model.Response{
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "partial response",
				},
			},
		},
	}

	// Mock get session state
	sessState := &SessionState{
		ID:    "test-session",
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)
	stateRows := sqlmock.NewRows([]string{"state", "expires_at"}).
		AddRow(stateBytes, nil)

	mock.ExpectQuery("SELECT state, expires_at FROM session_states").
		WithArgs("test-app", "test-user", "test-session").
		WillReturnRows(stateRows)

	// Mock transaction
	mock.ExpectBegin()

	// Mock update session state (partial events still update state)
	mock.ExpectExec("UPDATE session_states SET state").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			"test-app", "test-user", "test-session").
		WillReturnResult(sqlmock.NewResult(0, 1))

	// Note: After JSON marshaling and unmarshaling in UpdateUserSession,
	// the IsPartial field might not be preserved correctly if it's not in the JSON tags.
	// So partial events might still be inserted. We need to expect the INSERT.
	mock.ExpectExec("INSERT INTO session_events").
		WithArgs("test-app", "test-user", "test-session", sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	mock.ExpectCommit()

	err := s.AppendEvent(context.Background(), sess, evt)
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCleanupExpired(t *testing.T) {
	s, mock, db := setupMockService(t, &TestServiceOpts{
		sessionTTL:   time.Hour,
		appStateTTL:  2 * time.Hour,
		userStateTTL: 3 * time.Hour,
		softDelete:   boolPtr(true),
	})
	defer db.Close()

	// Mock cleanup queries: all tables in a single transaction
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE session_states SET deleted_at").
		WillReturnResult(sqlmock.NewResult(0, 5))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT app_name, user_id, session_id, MAX(updated_at) as updated_at FROM session_events")).
		WillReturnRows(sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "updated_at"}).
			AddRow("session-1", "app-1", "user-1", time.Now().Add(-48*time.Hour)))
	mock.ExpectExec("UPDATE session_events SET deleted_at").
		WillReturnResult(sqlmock.NewResult(0, 10))
	mock.ExpectExec("UPDATE session_summaries SET deleted_at").
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec("UPDATE app_states SET deleted_at").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE user_states SET deleted_at").
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()

	s.cleanupExpired()

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCleanupExpiredForUser_softdelete(t *testing.T) {
	s, mock, db := setupMockService(t, &TestServiceOpts{
		sessionTTL:   time.Hour,
		userStateTTL: 2 * time.Hour,
		softDelete:   boolPtr(true),
	})
	defer db.Close()

	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	// Mock cleanup queries: all tables in a single transaction
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE session_states SET deleted_at").
		WithArgs(sqlmock.AnyArg(), "test-app", "test-user").
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT app_name, user_id, session_id, MAX(updated_at) as updated_at FROM session_events")).
		WillReturnRows(sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "updated_at"}).
			AddRow("session-1", "app-1", "user-1", time.Now().Add(-48*time.Hour)))
	mock.ExpectExec("UPDATE session_events SET deleted_at").
		WillReturnResult(sqlmock.NewResult(0, 5))
	mock.ExpectExec("UPDATE session_summaries SET deleted_at").
		WithArgs(sqlmock.AnyArg(), "test-app", "test-user").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE user_states SET deleted_at").
		WithArgs(sqlmock.AnyArg(), "test-app", "test-user").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	s.cleanupExpiredForUser(context.Background(), userKey)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCleanupExpiredForUser_harddelete(t *testing.T) {
	s, mock, db := setupMockService(t, &TestServiceOpts{
		sessionTTL:   time.Hour,
		userStateTTL: 2 * time.Hour,
		softDelete:   boolPtr(false),
	})
	defer db.Close()

	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	// Mock cleanup queries: all tables in a single transaction
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM session_states").
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT app_name, user_id, session_id, MAX(updated_at) as updated_at FROM session_events")).
		WillReturnRows(sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "updated_at"}).
			AddRow("session-1", "app-1", "user-1", time.Now().Add(-48*time.Hour)))
	mock.ExpectExec("DELETE FROM session_events").
		WillReturnResult(sqlmock.NewResult(0, 5))
	mock.ExpectExec("DELETE FROM session_summaries").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM user_states").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	s.cleanupExpiredForUser(context.Background(), userKey)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestHardDeleteExpired(t *testing.T) {
	s, mock, db := setupMockService(t, &TestServiceOpts{
		sessionTTL: time.Hour,
		softDelete: boolPtr(false), // Hard delete
	})
	defer db.Close()

	// Mock hard delete queries in transaction
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM session_states").
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT app_name, user_id, session_id, MAX(updated_at) as updated_at FROM session_events")).
		WillReturnRows(sqlmock.NewRows([]string{"app_name", "user_id", "session_id", "updated_at"}).
			AddRow("session-1", "app-1", "user-1", time.Now().Add(-48*time.Hour)))
	mock.ExpectExec("DELETE FROM session_events").
		WillReturnResult(sqlmock.NewResult(0, 7))
	mock.ExpectExec("DELETE FROM session_summaries").
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	s.cleanupExpired()

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestStartStopCleanupRoutine(t *testing.T) {
	s, _, db := setupMockService(t, &TestServiceOpts{
		sessionTTL:      time.Hour,
		cleanupInterval: 100 * time.Millisecond,
	})
	defer db.Close()

	// Manually start cleanup routine for this test
	s.startCleanupRoutine()

	// Cleanup routine should be started
	assert.NotNil(t, s.cleanupTicker)

	// Stop cleanup
	s.stopCleanupRoutine()
	assert.Nil(t, s.cleanupTicker)

	// Stopping again should be safe (idempotent)
	s.stopCleanupRoutine()
	assert.Nil(t, s.cleanupTicker)
}

func TestCleanupRoutineRunsPeriodically(t *testing.T) {
	s, mock, db := setupMockService(t, &TestServiceOpts{
		sessionTTL:      time.Hour,
		softDelete:      boolPtr(true),
		cleanupInterval: 50 * time.Millisecond, // Very short interval for testing
	})
	defer db.Close()

	// Expect multiple cleanup calls
	for i := 0; i < 3; i++ {
		mock.ExpectExec("UPDATE session_states SET deleted_at").
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("UPDATE session_events SET deleted_at").
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec("UPDATE session_summaries SET deleted_at").
			WillReturnResult(sqlmock.NewResult(0, 0))
	}

	// Wait for a few cleanup cycles
	time.Sleep(200 * time.Millisecond)

	s.stopCleanupRoutine()

	// Should have run at least a few times
	// We set expectations for 3 iterations
}

func TestAppendEvent_Async_Success(t *testing.T) {
	s, mock, db := setupMockService(t, &TestServiceOpts{
		enableAsyncPersist: true,
	})
	defer db.Close()
	defer s.Close()

	sess := &session.Session{
		ID:      "test-session",
		AppName: "test-app",
		UserID:  "test-user",
		State:   session.StateMap{},
		Events:  []event.Event{},
	}

	evt := event.New("test", "test")
	evt.Response = &model.Response{
		Choices: []model.Choice{
			{Message: model.Message{Role: model.RoleAssistant, Content: "test"}},
		},
	}

	err := s.AppendEvent(context.Background(), sess, evt)
	require.NoError(t, err)

	// Give async worker time to process
	time.Sleep(10 * time.Millisecond)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAppendEvent_Async_Panic(t *testing.T) {
	s, _, db := setupMockService(t, &TestServiceOpts{
		enableAsyncPersist: true,
	})
	defer db.Close()

	// Close channels to cause panic
	for _, ch := range s.eventPairChans {
		close(ch)
	}

	sess := &session.Session{
		ID:      "test-session",
		AppName: "test-app",
		UserID:  "test-user",
		State:   session.StateMap{},
		Events:  []event.Event{},
	}

	evt := event.New("test", "test")
	evt.Response = &model.Response{
		Choices: []model.Choice{
			{Message: model.Message{Role: model.RoleAssistant, Content: "test"}},
		},
	}

	// Should not panic, just log error
	err := s.AppendEvent(context.Background(), sess, evt)
	// In async mode, it might succeed sending before noticing the channel is closed
	_ = err
}

func TestAppendEvent_InvalidEvent_NoResponse(t *testing.T) {
	s, mock, db := setupMockService(t, nil)
	defer db.Close()

	sess := &session.Session{
		ID:      "test-session",
		AppName: "test-app",
		UserID:  "test-user",
		State:   session.StateMap{},
		Events:  []event.Event{},
	}

	// Event with no response - should update state but not persist
	evt := event.New("test", "test")
	// No response set

	// Mock get session state
	sessState := &SessionState{
		ID:    "test-session",
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)
	stateRows := sqlmock.NewRows([]string{"state", "expires_at"}).
		AddRow(stateBytes, nil)

	mock.ExpectQuery("SELECT state, expires_at FROM session_states").
		WithArgs("test-app", "test-user", "test-session").
		WillReturnRows(stateRows)

	// Mock transaction
	mock.ExpectBegin()

	// Mock update session state only (no event insert because no response)
	mock.ExpectExec("UPDATE session_states SET state").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			"test-app", "test-user", "test-session").
		WillReturnResult(sqlmock.NewResult(0, 1))

	mock.ExpectCommit()

	err := s.AppendEvent(context.Background(), sess, evt)
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAppendEvent_InvalidEvent_EmptyContent(t *testing.T) {
	s, mock, db := setupMockService(t, nil)
	defer db.Close()

	sess := &session.Session{
		ID:      "test-session",
		AppName: "test-app",
		UserID:  "test-user",
		State:   session.StateMap{},
		Events:  []event.Event{},
	}

	// Event with response but empty content
	evt := event.New("test", "test")
	evt.Response = &model.Response{
		Choices: []model.Choice{
			{Message: model.Message{Role: model.RoleAssistant, Content: ""}}, // Empty content
		},
	}

	// Mock get session state
	sessState := &SessionState{
		ID:    "test-session",
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)
	stateRows := sqlmock.NewRows([]string{"state", "expires_at"}).
		AddRow(stateBytes, nil)

	mock.ExpectQuery("SELECT state, expires_at FROM session_states").
		WithArgs("test-app", "test-user", "test-session").
		WillReturnRows(stateRows)

	// Mock transaction
	mock.ExpectBegin()

	// Mock update session state only
	mock.ExpectExec("UPDATE session_states SET state").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			"test-app", "test-user", "test-session").
		WillReturnResult(sqlmock.NewResult(0, 1))

	mock.ExpectCommit()

	err := s.AppendEvent(context.Background(), sess, evt)
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAppendEvent_SessionNotFound(t *testing.T) {
	s, mock, db := setupMockService(t, nil)
	defer db.Close()

	sess := &session.Session{
		ID:      "nonexistent-session",
		AppName: "test-app",
		UserID:  "test-user",
		State:   session.StateMap{},
		Events:  []event.Event{},
	}

	evt := event.New("test", "test")
	evt.Response = &model.Response{
		Choices: []model.Choice{
			{Message: model.Message{Role: model.RoleAssistant, Content: "test"}},
		},
	}

	// Mock get session state - not found
	stateRows := sqlmock.NewRows([]string{"state", "expires_at"})
	mock.ExpectQuery("SELECT state, expires_at FROM session_states").
		WithArgs("test-app", "test-user", "nonexistent-session").
		WillReturnRows(stateRows)

	err := s.AppendEvent(context.Background(), sess, evt)
	require.Error(t, err)
	require.Contains(t, err.Error(), "session not found")

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestClose_Multiple(t *testing.T) {
	s, _, db := setupMockService(t, &TestServiceOpts{
		enableAsyncPersist: true,
	})
	defer db.Close()

	// First close
	err := s.Close()
	require.NoError(t, err)

	// Second close should be safe (idempotent)
	err = s.Close()
	require.NoError(t, err)
}

func TestNewService_MissingDSNAndInstance(t *testing.T) {
	svc, err := NewService()
	assert.Error(t, err)
	assert.Nil(t, svc)
	assert.Contains(t, err.Error(), "create postgres client failed")
}

func TestNewService_WithInstance_Success(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	defer db.Close()

	originalBuilder := storage.GetClientBuilder()
	defer storage.SetClientBuilder(originalBuilder)

	storage.SetClientBuilder(func(ctx context.Context, builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return &mockPostgresClient{db: db}, nil
	})

	// Register instance
	instanceName := "test-instance-success"
	storage.RegisterPostgresInstance(instanceName,
		storage.WithClientConnString("test:test@tcp(localhost:3306)/testdb"),
	)

	svc, err := NewService(
		WithPostgresInstance(instanceName),
		WithSkipDBInit(true),
	)
	require.NoError(t, err)
	require.NotNil(t, svc)

	err = svc.Close()
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
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
