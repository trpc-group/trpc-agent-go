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
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/summaryview"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	isummarycontext "trpc.group/trpc-go/trpc-agent-go/session/internal/summarycontext"
)

func TestSessionSummarizer_UsesLargestCompletePrefix(t *testing.T) {
	for _, test := range []struct {
		name          string
		cacheSafeFork bool
	}{
		{name: "standalone"},
		{name: "cache-safe fork", cacheSafeFork: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			capture := &cacheSafeCaptureModel{
				response:      "prefix summary",
				contextWindow: 100_000,
			}
			opts := []Option{
				WithPrompt("Conversation:\n{conversation_text}\n\nSummary:"),
			}
			if test.cacheSafeFork {
				opts = append(opts, WithCacheSafeForking(true))
			}
			s := NewSummarizer(capture, opts...).(*sessionSummarizer)
			events := newSummaryPrefixRounds(3, 48)

			twoRoundTokens := summaryPrefixRequestTokens(t, s, events[:4])
			allRoundTokens := summaryPrefixRequestTokens(t, s, events)
			require.Greater(t, allRoundTokens, twoRoundTokens)
			capture.inputBudget = twoRoundTokens +
				(allRoundTokens-twoRoundTokens)/2

			ctx := context.Background()
			if test.cacheSafeFork {
				parent := &model.Request{Messages: []model.Message{
					model.NewSystemMessage("stable system instruction"),
					model.NewUserMessage(strings.Repeat("parent history ", 2_000)),
				}}
				ctx = ContextWithCacheSafeForkRequest(ctx, parent)
			}
			sess := &session.Session{ID: "prefix-session", Events: events}

			text, err := s.Summarize(ctx, sess)
			require.NoError(t, err)
			require.Equal(t, "prefix summary", text)
			require.NotNil(t, capture.request)
			require.Len(t, capture.request.Messages, 1)
			prompt := capture.request.Messages[0].Content
			require.Contains(t, prompt, "user-marker-1")
			require.Contains(t, prompt, "assistant-marker-2")
			require.NotContains(t, prompt, "user-marker-3")
			require.NotContains(t, prompt, "assistant-marker-3")
			tokens, err := countSummaryRequestTokens(ctx, capture.request)
			require.NoError(t, err)
			require.LessOrEqual(t, tokens, capture.inputBudget)
			require.Equal(
				t,
				"assistant-event-2",
				string(sess.State[lastIncludedEventIDKey]),
			)
		})
	}
}

func TestSessionSummarizer_SafePrefixUsesStoredBoundary(t *testing.T) {
	capture := &cacheSafeCaptureModel{
		response:      "prefix summary",
		contextWindow: 100_000,
	}
	s := NewSummarizer(
		capture,
		WithPrompt("Conversation:\n{conversation_text}\n\nSummary:"),
	).(*sessionSummarizer)
	rawEvents := newSummaryPrefixRounds(3, 48)
	effectiveEvents := append([]event.Event(nil), rawEvents...)
	items := make([]summaryview.Item, len(effectiveEvents))
	for i := range effectiveEvents {
		effectiveEvents[i].ID = fmt.Sprintf("effective-event-%d", i+1)
		items[i] = summaryview.Item{
			Message:        effectiveEvents[i].Response.Choices[0].Message,
			EffectiveEvent: effectiveEvents[i],
			Boundary: summaryview.Boundary{
				EventID:   rawEvents[i].ID,
				Timestamp: rawEvents[i].Timestamp,
			},
		}
	}
	twoRoundTokens := summaryPrefixRequestTokens(t, s, effectiveEvents[:4])
	allRoundTokens := summaryPrefixRequestTokens(t, s, effectiveEvents)
	require.Greater(t, allRoundTokens, twoRoundTokens)
	capture.inputBudget = twoRoundTokens +
		(allRoundTokens-twoRoundTokens)/2
	sess := &session.Session{ID: "mapped-prefix-session", Events: rawEvents}
	ctx := summaryview.ContextWithView(context.Background(), &summaryview.View{
		SessionID: sess.ID,
		Items:     items,
		Bound:     true,
	})

	text, err := s.Summarize(ctx, sess)
	require.NoError(t, err)
	require.Equal(t, "prefix summary", text)
	require.Contains(t, capture.request.Messages[0].Content, "assistant-marker-2")
	require.NotContains(t, capture.request.Messages[0].Content, "assistant-marker-3")
	require.Equal(
		t,
		"assistant-event-2",
		string(sess.State[lastIncludedEventIDKey]),
	)
}

func TestSessionSummarizer_SafePrefixRollsPreviousSummary(t *testing.T) {
	capture := &cacheSafeCaptureModel{
		response:      "rolling prefix summary",
		contextWindow: 100_000,
	}
	s := NewSummarizer(
		capture,
		WithPrompt(
			"Previous:\n{previous_summary}\n\nConversation:\n"+
				"{conversation_text}\n\nSummary:",
		),
	).(*sessionSummarizer)
	conversation := newSummaryPrefixRounds(3, 48)
	twoRoundTokens := summaryPrefixRequestTokens(t, s, conversation[:4])
	allRoundTokens := summaryPrefixRequestTokens(t, s, conversation)
	require.Greater(t, allRoundTokens, twoRoundTokens)
	capture.inputBudget = twoRoundTokens +
		(allRoundTokens-twoRoundTokens)/2
	previous := strings.Repeat("historical summary ", 2_000)
	sess := &session.Session{
		ID: "rolling-prefix-session",
		Events: append([]event.Event{{
			Author:    authorSystem,
			Timestamp: time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC),
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{Content: previous},
			}}},
		}}, conversation...),
	}
	ctx := isummarycontext.WithPreviousSummary(context.Background(), previous)

	text, err := s.Summarize(ctx, sess)
	require.NoError(t, err)
	require.Equal(t, "rolling prefix summary", text)
	require.NotNil(t, capture.request)
	prompt := capture.request.Messages[0].Content
	require.Contains(t, prompt, "assistant-marker-2")
	require.NotContains(t, prompt, "assistant-marker-3")
	require.Contains(t, prompt, summaryPreviousOmitted)
	require.Equal(
		t,
		"assistant-event-2",
		string(sess.State[lastIncludedEventIDKey]),
	)
}

func TestSessionSummarizer_SafePrefixKeepsConfiguredRecentEvents(t *testing.T) {
	capture := &cacheSafeCaptureModel{
		response:      "prefix summary",
		contextWindow: 100_000,
	}
	s := NewSummarizer(
		capture,
		WithPrompt("Conversation:\n{conversation_text}\n\nSummary:"),
		WithSkipRecent(func([]event.Event) int { return 2 }),
	).(*sessionSummarizer)
	events := newSummaryPrefixRounds(4, 48)
	twoRoundTokens := summaryPrefixRequestTokens(t, s, events[:4])
	threeRoundTokens := summaryPrefixRequestTokens(t, s, events[:6])
	require.Greater(t, threeRoundTokens, twoRoundTokens)
	capture.inputBudget = twoRoundTokens +
		(threeRoundTokens-twoRoundTokens)/2
	sess := &session.Session{ID: "skip-recent-prefix-session", Events: events}

	text, err := s.Summarize(context.Background(), sess)
	require.NoError(t, err)
	require.Equal(t, "prefix summary", text)
	prompt := capture.request.Messages[0].Content
	require.Contains(t, prompt, "assistant-marker-2")
	require.NotContains(t, prompt, "assistant-marker-3")
	require.NotContains(t, prompt, "assistant-marker-4")
	require.Equal(
		t,
		"assistant-event-2",
		string(sess.State[lastIncludedEventIDKey]),
	)
}

func TestSessionSummarizer_SafePrefixPreservesToolRounds(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	events := []event.Event{
		newSummaryPrefixEvent(
			"user-event", "user-response", authorUser, model.RoleUser,
			"request", now,
		),
		{
			ID:           "tool-call-event",
			RequestID:    "request-1",
			InvocationID: "invocation-1",
			Author:       "agent",
			Timestamp:    now.Add(time.Second),
			Response: &model.Response{
				ID: "tool-call-response",
				Choices: []model.Choice{{Message: model.Message{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{
						{ID: "call-1"},
						{ID: "call-2"},
					},
				}}},
			},
		},
		{
			ID:           "first-tool-result",
			RequestID:    "request-1",
			InvocationID: "invocation-1",
			Author:       "agent",
			Timestamp:    now.Add(2 * time.Second),
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.NewToolMessage("call-1", "lookup", "first"),
			}}},
		},
		{
			ID:           "second-tool-result",
			RequestID:    "request-1",
			InvocationID: "invocation-1",
			Author:       "agent",
			Timestamp:    now.Add(3 * time.Second),
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.NewToolMessage("call-2", "lookup", "second"),
			}}},
		},
		newSummaryPrefixEvent(
			"assistant-event", "assistant-response", "agent",
			model.RoleAssistant, "complete", now.Add(4*time.Second),
		),
	}

	require.Equal(t, []int{4, 5}, safeSummaryPrefixEnds(events))
}

func TestSessionSummarizer_SafePrefixFormatsEachToolPayloadOnce(t *testing.T) {
	callFormats := 0
	resultFormats := 0
	capture := &cacheSafeCaptureModel{
		response:      "prefix summary",
		contextWindow: 100_000,
	}
	s := NewSummarizer(
		capture,
		WithPrompt("Conversation:\n{conversation_text}\n\nSummary:"),
		WithToolCallFormatter(func(model.ToolCall) string {
			callFormats++
			return "[tool call]"
		}),
		WithToolResultFormatter(func(model.Message) string {
			resultFormats++
			return "[tool result]"
		}),
	).(*sessionSummarizer)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	events := []event.Event{
		newSummaryPrefixEvent(
			"user-event-1", "user-response-1", authorUser, model.RoleUser,
			"request", now,
		),
		{
			ID:        "tool-call-event",
			Author:    "agent",
			Timestamp: now.Add(time.Second),
			Response: &model.Response{Choices: []model.Choice{{Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID: "call-1",
				}},
			}}}},
		},
		{
			ID:        "tool-result-event",
			Author:    "agent",
			Timestamp: now.Add(2 * time.Second),
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.NewToolMessage("call-1", "lookup", "result"),
			}}},
		},
		newSummaryPrefixEvent(
			"assistant-event-1", "assistant-response-1", "agent",
			model.RoleAssistant, "complete", now.Add(3*time.Second),
		),
		newSummaryPrefixEvent(
			"user-event-2", "user-response-2", authorUser, model.RoleUser,
			strings.Repeat("later request ", 96), now.Add(4*time.Second),
		),
		newSummaryPrefixEvent(
			"assistant-event-2", "assistant-response-2", "agent",
			model.RoleAssistant, strings.Repeat("later response ", 96),
			now.Add(5*time.Second),
		),
	}
	prefixTokens := summaryPrefixRequestTokens(t, s, events[:4])
	allTokens := summaryPrefixRequestTokens(t, s, events)
	require.Greater(t, allTokens, prefixTokens)
	capture.inputBudget = prefixTokens + (allTokens-prefixTokens)/2
	callFormats = 0
	resultFormats = 0
	sess := &session.Session{ID: "formatter-prefix-session", Events: events}

	_, err := s.Summarize(context.Background(), sess)
	require.NoError(t, err)
	require.Equal(t, 1, callFormats)
	require.Equal(t, 1, resultFormats)
	require.Equal(
		t,
		"assistant-event-1",
		string(sess.State[lastIncludedEventIDKey]),
	)
}

func TestSessionSummarizer_SafePrefixSkipsEmptyBoundaries(t *testing.T) {
	capture := &cacheSafeCaptureModel{
		response:      "prefix summary",
		contextWindow: 100_000,
	}
	s := NewSummarizer(
		capture,
		WithPrompt("Conversation:\n{conversation_text}\n\nSummary:"),
		WithToolCallFormatter(func(model.ToolCall) string { return "" }),
		WithToolResultFormatter(func(model.Message) string { return "" }),
	).(*sessionSummarizer)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	events := []event.Event{
		{
			ID:        "tool-call-event",
			Author:    "agent",
			Timestamp: now,
			Response: &model.Response{Choices: []model.Choice{{Message: model.Message{
				Role:      model.RoleAssistant,
				ToolCalls: []model.ToolCall{{ID: "call-1"}},
			}}}},
		},
		{
			ID:        "tool-result-event",
			Author:    "agent",
			Timestamp: now.Add(time.Second),
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.NewToolMessage("call-1", "lookup", "result"),
			}}},
		},
		newSummaryPrefixEvent(
			"user-event", "user-response", authorUser, model.RoleUser,
			"small request", now.Add(2*time.Second),
		),
		newSummaryPrefixEvent(
			"assistant-event", "assistant-response", "agent",
			model.RoleAssistant, "small response", now.Add(3*time.Second),
		),
		newSummaryPrefixEvent(
			"large-user-event", "large-user-response", authorUser,
			model.RoleUser, strings.Repeat("later request ", 96),
			now.Add(4*time.Second),
		),
		newSummaryPrefixEvent(
			"large-assistant-event", "large-assistant-response", "agent",
			model.RoleAssistant, strings.Repeat("later response ", 96),
			now.Add(5*time.Second),
		),
	}
	prefixTokens := summaryPrefixRequestTokens(t, s, events[:4])
	allTokens := summaryPrefixRequestTokens(t, s, events)
	require.Greater(t, allTokens, prefixTokens)
	capture.inputBudget = prefixTokens + (allTokens-prefixTokens)/2
	sess := &session.Session{ID: "empty-boundary-session", Events: events}

	text, err := s.Summarize(context.Background(), sess)
	require.NoError(t, err)
	require.Equal(t, "prefix summary", text)
	require.Contains(t, capture.request.Messages[0].Content, "small response")
	require.NotContains(t, capture.request.Messages[0].Content, "later request")
	require.Equal(
		t,
		"assistant-event",
		string(sess.State[lastIncludedEventIDKey]),
	)
}

func TestSessionSummarizer_SafePrefixDoesNotEndAfterEmptyBoundary(t *testing.T) {
	capture := &cacheSafeCaptureModel{
		response:      "prefix summary",
		contextWindow: 100_000,
	}
	s := NewSummarizer(
		capture,
		WithPrompt("Conversation:\n{conversation_text}\n\nSummary:"),
		WithToolCallFormatter(func(model.ToolCall) string { return "" }),
		WithToolResultFormatter(func(model.Message) string { return "" }),
	).(*sessionSummarizer)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	events := []event.Event{
		newSummaryPrefixEvent(
			"user-event", "user-response", authorUser, model.RoleUser,
			"small request", now,
		),
		newSummaryPrefixEvent(
			"assistant-event", "assistant-response", "agent",
			model.RoleAssistant, "small response", now.Add(time.Second),
		),
		{
			ID:        "tool-call-event",
			Author:    "agent",
			Timestamp: now.Add(2 * time.Second),
			Response: &model.Response{Choices: []model.Choice{{Message: model.Message{
				Role:      model.RoleAssistant,
				ToolCalls: []model.ToolCall{{ID: "call-1"}},
			}}}},
		},
		{
			ID:        "tool-result-event",
			Author:    "agent",
			Timestamp: now.Add(3 * time.Second),
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.NewToolMessage("call-1", "lookup", "result"),
			}}},
		},
		newSummaryPrefixEvent(
			"large-user-event", "large-user-response", authorUser,
			model.RoleUser, strings.Repeat("later request ", 96),
			now.Add(4*time.Second),
		),
		newSummaryPrefixEvent(
			"large-assistant-event", "large-assistant-response", "agent",
			model.RoleAssistant, strings.Repeat("later response ", 96),
			now.Add(5*time.Second),
		),
	}
	prefixTokens := summaryPrefixRequestTokens(t, s, events[:4])
	allTokens := summaryPrefixRequestTokens(t, s, events)
	require.Greater(t, allTokens, prefixTokens)
	capture.inputBudget = prefixTokens + (allTokens-prefixTokens)/2
	sess := &session.Session{ID: "trailing-empty-boundary-session", Events: events}

	text, err := s.Summarize(context.Background(), sess)
	require.NoError(t, err)
	require.Equal(t, "prefix summary", text)
	require.Contains(t, capture.request.Messages[0].Content, "small response")
	require.NotContains(t, capture.request.Messages[0].Content, "later request")
	require.Equal(
		t,
		"assistant-event",
		string(sess.State[lastIncludedEventIDKey]),
	)
}

func TestSafeSummaryPrefixEnds_RejectsIncompleteBoundaries(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	t.Run("response chunks stay together", func(t *testing.T) {
		events := []event.Event{
			newSummaryPrefixEvent(
				"user-event", "user-response", authorUser, model.RoleUser,
				"request", now,
			),
			newSummaryPrefixEvent(
				"assistant-chunk-1", "shared-response", "agent",
				model.RoleAssistant, "first", now.Add(time.Second),
			),
			newSummaryPrefixEvent(
				"assistant-chunk-2", "shared-response", "agent",
				model.RoleAssistant, "second", now.Add(2*time.Second),
			),
		}
		events[1].Response.IsPartial = true

		require.Equal(t, []int{3}, safeSummaryPrefixEnds(events))
	})

	t.Run("delta tool round stays together", func(t *testing.T) {
		events := []event.Event{
			{
				ID:        "delta-call",
				Author:    "agent",
				Timestamp: now,
				Response: &model.Response{Choices: []model.Choice{{
					Delta: model.Message{
						Role:      model.RoleAssistant,
						ToolCalls: []model.ToolCall{{ID: "call-1"}},
					},
				}}},
			},
			{
				ID:        "delta-result",
				Author:    "agent",
				Timestamp: now.Add(time.Second),
				Response: &model.Response{Choices: []model.Choice{{
					Delta: model.Message{
						Role:    model.RoleTool,
						ToolID:  "call-1",
						Content: "result",
					},
				}}},
			},
		}

		require.Equal(t, []int{2}, safeSummaryPrefixEnds(events))
	})

	t.Run("anonymous tool call stays open", func(t *testing.T) {
		events := []event.Event{
			newSummaryPrefixEvent(
				"assistant-event-1", "assistant-response-1", "agent",
				model.RoleAssistant, "complete", now,
			),
			{
				ID:        "anonymous-call",
				Author:    "agent",
				Timestamp: now.Add(time.Second),
				Response: &model.Response{Choices: []model.Choice{{
					Message: model.Message{
						Role:      model.RoleAssistant,
						ToolCalls: []model.ToolCall{{}},
					},
				}}},
			},
			newSummaryPrefixEvent(
				"assistant-event-2", "assistant-response-2", "agent",
				model.RoleAssistant, "later", now.Add(2*time.Second),
			),
		}

		require.Equal(t, []int{1}, safeSummaryPrefixEnds(events))
	})

	t.Run("reused tool call ID stays ambiguous", func(t *testing.T) {
		toolCall := func(id string, timestamp time.Time) event.Event {
			return event.Event{
				ID:        id,
				Author:    "agent",
				Timestamp: timestamp,
				Response: &model.Response{Choices: []model.Choice{{
					Message: model.Message{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{{
							ID: "reused-call",
						}},
					},
				}}},
			}
		}
		toolResult := func(id string, timestamp time.Time) event.Event {
			return event.Event{
				ID:        id,
				Author:    "agent",
				Timestamp: timestamp,
				Response: &model.Response{Choices: []model.Choice{{
					Message: model.NewToolMessage(
						"reused-call", "lookup", "result",
					),
				}}},
			}
		}
		events := []event.Event{
			toolCall("call-event-1", now),
			toolResult("result-event-1", now.Add(time.Second)),
			toolCall("call-event-2", now.Add(2*time.Second)),
			toolResult("result-event-2", now.Add(3*time.Second)),
			newSummaryPrefixEvent(
				"assistant-event", "assistant-response", "agent",
				model.RoleAssistant, "later", now.Add(4*time.Second),
			),
		}

		require.Equal(t, []int{2}, safeSummaryPrefixEnds(events))
	})

	t.Run("unstable events are not cut points", func(t *testing.T) {
		missingID := newSummaryPrefixEvent(
			"", "assistant-response-1", "agent", model.RoleAssistant,
			"first", now,
		)
		missingTimestamp := newSummaryPrefixEvent(
			"assistant-event-2", "assistant-response-2", "agent",
			model.RoleAssistant, "second", time.Time{},
		)

		require.Empty(
			t,
			safeSummaryPrefixEnds([]event.Event{missingID, missingTimestamp}),
		)
	})

	t.Run("system event is not a cut point", func(t *testing.T) {
		evt := newSummaryPrefixEvent(
			"system-event", "system-response", "agent", model.RoleSystem,
			"instruction", now,
		)

		require.Empty(t, safeSummaryPrefixEnds([]event.Event{evt}))
	})
}

func TestSessionSummarizer_DoesNotPrefixHookRewrittenInput(t *testing.T) {
	capture := &cacheSafeCaptureModel{
		response:      "must not be called",
		contextWindow: 100_000,
	}
	events := newSummaryPrefixRounds(3, 48)
	hookCalls := 0
	s := NewSummarizer(
		capture,
		WithPrompt("Conversation:\n{conversation_text}\n\nSummary:"),
		WithPreSummaryHook(func(*PreSummaryHookContext) error {
			hookCalls++
			return nil
		}),
	).(*sessionSummarizer)
	twoRoundTokens := summaryPrefixRequestTokens(t, s, events[:4])
	allRoundTokens := summaryPrefixRequestTokens(t, s, events)
	capture.inputBudget = twoRoundTokens + (allRoundTokens-twoRoundTokens)/2
	sess := &session.Session{ID: "hook-session", Events: events}

	_, err := s.Summarize(context.Background(), sess)
	require.ErrorContains(t, err, "refusing to omit unsummarized conversation")
	require.Equal(t, 1, hookCalls)
	require.Nil(t, capture.request)
	assert.NotContains(t, sess.State, lastIncludedTsKey)
	assert.NotContains(t, sess.State, lastIncludedEventIDKey)
}

func TestSessionSummarizer_RejectsOversizedCompletePrefix(t *testing.T) {
	capture := &cacheSafeCaptureModel{
		response:      "must not be called",
		contextWindow: 100_000,
	}
	s := NewSummarizer(
		capture,
		WithPrompt("Conversation:\n{conversation_text}\n\nSummary:"),
	).(*sessionSummarizer)
	events := newSummaryPrefixRounds(2, 96)
	oneRoundTokens := summaryPrefixRequestTokens(t, s, events[:2])
	require.Greater(t, oneRoundTokens, 1)
	capture.inputBudget = oneRoundTokens - 1
	sess := &session.Session{ID: "oversized-prefix-session", Events: events}

	_, err := s.Summarize(context.Background(), sess)
	require.ErrorContains(t, err, "refusing to omit unsummarized conversation")
	require.Nil(t, capture.request)
	assert.NotContains(t, sess.State, lastIncludedTsKey)
	assert.NotContains(t, sess.State, lastIncludedEventIDKey)
}

func TestSessionSummarizer_RetryShrinksSafePrefix(t *testing.T) {
	code := "context_length_exceeded"
	capture := &retrySummaryModel{
		contextWindow: 100_000,
		responses: []*model.Response{
			{
				Done: true,
				Error: &model.ResponseError{
					Message: "maximum context length exceeded",
					Code:    &code,
				},
			},
			{
				Done: true,
				Choices: []model.Choice{{Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "smaller prefix summary",
				}}},
			},
		},
	}
	s := NewSummarizer(
		capture,
		WithPrompt("Conversation:\n{conversation_text}\n\nSummary:"),
	).(*sessionSummarizer)
	events := newSummaryPrefixRounds(3, 48)
	oneRoundTokens := summaryPrefixRequestTokens(t, s, events[:2])
	twoRoundTokens := summaryPrefixRequestTokens(t, s, events[:4])
	allRoundTokens := summaryPrefixRequestTokens(t, s, events)
	minimumBudget := max(twoRoundTokens, 2*oneRoundTokens)
	maximumBudget := min(allRoundTokens-1, 2*twoRoundTokens-1)
	require.LessOrEqual(t, minimumBudget, maximumBudget)
	capture.inputBudget = minimumBudget
	sess := &session.Session{ID: "retry-prefix-session", Events: events}

	text, err := s.Summarize(context.Background(), sess)
	require.NoError(t, err)
	require.Equal(t, "smaller prefix summary", text)
	require.Len(t, capture.requests, 2)
	require.Contains(t, capture.requests[0].Messages[0].Content,
		"assistant-marker-2")
	require.NotContains(t, capture.requests[0].Messages[0].Content,
		"assistant-marker-3")
	require.Contains(t, capture.requests[1].Messages[0].Content,
		"assistant-marker-1")
	require.NotContains(t, capture.requests[1].Messages[0].Content,
		"assistant-marker-2")
	require.Equal(
		t,
		"assistant-event-1",
		string(sess.State[lastIncludedEventIDKey]),
	)
}

func TestSessionSummarizer_SafePrefixRequiresStrictProgress(t *testing.T) {
	s := NewSummarizer(
		&cacheSafeCaptureModel{response: "summary", contextWindow: 100_000},
		WithPrompt("Conversation:\n{conversation_text}\n\nSummary:"),
	).(*sessionSummarizer)
	events := newSummaryPrefixRounds(1, 8)
	texts := s.extractConversationEventTexts(events)
	input := summaryPromptInput{conversationText: joinSummaryEventTexts(texts)}
	source := &summarySource{
		input:          input,
		boundaryEvents: events,
		prefixEvents:   events,
		prefixTexts:    texts,
		allowPrefix:    true,
	}
	budget := summaryPrefixRequestTokens(t, s, events)

	request, selected, err := s.buildSafeSummaryPrefixRequest(
		context.Background(),
		source,
		budget,
	)
	require.NoError(t, err)
	require.False(t, selected)
	require.Nil(t, request)
	require.Equal(t, input, source.input)
	require.Len(t, source.prefixEvents, len(events))
	require.Len(t, source.boundaryEvents, len(events))
}

func TestSessionSummarizer_CommitsBoundaryAfterPostHook(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	evt := newSummaryPrefixEvent(
		"event-1", "response-1", authorUser, model.RoleUser, "request", now,
	)

	t.Run("aborting hook leaves boundary unchanged", func(t *testing.T) {
		var hookBoundary string
		s := NewSummarizer(
			&cacheSafeCaptureModel{response: "summary", contextWindow: 1_000},
			WithPostSummaryHook(func(in *PostSummaryHookContext) error {
				hookBoundary = string(
					in.Session.State[lastIncludedEventIDKey],
				)
				return assert.AnError
			}),
			WithSummaryHookAbortOnError(true),
		)
		sess := &session.Session{ID: "post-hook-error", Events: []event.Event{evt}}
		sess.SetState(lastIncludedTsKey, []byte("previous-timestamp"))
		sess.SetState(lastIncludedEventIDKey, []byte("previous-event"))

		_, err := s.Summarize(context.Background(), sess)
		require.ErrorContains(t, err, "post-summary hook failed")
		require.Equal(t, "event-1", hookBoundary)
		require.Equal(
			t,
			"previous-timestamp",
			string(sess.State[lastIncludedTsKey]),
		)
		require.Equal(
			t,
			"previous-event",
			string(sess.State[lastIncludedEventIDKey]),
		)
	})

	t.Run("non-aborting hook commits successful summary", func(t *testing.T) {
		s := NewSummarizer(
			&cacheSafeCaptureModel{response: "summary", contextWindow: 1_000},
			WithPostSummaryHook(func(in *PostSummaryHookContext) error {
				in.Session.SetState(
					lastIncludedEventIDKey,
					[]byte("hook-boundary"),
				)
				return assert.AnError
			}),
			WithSummaryHookAbortOnError(false),
		)
		sess := &session.Session{ID: "post-hook-ignored", Events: []event.Event{evt}}

		text, err := s.Summarize(context.Background(), sess)
		require.NoError(t, err)
		require.Equal(t, "summary", text)
		require.Equal(t, "event-1", string(sess.State[lastIncludedEventIDKey]))
	})

	t.Run("panicking hook restores previous boundary", func(t *testing.T) {
		s := NewSummarizer(
			&cacheSafeCaptureModel{response: "summary", contextWindow: 1_000},
			WithPostSummaryHook(func(*PostSummaryHookContext) error {
				panic("hook panic")
			}),
		)
		sess := &session.Session{ID: "post-hook-panic", Events: []event.Event{evt}}
		sess.SetState(lastIncludedTsKey, []byte("previous-timestamp"))
		sess.SetState(lastIncludedEventIDKey, []byte("previous-event"))

		require.Panics(t, func() {
			_, _ = s.Summarize(context.Background(), sess)
		})
		require.Equal(
			t,
			"previous-timestamp",
			string(sess.State[lastIncludedTsKey]),
		)
		require.Equal(
			t,
			"previous-event",
			string(sess.State[lastIncludedEventIDKey]),
		)
	})
}

func newSummaryPrefixRounds(rounds int, repeatedWords int) []event.Event {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	events := make([]event.Event, 0, rounds*2)
	for i := 1; i <= rounds; i++ {
		events = append(
			events,
			newSummaryPrefixEvent(
				fmt.Sprintf("user-event-%d", i),
				fmt.Sprintf("user-response-%d", i),
				authorUser,
				model.RoleUser,
				fmt.Sprintf("user-marker-%d %s", i,
					strings.Repeat("request ", repeatedWords)),
				now.Add(time.Duration(len(events))*time.Second),
			),
			newSummaryPrefixEvent(
				fmt.Sprintf("assistant-event-%d", i),
				fmt.Sprintf("assistant-response-%d", i),
				"agent",
				model.RoleAssistant,
				fmt.Sprintf("assistant-marker-%d %s", i,
					strings.Repeat("response ", repeatedWords)),
				now.Add(time.Duration(len(events)+1)*time.Second),
			),
		)
	}
	return events
}

func newSummaryPrefixEvent(
	eventID string,
	responseID string,
	author string,
	role model.Role,
	content string,
	timestamp time.Time,
) event.Event {
	return event.Event{
		ID:           eventID,
		RequestID:    "request-1",
		InvocationID: "invocation-1",
		Author:       author,
		Timestamp:    timestamp,
		Response: &model.Response{
			ID: responseID,
			Choices: []model.Choice{{Message: model.Message{
				Role:    role,
				Content: content,
			}}},
		},
	}
}

func summaryPrefixRequestTokens(
	t *testing.T,
	s *sessionSummarizer,
	events []event.Event,
) int {
	t.Helper()
	request, err := s.buildStandaloneSummaryRequest(summaryPromptInput{
		conversationText: s.extractConversationText(events),
	})
	require.NoError(t, err)
	tokens, err := countSummaryRequestTokens(context.Background(), request)
	require.NoError(t, err)
	return tokens
}
