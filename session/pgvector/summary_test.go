//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package pgvector

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// activeSummarizer is a mock summarizer that actually
// produces summaries.
type activeSummarizer struct {
	text string
	err  error
}

func (a *activeSummarizer) ShouldSummarize(
	_ *session.Session,
) bool {
	return true
}

func (a *activeSummarizer) Summarize(
	_ context.Context, _ *session.Session,
) (string, error) {
	return a.text, a.err
}

func (a *activeSummarizer) SummarizeWithFilter(
	_ context.Context, _ *session.Session,
	_ string,
) (string, error) {
	return a.text, a.err
}

func (a *activeSummarizer) SetPrompt(_ string)     {}
func (a *activeSummarizer) SetModel(_ model.Model) {}
func (a *activeSummarizer) Metadata() map[string]any {
	return nil
}

// --- Tests for CreateSessionSummary ---

func TestCreateSessionSummary_NilSummarizer(
	t *testing.T,
) {
	s, _, db := newTestService(t, nil)
	defer db.Close()
	s.opts.summarizer = nil

	err := s.CreateSessionSummary(
		context.Background(),
		session.NewSession("app", "user", "sess"),
		"", false,
	)
	assert.NoError(t, err)
}

func TestCreateSessionSummary_NilSession(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()
	s.opts.summarizer = &mockSummarizer{}

	err := s.CreateSessionSummary(
		context.Background(), nil, "", false,
	)
	assert.Error(t, err)
	assert.Equal(t, session.ErrNilSession, err)
}

func TestCreateSessionSummary_InvalidKey(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()
	s.opts.summarizer = &mockSummarizer{}

	sess := &session.Session{
		AppName: "", UserID: "u", ID: "s",
	}
	err := s.CreateSessionSummary(
		context.Background(), sess, "", false,
	)
	assert.Error(t, err)
}

// --- Tests for EnqueueSummaryJob ---

func TestEnqueueSummaryJob_NilSummarizer(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()
	s.opts.summarizer = nil

	err := s.EnqueueSummaryJob(
		context.Background(),
		session.NewSession("app", "user", "sess"),
		"", false,
	)
	assert.NoError(t, err)
}

func TestEnqueueSummaryJob_NilSession(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()
	s.opts.summarizer = &mockSummarizer{}

	err := s.EnqueueSummaryJob(
		context.Background(), nil, "", false,
	)
	assert.Error(t, err)
	assert.Equal(t, session.ErrNilSession, err)
}

func TestEnqueueSummaryJob_InvalidKey(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()
	s.opts.summarizer = &mockSummarizer{}

	sess := &session.Session{
		AppName: "", UserID: "u", ID: "s",
	}
	err := s.EnqueueSummaryJob(
		context.Background(), sess, "", false,
	)
	assert.Error(t, err)
}

// --- Tests for GetSessionSummaryText ---

func TestGetSessionSummaryText_NilSession(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	text, ok := s.GetSessionSummaryText(
		context.Background(), nil,
	)
	assert.Empty(t, text)
	assert.False(t, ok)
}

func TestGetSessionSummaryText_InvalidKey(t *testing.T) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	sess := &session.Session{
		AppName: "", UserID: "u", ID: "s",
	}
	text, ok := s.GetSessionSummaryText(
		context.Background(), sess,
	)
	assert.Empty(t, text)
	assert.False(t, ok)
}

func TestGetSessionSummaryText_FromDB(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	sess := session.NewSession("app", "user", "sess")

	sum := session.Summary{Summary: "db summary"}
	sumBytes, _ := json.Marshal(sum)

	rows := sqlmock.NewRows([]string{"summary"}).
		AddRow(sumBytes)
	mock.ExpectQuery("SELECT summary FROM").
		WillReturnRows(rows)

	text, ok := s.GetSessionSummaryText(
		context.Background(), sess,
	)
	assert.True(t, ok)
	assert.Equal(t, "db summary", text)
}

func TestGetSessionSummaryText_NotFound(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	sess := session.NewSession("app", "user", "sess")

	// Primary query returns no rows.
	mock.ExpectQuery("SELECT summary FROM").
		WillReturnRows(
			sqlmock.NewRows([]string{"summary"}),
		)
	// Fallback query returns no rows.
	mock.ExpectQuery("SELECT summary FROM").
		WillReturnRows(
			sqlmock.NewRows([]string{"summary"}),
		)

	text, ok := s.GetSessionSummaryText(
		context.Background(), sess,
		session.WithSummaryFilterKey("custom"),
	)
	assert.Empty(t, text)
	assert.False(t, ok)
}

func TestGetSessionSummaryText_FallbackInvalidJSON(
	t *testing.T,
) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	sess := session.NewSession("app", "user", "sess")

	// Primary query returns invalid JSON.
	mock.ExpectQuery("SELECT summary FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"summary"},
		).AddRow([]byte(`{invalid`)))

	text, ok := s.GetSessionSummaryText(
		context.Background(), sess,
		session.WithSummaryFilterKey("custom"),
	)
	assert.Empty(t, text)
	assert.False(t, ok)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSessionSummaryText_PrimaryInvalidJSON(
	t *testing.T,
) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	sess := session.NewSession("app", "user", "sess")

	// Primary query returns invalid JSON for default
	// filter key.
	mock.ExpectQuery("SELECT summary FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"summary"},
		).AddRow([]byte(`{invalid`)))

	text, ok := s.GetSessionSummaryText(
		context.Background(), sess,
	)
	assert.Empty(t, text)
	assert.False(t, ok)
}

func TestGetSessionSummaryText_DBErrorNoFallback(
	t *testing.T,
) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	sess := session.NewSession("app", "user", "sess")

	// Primary query error.
	mock.ExpectQuery("SELECT summary FROM").
		WillReturnError(fmt.Errorf("db error"))

	text, ok := s.GetSessionSummaryText(
		context.Background(), sess,
		session.WithSummaryFilterKey("custom"),
	)
	assert.Empty(t, text)
	assert.False(t, ok)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateSessionSummary_SummarizerNoUpdate(
	t *testing.T,
) {
	s, _, db := newTestService(t, nil)
	defer db.Close()
	s.opts.summarizer = &mockSummarizer{}

	sess := session.NewSession("app", "user", "sess")
	// mockSummarizer.ShouldSummarize returns false,
	// isummary.SummarizeSession returns updated=false.
	err := s.CreateSessionSummary(
		context.Background(), sess, "", false,
	)
	assert.NoError(t, err)
}

func TestCreateSessionSummary_Success(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()
	s.opts.summarizer = &activeSummarizer{
		text: "test summary output",
	}

	sess := session.NewSession("app", "user", "sess")
	// Add events so SummarizeSession has delta events.
	sess.Events = []event.Event{
		{
			InvocationID: "inv-1",
			Timestamp:    time.Now().Add(-time.Minute),
			Response: &model.Response{
				Choices: []model.Choice{
					{Message: model.Message{
						Role:    model.RoleUser,
						Content: "hello",
					}},
				},
			},
		},
	}

	// Expect the upsert query.
	mock.ExpectExec("INSERT INTO .* ON CONFLICT").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.CreateSessionSummary(
		context.Background(), sess, "", false,
	)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateSessionSummary_UpsertError(
	t *testing.T,
) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()
	s.opts.summarizer = &activeSummarizer{
		text: "summary text",
	}

	sess := session.NewSession("app", "user", "sess")
	sess.Events = []event.Event{
		{
			InvocationID: "inv-1",
			Timestamp:    time.Now().Add(-time.Minute),
			Response: &model.Response{
				Choices: []model.Choice{
					{Message: model.Message{
						Role:    model.RoleUser,
						Content: "hello",
					}},
				},
			},
		},
	}

	mock.ExpectExec("INSERT INTO .* ON CONFLICT").
		WillReturnError(fmt.Errorf("db error"))

	err := s.CreateSessionSummary(
		context.Background(), sess, "", false,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(),
		"upsert summary failed")
}

func TestCreateSessionSummary_SummarizerError(
	t *testing.T,
) {
	s, _, db := newTestService(t, nil)
	defer db.Close()
	s.opts.summarizer = &activeSummarizer{
		err: fmt.Errorf("summarizer error"),
	}

	sess := session.NewSession("app", "user", "sess")
	sess.Events = []event.Event{
		{
			InvocationID: "inv-1",
			Timestamp:    time.Now().Add(-time.Minute),
			Response: &model.Response{
				Choices: []model.Choice{
					{Message: model.Message{
						Role:    model.RoleUser,
						Content: "hello",
					}},
				},
			},
		},
	}

	err := s.CreateSessionSummary(
		context.Background(), sess, "", false,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "summarize session")
}

func TestCreateSessionSummary_WithTTL(t *testing.T) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()
	s.opts.summarizer = &activeSummarizer{
		text: "summary with ttl",
	}
	s.opts.sessionTTL = 30 * time.Minute

	sess := session.NewSession("app", "user", "sess")
	sess.Events = []event.Event{
		{
			InvocationID: "inv-1",
			Timestamp:    time.Now().Add(-time.Minute),
			Response: &model.Response{
				Choices: []model.Choice{
					{Message: model.Message{
						Role:    model.RoleUser,
						Content: "hello",
					}},
				},
			},
		},
	}

	mock.ExpectExec("INSERT INTO .* ON CONFLICT").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := s.CreateSessionSummary(
		context.Background(), sess, "", false,
	)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSessionSummaryText_FallbackFound(
	t *testing.T,
) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	sess := session.NewSession("app", "user", "sess")

	// Primary query returns empty.
	mock.ExpectQuery("SELECT summary FROM").
		WillReturnRows(
			sqlmock.NewRows([]string{"summary"}),
		)

	// Fallback query returns a summary.
	sum := session.Summary{Summary: "fallback summary"}
	sumBytes, _ := json.Marshal(sum)
	mock.ExpectQuery("SELECT summary FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"summary"},
		).AddRow(sumBytes))

	text, ok := s.GetSessionSummaryText(
		context.Background(), sess,
		session.WithSummaryFilterKey("custom"),
	)
	assert.True(t, ok)
	assert.Equal(t, "fallback summary", text)
}

func TestGetSessionSummaryText_FallbackInvalidJSON2(
	t *testing.T,
) {
	s, mock, db := newTestService(t, nil)
	defer db.Close()

	sess := session.NewSession("app", "user", "sess")

	// Primary returns empty.
	mock.ExpectQuery("SELECT summary FROM").
		WillReturnRows(
			sqlmock.NewRows([]string{"summary"}),
		)

	// Fallback returns invalid JSON.
	mock.ExpectQuery("SELECT summary FROM").
		WillReturnRows(sqlmock.NewRows(
			[]string{"summary"},
		).AddRow([]byte(`{bad`)))

	text, ok := s.GetSessionSummaryText(
		context.Background(), sess,
		session.WithSummaryFilterKey("custom"),
	)
	assert.Empty(t, text)
	assert.False(t, ok)
}

func TestGetSessionSummaryText_FromSession(
	t *testing.T,
) {
	s, _, db := newTestService(t, nil)
	defer db.Close()

	sess := session.NewSession("app", "user", "sess")
	sess.Summaries = map[string]*session.Summary{
		session.SummaryFilterKeyAllContents: {
			Summary:   "in-memory summary",
			UpdatedAt: time.Now(),
		},
	}

	// Should return from session without DB query.
	text, ok := s.GetSessionSummaryText(
		context.Background(), sess,
	)
	assert.True(t, ok)
	assert.Equal(t, "in-memory summary", text)
}
