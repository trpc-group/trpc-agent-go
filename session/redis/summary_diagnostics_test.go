//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package redis

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	isummary "trpc.group/trpc-go/trpc-agent-go/session/internal/summary"
	"trpc.group/trpc-go/trpc-agent-go/session/redis/internal/util"
)

// summaryDiagLogs captures summary records emitted through the package logger.
// The logger is process-global, so the recorder is synchronized.
type summaryDiagLogs struct {
	mu    sync.Mutex
	lines []string
}

func (l *summaryDiagLogs) add(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, line)
}

// record returns the single session summary record captured for the call.
func (l *summaryDiagLogs) record(t *testing.T) string {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		if strings.HasPrefix(line, "Session summary result:") {
			return line
		}
	}
	t.Fatalf("no session summary record in %q", l.lines)
	return ""
}

func captureSummaryDiagLogs(t *testing.T) *summaryDiagLogs {
	t.Helper()
	logs := &summaryDiagLogs{}
	oldDebug, oldInfo, oldWarn :=
		log.DebugfContext, log.InfofContext, log.WarnfContext
	capture := func(_ context.Context, format string, args ...any) {
		logs.add(fmt.Sprintf(format, args...))
	}
	log.DebugfContext, log.InfofContext, log.WarnfContext =
		capture, capture, capture
	t.Cleanup(func() {
		log.DebugfContext, log.InfofContext, log.WarnfContext =
			oldDebug, oldInfo, oldWarn
	})
	return logs
}

// diagSummarizer writes a fixed summary with a caller-chosen cutoff so tests
// can drive the set-if-newer decision in the storage layer.
type diagSummarizer struct {
	text string
}

func (s *diagSummarizer) ShouldSummarize(*session.Session) bool { return true }

func (s *diagSummarizer) Summarize(
	context.Context, *session.Session,
) (string, error) {
	return s.text, nil
}

func (s *diagSummarizer) SetPrompt(string)         {}
func (s *diagSummarizer) SetModel(model.Model)     {}
func (s *diagSummarizer) Metadata() map[string]any { return nil }

// diagSummarySession builds a session whose single event carries the cutoff the
// generated summary boundary will adopt.
func diagSummarySession(t *testing.T, svc *Service, at time.Time) *session.Session {
	t.Helper()
	key := session.Key{AppName: "app", UserID: "u1", SessionID: "diag-sum"}
	stored, err := svc.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)
	return &session.Session{
		ID:        stored.ID,
		AppName:   key.AppName,
		UserID:    key.UserID,
		Summaries: make(map[string]*session.Summary),
		Events: []event.Event{{
			ID:        "e1",
			Author:    "user",
			Timestamp: at,
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleUser,
					Content: "hello",
				},
			}}},
		}},
	}
}

// TestCreateSessionSummary_ReportsStoredWrite proves a durable Redis write is
// reported as stored rather than assumed from a nil error.
func TestCreateSessionSummary_ReportsStoredWrite(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(
		WithRedisClientURL(redisURL),
		WithSummarizer(&diagSummarizer{text: "generated"}),
	)
	require.NoError(t, err)
	defer svc.Close()

	logs := captureSummaryDiagLogs(t)
	sess := diagSummarySession(t, svc, time.Now().UTC())

	require.NoError(t,
		svc.CreateSessionSummary(context.Background(), sess, "", true))

	line := logs.record(t)
	require.Contains(t, line, "schema_version=1")
	require.Contains(t, line, "persist_result=stored")
	require.Contains(t, line, "outcome=success")
	require.Contains(t, line, `filter_key=""`)
	require.Contains(t, line, "filter_key_truncated=false")
}

// TestCreateSessionSummary_ReportsStaleWrite proves that a Redis set-if-newer
// script that deliberately skips the write is reported as stale instead of
// being reported as a backend-confirmed store. This is the diagnostic that separates a
// lost summary from a summary that was never generated.
func TestCreateSessionSummary_ReportsStaleWrite(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	defer cleanup()

	svc, err := NewService(
		WithRedisClientURL(redisURL),
		WithSummarizer(&diagSummarizer{text: "generated"}),
	)
	require.NoError(t, err)
	defer svc.Close()

	ctx := context.Background()
	newer := time.Now().UTC()

	// Persist a newer summary first so the second attempt is stale.
	first := diagSummarySession(t, svc, newer)
	require.NoError(t, svc.CreateSessionSummary(ctx, first, "", true))

	logs := captureSummaryDiagLogs(t)
	stale := diagSummarySession(t, svc, newer.Add(-time.Hour))
	require.NoError(t, svc.CreateSessionSummary(ctx, stale, "", true),
		"a skipped stale write must remain a successful call")

	line := logs.record(t)
	require.Contains(t, line, "schema_version=1")
	require.Contains(t, line, "persist_result=stale")
	require.Contains(t, line, "outcome=stale_write")

	text, ok := svc.GetSessionSummaryText(ctx, &session.Session{
		ID:      first.ID,
		AppName: first.AppName,
		UserID:  first.UserID,
	})
	require.True(t, ok)
	require.Equal(t, "generated", text,
		"the newer stored summary must be preserved")
}

// TestSummaryWriteRecorderMapsUnexpectedLuaReply proves an unrecognized
// set-if-newer reply is diagnostic uncertainty, never stored/stale/success/
// persistence_error, and does not become a CreateSessionSummary error.
func TestSummaryWriteRecorderMapsUnexpectedLuaReply(t *testing.T) {
	logs := captureSummaryDiagLogs(t)
	sess := &session.Session{ID: "s", AppName: "app", UserID: "u1"}
	_, att := isummary.BeginAttempt(context.Background(), sess, "")
	att.Summarized(true, nil)

	write := util.ParseSummaryWriteResult("1")
	require.Equal(t, util.SummaryWriteUnknown, write)
	err := summaryWriteRecorder(att)(write, nil)
	require.NoError(t, err, "unknown reply must not add a business error")
	att.Report()

	line := logs.record(t)
	require.Contains(t, line, "schema_version=1")
	require.Contains(t, line, "persist_result=unknown")
	require.Contains(t, line, "outcome=unknown_write")
	require.NotContains(t, line, "persist_result=stored")
	require.NotContains(t, line, "persist_result=stale")
	require.NotContains(t, line, "persist_result=error")
	require.NotContains(t, line, "outcome=success")
	require.NotContains(t, line, "outcome=persistence_error")
	require.NotContains(t, line, "unexpected set-if-newer")
}

// TestSummaryWriteRecorderMapsScriptError proves a real script failure still
// surfaces as a persistence error.
func TestSummaryWriteRecorderMapsScriptError(t *testing.T) {
	logs := captureSummaryDiagLogs(t)
	sess := &session.Session{ID: "s", AppName: "app", UserID: "u1"}
	_, att := isummary.BeginAttempt(context.Background(), sess, "")
	att.Summarized(true, nil)

	err := summaryWriteRecorder(att)(
		util.SummaryWriteUnknown,
		fmt.Errorf("store summary failed: %w", errors.New("boom")),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "store summary failed")
	att.Report()

	line := logs.record(t)
	require.Contains(t, line, "persist_result=error")
	require.Contains(t, line, "outcome=persistence_error")
	require.NotContains(t, line, "boom")
}
