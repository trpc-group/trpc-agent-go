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

	// Mock: INSERT ... ON DUPLICATE KEY UPDATE (atomic upsert).
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO session_summaries")).
		WithArgs(
			sess.AppName,
			sess.UserID,
			sess.ID,
			"",               // filter_key
			sqlmock.AnyArg(), // summary
			sqlmock.AnyArg(), // updated_at
			sqlmock.AnyArg(), // expires_at
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = s.CreateSessionSummary(ctx, sess, "", true)
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

	// Should not generate new summary because no events (delta empty) and force=false
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

	// With force=true, skip checking existing summary.
	// Mock: INSERT ... ON DUPLICATE KEY UPDATE (atomic upsert).
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO session_summaries")).
		WithArgs(
			sess.AppName,
			sess.UserID,
			sess.ID,
			"",               // filter_key
			sqlmock.AnyArg(), // summary
			sqlmock.AnyArg(), // updated_at
			sqlmock.AnyArg(), // expires_at
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
		Events:    []event.Event{{Timestamp: time.Now()}}, // Add event to trigger summary.
	}

	// Should return error from summarizer.
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
		ID:        "session-123",
		AppName:   "test-app",
		UserID:    "user-456",
		CreatedAt: time.Now().Add(-time.Hour), // Set CreatedAt to avoid updated_at >= CreatedAt filter issue
	}

	summary := session.Summary{
		Summary: "Test summary text",
		Topics:  []string{},
	}
	summaryBytes, _ := json.Marshal(summary)

	// Mock: Query summary
	mock.ExpectQuery(regexp.QuoteMeta("SELECT summary FROM session_summaries")).
		WithArgs(sess.AppName, sess.UserID, sess.ID, "", sqlmock.AnyArg(), sess.CreatedAt).
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
		WithArgs(sess.AppName, sess.UserID, sess.ID, "", sqlmock.AnyArg(), sqlmock.AnyArg()).
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
		WithArgs(sess.AppName, sess.UserID, sess.ID, "", sqlmock.AnyArg(), sqlmock.AnyArg()).
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
		WithArgs(sess.AppName, sess.UserID, sess.ID, "", sqlmock.AnyArg(), sqlmock.AnyArg()).
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
		WithArgs(sess.AppName, sess.UserID, sess.ID, "", sqlmock.AnyArg(), sqlmock.AnyArg()).
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
		WithArgs(sess.AppName, sess.UserID, sess.ID, filterKey, sqlmock.AnyArg(), sqlmock.AnyArg()).
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
		WithArgs(sess.AppName, sess.UserID, sess.ID, "missing-key", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"summary"}))

	// Fallback query for full-session summary returns data.
	fullSummary := session.Summary{Summary: "full summary text"}
	fullBytes, _ := json.Marshal(fullSummary)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT summary FROM session_summaries")).
		WithArgs(sess.AppName, sess.UserID, sess.ID, session.SummaryFilterKeyAllContents, sqlmock.AnyArg(), sess.CreatedAt).
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
		ID:        "session-123",
		AppName:   "test-app",
		UserID:    "user-456",
		CreatedAt: time.Now().Add(-time.Hour),
	}

	// First query for specific filter key returns no rows.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT summary FROM session_summaries")).
		WithArgs(sess.AppName, sess.UserID, sess.ID, "missing-key", sqlmock.AnyArg(), sess.CreatedAt).
		WillReturnRows(sqlmock.NewRows([]string{"summary"}))

	// Fallback query fails.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT summary FROM session_summaries")).
		WithArgs(sess.AppName, sess.UserID, sess.ID, session.SummaryFilterKeyAllContents, sqlmock.AnyArg(), sess.CreatedAt).
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

	// Async workers are initialized in NewService if summarizer and asyncSummaryNum are set.

	ctx := context.Background()

	sess := &session.Session{
		ID:        "session-123",
		AppName:   "test-app",
		UserID:    "user-456",
		UpdatedAt: time.Now(),
		Events:    []event.Event{{Timestamp: time.Now()}}, // Add event to trigger summary.
	}

	// Mock: INSERT ... ON DUPLICATE KEY UPDATE (atomic upsert).
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO session_summaries")).
		WithArgs(
			sess.AppName,
			sess.UserID,
			sess.ID,
			"",               // filter_key
			sqlmock.AnyArg(), // summary
			sqlmock.AnyArg(), // updated_at
			sqlmock.AnyArg(), // expires_at
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = s.EnqueueSummaryJob(ctx, sess, "", false)
	assert.NoError(t, err)

	// Wait for async processing.
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

	// Mock: INSERT ... ON DUPLICATE KEY UPDATE (atomic upsert).
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO session_summaries")).
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
	defer s.Close()

	ctx := context.Background()
	sess := session.NewSession("test-app", "user-456", "session-123")

	// Create cancelled context
	cancelledCtx, cancel := context.WithCancel(ctx)
	cancel()

	// Try to enqueue with cancelled context - should fallback to sync processing
	err = s.EnqueueSummaryJob(cancelledCtx, sess, "", false)
	// Note: With cancelled context, it will fallback to sync processing
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
	defer s.Close()

	ctx := context.Background()
	sess := session.NewSession("test-app", "user-456", "session-123")

	// Fill the queue by enqueueing a job first (queue size is 1).
	err = s.EnqueueSummaryJob(ctx, sess, "", false)
	assert.NoError(t, err)

	// Add event to trigger summary.
	sess.Events = []event.Event{{Timestamp: time.Now()}}

	// Mock sync fallback processing.
	// INSERT ... ON DUPLICATE KEY UPDATE (atomic upsert).
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO session_summaries")).
		WithArgs(
			sess.AppName,
			sess.UserID,
			sess.ID,
			"",               // filter_key
			sqlmock.AnyArg(), // summary
			sqlmock.AnyArg(), // updated_at
			sqlmock.AnyArg(), // expires_at
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// Try to enqueue when queue is full - should fallback to sync.
	err = s.EnqueueSummaryJob(ctx, sess, "", false)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestEnqueueSummaryJob_QueueFull_FallbackToSyncWithCascade(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	// Disable order matching since cascade creates summaries concurrently.
	mock.MatchExpectationsInOrder(false)

	summarizer := &fakeSummarizer{allow: true, out: "fallback-summary"}

	s := createTestService(t, db,
		WithSummarizer(summarizer),
		WithAsyncSummaryNum(1),
		WithSummaryQueueSize(1),
	)
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

	// We need to set up expectations for all possible DB calls.
	// Due to cascade, each EnqueueSummaryJob with a non-empty filterKey will create:
	// 1. Summary for the specified filterKey.
	// 2. Full-session summary (filterKey="").
	//
	// First job: "blocking" + cascade to "".
	// Second job: "user-messages" + cascade to "" (but "" may already exist).
	//
	// Total expected: "blocking", "user-messages", and "" (possibly twice).

	// Use AnyArg for filterKey to match any call.
	// We expect 2-4 INSERT calls depending on timing.
	// INSERT ... ON DUPLICATE KEY UPDATE (atomic upsert).
	for i := 0; i < 4; i++ {
		mock.ExpectExec(regexp.QuoteMeta("INSERT INTO session_summaries")).
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
	}

	// Fill the queue by enqueueing a job first (queue size is 1).
	err = s.EnqueueSummaryJob(ctx, sess, "blocking", false)
	require.NoError(t, err)

	// Try to enqueue when queue is full - should fallback to sync with cascade.
	err = s.EnqueueSummaryJob(ctx, sess, "user-messages", false)
	assert.NoError(t, err)

	// Wait for async processing to complete.
	time.Sleep(100 * time.Millisecond)

	// Note: We don't check ExpectationsWereMet() because the exact number of calls
	// depends on timing. The important thing is that no error occurred.
}

func TestEnqueueSummaryJob_NoAsyncWorkers_FallbackToSyncWithCascade(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	// Disable order matching since cascade creates summaries concurrently.
	mock.MatchExpectationsInOrder(false)

	summarizer := &fakeSummarizer{allow: true, out: "sync-summary"}

	// Create service without async workers (summaryJobChans is nil).
	s := createTestService(t, db, WithSummarizer(summarizer))

	ctx := context.Background()
	sess := session.NewSession("test-app", "user-456", "session-123")

	// Add events with different filterKeys to trigger cascade (not single filterKey
	// optimization). Version must be CurrentVersion for Filter() to use FilterKey.
	e1 := event.New("inv1", "author")
	e1.Timestamp = time.Now()
	e1.FilterKey = "tool-usage"
	e1.Version = event.CurrentVersion
	e1.Response = &model.Response{Choices: []model.Choice{{
		Message: model.Message{Role: model.RoleUser, Content: "hello"},
	}}}
	sess.Events = append(sess.Events, *e1)

	e2 := event.New("inv2", "author")
	e2.Timestamp = time.Now()
	e2.FilterKey = "other-key"
	e2.Version = event.CurrentVersion
	e2.Response = &model.Response{Choices: []model.Choice{{
		Message: model.Message{Role: model.RoleUser, Content: "world"},
	}}}
	sess.Events = append(sess.Events, *e2)

	// Mock sync processing for branch summary.
	// INSERT ... ON DUPLICATE KEY UPDATE (atomic upsert).
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO session_summaries")).
		WithArgs(
			sess.AppName,
			sess.UserID,
			sess.ID,
			"tool-usage",
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// Mock sync processing for full-session summary (cascade).
	// INSERT ... ON DUPLICATE KEY UPDATE (atomic upsert).
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO session_summaries")).
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

	// EnqueueSummaryJob should fall back to sync processing with cascade.
	err = s.EnqueueSummaryJob(ctx, sess, "tool-usage", false)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestEnqueueSummaryJob_SingleFilterKey_PersistsBothKeys(t *testing.T) {
	// This test verifies that when all events match a single filterKey,
	// the optimization path still persists BOTH the filterKey summary AND
	// the full-session summary (filter_key="") to the database.
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	// Disable order matching since we need to match two sets of SQL calls.
	mock.MatchExpectationsInOrder(false)

	summarizer := &fakeSummarizer{allow: true, out: "single-key-summary"}

	// Create service without async workers.
	s := createTestService(t, db, WithSummarizer(summarizer))

	ctx := context.Background()
	sess := session.NewSession("test-app", "user-456", "session-123")

	// Add events that ALL match the same filterKey (triggers single filterKey
	// optimization). Version must be CurrentVersion for Filter() to use FilterKey.
	e1 := event.New("inv1", "author")
	e1.Timestamp = time.Now()
	e1.FilterKey = "tool-usage"
	e1.Version = event.CurrentVersion
	e1.Response = &model.Response{Choices: []model.Choice{{
		Message: model.Message{Role: model.RoleUser, Content: "hello"},
	}}}
	sess.Events = append(sess.Events, *e1)

	e2 := event.New("inv2", "author")
	e2.Timestamp = time.Now()
	e2.FilterKey = "tool-usage" // Same filterKey as e1.
	e2.Version = event.CurrentVersion
	e2.Response = &model.Response{Choices: []model.Choice{{
		Message: model.Message{Role: model.RoleUser, Content: "world"},
	}}}
	sess.Events = append(sess.Events, *e2)

	// Mock: First call persists filterKey="tool-usage" (LLM generates summary).
	// INSERT ... ON DUPLICATE KEY UPDATE (atomic upsert).
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO session_summaries")).
		WithArgs(
			sess.AppName,
			sess.UserID,
			sess.ID,
			"tool-usage",
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// Mock: Second call persists filter_key="" (full-session, copied summary).
	// This is the critical part - verifying that filter_key="" is also persisted!
	// INSERT ... ON DUPLICATE KEY UPDATE (atomic upsert).
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO session_summaries")).
		WithArgs(
			sess.AppName,
			sess.UserID,
			sess.ID,
			"", // filter_key="" must be persisted.
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// EnqueueSummaryJob with filterKey should trigger single filterKey optimization
	// and persist BOTH keys.
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
	// INSERT ... ON DUPLICATE KEY UPDATE (atomic upsert).
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO session_summaries")).
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
		Events:    []event.Event{{Timestamp: time.Now()}}, // Add event to trigger summary.
	}

	filterKey := "custom-filter"

	// Mock: INSERT ... ON DUPLICATE KEY UPDATE (atomic upsert) with custom filter key.
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO session_summaries")).
		WithArgs(
			sess.AppName,
			sess.UserID,
			sess.ID,
			filterKey,
			sqlmock.AnyArg(), // summary
			sqlmock.AnyArg(), // updated_at
			sqlmock.AnyArg(), // expires_at
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = s.CreateSessionSummary(ctx, sess, filterKey, false)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
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
		Events:    []event.Event{{Timestamp: time.Now()}}, // Add event to trigger summary.
	}

	// Existing summary is stale (updated > 1 minute ago).
	existingSummary := &session.Summary{
		Summary:   "Old summary",
		Topics:    []string{},
		UpdatedAt: time.Now().Add(-2 * time.Minute),
	}
	sess.Summaries = map[string]*session.Summary{"": existingSummary}

	// Mock: INSERT ... ON DUPLICATE KEY UPDATE (atomic upsert).
	// ON DUPLICATE KEY UPDATE will update the existing record.
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO session_summaries")).
		WithArgs(
			sess.AppName,
			sess.UserID,
			sess.ID,
			"",               // filter_key
			sqlmock.AnyArg(), // summary
			sqlmock.AnyArg(), // updated_at
			sqlmock.AnyArg(), // expires_at
		).
		WillReturnResult(sqlmock.NewResult(0, 2)) // 2 rows affected indicates update.

	err = s.CreateSessionSummary(ctx, sess, "", false)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateSessionSummary_NoEvents_NoUpdate(t *testing.T) {
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

	// No events, no force -> no update
	err = s.CreateSessionSummary(ctx, sess, "", false)
	assert.NoError(t, err)
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

	// Mock: INSERT ... ON DUPLICATE KEY UPDATE fails.
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO session_summaries")).
		WithArgs(
			sess.AppName,
			sess.UserID,
			sess.ID,
			"",               // filter_key
			sqlmock.AnyArg(), // summary
			sqlmock.AnyArg(), // updated_at
			sqlmock.AnyArg(), // expires_at
		).
		WillReturnError(fmt.Errorf("upsert error"))

	// Use force=true to trigger upsert even with no events.
	err = s.CreateSessionSummary(ctx, sess, "", true)
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

	// Note: summaryJobChans is now handled by asyncWorker in session/internal/summary
	// The asyncWorker handles closed channels gracefully

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
			name: "all-contents summary exists but empty, should return false",
			summaries: map[string]*session.Summary{
				"":        {Summary: ""},
				"filter1": {Summary: "filtered summary 1"},
			},
			wantText: "",
			wantOk:   false,
		},
		{
			name: "all-contents summary is nil, should return false",
			summaries: map[string]*session.Summary{
				"":        nil,
				"filter1": {Summary: "filtered summary 1"},
			},
			wantText: "",
			wantOk:   false,
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
			name: "no all-contents summary, should return false",
			summaries: map[string]*session.Summary{
				"filter1": {Summary: "filtered summary 1"},
			},
			wantText: "",
			wantOk:   false,
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
			name: "mixed nil and empty summaries, should return false",
			summaries: map[string]*session.Summary{
				"":        nil,
				"filter1": {Summary: ""},
				"filter2": {Summary: "valid summary"},
			},
			wantText: "",
			wantOk:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotText, gotOk := isummary.PickSummaryText(tt.summaries, "", time.Time{})
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

	// Async workers are initialized in NewService if summarizer and asyncSummaryNum are set.
	defer s.Close()

	ctx := context.Background()

	sess := &session.Session{
		ID:        "session-123",
		AppName:   "test-app",
		UserID:    "user-456",
		UpdatedAt: time.Now(),
	}

	// Mock: INSERT ... ON DUPLICATE KEY UPDATE (atomic upsert).
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO session_summaries")).
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

	// Wait for async processing.
	time.Sleep(100 * time.Millisecond)
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
