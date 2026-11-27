//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package inmemory_test

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
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

const (
	externalSecretTrigger = "tenant-42-escalation"
	externalSummaryText   = "SECRET-SUMMARY-CONTENT"
	externalEventText     = "SECRET-EVENT-CONTENT"
	externalUserID        = "SECRET-USER-ID"
)

type externalDiagLogs struct {
	mu    sync.Mutex
	debug []string
	info  []string
	warn  []string
}

func (l *externalDiagLogs) add(level *[]string, line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	*level = append(*level, line)
}

func (l *externalDiagLogs) snapshot() (debug, info, warn []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.debug...),
		append([]string(nil), l.info...),
		append([]string(nil), l.warn...)
}

func (l *externalDiagLogs) all() string {
	debug, info, warn := l.snapshot()
	return strings.Join(append(append(debug, info...), warn...), "\n")
}

func (l *externalDiagLogs) summaryRecord(t *testing.T) (level, line string) {
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

func captureExternalDiagLogs(t *testing.T) *externalDiagLogs {
	t.Helper()
	logs := &externalDiagLogs{}
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

type reportOnlyGateSummarizer struct {
	fired bool
	name  string
	text  string
}

func (s *reportOnlyGateSummarizer) ShouldSummarize(*session.Session) bool {
	return s.fired
}

func (s *reportOnlyGateSummarizer) ShouldSummarizeWithContext(
	ctx context.Context, _ *session.Session,
) bool {
	if report, ok := summary.ReportFromContext(ctx); ok {
		report.Trigger.Fired = s.fired
		report.Trigger.Name = s.name
		report.Trigger.Metric = s.name
		report.Trigger.Value = 7
	}
	return s.fired
}

func (s *reportOnlyGateSummarizer) Summarize(
	context.Context, *session.Session,
) (string, error) {
	return s.text, nil
}

func (s *reportOnlyGateSummarizer) SetPrompt(string)         {}
func (s *reportOnlyGateSummarizer) SetModel(model.Model)     {}
func (s *reportOnlyGateSummarizer) Metadata() map[string]any { return nil }

var _ summary.ContextAwareSummarizer = (*reportOnlyGateSummarizer)(nil)

func externalSession(id string) *session.Session {
	return &session.Session{
		ID:      id,
		AppName: "app",
		UserID:  externalUserID,
		Events: []event.Event{{
			Author:    "user",
			Timestamp: time.Now().Add(-time.Minute),
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleUser,
					Content: externalEventText,
				},
			}}},
		}},
	}
}

func TestCreateSessionSummaryIgnoresCallerReportTrigger(t *testing.T) {
	for _, tc := range []struct {
		name            string
		fired           bool
		wantLevel       string
		wantTriggered   string
		wantOutcome     string
		wantTrigger     string
		createInStorage bool
	}{
		{
			name:            "fired true stays unpublished none",
			fired:           true,
			wantLevel:       "info",
			wantTriggered:   "true",
			wantOutcome:     "success",
			wantTrigger:     "none",
			createInStorage: true,
		},
		{
			name:            "fired false stays unobserved none",
			fired:           false,
			wantLevel:       "debug",
			wantTriggered:   "false",
			wantOutcome:     "unobserved",
			wantTrigger:     "none",
			createInStorage: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureExternalDiagLogs(t)
			summarizer := &reportOnlyGateSummarizer{
				fired: tc.fired,
				name:  externalSecretTrigger,
				text:  externalSummaryText,
			}
			service := inmemory.NewSessionService(inmemory.WithSummarizer(summarizer))
			defer service.Close()

			sess := externalSession("external-" + strings.ReplaceAll(tc.name, " ", "-"))
			if tc.createInStorage {
				stored, err := service.CreateSession(context.Background(), session.Key{
					AppName:   sess.AppName,
					UserID:    sess.UserID,
					SessionID: sess.ID,
				}, nil)
				require.NoError(t, err)
				sess.ID = stored.ID
			}

			report := &summary.Report{}
			ctx := summary.ContextWithReport(context.Background(), report)
			require.NoError(t, service.CreateSessionSummary(ctx, sess, "", false))

			require.Equal(t, tc.fired, report.Trigger.Fired)
			require.Equal(t, externalSecretTrigger, report.Trigger.Name)
			require.Equal(t, externalSecretTrigger, report.Trigger.Metric)
			require.Equal(t, 7, report.Trigger.Value)

			level, line := logs.summaryRecord(t)
			require.Equal(t, tc.wantLevel, level)
			require.Contains(t, line, "triggered="+tc.wantTriggered)
			require.Contains(t, line, "outcome="+tc.wantOutcome)
			require.Contains(t, line, "trigger="+tc.wantTrigger)
			require.NotContains(t, line, "trigger=custom")
			require.NotContains(t, logs.all(), externalSecretTrigger)
			require.NotContains(t, logs.all(), externalSummaryText)
		})
	}
}
