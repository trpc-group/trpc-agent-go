//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summary

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/summaryview"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	isummarycontext "trpc.group/trpc-go/trpc-agent-go/session/internal/summarycontext"
)

func TestInputOverflowHook_AuthorizesOnlyFirstCompleteBatch(t *testing.T) {
	for _, test := range []struct {
		name    string
		replace bool
		fork    bool
	}{
		{name: "original"},
		{name: "replacement", replace: true},
		{name: "cache-safe fork", fork: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			capture := &retrySummaryModel{contextWindow: 100_000, inputBudget: 100,
				responses: []*model.Response{{Done: true, Choices: []model.Choice{{
					Message: model.NewAssistantMessage("summary"),
				}}}},
			}
			var approved string
			hookCalls, beforeCalls := 0, 0
			s := NewSummarizer(capture,
				WithCacheSafeForking(test.fork),
				WithPrompt("Previous: {previous_summary}\nConversation: {conversation_text}"),
				WithInputOverflowHook(func(ctx context.Context, in InputOverflow) (*OverflowDecision, error) {
					hookCalls++
					require.Contains(t, in.Text, "assistant-marker-1")
					require.NotContains(t, in.Text, "user-marker-2")
					approved = in.Text
					if test.replace {
						approved = "replacement " + strings.Repeat("retained fact ", 100)
					}
					return &OverflowDecision{Text: approved}, nil
				}),
				WithModelCallbacks(&model.Callbacks{BeforeModel: []model.BeforeModelCallbackStructured{
					func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
						beforeCalls++
						require.Contains(t, args.Request.Messages[0].Content, approved)
						return nil, nil
					},
				}}),
			)
			events := newSummaryPrefixRounds(2, 128)
			original := events[1].Response.Choices[0].Message.Content
			sess := &session.Session{ID: "overflow", Events: events}
			previous := strings.Repeat("previous summary fact ", 100)
			ctx := isummarycontext.WithPreviousSummary(context.Background(), previous)
			parentText := strings.Repeat("unrelated parent prefix ", 100)
			parent := &model.Request{Messages: []model.Message{model.NewUserMessage(parentText)}}
			if test.fork {
				ctx = ContextWithCacheSafeForkRequest(ctx, parent)
			}
			text, err := s.Summarize(ctx, sess)
			require.NoError(t, err)
			require.Equal(t, "summary", text)
			require.Equal(t, 1, hookCalls)
			require.Equal(t, 1, beforeCalls)
			require.Len(t, capture.requests, 1)
			wire := capture.requests[0].Messages[0].Content
			require.Contains(t, wire, approved)
			require.Contains(t, wire, previous)
			require.NotContains(t, wire, "user-marker-2")
			require.NotContains(t, wire, "unrelated parent prefix")
			require.Equal(t, parentText, parent.Messages[0].Content)
			tokens, err := countSummaryRequestTokens(ctx, capture.requests[0])
			require.NoError(t, err)
			require.Greater(t, tokens, capture.inputBudget)
			require.Equal(t, "assistant-event-1", string(sess.State[lastIncludedEventIDKey]))
			require.Equal(t, original, events[1].Response.Choices[0].Message.Content)
		})
	}
}

func TestInputOverflowHook_DeclinesAndFailuresKeepBoundary(t *testing.T) {
	businessErr := errors.New("business rejected input")
	for _, test := range []struct {
		name     string
		decision *OverflowDecision
		err      error
		want     string
	}{
		{name: "declined", want: "refusing to omit unsummarized conversation"},
		{name: "business error", err: businessErr, want: businessErr.Error()},
		{name: "error wins", decision: &OverflowDecision{Text: "replacement"}, err: businessErr, want: businessErr.Error()},
		{name: "empty", decision: &OverflowDecision{}, want: "input overflow hook returned empty text"},
		{name: "whitespace", decision: &OverflowDecision{Text: " \n\t"}, want: "input overflow hook returned empty text"},
	} {
		t.Run(test.name, func(t *testing.T) {
			capture := &cacheSafeCaptureModel{contextWindow: 100_000, inputBudget: 100}
			calls := 0
			s := NewSummarizer(capture, WithInputOverflowHook(
				func(context.Context, InputOverflow) (*OverflowDecision, error) {
					calls++
					return test.decision, test.err
				}),
			)
			sess := &session.Session{ID: "overflow", Events: newSummaryPrefixRounds(2, 128),
				State: session.StateMap{lastIncludedEventIDKey: []byte("previous-boundary")},
			}
			_, err := s.Summarize(context.Background(), sess)
			require.ErrorContains(t, err, test.want)
			if test.err != nil {
				require.ErrorIs(t, err, businessErr)
			}
			require.Equal(t, 1, calls)
			require.Nil(t, capture.request)
			require.Equal(t, "previous-boundary", string(sess.State[lastIncludedEventIDKey]))
		})
	}
}

func TestInputOverflowHook_PreservesOrdinaryPrefixFallback(t *testing.T) {
	capture := &cacheSafeCaptureModel{contextWindow: 100_000, response: "prefix summary"}
	s := NewSummarizer(capture,
		WithPrompt("Conversation: {conversation_text}"),
		WithInputOverflowHook(func(context.Context, InputOverflow) (*OverflowDecision, error) {
			t.Fatal("a complete prefix fits without consulting the hook")
			return nil, nil
		}),
	).(*sessionSummarizer)
	events := newSummaryPrefixRounds(3, 48)
	capture.inputBudget = summaryPrefixRequestTokens(t, s, events[:4])
	sess := &session.Session{ID: "overflow", Events: events}
	_, err := s.Summarize(context.Background(), sess)
	require.NoError(t, err)
	require.Equal(t, "assistant-event-2", string(sess.State[lastIncludedEventIDKey]))
}

func TestInputOverflowHook_HandlesSingleCompleteBatch(t *testing.T) {
	capture := &cacheSafeCaptureModel{contextWindow: 100_000, inputBudget: 100, response: "summary"}
	s := NewSummarizer(capture, WithInputOverflowHook(
		func(_ context.Context, in InputOverflow) (*OverflowDecision, error) {
			return &OverflowDecision{Text: in.Text}, nil
		}),
	)
	sess := &session.Session{ID: "overflow", Events: newSummaryPrefixRounds(1, 128)}
	_, err := s.Summarize(context.Background(), sess)
	require.NoError(t, err)
	require.Equal(t, "assistant-event-1", string(sess.State[lastIncludedEventIDKey]))
}

func TestInputOverflowHook_DoesNotRetryProviderFailureOrEmptySummary(t *testing.T) {
	code := "context_length_exceeded"
	for _, response := range []*model.Response{
		{Done: true, Error: &model.ResponseError{Code: &code, Message: "maximum context length exceeded"}},
		{Done: true},
	} {
		capture := &retrySummaryModel{contextWindow: 100_000, inputBudget: 100,
			responses: []*model.Response{response},
		}
		hookCalls := 0
		s := NewSummarizer(capture, WithInputOverflowHook(
			func(_ context.Context, in InputOverflow) (*OverflowDecision, error) {
				hookCalls++
				return &OverflowDecision{Text: in.Text}, nil
			}),
		)
		sess := &session.Session{ID: "overflow", Events: newSummaryPrefixRounds(2, 128)}
		_, err := s.Summarize(context.Background(), sess)
		require.Error(t, err)
		require.Equal(t, 1, hookCalls)
		require.Len(t, capture.requests, 1)
		require.Empty(t, sess.State[lastIncludedEventIDKey])
	}
}

func TestInputOverflowHook_AuthorizesOverflowAfterProviderRetry(t *testing.T) {
	code := "context_length_exceeded"
	capture := &retrySummaryModel{contextWindow: 100_000,
		responses: []*model.Response{
			{Done: true, Error: &model.ResponseError{Code: &code, Message: "maximum context length exceeded"}},
			{Done: true, Choices: []model.Choice{{Message: model.NewAssistantMessage("summary")}}},
		},
	}
	hookCalls := 0
	var approved string
	s := NewSummarizer(capture,
		WithPrompt("Conversation: {conversation_text}"),
		WithInputOverflowHook(func(_ context.Context, in InputOverflow) (*OverflowDecision, error) {
			hookCalls++
			approved = in.Text
			return &OverflowDecision{Text: approved}, nil
		}),
	).(*sessionSummarizer)
	events := newSummaryPrefixRounds(1, 128)
	capture.inputBudget = summaryPrefixRequestTokens(t, s, events) + 1
	sess := &session.Session{ID: "retry-overflow", Events: events}

	text, err := s.Summarize(context.Background(), sess)

	require.NoError(t, err)
	require.Equal(t, "summary", text)
	require.Equal(t, 1, hookCalls)
	require.Len(t, capture.requests, 2)
	require.Contains(t, capture.requests[1].Messages[0].Content, approved)
	require.Equal(t, "assistant-event-1", string(sess.State[lastIncludedEventIDKey]))
}

func TestInputOverflowHook_RespectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	capture := &cacheSafeCaptureModel{contextWindow: 100_000, inputBudget: 100}
	s := NewSummarizer(capture, WithInputOverflowHook(
		func(_ context.Context, in InputOverflow) (*OverflowDecision, error) {
			cancel()
			return &OverflowDecision{Text: in.Text}, nil
		}),
	)
	sess := &session.Session{ID: "overflow", Events: newSummaryPrefixRounds(1, 128)}
	_, err := s.Summarize(ctx, sess)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, capture.request)
	require.Empty(t, sess.State[lastIncludedEventIDKey])
}

func TestInputOverflowHook_AppliesAfterPreSummaryHook(t *testing.T) {
	capture := &cacheSafeCaptureModel{contextWindow: 100_000, inputBudget: 100, response: "summary"}
	rewritten := strings.Repeat("pre-hook text ", 128)
	s := NewSummarizer(capture,
		WithPreSummaryHook(func(in *PreSummaryHookContext) error {
			in.Text = rewritten
			return nil
		}),
		WithInputOverflowHook(func(_ context.Context, in InputOverflow) (*OverflowDecision, error) {
			require.Equal(t, rewritten, in.Text)
			return &OverflowDecision{Text: in.Text}, nil
		}),
	)
	sess := &session.Session{ID: "overflow", Events: newSummaryPrefixRounds(2, 128)}
	_, err := s.Summarize(context.Background(), sess)
	require.NoError(t, err)
	require.Contains(t, capture.request.Messages[0].Content, rewritten)
	require.Equal(t, "assistant-event-2", string(sess.State[lastIncludedEventIDKey]))
}

func TestInputOverflowHook_RequiresACompleteBoundary(t *testing.T) {
	capture := &cacheSafeCaptureModel{contextWindow: 100_000, inputBudget: 100}
	s := NewSummarizer(capture, WithInputOverflowHook(
		func(context.Context, InputOverflow) (*OverflowDecision, error) {
			t.Fatal("an unfinished user message cannot be committed")
			return nil, nil
		}),
	)
	sess := &session.Session{ID: "overflow", Events: newSummaryPrefixRounds(1, 128)[:1]}
	_, err := s.Summarize(context.Background(), sess)
	require.ErrorContains(t, err, "refusing to omit unsummarized conversation")
	require.Nil(t, capture.request)
}

func TestInputOverflowHook_CommitsStoredSourceBoundary(t *testing.T) {
	capture := &cacheSafeCaptureModel{contextWindow: 100_000, inputBudget: 100, response: "summary"}
	s := NewSummarizer(capture, WithInputOverflowHook(
		func(_ context.Context, in InputOverflow) (*OverflowDecision, error) {
			return &OverflowDecision{Text: in.Text}, nil
		}),
	)
	raw := newSummaryPrefixRounds(2, 128)
	effective := append([]event.Event(nil), raw...)
	items := make([]summaryview.Item, len(effective))
	for i := range effective {
		effective[i].ID = "effective-" + raw[i].ID
		items[i] = summaryview.Item{
			Message:        effective[i].Response.Choices[0].Message,
			EffectiveEvent: effective[i],
			Boundary: summaryview.Boundary{
				EventID: raw[i].ID, Timestamp: raw[i].Timestamp,
			},
		}
	}
	sess := &session.Session{ID: "overflow", Events: raw}
	ctx := summaryview.ContextWithView(context.Background(), &summaryview.View{
		SessionID: sess.ID, Bound: true, Items: items,
	})
	_, err := s.Summarize(ctx, sess)
	require.NoError(t, err)
	require.Equal(t, "assistant-event-1", string(sess.State[lastIncludedEventIDKey]))
}
