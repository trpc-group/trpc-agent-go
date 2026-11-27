//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package summary

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestSessionSummarizer_RequestInputTokenBudget(t *testing.T) {
	ctx := context.Background()
	newSession := func() *session.Session {
		evt := newEventWithContent(strings.Repeat("source content ", 64))
		evt.ID = "budget-event"
		return &session.Session{ID: "budget-session", Events: []event.Event{evt}}
	}
	baseOptions := []Option{
		WithSystemPrompt("Summarize the supplied conversation."),
		WithPrompt("Conversation:\n{conversation_text}\n\nSummary:"),
	}
	baseline := &cacheSafeCaptureModel{response: "summary", contextWindow: 100_000}
	_, err := NewSummarizer(baseline, baseOptions...).Summarize(ctx, newSession())
	require.NoError(t, err)
	tokens, err := countSummaryRequestTokens(ctx, baseline.request)
	require.NoError(t, err)
	require.Greater(t, tokens, 4)
	// The request fits this window but exceeds its default 70% input budget.
	window := tokens + tokens/4

	for _, test := range []struct {
		name           string
		contextWindow  int
		providerBudget int
		budgets        []int
		wantError      bool
	}{
		{name: "default budget unchanged", contextWindow: window, wantError: true},
		{name: "default admits smaller requests", contextWindow: 2 * tokens},
		{name: "raises the default budget", contextWindow: window, budgets: []int{tokens}},
		{name: "rejects one token over budget", contextWindow: window, budgets: []int{tokens - 1}, wantError: true},
		{name: "accepts a smaller explicit budget", contextWindow: 2 * tokens, budgets: []int{tokens}},
		{name: "rejects at a smaller explicit budget", contextWindow: 2 * tokens, budgets: []int{tokens - 1}, wantError: true},
		{name: "one token budget", contextWindow: window, budgets: []int{1}, wantError: true},
		{name: "caps at model window", contextWindow: tokens - 1, budgets: []int{2 * tokens}, wantError: true},
		{name: "caps at provider budget", contextWindow: window, providerBudget: tokens - 1, budgets: []int{tokens}, wantError: true},
		{name: "fits provider budget exactly", contextWindow: window, providerBudget: tokens, budgets: []int{2 * tokens}},
		{name: "last positive value wins", contextWindow: window, budgets: []int{1, tokens}},
		{name: "last smaller value wins", contextWindow: window, budgets: []int{tokens, 1}, wantError: true},
		{name: "zero restores default", contextWindow: window, budgets: []int{tokens, 0}, wantError: true},
		{name: "negative restores default", contextWindow: window, budgets: []int{tokens, -1}, wantError: true},
		{name: "unknown model accepts explicit budget", budgets: []int{tokens}},
	} {
		t.Run(test.name, func(t *testing.T) {
			capture := &cacheSafeCaptureModel{
				response: "summary", contextWindow: test.contextWindow,
				inputBudget: test.providerBudget,
			}
			opts := append([]Option(nil), baseOptions...)
			for _, budget := range test.budgets {
				opts = append(opts, WithRequestInputTokenBudget(budget))
			}
			s := NewSummarizer(capture, opts...)
			sess := newSession()
			require.True(t, s.ShouldSummarize(sess), "input budgeting must not change the trigger")
			text, err := s.Summarize(ctx, sess)
			if test.wantError {
				require.Error(t, err)
				require.Nil(t, capture.request, "oversized input must not reach the model")
				require.NotContains(t, sess.State, lastIncludedEventIDKey)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "summary", text)
			require.Equal(t, baseline.request.Messages, capture.request.Messages)
			require.Equal(t, "budget-event", string(sess.State[lastIncludedEventIDKey]))
		})
	}

	t.Run("unknown model retains fallback window limit", func(t *testing.T) {
		capture := &cacheSafeCaptureModel{response: "must not be called"}
		s := NewSummarizer(capture, WithRequestInputTokenBudget(100_000))
		sess := &session.Session{ID: "unknown-model", Events: []event.Event{
			newEventWithContent(strings.Repeat("large source content ", 8192)),
		}}
		_, err := s.Summarize(ctx, sess)
		require.ErrorContains(t, err, "input budget is 8192")
		require.Nil(t, capture.request)
		require.NotContains(t, sess.State, lastIncludedEventIDKey)
	})
}

func TestSessionSummarizer_RequestInputTokenBudgetAfterCallback(t *testing.T) {
	ctx := context.Background()
	newSession := func() *session.Session {
		return &session.Session{ID: "callback-budget", Events: []event.Event{
			newEventWithContent("source conversation"),
		}}
	}
	for _, fork := range []bool{false, true} {
		name := "standalone"
		if fork {
			name = "cache-safe fork"
		}
		t.Run(name, func(t *testing.T) {
			capture := &cacheSafeCaptureModel{response: "summary", contextWindow: 100_000}
			parent := &model.Request{Messages: []model.Message{
				model.NewUserMessage("source conversation"),
			}}
			ctx := ContextWithCacheSafeForkRequest(ctx, parent)
			opts := []Option{WithCacheSafeForking(fork)}
			_, err := NewSummarizer(capture, opts...).Summarize(ctx, newSession())
			require.NoError(t, err)
			budget, err := countSummaryRequestTokens(ctx, capture.request)
			require.NoError(t, err)
			capture.request = nil
			called := false
			callbacks := model.NewCallbacks().RegisterBeforeModel(func(
				ctx context.Context, args *model.BeforeModelArgs,
			) (*model.BeforeModelResult, error) {
				called = true
				args.Request.Messages = append(args.Request.Messages,
					model.NewUserMessage(strings.Repeat("extra input ", 100)))
				return nil, nil
			})
			s := NewSummarizer(capture, append(opts,
				WithRequestInputTokenBudget(budget), WithModelCallbacks(callbacks),
			)...)
			sess := newSession()
			_, err = s.Summarize(ctx, sess)
			require.True(t, called)
			require.ErrorContains(t, err, "no longer fits after before-model callbacks")
			require.Nil(t, capture.request)
			require.NotContains(t, sess.State, lastIncludedEventIDKey)
			require.Len(t, parent.Messages, 1)
		})
	}
}

func TestSessionSummarizer_RequestInputTokenBudgetOnRetry(t *testing.T) {
	ctx := context.Background()
	capture := &retrySummaryModel{
		contextWindow: 100_000,
		responses: []*model.Response{
			{Done: true},
			{Done: true, Choices: []model.Choice{{
				Message: model.NewAssistantMessage("retry summary"),
			}}},
		},
	}
	opts := []Option{WithPrompt("Conversation:\n{conversation_text}\n\nSummary:")}
	s := NewSummarizer(capture, opts...).(*sessionSummarizer)
	events := newSummaryPrefixRounds(3, 48)
	oneRoundTokens := summaryPrefixRequestTokens(t, s, events[:2])
	twoRoundTokens := summaryPrefixRequestTokens(t, s, events[:4])
	allRoundTokens := summaryPrefixRequestTokens(t, s, events)
	budget := max(twoRoundTokens, 2*oneRoundTokens)
	require.Less(t, budget, allRoundTokens)
	require.Less(t, budget/2, twoRoundTokens)
	summarizer := NewSummarizer(capture, append(opts,
		WithRequestInputTokenBudget(budget),
	)...)
	sess := &session.Session{ID: "request-budget-retry", Events: events}

	text, err := summarizer.Summarize(ctx, sess)
	require.NoError(t, err)
	require.Equal(t, "retry summary", text)
	require.Len(t, capture.requests, 2)
	for i, limit := range []int{budget, budget / 2} {
		tokens, err := countSummaryRequestTokens(ctx, capture.requests[i])
		require.NoError(t, err)
		require.LessOrEqual(t, tokens, limit)
	}
	require.Contains(t, capture.requests[0].Messages[0].Content, "assistant-marker-2")
	require.NotContains(t, capture.requests[1].Messages[0].Content, "assistant-marker-2")
	require.Equal(t, "assistant-event-1", string(sess.State[lastIncludedEventIDKey]))
}
