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
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	isummary "trpc.group/trpc-go/trpc-agent-go/session/internal/summary"
)

// mockSummarizerImpl is a mock summarizer for testing
type mockSummarizerImpl struct {
	summaryText     string
	err             error
	shouldSummarize bool
}

func (m *mockSummarizerImpl) ShouldSummarize(sess *session.Session) bool {
	return m.shouldSummarize
}

func (m *mockSummarizerImpl) Summarize(ctx context.Context, sess *session.Session) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.summaryText, nil
}

func (m *mockSummarizerImpl) SetPrompt(prompt string) {}

func (m *mockSummarizerImpl) SetModel(mdl model.Model) {}

func (m *mockSummarizerImpl) Metadata() map[string]any {
	return map[string]any{}
}

func TestCreateSessionSummary_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	summarizer := &fakeSummarizer{allow: true, out: "test summary"}

	s := createTestService(t, db, WithSessionTTL(1*time.Hour), WithSummarizer(summarizer))
	ctx := context.Background()

	sess := &session.Session{
		ID:        "session-123",
		AppName:   "test-app",
		UserID:    "user-456",
		UpdatedAt: time.Now(),
	}

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

	err = s.CreateSessionSummary(ctx, sess, "", true)
	assert.NoError(t, err)
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
	assert.NoError(t, err)
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
	assert.NoError(t, err)
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

func TestGetSessionSummaryText_FromInMemory(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()

	sess := &session.Session{
		ID:      "session-123",
		AppName: "test-app",
		UserID:  "user-456",
		Summaries: map[string]*session.Summary{
			"": {
				Summary: "cached summary",
			},
		},
	}

	text, found := s.GetSessionSummaryText(ctx, sess)
	assert.True(t, found)
	assert.Equal(t, "cached summary", text)
}

func TestGetSessionSummaryText_WithNilSession(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db)
	ctx := context.Background()
	text, found := s.GetSessionSummaryText(ctx, nil)
	assert.False(t, found)
	assert.Empty(t, text)
}

func TestGetSessionSummaryText_WithFilterKey(t *testing.T) {
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

	filterKey := "user-messages"
	summary := session.Summary{
		Summary: "Filtered summary text",
		Topics:  []string{},
	}
	summaryBytes, _ := json.Marshal(summary)

	// Mock: Query summary with specific filter key
	mock.ExpectQuery(regexp.QuoteMeta("SELECT summary FROM session_summaries")).
		WithArgs(sess.AppName, sess.UserID, sess.ID, filterKey, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"summary"}).
			AddRow(summaryBytes))

	text, found := s.GetSessionSummaryText(ctx, sess, session.WithSummaryFilterKey(filterKey))
	assert.True(t, found)
	assert.Equal(t, "Filtered summary text", text)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSessionSummaryText_FallbackToFullSession(t *testing.T) {
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

	// First query for specific filter key returns no rows.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT summary FROM session_summaries")).
		WithArgs(sess.AppName, sess.UserID, sess.ID, "missing-key", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"summary"}))

	// Fallback query for full-session summary returns data.
	fullSummary := session.Summary{Summary: "full summary text"}
	fullBytes, _ := json.Marshal(fullSummary)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT summary FROM session_summaries")).
		WithArgs(sess.AppName, sess.UserID, sess.ID, session.SummaryFilterKeyAllContents, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"summary"}).AddRow(fullBytes))

	text, found := s.GetSessionSummaryText(ctx, sess, session.WithSummaryFilterKey("missing-key"))
	assert.True(t, found)
	assert.Equal(t, "full summary text", text)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSessionSummaryText_FallbackQueryError(t *testing.T) {
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

	// First query for specific filter key returns no rows.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT summary FROM session_summaries")).
		WithArgs(sess.AppName, sess.UserID, sess.ID, "missing-key", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"summary"}))

	// Fallback query fails.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT summary FROM session_summaries")).
		WithArgs(sess.AppName, sess.UserID, sess.ID, session.SummaryFilterKeyAllContents, sqlmock.AnyArg()).
		WillReturnError(fmt.Errorf("fallback query error"))

	text, found := s.GetSessionSummaryText(ctx, sess, session.WithSummaryFilterKey("missing-key"))
	assert.False(t, found)
	assert.Empty(t, text)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// func TestPickSummaryText

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

func TestEnqueueSummaryJob_WithNilSession(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	summarizer := &mockSummarizer{}
	s := createTestService(t, db, WithSummarizer(summarizer))
	ctx := context.Background()
	err = s.EnqueueSummaryJob(ctx, nil, "", false)
	assert.Error(t, err)
}

func TestEnqueueSummaryJob_WithNotChan(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	summarizer := &fakeSummarizer{allow: true, out: "test summary"}

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

	err = s.EnqueueSummaryJob(ctx, sess, "", false)
	assert.NoError(t, err)
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
	assert.NoError(t, err)
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
	// Manually initialize summary workers.
	s.summaryJobChans = []chan *summaryJob{make(chan *summaryJob, 1)}
	defer s.Close()

	ctx := context.Background()
	sess := session.NewSession("test-app", "user-456", "session-123")

	// Fill the queue first.
	job1 := &summaryJob{
		filterKey: "",
		force:     false,
		session:   sess,
	}
	index := sess.Hash % len(s.summaryJobChans)
	s.summaryJobChans[index] <- job1

	// Mock sync fallback processing.
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

	// Try to enqueue when queue is full - should fallback to sync.
	err = s.EnqueueSummaryJob(ctx, sess, "", false)
	assert.NoError(t, err)
}

func TestEnqueueSummaryJob_QueueFull_FallbackToSyncWithCascade(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	summarizer := &fakeSummarizer{allow: true, out: "fallback-summary"}

	s := createTestService(t, db,
		WithSummarizer(summarizer),
		WithAsyncSummaryNum(1),
		WithSummaryQueueSize(1),
	)
	// Manually initialize summary workers.
	s.summaryJobChans = []chan *summaryJob{make(chan *summaryJob, 1)}
	defer s.Close()

	ctx := context.Background()
	sess := session.NewSession("test-app", "user-456", "session-123")

	// Add an event to make delta non-empty.
	e := event.New("inv", "author")
	e.Timestamp = time.Now()
	e.Response = &model.Response{Choices: []model.Choice{{
		Message: model.Message{Role: model.RoleUser, Content: "hello"},
	}}}
	sess.Events = append(sess.Events, *e)

	// Fill the queue first.
	job1 := &summaryJob{
		filterKey: "blocking",
		force:     false,
		session:   sess,
	}
	index := sess.Hash % len(s.summaryJobChans)
	s.summaryJobChans[index] <- job1

	// Mock sync fallback processing for branch summary.
	mock.ExpectExec(regexp.QuoteMeta("REPLACE INTO session_summaries")).
		WithArgs(
			sess.AppName,
			sess.UserID,
			sess.ID,
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// Mock sync fallback processing for full-session summary (cascade).
	mock.ExpectExec(regexp.QuoteMeta("REPLACE INTO session_summaries")).
		WithArgs(
			sess.AppName,
			sess.UserID,
			sess.ID,
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// Try to enqueue when queue is full - should fallback to sync with cascade.
	err = s.EnqueueSummaryJob(ctx, sess, "user-messages", false)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestEnqueueSummaryJob_NoAsyncWorkers_FallbackToSyncWithCascade(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	summarizer := &fakeSummarizer{allow: true, out: "sync-summary"}

	// Create service without async workers (summaryJobChans is nil).
	s := createTestService(t, db, WithSummarizer(summarizer))

	ctx := context.Background()
	sess := session.NewSession("test-app", "user-456", "session-123")

	// Add an event to make delta non-empty.
	e := event.New("inv", "author")
	e.Timestamp = time.Now()
	e.Response = &model.Response{Choices: []model.Choice{{
		Message: model.Message{Role: model.RoleUser, Content: "hello"},
	}}}
	sess.Events = append(sess.Events, *e)

	// Mock sync processing for branch summary.
	mock.ExpectExec(regexp.QuoteMeta("REPLACE INTO session_summaries")).
		WithArgs(
			sess.AppName,
			sess.UserID,
			sess.ID,
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// Mock sync processing for full-session summary (cascade).
	mock.ExpectExec(regexp.QuoteMeta("REPLACE INTO session_summaries")).
		WithArgs(
			sess.AppName,
			sess.UserID,
			sess.ID,
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// EnqueueSummaryJob should fall back to sync processing with cascade.
	err = s.EnqueueSummaryJob(ctx, sess, "tool-usage", false)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestEnqueueSummaryJob_FullSessionKey_NoCascade(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	summarizer := &fakeSummarizer{allow: true, out: "full-summary"}

	// Create service without async workers.
	s := createTestService(t, db, WithSummarizer(summarizer))

	ctx := context.Background()
	sess := session.NewSession("test-app", "user-456", "session-123")

	// Add an event to make delta non-empty.
	e := event.New("inv", "author")
	e.Timestamp = time.Now()
	e.Response = &model.Response{Choices: []model.Choice{{
		Message: model.Message{Role: model.RoleUser, Content: "hello"},
	}}}
	sess.Events = append(sess.Events, *e)

	// Mock sync processing for full-session summary only (no cascade needed).
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

	// EnqueueSummaryJob with empty filterKey should not cascade.
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
}

func TestCreateSessionSummary_WithNilSession(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	summarizer := &mockSummarizer{}
	s := createTestService(t, db, WithSummarizer(summarizer))
	ctx := context.Background()

	err = s.CreateSessionSummary(ctx, nil, "", false)
	assert.Error(t, err)
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
	assert.NoError(t, err)
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
	assert.NoError(t, err)
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

func TestTryEnqueueJob(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(t *testing.T, service *Service) (context.Context, *summaryJob, bool)
		expectSend bool
	}{
		{
			name: "successful enqueue",
			setup: func(t *testing.T, service *Service) (context.Context, *summaryJob, bool) {
				key := session.Key{AppName: "app", UserID: "user", SessionID: "sid"}
				job := &summaryJob{
					filterKey: "",
					force:     false,
					session:   &session.Session{ID: key.SessionID, AppName: key.AppName, UserID: key.UserID},
				}
				return context.Background(), job, true
			},
			expectSend: true,
		},
		{
			name: "queue full fallback",
			setup: func(t *testing.T, service *Service) (context.Context, *summaryJob, bool) {
				// Fill up the queue by creating a job that blocks
				key := session.Key{AppName: "app", UserID: "user", SessionID: "sid3"}
				job := &summaryJob{
					filterKey: "",
					force:     false,
					session:   &session.Session{ID: key.SessionID, AppName: key.AppName, UserID: key.UserID},
				}

				// Fill the channel to capacity
			loop:
				for i := 0; i < service.opts.summaryQueueSize; i++ {
					select {
					case service.summaryJobChans[0] <- job:
						// Successfully sent
					default:
						// Channel is full
						break loop
					}
				}

				return context.Background(), job, false
			},
			expectSend: false,
		},
	}

	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			summarizer := &mockSummarizerImpl{summaryText: "test summary", shouldSummarize: true}
			s := createTestService(t, db,
				WithSummaryQueueSize(1),
				WithAsyncSummaryNum(1),
				WithSummarizer(summarizer),
			)
			s.startAsyncSummaryWorker()

			ctx, job, expected := tt.setup(t, s)
			result := s.tryEnqueueJob(ctx, job)

			assert.Equal(t, expected, result)
		})
	}
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
	require.NoError(t, err)
}

func TestRedisService_ProcessSummaryJob_Panic(t *testing.T) {
	summarizer := &mockSummarizerImpl{summaryText: "test summary", shouldSummarize: true}
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	s := createTestService(t, db,
		WithSummaryQueueSize(1),
		WithAsyncSummaryNum(1),
		WithSummarizer(summarizer),
	)

	key := session.Key{AppName: "app", UserID: "user", SessionID: "sid"}

	// Process a job with no stored session - should trigger error but not panic.
	job := &summaryJob{
		filterKey: "",
		force:     false,
		session:   &session.Session{ID: key.SessionID, AppName: key.AppName, UserID: key.UserID},
	}

	// This should not panic, just log error.
	require.NotPanics(t, func() {
		s.processSummaryJob(job)
	})
}

func TestProcessSummaryJob(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(t *testing.T, service *Service) *summaryJob
		expectError bool
	}{
		{
			name: "successful summary processing",
			setup: func(t *testing.T, service *Service) *summaryJob {
				// Create a session with events
				key := session.Key{AppName: "app", UserID: "user", SessionID: "sid"}
				sess := &session.Session{ID: key.SessionID, AppName: key.AppName, UserID: key.UserID}

				// Add an event to make delta non-empty
				e := event.New("inv", "author")
				e.Timestamp = time.Now()
				e.Response = &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hello"}}}}
				sess.Events = append(sess.Events, *e)

				// Enable summarizer
				service.opts.summarizer = &fakeSummarizer{allow: true, out: "test summary"}

				return &summaryJob{
					filterKey: "",
					force:     false,
					session:   sess,
				}
			},
			expectError: false,
		},
		{
			name: "summary job with branch filter",
			setup: func(t *testing.T, service *Service) *summaryJob {
				// Create a session with events
				key := session.Key{AppName: "app", UserID: "user", SessionID: "sid2"}
				sess := &session.Session{ID: key.SessionID, AppName: key.AppName, UserID: key.UserID}

				// Add an event
				e := event.New("inv", "author")
				e.Timestamp = time.Now()
				e.Response = &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hello"}}}}
				sess.Events = append(sess.Events, *e)
				service.opts.summarizer = &fakeSummarizer{allow: true, out: "branch summary"}

				return &summaryJob{
					filterKey: "branch1",
					force:     false,
					session:   sess,
				}
			},
			expectError: false,
		},
		{
			name: "summarizer returns false",
			setup: func(t *testing.T, service *Service) *summaryJob {
				// Create a session
				key := session.Key{AppName: "app", UserID: "user", SessionID: "sid3"}
				sess := &session.Session{ID: key.SessionID, AppName: key.AppName, UserID: key.UserID}
				service.opts.summarizer = &fakeSummarizer{allow: false, out: "no update"}

				return &summaryJob{
					filterKey: "",
					force:     false,
					session:   sess,
				}
			},
			expectError: false,
		},
		{
			name: "summarizer returns error",
			setup: func(t *testing.T, service *Service) *summaryJob {
				// Create a session
				key := session.Key{AppName: "app", UserID: "user", SessionID: "sid4"}
				sess := &session.Session{ID: key.SessionID, AppName: key.AppName, UserID: key.UserID}
				service.opts.summarizer = &fakeErrorSummarizer{}

				return &summaryJob{
					filterKey: "",
					force:     false,
					session:   sess,
				}
			},
			expectError: false, // Should not panic or error, just log
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			summarizer := &mockSummarizerImpl{summaryText: "test summary", shouldSummarize: true}
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			s := createTestService(t, db,
				WithSummaryQueueSize(1),
				WithAsyncSummaryNum(1),
				WithSummarizer(summarizer),
				WithSummaryJobTimeout(time.Second*10),
			)
			job := tt.setup(t, s)

			mock.ExpectExec(regexp.QuoteMeta(fmt.Sprintf(`REPLACE INTO %s (app_name, user_id, session_id, filter_key, summary, updated_at, expires_at, deleted_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, NULL)`, s.tableSessionSummaries))).
				WithArgs(job.session.AppName, job.session.UserID, job.session.ID, job.filterKey, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
				WillReturnResult(sqlmock.NewResult(1, 1))
			defer db.Close()

			// This should not panic
			require.NotPanics(t, func() {
				s.processSummaryJob(job)
			})
		})
	}
}

type fakeSummarizer struct {
	allow bool
	out   string
}

func (f *fakeSummarizer) ShouldSummarize(sess *session.Session) bool { return f.allow }
func (f *fakeSummarizer) Summarize(ctx context.Context, sess *session.Session) (string, error) {
	return f.out, nil
}
func (f *fakeSummarizer) SetPrompt(prompt string)  {}
func (f *fakeSummarizer) SetModel(m model.Model)   {}
func (f *fakeSummarizer) Metadata() map[string]any { return map[string]any{} }

type fakeErrorSummarizer struct{}

func (f *fakeErrorSummarizer) ShouldSummarize(sess *session.Session) bool { return true }
func (f *fakeErrorSummarizer) Summarize(ctx context.Context, sess *session.Session) (string, error) {
	return "", fmt.Errorf("summarizer error")
}
func (f *fakeErrorSummarizer) SetPrompt(prompt string)  {}
func (f *fakeErrorSummarizer) SetModel(m model.Model)   {}
func (f *fakeErrorSummarizer) Metadata() map[string]any { return map[string]any{} }

func TestPickSummaryText(t *testing.T) {
	tests := []struct {
		name      string
		summaries map[string]*session.Summary
		wantText  string
		wantOk    bool
	}{
		{
			name:      "nil summaries",
			summaries: nil,
			wantText:  "",
			wantOk:    false,
		},
		{
			name:      "empty summaries",
			summaries: map[string]*session.Summary{},
			wantText:  "",
			wantOk:    false,
		},
		{
			name: "prefer all-contents summary when available",
			summaries: map[string]*session.Summary{
				"":        {Summary: "full summary"},
				"filter1": {Summary: "filtered summary 1"},
			},
			wantText: "full summary",
			wantOk:   true,
		},
		{
			name: "all-contents summary exists but empty, should pick other non-empty",
			summaries: map[string]*session.Summary{
				"":        {Summary: ""},
				"filter1": {Summary: "filtered summary 1"},
			},
			wantText: "filtered summary 1",
			wantOk:   true,
		},
		{
			name: "all-contents summary is nil, should pick other non-empty",
			summaries: map[string]*session.Summary{
				"":        nil,
				"filter1": {Summary: "filtered summary 1"},
			},
			wantText: "filtered summary 1",
			wantOk:   true,
		},
		{
			name: "only all-contents summary exists and is non-empty",
			summaries: map[string]*session.Summary{
				"": {Summary: "full summary"},
			},
			wantText: "full summary",
			wantOk:   true,
		},
		{
			name: "only all-contents summary exists but is empty",
			summaries: map[string]*session.Summary{
				"": {Summary: ""},
			},
			wantText: "",
			wantOk:   false,
		},
		{
			name: "only all-contents summary exists but is nil",
			summaries: map[string]*session.Summary{
				"": nil,
			},
			wantText: "",
			wantOk:   false,
		},
		{
			name: "no all-contents summary, pick first non-empty",
			summaries: map[string]*session.Summary{
				"filter1": {Summary: "filtered summary 1"},
			},
			wantText: "filtered summary 1",
			wantOk:   true,
		},
		{
			name: "all summaries are empty",
			summaries: map[string]*session.Summary{
				"":        {Summary: ""},
				"filter1": {Summary: ""},
				"filter2": {Summary: ""},
			},
			wantText: "",
			wantOk:   false,
		},
		{
			name: "all summaries are nil",
			summaries: map[string]*session.Summary{
				"":        nil,
				"filter1": nil,
				"filter2": nil,
			},
			wantText: "",
			wantOk:   false,
		},
		{
			name: "mixed nil and empty summaries, pick first non-empty",
			summaries: map[string]*session.Summary{
				"":        nil,
				"filter1": {Summary: ""},
				"filter2": {Summary: "valid summary"},
			},
			wantText: "valid summary",
			wantOk:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotText, gotOk := isummary.PickSummaryText(tt.summaries, "")
			if gotText != tt.wantText {
				t.Errorf("pickSummaryText() text = %v, want %v", gotText, tt.wantText)
			}
			if gotOk != tt.wantOk {
				t.Errorf("pickSummaryText() ok = %v, want %v", gotOk, tt.wantOk)
			}
		})
	}
}

func TestEnqueueSummaryJob_AsyncProcessing(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	summarizer := &fakeSummarizer{allow: true, out: "async summary"}

	s := createTestService(t, db, WithSessionTTL(1*time.Hour), WithSummarizer(summarizer),
		WithAsyncSummaryNum(1), WithSummaryQueueSize(10))

	// Start async workers
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

	// Mock: Insert summary via async worker
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
	require.NoError(t, err)

	// Wait for async processing
	time.Sleep(100 * time.Millisecond)
}

func TestTryEnqueueJob_QueueFull(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db,
		WithAsyncSummaryNum(1),
		WithSummaryQueueSize(1),
	)

	// Initialize channels without starting workers to keep queue full.
	s.summaryJobChans = []chan *summaryJob{
		make(chan *summaryJob, 1),
	}

	ctx := context.Background()
	sess := session.NewSession("test-app", "user-456", "session-123")
	job := &summaryJob{
		filterKey: "",
		force:     false,
		session:   sess,
	}

	// Fill the queue by sending to the channel directly
	index := sess.Hash % len(s.summaryJobChans)
	select {
	case s.summaryJobChans[index] <- job:
		// Successfully sent, now try to enqueue another which should fail
	default:
		t.Skip("Could not fill queue for testing")
	}

	// Try to enqueue when queue is full - should return false
	result := s.tryEnqueueJob(ctx, job)
	assert.False(t, result)
}

func TestTryEnqueueJob_ContextCancelled(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db,
		WithAsyncSummaryNum(1),
		WithSummaryQueueSize(1),
	)

	// Start async workers to initialize channels
	s.startAsyncSummaryWorker()
	defer s.Close() // This will close the channels

	sess := session.NewSession("test-app", "user-456", "session-123")
	job := &summaryJob{
		filterKey: "",
		force:     false,
		session:   sess,
	}

	// First, fill the queue to make subsequent enqueue operations check ctx.Done()
	index := sess.Hash % len(s.summaryJobChans)
	select {
	case s.summaryJobChans[index] <- job:
		// Successfully filled the queue
	default:
		t.Skip("Could not fill queue for testing")
	}

	// Create cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Try to enqueue with cancelled context when queue is full - should return false
	result := s.tryEnqueueJob(ctx, job)
	assert.False(t, result, "tryEnqueueJob should return false when context is cancelled and queue is full")
}

type doneNoErrContext struct {
	context.Context
	done <-chan struct{}
}

func (c doneNoErrContext) Done() <-chan struct{} {
	return c.done
}

func (doneNoErrContext) Err() error {
	return nil
}

func TestTryEnqueueJob_ContextDoneBranch(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db,
		WithAsyncSummaryNum(1),
		WithSummaryQueueSize(1),
	)
	// Initialize channels without starting workers to keep queue full.
	s.summaryJobChans = []chan *summaryJob{
		make(chan *summaryJob, 1),
	}

	sess := session.NewSession("test-app", "user-456", "session-123")
	job := &summaryJob{
		filterKey: "",
		force:     false,
		session:   sess,
	}

	index := sess.Hash % len(s.summaryJobChans)
	s.summaryJobChans[index] <- job

	doneCh := make(chan struct{})
	close(doneCh)
	ctx := doneNoErrContext{
		Context: context.Background(),
		done:    doneCh,
	}

	result := s.tryEnqueueJob(ctx, job)
	assert.False(t, result)
}

func TestProcessSummaryJob_NilJob_Recovers(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	summarizer := &fakeSummarizer{allow: true, out: "test"}
	s := createTestService(t, db, WithSummarizer(summarizer))
	defer s.Close()

	require.NotPanics(t, func() {
		s.processSummaryJob(nil)
	})
}

func TestProcessSummaryJob_NilSession_LogsWarning(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	summarizer := &fakeSummarizer{allow: true, out: "test"}
	s := createTestService(t, db, WithSummarizer(summarizer))
	defer s.Close()

	job := &summaryJob{
		filterKey: "",
		force:     false,
		session:   nil,
	}
	require.NotPanics(t, func() {
		s.processSummaryJob(job)
	})
}

func TestProcessSummaryJob_Timeout(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	summarizer := &fakeSummarizer{allow: true, out: "timeout summary"}

	s := createTestService(t, db, WithSummarizer(summarizer),
		WithSummaryJobTimeout(10*time.Millisecond))

	job := &summaryJob{
		filterKey: "",
		force:     false,
		session: &session.Session{
			ID:        "session-123",
			AppName:   "test-app",
			UserID:    "user-456",
			UpdatedAt: time.Now(),
		},
	}

	// This should not panic even with timeout
	require.NotPanics(t, func() {
		s.processSummaryJob(job)
	})
}
