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
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/storage/postgres"
)

// mockPostgresClient wraps sql.DB to implement postgres.Client for testing
type mockPostgresClient struct {
	db *sql.DB
}

func (c *mockPostgresClient) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return c.db.ExecContext(ctx, query, args...)
}

func (c *mockPostgresClient) Query(ctx context.Context, handler postgres.HandlerFunc, query string, args ...any) error {
	rows, err := c.db.QueryContext(ctx, query, args...)
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

func (c *mockPostgresClient) Transaction(ctx context.Context, fn postgres.TxFunc) error {
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

// mockSummarizer is a mock summarizer for testing
type mockSummarizer struct {
	summaryText     string
	err             error
	shouldSummarize bool
}

func (m *mockSummarizer) ShouldSummarize(sess *session.Session) bool {
	return m.shouldSummarize
}

func (m *mockSummarizer) Summarize(ctx context.Context, sess *session.Session) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.summaryText, nil
}

func (m *mockSummarizer) Metadata() map[string]any {
	return map[string]any{}
}

// setupMockService creates a Service with mocked postgres client
func setupMockService(t *testing.T, summarizer *mockSummarizer) (*Service, sqlmock.Sqlmock, *sql.DB) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)

	client := &mockPostgresClient{db: db}

	s := &Service{
		pgClient:   client,
		sessionTTL: 0,
		opts: ServiceOpts{
			summarizer: summarizer,
			softDelete: true, // Enable soft delete by default
		},
		summaryJobChans: make([]chan *summaryJob, 3),
	}

	// Initialize summary job channels
	for i := range s.summaryJobChans {
		s.summaryJobChans[i] = make(chan *summaryJob, 10)
	}

	return s, mock, db
}

func TestCreateSessionSummary_NoSummarizer(t *testing.T) {
	// Create service without summarizer (pass nil)
	s := &Service{
		opts: ServiceOpts{
			summarizer: nil,
		},
	}

	sess := &session.Session{
		ID:      "test-session",
		AppName: "test-app",
		UserID:  "test-user",
	}

	err := s.CreateSessionSummary(context.Background(), sess, "", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "summarizer not configured")
}

func TestCreateSessionSummary_InvalidKey(t *testing.T) {
	summarizer := &mockSummarizer{summaryText: "test summary", shouldSummarize: true}
	s, _, db := setupMockService(t, summarizer)
	defer db.Close()

	// Test with empty session ID
	sess := &session.Session{
		ID:      "",
		AppName: "test-app",
		UserID:  "test-user",
	}

	err := s.CreateSessionSummary(context.Background(), sess, "", false)
	require.Error(t, err)
}

func TestCreateSessionSummary_ExistingSummaryRecent(t *testing.T) {
	summarizer := &mockSummarizer{summaryText: "new summary", shouldSummarize: true}
	s, mock, db := setupMockService(t, summarizer)
	defer db.Close()

	sess := &session.Session{
		ID:        "test-session",
		AppName:   "test-app",
		UserID:    "test-user",
		UpdatedAt: time.Now(),
	}

	// Mock existing summary query
	existingSummary := session.Summary{
		Summary:   "existing summary",
		UpdatedAt: time.Now().Add(-30 * time.Second), // 30 seconds ago
	}
	summaryBytes, _ := json.Marshal(existingSummary)

	rows := sqlmock.NewRows([]string{"summary", "updated_at"}).
		AddRow(summaryBytes, existingSummary.UpdatedAt)

	mock.ExpectQuery("SELECT summary, updated_at FROM session_summaries").
		WithArgs("test-app", "test-user", "test-session", "", sqlmock.AnyArg()).
		WillReturnRows(rows)

	err := s.CreateSessionSummary(context.Background(), sess, "", false)
	require.NoError(t, err)

	// Verify expectations
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateSessionSummary_CreateNewSummary(t *testing.T) {
	summarizer := &mockSummarizer{summaryText: "new summary", shouldSummarize: true}
	s, mock, db := setupMockService(t, summarizer)
	defer db.Close()

	sess := &session.Session{
		ID:        "test-session",
		AppName:   "test-app",
		UserID:    "test-user",
		UpdatedAt: time.Now(),
	}

	// Mock no existing summary
	rows := sqlmock.NewRows([]string{"summary", "updated_at"})
	mock.ExpectQuery("SELECT summary, updated_at FROM session_summaries").
		WithArgs("test-app", "test-user", "test-session", "", sqlmock.AnyArg()).
		WillReturnRows(rows)

	// Mock upsert: first UPDATE (returns 0 rows), then INSERT
	mock.ExpectExec("UPDATE session_summaries").
		WithArgs("test-app", "test-user", "test-session", "", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))

	mock.ExpectExec("INSERT INTO session_summaries").
		WithArgs("test-app", "test-user", "test-session", "", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.CreateSessionSummary(context.Background(), sess, "", false)
	require.NoError(t, err)

	// Verify expectations
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateSessionSummary_WithTTL(t *testing.T) {
	summarizer := &mockSummarizer{summaryText: "new summary", shouldSummarize: true}
	s, mock, db := setupMockService(t, summarizer)
	s.sessionTTL = 1 * time.Hour
	defer db.Close()

	sess := &session.Session{
		ID:        "test-session",
		AppName:   "test-app",
		UserID:    "test-user",
		UpdatedAt: time.Now(),
	}

	// Mock no existing summary
	rows := sqlmock.NewRows([]string{"summary", "updated_at"})
	mock.ExpectQuery("SELECT summary, updated_at FROM session_summaries").
		WithArgs("test-app", "test-user", "test-session", "", sqlmock.AnyArg()).
		WillReturnRows(rows)

	// Mock upsert: first UPDATE (returns 0 rows), then INSERT
	mock.ExpectExec("UPDATE session_summaries").
		WithArgs("test-app", "test-user", "test-session", "", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))

	mock.ExpectExec("INSERT INTO session_summaries").
		WithArgs("test-app", "test-user", "test-session", "", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.CreateSessionSummary(context.Background(), sess, "", false)
	require.NoError(t, err)

	// Verify expectations
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateSessionSummary_ForcedUpdate(t *testing.T) {
	summarizer := &mockSummarizer{summaryText: "forced summary", shouldSummarize: true}
	s, mock, db := setupMockService(t, summarizer)
	defer db.Close()

	sess := &session.Session{
		ID:        "test-session",
		AppName:   "test-app",
		UserID:    "test-user",
		UpdatedAt: time.Now(),
	}

	// When force=true, should skip checking existing summary
	// and directly create new one (upsert: UPDATE then INSERT)
	mock.ExpectExec("UPDATE session_summaries").
		WithArgs("test-app", "test-user", "test-session", "", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))

	mock.ExpectExec("INSERT INTO session_summaries").
		WithArgs("test-app", "test-user", "test-session", "", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.CreateSessionSummary(context.Background(), sess, "", true)
	require.NoError(t, err)

	// Verify expectations
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateSessionSummary_SummarizerError(t *testing.T) {
	summarizer := &mockSummarizer{err: fmt.Errorf("summarizer error"), shouldSummarize: true}
	s, mock, db := setupMockService(t, summarizer)
	defer db.Close()

	sess := &session.Session{
		ID:        "test-session",
		AppName:   "test-app",
		UserID:    "test-user",
		UpdatedAt: time.Now(),
	}

	// Mock no existing summary
	rows := sqlmock.NewRows([]string{"summary", "updated_at"})
	mock.ExpectQuery("SELECT summary, updated_at FROM session_summaries").
		WithArgs("test-app", "test-user", "test-session", "", sqlmock.AnyArg()).
		WillReturnRows(rows)

	err := s.CreateSessionSummary(context.Background(), sess, "", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "generate summary failed")

	// Verify expectations
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEnqueueSummaryJob_NoSummarizer(t *testing.T) {
	// Create service without summarizer (pass nil)
	s := &Service{
		opts: ServiceOpts{
			summarizer: nil,
		},
	}

	sess := &session.Session{
		ID:      "test-session",
		AppName: "test-app",
		UserID:  "test-user",
	}

	err := s.EnqueueSummaryJob(context.Background(), sess, "", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "summarizer not configured")
}

func TestEnqueueSummaryJob_InvalidKey(t *testing.T) {
	summarizer := &mockSummarizer{summaryText: "test summary", shouldSummarize: true}
	s, _, db := setupMockService(t, summarizer)
	defer db.Close()

	// Test with empty session ID
	sess := &session.Session{
		ID:      "",
		AppName: "test-app",
		UserID:  "test-user",
	}

	err := s.EnqueueSummaryJob(context.Background(), sess, "", false)
	require.Error(t, err)
}

func TestEnqueueSummaryJob_Success(t *testing.T) {
	summarizer := &mockSummarizer{summaryText: "test summary", shouldSummarize: true}
	s, _, db := setupMockService(t, summarizer)
	defer db.Close()

	sess := &session.Session{
		ID:      "test-session",
		AppName: "test-app",
		UserID:  "test-user",
	}

	err := s.EnqueueSummaryJob(context.Background(), sess, "", false)
	require.NoError(t, err)

	// Verify job was enqueued
	time.Sleep(10 * time.Millisecond)
}

func TestEnqueueSummaryJob_ContextCanceled(t *testing.T) {
	summarizer := &mockSummarizer{summaryText: "test summary", shouldSummarize: true}
	s, _, db := setupMockService(t, summarizer)
	defer db.Close()

	// Fill up all channels first
	for _, ch := range s.summaryJobChans {
		for i := 0; i < cap(ch); i++ {
			ch <- &summaryJob{}
		}
	}

	sess := &session.Session{
		ID:      "test-session",
		AppName: "test-app",
		UserID:  "test-user",
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := s.EnqueueSummaryJob(ctx, sess, "", false)
	require.Error(t, err)
	assert.Equal(t, context.Canceled, err)
}

func TestEnqueueSummaryJob_QueueFullFallbackToSync(t *testing.T) {
	summarizer := &mockSummarizer{summaryText: "test summary", shouldSummarize: true}
	s, mock, db := setupMockService(t, summarizer)
	defer db.Close()

	// Fill up all channels
	for _, ch := range s.summaryJobChans {
		for i := 0; i < cap(ch); i++ {
			ch <- &summaryJob{}
		}
	}

	sess := &session.Session{
		ID:        "test-session",
		AppName:   "test-app",
		UserID:    "test-user",
		UpdatedAt: time.Now(),
	}

	// Mock for sync fallback
	rows := sqlmock.NewRows([]string{"summary", "updated_at"})
	mock.ExpectQuery("SELECT summary, updated_at FROM session_summaries").
		WithArgs("test-app", "test-user", "test-session", "", sqlmock.AnyArg()).
		WillReturnRows(rows)

	// Mock upsert: first UPDATE (returns 0 rows), then INSERT
	mock.ExpectExec("UPDATE session_summaries").
		WithArgs("test-app", "test-user", "test-session", "", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))

	mock.ExpectExec("INSERT INTO session_summaries").
		WithArgs("test-app", "test-user", "test-session", "", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.EnqueueSummaryJob(context.Background(), sess, "", false)
	require.NoError(t, err)

	// Verify expectations
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEnqueueSummaryJob_ChannelClosedPanicRecovery(t *testing.T) {
	summarizer := &mockSummarizer{summaryText: "test summary", shouldSummarize: true}
	s, _, db := setupMockService(t, summarizer)
	defer db.Close()

	// Close all channels to trigger panic
	for _, ch := range s.summaryJobChans {
		close(ch)
	}

	sess := &session.Session{
		ID:        "test-session",
		AppName:   "test-app",
		UserID:    "test-user",
		UpdatedAt: time.Now(),
	}

	// Should not panic, should log error and return without falling back to sync
	// because the panic is recovered in EnqueueSummaryJob
	err := s.EnqueueSummaryJob(context.Background(), sess, "", false)
	// The function should not return error, it just logs the error
	require.NoError(t, err)
}

func TestGetSessionSummaryText_Success(t *testing.T) {
	// GetSessionSummaryText doesn't need summarizer
	s, mock, db := setupMockService(t, &mockSummarizer{shouldSummarize: false})
	defer db.Close()

	sess := &session.Session{
		ID:      "test-session",
		AppName: "test-app",
		UserID:  "test-user",
	}

	// Mock summary query
	summary := session.Summary{
		Summary: "test summary text",
	}
	summaryBytes, _ := json.Marshal(summary)

	rows := sqlmock.NewRows([]string{"summary"}).
		AddRow(summaryBytes)

	mock.ExpectQuery("SELECT summary FROM session_summaries").
		WithArgs("test-app", "test-user", "test-session", "", sqlmock.AnyArg()).
		WillReturnRows(rows)

	text, ok := s.GetSessionSummaryText(context.Background(), sess)
	require.True(t, ok)
	assert.Equal(t, "test summary text", text)

	// Verify expectations
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSessionSummaryText_NoSummary(t *testing.T) {
	s, mock, db := setupMockService(t, &mockSummarizer{shouldSummarize: false})
	defer db.Close()

	sess := &session.Session{
		ID:      "test-session",
		AppName: "test-app",
		UserID:  "test-user",
	}

	// Mock empty result
	rows := sqlmock.NewRows([]string{"summary"})
	mock.ExpectQuery("SELECT summary FROM session_summaries").
		WithArgs("test-app", "test-user", "test-session", "", sqlmock.AnyArg()).
		WillReturnRows(rows)

	text, ok := s.GetSessionSummaryText(context.Background(), sess)
	require.False(t, ok)
	assert.Empty(t, text)

	// Verify expectations
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSessionSummaryText_InvalidKey(t *testing.T) {
	s := &Service{
		opts: ServiceOpts{},
	}

	// Test with empty session ID
	sess := &session.Session{
		ID:      "",
		AppName: "test-app",
		UserID:  "test-user",
	}

	text, ok := s.GetSessionSummaryText(context.Background(), sess)
	require.False(t, ok)
	assert.Empty(t, text)
}

func TestGetSessionSummaryText_QueryError(t *testing.T) {
	s, mock, db := setupMockService(t, &mockSummarizer{shouldSummarize: false})
	defer db.Close()

	sess := &session.Session{
		ID:      "test-session",
		AppName: "test-app",
		UserID:  "test-user",
	}

	// Mock query error
	mock.ExpectQuery("SELECT summary FROM session_summaries").
		WithArgs("test-app", "test-user", "test-session", "", sqlmock.AnyArg()).
		WillReturnError(fmt.Errorf("database error"))

	text, ok := s.GetSessionSummaryText(context.Background(), sess)
	require.False(t, ok)
	assert.Empty(t, text)

	// Verify expectations
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSessionSummaryText_EmptySummaryText(t *testing.T) {
	s, mock, db := setupMockService(t, &mockSummarizer{shouldSummarize: false})
	defer db.Close()

	sess := &session.Session{
		ID:      "test-session",
		AppName: "test-app",
		UserID:  "test-user",
	}

	// Mock summary with empty text
	summary := session.Summary{
		Summary: "",
	}
	summaryBytes, _ := json.Marshal(summary)

	rows := sqlmock.NewRows([]string{"summary"}).
		AddRow(summaryBytes)

	mock.ExpectQuery("SELECT summary FROM session_summaries").
		WithArgs("test-app", "test-user", "test-session", "", sqlmock.AnyArg()).
		WillReturnRows(rows)

	text, ok := s.GetSessionSummaryText(context.Background(), sess)
	require.False(t, ok)
	assert.Empty(t, text)

	// Verify expectations
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateSessionSummary_WithFilterKey(t *testing.T) {
	summarizer := &mockSummarizer{summaryText: "filtered summary", shouldSummarize: true}
	s, mock, db := setupMockService(t, summarizer)
	defer db.Close()

	sess := &session.Session{
		ID:        "test-session",
		AppName:   "test-app",
		UserID:    "test-user",
		UpdatedAt: time.Now(),
	}

	// Mock no existing summary
	rows := sqlmock.NewRows([]string{"summary", "updated_at"})
	mock.ExpectQuery("SELECT summary, updated_at FROM session_summaries").
		WithArgs("test-app", "test-user", "test-session", "filter1", sqlmock.AnyArg()).
		WillReturnRows(rows)

	// Mock upsert: first UPDATE (returns 0 rows), then INSERT
	mock.ExpectExec("UPDATE session_summaries").
		WithArgs("test-app", "test-user", "test-session", "filter1", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))

	mock.ExpectExec("INSERT INTO session_summaries").
		WithArgs("test-app", "test-user", "test-session", "filter1", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.CreateSessionSummary(context.Background(), sess, "filter1", false)
	require.NoError(t, err)

	// Verify expectations
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateSessionSummary_UnmarshalError(t *testing.T) {
	summarizer := &mockSummarizer{summaryText: "new summary", shouldSummarize: true}
	s, mock, db := setupMockService(t, summarizer)
	defer db.Close()

	sess := &session.Session{
		ID:        "test-session",
		AppName:   "test-app",
		UserID:    "test-user",
		UpdatedAt: time.Now(),
	}

	// Mock existing summary with invalid JSON
	rows := sqlmock.NewRows([]string{"summary", "updated_at"}).
		AddRow([]byte("invalid json"), time.Now())

	mock.ExpectQuery("SELECT summary, updated_at FROM session_summaries").
		WithArgs("test-app", "test-user", "test-session", "", sqlmock.AnyArg()).
		WillReturnRows(rows)

	err := s.CreateSessionSummary(context.Background(), sess, "", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal summary failed")

	// Verify expectations
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSessionSummaryText_UnmarshalError(t *testing.T) {
	s, mock, db := setupMockService(t, &mockSummarizer{shouldSummarize: false})
	defer db.Close()

	sess := &session.Session{
		ID:      "test-session",
		AppName: "test-app",
		UserID:  "test-user",
	}

	// Mock summary with invalid JSON
	rows := sqlmock.NewRows([]string{"summary"}).
		AddRow([]byte("invalid json"))

	mock.ExpectQuery("SELECT summary FROM session_summaries").
		WithArgs("test-app", "test-user", "test-session", "", sqlmock.AnyArg()).
		WillReturnRows(rows)

	text, ok := s.GetSessionSummaryText(context.Background(), sess)
	require.False(t, ok)
	assert.Empty(t, text)

	// Verify expectations
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestEnqueueSummaryJob_HashDistribution(t *testing.T) {
	summarizer := &mockSummarizer{summaryText: "test summary", shouldSummarize: true}
	s, _, db := setupMockService(t, summarizer)
	defer db.Close()

	// Test that different sessions are distributed across channels
	sessions := []*session.Session{
		{ID: "session1", AppName: "app1", UserID: "user1"},
		{ID: "session2", AppName: "app2", UserID: "user2"},
		{ID: "session3", AppName: "app3", UserID: "user3"},
	}

	for _, sess := range sessions {
		err := s.EnqueueSummaryJob(context.Background(), sess, "", false)
		require.NoError(t, err)
	}

	// Give some time for jobs to be enqueued
	time.Sleep(10 * time.Millisecond)

	// Verify that jobs were distributed (at least one channel should have jobs)
	totalJobs := 0
	for _, ch := range s.summaryJobChans {
		totalJobs += len(ch)
	}
	assert.Equal(t, 3, totalJobs)
}
