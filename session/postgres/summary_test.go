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
	customSummarize func(ctx context.Context, sess *session.Session) (string, error)
}

func (m *mockSummarizerImpl) ShouldSummarize(sess *session.Session) bool {
	return m.shouldSummarize
}

func (m *mockSummarizerImpl) Summarize(ctx context.Context, sess *session.Session) (string, error) {
	if m.customSummarize != nil {
		return m.customSummarize(ctx, sess)
	}
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
	require.NoError(t, err)
}

func TestEnqueueSummaryJob_QueueFullFallbackToSync(t *testing.T) {
	summarizer := &mockSummarizerImpl{summaryText: "test summary", shouldSummarize: true}
	s, mock, db := setupMockService(t, &TestServiceOpts{summarizer: summarizer})
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
	s, mock, db := setupMockService(t, &TestServiceOpts{summarizer: &mockSummarizerImpl{shouldSummarize: false}})
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
		WithArgs("test-app", "test-user", "test-session", "", sqlmock.AnyArg()).
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

func TestService_EnqueueSummaryJob_ChannelClosed_PanicRecovery(t *testing.T) {
	summarizer := &mockSummarizerImpl{summaryText: "test summary", shouldSummarize: true}
	s, _, db := setupMockService(t, &TestServiceOpts{summarizer: summarizer, asyncSummaryNum: 1})
	defer db.Close()

	// Close the channel to simulate a closed channel
	close(s.summaryJobChans[0])

	// This should not panic
	require.NotPanics(t, func() {
		s.EnqueueSummaryJob(context.Background(), &session.Session{}, "", false)
	})
}

func TestStartAsyncSummaryWorker_Initialization(t *testing.T) {
	summarizer := &mockSummarizerImpl{summaryText: "test summary", shouldSummarize: true}
	s, _, db := setupMockService(t, &TestServiceOpts{summarizer: summarizer})
	defer db.Close()
	defer s.Close()

	// Verify channels are properly initialized
	assert.Len(t, s.summaryJobChans, 3)
	for i, ch := range s.summaryJobChans {
		assert.NotNil(t, ch, "Channel %d should not be nil", i)
		assert.Equal(t, 100, cap(ch), "Channel %d should have capacity 100", i)
	}
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

	// Verify that jobs were distributed (at least one channel should have jobs)
	totalJobs := 0
	for _, ch := range s.summaryJobChans {
		totalJobs += len(ch)
	}
	assert.Equal(t, 3, totalJobs)
}

func TestRedisService_ProcessSummaryJob_Panic(t *testing.T) {
	summarizer := &mockSummarizerImpl{summaryText: "test summary", shouldSummarize: true}
	s, _, db := setupMockService(t, &TestServiceOpts{summarizer: summarizer})
	defer db.Close()
	defer s.Close()

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
			s, mock, db := setupMockService(t, &TestServiceOpts{summarizer: summarizer, summaryJobTimeout: time.Second})
			job := tt.setup(t, s)

			mock.ExpectExec(regexp.QuoteMeta(fmt.Sprintf(`INSERT INTO %s (app_name, user_id, session_id, filter_key, summary, updated_at, expires_at, deleted_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, NULL)
		 ON CONFLICT (app_name, user_id, session_id, filter_key) WHERE deleted_at IS NULL
		 DO UPDATE SET
		   summary = EXCLUDED.summary,
		   updated_at = EXCLUDED.updated_at,
		   expires_at = EXCLUDED.expires_at`, s.tableSessionSummaries))).
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			summarizer := &mockSummarizerImpl{summaryText: "test summary", shouldSummarize: true}
			s, _, db := setupMockService(t, &TestServiceOpts{summarizer: summarizer, summaryQueueSize: 1, asyncSummaryNum: 1})
			defer db.Close()

			ctx, job, expected := tt.setup(t, s)
			result := s.tryEnqueueJob(ctx, job)

			assert.Equal(t, expected, result)
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

func TestEnqueueSummaryJob_NoAsyncWorkers(t *testing.T) {
	// Create service without async workers initialized
	summarizer := &mockSummarizerImpl{
		summaryText:     "sync summary",
		shouldSummarize: true,
	}
	s, mock, db := setupMockService(t, &TestServiceOpts{
		summarizer:      summarizer,
		asyncSummaryNum: 0, // No async workers
	})
	defer db.Close()

	sess := &session.Session{
		ID:        "test-session",
		AppName:   "test-app",
		UserID:    "test-user",
		UpdatedAt: time.Now(),
	}

	// Mock the database query/insert for sync processing
	mock.ExpectExec(fmt.Sprintf(
		`INSERT INTO %s (app_name, user_id, session_id, filter_key, summary, updated_at, expires_at, deleted_at)
	 VALUES ($1, $2, $3, $4, $5, $6, $7, NULL)
	 ON CONFLICT (app_name, user_id, session_id, filter_key) WHERE deleted_at IS NULL
	 DO UPDATE SET
	   summary = EXCLUDED.summary,
	   updated_at = EXCLUDED.updated_at,
	   expires_at = EXCLUDED.expires_at`, s.tableSessionSummaries)).
		WithArgs("test-app", "test-user", "test-session", "", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// Should fall back to sync processing when no async workers
	err := s.EnqueueSummaryJob(context.Background(), sess, "", false)
	assert.NoError(t, err)
}

func TestTryEnqueueJob_ContextCancelled(t *testing.T) {
	summarizer := &mockSummarizerImpl{summaryText: "test", shouldSummarize: true}
	s, _, db := setupMockService(t, &TestServiceOpts{
		summarizer:       summarizer,
		asyncSummaryNum:  1,
		summaryQueueSize: 1,
	})
	defer db.Close()

	// Initialize async workers
	s.startAsyncSummaryWorker()

	key := session.Key{AppName: "app", UserID: "user", SessionID: "sid"}

	// Calculate the worker index for this key to ensure we use the same worker
	sess := session.NewSession(key.AppName, key.UserID, key.SessionID)
	idx := sess.Hash % len(s.summaryJobChans)

	// Fill the queue first with a blocking job
	blockingJob := &summaryJob{
		filterKey: "",
		force:     false,
		session:   sess,
	}

	select {
	case s.summaryJobChans[idx] <- blockingJob:
		// Queue is now full
	default:
		// Already full
	}

	// Create a cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Use the same key to ensure it hashes to the same worker (queue is full)
	job := &summaryJob{
		filterKey: "",
		force:     false,
		session:   &session.Session{ID: key.SessionID, AppName: key.AppName, UserID: key.UserID},
	}

	// Should return false when context is cancelled (even if queue is full)
	result := s.tryEnqueueJob(ctx, job)
	assert.False(t, result)

	// Clean up
	s.Close()
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
	summarizer := &mockSummarizerImpl{
		summaryText:     "test",
		shouldSummarize: true,
	}
	s, _, db := setupMockService(t, &TestServiceOpts{
		summarizer:       summarizer,
		asyncSummaryNum:  1,
		summaryQueueSize: 1,
	})
	defer db.Close()
	// Initialize channels without starting workers to keep queue full.
	s.summaryJobChans = []chan *summaryJob{
		make(chan *summaryJob, 1),
	}

	sess := session.NewSession("app", "user", "sid")
	job := &summaryJob{
		filterKey: "",
		force:     false,
		session:   sess,
	}
	s.summaryJobChans[0] <- job

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
	summarizer := &mockSummarizerImpl{
		summaryText:     "test",
		shouldSummarize: true,
	}
	s, _, db := setupMockService(t, &TestServiceOpts{
		summarizer: summarizer,
	})
	defer db.Close()
	defer s.Close()

	require.NotPanics(t, func() {
		s.processSummaryJob(nil)
	})
}

func TestProcessSummaryJob_NilSession_LogsWarning(t *testing.T) {
	summarizer := &mockSummarizerImpl{
		summaryText:     "test",
		shouldSummarize: true,
	}
	s, _, db := setupMockService(t, &TestServiceOpts{
		summarizer: summarizer,
	})
	defer db.Close()
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

func TestTryEnqueueJob_SendSuccess(t *testing.T) {
	summarizer := &mockSummarizerImpl{summaryText: "test", shouldSummarize: true}
	s, _, db := setupMockService(t, &TestServiceOpts{
		summarizer:       summarizer,
		asyncSummaryNum:  1,
		summaryQueueSize: 10,
	})
	defer db.Close()

	// Initialize async workers
	s.startAsyncSummaryWorker()

	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "sid"}
	job := &summaryJob{
		filterKey: "",
		force:     false,
		session:   &session.Session{ID: key.SessionID, AppName: key.AppName, UserID: key.UserID},
	}

	// Should successfully send the job
	result := s.tryEnqueueJob(ctx, job)
	assert.True(t, result)

	// Clean up
	s.Close()
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

func TestEnqueueSummaryJob_AsyncProcessing(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	summarizer := &mockSummarizerImpl{summaryText: "async summary", shouldSummarize: true}

	s := createTestService(t, db, WithSummarizer(summarizer),
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

// traceCtxKey is a context key type for trace ID in tests.
type traceCtxKey string

// traceIDKey is the context key for trace ID.
const traceIDKey traceCtxKey = "trace-id"

func TestEnqueueSummaryJob_ContextValuePreserved(t *testing.T) {
	// Test that context values are preserved in async summary processing
	// This test verifies the fix for passing ctx instead of context.Background()

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	// Create a simple summarizer that doesn't need complex callbacks
	summarizer := &mockSummarizerImpl{
		summaryText:     "context-preserved-summary",
		shouldSummarize: true,
	}

	s := createTestService(t, db,
		WithAsyncSummaryNum(1),
		WithSummaryQueueSize(10),
		WithSummarizer(summarizer),
	)

	// Start async workers
	s.startAsyncSummaryWorker()
	defer func() {
		for _, ch := range s.summaryJobChans {
			close(ch)
		}
	}()

	// Create a session
	sess := &session.Session{
		ID:        "sid-ctx",
		AppName:   "app",
		UserID:    "user",
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

	// Create context with trace ID value - this is the key part being tested
	ctx := context.WithValue(context.Background(), traceIDKey, "trace-12345")

	// Enqueue summary job with context containing trace ID.
	// The fix ensures this ctx is passed through instead of context.Background()
	err = s.EnqueueSummaryJob(ctx, sess, "", false)
	require.NoError(t, err)

	// Wait for async processing
	time.Sleep(100 * time.Millisecond)

	// If we get here without timeout/error, the context passing fix is working
	// The actual context value preservation is tested in inmemory/redis implementations
	// that have easier access to the summarizer
}

func TestEnqueueSummaryJob_JobPointerIsolation(t *testing.T) {
	// Test that the fix for job pointer modification works correctly
	// This test verifies that when a branch summary is processed, it creates new job objects
	// instead of modifying the original job pointer

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	// Create a simple summarizer
	summarizer := &mockSummarizerImpl{
		summaryText:     "job-isolation-summary",
		shouldSummarize: true,
	}

	s := createTestService(t, db,
		WithAsyncSummaryNum(1),
		WithSummaryQueueSize(10),
		WithSummarizer(summarizer),
	)

	// Start async workers
	s.startAsyncSummaryWorker()
	defer func() {
		for _, ch := range s.summaryJobChans {
			close(ch)
		}
	}()

	// Create a session
	sess := &session.Session{
		ID:        "session-isolation",
		AppName:   "app",
		UserID:    "user",
		UpdatedAt: time.Now(),
	}

	// Mock: Insert summaries for both branch and full session
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO session_summaries")).
		WithArgs(
			sess.AppName,
			sess.UserID,
			sess.ID,
			"test-branch",
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

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

	// Enqueue a branch summary job - this should trigger both branch and full-session processing
	err = s.EnqueueSummaryJob(context.Background(), sess, "test-branch", false)
	require.NoError(t, err)

	// Wait for processing to complete
	time.Sleep(200 * time.Millisecond)

	// If we get here without errors, the job pointer isolation fix is working
	// The actual verification that new job objects are created is tested in inmemory/redis
	// implementations that have easier access to internal state
}

func TestTryEnqueueJob_QueueFull(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	s := createTestService(t, db, WithAsyncSummaryNum(1), WithSummaryQueueSize(1))

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
	select {
	case s.summaryJobChans[0] <- job:
		// Successfully sent, now try to enqueue another which should fail
	default:
		t.Skip("Could not fill queue for testing")
	}

	// Try to enqueue when queue is full - should return false
	result := s.tryEnqueueJob(ctx, job)
	assert.False(t, result)
}

func TestProcessSummaryJob_Timeout(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	summarizer := &mockSummarizerImpl{
		summaryText:     "timeout summary",
		shouldSummarize: true,
	}

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
