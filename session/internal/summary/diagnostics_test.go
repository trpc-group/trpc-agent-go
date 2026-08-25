//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package summary

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
	"trpc.group/trpc-go/trpc-agent-go/internal/state/summaryview"
	"trpc.group/trpc-go/trpc-agent-go/internal/summarydiag"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	isummarycontext "trpc.group/trpc-go/trpc-agent-go/session/internal/summarycontext"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

// Sensitive values that diagnostics must never reproduce.
const (
	secretSummaryText = "SECRET-SUMMARY-CONTENT"
	secretEventText   = "SECRET-EVENT-CONTENT"
	secretErrorText   = "SECRET-DSN=user:password@tcp(db:3306)/prod"
	secretSessionID   = "SECRET-SESSION-ID"
	secretUserID      = "SECRET-USER-ID"
)

// capturedLogs records every diagnostic line emitted during a test, keyed by
// level, so tests can assert both the content and the severity of a record.
// Cascade targets report concurrently, so the recorder is synchronized.
type capturedLogs struct {
	mu    sync.Mutex
	debug []string
	info  []string
	warn  []string
}

func (c *capturedLogs) add(level *[]string, line string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	*level = append(*level, line)
}

func (c *capturedLogs) snapshot() (debug, info, warn []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.debug...),
		append([]string(nil), c.info...),
		append([]string(nil), c.warn...)
}

func (c *capturedLogs) all() string {
	debug, info, warn := c.snapshot()
	return strings.Join(append(append(debug, info...), warn...), "\n")
}

// only returns the single record emitted at any level, failing when the number
// of emitted records is not exactly one.
func (c *capturedLogs) only(t *testing.T) (level string, line string) {
	t.Helper()
	debug, info, warn := c.snapshot()
	switch {
	case len(debug) == 1 && len(info) == 0 && len(warn) == 0:
		level, line = "debug", debug[0]
	case len(info) == 1 && len(debug) == 0 && len(warn) == 0:
		level, line = "info", info[0]
	case len(warn) == 1 && len(debug) == 0 && len(info) == 0:
		level, line = "warn", warn[0]
	default:
		t.Fatalf("expected exactly one record, got %q %q %q", debug, info, warn)
	}
	requireSessionSummaryRecord(t, line)
	return level, line
}

func requireSessionSummaryRecord(t *testing.T, line string) {
	t.Helper()
	require.True(t,
		strings.HasPrefix(line, "Session summary result: schema_version=1,"),
		"schema_version=1 must follow the record name in %q", line)
}

func captureLogs(t *testing.T) *capturedLogs {
	t.Helper()
	captured := &capturedLogs{}
	oldDebug, oldInfo, oldWarn :=
		log.DebugfContext, log.InfofContext, log.WarnfContext
	log.DebugfContext = func(_ context.Context, format string, args ...any) {
		captured.add(&captured.debug, fmt.Sprintf(format, args...))
	}
	log.InfofContext = func(_ context.Context, format string, args ...any) {
		captured.add(&captured.info, fmt.Sprintf(format, args...))
	}
	log.WarnfContext = func(_ context.Context, format string, args ...any) {
		captured.add(&captured.warn, fmt.Sprintf(format, args...))
	}
	t.Cleanup(func() {
		log.DebugfContext = oldDebug
		log.InfofContext = oldInfo
		log.WarnfContext = oldWarn
	})
	return captured
}

// requireNoSensitiveText asserts the shared redaction contract for every
// diagnostic record produced by this package.
func requireNoSensitiveText(t *testing.T, logged string) {
	t.Helper()
	for _, secret := range []string{
		secretSummaryText,
		secretEventText,
		secretErrorText,
		secretSessionID,
		secretUserID,
	} {
		require.NotContains(t, logged, secret)
	}
}

// diagSummarizer is a scriptable summarizer that reproduces every summary
// outcome an operator has to distinguish.
type diagSummarizer struct {
	fired          bool
	trigger        *summary.Trigger
	callMode       string
	text           string
	err            error
	summarizeCalls int
}

func (s *diagSummarizer) ShouldSummarize(*session.Session) bool { return s.fired }

// ShouldSummarizeWithContext mirrors the built-in summarizer: it publishes the
// evaluated trigger on the report before returning the gate decision.
func (s *diagSummarizer) ShouldSummarizeWithContext(
	ctx context.Context, _ *session.Session,
) bool {
	if report, ok := summary.ReportFromContext(ctx); ok && s.trigger != nil {
		report.Trigger = *s.trigger
	}
	return s.fired
}

func (s *diagSummarizer) Summarize(
	ctx context.Context, _ *session.Session,
) (string, error) {
	s.summarizeCalls++
	if s.callMode != "" {
		if report, ok := summary.ReportFromContext(ctx); ok {
			report.Call.Mode = s.callMode
		}
	}
	return s.text, s.err
}

func (s *diagSummarizer) FilterEventsForSummary(
	events []event.Event,
) []event.Event {
	return events
}

func (s *diagSummarizer) SetPrompt(string)         {}
func (s *diagSummarizer) SetModel(model.Model)     {}
func (s *diagSummarizer) Metadata() map[string]any { return nil }

func diagSession(events ...event.Event) *session.Session {
	return &session.Session{
		ID:      secretSessionID,
		AppName: "app",
		UserID:  secretUserID,
		Events:  events,
	}
}

func diagEvent(offset time.Duration) event.Event {
	return event.Event{
		Author:    "user",
		Timestamp: time.Now().Add(offset),
		Response: &model.Response{Choices: []model.Choice{{
			Message: model.Message{
				Role:    model.RoleUser,
				Content: secretEventText,
			},
		}}},
	}
}

// diagAssistantEvent builds summarizable history that carries no user message,
// so a retained prefix built only from these events has no anchor.
func diagAssistantEvent(offset time.Duration) event.Event {
	return event.Event{
		Author:    "assistant",
		Timestamp: time.Now().Add(offset),
		Response: &model.Response{Choices: []model.Choice{{
			Message: model.Message{
				Role:    model.RoleAssistant,
				Content: secretEventText,
			},
		}}},
	}
}

// diagScopedEvent builds a user event that belongs to one branch scope.
func diagScopedEvent(offset time.Duration, filterKey string) event.Event {
	e := diagEvent(offset)
	e.FilterKey = filterKey
	e.Version = event.CurrentVersion
	return e
}

// field extracts one "key=value" field from a diagnostic record.
func field(t *testing.T, line, key string) string {
	t.Helper()
	marker := key + "="
	start := strings.Index(line, marker)
	require.GreaterOrEqualf(t, start, 0, "field %q missing from %q", key, line)
	rest := line[start+len(marker):]
	if end := strings.Index(rest, ", "); end >= 0 {
		rest = rest[:end]
	}
	return rest
}

func requireFields(t *testing.T, line string, want map[string]string) {
	t.Helper()
	for key, value := range want {
		require.Equalf(t, value, field(t, line, key),
			"field %q in %q", key, line)
	}
}

// summarizeAndPersist mirrors the shape every built-in backend uses: begin an
// attempt, summarize with the attempt context, then report what the
// persistence stage actually did.
func summarizeAndPersist(
	ctx context.Context,
	m summary.SessionSummarizer,
	sess *session.Session,
	filterKey string,
	force bool,
	persist func(context.Context, *session.Summary) error,
) error {
	ctx, att := BeginAttempt(ctx, sess, filterKey)
	defer att.Report()

	updated, err := SummarizeSession(ctx, m, sess, filterKey, force)
	att.Summarized(updated, err)
	if err != nil || !updated {
		return err
	}
	var sum *session.Summary
	if sess != nil {
		sess.SummariesMu.RLock()
		sum = sess.Summaries[filterKey]
		sess.SummariesMu.RUnlock()
	}
	if sum == nil {
		att.Persisted(PersistNoSummary)
		return nil
	}
	if persist == nil {
		att.Persisted(PersistStored)
		return nil
	}
	return att.RecordWrite(persist(ctx, sum))
}

func TestAttemptReportsSuccess(t *testing.T) {
	logs := captureLogs(t)
	sess := diagSession(diagEvent(-time.Minute))
	summarizer := &diagSummarizer{
		fired: true,
		trigger: &summary.Trigger{
			Fired:          true,
			Name:           "token_threshold",
			Metric:         "tokens",
			Value:          41_959,
			Threshold:      32_768,
			ContextWindow:  40_960,
			ThresholdRatio: 0.8,
		},
		callMode: "standalone",
		text:     secretSummaryText,
	}

	var persisted int
	require.NoError(t, summarizeAndPersist(
		context.Background(), summarizer, sess, "", false,
		func(_ context.Context, sum *session.Summary) error {
			persisted++
			require.Equal(t, secretSummaryText, sum.Summary)
			return nil
		},
	))
	require.Equal(t, 1, persisted)
	require.Equal(t, 1, summarizer.summarizeCalls)

	level, line := logs.only(t)
	require.Equal(t, "info", level)
	requireFields(t, line, map[string]string{
		"schema_version":       "1",
		"outcome":              outcomeSuccess,
		"target_kind":          targetKindFull,
		"dispatch":             dispatchRequest,
		"filter_key":           `""`,
		"filter_key_truncated": "false",
		"triggered":            "true",
		"trigger":              "token_threshold",
		"trigger_metric":       "tokens",
		"trigger_value":        "41959",
		"trigger_threshold":    "32768",
		"threshold_ratio":      "0.80",
		"context_window":       "40960",
		"model_call_status":    modelCallStatusCalled,
		"updated":              "true",
		"boundary_advanced":    "true",
		"persist_result":       string(PersistStored),
		"summary_view_present": "false",
		"binding_reason":       summaryview.BindingReasonAbsent,
	})
	requireNoSensitiveText(t, logs.all())
}

// TestAttemptReportsStaleWrite covers backends such as MySQL and ClickHouse
// that deliberately skip a write when a newer summary is already persisted.
// The record must not claim a backend-confirmed store.
func TestAttemptReportsStaleWrite(t *testing.T) {
	logs := captureLogs(t)
	sess := diagSession(diagEvent(-time.Minute))
	summarizer := &diagSummarizer{
		fired:    true,
		trigger:  &summary.Trigger{Fired: true, Name: "event_count"},
		callMode: "standalone",
		text:     secretSummaryText,
	}

	ctx, att := BeginAttempt(context.Background(), sess, "")
	updated, err := SummarizeSession(ctx, summarizer, sess, "", false)
	att.Summarized(updated, err)
	require.NoError(t, err)
	require.True(t, updated)
	att.Persisted(PersistStale)
	att.Report()

	level, line := logs.only(t)
	require.Equal(t, "debug", level,
		"a set-if-newer skip is a successful no-op, not a persistence failure")
	requireFields(t, line, map[string]string{
		"outcome":        outcomeStaleWrite,
		"updated":        "true",
		"persist_result": string(PersistStale),
	})
	requireNoSensitiveText(t, logs.all())
}

// TestAttemptReportsUnknownWrite covers a backend write that completed
// without error but whose reply could not be classified as stored or stale.
func TestAttemptReportsUnknownWrite(t *testing.T) {
	logs := captureLogs(t)
	sess := diagSession(diagEvent(-time.Minute))
	summarizer := &diagSummarizer{
		fired:    true,
		trigger:  &summary.Trigger{Fired: true, Name: "event_count"},
		callMode: "standalone",
		text:     secretSummaryText,
	}

	ctx, att := BeginAttempt(context.Background(), sess, "")
	updated, err := SummarizeSession(ctx, summarizer, sess, "", false)
	att.Summarized(updated, err)
	require.NoError(t, err)
	require.True(t, updated)
	att.Persisted(PersistUnknown)
	att.Report()

	level, line := logs.only(t)
	require.Equal(t, "debug", level,
		"an unclassified reply is diagnostic uncertainty, not a failure")
	requireFields(t, line, map[string]string{
		"outcome":        outcomeUnknownWrite,
		"updated":        "true",
		"persist_result": string(PersistUnknown),
	})
	require.NotContains(t, line, "outcome=success")
	require.NotContains(t, line, "outcome=persistence_error")
	requireNoSensitiveText(t, logs.all())
}

// TestAttemptReportsNotStored covers a backend that neither stored nor
// rejected a generated summary, for example a missing session row.
func TestAttemptReportsNotStored(t *testing.T) {
	logs := captureLogs(t)
	sess := diagSession(diagEvent(-time.Minute))
	summarizer := &diagSummarizer{
		fired:    true,
		trigger:  &summary.Trigger{Fired: true, Name: "event_count"},
		callMode: "standalone",
		text:     secretSummaryText,
	}

	ctx, att := BeginAttempt(context.Background(), sess, "")
	updated, err := SummarizeSession(ctx, summarizer, sess, "", false)
	att.Summarized(updated, err)
	require.NoError(t, err)
	att.Report()

	level, line := logs.only(t)
	require.Equal(t, "warn", level)
	requireFields(t, line, map[string]string{
		"outcome":        outcomeNotStored,
		"updated":        "true",
		"persist_result": string(PersistNotAttempted),
	})
}

func TestAttemptReportsNoSummaryPersistResult(t *testing.T) {
	logs := captureLogs(t)
	sess := diagSession(diagEvent(-time.Minute))

	ctx, att := BeginAttempt(context.Background(), sess, "")
	_ = ctx
	att.Summarized(true, nil)
	att.Persisted(PersistNoSummary)
	att.Report()

	level, line := logs.only(t)
	require.Equal(t, "warn", level)
	requireFields(t, line, map[string]string{
		"outcome":        outcomeNoUpdate,
		"persist_result": string(PersistNoSummary),
	})
}

func TestAttemptReportsPersistenceError(t *testing.T) {
	logs := captureLogs(t)
	sess := diagSession(diagEvent(-time.Minute))
	summarizer := &diagSummarizer{
		fired:    true,
		trigger:  &summary.Trigger{Fired: true, Name: "event_count"},
		callMode: "standalone",
		text:     secretSummaryText,
	}
	persistErr := errors.New(secretErrorText)

	err := summarizeAndPersist(
		context.Background(), summarizer, sess, "branch", false,
		func(context.Context, *session.Summary) error { return persistErr },
	)
	require.ErrorIs(t, err, persistErr,
		"backend persistence errors must be returned unchanged")

	level, line := logs.only(t)
	require.Equal(t, "warn", level)
	requireFields(t, line, map[string]string{
		"outcome":           outcomePersistenceError,
		"target_kind":       targetKindBranch,
		"model_call_status": modelCallStatusCalled,
		"updated":           "true",
		"persist_result":    string(PersistError),
	})
	require.Contains(t, line, `filter_key="branch"`)
	require.Contains(t, line, "filter_key_truncated=false")
	requireNoSensitiveText(t, logs.all())
}

func TestAttemptReportsModelError(t *testing.T) {
	logs := captureLogs(t)
	sess := diagSession(diagEvent(-time.Minute))
	summarizer := &diagSummarizer{
		fired:    true,
		trigger:  &summary.Trigger{Fired: true, Name: "event_count"},
		callMode: "standalone",
		err:      errors.New(secretErrorText),
	}

	var persisted int
	err := summarizeAndPersist(
		context.Background(), summarizer, sess, "", false,
		func(context.Context, *session.Summary) error {
			persisted++
			return nil
		},
	)
	require.Error(t, err)
	require.Zero(t, persisted, "a failed summary must not be persisted")

	level, line := logs.only(t)
	require.Equal(t, "warn", level)
	requireFields(t, line, map[string]string{
		"outcome":           outcomeSummaryError,
		"triggered":         "true",
		"model_call_status": modelCallStatusCalled,
		"updated":           "false",
		"boundary_advanced": "false",
		"persist_result":    string(PersistNotAttempted),
	})
	requireNoSensitiveText(t, logs.all())
}

// TestAttemptReportsSummaryErrorBeforeModelCall pins the error classification
// contract: a non-context failure in the summarization stage is a summary
// error even when the summary model was never reached. model_call_status
// separates a failed provider call from a pre-model build, a custom
// response, or an unobserved custom summarizer.
func TestAttemptReportsSummaryErrorBeforeModelCall(t *testing.T) {
	logs := captureLogs(t)
	sess := diagSession(diagEvent(-time.Minute))
	summarizer := &diagSummarizer{
		fired:   true,
		trigger: &summary.Trigger{Fired: true, Name: "event_count"},
		err:     errors.New(secretErrorText),
	}

	err := summarizeAndPersist(
		context.Background(), summarizer, sess, "", false, nil,
	)
	require.Error(t, err)

	level, line := logs.only(t)
	require.Equal(t, "warn", level)
	requireFields(t, line, map[string]string{
		"outcome":           outcomeSummaryError,
		"triggered":         "true",
		"model_call_status": modelCallStatusUnobserved,
	})
	requireNoSensitiveText(t, logs.all())
}

// TestAttemptKeepsSummaryErrorForUnboundView guards the same classification
// when a model-visible view is present but unbound: the error still wins over
// the view-derived outcome.
func TestAttemptKeepsSummaryErrorForUnboundView(t *testing.T) {
	logs := captureLogs(t)
	sess := diagSession(diagEvent(-time.Minute))
	summarizer := &diagSummarizer{
		fired:   true,
		trigger: &summary.Trigger{Fired: true, Name: "event_count"},
		err:     errors.New(secretErrorText),
	}
	ctx := summaryview.ContextWithView(context.Background(), &summaryview.View{
		SessionID:     secretSessionID,
		Items:         []summaryview.Item{{}},
		BindingReason: summaryview.BindingReasonRequestMismatch,
	})

	require.Error(t, summarizeAndPersist(ctx, summarizer, sess, "", false, nil))

	_, line := logs.only(t)
	requireFields(t, line, map[string]string{
		"outcome":            outcomeSummaryError,
		"summary_view_bound": "false",
	})
}

func TestAttemptReportsContextError(t *testing.T) {
	logs := captureLogs(t)
	sess := diagSession(diagEvent(-time.Minute))
	summarizer := &diagSummarizer{
		fired:    true,
		trigger:  &summary.Trigger{Fired: true, Name: "event_count"},
		callMode: "standalone",
		err: fmt.Errorf("%s: %w",
			secretErrorText, context.DeadlineExceeded),
	}

	err := summarizeAndPersist(
		context.Background(), summarizer, sess, "", false,
		func(context.Context, *session.Summary) error { return nil },
	)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	level, line := logs.only(t)
	require.Equal(t, "warn", level)
	requireFields(t, line, map[string]string{
		"outcome":           outcomeContextError,
		"model_call_status": modelCallStatusCalled,
	})
	requireNoSensitiveText(t, logs.all())
}

func TestAttemptReportsEmptyModelResult(t *testing.T) {
	logs := captureLogs(t)
	sess := diagSession(diagEvent(-time.Minute))
	summarizer := &diagSummarizer{
		fired:    true,
		trigger:  &summary.Trigger{Fired: true, Name: "event_count"},
		callMode: "standalone",
		text:     "",
	}

	require.NoError(t, summarizeAndPersist(
		context.Background(), summarizer, sess, "", false,
		func(context.Context, *session.Summary) error {
			t.Fatal("persist must not run without a summary")
			return nil
		},
	))

	level, line := logs.only(t)
	require.Equal(t, "warn", level,
		"a triggered attempt that produced nothing is an operator problem")
	requireFields(t, line, map[string]string{
		"outcome":           outcomeNoUpdate,
		"triggered":         "true",
		"model_call_status": modelCallStatusCalled,
		"updated":           "false",
		"boundary_advanced": "false",
	})
	requireNoSensitiveText(t, logs.all())
}

func TestAttemptReportsBelowThresholdWithGateDetails(t *testing.T) {
	logs := captureLogs(t)
	sess := diagSession(diagEvent(-time.Minute))
	summarizer := &diagSummarizer{
		fired: false,
		trigger: &summary.Trigger{
			Name:           "context_threshold",
			Metric:         "tokens",
			Value:          3_120,
			Threshold:      32_768,
			ContextWindow:  40_960,
			ThresholdRatio: 0.8,
			Checks: []summary.Check{{
				Name:      "context_threshold",
				Passed:    false,
				Metric:    "tokens",
				Value:     3_120,
				Threshold: 32_768,
			}},
		},
	}

	require.NoError(t, summarizeAndPersist(
		context.Background(), summarizer, sess, "", false, nil,
	))
	require.Zero(t, summarizer.summarizeCalls)

	level, line := logs.only(t)
	require.Equal(t, "debug", level,
		"routine below-threshold decisions must stay quiet by default")
	requireFields(t, line, map[string]string{
		"outcome":           outcomeBelowThreshold,
		"triggered":         "false",
		"trigger":           "context_threshold",
		"trigger_metric":    "tokens",
		"trigger_value":     "3120",
		"trigger_threshold": "32768",
		"threshold_ratio":   "0.80",
		"context_window":    "40960",
		"model_call_status": modelCallStatusUnobserved,
		"updated":           "false",
	})
	requireNoSensitiveText(t, logs.all())
}

func TestAttemptReportsNoContentWhenGateSawNothing(t *testing.T) {
	logs := captureLogs(t)
	sess := diagSession(diagEvent(-time.Minute))
	// A gate that publishes no check result never saw eligible content.
	summarizer := &diagSummarizer{fired: false}

	require.NoError(t, summarizeAndPersist(
		context.Background(), summarizer, sess, "", false, nil,
	))

	level, line := logs.only(t)
	require.Equal(t, "debug", level,
		"a session with nothing to summarize is not an operator problem")
	requireFields(t, line, map[string]string{
		"outcome":              outcomeNoContent,
		"triggered":            "false",
		"trigger":              "none",
		"summary_view_present": "false",
	})
	requireNoSensitiveText(t, logs.all())
}

func TestAttemptReportsUnsafeViewForUnboundView(t *testing.T) {
	logs := captureLogs(t)
	sess := diagSession(diagEvent(-time.Minute))
	summarizer := &diagSummarizer{fired: false}
	ctx := summaryview.ContextWithView(context.Background(), &summaryview.View{
		SessionID:     secretSessionID,
		Items:         []summaryview.Item{{}, {}},
		RequestTokens: 41_959,
		Bound:         false,
		BindingReason: summaryview.BindingReasonRequestMismatch,
	})

	require.NoError(t, summarizeAndPersist(
		ctx, summarizer, sess, "", false, nil,
	))

	level, line := logs.only(t)
	require.Equal(t, "warn", level)
	requireFields(t, line, map[string]string{
		"outcome":              outcomeUnsafeView,
		"summary_view_present": "true",
		"summary_view_bound":   "false",
		"binding_reason":       summaryview.BindingReasonRequestMismatch,
		"summary_view_items":   "2",
		"request_tokens":       "41959",
	})
	requireNoSensitiveText(t, logs.all())
}

func TestAttemptKeepsNoContentForBoundView(t *testing.T) {
	logs := captureLogs(t)
	sess := diagSession(diagEvent(-time.Minute))
	summarizer := &diagSummarizer{fired: false}
	ctx := summaryview.ContextWithView(context.Background(), &summaryview.View{
		SessionID:     secretSessionID,
		Items:         []summaryview.Item{{}},
		Bound:         true,
		BindingReason: summaryview.BindingReasonBound,
	})

	require.NoError(t, summarizeAndPersist(
		ctx, summarizer, sess, "", false, nil,
	))

	_, line := logs.only(t)
	requireFields(t, line, map[string]string{
		"outcome":            outcomeNoContent,
		"summary_view_bound": "true",
		"binding_reason":     summaryview.BindingReasonBound,
	})
}

func TestAttemptReportsNoDelta(t *testing.T) {
	logs := captureLogs(t)
	evt := diagEvent(-time.Minute)
	sess := diagSession(evt)
	sess.Summaries = map[string]*session.Summary{
		"": {
			Summary:   secretSummaryText,
			UpdatedAt: time.Now(),
			Boundary: session.NewSummaryBoundary(
				"", time.Now().Add(time.Minute),
			),
		},
	}
	summarizer := &diagSummarizer{fired: true, text: "new"}

	require.NoError(t, summarizeAndPersist(
		context.Background(), summarizer, sess, "", false, nil,
	))
	require.Zero(t, summarizer.summarizeCalls)

	level, line := logs.only(t)
	require.Equal(t, "debug", level)
	requireFields(t, line, map[string]string{
		"outcome":           outcomeNoDelta,
		"model_call_status": modelCallStatusUnobserved,
		"updated":           "false",
		"boundary_advanced": "false",
	})
	requireNoSensitiveText(t, logs.all())
}

// TestAttemptReportsCopiedSummary covers the cascade materialization path: an
// existing summary is persisted for a second target without a model call.
func TestAttemptReportsCopiedSummary(t *testing.T) {
	logs := captureLogs(t)
	sess := diagSession(diagEvent(-time.Minute))
	sess.Summaries = map[string]*session.Summary{
		// Zero UpdatedAt marks a copied summary awaiting persistence.
		"": {Summary: secretSummaryText},
	}
	summarizer := &diagSummarizer{fired: true, text: "unused"}

	var persisted int
	require.NoError(t, summarizeAndPersist(
		context.Background(), summarizer, sess, "", false,
		func(context.Context, *session.Summary) error {
			persisted++
			return nil
		},
	))
	require.Equal(t, 1, persisted)
	require.Zero(t, summarizer.summarizeCalls,
		"materializing a copied summary must not call the summary model")

	level, line := logs.only(t)
	require.Equal(t, "debug", level)
	requireFields(t, line, map[string]string{
		"outcome":           outcomeCopied,
		"model_call_status": modelCallStatusUnobserved,
		"updated":           "true",
		"persist_result":    string(PersistStored),
	})
	requireNoSensitiveText(t, logs.all())
}

func TestAttemptReportsModelCallStatus(t *testing.T) {
	tests := []struct {
		name     string
		callMode string
		want     string
	}{
		{
			name:     "built-in standalone call",
			callMode: callModeStandalone,
			want:     modelCallStatusCalled,
		},
		{
			name:     "built-in cache-safe fork call",
			callMode: callModeCacheSafeFork,
			want:     modelCallStatusCalled,
		},
		{
			name:     "before-model custom response",
			callMode: callModeCustomResponse,
			want:     modelCallStatusCustomResponse,
		},
		{
			name:     "custom summarizer left report unobserved",
			callMode: "",
			want:     modelCallStatusUnobserved,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureLogs(t)
			sess := diagSession(diagEvent(-time.Minute))
			summarizer := &diagSummarizer{
				fired:    true,
				trigger:  &summary.Trigger{Fired: true, Name: "event_count"},
				callMode: tt.callMode,
				text:     secretSummaryText,
			}

			require.NoError(t, summarizeAndPersist(
				context.Background(), summarizer, sess, "", false,
				func(context.Context, *session.Summary) error { return nil },
			))

			_, line := logs.only(t)
			requireFields(t, line, map[string]string{
				"outcome":           outcomeSuccess,
				"model_call_status": tt.want,
			})
			require.NotContains(t, line, "model_called=")
			requireNoSensitiveText(t, logs.all())
		})
	}
}

// TestAttemptTruncatesLongFilterKey proves diagnostic display is length-limited
// without changing the business filter key used for storage.
func TestAttemptTruncatesLongFilterKey(t *testing.T) {
	logs := captureLogs(t)
	key := strings.Repeat("a", summarydiag.FilterKeyMaxRunes+8)
	display, truncated := summarydiag.FormatFilterKey(key)
	require.True(t, truncated)
	sess := diagSession(diagEvent(-time.Minute))

	_, att := BeginAttempt(context.Background(), sess, key)
	att.Report()

	_, line := logs.only(t)
	require.Contains(t, line, fmt.Sprintf("filter_key=%q", display))
	require.Contains(t, line, "filter_key_truncated=true")
	require.NotContains(t, line, key)
	require.Equal(t, key, att.filterKey,
		"truncation must not change the attempt's business filter key")
	requireNoSensitiveText(t, logs.all())
}

// TestAttemptReportsUnknownSelectionForCustomSummarizer documents that a
// summarizer that does not publish the built-in event selection reports a
// stable custom reason with unknown counts instead of guessing.
func TestAttemptReportsUnknownSelectionForCustomSummarizer(t *testing.T) {
	logs := captureLogs(t)
	sess := diagSession(diagEvent(-time.Minute))
	summarizer := &diagSummarizer{
		fired:    true,
		trigger:  &summary.Trigger{Fired: true, Name: "event_threshold"},
		callMode: "standalone",
		text:     secretSummaryText,
	}

	require.NoError(t, summarizeAndPersist(
		context.Background(), summarizer, sess, "", false,
		func(context.Context, *session.Summary) error { return nil },
	))

	_, line := logs.only(t)
	requireFields(t, line, map[string]string{
		"input_source":          "custom",
		"selection_reason":      "custom",
		"eligible_events":       "-1",
		"skip_recent_requested": "-1",
		"skip_recent_applied":   "-1",
		"selected_events":       "-1",
	})
}

// TestAttemptReportsNoSelectionWhenSummarizerNeverRuns proves that a gate which
// stops before the summary call is reported as an unobserved selection rather
// than as a custom summarizer.
func TestAttemptReportsNoSelectionWhenSummarizerNeverRuns(t *testing.T) {
	logs := captureLogs(t)
	sess := diagSession(diagEvent(-time.Minute))
	summarizer := &diagSummarizer{
		trigger: &summary.Trigger{
			Name:   "event_threshold",
			Metric: "events",
			Checks: []summary.Check{{Name: "event_threshold"}},
		},
	}

	require.NoError(t, summarizeAndPersist(
		context.Background(), summarizer, sess, "", false,
		func(context.Context, *session.Summary) error { return nil },
	))

	_, line := logs.only(t)
	requireFields(t, line, map[string]string{
		"outcome":          "below_threshold",
		"input_source":     "none",
		"selection_reason": "none",
		"selected_events":  "-1",
	})
}

// TestNormalizedTriggerValuesAreBounded proves that arbitrary trigger strings,
// which any caller can supply through the exported summary.Trigger fields,
// never reach the log and never inflate the cardinality of these fields.
func TestNormalizedTriggerValuesAreBounded(t *testing.T) {
	for _, tc := range []struct {
		name, metric string
		wantName     string
		wantMetric   string
	}{
		{"", "", "none", "none"},
		{"event_threshold", "events", "event_threshold", "events"},
		{"token_threshold", "tokens", "token_threshold", "tokens"},
		{"time_threshold", "duration", "time_threshold", "duration"},
		{"context_threshold", "tokens", "context_threshold", "tokens"},
		{"force", "custom", "force", "custom"},
		{"manual", "custom", "manual", "custom"},
		{"always", "custom", "always", "custom"},
		{"custom", "custom", "custom", "custom"},
		{"tenant-42/user-secret", "user@example.com", "custom", "custom"},
		{"event_count", "Events", "custom", "custom"},
	} {
		require.Equal(t, tc.wantName, normalizedTriggerName(tc.name),
			"trigger name %q", tc.name)
		require.Equal(t, tc.wantMetric, normalizedTriggerMetric(tc.metric),
			"trigger metric %q", tc.metric)
	}
}

// TestAttemptNeverLogsCustomTriggerStrings proves the normalization is applied
// on the real reporting path, not only in the helper.
func TestAttemptNeverLogsCustomTriggerStrings(t *testing.T) {
	const secretTrigger = "tenant-42-escalation"
	logs := captureLogs(t)
	sess := diagSession(diagEvent(-time.Minute))
	summarizer := &diagSummarizer{
		fired: true,
		trigger: &summary.Trigger{
			Fired:  true,
			Name:   secretTrigger,
			Metric: secretTrigger,
			Value:  7,
		},
		callMode: "standalone",
		text:     secretSummaryText,
	}

	require.NoError(t, summarizeAndPersist(
		context.Background(), summarizer, sess, "", false,
		func(context.Context, *session.Summary) error { return nil },
	))

	_, line := logs.only(t)
	require.NotContains(t, line, secretTrigger)
	requireFields(t, line, map[string]string{
		"trigger":        "custom",
		"trigger_metric": "custom",
		"trigger_value":  "7",
	})
}

// TestAttemptPreservesCallerReport verifies the diagnostics reuse the caller's
// report instead of replacing it, so summary hooks keep observing the trigger
// and usage they observed before.
func TestAttemptPreservesCallerReport(t *testing.T) {
	captureLogs(t)
	sess := diagSession(diagEvent(-time.Minute))
	summarizer := &diagSummarizer{
		fired:    true,
		trigger:  &summary.Trigger{Fired: true, Name: "token_threshold"},
		callMode: "standalone",
		text:     secretSummaryText,
	}
	report := &summary.Report{}
	ctx := summary.ContextWithReport(context.Background(), report)

	require.NoError(t, summarizeAndPersist(
		ctx, summarizer, sess, "", false,
		func(context.Context, *session.Summary) error { return nil },
	))
	require.Equal(t, "token_threshold", report.Trigger.Name)
	require.Equal(t, "standalone", report.Call.Mode)
}

func TestAttemptTolerantOfNilInputs(t *testing.T) {
	logs := captureLogs(t)
	require.NoError(t, summarizeAndPersist(
		context.Background(), nil, nil, "", false, nil,
	))
	level, line := logs.only(t)
	require.Equal(t, "warn", level)
	requireFields(t, line, map[string]string{"outcome": outcomeNoUpdate})

	var missing *Attempt
	missing.Summarized(true, nil)
	missing.Persisted(PersistStored)
	require.NoError(t, missing.RecordWrite(nil))
	missing.Report()
}

// TestAttemptReportsUnsafeViewWithRealSummarizer is the end-to-end proof for
// the incident signature: the built-in summarizer refuses to summarize content
// it cannot prove the model saw, and the record names the stage that broke the
// binding instead of reporting a bare "nothing to summarize".
func TestAttemptReportsUnsafeViewWithRealSummarizer(t *testing.T) {
	logs := captureLogs(t)
	sess := diagSession(diagEvent(-time.Minute))
	summarizer := summary.NewSummarizer(
		&reportModel{},
		summary.WithChecksAny(summary.CheckEventThreshold(1)),
	)

	// The request that the model answered could not be matched, so the view
	// exists but carries no proof of visibility.
	ctx := summaryview.ContextWithView(context.Background(), &summaryview.View{
		SessionID:     secretSessionID,
		Items:         []summaryview.Item{{}, {}, {}},
		RequestTokens: 8_192,
		BindingReason: summaryview.BindingReasonTransformMismatch,
	})

	require.NoError(t, summarizeAndPersist(
		ctx, summarizer, sess, "", false,
		func(context.Context, *session.Summary) error {
			t.Fatal("unbound history must never be persisted as a summary")
			return nil
		},
	))

	level, line := logs.only(t)
	require.Equal(t, "warn", level)
	requireFields(t, line, map[string]string{
		"outcome":              outcomeUnsafeView,
		"triggered":            "false",
		"model_call_status":    modelCallStatusUnobserved,
		"summary_view_present": "true",
		"summary_view_bound":   "false",
		"binding_reason":       summaryview.BindingReasonTransformMismatch,
		"request_tokens":       "8192",
		"input_source":         isummarycontext.SourceUnboundView,
		"selection_reason":     isummarycontext.ReasonUnboundView,
		"eligible_events":      "3",
		"selected_events":      "0",
	})
	requireNoSensitiveText(t, logs.all())
}

// TestAttemptFallsBackWithoutView guards the preserved fallback: a session
// with no model-visible view still summarizes from stored events.
func TestAttemptFallsBackWithoutView(t *testing.T) {
	logs := captureLogs(t)
	sess := diagSession(
		diagEvent(-2*time.Minute),
		diagEvent(-time.Minute),
	)
	summarizer := summary.NewSummarizer(
		&reportModel{},
		summary.WithChecksAny(summary.CheckEventThreshold(1)),
	)

	var persisted int
	require.NoError(t, summarizeAndPersist(
		context.Background(), summarizer, sess, "", false,
		func(context.Context, *session.Summary) error {
			persisted++
			return nil
		},
	))
	require.Equal(t, 1, persisted)

	level, line := logs.only(t)
	require.Equal(t, "info", level)
	requireFields(t, line, map[string]string{
		"outcome":               outcomeSuccess,
		"triggered":             "true",
		"model_call_status":     modelCallStatusCalled,
		"summary_view_present":  "false",
		"binding_reason":        summaryview.BindingReasonAbsent,
		"input_source":          isummarycontext.SourceSessionEvents,
		"selection_reason":      isummarycontext.ReasonSelected,
		"eligible_events":       "2",
		"skip_recent_requested": "0",
		"skip_recent_applied":   "0",
		"selected_events":       "2",
	})
}

// TestAttemptReportsSkipRecentSelectingSome shows the counts an operator needs
// to see that skip-recent narrowed the summarized history.
func TestAttemptReportsSkipRecentSelectingSome(t *testing.T) {
	logs := captureLogs(t)
	sess := diagSession(
		diagEvent(-3*time.Minute),
		diagEvent(-2*time.Minute),
		diagEvent(-time.Minute),
	)
	summarizer := summary.NewSummarizer(
		&reportModel{},
		summary.WithChecksAny(summary.CheckEventThreshold(1)),
		summary.WithSkipRecent(func([]event.Event) int { return 1 }),
	)

	require.NoError(t, summarizeAndPersist(
		context.Background(), summarizer, sess, "", false,
		func(context.Context, *session.Summary) error { return nil },
	))

	_, line := logs.only(t)
	requireFields(t, line, map[string]string{
		"outcome":               outcomeSuccess,
		"input_source":          isummarycontext.SourceSessionEvents,
		"selection_reason":      isummarycontext.ReasonSelected,
		"eligible_events":       "3",
		"skip_recent_requested": "1",
		"skip_recent_applied":   "1",
		"selected_events":       "2",
	})
}

// TestAttemptReportsSkipRecentSelectingZero covers the case where skip-recent
// leaves nothing to summarize: the record must show that candidates existed
// but none were selected.
func TestAttemptReportsSkipRecentSelectingZero(t *testing.T) {
	logs := captureLogs(t)
	sess := diagSession(
		diagEvent(-2*time.Minute),
		diagEvent(-time.Minute),
	)
	summarizer := summary.NewSummarizer(
		&reportModel{},
		summary.WithChecksAny(summary.CheckEventThreshold(1)),
		summary.WithSkipRecent(func(events []event.Event) int {
			return len(events)
		}),
	)

	require.NoError(t, summarizeAndPersist(
		context.Background(), summarizer, sess, "", false,
		func(context.Context, *session.Summary) error {
			t.Fatal("nothing was selected, so nothing may be persisted")
			return nil
		},
	))

	level, line := logs.only(t)
	require.Equal(t, "debug", level)
	requireFields(t, line, map[string]string{
		"outcome":               outcomeNoContent,
		"input_source":          isummarycontext.SourceSessionEvents,
		"selection_reason":      isummarycontext.ReasonSkipRecentAll,
		"eligible_events":       "2",
		"skip_recent_requested": "2",
		"skip_recent_applied":   "2",
		"selected_events":       "0",
	})
}

// TestAttemptReportsUnsafePrefix covers the skip-recent outcome that is easiest
// to mistake for "nothing to summarize": events remained after skip-recent, but
// the retained prefix had no user message and no previous-summary anchor, so it
// was dropped as unsafe. skip_recent_applied stays at the clamped callback
// count; the extra drop is named by selection_reason, not attributed to
// SkipRecent.
func TestAttemptReportsUnsafePrefix(t *testing.T) {
	logs := captureLogs(t)
	sess := diagSession(
		diagAssistantEvent(-3*time.Minute),
		diagAssistantEvent(-2*time.Minute),
		diagEvent(-time.Minute),
	)
	summarizer := summary.NewSummarizer(
		&reportModel{},
		summary.WithChecksAny(summary.CheckEventThreshold(1)),
		summary.WithSkipRecent(func([]event.Event) int { return 1 }),
	)

	require.NoError(t, summarizeAndPersist(
		context.Background(), summarizer, sess, "", false,
		func(context.Context, *session.Summary) error {
			t.Fatal("an unsafe prefix must never be summarized")
			return nil
		},
	))

	level, line := logs.only(t)
	require.Equal(t, "debug", level)
	requireFields(t, line, map[string]string{
		"outcome":               outcomeNoContent,
		"input_source":          isummarycontext.SourceSessionEvents,
		"selection_reason":      isummarycontext.ReasonUnsafePrefix,
		"eligible_events":       "3",
		"skip_recent_requested": "1",
		"skip_recent_applied":   "1",
		"selected_events":       "0",
	})
	requireNoSensitiveText(t, logs.all())
}

// TestAttemptReportsSessionFilterEmpty covers history that survives skip-recent
// and is then removed entirely by the summary's branch scoping, which an
// operator cannot otherwise tell apart from an empty session. Events on an
// ancestor branch reach the delta but fall outside the narrower summary scope.
func TestAttemptReportsSessionFilterEmpty(t *testing.T) {
	logs := captureLogs(t)
	sess := diagSession(
		diagScopedEvent(-2*time.Minute, "app"),
		diagScopedEvent(-time.Minute, "app"),
	)
	summarizer := summary.NewSummarizer(
		&reportModel{},
		summary.WithChecksAny(summary.CheckEventThreshold(1)),
	)

	require.NoError(t, summarizeAndPersist(
		context.Background(), summarizer, sess, "app/wanted", false,
		func(context.Context, *session.Summary) error {
			t.Fatal("out-of-scope history must never be summarized")
			return nil
		},
	))

	level, line := logs.only(t)
	require.Equal(t, "debug", level)
	requireFields(t, line, map[string]string{
		"outcome":               outcomeNoContent,
		"input_source":          isummarycontext.SourceSessionEvents,
		"selection_reason":      isummarycontext.ReasonSessionFilterEmpty,
		"eligible_events":       "2",
		"skip_recent_requested": "0",
		"skip_recent_applied":   "0",
		"selected_events":       "0",
	})
	requireNoSensitiveText(t, logs.all())
}

// TestAttemptReportsBoundaryUnmapped covers a bound view whose items have no
// stored-event boundary. The summary is dropped rather than advance the
// persistence boundary past content that cannot be located again.
func TestAttemptReportsBoundaryUnmapped(t *testing.T) {
	logs := captureLogs(t)
	sess := diagSession(diagEvent(-time.Minute))
	summarizer := summary.NewSummarizer(
		&reportModel{},
		summary.WithChecksAny(summary.CheckEventThreshold(1)),
	)

	// Items are bound to the request but carry zero boundaries, so no stored
	// event corresponds to the summarized content.
	ctx := summaryview.ContextWithView(context.Background(), &summaryview.View{
		SessionID: secretSessionID,
		Bound:     true,
		Items: []summaryview.Item{
			{EffectiveEvent: diagEvent(-2 * time.Minute)},
			{EffectiveEvent: diagEvent(-time.Minute)},
		},
	})

	require.NoError(t, summarizeAndPersist(
		ctx, summarizer, sess, "", false,
		func(context.Context, *session.Summary) error {
			t.Fatal("unmapped content must never advance persistence")
			return nil
		},
	))

	level, line := logs.only(t)
	require.Equal(t, "debug", level)
	requireFields(t, line, map[string]string{
		"outcome":               outcomeNoContent,
		"input_source":          isummarycontext.SourceModelVisible,
		"selection_reason":      isummarycontext.ReasonBoundaryUnmapped,
		"eligible_events":       "2",
		"skip_recent_requested": "0",
		"skip_recent_applied":   "0",
		"selected_events":       "0",
	})
	requireNoSensitiveText(t, logs.all())
}

// TestAttemptReportsNoCandidates covers a bound view that carries no history at
// all, which is distinct from history that was filtered away.
func TestAttemptReportsNoCandidates(t *testing.T) {
	logs := captureLogs(t)
	sess := diagSession(diagEvent(-time.Minute))
	summarizer := summary.NewSummarizer(
		&reportModel{},
		summary.WithChecksAny(summary.CheckEventThreshold(1)),
	)

	ctx := summaryview.ContextWithView(context.Background(), &summaryview.View{
		SessionID: secretSessionID,
		Bound:     true,
	})

	require.NoError(t, summarizeAndPersist(
		ctx, summarizer, sess, "", false,
		func(context.Context, *session.Summary) error {
			t.Fatal("an empty view has nothing to summarize")
			return nil
		},
	))

	_, line := logs.only(t)
	requireFields(t, line, map[string]string{
		"outcome":          outcomeNoContent,
		"input_source":     isummarycontext.SourceModelVisible,
		"selection_reason": isummarycontext.ReasonNoCandidates,
		"eligible_events":  "0",
		"selected_events":  "0",
	})
}

// TestSelectionReasonsAreBounded pins the closed set of selection reasons that
// operators alert on.
func TestSelectionReasonsAreBounded(t *testing.T) {
	require.ElementsMatch(t, []string{
		"none",
		"custom",
		"selected",
		"no_candidates",
		"skip_recent_all",
		"unsafe_prefix",
		"session_filter_empty",
		"unbound_view",
		"boundary_unmapped",
	}, []string{
		isummarycontext.ReasonNone,
		isummarycontext.ReasonCustom,
		isummarycontext.ReasonSelected,
		isummarycontext.ReasonNoCandidates,
		isummarycontext.ReasonSkipRecentAll,
		isummarycontext.ReasonUnsafePrefix,
		isummarycontext.ReasonSessionFilterEmpty,
		isummarycontext.ReasonUnboundView,
		isummarycontext.ReasonBoundaryUnmapped,
	})
}

func TestSummaryTargetKindIsBounded(t *testing.T) {
	require.Equal(t, targetKindFull,
		summaryTargetKind(session.SummaryFilterKeyAllContents))
	require.Equal(t, targetKindBranch, summaryTargetKind("app/branch-42"))
}
