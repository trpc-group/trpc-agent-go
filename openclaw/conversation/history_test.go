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
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/session"
	summarypkg "trpc.group/trpc-go/trpc-agent-go/session/summary"
)

func TestMergeRequestExtensionRoundTrip(t *testing.T) {
	extensions, err := MergeRequestExtension(nil, Annotation{
		HistoryMode:   HistoryModeShared,
		StorageUserID: "chat-scope",
		ActorID:       "u1",
		ActorLabel:    "Alice",
		ActorLabels:   map[string]string{"u2": "Bob"},
		QuoteText:     "hello",
	})
	require.NoError(t, err)

	annotation, ok, err := AnnotationFromRequestExtensions(
		extensions,
	)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, HistoryModeShared, annotation.HistoryMode)
	require.Equal(t, "chat-scope", annotation.StorageUserID)
	require.Equal(t, "u1", annotation.ActorID)
	require.Equal(t, "Alice", annotation.ActorLabel)
	require.Equal(
		t,
		map[string]string{"u2": "Bob"},
		annotation.ActorLabels,
	)
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

func TestAnnotationHelpers_EdgeCases(t *testing.T) {
	t.Parallel()

	base := map[string]json.RawMessage{
		"keep": json.RawMessage(`"value"`),
	}
	got, err := MergeRequestExtension(base, Annotation{})
	require.NoError(t, err)
	require.Equal(t, base, got)

	require.Nil(t, RuntimeState(Annotation{}))

	_, ok := AnnotationFromRuntimeState(nil)
	require.False(t, ok)

	_, ok = AnnotationFromRuntimeState(
		map[string]any{RuntimeStateKey: "bad"},
	)
	require.False(t, ok)

	_, ok = AnnotationFromRuntimeState(
		map[string]any{RuntimeStateKey: Annotation{}},
	)
	require.False(t, ok)

	runtimeOnly := Annotation{
		ActorLabels: map[string]string{"u1": "Alice"},
	}
	state := RuntimeState(runtimeOnly)
	require.NotNil(t, state)
	annotation, ok := AnnotationFromRuntimeState(state)
	require.True(t, ok)
	require.Equal(t, runtimeOnly.ActorLabels, annotation.ActorLabels)

	evtOnlyLabels := event.New("inv", authorUser)
	err = SetEventAnnotation(evtOnlyLabels, runtimeOnly)
	require.NoError(t, err)
	_, ok, err = AnnotationFromEvent(*evtOnlyLabels)
	require.NoError(t, err)
	require.False(t, ok)

	_, ok, err = AnnotationFromRequestExtensions(
		map[string]json.RawMessage{
			ExtensionKey: json.RawMessage("{"),
		},
	)
	require.Error(t, err)
	require.False(t, ok)

	evt := event.New("inv", authorUser)
	evt.Extensions = map[string]json.RawMessage{
		ExtensionKey: json.RawMessage("{"),
	}
	_, ok, err = AnnotationFromEvent(*evt)
	require.Error(t, err)
	require.False(t, ok)
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
		LabelOverrides: map[string]string{
			"u2": "Robert",
		},
	})
	require.Len(t, got, 3)
	require.Equal(t, model.RoleSystem, got[0].Role)
	require.Contains(t, got[0].Content, "older history")
	require.Equal(t, model.RoleUser, got[1].Role)
	require.Contains(t, got[1].Content, "Speaker: Robert")
	require.Contains(t, got[1].Content, "Message: latest question")
	require.Equal(t, model.RoleAssistant, got[2].Role)
	require.Equal(t, "latest answer", got[2].Content)
}

func TestProjectEventMessage(t *testing.T) {
	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.Message{
			Role: model.RoleUser,
			ContentParts: []model.ContentPart{
				{
					Type: model.ContentTypeText,
					Text: stringPointer("hello"),
				},
				{
					Type: model.ContentTypeFile,
					File: &model.File{Name: "a.txt"},
				},
			},
		}),
	)
	inv.RunOptions.RuntimeState = RuntimeState(Annotation{
		ActorID:    "u1",
		ActorLabel: "Alice",
		QuoteText:  "earlier",
	})

	got := ProjectEventMessage(
		inv,
		event.Event{},
		inv.Message,
	)
	require.Equal(
		t,
		"Speaker: Alice\nQuoted message: earlier\nMessage: hello",
		got.Content,
	)
	require.Len(t, got.ContentParts, 1)
	require.Equal(
		t,
		model.ContentTypeFile,
		got.ContentParts[0].Type,
	)
}

func TestProjectEventMessage_EdgeCases(t *testing.T) {
	t.Parallel()

	assistant := model.NewAssistantMessage("hi")
	require.Equal(
		t,
		assistant,
		ProjectEventMessage(nil, event.Event{}, assistant),
	)

	plainUser := model.NewUserMessage("hello")
	require.Equal(
		t,
		plainUser,
		ProjectEventMessage(nil, event.Event{}, plainUser),
	)

	emptyInv := agent.NewInvocation(
		agent.WithInvocationMessage(model.Message{
			Role: model.RoleUser,
		}),
	)
	emptyInv.RunOptions.RuntimeState = RuntimeState(Annotation{
		ActorID:    "u1",
		ActorLabel: "Alice",
	})
	require.Equal(
		t,
		emptyInv.Message,
		ProjectEventMessage(emptyInv, event.Event{}, emptyInv.Message),
	)

	overrideInv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hello")),
	)
	overrideInv.RunOptions.RuntimeState = RuntimeState(Annotation{
		ActorLabels: map[string]string{
			"u1": "Resolved Alice",
		},
	})
	got := ProjectEventMessage(
		overrideInv,
		userEventWithQuote(
			"u1",
			"Alice",
			"ignored history body",
			"earlier",
			time.Now(),
		),
		overrideInv.Message,
	)
	require.Equal(
		t,
		"Speaker: Resolved Alice\nQuoted message: earlier\n"+
			"Message: hello",
		got.Content,
	)
}

func TestProjectionHelpers(t *testing.T) {
	t.Parallel()

	require.Nil(t, runtimeState(nil))

	require.False(t, hasProjectionMetadata(Annotation{}))
	require.True(
		t,
		hasProjectionMetadata(Annotation{ActorLabel: "Alice"}),
	)

	require.True(t, isSyntheticProjectionEvent(event.Event{}))
	require.False(
		t,
		isSyntheticProjectionEvent(event.Event{RequestID: "req-1"}),
	)

	require.Nil(t, nonTextContentParts(nil))
	require.Nil(
		t,
		nonTextContentParts([]model.ContentPart{{
			Type: model.ContentTypeText,
			Text: stringPointer("hello"),
		}}),
	)

	kept := nonTextContentParts([]model.ContentPart{
		{
			Type: model.ContentTypeText,
			Text: stringPointer("hello"),
		},
		{
			Type: model.ContentTypeFile,
			File: &model.File{Name: "a.txt"},
		},
	})
	require.Len(t, kept, 1)
	require.Equal(t, model.ContentTypeFile, kept[0].Type)

	require.Empty(
		t,
		projectedUserContentText(model.Message{}, Annotation{}, nil),
	)
	require.Equal(
		t,
		"Speaker: user\nMessage: hello",
		projectedUserContentText(
			model.NewUserMessage("hello"),
			Annotation{},
			nil,
		),
	)
}

func TestProjectionMetadata(t *testing.T) {
	t.Parallel()

	invalid := event.Event{
		Extensions: map[string]json.RawMessage{
			ExtensionKey: json.RawMessage("{"),
		},
	}
	_, _, ok := projectionMetadata(nil, invalid)
	require.False(t, ok)

	_, _, ok = projectionMetadata(
		nil,
		event.Event{Author: authorUser},
	)
	require.False(t, ok)

	plainInv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hello")),
	)
	_, _, ok = projectionMetadata(plainInv, event.Event{})
	require.False(t, ok)

	runtimeInv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hello")),
	)
	runtimeInv.RunOptions.RuntimeState = RuntimeState(Annotation{
		ActorID:    "u1",
		ActorLabel: "Alice",
	})
	annotation, labels, ok := projectionMetadata(
		runtimeInv,
		event.Event{},
	)
	require.True(t, ok)
	require.Equal(t, "u1", annotation.ActorID)
	require.Equal(t, "Alice", annotation.ActorLabel)
	require.Nil(t, labels)

	persistedInv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hello")),
	)
	persistedInv.RunOptions.RuntimeState = RuntimeState(Annotation{
		ActorLabels: map[string]string{
			"u2": "Resolved Bob",
		},
	})
	annotation, labels, ok = projectionMetadata(
		persistedInv,
		userEvent("u2", "Bob", "hello", time.Now()),
	)
	require.True(t, ok)
	require.Equal(t, "u2", annotation.ActorID)
	require.Equal(
		t,
		map[string]string{"u2": "Resolved Bob"},
		labels,
	)
}

func TestHistoryHelpers_RenderAndFilter(t *testing.T) {
	t.Parallel()

	require.Nil(t, BuildInjectedContextMessages(nil, HistoryOptions{}))
	require.Nil(t, BuildTurns(nil, TurnOptions{}))
	require.Empty(t, BuildSummaryText(nil))

	msg := model.Message{
		ContentParts: []model.ContentPart{
			{
				Type: model.ContentTypeText,
				Text: stringPointer("first"),
			},
			{
				Type: model.ContentTypeText,
				Text: stringPointer("second"),
			},
		},
	}
	require.Equal(t, "first\nsecond", messageText(msg))

	fileName := "report.pdf"
	require.Equal(
		t,
		"sent 1 attachment",
		messageText(model.Message{
			ContentParts: []model.ContentPart{{
				Type: model.ContentTypeFile,
				File: &model.File{Name: fileName},
			}},
		}),
	)
	require.Equal(
		t,
		"sent 2 attachments",
		messageText(model.Message{
			ContentParts: []model.ContentPart{
				{
					Type: model.ContentTypeFile,
					File: &model.File{Name: "a.txt"},
				},
				{
					Type: model.ContentTypeFile,
					File: &model.File{Name: "b.txt"},
				},
			},
		}),
	)
	require.Empty(
		t,
		renderAssistantMessage(model.Message{
			ToolID: "tool-1",
		}),
	)
	require.Equal(
		t,
		"Speaker: Alice\nQuoted message: earlier\nMessage: hello",
		renderUserMessage(
			model.NewUserMessage("hello"),
			Annotation{
				ActorID:    "u1",
				ActorLabel: "Alice",
				QuoteText:  "earlier",
			},
			nil,
		),
	)

	require.Equal(t, authorUser, speakerLabel(Annotation{}, nil))
	require.Equal(
		t,
		"u-1",
		speakerLabel(Annotation{ActorID: "u-1"}, nil),
	)
	require.Equal(
		t,
		"Bob",
		speakerLabel(Annotation{
			ActorID:    "u-1",
			ActorLabel: "Bob",
		}, nil),
	)
	require.Equal(
		t,
		"Robert",
		speakerLabel(
			Annotation{
				ActorID:    "u-1",
				ActorLabel: "Bob",
			},
			map[string]string{"u-1": "Robert"},
		),
	)

	require.Equal(
		t,
		"user: hello",
		formatTurn(Turn{
			Role: string(model.RoleUser),
			Text: "hello",
		}),
	)
	require.Empty(t, formatTurn(Turn{}))

	invalidUser := event.NewResponseEvent(
		"inv",
		authorUser,
		&model.Response{
			Choices: []model.Choice{{
				Message: model.NewUserMessage("hello"),
			}},
		},
	)
	invalidUser.Extensions = map[string]json.RawMessage{
		ExtensionKey: json.RawMessage("{"),
	}
	require.Nil(t, visibleUserMessages(*invalidUser, nil))

	assistantToolOnly := event.Event{
		Author: authorAssistant,
		Response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role:      model.RoleAssistant,
					ToolCalls: []model.ToolCall{{ID: "call-1"}},
				},
			}},
		},
	}
	require.Empty(t, visibleAssistantMessages(assistantToolOnly))

	require.Empty(
		t,
		visibleSystemMessages(systemEvent("   ")),
	)
	require.Nil(
		t,
		turnsFromEvent(event.Event{}, true, nil),
	)
	require.Nil(
		t,
		buildSummaryLines([]event.Event{
			assistantEvent("hi", time.Now()),
			systemEvent("sum"),
		}),
	)

	base := time.Now()
	require.False(
		t,
		includeEvent(event.Event{
			Response: &model.Response{
				IsPartial: true,
				Choices:   []model.Choice{{}},
			},
		}, time.Time{}),
	)
	require.False(
		t,
		includeEvent(assistantEvent("old", base), base),
	)

	sess := &session.Session{
		Summaries: map[string]*session.Summary{
			session.SummaryFilterKeyAllContents: {
				Summary:   "summary",
				UpdatedAt: base,
			},
		},
	}
	text, updatedAt, ok := sessionSummary(sess)
	require.True(t, ok)
	require.Equal(t, "summary", text)
	require.Equal(t, base, updatedAt)
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

func TestBuildTurns(t *testing.T) {
	base := time.Now()
	sess := &session.Session{
		Events: []event.Event{
			systemEvent("previous summary"),
			userEventWithQuote(
				"u1",
				"Alice",
				"what changed",
				"the earlier topic",
				base,
			),
			assistantEvent("here is the update", base.Add(time.Second)),
		},
	}

	got := BuildTurns(sess, TurnOptions{
		Limit:         10,
		IncludeSystem: false,
		LabelOverrides: map[string]string{
			"u1": "alice.dev",
		},
	})
	require.Len(t, got, 2)
	require.Equal(t, string(model.RoleUser), got[0].Role)
	require.Equal(t, "alice.dev", got[0].Speaker)
	require.Equal(t, "u1", got[0].ActorID)
	require.Equal(t, "the earlier topic", got[0].QuoteText)
	require.Equal(t, "what changed", got[0].Text)
	require.Equal(t, string(model.RoleAssistant), got[1].Role)
	require.Equal(t, summarySpeakerAssistant, got[1].Speaker)
	require.Equal(t, "here is the update", got[1].Text)
}

func TestFormatTurns(t *testing.T) {
	text := FormatTurns([]Turn{
		{
			Role:      string(model.RoleUser),
			Speaker:   "Alice",
			QuoteText: "earlier",
			Text:      "what changed",
		},
		{
			Role:    string(model.RoleAssistant),
			Speaker: summarySpeakerAssistant,
			Text:    "here is the update",
		},
	})

	require.Contains(
		t,
		text,
		"1. Alice (replying to: earlier): what changed",
	)
	require.Contains(
		t,
		text,
		"2. Assistant: here is the update",
	)
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
	require.NotContains(t, ctx.Text, "fallback")
}

func TestPluginPersistsRuntimeAnnotation(t *testing.T) {
	t.Parallel()

	mgr, err := plugin.NewManager(Plugin{})
	require.NoError(t, err)

	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hello")),
	)
	inv.RunOptions.RuntimeState = RuntimeState(Annotation{
		ActorID:    "u-1",
		ActorLabel: "Alice",
		QuoteText:  "earlier",
	})
	evt := event.NewResponseEvent(
		"inv",
		authorUser,
		&model.Response{
			Choices: []model.Choice{{
				Message: model.NewUserMessage("hello"),
			}},
		},
	)
	out, err := mgr.OnEvent(context.Background(), inv, evt)
	require.NoError(t, err)

	annotation, ok, err := AnnotationFromEvent(*out)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "Alice", annotation.ActorLabel)
	require.Equal(t, "earlier", annotation.QuoteText)
}

func TestPluginSkipsUnsupportedEvents(t *testing.T) {
	t.Parallel()

	mgr, err := plugin.NewManager(Plugin{})
	require.NoError(t, err)

	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hello")),
	)
	assistant := event.NewResponseEvent(
		"inv",
		authorAssistant,
		&model.Response{
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("hi"),
			}},
		},
	)
	out, err := mgr.OnEvent(context.Background(), inv, assistant)
	require.NoError(t, err)
	require.Same(t, assistant, out)

	user := event.NewResponseEvent(
		"inv",
		authorUser,
		&model.Response{
			Choices: []model.Choice{{
				Message: model.NewUserMessage("hello"),
			}},
		},
	)
	out, err = mgr.OnEvent(context.Background(), nil, user)
	require.NoError(t, err)
	require.Same(t, user, out)
}

func TestPreSummaryHook_NoProjectionLeavesText(t *testing.T) {
	t.Parallel()

	ctx := &summarypkg.PreSummaryHookContext{
		Events: []event.Event{
			assistantEvent("hi", time.Now()),
		},
		Text: "fallback",
	}
	require.NoError(t, PreSummaryHook(ctx))
	require.Equal(t, "fallback", ctx.Text)

	require.NoError(t, PreSummaryHook(nil))
}

func TestHistoryHelpers_SystemTurnsAndSummary(t *testing.T) {
	t.Parallel()

	sysEvt := systemEvent("carry forward")
	turns := systemTurnsFromEvent(sysEvt)
	require.Len(t, turns, 1)
	require.Equal(t, string(model.RoleSystem), turns[0].Role)
	require.Equal(t, summarySpeakerSystem, turns[0].Speaker)
	require.Equal(t, "carry forward", turns[0].Text)

	lines, ok := summaryLinesFromEvent(sysEvt, nil)
	require.False(t, ok)
	require.Equal(
		t,
		[]string{summarySpeakerSystem + ": carry forward"},
		lines,
	)

	msg := model.Message{
		ContentParts: []model.ContentPart{
			{
				Type: model.ContentTypeText,
				Text: stringPointer("hello"),
			},
			{
				Type: model.ContentTypeFile,
				File: &model.File{Name: "a.txt"},
			},
		},
	}
	require.Equal(t, "hello", messageText(msg))

	text, ts, ok := sessionSummary(&session.Session{})
	require.False(t, ok)
	require.Empty(t, text)
	require.True(t, ts.IsZero())

	sess := &session.Session{
		Summaries: map[string]*session.Summary{
			session.SummaryFilterKeyAllContents: {
				Summary: "  kept  ",
			},
		},
	}
	text, _, ok = sessionSummary(sess)
	require.True(t, ok)
	require.Equal(t, "kept", text)
}

func stringPointer(v string) *string {
	return &v
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
