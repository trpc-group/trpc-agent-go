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
