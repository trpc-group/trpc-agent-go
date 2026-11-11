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
	"time"

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

func TestInputOverflowHook_PreservesMergedPreviousSummary(t *testing.T) {
	capture := &cacheSafeCaptureModel{contextWindow: 100_000, inputBudget: 100, response: "summary"}
	previous := "The previous summary contains an important earlier fact."
	s := NewSummarizer(capture, WithInputOverflowHook(
		func(_ context.Context, in InputOverflow) (*OverflowDecision, error) {
			require.Contains(t, in.Text, previous)
			require.Contains(t, in.Text, "assistant-marker-1")
			require.NotContains(t, in.Text, "user-marker-2")
			return &OverflowDecision{Text: in.Text}, nil
		}),
	)
	conversation := newSummaryPrefixRounds(2, 128)
	sess := &session.Session{ID: "merged-overflow", Events: append([]event.Event{{
		Author: authorSystem, Timestamp: conversation[0].Timestamp.Add(-time.Second),
		Response: &model.Response{Choices: []model.Choice{{Message: model.NewSystemMessage(previous)}}},
	}}, conversation...)}
	ctx := isummarycontext.WithPreviousSummary(context.Background(), previous)

	_, err := s.Summarize(ctx, sess)

	require.NoError(t, err)
	require.NotNil(t, capture.request)
	require.Contains(t, capture.request.Messages[0].Content, previous)
	require.Contains(t, capture.request.Messages[0].Content, "assistant-marker-1")
	require.NotContains(t, capture.request.Messages[0].Content, "user-marker-2")
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

func TestInputOverflowHook_TruncatedSummaryKeepsBoundary(t *testing.T) {
	for _, reason := range []string{"model_context_window_exceeded", "max_tokens", "MAX_TOKENS", "length"} {
		t.Run(reason, func(t *testing.T) {
			capture := &retrySummaryModel{contextWindow: 100_000, inputBudget: 100,
				responses: []*model.Response{{Done: true, Choices: []model.Choice{{
					Message: model.NewAssistantMessage("incomplete summary"), FinishReason: &reason,
				}}}},
			}
			s := NewSummarizer(capture, WithInputOverflowHook(
				func(_ context.Context, in InputOverflow) (*OverflowDecision, error) {
					return &OverflowDecision{Text: in.Text}, nil
				}),
			)
			sess := &session.Session{ID: "truncated-overflow", Events: newSummaryPrefixRounds(1, 128),
				State: session.StateMap{lastIncludedEventIDKey: []byte("previous-boundary")},
			}

			text, err := s.Summarize(context.Background(), sess)

			require.ErrorContains(t, err, reason)
			require.Empty(t, text)
			require.Len(t, capture.requests, 1)
			require.Equal(t, "previous-boundary", string(sess.State[lastIncludedEventIDKey]))
		})
	}
}

func TestInputOverflowHook_UnusedHookPreservesFinishReasonBehavior(t *testing.T) {
	reason := "length"
	capture := &retrySummaryModel{contextWindow: 100_000, inputBudget: 100_000,
		responses: []*model.Response{{Done: true, Choices: []model.Choice{{
			Message: model.NewAssistantMessage("existing summary behavior"), FinishReason: &reason,
		}}}},
	}
	s := NewSummarizer(capture, WithInputOverflowHook(
		func(context.Context, InputOverflow) (*OverflowDecision, error) {
			t.Fatal("the request fits the ordinary budget")
			return nil, nil
		}),
	)
	sess := &session.Session{ID: "ordinary-summary", Events: newSummaryPrefixRounds(1, 8)}

	text, err := s.Summarize(context.Background(), sess)

	require.NoError(t, err)
	require.Equal(t, "existing summary behavior", text)
	require.Equal(t, "assistant-event-1", string(sess.State[lastIncludedEventIDKey]))
}

func TestInputOverflowHook_TruncatedStreamProcessesFinalResponse(t *testing.T) {
	reason := "model_context_window_exceeded"
	final := &model.Response{Done: true, Usage: &model.Usage{PromptTokens: 500, CompletionTokens: 8},
		Choices: []model.Choice{{Message: model.NewAssistantMessage("incomplete summary"), FinishReason: &reason}},
	}
	capture := &reportModel{responses: []*model.Response{
		{IsPartial: true, Choices: []model.Choice{{FinishReason: &reason}}},
		final,
	}}
	callbacksSeen := 0
	var report Report
	s := NewSummarizer(capture,
		WithRequestInputTokenBudget(100),
		WithReportHook(func(_ context.Context, r Report) { report = r }),
		WithInputOverflowHook(func(_ context.Context, in InputOverflow) (*OverflowDecision, error) {
			return &OverflowDecision{Text: in.Text}, nil
		}),
		WithModelCallbacks(model.NewCallbacks().RegisterBeforeModel(
			func(_ context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
				args.Request.Stream = true
				return nil, nil
			},
		).RegisterAfterModel(
			func(_ context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
				callbacksSeen++
				if args.Response.Done {
					require.Same(t, final, args.Response)
					require.Equal(t, 500, args.Response.Usage.PromptTokens)
				}
				return nil, nil
			},
		)),
	)
	sess := &session.Session{ID: "truncated-stream", Events: newSummaryPrefixRounds(1, 128)}

	_, err := s.Summarize(context.Background(), sess)

	require.ErrorContains(t, err, reason)
	require.Equal(t, 2, callbacksSeen)
	require.Equal(t, 500, report.Call.PromptTokens)
	require.ErrorContains(t, report.Error, reason)
	require.Empty(t, sess.State[lastIncludedEventIDKey])
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
	for _, stage := range []string{"before summary", "overflow hook", "before model"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			capture := &cacheSafeCaptureModel{contextWindow: 100_000, inputBudget: 100}
			s := NewSummarizer(capture,
				WithInputOverflowHook(func(_ context.Context, in InputOverflow) (*OverflowDecision, error) {
					if stage == "overflow hook" {
						cancel()
					}
					return &OverflowDecision{Text: in.Text}, nil
				}),
				WithModelCallbacks(model.NewCallbacks().RegisterBeforeModel(
					func(context.Context, *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
						if stage == "before model" {
							cancel()
						}
						return nil, nil
					},
				)),
			)
			if stage == "before summary" {
				cancel()
			}
			sess := &session.Session{ID: "overflow", Events: newSummaryPrefixRounds(1, 128)}
			_, err := s.Summarize(ctx, sess)
			require.ErrorIs(t, err, context.Canceled)
			require.Nil(t, capture.request)
			require.Empty(t, sess.State[lastIncludedEventIDKey])
		})
	}
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
