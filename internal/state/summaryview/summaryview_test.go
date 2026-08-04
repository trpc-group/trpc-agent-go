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
