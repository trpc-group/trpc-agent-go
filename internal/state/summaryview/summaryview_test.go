//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summaryview

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestFinalizeBindsModelVisiblePrefix(t *testing.T) {
	timestamp := time.Now()
	invocation := agent.NewInvocation()
	AttachProjection(invocation, &View{
		SessionID:            "session",
		ContentRequestLength: 3,
		Items: []Item{
			{
				Message:      model.NewUserMessage("old"),
				Boundary:     Boundary{EventID: "event-1", Timestamp: timestamp},
				RequestIndex: 1,
			},
			{
				Message:      model.NewAssistantMessage("answer"),
				Boundary:     Boundary{EventID: "event-2", Timestamp: timestamp.Add(time.Second)},
				RequestIndex: 2,
			},
		},
	})
	request := &model.Request{Messages: []model.Message{
		model.NewSystemMessage("inserted"),
		model.NewSystemMessage("stable"),
		model.NewUserMessage("old"),
		model.NewAssistantMessage("answer"),
	}}

	Finalize(invocation, request, 41_959)
	view, ok := Snapshot(invocation)
	require.True(t, ok)
	require.True(t, view.Bound)
	require.Equal(t, 41_959, view.RequestTokens)
	require.Equal(t, 2, view.Items[0].RequestIndex)
	require.Equal(t, 3, view.Items[1].RequestIndex)

	prefix, ok := view.PrefixMessages(request.Messages, 1)
	require.True(t, ok)
	require.Equal(t, request.Messages[:3], prefix)
	boundary, ok := view.PrefixBoundary(1)
	require.True(t, ok)
	require.Equal(t, "event-1", boundary.EventID)

}

func TestSnapshotIsIsolated(t *testing.T) {
	invocation := agent.NewInvocation()
	AttachProjection(invocation, &View{
		Items: []Item{{
			Message: model.NewUserMessage("visible"),
			EffectiveEvent: event.Event{
				Response: &model.Response{Choices: []model.Choice{{
					Message: model.NewUserMessage("visible"),
				}}},
			},
		}},
	})

	first, ok := Snapshot(invocation)
	require.True(t, ok)
	first.Items[0].Message.Content = "mutated"
	first.Items[0].EffectiveEvent.Response.Choices[0].Message.Content = "mutated"

	second, ok := Snapshot(invocation)
	require.True(t, ok)
	require.Equal(t, "visible", second.Items[0].Message.Content)
	require.Equal(
		t,
		"visible",
		second.Items[0].EffectiveEvent.Response.Choices[0].Message.Content,
	)
}

func TestInvocationViewFinalizationIsIsolated(t *testing.T) {
	invocation := agent.NewInvocation()
	AttachProjection(invocation, &View{
		ContentRequestLength: 1,
		Items: []Item{{
			Message:      model.NewUserMessage("visible"),
			RequestIndex: 0,
		}},
	})

	view := invocation.View()
	Finalize(view, &model.Request{Messages: []model.Message{
		model.NewUserMessage("visible"),
	}}, 42)

	viewSnapshot, ok := Snapshot(view)
	require.True(t, ok)
	require.True(t, viewSnapshot.Bound)
	require.Equal(t, 42, viewSnapshot.RequestTokens)

	originalSnapshot, ok := Snapshot(invocation)
	require.True(t, ok)
	require.False(t, originalSnapshot.Bound)
	require.Zero(t, originalSnapshot.RequestTokens)
}

func TestContextAndInvocationLifecycle(t *testing.T) {
	invocation := agent.NewInvocation()
	_, ok := Snapshot(invocation)
	require.False(t, ok)
	Finalize(invocation, &model.Request{}, 1)
	Clear(nil)
	AttachProjection(nil, &View{})
	AttachProjection(invocation, nil)

	view := &View{
		SessionID: "session",
		Items: []Item{{
			Message: model.NewUserMessage("visible"),
		}},
	}
	AttachProjection(invocation, view)
	view.Items[0].Message.Content = "mutated source"
	stored, ok := Snapshot(invocation)
	require.True(t, ok)
	require.Equal(t, "visible", stored.Items[0].Message.Content)

	ctx := ContextWithView(nil, stored)
	fromContext, ok := FromContext(ctx)
	require.True(t, ok)
	fromContext.Items[0].Message.Content = "mutated context copy"
	again, ok := FromContext(ctx)
	require.True(t, ok)
	require.Equal(t, "visible", again.Items[0].Message.Content)
	require.Same(t, ctx, ContextWithView(ctx, nil))
	_, ok = FromContext(nil)
	require.False(t, ok)
	_, ok = FromContext(context.Background())
	require.False(t, ok)

	Clear(invocation)
	_, ok = Snapshot(invocation)
	require.False(t, ok)
}

func TestViewSelectsNonContiguousItems(t *testing.T) {
	now := time.Now()
	view := &View{
		Bound: true,
		Items: []Item{
			{
				RequestIndex: 1,
				Boundary:     Boundary{EventID: "first", Timestamp: now},
			},
			{
				RequestIndex: 2,
				Boundary:     Boundary{},
			},
			{
				RequestIndex: 3,
				Boundary: Boundary{
					EventID:   "third",
					Timestamp: now.Add(time.Second),
				},
			},
		},
	}
	parent := []model.Message{
		model.NewSystemMessage("fixed"),
		model.NewUserMessage("first"),
		model.NewAssistantMessage("excluded"),
		model.NewUserMessage("third"),
	}

	messages, ok := view.MessagesForItems(parent, []int{0, 2})
	require.True(t, ok)
	require.Equal(t, []model.Message{parent[0], parent[1], parent[3]}, messages)
	boundary, ok := view.BoundaryForItems([]int{0, 1, 2})
	require.True(t, ok)
	require.Equal(t, "third", boundary.EventID)
	boundary, ok = view.BoundaryForItems([]int{0, 1})
	require.True(t, ok)
	require.Equal(t, "first", boundary.EventID)

	_, ok = view.MessagesForItems(parent, nil)
	require.False(t, ok)
	_, ok = view.MessagesForItems(parent, []int{2, 1})
	require.False(t, ok)
	_, ok = view.MessagesForItems(parent, []int{3})
	require.False(t, ok)
	_, ok = (&View{}).MessagesForItems(parent, []int{0})
	require.False(t, ok)
	_, ok = view.BoundaryForItems([]int{3})
	require.False(t, ok)
	_, ok = (*View)(nil).BoundaryForItems([]int{0})
	require.False(t, ok)
	_, ok = view.PrefixMessages(parent, 0)
	require.False(t, ok)
	_, ok = view.PrefixMessages(parent, 4)
	require.False(t, ok)
	_, ok = view.PrefixBoundary(0)
	require.False(t, ok)
	require.Nil(t, (*View)(nil).Events())
}

func TestSnapshotClonesEffectiveEventMetadata(t *testing.T) {
	invocation := agent.NewInvocation()
	AttachProjection(invocation, &View{Items: []Item{{
		EffectiveEvent: event.Event{
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.NewAssistantMessage("answer"),
			}}},
			LongRunningToolIDs: map[string]struct{}{"call": {}},
			StateDelta:         map[string][]byte{"key": []byte("value")},
			Extensions: map[string]json.RawMessage{
				"metadata": json.RawMessage(`{"value":"original"}`),
			},
			Actions: &event.EventActions{SkipSummarization: true},
		},
	}}})

	first, ok := Snapshot(invocation)
	require.True(t, ok)
	first.Items[0].EffectiveEvent.LongRunningToolIDs["other"] = struct{}{}
	first.Items[0].EffectiveEvent.StateDelta["key"][0] = 'x'
	first.Items[0].EffectiveEvent.Extensions["metadata"][10] = 'x'
	first.Items[0].EffectiveEvent.Actions.SkipSummarization = false

	second, ok := Snapshot(invocation)
	require.True(t, ok)
	require.NotContains(t, second.Items[0].EffectiveEvent.LongRunningToolIDs, "other")
	require.Equal(t, "value", string(second.Items[0].EffectiveEvent.StateDelta["key"]))
	require.JSONEq(
		t,
		`{"value":"original"}`,
		string(second.Items[0].EffectiveEvent.Extensions["metadata"]),
	)
	require.True(t, second.Items[0].EffectiveEvent.Actions.SkipSummarization)
	require.Len(t, second.Events(), 1)
}

func TestFinalizeLeavesUnmatchedProjectionUnbound(t *testing.T) {
	invocation := agent.NewInvocation()
	AttachProjection(invocation, &View{
		ContentRequestLength: 1,
		Items: []Item{{
			Message:      model.NewUserMessage("expected"),
			RequestIndex: 0,
		}},
	})

	Finalize(invocation, &model.Request{Messages: []model.Message{
		model.NewAssistantMessage("different"),
	}}, 10)
	view, ok := Snapshot(invocation)
	require.True(t, ok)
	require.False(t, view.Bound)
	require.Equal(t, 10, view.RequestTokens)

	Finalize(nil, &model.Request{}, 1)
	Finalize(invocation, nil, 1)
}

func TestViewBoundaryAndBindingEdgeCases(t *testing.T) {
	view := &View{Items: []Item{{Boundary: Boundary{}}}}
	_, ok := view.PrefixBoundary(2)
	require.False(t, ok)

	parent := []model.Message{model.NewUserMessage("visible")}
	invalidFirst := &View{
		Bound: true,
		Items: []Item{{
			RequestIndex: -1,
		}},
	}
	_, ok = invalidFirst.MessagesForItems(parent, []int{0})
	require.False(t, ok)
	invalidFirst.Items[0].RequestIndex = len(parent) + 1
	_, ok = invalidFirst.MessagesForItems(parent, []int{0})
	require.False(t, ok)

	invalidItem := &View{
		Bound: true,
		Items: []Item{
			{RequestIndex: 0},
			{RequestIndex: len(parent)},
		},
	}
	_, ok = invalidItem.MessagesForItems(parent, []int{1})
	require.False(t, ok)

	require.False(t, bindItems(nil, parent))
	require.False(t, bindItems(&View{}, parent))
	require.False(t, bindItems(view, nil))

	messages := []model.Message{
		model.NewUserMessage("different"),
		model.NewUserMessage("visible"),
	}
	require.Equal(t, 1, findItem(
		messages,
		model.NewUserMessage("visible"),
		0,
		0,
		0,
	))
	require.Equal(t, -1, findItem(
		messages,
		model.NewAssistantMessage("missing"),
		0,
		0,
		0,
	))

	require.Nil(t, cloneView(nil))
	setEffectiveMessage(nil, model.NewUserMessage("ignored"))
	effective := event.Event{Response: &model.Response{
		Choices: []model.Choice{{Message: model.NewAssistantMessage("old")}},
	}}
	setEffectiveMessage(&effective, model.NewAssistantMessage("new"))
	require.Equal(
		t,
		"new",
		effective.Response.Choices[0].Message.Content,
	)
}

func TestMessageIdentityMatchesStableFields(t *testing.T) {
	require.False(t, messageIdentityMatches(
		model.NewAssistantMessage("same"),
		model.NewUserMessage("same"),
	))
	require.True(t, messageIdentityMatches(
		model.NewToolMessage("call", "renamed", "new payload"),
		model.NewToolMessage("call", "lookup", "old payload"),
	))
	require.False(t, messageIdentityMatches(
		model.NewToolMessage("other", "lookup", "payload"),
		model.NewToolMessage("call", "lookup", "payload"),
	))

	wantToolCalls := model.NewAssistantMessage("old")
	wantToolCalls.ToolCalls = []model.ToolCall{{ID: "call"}}
	gotToolCalls := model.NewAssistantMessage("new")
	gotToolCalls.ToolCalls = []model.ToolCall{{
		ID: "call",
		Function: model.FunctionDefinitionParam{
			Name:      "lookup",
			Arguments: []byte(`{"query":"new"}`),
		},
	}}
	require.True(t, messageIdentityMatches(gotToolCalls, wantToolCalls))
	gotToolCalls.ToolCalls[0].ID = "other"
	require.False(t, messageIdentityMatches(gotToolCalls, wantToolCalls))

	wantContent := model.NewUserMessage("same")
	wantContent.ReasoningContent = "reasoning"
	gotContent := wantContent
	gotContent.ReasoningSignature = "provider-specific-signature"
	require.True(t, messageIdentityMatches(gotContent, wantContent))
	gotContent.Content = "different"
	require.False(t, messageIdentityMatches(gotContent, wantContent))

	require.Nil(t, toolCallIDs(nil))
	require.Equal(
		t,
		[]string{"first", "second"},
		toolCallIDs([]model.ToolCall{{ID: "first"}, {ID: "second"}}),
	)
}
