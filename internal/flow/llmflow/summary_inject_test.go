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
func (l *injectionLogs) injectionLines() []string {
	debug, warn := l.snapshot()
	var lines []string
	for _, candidate := range append(append([]string{}, warn...), debug...) {
		if strings.Contains(candidate, "injection result") {
			lines = append(lines, candidate)
		}
	}
	return lines
}

func drainResponseSeq(t *testing.T, seq model.Seq[*model.Response]) {
	t.Helper()
	require.NotNil(t, seq)
	seq(func(*model.Response) bool { return true })
}

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
		"outcome="+summaryInjectionOutcomeBlockTextPresent)
	require.Contains(t, line, `agent="test-agent"`)
	require.Contains(t, line, "agent_truncated=false")
	require.Contains(t, line, `filter_key="test-agent"`)
	require.Contains(t, line, "filter_key_truncated=false")
	require.Contains(t, line, "selected=true")
	require.Contains(t, line, "block_text_present=true")
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
// the response sequence has been observed. That does not claim the provider payload.
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
		"outcome="+summaryInjectionOutcomeBlockTextMissing)
	require.Contains(t, line, "selected=true")
	require.Contains(t, line, "block_text_present=false")
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
// after their token tailoring is reported as block_text_missing rather
// than block_text_present.
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
	drainResponseSeq(t, seq)

	level, line := logs.record(t)
	require.Equal(t, "warn", level)
	require.Contains(t, line,
		"outcome="+summaryInjectionOutcomeBlockTextMissing)
	require.Contains(t, line, "selected=true")
	require.Contains(t, line, "block_text_present=false")
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
	_, seq, _, err := flow.callLLM(context.Background(), inv, req, tailored)
	require.NoError(t, err)
	drainResponseSeq(t, seq)

	level, line := logs.record(t)
	require.Equal(t, "debug", level)
	require.Contains(t, line,
		"outcome="+summaryInjectionOutcomeBlockTextPresent)
	require.Contains(t, line, "block_text_present=true")
	require.NotContains(t, line, "injected=")
	require.Contains(t, line, "request_messages=2")
}

// injectLazyTailoringIterModel simulates a lazy IterModel: GenerateContentIter
// returns a seq immediately, and token tailoring mutates the shared request
// only while that seq runs.
type injectLazyTailoringIterModel struct {
	tailor func(*model.Request)
	err    error
}

func (m *injectLazyTailoringIterModel) Info() model.Info {
	return model.Info{Name: "lazy-tailoring"}
}

func (m *injectLazyTailoringIterModel) GenerateContent(
	context.Context,
	*model.Request,
) (<-chan *model.Response, error) {
	return nil, fmt.Errorf("unexpected GenerateContent call")
}

func (m *injectLazyTailoringIterModel) GenerateContentIter(
	ctx context.Context,
	req *model.Request,
) (model.Seq[*model.Response], error) {
	if m.err != nil {
		return nil, m.err
	}
	return func(yield func(*model.Response) bool) {
		before := len(req.Messages)
		if m.tailor != nil {
			m.tailor(req)
		}
		imodelrequest.RecordTokenTailoring(ctx, imodelrequest.TokenTailoringRecord{
			Provider:       "lazy-tailoring",
			MaxInputTokens: 16,
			BeforeMessages: before,
			AfterMessages:  len(req.Messages),
		})
		yield(&model.Response{Done: true})
	}, nil
}

func selectedSummaryInjectionInvocation(t *testing.T) (*agent.Invocation, *model.Request) {
	t.Helper()
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
	return inv, req
}

func dropAllButLastMessage(r *model.Request) {
	r.Messages = r.Messages[len(r.Messages)-1:]
}

// TestCallLLMReportsInjectionAfterLazyIterTailoring is the lazy-IterModel
// counterpart of TestCallLLMReportsInjectionAfterTokenTailoring: the seq is
// returned before tailoring runs, so reporting immediately would falsely
// record block_text_present. The final record must be block_text_missing, once.
func TestCallLLMReportsInjectionAfterLazyIterTailoring(t *testing.T) {
	logs := captureInjectionLogs(t)
	inv, req := selectedSummaryInjectionInvocation(t)
	lazy := &injectLazyTailoringIterModel{tailor: dropAllButLastMessage}

	flow := New(nil, nil, Options{})
	_, seq, _, err := flow.callLLM(context.Background(), inv, req, lazy)
	require.NoError(t, err)
	require.NotNil(t, seq)
	require.Empty(t, logs.injectionLines(),
		"lazy IterModel must not report injected before the seq runs")

	drainResponseSeq(t, seq)

	require.Len(t, logs.injectionLines(), 1)
	level, line := logs.record(t)
	require.Equal(t, "warn", level)
	require.Contains(t, line,
		"outcome="+summaryInjectionOutcomeBlockTextMissing)
	require.Contains(t, line, "selected=true")
	require.Contains(t, line, "block_text_present=false")
	require.NotContains(t, line, "injected=")
	require.Contains(t, line, "request_messages=1")
	require.NotContains(t, line, "SECRET-SUMMARY-CONTENT")
}

// TestCallLLMReportsInjectionOnceWhenLazyIterStopsEarly proves the seq
// finalizer still reports once when the consumer stops before draining.
func TestCallLLMReportsInjectionOnceWhenLazyIterStopsEarly(t *testing.T) {
	logs := captureInjectionLogs(t)
	inv, req := selectedSummaryInjectionInvocation(t)
	lazy := &injectLazyTailoringIterModel{tailor: dropAllButLastMessage}

	flow := New(nil, nil, Options{})
	_, seq, _, err := flow.callLLM(context.Background(), inv, req, lazy)
	require.NoError(t, err)
	require.Empty(t, logs.injectionLines())

	seq(func(*model.Response) bool { return false })

	require.Len(t, logs.injectionLines(), 1)
	level, line := logs.record(t)
	require.Equal(t, "warn", level)
	require.Contains(t, line,
		"outcome="+summaryInjectionOutcomeBlockTextMissing)
	require.Contains(t, line, "block_text_present=false")
}

// TestCallLLMReportsInjectionOnceWhenGenerateContentSeqFails covers the
// error path: generateContentSeq returns an error, the injection record is
// emitted immediately, and it is not repeated.
func TestCallLLMReportsInjectionOnceWhenGenerateContentSeqFails(t *testing.T) {
	logs := captureInjectionLogs(t)
	inv, req := selectedSummaryInjectionInvocation(t)
	lazy := &injectLazyTailoringIterModel{
		err: fmt.Errorf("generate content failed"),
	}

	flow := New(nil, nil, Options{})
	_, seq, _, err := flow.callLLM(context.Background(), inv, req, lazy)
	require.Error(t, err)
	require.Nil(t, seq)

	require.Len(t, logs.injectionLines(), 1)
	level, line := logs.record(t)
	require.Equal(t, "debug", level)
	require.Contains(t, line,
		"outcome="+summaryInjectionOutcomeBlockTextPresent)
	require.Contains(t, line, "block_text_present=true")
	require.Contains(t, line, "request_messages=3")
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

func TestReportSummaryInjectionQuotesUnsafeAgentName(t *testing.T) {
	logs := captureInjectionLogs(t)
	inv := injectionInvocation(t, summaryinject.Selection{
		LookupStrategy: summaryinject.LookupStrategyAll,
		LookupResult:   summaryinject.LookupResultNone,
	})
	inv.AgentName = "agent\nextra,field=1"
	display, truncated := summarydiag.FormatAgentName(inv.AgentName)
	require.False(t, truncated)
	req := &model.Request{Messages: []model.Message{
		model.NewUserMessage("current request"),
	}}

	reportSummaryInjection(context.Background(), inv, req)

	_, line := logs.record(t)
	require.Contains(t, line, fmt.Sprintf("agent=%q", display))
	require.Contains(t, line, "agent_truncated=false")
	require.NotContains(t, line, "\n")
}

func TestReportSummaryInjectionTreatsUnrelatedCopyAsBlockTextPresent(t *testing.T) {
	logs := captureInjectionLogs(t)
	inv := injectionInvocation(t, summaryinject.Selection{
		LookupStrategy:  summaryinject.LookupStrategyPrefix,
		LookupResult:    summaryinject.LookupResultExact,
		Selected:        true,
		StoredSummaries: 1,
		Block:           injectedSummaryBlock,
	})
	req := &model.Request{Messages: []model.Message{
		model.NewSystemMessage("instructions without the original block"),
		model.NewUserMessage("unrelated copy:\n" + injectedSummaryBlock),
	}}

	reportSummaryInjection(context.Background(), inv, req)

	level, line := logs.record(t)
	require.Equal(t, "debug", level)
	require.Contains(t, line,
		"outcome="+summaryInjectionOutcomeBlockTextPresent)
	require.Contains(t, line, "block_text_present=true")
	require.NotContains(t, line, "SECRET-SUMMARY-CONTENT")
}

func TestCallLLMReportsInjectionOnceOnBeforeModelError(t *testing.T) {
	logs := captureInjectionLogs(t)
	inv, req := selectedSummaryInjectionInvocation(t)
	callbacks := model.NewCallbacks()
	callbacks.RegisterBeforeModel(func(
		context.Context, *model.BeforeModelArgs,
	) (*model.BeforeModelResult, error) {
		return nil, fmt.Errorf("before model failed")
	})
	flow := New(nil, nil, Options{ModelCallbacks: callbacks})

	_, seq, modelCalled, err := flow.callLLM(
		context.Background(), inv, req, &injectTailoringModel{},
	)
	require.Error(t, err)
	require.Nil(t, seq)
	require.False(t, modelCalled)
	require.Len(t, logs.injectionLines(), 1)
	_, line := logs.record(t)
	require.Contains(t, line, "block_text_present=true")
}

func TestCallLLMReportsInjectionOnceOnBeforeModelCustomResponse(t *testing.T) {
	logs := captureInjectionLogs(t)
	inv, req := selectedSummaryInjectionInvocation(t)
	callbacks := model.NewCallbacks()
	callbacks.RegisterBeforeModel(func(
		context.Context, *model.BeforeModelArgs,
	) (*model.BeforeModelResult, error) {
		return &model.BeforeModelResult{
			CustomResponse: &model.Response{Done: true},
		}, nil
	})
	flow := New(nil, nil, Options{ModelCallbacks: callbacks})

	_, seq, modelCalled, err := flow.callLLM(
		context.Background(), inv, req, &injectTailoringModel{},
	)
	require.NoError(t, err)
	require.False(t, modelCalled)
	require.Empty(t, logs.injectionLines())

	seq(func(*model.Response) bool { return false })

	require.Len(t, logs.injectionLines(), 1)
	level, line := logs.record(t)
	require.Equal(t, "debug", level)
	require.Contains(t, line,
		"outcome="+summaryInjectionOutcomeBlockTextPresent)
}
