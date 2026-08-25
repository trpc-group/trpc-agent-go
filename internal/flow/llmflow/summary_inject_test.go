//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package llmflow

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	imodelrequest "trpc.group/trpc-go/trpc-agent-go/internal/modelrequest"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/summaryinject"
	"trpc.group/trpc-go/trpc-agent-go/internal/summarydiag"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const injectedSummaryBlock = "Session summary: SECRET-SUMMARY-CONTENT"

// injectionLogs captures injection records emitted through the package logger.
// The logger is process-global, so the recorder is synchronized.
type injectionLogs struct {
	mu    sync.Mutex
	debug []string
	warn  []string
}

func captureInjectionLogs(t *testing.T) *injectionLogs {
	t.Helper()
	logs := &injectionLogs{}
	oldDebug, oldWarn := log.DebugfContext, log.WarnfContext
	log.DebugfContext = func(_ context.Context, format string, args ...any) {
		logs.append(&logs.debug, fmt.Sprintf(format, args...))
	}
	log.WarnfContext = func(_ context.Context, format string, args ...any) {
		logs.append(&logs.warn, fmt.Sprintf(format, args...))
	}
	t.Cleanup(func() {
		log.DebugfContext = oldDebug
		log.WarnfContext = oldWarn
	})
	return logs
}

func (l *injectionLogs) append(level *[]string, line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	*level = append(*level, line)
}

func (l *injectionLogs) snapshot() (debug, warn []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.debug...), append([]string(nil), l.warn...)
}

// record returns the single injection record captured at any level.
func (l *injectionLogs) record(t *testing.T) (level, line string) {
	t.Helper()
	debug, warn := l.snapshot()
	for _, candidate := range warn {
		if strings.Contains(candidate, "injection result") {
			requireInjectionRecord(t, candidate)
			return "warn", candidate
		}
	}
	for _, candidate := range debug {
		if strings.Contains(candidate, "injection result") {
			requireInjectionRecord(t, candidate)
			return "debug", candidate
		}
	}
	t.Fatalf("no injection record in %q %q", debug, warn)
	return "", ""
}

func requireInjectionRecord(t *testing.T, line string) {
	t.Helper()
	require.True(t,
		strings.HasPrefix(line, "Session summary injection result: schema_version=1,"),
		"schema_version=1 must follow the record name in %q", line)
}

func injectionInvocation(
	t *testing.T, selection summaryinject.Selection,
) *agent.Invocation {
	t.Helper()
	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{ID: "SECRET-SESSION-ID"}),
		agent.WithInvocationEventFilterKey("test-agent"),
	)
	inv.AgentName = "test-agent"
	summaryinject.Record(inv, selection)
	return inv
}

func TestReportSummaryInjectionReportsInjectedSummary(t *testing.T) {
	logs := captureInjectionLogs(t)
	inv := injectionInvocation(t, summaryinject.Selection{
		LookupStrategy:     summaryinject.LookupStrategyPrefix,
		LookupResult:       summaryinject.LookupResultExact,
		Selected:           true,
		BoundaryPresent:    true,
		StoredSummaries:    2,
		MatchingCandidates: 1,
		ScopedRequest:      true,
		SessionEvents:      12,
		HistoryMessages:    4,
		Block:              injectedSummaryBlock,
	})
	req := &model.Request{Messages: []model.Message{
		model.NewSystemMessage("instructions\n\n" + injectedSummaryBlock),
		model.NewUserMessage("current request"),
	}}

	reportSummaryInjection(context.Background(), inv, req)

	level, line := logs.record(t)
	require.Equal(t, "debug", level,
		"a correctly injected summary is routine")
	require.Contains(t, line,
		"outcome="+summaryInjectionOutcomeInjected)
	require.Contains(t, line, `filter_key="test-agent"`)
	require.Contains(t, line, "filter_key_truncated=false")
	require.Contains(t, line, "selected=true")
	require.Contains(t, line, "selected_block_present=true")
	require.NotContains(t, line, "injected=")
	require.Contains(t, line, "boundary_present=true")
	require.Contains(t, line, "stored_summaries=2")
	require.Contains(t, line, "matching_candidates=1")
	require.Contains(t, line, "lookup_strategy=prefix")
	require.Contains(t, line, "lookup_result=exact")
	require.Contains(t, line, "history_messages=4")
	require.Contains(t, line, "request_messages=2")
	require.NotContains(t, line, "SECRET-SUMMARY-CONTENT")
	require.NotContains(t, line, "SECRET-SESSION-ID")
}

// TestReportSummaryInjectionWarnsOnSelectedBlockMissing covers the only
// injection state that Warns: a summary was selected, but the original
// summary block is not observable in the same framework model.Request after
// GenerateContent returns. That does not claim the provider payload.
func TestReportSummaryInjectionWarnsOnSelectedBlockMissing(t *testing.T) {
	logs := captureInjectionLogs(t)
	inv := injectionInvocation(t, summaryinject.Selection{
		LookupStrategy:  summaryinject.LookupStrategyPrefix,
		LookupResult:    summaryinject.LookupResultExact,
		Selected:        true,
		StoredSummaries: 1,
		Block:           injectedSummaryBlock,
	})
	req := &model.Request{Messages: []model.Message{
		model.NewSystemMessage("instructions"),
		model.NewUserMessage("current request"),
	}}

	reportSummaryInjection(context.Background(), inv, req)

	level, line := logs.record(t)
	require.Equal(t, "warn", level)
	require.Contains(t, line,
		"outcome="+summaryInjectionOutcomeSelectedBlockMissing)
	require.Contains(t, line, "selected=true")
	require.Contains(t, line, "selected_block_present=false")
	require.NotContains(t, line, "injected=")
	require.NotContains(t, line, "SECRET-SUMMARY-CONTENT")
}

// TestReportSummaryInjectionReportsLegitimateMiss covers a scoped request whose
// branch has no summary yet while other branches do. That is normal and must
// not add warning noise.
func TestReportSummaryInjectionReportsLegitimateMiss(t *testing.T) {
	logs := captureInjectionLogs(t)
	inv := injectionInvocation(t, summaryinject.Selection{
		LookupStrategy:  summaryinject.LookupStrategyPrefix,
		LookupResult:    summaryinject.LookupResultNone,
		Selected:        false,
		ScopedRequest:   true,
		StoredSummaries: 3,
		SessionEvents:   40,
	})
	req := &model.Request{Messages: []model.Message{
		model.NewUserMessage("current request"),
	}}

	reportSummaryInjection(context.Background(), inv, req)

	level, line := logs.record(t)
	require.Equal(t, "debug", level)
	require.Contains(t, line,
		"outcome="+summaryInjectionOutcomeLookupMiss)
	require.Contains(t, line, "stored_summaries=3")
	require.Contains(t, line, "matching_candidates=0")
	require.Contains(t, line, "session_events=40")
}

// TestReportSummaryInjectionReportsScopeMismatch covers a branch-scoped
// request that finds nothing in scope while a full-session summary exists
// outside it. That unused summary is diagnostic at Debug; it does not mean
// the scoped history was dropped from this request.
func TestReportSummaryInjectionReportsScopeMismatch(t *testing.T) {
	logs := captureInjectionLogs(t)
	inv := injectionInvocation(t, summaryinject.Selection{
		LookupStrategy:     summaryinject.LookupStrategyPrefix,
		LookupResult:       summaryinject.LookupResultNone,
		Selected:           false,
		ScopedRequest:      true,
		StoredSummaries:    1,
		FullSessionPresent: true,
		SessionEvents:      120,
	})
	req := &model.Request{Messages: []model.Message{
		model.NewUserMessage("current request"),
	}}

	reportSummaryInjection(context.Background(), inv, req)

	level, line := logs.record(t)
	require.Equal(t, "debug", level)
	require.Contains(t, line,
		"outcome="+summaryInjectionOutcomeScopeMismatch)
	require.Contains(t, line, "full_session_summary=true")
	require.Contains(t, line, "matching_candidates=0")
	require.NotContains(t, line, "SECRET-SESSION-ID")
	_, warn := logs.snapshot()
	require.Empty(t, warn,
		"an unused full-session summary must not Warn")
}

func TestReportSummaryInjectionReportsNoStoredSummary(t *testing.T) {
	logs := captureInjectionLogs(t)
	inv := injectionInvocation(t, summaryinject.Selection{
		LookupStrategy: summaryinject.LookupStrategyPrefix,
		LookupResult:   summaryinject.LookupResultNone,
	})
	req := &model.Request{Messages: []model.Message{
		model.NewUserMessage("current request"),
	}}

	reportSummaryInjection(context.Background(), inv, req)

	level, line := logs.record(t)
	require.Equal(t, "debug", level)
	require.Contains(t, line,
		"outcome="+summaryInjectionOutcomeNotSelected)
	_, warn := logs.snapshot()
	require.Empty(t, warn,
		"sessions without a stored summary must stay quiet")
}

// injectTailoringModel simulates a built-in provider: it applies token tailoring to
// the shared request while constructing the provider payload, then returns.
type injectTailoringModel struct {
	tailor func(*model.Request)
}

func (m *injectTailoringModel) Info() model.Info {
	return model.Info{Name: "tailoring"}
}

func (m *injectTailoringModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	before := len(req.Messages)
	m.tailor(req)
	imodelrequest.RecordTokenTailoring(ctx, imodelrequest.TokenTailoringRecord{
		Provider:       "tailoring",
		MaxInputTokens: 16,
		BeforeMessages: before,
		AfterMessages:  len(req.Messages),
	})
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{Done: true}
	close(ch)
	return ch, nil
}

// TestCallLLMReportsInjectionAfterTokenTailoring pins the reporting boundary:
// the injection record describes the same model.Request after GenerateContent
// returns. Built-in providers mutate it in place, so a summary block missing
// after their token tailoring is reported as selected_block_missing rather
// than injected.
func TestCallLLMReportsInjectionAfterTokenTailoring(t *testing.T) {
	logs := captureInjectionLogs(t)
	inv := injectionInvocation(t, summaryinject.Selection{
		LookupStrategy:  summaryinject.LookupStrategyPrefix,
		LookupResult:    summaryinject.LookupResultExact,
		Selected:        true,
		StoredSummaries: 1,
		Block:           injectedSummaryBlock,
	})
	req := &model.Request{Messages: []model.Message{
		model.NewSystemMessage("instructions\n\n" + injectedSummaryBlock),
		model.NewUserMessage("older turn"),
		model.NewUserMessage("current request"),
	}}
	tailored := &injectTailoringModel{tailor: func(r *model.Request) {
		r.Messages = r.Messages[len(r.Messages)-1:]
	}}

	flow := New(nil, nil, Options{})
	_, seq, _, err := flow.callLLM(context.Background(), inv, req, tailored)
	require.NoError(t, err)
	require.NotNil(t, seq)

	level, line := logs.record(t)
	require.Equal(t, "warn", level)
	require.Contains(t, line,
		"outcome="+summaryInjectionOutcomeSelectedBlockMissing)
	require.Contains(t, line, "selected=true")
	require.Contains(t, line, "selected_block_present=false")
	require.NotContains(t, line, "injected=")
	require.Contains(t, line, "request_messages=1")
	require.NotContains(t, line, "SECRET-SUMMARY-CONTENT")
}

// TestCallLLMReportsInjectionWhenTailoringKeepsSummary is the counterpart:
// tailoring that preserves the summary head still reports a real injection.
func TestCallLLMReportsInjectionWhenTailoringKeepsSummary(t *testing.T) {
	logs := captureInjectionLogs(t)
	inv := injectionInvocation(t, summaryinject.Selection{
		LookupStrategy:  summaryinject.LookupStrategyPrefix,
		LookupResult:    summaryinject.LookupResultExact,
		Selected:        true,
		StoredSummaries: 1,
		Block:           injectedSummaryBlock,
	})
	req := &model.Request{Messages: []model.Message{
		model.NewSystemMessage("instructions\n\n" + injectedSummaryBlock),
		model.NewUserMessage("older turn"),
		model.NewUserMessage("current request"),
	}}
	tailored := &injectTailoringModel{tailor: func(r *model.Request) {
		r.Messages = append(r.Messages[:1], r.Messages[len(r.Messages)-1])
	}}

	flow := New(nil, nil, Options{})
	_, _, _, err := flow.callLLM(context.Background(), inv, req, tailored)
	require.NoError(t, err)

	level, line := logs.record(t)
	require.Equal(t, "debug", level)
	require.Contains(t, line,
		"outcome="+summaryInjectionOutcomeInjected)
	require.Contains(t, line, "selected_block_present=true")
	require.NotContains(t, line, "injected=")
	require.Contains(t, line, "request_messages=2")
}

// TestReportSummaryInjectionStaysSilentWithoutSelection verifies that requests
// which never consult session summaries report nothing at all.
func TestReportSummaryInjectionStaysSilentWithoutSelection(t *testing.T) {
	logs := captureInjectionLogs(t)
	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{ID: "session"}),
	)
	req := &model.Request{Messages: []model.Message{
		model.NewUserMessage("current request"),
	}}

	reportSummaryInjection(context.Background(), inv, req)
	reportSummaryInjection(context.Background(), nil, req)
	reportSummaryInjection(context.Background(), inv, nil)

	debug, warn := logs.snapshot()
	require.Empty(t, debug)
	require.Empty(t, warn)
}

func TestReportSummaryInjectionLogsEmptyFilterKey(t *testing.T) {
	logs := captureInjectionLogs(t)
	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{ID: "SECRET-SESSION-ID"}),
	)
	inv.AgentName = "test-agent"
	summaryinject.Record(inv, summaryinject.Selection{
		LookupStrategy:  summaryinject.LookupStrategyAll,
		LookupResult:    summaryinject.LookupResultNone,
		Selected:        false,
		StoredSummaries: 0,
	})
	req := &model.Request{Messages: []model.Message{
		model.NewUserMessage("current request"),
	}}

	reportSummaryInjection(context.Background(), inv, req)

	_, line := logs.record(t)
	require.Contains(t, line, `filter_key=""`)
	require.Contains(t, line, "filter_key_truncated=false")
}

func TestReportSummaryInjectionTruncatesLongFilterKey(t *testing.T) {
	logs := captureInjectionLogs(t)
	key := strings.Repeat("支", summarydiag.FilterKeyMaxRunes+4)
	display, truncated := summarydiag.FormatFilterKey(key)
	require.True(t, truncated)
	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{ID: "SECRET-SESSION-ID"}),
		agent.WithInvocationEventFilterKey(key),
	)
	inv.AgentName = "test-agent"
	summaryinject.Record(inv, summaryinject.Selection{
		LookupStrategy:  summaryinject.LookupStrategyExact,
		LookupResult:    summaryinject.LookupResultNone,
		Selected:        false,
		ScopedRequest:   true,
		StoredSummaries: 1,
	})
	req := &model.Request{Messages: []model.Message{
		model.NewUserMessage("current request"),
	}}

	reportSummaryInjection(context.Background(), inv, req)

	_, line := logs.record(t)
	require.Contains(t, line, fmt.Sprintf("filter_key=%q", display))
	require.Contains(t, line, "filter_key_truncated=true")
	require.NotContains(t, line, key)
	require.Equal(t, key, inv.GetEventFilterKey(),
		"truncation must not change the invocation filter key")
}
