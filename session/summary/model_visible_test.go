//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summary

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/summaryview"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	isummarycontext "trpc.group/trpc-go/trpc-agent-go/session/internal/summarycontext"
	isummaryscope "trpc.group/trpc-go/trpc-agent-go/session/internal/summaryscope"
)

func TestTokenThresholdUsesModelVisibleRequestTokens(t *testing.T) {
	sess := &session.Session{
		ID: "session",
		Events: []event.Event{
			modelVisibleTestEvent(
				"raw",
				"user",
				model.NewUserMessage(strings.Repeat("raw ", 1000)),
				time.Now(),
			),
		},
	}
	ctx := summaryview.ContextWithView(context.Background(), &summaryview.View{
		SessionID:     sess.ID,
		RequestTokens: 130_000,
		Items: []summaryview.Item{{
			EffectiveEvent: modelVisibleTestEvent(
				"raw",
				"user",
				model.NewUserMessage("projected"),
				time.Now(),
			),
		}},
	})

	check := evaluateTokenThreshold(129_999)(ctx, sess)
	require.True(t, check.Passed)
	require.Equal(t, 130_000, check.Value)

	check = evaluateTokenThreshold(130_000)(ctx, sess)
	require.False(t, check.Passed)
	require.Equal(t, 130_000, check.Value)
}

func TestSummaryDoesNotAdvanceWithoutStoredBoundary(t *testing.T) {
	sess := &session.Session{
		ID: "session",
		Events: []event.Event{
			modelVisibleTestEvent(
				"raw-event",
				"user",
				model.NewUserMessage("raw history"),
				time.Now(),
			),
		},
	}
	ctx := summaryview.ContextWithView(context.Background(), &summaryview.View{
		SessionID:     sess.ID,
		RequestTokens: 100_000,
		Items: []summaryview.Item{{
			EffectiveEvent: modelVisibleTestEvent(
				"",
				"user",
				model.NewUserMessage("unpersisted request"),
				time.Now(),
			),
		}},
	})
	summarizer := NewSummarizer(
		&fakeModel{},
		WithTokenThreshold(1),
	)

	require.False(t, summarizer.(ContextAwareSummarizer).ShouldSummarizeWithContext(
		ctx,
		sess,
	))
}

func TestPreSummaryHookSeparatesModelVisibleAndSourceEvents(t *testing.T) {
	now := time.Now()
	raw := modelVisibleTestEvent(
		"event-1",
		"tool",
		model.NewToolMessage(
			"call-1",
			"lookup",
			"raw tool payload",
		),
		now,
	)
	effectiveMessage := model.NewToolMessage(
		"call-1",
		"lookup",
		"projected tool result",
	)
	effective := modelVisibleTestEvent(
		raw.ID,
		raw.Author,
		effectiveMessage,
		now,
	)
	ctx := summaryview.ContextWithView(context.Background(), &summaryview.View{
		SessionID: "session",
		Bound:     true,
		Items: []summaryview.Item{{
			Message:        effectiveMessage,
			EffectiveEvent: effective,
			Boundary: summaryview.Boundary{
				EventID:   raw.ID,
				Timestamp: raw.Timestamp,
			},
		}},
	})
	mdl := &echoPromptModel{}
	summarizer := NewSummarizer(
		mdl,
		WithPreSummaryHook(func(in *PreSummaryHookContext) error {
			require.Len(t, in.Events, 1)
			require.Equal(
				t,
				"projected tool result",
				in.Events[0].Response.Choices[0].Message.Content,
			)
			require.Len(t, in.SourceEvents, 1)
			require.Equal(
				t,
				"raw tool payload",
				in.SourceEvents[0].Response.Choices[0].Message.Content,
			)
			// Rebuild from Events to exercise the hook fallback path.
			in.Text = ""
			return nil
		}),
	)
	sess := &session.Session{ID: "session", Events: []event.Event{raw}}

	result, err := summarizer.Summarize(ctx, sess)
	require.NoError(t, err)
	require.Contains(t, result, "projected tool result")
	require.NotContains(t, result, "raw tool payload")
}

func TestSummarizeUsesModelVisiblePrefix(t *testing.T) {
	baseTime := time.Now().Add(-time.Minute)
	rawEvents := []event.Event{
		modelVisibleTestEvent(
			"event-1",
			"user",
			model.NewUserMessage("original goal"),
			baseTime,
		),
		modelVisibleTestEvent(
			"event-2",
			"tool",
			model.NewToolMessage(
				"call-1",
				"lookup",
				strings.Repeat("raw tool payload ", 1000),
			),
			baseTime.Add(time.Second),
		),
		modelVisibleTestEvent(
			"event-3",
			"user",
			model.NewUserMessage("recent request"),
			baseTime.Add(2*time.Second),
		),
	}
	effectiveMessages := []model.Message{
		model.NewUserMessage("original goal"),
		model.NewToolMessage(
			"call-1",
			"lookup",
			"tool result omitted from model-visible history",
		),
		model.NewUserMessage("recent request"),
	}
	items := make([]summaryview.Item, len(effectiveMessages))
	for i := range effectiveMessages {
		items[i] = summaryview.Item{
			Message: effectiveMessages[i],
			EffectiveEvent: modelVisibleTestEvent(
				rawEvents[i].ID,
				rawEvents[i].Author,
				effectiveMessages[i],
				rawEvents[i].Timestamp,
			),
			Boundary: summaryview.Boundary{
				EventID:   rawEvents[i].ID,
				Timestamp: rawEvents[i].Timestamp,
			},
			RequestIndex: i + 1,
		}
	}
	view := &summaryview.View{
		SessionID: "session",
		Items:     items,
		Bound:     true,
	}
	parent := &model.Request{Messages: []model.Message{
		model.NewSystemMessage("stable system prompt"),
		effectiveMessages[0],
		effectiveMessages[1],
		effectiveMessages[2],
		model.NewAssistantMessage("response appended after the request"),
	}}

	var skipInput []event.Event
	capture := &cacheSafeCaptureModel{response: "summary"}
	summarizer := NewSummarizer(
		capture,
		WithCacheSafeForking(true),
		WithSkipRecent(func(events []event.Event) int {
			skipInput = events
			return 1
		}),
	)
	sess := &session.Session{ID: "session", Events: rawEvents}
	ctx := summaryview.ContextWithView(context.Background(), view)
	ctx = ContextWithCacheSafeForkRequest(ctx, parent)

	result, err := summarizer.Summarize(ctx, sess)
	require.NoError(t, err)
	require.Equal(t, "summary", result)
	require.Len(t, skipInput, 3)
	require.Equal(
		t,
		"tool result omitted from model-visible history",
		skipInput[1].Response.Choices[0].Message.Content,
	)
	require.NotContains(
		t,
		skipInput[1].Response.Choices[0].Message.Content,
		"raw tool payload",
	)

	require.NotNil(t, capture.request)
	require.Len(t, capture.request.Messages, 4)
	require.Equal(t, "stable system prompt", capture.request.Messages[0].Content)
	require.Equal(t, "original goal", capture.request.Messages[1].Content)
	require.Equal(
		t,
		"tool result omitted from model-visible history",
		capture.request.Messages[2].Content,
	)
	require.NotContains(t, capture.request.Messages[3].Content, "recent request")
	require.NotContains(
		t,
		capture.request.Messages[3].Content,
		"response appended after the request",
	)

	lastEventID, ok := sess.GetState(lastIncludedEventIDKey)
	require.True(t, ok)
	require.Equal(t, "event-2", string(lastEventID))
}

func TestSummarizeScopesModelVisibleItemsBeforeSkipping(t *testing.T) {
	now := time.Now()
	root := modelVisibleTestEvent(
		"event-root",
		"user",
		model.NewUserMessage("ancestor context"),
		now,
	)
	root.FilterKey = "app"
	branch := modelVisibleTestEvent(
		"event-branch",
		"user",
		model.NewUserMessage("branch goal"),
		now.Add(time.Second),
	)
	branch.FilterKey = "app/sub"
	recent := modelVisibleTestEvent(
		"event-recent",
		"assistant",
		model.NewAssistantMessage("branch recent"),
		now.Add(2*time.Second),
	)
	recent.FilterKey = "app/sub"
	events := []event.Event{root, branch, recent}
	items := make([]summaryview.Item, len(events))
	for i := range events {
		items[i] = summaryview.Item{
			Message:        events[i].Response.Choices[0].Message,
			EffectiveEvent: events[i],
			Boundary: summaryview.Boundary{
				EventID:   events[i].ID,
				Timestamp: events[i].Timestamp,
			},
			RequestIndex: i + 1,
		}
	}
	view := &summaryview.View{
		SessionID: "session",
		FilterKey: "app/sub",
		Items:     items,
		Bound:     true,
	}
	parent := &model.Request{Messages: []model.Message{
		model.NewSystemMessage("stable"),
		items[0].Message,
		items[1].Message,
		items[2].Message,
	}}
	sess := &session.Session{ID: "session", Events: events}
	isummaryscope.SetScopeFilterKey(sess, "app/sub")
	var skipInput []event.Event
	capture := &cacheSafeCaptureModel{response: "summary"}
	summarizer := NewSummarizer(
		capture,
		WithCacheSafeForking(true),
		WithSkipRecent(func(events []event.Event) int {
			skipInput = events
			return 1
		}),
	)
	ctx := summaryview.ContextWithView(context.Background(), view)
	ctx = ContextWithCacheSafeForkRequest(ctx, parent)

	_, err := summarizer.Summarize(ctx, sess)
	require.NoError(t, err)
	require.Len(t, skipInput, 2)
	require.Equal(t, "branch goal", skipInput[0].Response.Choices[0].Message.Content)
	require.Equal(t, "branch recent", skipInput[1].Response.Choices[0].Message.Content)
	require.Len(t, capture.request.Messages, 3)
	require.Equal(t, "stable", capture.request.Messages[0].Content)
	require.Equal(t, "branch goal", capture.request.Messages[1].Content)
	require.NotContains(t, capture.request.Messages[2].Content, "ancestor context")
	require.NotContains(t, capture.request.Messages[2].Content, "branch recent")

	lastEventID, ok := sess.GetState(lastIncludedEventIDKey)
	require.True(t, ok)
	require.Equal(t, "event-branch", string(lastEventID))
}

func TestModelVisibleViewMatchesSummaryScope(t *testing.T) {
	view := &summaryview.View{
		SessionID:       "session",
		FilterKey:       "branch",
		PreviousSummary: "previous",
	}
	ctx := summaryview.ContextWithView(context.Background(), view)

	sess := &session.Session{ID: "session"}
	_, ok := modelVisibleViewForSession(ctx, sess)
	require.False(t, ok)

	isummaryscope.SetScopeFilterKey(sess, "branch")
	_, ok = modelVisibleViewForSession(
		isummarycontext.WithPreviousSummary(ctx, "different"),
		sess,
	)
	require.False(t, ok)

	other := &session.Session{ID: "other"}
	isummaryscope.SetScopeFilterKey(other, "branch")
	_, ok = modelVisibleViewForSession(ctx, other)
	require.False(t, ok)

	composite := &session.Session{ID: "session:branch"}
	isummaryscope.SetScopeFilterKey(composite, "branch")
	matched, ok := modelVisibleViewForSession(
		isummarycontext.WithPreviousSummary(ctx, "previous"),
		composite,
	)
	require.True(t, ok)
	require.Equal(t, "session", matched.SessionID)
}

func TestModelVisibleItemContextIsIsolated(t *testing.T) {
	ctx := contextWithModelVisibleItems(nil, []int{1, 3})
	indexes, ok := modelVisibleItemsFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, []int{1, 3}, indexes)

	indexes[0] = 99
	again, ok := modelVisibleItemsFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, []int{1, 3}, again)

	_, ok = modelVisibleItemsFromContext(nil)
	require.False(t, ok)
	_, ok = modelVisibleItemsFromContext(context.Background())
	require.False(t, ok)
	_, ok = modelVisibleItemsFromContext(contextWithModelVisibleItems(
		context.Background(),
		nil,
	))
	require.False(t, ok)
	_, ok = modelVisibleItemsFromContext(context.WithValue(
		context.Background(),
		modelVisibleItemsContextKey{},
		"invalid",
	))
	require.False(t, ok)
}

func TestSelectSummaryEventsPrependsPreviousSummary(t *testing.T) {
	now := time.Now()
	raw := modelVisibleTestEvent(
		"event-1",
		"user",
		model.NewUserMessage("visible"),
		now,
	)
	view := &summaryview.View{
		SessionID:       "session",
		PreviousSummary: "previous summary",
		Items: []summaryview.Item{{
			Message:        raw.Response.Choices[0].Message,
			EffectiveEvent: raw,
			Boundary: summaryview.Boundary{
				EventID:   raw.ID,
				Timestamp: raw.Timestamp,
			},
		}},
	}
	sess := &session.Session{ID: "session", Events: []event.Event{raw}}
	selection := (&sessionSummarizer{}).selectSummaryEvents(
		summaryview.ContextWithView(context.Background(), view),
		sess,
	)

	require.True(t, selection.effective)
	require.Len(t, selection.events, 2)
	require.Equal(t, model.RoleSystem, selection.events[0].Response.Choices[0].Message.Role)
	require.Equal(
		t,
		"previous summary",
		selection.events[0].Response.Choices[0].Message.Content,
	)
	require.Equal(t, []int{0}, selection.itemIndexes)
	require.Equal(t, "event-1", selection.boundary.EventID)
	require.Equal(t, []event.Event{raw}, selection.sourceEvents)
}

func TestSourceEventsThroughBoundaryFallbacks(t *testing.T) {
	baseTime := time.Now()
	events := []event.Event{
		{ID: "first", Timestamp: baseTime},
		{ID: "second", Timestamp: baseTime.Add(time.Second)},
		{ID: "third", Timestamp: baseTime.Add(2 * time.Second)},
	}

	require.Nil(t, sourceEventsThroughBoundary(events, summaryview.Boundary{}))
	require.Equal(t, events[:2], sourceEventsThroughBoundary(
		events,
		summaryview.Boundary{EventID: "second"},
	))
	require.Nil(t, sourceEventsThroughBoundary(
		events,
		summaryview.Boundary{EventID: "missing"},
	))
	require.Equal(t, events[:2], sourceEventsThroughBoundary(
		events,
		summaryview.Boundary{
			EventID:   "missing",
			Timestamp: baseTime.Add(time.Second),
		},
	))
	require.Equal(t, events, sourceEventsThroughBoundary(
		events,
		summaryview.Boundary{Timestamp: baseTime.Add(3 * time.Second)},
	))
}

func TestSummaryBoundaryAndEffectiveCheckSession(t *testing.T) {
	now := time.Now()
	summarizer := &sessionSummarizer{}
	summarizer.recordIncludedBoundary(nil, summaryview.Boundary{Timestamp: now})

	sess := &session.Session{ID: "session"}
	sess.SetState(lastIncludedEventIDKey, []byte("old"))
	summarizer.recordIncludedBoundary(sess, summaryview.Boundary{})
	lastEventID, ok := sess.GetState(lastIncludedEventIDKey)
	require.True(t, ok)
	require.Equal(t, "old", string(lastEventID))

	summarizer.recordIncludedBoundary(sess, summaryview.Boundary{Timestamp: now})
	_, ok = sess.GetState(lastIncludedEventIDKey)
	require.False(t, ok)
	lastTimestamp, ok := sess.GetState(lastIncludedTsKey)
	require.True(t, ok)
	require.Equal(t, now.UTC().Format(time.RFC3339Nano), string(lastTimestamp))

	summarizer.recordIncludedBoundary(sess, summaryview.Boundary{
		EventID:   "event-1",
		Timestamp: now,
	})
	lastEventID, ok = sess.GetState(lastIncludedEventIDKey)
	require.True(t, ok)
	require.Equal(t, "event-1", string(lastEventID))

	effective := modelVisibleTestEvent(
		"event-1",
		"user",
		model.NewUserMessage("effective"),
		now,
	)
	check := summarizer.buildCheckSessionWithSelection(
		sess,
		summaryEventSelection{
			events:    []event.Event{effective},
			effective: true,
		},
	)
	require.Len(t, check.Events, 1)
	require.Equal(
		t,
		"effective",
		check.Events[0].Response.Choices[0].Message.Content,
	)
}

func modelVisibleTestEvent(
	id string,
	author string,
	message model.Message,
	timestamp time.Time,
) event.Event {
	return event.Event{
		ID:        id,
		Author:    author,
		Timestamp: timestamp,
		Response: &model.Response{Choices: []model.Choice{{
			Message: message,
		}}},
	}
}
