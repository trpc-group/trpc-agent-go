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
	return parallelArgumentsViewWithArgument(n, strings.Repeat("x", 16<<10))
}

func parallelArgumentsViewWithArgument(n int, argument string) (*View, *model.Request) {
	args := make(map[string]string, n)
	for i := 0; i < n; i++ {
		args[fmt.Sprintf("call-%d", i)] = argument
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
	for _, tc := range []struct {
		n        int
		argument string
	}{
		{1, strings.Repeat("x", 16<<10)},
		{4, strings.Repeat("x", 16<<10)},
		{8, strings.Repeat("x", 16<<10)},
		{64, "{}"},
		{256, "{}"},
		{1024, "{}"},
	} {
		t.Run(fmt.Sprintf("tools=%d/argument_bytes=%d", tc.n, len(tc.argument)), func(t *testing.T) {
			input, req := parallelArgumentsViewWithArgument(tc.n, tc.argument)
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
	// A transform can duplicate a history item. Its shared extensions must
	// remain isolated from the source and the older invocation view as well.
	after := append([]model.Message{req.Messages[0]}, req.Messages...)
	require.True(t, RebaseAfterTransform(inv, req.Messages, after, []int{0, 0, 1, 2, 3}))
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

// BenchmarkSmallParallelArgumentsView covers large batches whose argument
// payloads are small but whose full-batch argument map grows with the batch.
func BenchmarkSmallParallelArgumentsView(b *testing.B) {
	for _, n := range []int{64, 256, 1024} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			input, req := parallelArgumentsViewWithArgument(n, "{}")
			inv := agent.NewInvocation()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				AttachProjection(inv, input)
				Finalize(inv, req, n)
			}
		})
	}
}

// BenchmarkStoredExtensionSharing measures the cache cost when source buffers
// are shared, distinct, or mixed. All payloads have equal contents: only their
// identity differs, so distinct buffers cannot benefit from the copy cache.
func BenchmarkStoredExtensionSharing(b *testing.B) {
	for _, payloadBytes := range []int{16, 16 << 10} {
		for _, sharing := range []string{"shared", "distinct", "mixed"} {
			b.Run(fmt.Sprintf("%s/payload_bytes=%d", sharing, payloadBytes), func(b *testing.B) {
				input, req := parallelArgumentsViewWithArgument(256, "{}")
				raw := json.RawMessage(`"` + strings.Repeat("x", payloadBytes-2) + `"`)
				for i := range input.Items {
					payload := raw
					if sharing == "distinct" || (sharing == "mixed" && i%2 != 0) {
						payload = append(json.RawMessage(nil), raw...)
					}
					input.Items[i].EffectiveEvent.Extensions = map[string]json.RawMessage{"payload": payload}
				}
				inv := agent.NewInvocation()
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					AttachProjection(inv, input)
					Finalize(inv, req, len(input.Items))
				}
			})
		}
	}
}

var extensionEventsBenchmarkSink []event.Event

// BenchmarkParallelArgumentsCopyChain includes the mutable-copy boundaries
// that intentionally retain per-item isolation. It measures this synthetic
// copy chain, not a complete summarization run or whole-process memory.
func BenchmarkParallelArgumentsCopyChain(b *testing.B) {
	for _, n := range []int{1, 4, 8} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			input, req := parallelArgumentsView(n)
			inv := agent.NewInvocation()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				AttachProjection(inv, input)
				Finalize(inv, req, 10)
				view, ok := Snapshot(inv)
				if !ok {
					b.Fatal("stored view is missing")
				}
				ctx := ContextWithView(context.Background(), view)
				fromContext, ok := FromContext(ctx)
				if !ok {
					b.Fatal("context view is missing")
				}
				extensionEventsBenchmarkSink = fromContext.Events()
			}
		})
	}
}
