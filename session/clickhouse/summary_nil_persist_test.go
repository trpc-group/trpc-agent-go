//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package clickhouse

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/session"
	isummary "trpc.group/trpc-go/trpc-agent-go/session/internal/summary"
)

type persistLogs struct {
	mu    sync.Mutex
	debug []string
	info  []string
	warn  []string
}

func capturePersistLogs(t *testing.T) *persistLogs {
	t.Helper()
	logs := &persistLogs{}
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

func (l *persistLogs) add(level *[]string, line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	*level = append(*level, line)
}

func (l *persistLogs) summaryRecord(t *testing.T) (level, line string) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, group := range []struct {
		level string
		lines []string
	}{{"warn", l.warn}, {"info", l.info}, {"debug", l.debug}} {
		for _, candidate := range group.lines {
			if strings.HasPrefix(candidate, "Session summary result:") {
				return group.level, candidate
			}
		}
	}
	t.Fatalf("no session summary record in %q %q %q", l.debug, l.info, l.warn)
	return "", ""
}

// TestPersistSessionSummary_NilSummaryDoesNotInsert proves the persist stage
// classifies a nil in-memory summary as PersistNoSummary: no INSERT, no stale
// query, and no stored outcome. CreateSessionSummary reaches this stage only
// after SummarizeSession reports updated=true.
func TestPersistSessionSummary_NilSummaryDoesNotInsert(t *testing.T) {
	logs := capturePersistLogs(t)
	execCalls := 0
	queryCalls := 0
	mockCli := &mockClient{
		queryFunc: func(context.Context, string, ...any) (driver.Rows, error) {
			queryCalls++
			return newMockRows(nil), nil
		},
		execFunc: func(context.Context, string, ...any) error {
			execCalls++
			return nil
		},
	}
	s := newCompatService(t, mockCli, &mockSummarizer{})

	sess := &session.Session{
		ID:      "nil-summary-sess",
		AppName: "app1",
		UserID:  "user1",
		Summaries: map[string]*session.Summary{
			"key": nil,
		},
	}
	key := session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID}
	ctx, att := isummary.BeginAttempt(context.Background(), sess, "key")
	att.Summarized(true, nil)

	require.NoError(t, s.persistSessionSummary(ctx, att, key, sess, "key"))
	att.Report()

	require.Zero(t, execCalls, "a nil summary must not INSERT")
	require.Zero(t, queryCalls, "a nil summary must not reach the stale check")

	level, line := logs.summaryRecord(t)
	require.Equal(t, "warn", level)
	require.Contains(t, line, "outcome=no_update")
	require.Contains(t, line, "persist_result="+string(isummary.PersistNoSummary))
	require.Contains(t, line, "updated=true")
	require.NotContains(t, line, "persist_result=stored")
	require.NotContains(t, line, "outcome=success")
}
