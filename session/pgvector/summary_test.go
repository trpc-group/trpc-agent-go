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
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

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
