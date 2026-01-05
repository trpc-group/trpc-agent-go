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
	require.NoError(t, err)
}

func TestCreateSessionSummary_InvalidKey(t *testing.T) {
	summarizer := &mockSummarizerImpl{summaryText: "test summary", shouldSummarize: true}
	s, _, db := setupMockService(t, &TestServiceOpts{summarizer: summarizer})
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
func TestCreateSessionSummary_CreateNewSummary(t *testing.T) {
	summarizer := &mockSummarizerImpl{summaryText: "new summary", shouldSummarize: true}
	s, mock, db := setupMockService(t, &TestServiceOpts{summarizer: summarizer})
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

	// Mock UPSERT (INSERT ... ON CONFLICT)
	mock.ExpectExec("INSERT INTO session_summaries").
		WithArgs("test-app", "test-user", "test-session", "", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.CreateSessionSummary(context.Background(), sess, "", false)
	require.NoError(t, err)
}

func TestCreateSessionSummary_WithTTL(t *testing.T) {
	summarizer := &mockSummarizerImpl{summaryText: "new summary", shouldSummarize: true}
	s, mock, db := setupMockService(t, &TestServiceOpts{summarizer: summarizer})
	s.opts.sessionTTL = 1 * time.Hour
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

	// Mock UPSERT (INSERT ... ON CONFLICT)
	mock.ExpectExec("INSERT INTO session_summaries").
		WithArgs("test-app", "test-user", "test-session", "", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.CreateSessionSummary(context.Background(), sess, "", false)
	require.NoError(t, err)
}

func TestCreateSessionSummary_ForcedUpdate(t *testing.T) {
	summarizer := &mockSummarizerImpl{summaryText: "forced summary", shouldSummarize: true}
	s, mock, db := setupMockService(t, &TestServiceOpts{summarizer: summarizer})
	defer db.Close()

	sess := &session.Session{
		ID:        "test-session",
		AppName:   "test-app",
		UserID:    "test-user",
		UpdatedAt: time.Now(),
	}

	// When force=true, should skip checking existing summary
	// and directly create new one (UPSERT)
	mock.ExpectExec("INSERT INTO session_summaries").
		WithArgs("test-app", "test-user", "test-session", "", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.CreateSessionSummary(context.Background(), sess, "", true)
	require.NoError(t, err)

	// Verify expectations
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateSessionSummary_SummarizerError(t *testing.T) {
	summarizer := &mockSummarizerImpl{err: fmt.Errorf("summarizer error"), shouldSummarize: true}
	s, mock, db := setupMockService(t, &TestServiceOpts{summarizer: summarizer})
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
	require.NoError(t, err)
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

func TestEnqueueSummaryJob_InvalidKey(t *testing.T) {
	summarizer := &mockSummarizerImpl{summaryText: "test summary", shouldSummarize: true}
	s, _, db := setupMockService(t, &TestServiceOpts{summarizer: summarizer})
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
	summarizer := &mockSummarizerImpl{summaryText: "test summary", shouldSummarize: true}
	s, _, db := setupMockService(t, &TestServiceOpts{summarizer: summarizer})
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
	summarizer := &mockSummarizerImpl{summaryText: "test summary", shouldSummarize: true}
	s, _, db := setupMockService(t, &TestServiceOpts{summarizer: summarizer})
	defer db.Close()

	sess := &session.Session{
		ID:      "test-session",
		AppName: "test-app",
		UserID:  "test-user",
	}

	// Note: summaryJobChans is now handled by asyncWorker in session/internal/summary
	// Fill the queue by enqueueing jobs first
	for i := 0; i < 10; i++ {
		s.EnqueueSummaryJob(context.Background(), sess, "", false)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := s.EnqueueSummaryJob(ctx, sess, "", false)
	require.NoError(t, err)
}

func TestEnqueueSummaryJob_QueueFullFallbackToSync(t *testing.T) {
	summarizer := &mockSummarizerImpl{summaryText: "test summary", shouldSummarize: true}
	s, mock, db := setupMockService(t, &TestServiceOpts{summarizer: summarizer})
	defer db.Close()

	sess := &session.Session{
		ID:        "test-session",
		AppName:   "test-app",
		UserID:    "test-user",
		UpdatedAt: time.Now(),
	}

	// Note: summaryJobChans is now handled by asyncWorker in session/internal/summary
	// Fill the queue by enqueueing jobs first
	for i := 0; i < 10; i++ {
		s.EnqueueSummaryJob(context.Background(), sess, "", false)
	}

	// Mock for sync fallback
	rows := sqlmock.NewRows([]string{"summary", "updated_at"})
	mock.ExpectQuery("SELECT summary, updated_at FROM session_summaries").
		WithArgs("test-app", "test-user", "test-session", "", sqlmock.AnyArg()).
		WillReturnRows(rows)

	// Mock UPSERT (INSERT ... ON CONFLICT)
	mock.ExpectExec("INSERT INTO session_summaries").
		WithArgs("test-app", "test-user", "test-session", "", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.EnqueueSummaryJob(context.Background(), sess, "", false)
	require.NoError(t, err)
}

func TestEnqueueSummaryJob_ChannelClosedPanicRecovery(t *testing.T) {
	summarizer := &mockSummarizerImpl{summaryText: "test summary", shouldSummarize: true}
	s, _, db := setupMockService(t, &TestServiceOpts{summarizer: summarizer})
	defer db.Close()

	// Note: summaryJobChans is now handled by asyncWorker in session/internal/summary
	// The asyncWorker handles closed channels gracefully

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
	s, mock, db := setupMockService(t, &TestServiceOpts{summarizer: &mockSummarizerImpl{shouldSummarize: false}})
	defer db.Close()

	sess := &session.Session{
		ID:        "test-session",
		AppName:   "test-app",
		UserID:    "test-user",
		CreatedAt: time.Now().Add(-time.Hour),
	}

	// Mock summary query
	summary := session.Summary{
		Summary: "test summary text",
	}
	summaryBytes, _ := json.Marshal(summary)

	rows := sqlmock.NewRows([]string{"summary"}).
		AddRow(summaryBytes)

	mock.ExpectQuery("SELECT summary FROM session_summaries").
		WithArgs("test-app", "test-user", "test-session", "", sqlmock.AnyArg(), sess.CreatedAt).
		WillReturnRows(rows)

	text, ok := s.GetSessionSummaryText(context.Background(), sess)
	require.True(t, ok)
	assert.Equal(t, "test summary text", text)

	// Verify expectations
	require.NoError(t, mock.ExpectationsWereMet())

	sess = &session.Session{
		ID:      "test-session",
		AppName: "test-app",
		UserID:  "test-user",
		Summaries: map[string]*session.Summary{
			"": {Summary: "summary text"},
		},
	}

	text, ok = s.GetSessionSummaryText(context.Background(), sess)
	require.True(t, ok)
	assert.Equal(t, "summary text", text)
}

func TestGetSessionSummaryText_NoSummary(t *testing.T) {
	s, mock, db := setupMockService(t, &TestServiceOpts{summarizer: &mockSummarizerImpl{shouldSummarize: false}})
	defer db.Close()

	sess := &session.Session{
		ID:        "test-session",
		AppName:   "test-app",
		UserID:    "test-user",
		CreatedAt: time.Now().Add(-time.Hour),
	}

	// Mock empty result
	rows := sqlmock.NewRows([]string{"summary"})
	mock.ExpectQuery("SELECT summary FROM session_summaries").
		WithArgs("test-app", "test-user", "test-session", "", sqlmock.AnyArg(), sess.CreatedAt).
		WillReturnRows(rows)

	text, ok := s.GetSessionSummaryText(context.Background(), sess)
	require.False(t, ok)
	assert.Empty(t, text)

	// Verify expectations
	require.NoError(t, mock.ExpectationsWereMet())

	text, ok = s.GetSessionSummaryText(context.Background(), nil)
	require.False(t, ok)
	assert.Empty(t, text)
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
	s, mock, db := setupMockService(t, &TestServiceOpts{summarizer: &mockSummarizerImpl{shouldSummarize: false}})
	defer db.Close()

	sess := &session.Session{
		ID:      "test-session",
		AppName: "test-app",
		UserID:  "test-user",
	}

	// Mock query error
	mock.ExpectQuery("SELECT summary FROM session_summaries").
		WithArgs("test-app", "test-user", "test-session", "", sqlmock.AnyArg(), sess.CreatedAt).
		WillReturnError(fmt.Errorf("database error"))

	text, ok := s.GetSessionSummaryText(context.Background(), sess)
	require.False(t, ok)
	assert.Empty(t, text)

	// Verify expectations
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSessionSummaryText_EmptySummaryText(t *testing.T) {
	s, mock, db := setupMockService(t, &TestServiceOpts{summarizer: &mockSummarizerImpl{shouldSummarize: false}})
	defer db.Close()

	sess := &session.Session{
		ID:        "test-session",
		AppName:   "test-app",
		UserID:    "test-user",
		CreatedAt: time.Now().Add(-time.Hour),
	}

	// Mock summary with empty text
	summary := session.Summary{
		Summary: "",
	}
	summaryBytes, _ := json.Marshal(summary)

	rows := sqlmock.NewRows([]string{"summary"}).
		AddRow(summaryBytes)

	mock.ExpectQuery("SELECT summary FROM session_summaries").
		WithArgs("test-app", "test-user", "test-session", "", sqlmock.AnyArg(), sess.CreatedAt).
		WillReturnRows(rows)

	text, ok := s.GetSessionSummaryText(context.Background(), sess)
	require.False(t, ok)
	assert.Empty(t, text)

	// Verify expectations
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateSessionSummary_WithFilterKey(t *testing.T) {
	summarizer := &mockSummarizerImpl{summaryText: "filtered summary", shouldSummarize: true}
	s, mock, db := setupMockService(t, &TestServiceOpts{summarizer: summarizer})
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

	// Mock UPSERT (INSERT ... ON CONFLICT)
	mock.ExpectExec("INSERT INTO session_summaries").
		WithArgs("test-app", "test-user", "test-session", "filter1", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := s.CreateSessionSummary(context.Background(), sess, "filter1", false)
	require.NoError(t, err)
}

func TestCreateSessionSummary_UnmarshalError(t *testing.T) {
	summarizer := &mockSummarizerImpl{summaryText: "new summary", shouldSummarize: true}
	s, mock, db := setupMockService(t, &TestServiceOpts{summarizer: summarizer})
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
	require.NoError(t, err)
}

func TestCreateSessionSummary_SessionIsNil(t *testing.T) {
	summarizer := &mockSummarizerImpl{summaryText: "new summary", shouldSummarize: true}
	s, _, db := setupMockService(t, &TestServiceOpts{summarizer: summarizer})
	defer db.Close()

	err := s.CreateSessionSummary(context.Background(), nil, "", false)
	require.Error(t, err)
}

func TestGetSessionSummaryText_UnmarshalError(t *testing.T) {
	s, mock, db := setupMockService(t, &TestServiceOpts{summarizer: &mockSummarizerImpl{shouldSummarize: false}})
	defer db.Close()

	sess := &session.Session{
		ID:        "test-session",
		AppName:   "test-app",
		UserID:    "test-user",
		CreatedAt: time.Now().Add(-time.Hour),
	}

	// Mock summary with invalid JSON
	rows := sqlmock.NewRows([]string{"summary"}).
		AddRow([]byte("invalid json"))

	mock.ExpectQuery("SELECT summary FROM session_summaries").
		WithArgs("test-app", "test-user", "test-session", "", sqlmock.AnyArg(), sess.CreatedAt).
		WillReturnRows(rows)

	text, ok := s.GetSessionSummaryText(context.Background(), sess)
	require.False(t, ok)
	assert.Empty(t, text)

	// Verify expectations
	require.NoError(t, mock.ExpectationsWereMet())
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
		WithArgs(sess.AppName, sess.UserID, sess.ID, filterKey, sqlmock.AnyArg(), sess.CreatedAt).
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
		WithArgs(sess.AppName, sess.UserID, sess.ID, "missing-key", sqlmock.AnyArg(), sess.CreatedAt).
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
		ID:      "session-123",
		AppName: "test-app",
		UserID:  "user-456",
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

func TestService_EnqueueSummaryJob_ChannelClosed_PanicRecovery(t *testing.T) {
	summarizer := &mockSummarizerImpl{summaryText: "test summary", shouldSummarize: true}
	s, _, db := setupMockService(t, &TestServiceOpts{summarizer: summarizer, asyncSummaryNum: 1})
	defer db.Close()

	// Note: summaryJobChans is now handled by asyncWorker in session/internal/summary
	// The asyncWorker handles closed channels gracefully

	// This should not panic
	require.NotPanics(t, func() {
		s.EnqueueSummaryJob(context.Background(), &session.Session{}, "", false)
	})
}

func TestEnqueueSummaryJob_HashDistribution(t *testing.T) {
	summarizer := &mockSummarizerImpl{summaryText: "test summary", shouldSummarize: true}
	s, _, db := setupMockService(t, &TestServiceOpts{summarizer: summarizer})
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

	// Note: summaryJobChans is now handled by asyncWorker in session/internal/summary
	// We can't directly verify distribution, but jobs should be processed
	// The test verifies that EnqueueSummaryJob works correctly
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

func TestEnqueueSummaryJob_NoAsyncWorkers(t *testing.T) {
	// Create service without async workers initialized.
	summarizer := &mockSummarizerImpl{
		summaryText:     "sync summary",
		shouldSummarize: true,
	}
	s, mock, db := setupMockService(t, &TestServiceOpts{
		summarizer: summarizer,
	})
	defer db.Close()

	// Note: summaryJobChans is now handled by asyncWorker in session/internal/summary
	// To simulate no async workers, create service without summarizer or with asyncSummaryNum=0

	sess := &session.Session{
		ID:        "test-session",
		AppName:   "test-app",
		UserID:    "test-user",
		UpdatedAt: time.Now(),
	}

	// Add events with different filterKeys to trigger cascade (not single filterKey
	// optimization). Version must be CurrentVersion for Filter() to use FilterKey.
	e1 := event.New("inv1", "author")
	e1.Timestamp = time.Now()
	e1.FilterKey = "branch1"
	e1.Version = event.CurrentVersion
	e1.Response = &model.Response{
		Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hello"}}},
	}
	sess.Events = append(sess.Events, *e1)

	e2 := event.New("inv2", "author")
	e2.Timestamp = time.Now()
	e2.FilterKey = "other-key"
	e2.Version = event.CurrentVersion
	e2.Response = &model.Response{
		Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "world"}}},
	}
	sess.Events = append(sess.Events, *e2)

	// Mock the database insert for sync processing.
	// CreateSessionSummaryWithCascade calls CreateSessionSummary twice when
	// filterKey != SummaryFilterKeyAllContents: once for the filterKey and once
	// for SummaryFilterKeyAllContents.
	mock.ExpectExec(regexp.QuoteMeta(fmt.Sprintf(`INSERT INTO %s (app_name, user_id, session_id, filter_key, summary, updated_at, expires_at, deleted_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, NULL)
		 ON CONFLICT (app_name, user_id, session_id, filter_key) WHERE deleted_at IS NULL
		 DO UPDATE SET
		   summary = EXCLUDED.summary,
		   updated_at = EXCLUDED.updated_at,
		   expires_at = EXCLUDED.expires_at`, s.tableSessionSummaries))).
		WithArgs("test-app", "test-user", "test-session", sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// Cascade to full-session summary.
	mock.ExpectExec(regexp.QuoteMeta(fmt.Sprintf(`INSERT INTO %s (app_name, user_id, session_id, filter_key, summary, updated_at, expires_at, deleted_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, NULL)
		 ON CONFLICT (app_name, user_id, session_id, filter_key) WHERE deleted_at IS NULL
		 DO UPDATE SET
		   summary = EXCLUDED.summary,
		   updated_at = EXCLUDED.updated_at,
		   expires_at = EXCLUDED.expires_at`, s.tableSessionSummaries))).
		WithArgs("test-app", "test-user", "test-session", sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// Should fall back to sync processing when no async workers.
	err := s.EnqueueSummaryJob(context.Background(), sess, "branch1", false)
	assert.NoError(t, err)

	// Verify all expectations were met.
	assert.NoError(t, mock.ExpectationsWereMet())
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

func TestCreateSessionSummary_MarshalError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	summarizer := &mockSummarizerImpl{summaryText: "test summary", shouldSummarize: true}

	s := createTestService(t, db, WithSummarizer(summarizer))
	ctx := context.Background()

	sess := &session.Session{
		ID:        "session-123",
		AppName:   "test-app",
		UserID:    "user-456",
		UpdatedAt: time.Now(),
		Summaries: make(map[string]*session.Summary),
	}

	// Mock: Insert new summary (this won't be reached due to marshal error)
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

	// This should not panic but should handle the error gracefully
	// The test is mainly to ensure we don't crash on marshal errors
	require.NotPanics(t, func() {
		err = s.CreateSessionSummary(ctx, sess, "", false)
		require.NoError(t, err) // Should not return error, just log warning
	})
}

func TestCreateSessionSummary_UpsertError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	summarizer := &mockSummarizerImpl{summaryText: "test summary", shouldSummarize: true}

	s := createTestService(t, db, WithSummarizer(summarizer))
	ctx := context.Background()

	sess := &session.Session{
		ID:        "session-123",
		AppName:   "test-app",
		UserID:    "user-456",
		UpdatedAt: time.Now(),
	}

	// Mock: UPSERT fails.
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

func TestEnqueueSummaryJob_SingleFilterKey_PersistsBothKeys(t *testing.T) {
	// This test verifies that when all events match a single filterKey,
	// the optimization path still persists BOTH the filterKey summary AND
	// the full-session summary (filter_key="") to the database.
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	// Disable order matching since we need to match two sets of SQL calls.
	mock.MatchExpectationsInOrder(false)

	summarizer := &mockSummarizerImpl{summaryText: "single-key-summary", shouldSummarize: true}

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

func TestEnqueueSummaryJob_AsyncProcessing(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	summarizer := &mockSummarizerImpl{summaryText: "async summary", shouldSummarize: true}

	s := createTestService(t, db, WithSummarizer(summarizer),
		WithAsyncSummaryNum(1), WithSummaryQueueSize(10))

	// Async workers are initialized in NewService if summarizer and asyncSummaryNum are set
	defer s.Close()

	ctx := context.Background()

	sess := &session.Session{
		ID:        "session-123",
		AppName:   "test-app",
		UserID:    "user-456",
		UpdatedAt: time.Now(),
	}

	// Mock: Insert summary via async worker
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

	// Wait for async processing
	time.Sleep(100 * time.Millisecond)
}
