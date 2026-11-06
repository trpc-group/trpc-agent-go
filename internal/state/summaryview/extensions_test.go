//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package summaryview

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func parallelArgumentsView(n int) (*View, *model.Request) {
	args := make(map[string]string, n)
	for i := 0; i < n; i++ {
		args[fmt.Sprintf("call-%d", i)] = strings.Repeat("x", 16<<10)
	}
	raw, err := json.Marshal(args)
	if err != nil {
		panic(err)
	}
	extensions := map[string]json.RawMessage{
		event.ToolCallArgsExtensionKey: raw,
		"other":                        json.RawMessage(`{"keep":true}`),
	}
	view := &View{ContentRequestLength: n}
	req := &model.Request{}
	for i := 0; i < n; i++ {
		msg := model.Message{
			Role: model.RoleTool, ToolID: fmt.Sprintf("call-%d", i),
			Content: "ok",
		}
		view.Items = append(view.Items, Item{
			Message: msg, RequestIndex: i,
			EffectiveEvent: event.Event{
				Response: &model.Response{
					Choices: []model.Choice{{Message: msg}},
				},
				Extensions: extensions,
			},
		})
		req.Messages = append(req.Messages, msg)
	}
	return view, req
}

func TestStoredViewCopiesSharedExtensionsOnce(t *testing.T) {
	for _, n := range []int{1, 4, 8} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			input, req := parallelArgumentsView(n)
			inv := agent.NewInvocation()
			AttachProjection(inv, input)
			check := func() {
				t.Helper()
				state, ok := agent.GetStateValue[*invocationState](inv, stateKey)
				require.True(t, ok)
				buffers := make(map[*byte]int)
				for _, item := range state.view.Items {
					raw := item.EffectiveEvent.Extensions[event.ToolCallArgsExtensionKey]
					require.Equal(t, input.Items[0].EffectiveEvent.Extensions[event.ToolCallArgsExtensionKey], raw)
					require.NotSame(t, &input.Items[0].EffectiveEvent.Extensions[event.ToolCallArgsExtensionKey][0], &raw[0])
					buffers[&raw[0]] = len(raw)
				}
				// One complete argument map must suffice for a stored batch,
				// including after rebinding or duplicating projected items.
				require.Len(t, buffers, 1)
			}
			check()
			Finalize(inv, req, 10)
			check()
			after := append([]model.Message{req.Messages[0]}, req.Messages...)
			indexes := []int{0}
			for i := range req.Messages {
				indexes = append(indexes, i)
			}
			require.True(t, RebaseAfterTransform(inv, req.Messages, after, indexes))
			check()
			InvalidateBinding(inv)
			check()
		})
	}
}

func TestSharedExtensionsRemainIsolatedAtBoundaries(t *testing.T) {
	input, req := parallelArgumentsView(4)
	original := append(json.RawMessage(nil), input.Items[0].EffectiveEvent.Extensions[event.ToolCallArgsExtensionKey]...)
	inv := agent.NewInvocation()
	AttachProjection(inv, input)
	previous := inv.View()
	input.Items[0].EffectiveEvent.Extensions[event.ToolCallArgsExtensionKey][0] = '!'
	input.Items[0].EffectiveEvent.Extensions["other"] = json.RawMessage(`false`)
	Finalize(inv, req, 10)
	view, ok := Snapshot(inv)
	require.True(t, ok)
	for _, item := range view.Items {
		require.Equal(t, original, item.EffectiveEvent.Extensions[event.ToolCallArgsExtensionKey])
		require.JSONEq(t, `{"keep":true}`, string(item.EffectiveEvent.Extensions["other"]))
	}
	events := view.Events()
	events[0].Extensions[event.ToolCallArgsExtensionKey][0] = '?'
	require.Equal(t, original, events[1].Extensions[event.ToolCallArgsExtensionKey])
	require.Equal(t, original, view.Items[0].EffectiveEvent.Extensions[event.ToolCallArgsExtensionKey])
	ctx := ContextWithView(context.Background(), view)
	view.Items[0].EffectiveEvent.Extensions[event.ToolCallArgsExtensionKey][0] = '#'
	view.Items[0].EffectiveEvent.Extensions["other"][0] = '#'
	require.Equal(t, original, view.Items[1].EffectiveEvent.Extensions[event.ToolCallArgsExtensionKey])
	require.JSONEq(t, `{"keep":true}`, string(view.Items[1].EffectiveEvent.Extensions["other"]))
	for _, source := range []*agent.Invocation{inv, previous} {
		snapshot, ok := Snapshot(source)
		require.True(t, ok)
		for _, item := range snapshot.Items {
			require.Equal(t, original, item.EffectiveEvent.Extensions[event.ToolCallArgsExtensionKey])
		}
	}
	fromContext, ok := FromContext(ctx)
	require.True(t, ok)
	require.Equal(t, original, fromContext.Items[0].EffectiveEvent.Extensions[event.ToolCallArgsExtensionKey])
	fromContext.Items[0].EffectiveEvent.Extensions[event.ToolCallArgsExtensionKey][0] = '$'
	require.Equal(t, original, fromContext.Items[1].EffectiveEvent.Extensions[event.ToolCallArgsExtensionKey])
	again, ok := FromContext(ctx)
	require.True(t, ok)
	require.Equal(t, original, again.Items[0].EffectiveEvent.Extensions[event.ToolCallArgsExtensionKey])
}

func TestStoredExtensionsPreserveDistinctBuffersAndLengths(t *testing.T) {
	raw := json.RawMessage(`1234`)
	values := []json.RawMessage{nil, {}, raw, raw[:1], json.RawMessage(`1234`)}
	input := &View{}
	for _, value := range values {
		input.Items = append(input.Items, Item{EffectiveEvent: event.Event{
			// Equal event IDs must not coalesce different extension values.
			ID:         "same-event",
			Extensions: map[string]json.RawMessage{"value": value},
		}})
	}
	input.Items = append(input.Items, Item{EffectiveEvent: event.Event{}})
	inv := agent.NewInvocation()
	AttachProjection(inv, input)
	snapshot, ok := Snapshot(inv)
	require.True(t, ok)
	require.Nil(t, snapshot.Items[0].EffectiveEvent.Extensions["value"])
	require.Empty(t, snapshot.Items[1].EffectiveEvent.Extensions["value"])
	for i := 2; i < len(values); i++ {
		require.Equal(t, values[i], snapshot.Items[i].EffectiveEvent.Extensions["value"])
	}
	require.Nil(t, snapshot.Items[len(values)].EffectiveEvent.Extensions)
	raw[0] = '9'
	snapshot, ok = Snapshot(inv)
	require.True(t, ok)
	require.Equal(t, "1234", string(snapshot.Items[2].EffectiveEvent.Extensions["value"]))
	require.Equal(t, "1", string(snapshot.Items[3].EffectiveEvent.Extensions["value"]))
}

func BenchmarkParallelArgumentsView(b *testing.B) {
	for _, n := range []int{1, 4, 8} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			input, req := parallelArgumentsView(n)
			inv := agent.NewInvocation()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				AttachProjection(inv, input)
				Finalize(inv, req, 10)
			}
		})
	}
}
