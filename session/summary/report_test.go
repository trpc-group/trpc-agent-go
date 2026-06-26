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
