//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summary

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type reportModel struct {
	request   *model.Request
	usage     *model.Usage
	responses []*model.Response
}

func (m *reportModel) Info() model.Info {
	return model.Info{Name: "report"}
}

func (m *reportModel) GenerateContent(
	_ context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	m.request = req
	ch := make(chan *model.Response, 1)
	if len(m.responses) > 0 {
		go func() {
			defer close(ch)
			for _, response := range m.responses {
				ch <- response
			}
		}()
		return ch, nil
	}
	ch <- &model.Response{
		Done:  true,
		Usage: m.usage,
		Choices: []model.Choice{{
			Message: model.Message{Role: model.RoleAssistant, Content: "summary"},
		}},
	}
	close(ch)
	return ch, nil
}

func TestReportContextAndClone(t *testing.T) {
	report := &Report{
		Trigger: Trigger{
			Name:   checkNameTokenThreshold,
			Checks: []Check{{Name: checkNameTokenThreshold}},
		},
	}

	ctx := ContextWithReport(nil, report)
	got, ok := ReportFromContext(ctx)
	require.True(t, ok)
	require.Same(t, report, got)

	got, ok = ReportFromContext(ContextWithReport(context.Background(), nil))
	require.False(t, ok)
	require.Nil(t, got)

	cloned := report.Clone()
	require.Equal(t, *report, cloned)
	cloned.Trigger.Checks[0].Name = checkNameContextThreshold
	require.Equal(t, checkNameTokenThreshold, report.Trigger.Checks[0].Name)
}

func newReportSession() *session.Session {
	return &session.Session{
		ID:      "report-session",
		AppName: "app",
		Events: []event.Event{{
			Author:    "user",
			Timestamp: time.Now(),
			FilterKey: "app",
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{Content: "hello"},
			}}},
		}},
	}
}

func TestSessionSummarizer_ReportTriggerFromContextThreshold(t *testing.T) {
	defer SetTokenCounter(nil)
	SetTokenCounter(testFixedTokenCounter{tokens: 5000})

	s := NewSummarizer(
		&reportModel{},
		WithContextThreshold(
			WithContextThresholdFallbackWindow(10000),
			WithContextThresholdRatio(0.4),
		),
	)

	report := &Report{}
	ctx := ContextWithReport(context.Background(), report)
	contextual := s.(ContextAwareSummarizer)
	require.True(t, contextual.ShouldSummarizeWithContext(ctx, newReportSession()))
	require.True(t, report.Trigger.Fired)
	require.Equal(t, checkNameContextThreshold, report.Trigger.Name)
	require.Equal(t, metricTokens, report.Trigger.Metric)
	require.Equal(t, 5000, report.Trigger.Value)
	require.Equal(t, 4000, report.Trigger.Threshold)
	require.Equal(t, 10000, report.Trigger.ContextWindow)
	require.Equal(t, 0.4, report.Trigger.ThresholdRatio)
	require.Len(t, report.Trigger.Checks, 1)
}

func TestSessionSummarizer_ReportHookStandaloneCall(t *testing.T) {
	defer SetTokenCounter(nil)
	SetTokenCounter(testFixedTokenCounter{tokens: 7})

	var got Report
	m := &reportModel{usage: &model.Usage{
		PromptTokens: 123,
		PromptTokensDetails: model.PromptTokensDetails{
			CachedTokens: 45,
		},
	}}
	s := NewSummarizer(
		m,
		WithTokenThreshold(5),
		WithReportHook(func(_ context.Context, report Report) {
			got = report
		}),
	)

	report := &Report{}
	ctx := ContextWithReport(context.Background(), report)
	contextual := s.(ContextAwareSummarizer)
	require.True(t, contextual.ShouldSummarizeWithContext(ctx, newReportSession()))

	text, err := s.Summarize(ctx, newReportSession())
	require.NoError(t, err)
	require.Equal(t, "summary", text)
	require.Equal(t, checkNameTokenThreshold, got.Trigger.Name)
	require.Equal(t, 7, got.Trigger.Value)
	require.Equal(t, 5, got.Trigger.Threshold)
	require.Equal(t, callModeStandalone, got.Call.Mode)
	require.Equal(t, 7, got.Call.EstimatedPromptTokens)
	require.Equal(t, 123, got.Call.PromptTokens)
	require.Equal(t, 45, got.Call.CachedTokens)
	require.NoError(t, got.Error)
}

func TestSessionSummarizer_ReportHookCacheSafeForkCall(t *testing.T) {
	defer SetTokenCounter(nil)
	SetTokenCounter(testFixedTokenCounter{tokens: 3})

	var got Report
	parent := &model.Request{Messages: []model.Message{
		model.NewSystemMessage("stable system"),
		model.NewUserMessage("conversation"),
	}}
	m := &reportModel{}
	s := NewSummarizer(
		m,
		WithCacheSafeForking(true),
		WithReportHook(func(_ context.Context, report Report) {
			got = report
		}),
	)

	ctx := ContextWithCacheSafeForkRequest(context.Background(), parent)
	text, err := s.Summarize(ctx, newReportSession())
	require.NoError(t, err)
	require.Equal(t, "summary", text)
	require.NotNil(t, m.request)
	require.Len(t, m.request.Messages, 3)
	require.Equal(t, callModeCacheSafeFork, got.Call.Mode)
	require.Equal(t, 9, got.Call.EstimatedPromptTokens)
	require.Equal(t, "manual", got.Trigger.Name)
}

func TestSessionSummarizer_ReportHookSeedsManualTriggerForPreseededReport(t *testing.T) {
	var got Report
	report := &Report{}
	s := NewSummarizer(
		&reportModel{},
		WithReportHook(func(_ context.Context, report Report) {
			got = report
		}),
	)

	text, err := s.Summarize(ContextWithReport(context.Background(), report), newReportSession())
	require.NoError(t, err)
	require.Equal(t, "summary", text)
	require.True(t, report.Trigger.Fired)
	require.Equal(t, "manual", report.Trigger.Name)
	require.True(t, got.Trigger.Fired)
	require.Equal(t, "manual", got.Trigger.Name)
}

type rangeErrorTokenCounter struct {
	tokens int
}

func (c rangeErrorTokenCounter) CountTokens(_ context.Context, _ model.Message) (int, error) {
	return c.tokens, nil
}

func (c rangeErrorTokenCounter) CountTokensRange(
	_ context.Context,
	_ []model.Message,
	_,
	_ int,
) (int, error) {
	return 0, errors.New("range count failed")
}

func TestReportAccountingHelpers(t *testing.T) {
	defer SetTokenCounter(nil)
	SetTokenCounter(rangeErrorTokenCounter{tokens: 4})

	require.Zero(t, estimateRequestPromptTokens(context.Background(), nil))
	require.Zero(t, estimateRequestPromptTokens(context.Background(), &model.Request{}))
	require.Equal(
		t,
		8,
		estimateRequestPromptTokens(
			context.Background(),
			&model.Request{Messages: []model.Message{
				model.NewSystemMessage("system"),
				model.NewUserMessage("user"),
			}},
		),
	)

	require.False(t, usageHasTokenCounts(nil))
	require.False(t, usageHasTokenCounts(&model.Usage{}))
	require.True(t, usageHasTokenCounts(&model.Usage{TotalTokens: 1}))
	require.True(t, usageHasTokenCounts(&model.Usage{
		PromptTokensDetails: model.PromptTokensDetails{CacheReadTokens: 1},
	}))
	require.True(t, usageHasTokenCounts(&model.Usage{
		PromptTokensDetails: model.PromptTokensDetails{CacheCreationTokens: 1},
	}))
}

type reportContextTestKey struct{}

func TestInheritReportContext(t *testing.T) {
	report := &Report{Trigger: Trigger{Name: checkNameTokenThreshold}}
	current := ContextWithReport(context.Background(), report)
	next := context.WithValue(context.Background(), reportContextTestKey{}, "next")

	got := inheritReportContext(next, current)
	inherited, ok := ReportFromContext(got)
	require.True(t, ok)
	require.Same(t, report, inherited)
	require.Equal(t, "next", got.Value(reportContextTestKey{}))

	existing := &Report{Trigger: Trigger{Name: checkNameContextThreshold}}
	got = inheritReportContext(ContextWithReport(next, existing), current)
	inherited, ok = ReportFromContext(got)
	require.True(t, ok)
	require.Same(t, existing, inherited)

	got = inheritReportContext(nil, current)
	inherited, ok = ReportFromContext(got)
	require.True(t, ok)
	require.Same(t, report, inherited)

	got = inheritReportContext(next, context.Background())
	_, ok = ReportFromContext(got)
	require.False(t, ok)
	require.Equal(t, "next", got.Value(reportContextTestKey{}))
}

func TestSessionSummarizer_ReportHookEstimatesAfterBeforeModelCallback(t *testing.T) {
	defer SetTokenCounter(nil)
	SetTokenCounter(testFixedTokenCounter{tokens: 5})

	var got Report
	callbacks := model.NewCallbacks()
	callbacks.RegisterBeforeModel(func(
		_ context.Context,
		args *model.BeforeModelArgs,
	) (*model.BeforeModelResult, error) {
		args.Request.Messages = append(
			args.Request.Messages,
			model.NewSystemMessage("callback-added"),
		)
		return nil, nil
	})
	m := &reportModel{}
	s := NewSummarizer(
		m,
		WithModelCallbacks(callbacks),
		WithReportHook(func(_ context.Context, report Report) {
			got = report
		}),
	)

	text, err := s.Summarize(context.Background(), newReportSession())
	require.NoError(t, err)
	require.Equal(t, "summary", text)
	require.NotNil(t, m.request)
	require.Len(t, m.request.Messages, 2)
	require.Equal(t, 10, got.Call.EstimatedPromptTokens)
}

func TestSessionSummarizer_ReportHookKeepsUsageFromEarlierResponse(t *testing.T) {
	defer SetTokenCounter(nil)
	SetTokenCounter(testFixedTokenCounter{tokens: 1})

	var got Report
	m := &reportModel{responses: []*model.Response{
		{
			Usage: &model.Usage{PromptTokens: 21},
			Choices: []model.Choice{{
				Message: model.Message{Content: "summary"},
			}},
		},
		{Done: true},
	}}
	s := NewSummarizer(
		m,
		WithReportHook(func(_ context.Context, report Report) {
			got = report
		}),
	)

	text, err := s.Summarize(context.Background(), newReportSession())
	require.NoError(t, err)
	require.Equal(t, "summary", text)
	require.Equal(t, 21, got.Call.PromptTokens)
}
