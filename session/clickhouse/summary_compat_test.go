//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package clickhouse

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
	storage "trpc.group/trpc-go/trpc-agent-go/storage/clickhouse"
)

func newCompatService(t *testing.T, mockCli *mockClient, sum *mockSummarizer) *Service {
	t.Helper()
	storage.SetClientBuilder(func(...storage.ClientBuilderOpt) (storage.Client, error) {
		return &mockClient{}, nil
	})
	s, err := NewService(
		WithSummarizer(sum),
		WithSkipDBInit(true),
		WithClickHouseDSN("clickhouse://localhost:9000"),
	)
	require.NoError(t, err)
	s.chClient = mockCli
	return s
}

func compatSession() *session.Session {
	return &session.Session{
		ID:        "sess1",
		AppName:   "app1",
		UserID:    "user1",
		Summaries: make(map[string]*session.Summary),
		Events: []event.Event{{
			ID:        "1",
			Timestamp: time.Now().Add(-time.Minute),
		}},
	}
}

// TestCreateSessionSummary_WrapsSummarizeError pins the error message this
// backend has always returned for a failed summarization stage.
func TestCreateSessionSummary_WrapsSummarizeError(t *testing.T) {
	mockSum := &mockSummarizer{
		summarizeFunc: func(context.Context, *session.Session) (string, error) {
			return "", assert.AnError
		},
	}
	s := newCompatService(t, &mockClient{}, mockSum)

	err := s.CreateSessionSummary(context.Background(), compatSession(), "key", true)
	require.Error(t, err)
	assert.True(t,
		strings.HasPrefix(err.Error(), "summarize and persist failed: "),
		"unexpected error message: %s", err.Error())
	assert.ErrorIs(t, err, assert.AnError)
}

// TestCreateSessionSummary_StaleWriteIsSkipped pins the set-if-newer contract:
// a summary that would move the boundary backwards is not written and the call
// still succeeds.
func TestCreateSessionSummary_StaleWriteIsSkipped(t *testing.T) {
	sess := compatSession()
	newer := session.Summary{
		Summary:   "persisted",
		UpdatedAt: time.Now(),
		Boundary:  session.NewSummaryBoundary("1", time.Now().Add(time.Hour)),
	}
	newerBytes, err := json.Marshal(newer)
	require.NoError(t, err)

	execCalls := 0
	mockCli := &mockClient{
		queryFunc: func(context.Context, string, ...any) (driver.Rows, error) {
			return newMockRows([][]any{{newerBytes}}), nil
		},
		execFunc: func(context.Context, string, ...any) error {
			execCalls++
			return nil
		},
	}
	s := newCompatService(t, mockCli, &mockSummarizer{})

	require.NoError(t,
		s.CreateSessionSummary(context.Background(), sess, "key", true))
	require.NotNil(t, sess.Summaries["key"],
		"the summary must be generated before the staleness check runs")
	assert.Zero(t, execCalls, "a stale summary must not be written")
}

// TestCreateSessionSummary_WritesInMemorySummaryVerbatim pins that this backend
// serializes the non-nil in-memory summary for the target filter key and then
// reaches the staleness check and the insert. A nil summary is rejected before
// this path, matching the other backends.
func TestCreateSessionSummary_WritesInMemorySummaryVerbatim(t *testing.T) {
	sess := compatSession()
	var staleChecked bool
	var written string
	mockCli := &mockClient{
		queryFunc: func(context.Context, string, ...any) (driver.Rows, error) {
			staleChecked = true
			return newMockRows(nil), nil
		},
		execFunc: func(_ context.Context, _ string, args ...any) error {
			require.Len(t, args, 9)
			written = args[4].(string)
			return nil
		},
	}
	s := newCompatService(t, mockCli, &mockSummarizer{})

	require.NoError(t,
		s.CreateSessionSummary(context.Background(), sess, "key", true))
	assert.True(t, staleChecked, "the staleness check must always run")

	want, err := json.Marshal(sess.Summaries["key"])
	require.NoError(t, err)
	assert.Equal(t, string(want), written)
}

// TestCreateSessionSummary_StaleCheckErrorIsWrapped pins the error message for
// a failed set-if-newer check.
func TestCreateSessionSummary_StaleCheckErrorIsWrapped(t *testing.T) {
	mockCli := &mockClient{
		queryFunc: func(context.Context, string, ...any) (driver.Rows, error) {
			return nil, assert.AnError
		},
	}
	s := newCompatService(t, mockCli, &mockSummarizer{})

	err := s.CreateSessionSummary(context.Background(), compatSession(), "key", true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "check existing summary failed")
}
