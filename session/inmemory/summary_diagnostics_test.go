//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package inmemory

import (
	"context"
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
)

const (
	diagSummaryText = "SECRET-SUMMARY-CONTENT"
	diagEventText   = "SECRET-EVENT-CONTENT"
	diagSessionID   = "SECRET-SESSION-ID"
	diagUserID      = "SECRET-USER-ID"
)

// diagLogs captures summary records emitted through the package logger. The
// logger is process-global and summary work can run on worker goroutines, so
// the recorder is synchronized.
type diagLogs struct {
	mu    sync.Mutex
	debug []string
	info  []string
	warn  []string
}

func (l *diagLogs) add(level *[]string, line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	*level = append(*level, line)
}

func (l *diagLogs) snapshot() (debug, info, warn []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.debug...),
		append([]string(nil), l.info...),
		append([]string(nil), l.warn...)
}

func (l *diagLogs) all() string {
	debug, info, warn := l.snapshot()
	return strings.Join(append(append(debug, info...), warn...), "\n")
}

// summaryRecord returns the async summary record captured at any level.
func (l *diagLogs) summaryRecord(t *testing.T) (level, line string) {
	t.Helper()
	debug, info, warn := l.snapshot()
	for _, group := range []struct {
		level string
		lines []string
	}{{"warn", warn}, {"info", info}, {"debug", debug}} {
		for _, candidate := range group.lines {
			if strings.HasPrefix(candidate, "Session summary result:") {
				return group.level, candidate
			}
		}
	}
	t.Fatalf("no session summary record in %q %q %q", debug, info, warn)
	return "", ""
}

func captureDiagLogs(t *testing.T) *diagLogs {
	t.Helper()
	logs := &diagLogs{}
	oldDebug, oldInfo, oldWarn :=
		log.DebugfContext, log.InfofContext, log.WarnfContext
	log.DebugfContext = func(_ context.Context, format string, args ...any) {
		logs.add(&logs.debug, fmt.Sprintf(format, args...))
	}
	log.InfofContext = func(_ context.Context, format string, args ...any) {
		logs.add(&logs.info, fmt.Sprintf(format, args...))
	}
	log.WarnfContext = func(_ context.Context, format string, args ...any) {
		logs.add(&logs.warn, fmt.Sprintf(format, args...))
	}
	t.Cleanup(func() {
		log.DebugfContext = oldDebug
		log.InfofContext = oldInfo
		log.WarnfContext = oldWarn
	})
	return logs
}

type diagBackendSummarizer struct{ text string }

func (s *diagBackendSummarizer) ShouldSummarize(*session.Session) bool {
	return true
}

func (s *diagBackendSummarizer) Summarize(
	context.Context, *session.Session,
) (string, error) {
	return s.text, nil
}

func (s *diagBackendSummarizer) FilterEventsForSummary(
	events []event.Event,
) []event.Event {
	return events
}

func (s *diagBackendSummarizer) SetPrompt(string)         {}
func (s *diagBackendSummarizer) SetModel(model.Model)     {}
func (s *diagBackendSummarizer) Metadata() map[string]any { return nil }

func diagBackendSession() *session.Session {
	return &session.Session{
		ID:      diagSessionID,
		AppName: "app",
		UserID:  diagUserID,
		Events: []event.Event{{
			Author:    "user",
			Timestamp: time.Now().Add(-time.Minute),
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleUser,
					Content: diagEventText,
				},
			}}},
		}},
	}
}

// TestCreateSessionSummaryReportsSuccess proves the in-memory backend reports
// its persistence outcome through the shared diagnostics helper.
func TestCreateSessionSummaryReportsSuccess(t *testing.T) {
	logs := captureDiagLogs(t)
	service := NewSessionService(
		WithSummarizer(&diagBackendSummarizer{text: diagSummaryText}),
	)
	defer service.Close()

	stored, err := service.CreateSession(context.Background(), session.Key{
		AppName:   "app",
		UserID:    diagUserID,
		SessionID: diagSessionID,
	}, nil)
	require.NoError(t, err)
	sess := diagBackendSession()
	sess.ID = stored.ID

	require.NoError(t, service.CreateSessionSummary(
		context.Background(), sess, "", true,
	))

	level, line := logs.summaryRecord(t)
	require.Equal(t, "info", level)
	require.Contains(t, line, "schema_version=1")
	require.Contains(t, line, "outcome=success")
	require.Contains(t, line, `filter_key=""`)
	require.Contains(t, line, "filter_key_truncated=false")
	require.Contains(t, line, "persist_result=stored")
	require.Contains(t, line, "boundary_advanced=true")
	require.NotContains(t, logs.all(), diagSummaryText)
	require.NotContains(t, logs.all(), diagEventText)
	require.NotContains(t, logs.all(), diagUserID)

	text, ok := service.GetSessionSummaryText(context.Background(), sess)
	require.True(t, ok)
	require.Equal(t, diagSummaryText, text,
		"persistence behaviour must be unchanged")
}

// TestCreateSessionSummaryReportsPersistenceError covers a backend write that
// fails because the session is not in storage. The backend error text carries
// the session ID, which must never reach the diagnostic record.
func TestCreateSessionSummaryReportsPersistenceError(t *testing.T) {
	logs := captureDiagLogs(t)
	service := NewSessionService(
		WithSummarizer(&diagBackendSummarizer{text: diagSummaryText}),
	)
	defer service.Close()

	sess := diagBackendSession()
	err := service.CreateSessionSummary(context.Background(), sess, "", true)
	require.Error(t, err)
	require.Contains(t, err.Error(), diagSessionID,
		"the backend error contract must be unchanged")

	level, line := logs.summaryRecord(t)
	require.Equal(t, "warn", level)
	require.Contains(t, line, "schema_version=1")
	require.Contains(t, line, "outcome=persistence_error")
	require.Contains(t, line, "persist_result=error")
	require.Contains(t, line, "updated=true")
	require.NotContains(t, logs.all(), diagSessionID)
	require.NotContains(t, logs.all(), diagSummaryText)
	require.NotContains(t, logs.all(), diagUserID)
}
