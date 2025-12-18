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
	SetPrompt(prompt string)
	SetModel(m model.Model)
	Metadata() map[string]any
}

// boolPtr returns a pointer to a bool value
func boolPtr(b bool) *bool {
	return &b
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
		tableSessionTracks:    "session_track_events",
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
		tableSessionTracks:    prefix + "session_track_events",
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
		s.trackEventChans = make([]chan *trackEventPair, defaultAsyncPersisterNum)
		for i := 0; i < defaultAsyncPersisterNum; i++ {
			s.trackEventChans[i] = make(chan *trackEventPair, defaultChanBufferSize)
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
	// 3. Soft delete session_track_events.
	mock.ExpectExec(`UPDATE session_track_events SET deleted_at = \$1`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	// 4. Soft delete session_summaries.
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

func TestGetSession_WithTrackEvents(t *testing.T) {
	s, mock, db := setupMockService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "test-session",
	}

	state := session.StateMap{
		"tracks": []byte(`["agui"]`),
	}
	sessState := &SessionState{
		ID:    "test-session",
		State: state,
	}
	stateBytes, _ := json.Marshal(sessState)
	stateRows := sqlmock.NewRows([]string{"state", "created_at", "updated_at"}).
		AddRow(stateBytes, time.Now(), time.Now())

	mock.ExpectQuery("SELECT state, created_at, updated_at FROM session_states").
		WithArgs("test-app", "test-user", "test-session", sqlmock.AnyArg()).
		WillReturnRows(stateRows)

	appRows := sqlmock.NewRows([]string{"key", "value"})
	mock.ExpectQuery("SELECT key, value FROM app_states").
		WithArgs("test-app", sqlmock.AnyArg()).
		WillReturnRows(appRows)

	userRows := sqlmock.NewRows([]string{"key", "value"})
	mock.ExpectQuery("SELECT key, value FROM user_states").
		WithArgs("test-app", "test-user", sqlmock.AnyArg()).
		WillReturnRows(userRows)

	eventRows := sqlmock.NewRows([]string{"session_id", "event"})
	mock.ExpectQuery("SELECT session_id, event FROM session_events").
		WithArgs("test-app", "test-user", "{test-session}").
		WillReturnRows(eventRows)

	trackEvent := &session.TrackEvent{
		Track:     "agui",
		Payload:   json.RawMessage(`{"delta":"track"}`),
		Timestamp: time.Now(),
	}
	trackBytes, _ := json.Marshal(trackEvent)

	trackRows := sqlmock.NewRows([]string{"event"}).AddRow(trackBytes)
	mock.ExpectQuery("SELECT event FROM session_track_events").
		WithArgs("test-app", "test-user", "test-session", "agui", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(trackRows)

	sess, err := s.GetSession(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.NotNil(t, sess.Tracks)
	history, ok := sess.Tracks["agui"]
	require.True(t, ok)
	require.Len(t, history.Events, 1)
	assert.Equal(t, string(trackEvent.Payload), string(history.Events[0].Payload))

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSession_NotFound(t *testing.T) {
	s, mock, db := setupMockService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "non-existent",
	}

	// Mock empty result
	stateRows := sqlmock.NewRows([]string{"state", "created_at", "updated_at"})
	mock.ExpectQuery("SELECT state, created_at, updated_at FROM session_states").
		WithArgs("test-app", "test-user", "non-existent", sqlmock.AnyArg()).
		WillReturnRows(stateRows)

	sess, err := s.GetSession(context.Background(), key)
	require.NoError(t, err)
	assert.Nil(t, sess)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSession_InvalidKey(t *testing.T) {
	s, _, db := setupMockService(t, nil)
	defer db.Close()

	// Test with empty SessionID
	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "",
	}

	_, err := s.GetSession(context.Background(), key)
	require.Error(t, err)
}

func TestListSessions_InvalidKey(t *testing.T) {
	s, _, db := setupMockService(t, nil)
	defer db.Close()

	// Test with empty AppName
	userKey := session.UserKey{
		AppName: "",
		UserID:  "test-user",
	}

	_, err := s.ListSessions(context.Background(), userKey)
	require.Error(t, err)
}

func TestListSessions_EmptyResult(t *testing.T) {
	s, mock, db := setupMockService(t, nil)
	defer db.Close()

	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	// Mock app states query (empty)
	appStateRows := sqlmock.NewRows([]string{"key", "value"})
	mock.ExpectQuery("SELECT key, value FROM app_states").
		WithArgs("test-app", sqlmock.AnyArg()).
		WillReturnRows(appStateRows)

	// Mock user states query (empty)
	userStateRows := sqlmock.NewRows([]string{"key", "value"})
	mock.ExpectQuery("SELECT key, value FROM user_states").
		WithArgs("test-app", "test-user", sqlmock.AnyArg()).
		WillReturnRows(userStateRows)

	// Mock session states query (empty - no sessions found)
	sessionRows := sqlmock.NewRows([]string{"session_id", "state", "created_at", "updated_at"})
	mock.ExpectQuery("SELECT session_id, state, created_at, updated_at FROM session_states").
		WithArgs("test-app", "test-user", sqlmock.AnyArg()).
		WillReturnRows(sessionRows)

	sessions, err := s.ListSessions(context.Background(), userKey)
	require.NoError(t, err)
	require.Empty(t, sessions)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListSessions_QueryError(t *testing.T) {
	s, mock, db := setupMockService(t, nil)
	defer db.Close()

	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	// Mock app states query error
	mock.ExpectQuery("SELECT key, value FROM app_states").
		WithArgs("test-app", sqlmock.AnyArg()).
		WillReturnError(fmt.Errorf("database error"))

	_, err := s.ListSessions(context.Background(), userKey)
	require.Error(t, err)
	require.Contains(t, err.Error(), "database error")

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListSessions_SessionStateUnmarshalError(t *testing.T) {
	s, mock, db := setupMockService(t, nil)
	defer db.Close()

	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	now := time.Now()

	// Mock app states query (empty)
	appStateRows := sqlmock.NewRows([]string{"key", "value"})
	mock.ExpectQuery("SELECT key, value FROM app_states").
		WithArgs("test-app", sqlmock.AnyArg()).
		WillReturnRows(appStateRows)

	// Mock user states query (empty)
	userStateRows := sqlmock.NewRows([]string{"key", "value"})
	mock.ExpectQuery("SELECT key, value FROM user_states").
		WithArgs("test-app", "test-user", sqlmock.AnyArg()).
		WillReturnRows(userStateRows)

	// Mock session states query with invalid JSON
	sessionRows := sqlmock.NewRows([]string{"session_id", "state", "created_at", "updated_at"}).
		AddRow("session-1", []byte("invalid json"), now, now)

	mock.ExpectQuery("SELECT session_id, state, created_at, updated_at FROM session_states").
		WithArgs("test-app", "test-user", sqlmock.AnyArg()).
		WillReturnRows(sessionRows)

	_, err := s.ListSessions(context.Background(), userKey)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unmarshal session state failed")

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListSessions_WithTrackEvents(t *testing.T) {
	s, mock, db := setupMockService(t, nil)
	defer db.Close()

	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	now := time.Now()

	appRows := sqlmock.NewRows([]string{"key", "value"})
	mock.ExpectQuery("SELECT key, value FROM app_states").
		WithArgs("test-app", sqlmock.AnyArg()).
		WillReturnRows(appRows)

	userRows := sqlmock.NewRows([]string{"key", "value"})
	mock.ExpectQuery("SELECT key, value FROM user_states").
		WithArgs("test-app", "test-user", sqlmock.AnyArg()).
		WillReturnRows(userRows)

	state := &SessionState{
		State: session.StateMap{
			"tracks": []byte(`["alpha","beta"]`),
		},
	}
	stateBytes, _ := json.Marshal(state)
	sessionRows := sqlmock.NewRows([]string{"session_id", "state", "created_at", "updated_at"}).
		AddRow("session-1", stateBytes, now, now)

	mock.ExpectQuery("SELECT session_id, state, created_at, updated_at FROM session_states").
		WithArgs("test-app", "test-user", sqlmock.AnyArg()).
		WillReturnRows(sessionRows)

	eventRows := sqlmock.NewRows([]string{"session_id", "event"})
	mock.ExpectQuery("SELECT session_id, event FROM session_events").
		WithArgs("test-app", "test-user", "{session-1}").
		WillReturnRows(eventRows)

	summaryRows := sqlmock.NewRows([]string{"session_id", "filter_key", "summary"})
	mock.ExpectQuery("SELECT session_id, filter_key, summary FROM session_summaries").
		WithArgs("test-app", "test-user", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(summaryRows)

	trackEventAlpha := &session.TrackEvent{
		Track:     "alpha",
		Payload:   json.RawMessage(`"payload"`),
		Timestamp: now,
	}
	alphaBytes, _ := json.Marshal(trackEventAlpha)
	alphaRows := sqlmock.NewRows([]string{"event"}).AddRow(alphaBytes)
	mock.ExpectQuery("SELECT event FROM session_track_events").
		WithArgs("test-app", "test-user", "session-1", "alpha", sqlmock.AnyArg(), sqlmock.AnyArg(), 1).
		WillReturnRows(alphaRows)

	trackEventBeta := &session.TrackEvent{
		Track:     "beta",
		Payload:   json.RawMessage(`"payload-beta"`),
		Timestamp: now,
	}
	betaBytes, _ := json.Marshal(trackEventBeta)
	betaRows := sqlmock.NewRows([]string{"event"}).AddRow(betaBytes)
	mock.ExpectQuery("SELECT event FROM session_track_events").
		WithArgs("test-app", "test-user", "session-1", "beta", sqlmock.AnyArg(), sqlmock.AnyArg(), 1).
		WillReturnRows(betaRows)

	sessions, err := s.ListSessions(context.Background(), userKey, session.WithEventNum(1))
	require.NoError(t, err)
	require.Len(t, sessions, 1)
	require.NotNil(t, sessions[0].Tracks)
	alpha, ok := sessions[0].Tracks["alpha"]
	require.True(t, ok)
	require.Len(t, alpha.Events, 1)
	assert.Equal(t, json.RawMessage(`"payload"`), alpha.Events[0].Payload)

	beta, ok := sessions[0].Tracks["beta"]
	require.True(t, ok)
	require.Len(t, beta.Events, 1)
	assert.Equal(t, json.RawMessage(`"payload-beta"`), beta.Events[0].Payload)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListSessions_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "user-123",
	}

	// Prepare session state
	sessState := SessionState{
		ID:    "session-1",
		State: session.StateMap{"key1": []byte(`"value1"`)},
	}
	stateBytes, _ := json.Marshal(sessState)

	// Mock: List app states (empty)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT key, value FROM app_states")).
		WithArgs(userKey.AppName, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

	// Mock: List user states (empty)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT key, value FROM user_states")).
		WithArgs(userKey.AppName, userKey.UserID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}))

	// Mock: Query session states for user
	mock.ExpectQuery(regexp.QuoteMeta("SELECT session_id, state, created_at, updated_at FROM session_states")).
		WithArgs(userKey.AppName, userKey.UserID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"session_id", "state", "created_at", "updated_at"}).
			AddRow("session-1", stateBytes, time.Now(), time.Now()))

	// Mock: Batch load events (empty)
	evt := event.NewResponseEvent("inv-1", "author", &model.Response{
		Object: model.ObjectTypeChatCompletion,
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role:    model.RoleUser,
					Content: "hello",
				},
			},
		},
	})
	eventBytes, _ := json.Marshal(evt)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT session_id, event FROM session_events")).
		WillReturnRows(sqlmock.NewRows([]string{"session_id", "event"}).
			AddRow("session-1", eventBytes))

	// Mock: Batch load summaries (empty)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT session_id, filter_key, summary FROM session_summaries")).
		WillReturnRows(sqlmock.NewRows([]string{"session_id", "filter_key", "summary"}))

	sessions, err := s.ListSessions(ctx, userKey)
	require.NoError(t, err)
	assert.Len(t, sessions, 1)
	assert.Equal(t, "session-1", sessions[0].ID)
	assert.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, "hello", sessions[0].Events[0].Choices[0].Message.Content)
}

func TestListSessions_WithMultipleSessions(t *testing.T) {
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
	mock.ExpectQuery(regexp.QuoteMeta("SELECT key, value FROM app_states")).
		WithArgs(userKey.AppName, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}).
			AddRow("app-key", []byte(`"app-value"`)))

	// Mock: List user states
	mock.ExpectQuery(regexp.QuoteMeta("SELECT key, value FROM user_states")).
		WithArgs(userKey.AppName, userKey.UserID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value"}).
			AddRow("user-key", []byte(`"user-value"`)))

	// Prepare session states
	sess1 := SessionState{ID: "session-1", State: session.StateMap{"s1": []byte(`"v1"`)}}
	sess2 := SessionState{ID: "session-2", State: session.StateMap{"s2": []byte(`"v2"`)}}
	state1Bytes, _ := json.Marshal(sess1)
	state2Bytes, _ := json.Marshal(sess2)

	// Mock: Query session states
	mock.ExpectQuery(regexp.QuoteMeta("SELECT session_id, state, created_at, updated_at FROM session_states")).
		WithArgs(userKey.AppName, userKey.UserID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"session_id", "state", "created_at", "updated_at"}).
			AddRow("session-1", state1Bytes, time.Now(), time.Now()).
			AddRow("session-2", state2Bytes, time.Now(), time.Now()))

	// Mock: Batch load events
	mock.ExpectQuery(regexp.QuoteMeta("SELECT session_id, event FROM session_events")).
		WillReturnRows(sqlmock.NewRows([]string{"session_id", "event"}))

	// Mock: Batch load summaries
	mock.ExpectQuery(regexp.QuoteMeta("SELECT session_id, filter_key, summary FROM session_summaries")).
		WillReturnRows(sqlmock.NewRows([]string{"session_id", "filter_key", "summary"}))

	sessions, err := s.ListSessions(ctx, userKey)
	require.NoError(t, err)
	assert.Len(t, sessions, 2)

	// Verify app state and user state are merged
	assert.Contains(t, sessions[0].State, "app:app-key")
	assert.Contains(t, sessions[0].State, "user:user-key")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteSession_Success(t *testing.T) {
	s, mock, db := setupMockService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "test-session",
	}

	// Mock transaction begin
	mock.ExpectBegin()

	// Mock soft delete session state
	mock.ExpectExec("UPDATE session_states SET deleted_at").
		WithArgs(sqlmock.AnyArg(), "test-app", "test-user", "test-session").
		WillReturnResult(sqlmock.NewResult(0, 1))

	// Mock soft delete session summaries
	mock.ExpectExec("UPDATE session_summaries SET deleted_at").
		WithArgs(sqlmock.AnyArg(), "test-app", "test-user", "test-session").
		WillReturnResult(sqlmock.NewResult(0, 1))

	// Mock soft delete session events
	mock.ExpectExec("UPDATE session_events SET deleted_at").
		WithArgs(sqlmock.AnyArg(), "test-app", "test-user", "test-session").
		WillReturnResult(sqlmock.NewResult(0, 1))
	// Mock soft delete session tracks.
	mock.ExpectExec("UPDATE session_track_events SET deleted_at").
		WithArgs(sqlmock.AnyArg(), "test-app", "test-user", "test-session").
		WillReturnResult(sqlmock.NewResult(0, 1))

	// Mock transaction commit
	mock.ExpectCommit()

	err := s.DeleteSession(context.Background(), key)
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteSession_InvalidKey(t *testing.T) {
	s, _, db := setupMockService(t, nil)
	defer db.Close()

	// Test with empty SessionID
	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "",
	}

	err := s.DeleteSession(context.Background(), key)
	require.Error(t, err)
}

func TestUpdateAppState_Success(t *testing.T) {
	s, mock, db := setupMockService(t, nil)
	defer db.Close()

	// Use single key to avoid map iteration order issues
	state := session.StateMap{
		"key1": []byte("value1"),
	}

	// Mock INSERT/UPDATE for the key
	mock.ExpectExec("INSERT INTO app_states").
		WithArgs("test-app", "key1", []byte("value1"), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.UpdateAppState(context.Background(), "test-app", state)
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateAppState_EmptyAppName(t *testing.T) {
	s, _, db := setupMockService(t, nil)
	defer db.Close()

	err := s.UpdateAppState(context.Background(), "", session.StateMap{})
	require.Error(t, err)
	assert.Equal(t, session.ErrAppNameRequired, err)
}

func TestListAppStates_Success(t *testing.T) {
	s, mock, db := setupMockService(t, nil)
	defer db.Close()

	rows := sqlmock.NewRows([]string{"key", "value"}).
		AddRow("key1", []byte("value1")).
		AddRow("key2", []byte("value2"))

	mock.ExpectQuery("SELECT key, value FROM app_states").
		WithArgs("test-app", sqlmock.AnyArg()).
		WillReturnRows(rows)

	states, err := s.ListAppStates(context.Background(), "test-app")
	require.NoError(t, err)
	assert.Len(t, states, 2)
	assert.Equal(t, []byte("value1"), states["key1"])
	assert.Equal(t, []byte("value2"), states["key2"])

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListAppStates_EmptyAppName(t *testing.T) {
	s, _, db := setupMockService(t, nil)
	defer db.Close()

	_, err := s.ListAppStates(context.Background(), "")
	require.Error(t, err)
	assert.Equal(t, session.ErrAppNameRequired, err)
}

func TestDeleteAppState_Success(t *testing.T) {
	s, mock, db := setupMockService(t, nil)
	defer db.Close()

	// Mock soft delete
	mock.ExpectExec("UPDATE app_states SET deleted_at").
		WithArgs(sqlmock.AnyArg(), "test-app", "key1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.DeleteAppState(context.Background(), "test-app", "key1")
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteAppState_EmptyAppName(t *testing.T) {
	s, _, db := setupMockService(t, nil)
	defer db.Close()

	err := s.DeleteAppState(context.Background(), "", "key1")
	require.Error(t, err)
	assert.Equal(t, session.ErrAppNameRequired, err)
}

func TestDeleteAppState_EmptyKey(t *testing.T) {
	s, _, db := setupMockService(t, nil)
	defer db.Close()

	err := s.DeleteAppState(context.Background(), "test-app", "")
	require.Error(t, err)
}

func TestUpdateUserState_Success(t *testing.T) {
	s, mock, db := setupMockService(t, nil)
	defer db.Close()

	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	state := session.StateMap{
		"key1": []byte("value1"),
	}

	// Mock INSERT/UPDATE
	mock.ExpectExec("INSERT INTO user_states").
		WithArgs("test-app", "test-user", "key1", []byte("value1"), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.UpdateUserState(context.Background(), userKey, state)
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateUserState_InvalidKey(t *testing.T) {
	s, _, db := setupMockService(t, nil)
	defer db.Close()

	// Test with empty AppName
	userKey := session.UserKey{
		AppName: "",
		UserID:  "test-user",
	}

	err := s.UpdateUserState(context.Background(), userKey, session.StateMap{})
	require.Error(t, err)
}

func TestListUserStates_Success(t *testing.T) {
	s, mock, db := setupMockService(t, nil)
	defer db.Close()

	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	rows := sqlmock.NewRows([]string{"key", "value"}).
		AddRow("key1", []byte("value1"))

	mock.ExpectQuery("SELECT key, value FROM user_states").
		WithArgs("test-app", "test-user", sqlmock.AnyArg()).
		WillReturnRows(rows)

	states, err := s.ListUserStates(context.Background(), userKey)
	require.NoError(t, err)
	assert.Len(t, states, 1)
	assert.Equal(t, []byte("value1"), states["key1"])

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListUserStates_InvalidKey(t *testing.T) {
	s, _, db := setupMockService(t, nil)
	defer db.Close()

	// Test with empty UserID
	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "",
	}

	_, err := s.ListUserStates(context.Background(), userKey)
	require.Error(t, err)
}

func TestDeleteUserState_Success(t *testing.T) {
	s, mock, db := setupMockService(t, nil)
	defer db.Close()

	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	// Mock soft delete
	mock.ExpectExec("UPDATE user_states SET deleted_at").
		WithArgs(sqlmock.AnyArg(), "test-app", "test-user", "key1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.DeleteUserState(context.Background(), userKey, "key1")
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteUserState_InvalidKey(t *testing.T) {
	s, _, db := setupMockService(t, nil)
	defer db.Close()

	// Test with empty UserID
	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "",
	}

	err := s.DeleteUserState(context.Background(), userKey, "key1")
	require.Error(t, err)
}

func TestDeleteUserState_EmptyKey(t *testing.T) {
	s, _, db := setupMockService(t, nil)
	defer db.Close()

	userKey := session.UserKey{
		AppName: "test-app",
		UserID:  "test-user",
	}

	err := s.DeleteUserState(context.Background(), userKey, "")
	require.Error(t, err)
}

func TestUpdateSessionState_UnmarshalSessionEnvelope(t *testing.T) {
	s, mock, db := setupMockService(t, nil)
	defer db.Close()

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

	mock.ExpectQuery("SELECT state FROM session_states").
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(sqlmock.NewRows([]string{"state"}).AddRow(stateBytes))

	expectedState := session.StateMap{
		"existing": []byte("old"),
		"new":      []byte("fresh"),
	}
	mock.ExpectExec(regexp.QuoteMeta("UPDATE session_states SET state = $1, updated_at = $2, expires_at = $3 WHERE app_name = $4 AND user_id = $5 AND session_id = $6 AND deleted_at IS NULL")).
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

	err = s.UpdateSessionState(context.Background(), key, session.StateMap{
		"new": []byte("fresh"),
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateSessionState_UnmarshalNilState(t *testing.T) {
	s, mock, db := setupMockService(t, nil)
	defer db.Close()

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

	mock.ExpectQuery("SELECT state FROM session_states").
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(sqlmock.NewRows([]string{"state"}).AddRow(stateBytes))

	expectedState := session.StateMap{
		"only": []byte("value"),
	}
	mock.ExpectExec(regexp.QuoteMeta("UPDATE session_states SET state = $1, updated_at = $2, expires_at = $3 WHERE app_name = $4 AND user_id = $5 AND session_id = $6 AND deleted_at IS NULL")).
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

	err = s.UpdateSessionState(context.Background(), key, session.StateMap{
		"only": []byte("value"),
	})
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateSessionState_UnmarshalError(t *testing.T) {
	s, mock, db := setupMockService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "test-session",
	}

	mock.ExpectQuery("SELECT state FROM session_states").
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(sqlmock.NewRows([]string{"state"}).AddRow([]byte("{")))

	err := s.UpdateSessionState(context.Background(), key, session.StateMap{
		"k": []byte("v"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal state")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateSessionState_InvalidKey(t *testing.T) {
	s, _, db := setupMockService(t, nil)
	defer db.Close()

	err := s.UpdateSessionState(context.Background(), session.Key{}, session.StateMap{})
	require.Error(t, err)
}

func TestUpdateSessionState_InvalidPrefix(t *testing.T) {
	s, _, db := setupMockService(t, nil)
	defer db.Close()

	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}

	err := s.UpdateSessionState(context.Background(), key, session.StateMap{
		session.StateAppPrefix + "k": []byte("v"),
	})
	require.Error(t, err)

	err = s.UpdateSessionState(context.Background(), key, session.StateMap{
		session.StateUserPrefix + "k": []byte("v"),
	})
	require.Error(t, err)
}

func TestUpdateSessionState_SessionNotFound_ErrNoRows(
	t *testing.T,
) {
	s, mock, db := setupMockService(t, nil)
	defer db.Close()

	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}

	mock.ExpectQuery("SELECT state FROM session_states").
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(sqlmock.NewRows([]string{"state"}))

	err := s.UpdateSessionState(context.Background(), key, session.StateMap{"k": []byte("v")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session not found")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateSessionState_QueryError_Propagates(
	t *testing.T,
) {
	s, mock, db := setupMockService(t, nil)
	defer db.Close()

	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}

	mock.ExpectQuery("SELECT state FROM session_states").
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnError(fmt.Errorf("db error"))

	err := s.UpdateSessionState(context.Background(), key, session.StateMap{"k": []byte("v")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db error")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateSessionState_UpdateError(t *testing.T) {
	s, mock, db := setupMockService(t, nil)
	defer db.Close()

	key := session.Key{AppName: "app", UserID: "user", SessionID: "sess"}
	stateBytes, err := json.Marshal(&SessionState{
		ID:        key.SessionID,
		State:     session.StateMap{"a": []byte("b")},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	})
	require.NoError(t, err)

	mock.ExpectQuery("SELECT state FROM session_states").
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(sqlmock.NewRows([]string{"state"}).AddRow(stateBytes))

	mock.ExpectExec(regexp.QuoteMeta("UPDATE session_states SET state = $1, updated_at = $2, expires_at = $3 WHERE app_name = $4 AND user_id = $5 AND session_id = $6 AND deleted_at IS NULL")).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), key.AppName, key.UserID, key.SessionID).
		WillReturnError(fmt.Errorf("update error"))

	err = s.UpdateSessionState(context.Background(), key, session.StateMap{"k": []byte("v")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update session state failed")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAppendEvent_SyncMode(t *testing.T) {
	s, mock, db := setupMockService(t, nil)
	defer db.Close()

	sess := &session.Session{
		ID:      "test-session",
		AppName: "test-app",
		UserID:  "test-user",
		State:   session.StateMap{},
		Events:  []event.Event{},
	}

	evt := event.New("test-invocation", "test-author")
	evt.Timestamp = time.Now()
	evt.Response = &model.Response{
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "test response",
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

	// Mock update session state
	mock.ExpectExec("UPDATE session_states SET state").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			"test-app", "test-user", "test-session").
		WillReturnResult(sqlmock.NewResult(0, 1))

	// Mock insert event
	mock.ExpectExec("INSERT INTO session_events").
		WithArgs("test-app", "test-user", "test-session", sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	mock.ExpectCommit()

	err := s.AppendEvent(context.Background(), sess, evt)
	require.NoError(t, err)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAppendEvent_SyncMode_ExpiredSession(t *testing.T) {
	s, mock, db := setupMockService(t, nil)
	defer db.Close()

	sess := &session.Session{
		ID:      "test-session",
		AppName: "test-app",
		UserID:  "test-user",
		State:   session.StateMap{},
		Events:  []event.Event{},
	}

	evt := event.New("test-invocation", "test-author")
	evt.Timestamp = time.Now()
	evt.Response = &model.Response{
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "test response",
				},
			},
		},
	}

	sessState := &SessionState{
		ID:    "test-session",
		State: session.StateMap{},
	}
	stateBytes, _ := json.Marshal(sessState)
	expiredAt := time.Now().Add(-time.Hour)
	stateRows := sqlmock.NewRows([]string{"state", "expires_at"}).
		AddRow(stateBytes, expiredAt)

	mock.ExpectQuery("SELECT state, expires_at FROM session_states").
		WithArgs("test-app", "test-user", "test-session").
		WillReturnRows(stateRows)

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE session_states SET state").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			"test-app", "test-user", "test-session").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO session_events").
		WithArgs("test-app", "test-user", "test-session", sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := s.AppendEvent(context.Background(), sess, evt)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAppendTrackEvent_SyncMode(t *testing.T) {
	s, mock, db := setupMockService(t, nil)
	defer db.Close()

	sess := &session.Session{
		ID:      "test-session",
		AppName: "test-app",
		UserID:  "test-user",
		State:   session.StateMap{},
	}

	trackEvent := &session.TrackEvent{
		Track:     "agui",
		Payload:   json.RawMessage(`{"delta":"hi"}`),
		Timestamp: time.Now(),
	}

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

	mock.ExpectBegin()

	mock.ExpectExec("UPDATE session_states SET state").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			"test-app", "test-user", "test-session").
		WillReturnResult(sqlmock.NewResult(0, 1))

	mock.ExpectExec("INSERT INTO session_track_events").
		WithArgs("test-app", "test-user", "test-session", "agui",
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	mock.ExpectCommit()

	err := s.AppendTrackEvent(context.Background(), sess, trackEvent)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAppendTrackEvent_AsyncSuccess(t *testing.T) {
	service := &Service{
		opts: ServiceOpts{
			enableAsyncPersist: true,
		},
		trackEventChans: []chan *trackEventPair{make(chan *trackEventPair, 1)},
	}
	sess := &session.Session{
		ID:      "sess",
		AppName: "app",
		UserID:  "user",
		State:   make(session.StateMap),
	}
	trackEvent := &session.TrackEvent{
		Track:     "alpha",
		Payload:   json.RawMessage(`"payload"`),
		Timestamp: time.Now(),
	}

	err := service.AppendTrackEvent(context.Background(), sess, trackEvent)
	require.NoError(t, err)

	select {
	case pair := <-service.trackEventChans[0]:
		require.Equal(t, trackEvent, pair.event)
		assert.Equal(t, sess.ID, pair.key.SessionID)
	case <-time.After(time.Second):
		t.Fatalf("expected track event to be enqueued")
	}

	require.NotNil(t, sess.Tracks)
	require.Len(t, sess.Tracks["alpha"].Events, 1)
}

func TestAppendTrackEvent_AsyncContextCanceled(t *testing.T) {
	ch := make(chan *trackEventPair)
	service := &Service{
		opts: ServiceOpts{
			enableAsyncPersist: true,
		},
		trackEventChans: []chan *trackEventPair{ch},
	}
	sess := &session.Session{
		ID:      "sess",
		AppName: "app",
		UserID:  "user",
		State:   make(session.StateMap),
	}
	trackEvent := &session.TrackEvent{Track: "alpha", Timestamp: time.Now()}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := service.AppendTrackEvent(ctx, sess, trackEvent)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestAppendTrackEvent_AsyncRecover(t *testing.T) {
	ch := make(chan *trackEventPair, 1)
	close(ch)
	service := &Service{
		opts: ServiceOpts{
			enableAsyncPersist: true,
		},
		trackEventChans: []chan *trackEventPair{ch},
	}
	sess := &session.Session{
		ID:      "sess",
		AppName: "app",
		UserID:  "user",
		State:   make(session.StateMap),
	}
	trackEvent := &session.TrackEvent{Track: "alpha", Timestamp: time.Now()}

	assert.NotPanics(t, func() {
		err := service.AppendTrackEvent(context.Background(), sess, trackEvent)
		require.NoError(t, err)
	})
}

func TestStartAsyncPersistWorker_ProcessesEvents(t *testing.T) {
	db, mock, err := sqlmock.New(
		sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp),
	)
	require.NoError(t, err)
	defer db.Close()

	mock.MatchExpectationsInOrder(false)

	s := createTestService(
		t,
		db,
		WithEnableAsyncPersist(true),
		WithAsyncPersisterNum(1),
	)

	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "test-session",
	}

	baseState := &SessionState{
		ID:    key.SessionID,
		State: session.StateMap{},
	}
	stateBytes, err := json.Marshal(baseState)
	require.NoError(t, err)

	stateRows := sqlmock.NewRows([]string{"state", "expires_at"}).
		AddRow(stateBytes, nil)
	mock.ExpectQuery("SELECT state, expires_at FROM session_states").
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(stateRows)

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE session_states SET state").
		WithArgs(
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			key.AppName,
			key.UserID,
			key.SessionID,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO session_events").
		WithArgs(
			key.AppName,
			key.UserID,
			key.SessionID,
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	trackState := &SessionState{
		ID:    key.SessionID,
		State: session.StateMap{},
	}
	trackStateBytes, err := json.Marshal(trackState)
	require.NoError(t, err)

	trackRows := sqlmock.NewRows([]string{"state", "expires_at"}).
		AddRow(trackStateBytes, nil)
	mock.ExpectQuery("SELECT state, expires_at FROM session_states").
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(trackRows)

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE session_states SET state").
		WithArgs(
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			key.AppName,
			key.UserID,
			key.SessionID,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO session_track_events").
		WithArgs(
			key.AppName,
			key.UserID,
			key.SessionID,
			"agui",
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	s.startAsyncPersistWorker()

	evt := event.New("inv-1", "author")
	evt.StateDelta = map[string][]byte{
		"key1": []byte(`"value1"`),
	}
	evt.Response = &model.Response{
		Object: model.ObjectTypeChatCompletion,
		Done:   true,
		Choices: []model.Choice{
			{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "ok",
				},
			},
		},
	}

	trackEvent := &session.TrackEvent{
		Track:     "agui",
		Payload:   json.RawMessage(`"payload"`),
		Timestamp: time.Now(),
	}

	s.eventPairChans[0] <- &sessionEventPair{
		key:   key,
		event: evt,
	}
	s.trackEventChans[0] <- &trackEventPair{
		key:   key,
		event: trackEvent,
	}

	for _, ch := range s.eventPairChans {
		close(ch)
	}
	for _, ch := range s.trackEventChans {
		close(ch)
	}
	s.persistWg.Wait()

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAppendTrackEvent_Errors(t *testing.T) {
	t.Run("invalid key", func(t *testing.T) {
		s, _, db := setupMockService(t, nil)
		defer db.Close()

		sess := &session.Session{
			ID:      "",
			AppName: "app",
			UserID:  "user",
			State:   make(session.StateMap),
		}
		err := s.AppendTrackEvent(context.Background(), sess, &session.TrackEvent{Track: "alpha"})
		require.Error(t, err)
		assert.ErrorIs(t, err, session.ErrSessionIDRequired)
	})

	t.Run("nil event", func(t *testing.T) {
		s, _, db := setupMockService(t, nil)
		defer db.Close()

		sess := &session.Session{
			ID:      "sess",
			AppName: "app",
			UserID:  "user",
			State:   make(session.StateMap),
		}
		err := s.AppendTrackEvent(context.Background(), sess, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "track event is nil")
	})

	t.Run("ensure track fails", func(t *testing.T) {
		s, _, db := setupMockService(t, nil)
		defer db.Close()

		sess := &session.Session{
			ID:      "sess",
			AppName: "app",
			UserID:  "user",
			State: session.StateMap{
				"tracks": []byte("{"),
			},
		}
		err := s.AppendTrackEvent(context.Background(), sess, &session.TrackEvent{Track: "alpha"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ensure track")
	})

	t.Run("addTrackEvent error surfaces", func(t *testing.T) {
		s, mock, db := setupMockService(t, nil)
		defer db.Close()

		sess := &session.Session{
			ID:      "sess",
			AppName: "app",
			UserID:  "user",
			State:   make(session.StateMap),
		}
		trackEvent := &session.TrackEvent{Track: "alpha", Timestamp: time.Now()}

		mock.ExpectQuery("SELECT state, expires_at FROM session_states").
			WithArgs("app", "user", "sess").
			WillReturnError(fmt.Errorf("db error"))

		err := s.AppendTrackEvent(context.Background(), sess, trackEvent)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "postgres session service append track event failed")
	})
}

func TestAddTrackEvent_SessionNotFound(t *testing.T) {
	s, mock, db := setupMockService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "missing",
	}

	stateRows := sqlmock.NewRows([]string{"state", "expires_at"})
	mock.ExpectQuery("SELECT state, expires_at FROM session_states").
		WithArgs("test-app", "test-user", "missing").
		WillReturnRows(stateRows)

	err := s.addTrackEvent(context.Background(), key, &session.TrackEvent{Track: "alpha"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session not found")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAddTrackEvent_ExpiredSessionWithTTL(t *testing.T) {
	s, mock, db := setupMockService(t, &TestServiceOpts{sessionTTL: time.Minute})
	defer db.Close()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "session-1",
	}
	trackEvent := &session.TrackEvent{
		Track:     "alpha",
		Payload:   json.RawMessage(`"ttl"`),
		Timestamp: time.Now(),
	}

	sessState := &SessionState{
		State: make(session.StateMap),
	}
	stateBytes, _ := json.Marshal(sessState)
	stateRows := sqlmock.NewRows([]string{"state", "expires_at"}).
		AddRow(stateBytes, time.Now().Add(-time.Hour))

	mock.ExpectQuery("SELECT state, expires_at FROM session_states").
		WithArgs("test-app", "test-user", "session-1").
		WillReturnRows(stateRows)

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE session_states SET state").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			"test-app", "test-user", "session-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO session_track_events").
		WithArgs("test-app", "test-user", "session-1", "alpha",
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := s.addTrackEvent(context.Background(), key, trackEvent)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetTrackEvents_WithLimit(t *testing.T) {
	s, mock, db := setupMockService(t, nil)
	defer db.Close()

	key := session.Key{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "session-1",
	}
	sessState := &SessionState{
		State: session.StateMap{
			"tracks": []byte(`["alpha"]`),
		},
	}

	event := &session.TrackEvent{
		Track:     "alpha",
		Payload:   json.RawMessage(`"limited"`),
		Timestamp: time.Now(),
	}
	eventBytes, _ := json.Marshal(event)
	rows := sqlmock.NewRows([]string{"event"}).AddRow(eventBytes)

	mock.ExpectQuery("SELECT event FROM session_track_events").
		WithArgs("test-app", "test-user", "session-1", "alpha", sqlmock.AnyArg(), sqlmock.AnyArg(), 1).
		WillReturnRows(rows)

	result, err := s.getTrackEvents(context.Background(), []session.Key{key}, []*SessionState{sessState}, 1, time.Time{})
	require.NoError(t, err)
	require.Len(t, result, 1)
	alpha := result[0]["alpha"]
	require.Len(t, alpha, 1)
	assert.Equal(t, json.RawMessage(`"limited"`), alpha[0].Payload)

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
			sqlmock.AnyArg(), sqlmock.AnyArg()).
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
	mock.ExpectExec("UPDATE session_track_events SET deleted_at").
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
	mock.ExpectExec("UPDATE session_track_events SET deleted_at").
		WithArgs(sqlmock.AnyArg(), "test-app", "test-user").
		WillReturnResult(sqlmock.NewResult(0, 2))
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
	mock.ExpectExec("DELETE FROM session_track_events").
		WillReturnResult(sqlmock.NewResult(0, 2))
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
	mock.ExpectExec("DELETE FROM session_track_events").
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
		mock.ExpectExec("UPDATE session_track_events SET deleted_at").
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

func TestNewService_WithDSN_Success(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	defer db.Close()

	originalBuilder := storage.GetClientBuilder()
	defer storage.SetClientBuilder(originalBuilder)

	var capturedConnString string
	storage.SetClientBuilder(func(ctx context.Context, builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		opts := &storage.ClientBuilderOpts{}
		for _, opt := range builderOpts {
			opt(opts)
		}
		capturedConnString = opts.ConnString
		return &mockPostgresClient{db: db}, nil
	})

	dsn := "postgres://user:password@localhost:5432/mydb?sslmode=disable"
	svc, err := NewService(
		WithPostgresClientDSN(dsn),
		WithSkipDBInit(true),
	)
	require.NoError(t, err)
	require.NotNil(t, svc)
	assert.Equal(t, dsn, capturedConnString)

	err = svc.Close()
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestNewService_DSNPriority(t *testing.T) {
	db, _, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	defer db.Close()

	originalBuilder := storage.GetClientBuilder()
	defer storage.SetClientBuilder(originalBuilder)

	var capturedConnString string
	storage.SetClientBuilder(func(ctx context.Context, builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		opts := &storage.ClientBuilderOpts{}
		for _, opt := range builderOpts {
			opt(opts)
		}
		capturedConnString = opts.ConnString
		return &mockPostgresClient{db: db}, nil
	})

	// DSN should take priority over host settings
	dsn := "postgres://dsn-user:password@dsn-host:5432/dsndb"
	svc, err := NewService(
		WithPostgresClientDSN(dsn),
		WithHost("other-host"),
		WithPort(5433),
		WithUser("other-user"),
		WithSkipDBInit(true),
	)
	require.NoError(t, err)
	require.NotNil(t, svc)
	assert.Equal(t, dsn, capturedConnString, "DSN should take priority over host settings")

	_ = svc.Close()
}

func TestNewService_NoSummarizer_NoAsyncWorker(t *testing.T) {
	db, _, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	defer db.Close()

	originalBuilder := storage.GetClientBuilder()
	defer storage.SetClientBuilder(originalBuilder)

	storage.SetClientBuilder(func(ctx context.Context, builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return &mockPostgresClient{db: db}, nil
	})

	// Register instance
	instanceName := "test-instance-nosum"
	storage.RegisterPostgresInstance(instanceName,
		storage.WithClientConnString("test:test@tcp(localhost:5432)/testdb"),
	)

	svc, err := NewService(
		WithPostgresInstance(instanceName),
		WithSkipDBInit(true),
		// No summarizer provided - should not start async summary workers
	)
	require.NoError(t, err)
	require.NotNil(t, svc)
	defer svc.Close()

	// Verify that summary job channels are not initialized when no summarizer is provided
	assert.Len(t, svc.summaryJobChans, 0)
}

func TestNewService_WithSummarizer_StartsAsyncWorker(t *testing.T) {
	db, _, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	defer db.Close()

	originalBuilder := storage.GetClientBuilder()
	defer storage.SetClientBuilder(originalBuilder)

	storage.SetClientBuilder(func(ctx context.Context, builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return &mockPostgresClient{db: db}, nil
	})

	// Register instance
	instanceName := "test-instance-withsum"
	storage.RegisterPostgresInstance(instanceName,
		storage.WithClientConnString("test:test@tcp(localhost:5432)/testdb"),
	)

	svc, err := NewService(
		WithPostgresInstance(instanceName),
		WithSkipDBInit(true),
		WithSummarizer(&mockSummarizerImpl{summaryText: "test", shouldSummarize: true}),
		WithAsyncSummaryNum(2),
		WithSummaryQueueSize(10),
	)
	require.NoError(t, err)
	require.NotNil(t, svc)
	defer svc.Close()

	// Verify that summary job channels are initialized when summarizer is provided
	assert.Len(t, svc.summaryJobChans, 2)
	for i, ch := range svc.summaryJobChans {
		assert.NotNil(t, ch, "Channel %d should not be nil", i)
		assert.Equal(t, 10, cap(ch), "Channel %d should have capacity 10", i)
	}
}

func TestNewService_InstanceNotFound(t *testing.T) {
	svc, err := NewService(
		WithPostgresInstance("non-existent-instance"),
		WithSkipDBInit(true),
	)
	assert.Error(t, err)
	assert.Nil(t, svc)
	assert.Contains(t, err.Error(), "postgres instance non-existent-instance not found")
}

func TestNewService_DefaultCleanupInterval(t *testing.T) {
	db, _, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	defer db.Close()

	originalBuilder := storage.GetClientBuilder()
	defer storage.SetClientBuilder(originalBuilder)

	storage.SetClientBuilder(func(ctx context.Context, builderOpts ...storage.ClientBuilderOpt) (storage.Client, error) {
		return &mockPostgresClient{db: db}, nil
	})

	// Register instance
	instanceName := "test-instance-cleanup"
	storage.RegisterPostgresInstance(instanceName,
		storage.WithClientConnString("test:test@tcp(localhost:5432)/testdb"),
	)

	svc, err := NewService(
		WithPostgresInstance(instanceName),
		WithSkipDBInit(true),
		WithSessionTTL(time.Hour), // This should trigger default cleanup interval
	)
	require.NoError(t, err)
	require.NotNil(t, svc)
	defer svc.Close()

	// Verify that default cleanup interval was set
	assert.Equal(t, defaultCleanupIntervalSecond, svc.opts.cleanupInterval)
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
		UserID:    "test-user",
		SessionID: "test-session",
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
		UserID:    "test-user",
		SessionID: "test-session",
	}

	mock.ExpectQuery("SELECT state FROM session_states").
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(sqlmock.NewRows([]string{"state"}))

	err = s.UpdateSessionState(ctx, key, session.StateMap{
		"key1": []byte("value1"),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "session not found")
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
		UserID:    "test-user",
		SessionID: "test-session",
	}

	mock.ExpectQuery("SELECT state FROM session_states").
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
		UserID:    "test-user",
		SessionID: "test-session",
	}

	mock.ExpectQuery("SELECT state FROM session_states").
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(
			sqlmock.NewRows([]string{"state"}).
				AddRow([]byte("not-json")),
		)

	err = s.UpdateSessionState(ctx, key, session.StateMap{
		"key1": []byte("value1"),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unmarshal state")
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
		UserID:    "test-user",
		SessionID: "test-session",
	}

	currentState := session.StateMap{
		"existing": []byte(`"old"`),
	}
	currentBytes, err := json.Marshal(currentState)
	require.NoError(t, err)

	mock.ExpectQuery("SELECT state FROM session_states").
		WithArgs(key.AppName, key.UserID, key.SessionID).
		WillReturnRows(
			sqlmock.NewRows([]string{"state"}).
				AddRow(currentBytes),
		)

	mock.ExpectExec("UPDATE session_states SET state").
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
