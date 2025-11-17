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
	"encoding/json"
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestCreateSessionSummary_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	summarizer := &mockSummarizer{
		summarizeFunc: func(ctx context.Context, sess *session.Session) (string, error) {
			return "This is a test summary", nil
		},
	}

	s := createTestService(t, db, WithSessionTTL(1*time.Hour), WithSummarizer(summarizer))
	ctx := context.Background()

	sess := &session.Session{
		ID:        "session-123",
		AppName:   "test-app",
		UserID:    "user-456",
		UpdatedAt: time.Now(),
	}

	// Mock: Check if summary exists (no existing summary)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT summary, updated_at FROM session_summaries")).
		WithArgs(sess.AppName, sess.UserID, sess.ID, "", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"summary", "updated_at"}))

	// Mock: Insert new summary
	mock.ExpectExec(regexp.QuoteMeta("REPLACE INTO session_summaries")).
		WithArgs(
			sess.AppName,
			sess.UserID,
			sess.ID,
			"",
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = s.CreateSessionSummary(ctx, sess, "", false)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateSessionSummary_AlreadyExists(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	summarizer := &mockSummarizer{}
	s := createTestService(t, db, WithSummarizer(summarizer))
	ctx := context.Background()

	sess := &session.Session{
		ID:        "session-123",
		AppName:   "test-app",
		UserID:    "user-456",
		UpdatedAt: time.Now(),
	}

	// Existing summary created just now (within 1 minute)
	existingSummary := session.Summary{
		Summary:   "Existing summary",
		Topics:    []string{},
		UpdatedAt: time.Now(),
	}
	summaryBytes, _ := json.Marshal(existingSummary)

	// Mock: Check if summary exists
	mock.ExpectQuery(regexp.QuoteMeta("SELECT summary, updated_at FROM session_summaries")).
		WithArgs(sess.AppName, sess.UserID, sess.ID, "", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"summary", "updated_at"}).
			AddRow(summaryBytes, existingSummary.UpdatedAt))

	// Should not generate new summary
	err = s.CreateSessionSummary(ctx, sess, "", false)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateSessionSummary_Force(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	summarizer := &mockSummarizer{
		summarizeFunc: func(ctx context.Context, sess *session.Session) (string, error) {
			return "Forced summary", nil
		},
	}

	s := createTestService(t, db, WithSummarizer(summarizer))
	ctx := context.Background()

	sess := &session.Session{
		ID:        "session-123",
		AppName:   "test-app",
		UserID:    "user-456",
		UpdatedAt: time.Now(),
	}

	// With force=true, skip checking existing summary
	mock.ExpectExec(regexp.QuoteMeta("REPLACE INTO session_summaries")).
		WithArgs(
			sess.AppName,
			sess.UserID,
			sess.ID,
			"",
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = s.CreateSessionSummary(ctx, sess, "", true)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateSessionSummary_NoSummarizer(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db) // No summarizer
	ctx := context.Background()

	sess := &session.Session{
		ID:      "session-123",
		AppName: "test-app",
		UserID:  "user-456",
	}

	err = s.CreateSessionSummary(ctx, sess, "", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "summarizer not configured")
}

func TestCreateSessionSummary_InvalidKey(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	summarizer := &mockSummarizer{}
	s := createTestService(t, db, WithSummarizer(summarizer))
	ctx := context.Background()

	sess := &session.Session{
		ID:      "", // Invalid: empty session ID
		AppName: "test-app",
		UserID:  "user-456",
	}

	err = s.CreateSessionSummary(ctx, sess, "", false)
	assert.Error(t, err)
}

func TestCreateSessionSummary_GenerateError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	summarizer := &mockSummarizer{
		summarizeFunc: func(ctx context.Context, sess *session.Session) (string, error) {
			return "", fmt.Errorf("summarization error")
		},
	}

	s := createTestService(t, db, WithSummarizer(summarizer))
	ctx := context.Background()

	sess := &session.Session{
		ID:        "session-123",
		AppName:   "test-app",
		UserID:    "user-456",
		UpdatedAt: time.Now(),
	}

	// Mock: Check existing summary
	mock.ExpectQuery(regexp.QuoteMeta("SELECT summary, updated_at FROM session_summaries")).
		WithArgs(sess.AppName, sess.UserID, sess.ID, "", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"summary", "updated_at"}))

	err = s.CreateSessionSummary(ctx, sess, "", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "summarization error")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSessionSummaryText_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	sess := &session.Session{
		ID:      "session-123",
		AppName: "test-app",
		UserID:  "user-456",
	}

	summary := session.Summary{
		Summary: "Test summary text",
		Topics:  []string{},
	}
	summaryBytes, _ := json.Marshal(summary)

	// Mock: Query summary
	mock.ExpectQuery(regexp.QuoteMeta("SELECT summary FROM session_summaries")).
		WithArgs(sess.AppName, sess.UserID, sess.ID, "", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"summary"}).
			AddRow(summaryBytes))

	text, found := s.GetSessionSummaryText(ctx, sess)
	assert.True(t, found)
	assert.Equal(t, "Test summary text", text)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSessionSummaryText_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	sess := &session.Session{
		ID:      "session-123",
		AppName: "test-app",
		UserID:  "user-456",
	}

	// Mock: Query returns no rows
	mock.ExpectQuery(regexp.QuoteMeta("SELECT summary FROM session_summaries")).
		WithArgs(sess.AppName, sess.UserID, sess.ID, "", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"summary"}))

	text, found := s.GetSessionSummaryText(ctx, sess)
	assert.False(t, found)
	assert.Empty(t, text)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSessionSummaryText_InvalidKey(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	sess := &session.Session{
		ID:      "", // Invalid: empty session ID
		AppName: "test-app",
		UserID:  "user-456",
	}

	text, found := s.GetSessionSummaryText(ctx, sess)
	assert.False(t, found)
	assert.Empty(t, text)
}

func TestGetSessionSummaryText_QueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	sess := &session.Session{
		ID:      "session-123",
		AppName: "test-app",
		UserID:  "user-456",
	}

	// Mock: Query returns error
	mock.ExpectQuery(regexp.QuoteMeta("SELECT summary FROM session_summaries")).
		WithArgs(sess.AppName, sess.UserID, sess.ID, "", sqlmock.AnyArg()).
		WillReturnError(fmt.Errorf("database error"))

	text, found := s.GetSessionSummaryText(ctx, sess)
	assert.False(t, found)
	assert.Empty(t, text)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSessionSummaryText_UnmarshalError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	sess := &session.Session{
		ID:      "session-123",
		AppName: "test-app",
		UserID:  "user-456",
	}

	// Mock: Query returns invalid JSON
	mock.ExpectQuery(regexp.QuoteMeta("SELECT summary FROM session_summaries")).
		WithArgs(sess.AppName, sess.UserID, sess.ID, "", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"summary"}).
			AddRow([]byte("invalid-json")))

	text, found := s.GetSessionSummaryText(ctx, sess)
	assert.False(t, found)
	assert.Empty(t, text)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSessionSummaryText_EmptySummary(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	sess := &session.Session{
		ID:      "session-123",
		AppName: "test-app",
		UserID:  "user-456",
	}

	summary := session.Summary{
		Summary: "", // Empty summary text
		Topics:  []string{},
	}
	summaryBytes, _ := json.Marshal(summary)

	// Mock: Query returns empty summary
	mock.ExpectQuery(regexp.QuoteMeta("SELECT summary FROM session_summaries")).
		WithArgs(sess.AppName, sess.UserID, sess.ID, "", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"summary"}).
			AddRow(summaryBytes))

	text, found := s.GetSessionSummaryText(ctx, sess)
	assert.False(t, found)
	assert.Empty(t, text)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestEnqueueSummaryJob_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	summarizer := &mockSummarizer{
		summarizeFunc: func(ctx context.Context, sess *session.Session) (string, error) {
			return "async summary", nil
		},
	}

	s := createTestService(t, db, WithSessionTTL(1*time.Hour), WithSummarizer(summarizer),
		WithAsyncSummaryNum(1), WithSummaryQueueSize(10))

	// Start async summary workers
	s.startAsyncSummaryWorker()
	defer func() {
		for _, ch := range s.summaryJobChans {
			close(ch)
		}
	}()

	ctx := context.Background()

	sess := &session.Session{
		ID:        "session-123",
		AppName:   "test-app",
		UserID:    "user-456",
		UpdatedAt: time.Now(),
	}

	// Mock: Check if summary exists (async worker)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT summary, updated_at FROM session_summaries")).
		WithArgs(sess.AppName, sess.UserID, sess.ID, "", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"summary", "updated_at"}))

	// Mock: Insert new summary (async worker)
	mock.ExpectExec(regexp.QuoteMeta("REPLACE INTO session_summaries")).
		WithArgs(
			sess.AppName,
			sess.UserID,
			sess.ID,
			"",
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = s.EnqueueSummaryJob(ctx, sess, "", false)
	assert.NoError(t, err)

	// Wait for async processing
	time.Sleep(50 * time.Millisecond)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestEnqueueSummaryJob_InvalidKey(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	summarizer := &mockSummarizer{}
	s := createTestService(t, db, WithSummarizer(summarizer))
	ctx := context.Background()

	sess := &session.Session{
		ID:      "", // Invalid: empty session ID
		AppName: "test-app",
		UserID:  "user-456",
	}

	err = s.EnqueueSummaryJob(ctx, sess, "", false)
	assert.Error(t, err)
}

func TestEnqueueSummaryJob_ContextCancelled(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	summarizer := &mockSummarizer{
		summarizeFunc: func(ctx context.Context, sess *session.Session) (string, error) {
			time.Sleep(100 * time.Millisecond) // Simulate slow processing
			return "summary", nil
		},
	}

	s := createTestService(t, db,
		WithSummarizer(summarizer),
		WithAsyncSummaryNum(1),
		WithSummaryQueueSize(1),
	)
	// Manually initialize summary workers
	s.summaryJobChans = []chan *summaryJob{make(chan *summaryJob, 1)}
	defer s.Close()

	ctx := context.Background()
	sess := session.NewSession("test-app", "user-456", "session-123")

	// Fill the queue with a blocking job
	blockingJob := &summaryJob{
		filterKey: "",
		force:     false,
		session:   sess,
	}
	index := sess.Hash % len(s.summaryJobChans)
	s.summaryJobChans[index] <- blockingJob

	// Create cancelled context
	cancelledCtx, cancel := context.WithCancel(ctx)
	cancel()

	// Try to enqueue with cancelled context - should return context error
	err = s.EnqueueSummaryJob(cancelledCtx, sess, "", false)
	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)
}

func TestEnqueueSummaryJob_QueueFull(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	summarizer := &mockSummarizer{
		summarizeFunc: func(ctx context.Context, sess *session.Session) (string, error) {
			return "fallback summary", nil
		},
	}

	s := createTestService(t, db,
		WithSummarizer(summarizer),
		WithAsyncSummaryNum(1),
		WithSummaryQueueSize(1),
	)
	// Manually initialize summary workers
	s.summaryJobChans = []chan *summaryJob{make(chan *summaryJob, 1)}
	defer s.Close()

	ctx := context.Background()
	sess := session.NewSession("test-app", "user-456", "session-123")

	// Fill the queue first
	job1 := &summaryJob{
		filterKey: "",
		force:     false,
		session:   sess,
	}
	index := sess.Hash % len(s.summaryJobChans)
	s.summaryJobChans[index] <- job1

	// Mock sync fallback processing
	mock.ExpectQuery(regexp.QuoteMeta("SELECT summary, updated_at FROM session_summaries")).
		WithArgs(sess.AppName, sess.UserID, sess.ID, "", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"summary", "updated_at"}))

	mock.ExpectExec(regexp.QuoteMeta("REPLACE INTO session_summaries")).
		WithArgs(
			sess.AppName,
			sess.UserID,
			sess.ID,
			"",
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// Try to enqueue when queue is full - should fallback to sync
	err = s.EnqueueSummaryJob(ctx, sess, "", false)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateSessionSummary_WithFilterKey(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	summarizer := &mockSummarizer{
		summarizeFunc: func(ctx context.Context, sess *session.Session) (string, error) {
			return "Filtered summary", nil
		},
	}

	s := createTestService(t, db, WithSummarizer(summarizer))
	ctx := context.Background()

	sess := &session.Session{
		ID:        "session-123",
		AppName:   "test-app",
		UserID:    "user-456",
		UpdatedAt: time.Now(),
	}

	filterKey := "custom-filter"

	// Mock: Check existing summary with custom filter key
	mock.ExpectQuery(regexp.QuoteMeta("SELECT summary, updated_at FROM session_summaries")).
		WithArgs(sess.AppName, sess.UserID, sess.ID, filterKey, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"summary", "updated_at"}))

	// Mock: Insert new summary with custom filter key
	mock.ExpectExec(regexp.QuoteMeta("REPLACE INTO session_summaries")).
		WithArgs(
			sess.AppName,
			sess.UserID,
			sess.ID,
			filterKey,
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = s.CreateSessionSummary(ctx, sess, filterKey, false)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateSessionSummary_ExistingButStale(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	summarizer := &mockSummarizer{
		summarizeFunc: func(ctx context.Context, sess *session.Session) (string, error) {
			return "Updated summary", nil
		},
	}

	s := createTestService(t, db, WithSummarizer(summarizer))
	ctx := context.Background()

	sess := &session.Session{
		ID:        "session-123",
		AppName:   "test-app",
		UserID:    "user-456",
		UpdatedAt: time.Now(),
	}

	// Existing summary is stale (updated > 1 minute ago)
	existingSummary := session.Summary{
		Summary:   "Old summary",
		Topics:    []string{},
		UpdatedAt: time.Now().Add(-2 * time.Minute),
	}
	summaryBytes, _ := json.Marshal(existingSummary)

	// Mock: Check existing summary
	mock.ExpectQuery(regexp.QuoteMeta("SELECT summary, updated_at FROM session_summaries")).
		WithArgs(sess.AppName, sess.UserID, sess.ID, "", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"summary", "updated_at"}).
			AddRow(summaryBytes, existingSummary.UpdatedAt))

	// Mock: Insert updated summary
	mock.ExpectExec(regexp.QuoteMeta("REPLACE INTO session_summaries")).
		WithArgs(
			sess.AppName,
			sess.UserID,
			sess.ID,
			"",
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = s.CreateSessionSummary(ctx, sess, "", false)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateSessionSummary_CheckExistingError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	summarizer := &mockSummarizer{}
	s := createTestService(t, db, WithSummarizer(summarizer))
	ctx := context.Background()

	sess := &session.Session{
		ID:        "session-123",
		AppName:   "test-app",
		UserID:    "user-456",
		UpdatedAt: time.Now(),
	}

	// Mock: Query error when checking existing summary
	mock.ExpectQuery(regexp.QuoteMeta("SELECT summary, updated_at FROM session_summaries")).
		WithArgs(sess.AppName, sess.UserID, sess.ID, "", sqlmock.AnyArg()).
		WillReturnError(fmt.Errorf("database error"))

	err = s.CreateSessionSummary(ctx, sess, "", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "check existing summary failed")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateSessionSummary_UpsertError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	summarizer := &mockSummarizer{
		summarizeFunc: func(ctx context.Context, sess *session.Session) (string, error) {
			return "Test summary", nil
		},
	}

	s := createTestService(t, db, WithSummarizer(summarizer))
	ctx := context.Background()

	sess := &session.Session{
		ID:        "session-123",
		AppName:   "test-app",
		UserID:    "user-456",
		UpdatedAt: time.Now(),
	}

	// Mock: Check existing summary (no existing)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT summary, updated_at FROM session_summaries")).
		WithArgs(sess.AppName, sess.UserID, sess.ID, "", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"summary", "updated_at"}))

	// Mock: Replace fails
	mock.ExpectExec(regexp.QuoteMeta("REPLACE INTO session_summaries")).
		WithArgs(
			sess.AppName,
			sess.UserID,
			sess.ID,
			"",
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnError(fmt.Errorf("insert error"))

	err = s.CreateSessionSummary(ctx, sess, "", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "upsert summary failed")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestEnqueueSummaryJob_ChannelClosed(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	summarizer := &mockSummarizer{
		summarizeFunc: func(ctx context.Context, sess *session.Session) (string, error) {
			return "test summary", nil
		},
	}

	s := createTestService(t, db, WithSummarizer(summarizer), WithAsyncSummaryNum(1), WithSummaryQueueSize(1))

	// Manually initialize and close summary job channels
	s.summaryJobChans = []chan *summaryJob{make(chan *summaryJob, 1)}
	close(s.summaryJobChans[0])

	ctx := context.Background()

	sess := &session.Session{
		ID:        "session-123",
		AppName:   "test-app",
		UserID:    "user-456",
		UpdatedAt: time.Now(),
	}

	// When the channel is closed, panic is caught and logged, function returns nil
	// This test verifies the panic recovery mechanism works without crashing
	err = s.EnqueueSummaryJob(ctx, sess, "", false)
	// The panic is recovered, so no error is returned (defer return doesn't affect outer function)
	assert.NoError(t, err)
}
