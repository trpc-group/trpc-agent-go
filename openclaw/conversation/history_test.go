//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package conversation

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	summarypkg "trpc.group/trpc-go/trpc-agent-go/session/summary"
)

func TestMergeRequestExtensionRoundTrip(t *testing.T) {
	extensions, err := MergeRequestExtension(nil, Annotation{
		HistoryMode: HistoryModeShared,
		ActorID:     "u1",
		ActorLabel:  "Alice",
		QuoteText:   "hello",
	})
	require.NoError(t, err)

	annotation, ok, err := AnnotationFromRequestExtensions(
		extensions,
	)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, HistoryModeShared, annotation.HistoryMode)
	require.Equal(t, "u1", annotation.ActorID)
	require.Equal(t, "Alice", annotation.ActorLabel)
	require.Equal(t, "hello", annotation.QuoteText)
}

func TestSetEventAnnotationRoundTrip(t *testing.T) {
	evt := event.New("inv", "user")
	err := SetEventAnnotation(evt, Annotation{
		ActorID:    "u2",
		ActorLabel: "Bob",
		QuoteText:  "earlier",
	})
	require.NoError(t, err)

	annotation, ok, err := AnnotationFromEvent(*evt)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "u2", annotation.ActorID)
	require.Equal(t, "Bob", annotation.ActorLabel)
	require.Equal(t, "earlier", annotation.QuoteText)
}

func TestBuildInjectedContextMessages(t *testing.T) {
	base := time.Now().Add(-10 * time.Minute)
	sess := &session.Session{
		Summaries: map[string]*session.Summary{
			session.SummaryFilterKeyAllContents: {
				Summary:   "older history",
				UpdatedAt: base,
			},
		},
		Events: []event.Event{
			userEvent(
				"u1",
				"Alice",
				"first",
				base.Add(-time.Minute),
			),
			userEvent(
				"u2",
				"Bob",
				"latest question",
				base.Add(time.Minute),
			),
			assistantEvent(
				"latest answer",
				base.Add(2*time.Minute),
			),
		},
	}

	got := BuildInjectedContextMessages(sess, HistoryOptions{
		AddSessionSummary: true,
		MaxHistoryRuns:    10,
	})
	require.Len(t, got, 3)
	require.Equal(t, model.RoleSystem, got[0].Role)
	require.Contains(t, got[0].Content, "older history")
	require.Equal(t, model.RoleUser, got[1].Role)
	require.Contains(t, got[1].Content, "Speaker: Bob")
	require.Contains(t, got[1].Content, "Message: latest question")
	require.Equal(t, model.RoleAssistant, got[2].Role)
	require.Equal(t, "latest answer", got[2].Content)
}

func TestBuildSummaryText(t *testing.T) {
	events := []event.Event{
		systemEvent("previous summary"),
		userEventWithQuote(
			"u1",
			"Alice",
			"what changed",
			"the earlier topic",
			time.Now(),
		),
		assistantEvent("here is the update", time.Now()),
	}

	got := BuildSummaryText(events)
	require.Contains(t, got, "Previous summary: previous summary")
	require.Contains(
		t,
		got,
		"Alice (replying to: the earlier topic): what changed",
	)
	require.Contains(t, got, "Assistant: here is the update")
}

func TestPreSummaryHookUsesConversationProjection(t *testing.T) {
	events := []event.Event{
		userEvent("u1", "Alice", "hello", time.Now()),
		assistantEvent("hi", time.Now()),
	}

	ctx := &summarypkg.PreSummaryHookContext{
		Events: events,
		Text:   "fallback",
	}
	require.NoError(t, PreSummaryHook(ctx))
	require.Contains(t, ctx.Text, "Alice: hello")
	require.Contains(t, ctx.Text, "Assistant: hi")
}

func userEvent(
	actorID string,
	actorLabel string,
	content string,
	ts time.Time,
) event.Event {
	return userEventWithQuote(
		actorID,
		actorLabel,
		content,
		"",
		ts,
	)
}

func userEventWithQuote(
	actorID string,
	actorLabel string,
	content string,
	quote string,
	ts time.Time,
) event.Event {
	evt := event.NewResponseEvent("inv", authorUser, &model.Response{
		Choices: []model.Choice{{
			Message: model.NewUserMessage(content),
		}},
	})
	evt.Timestamp = ts
	_ = SetEventAnnotation(evt, Annotation{
		ActorID:    actorID,
		ActorLabel: actorLabel,
		QuoteText:  quote,
	})
	return *evt
}

func assistantEvent(content string, ts time.Time) event.Event {
	evt := event.NewResponseEvent(
		"inv",
		authorAssistant,
		&model.Response{
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage(content),
			}},
		},
	)
	evt.Timestamp = ts
	return *evt
}

func systemEvent(content string) event.Event {
	return event.Event{
		Author: authorSystem,
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.NewSystemMessage(content),
			}},
		},
	}
}
