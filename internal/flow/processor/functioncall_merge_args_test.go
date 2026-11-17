//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

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

func TestBuildMergedParallelEventArgumentPrecedence(t *testing.T) {
	calls := []model.ToolCall{
		mergeArgsCall("original", `{"query":"original"}`),
		mergeArgsCall("rewritten", `{"query":"before callback"}`),
		mergeArgsCall("empty", `{"query":"not empty"}`),
		mergeArgsCall("duplicate", `{"query":"first"}`),
		mergeArgsCall("duplicate", `{"query":"last"}`),
		mergeArgsCall("", `{"query":"ignored"}`),
		mergeArgsCall("nil", ""),
	}
	calls[6].Function.Arguments = nil
	results := []toolResult{
		{index: 1, toolArgs: []byte(`{"query":"first rewrite"}`)},
		{index: 2, toolArgs: []byte{}},
		{index: 1, toolArgs: []byte(`{"query":"final rewrite"}`)},
		{index: 1, toolArgs: nil},
		{index: -1, toolArgs: []byte("ignored")},
		{index: len(calls), toolArgs: []byte("ignored")},
	}
	want := map[string]string{
		"original":  `{"query":"original"}`,
		"rewritten": `{"query":"final rewrite"}`,
		"empty":     "",
		"duplicate": `{"query":"last"}`,
		"nil":       "",
	}
	for _, mode := range []string{"minimal", "single", "multiple"} {
		t.Run(mode, func(t *testing.T) {
			var children []*event.Event
			expected := make(map[string]string, len(want)+1)
			for id, args := range want {
				expected[id] = args
			}
			if mode != "minimal" {
				child := event.New("inv", "main", event.WithResponse(&model.Response{
					Choices: []model.Choice{{Message: model.NewToolMessage("rewritten", "noop", "ok")}},
				}))
				require.NoError(t, event.SetExtension(child, event.ToolCallArgsExtensionKey, map[string]string{
					"rewritten": "stale", "extra": `{"retained":true}`,
				}))
				require.NoError(t, event.SetExtension(child, "custom", "keep"))
				children = append(children, child)
				expected["extra"] = `{"retained":true}`
				if mode == "multiple" {
					children = append(children, event.New("inv", "main", event.WithResponse(&model.Response{
						Choices: []model.Choice{{Message: model.NewToolMessage("original", "noop", "ok")}},
					})))
				}
			}
			var originalExtension []byte
			if len(children) > 0 {
				originalExtension = append([]byte(nil), children[0].Extensions[event.ToolCallArgsExtensionKey]...)
			}
			p := NewFunctionCallResponseProcessor(false, nil)
			got := p.buildMergedParallelEvent(context.Background(), mergeArgsInvocation(),
				&model.Response{}, nil, calls, results, children)
			args, ok, err := event.GetExtension[map[string]string](got, event.ToolCallArgsExtensionKey)
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, expected, args)
			switch mode {
			case "minimal":
				require.Len(t, got.Choices, len(calls))
				for i, call := range calls {
					require.Equal(t, call.ID, got.Choices[i].Message.ToolID)
				}
			case "single":
				require.Same(t, children[0], got)
				require.JSONEq(t, `"keep"`, string(got.Extensions["custom"]))
			case "multiple":
				require.Len(t, got.Choices, 2)
				require.Equal(t, "rewritten", got.Choices[0].Message.ToolID)
				require.Equal(t, "original", got.Choices[1].Message.ToolID)
				require.Equal(t, originalExtension, []byte(children[0].Extensions[event.ToolCallArgsExtensionKey]))
			}
		})
	}
}

func TestBuildMergedParallelEventArgumentExtensionEdges(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		id   string
		want string
	}{
		{"missing", "", "call", `{"call":"{}"}`},
		{"null", "null", "call", `{"call":"{}"}`},
		{"malformed", "{", "call", "{"},
		{"wrong_type", `{"other":123}`, "call", `{"other":123}`},
		{"empty_id", ` {"other":"preserved"} `, "", ` {"other":"preserved"} `},
		{"empty_id_without_extension", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			child := &event.Event{Extensions: map[string]json.RawMessage{"custom": json.RawMessage(`true`)}}
			if tc.raw != "" {
				child.Extensions[event.ToolCallArgsExtensionKey] = json.RawMessage(tc.raw)
			}
			p := NewFunctionCallResponseProcessor(false, nil)
			got := p.buildMergedParallelEvent(context.Background(), mergeArgsInvocation(),
				&model.Response{}, nil, []model.ToolCall{mergeArgsCall(tc.id, "{}")}, nil, []*event.Event{child})
			require.Same(t, child, got)
			require.Equal(t, tc.want, string(got.Extensions[event.ToolCallArgsExtensionKey]))
			require.Equal(t, json.RawMessage(`true`), got.Extensions["custom"])
		})
	}
	// No child events and no calls must not introduce an empty args extension.
	p := NewFunctionCallResponseProcessor(false, nil)
	got := p.buildMergedParallelEvent(context.Background(), mergeArgsInvocation(), &model.Response{}, nil, nil, nil, nil)
	require.NotContains(t, got.Extensions, event.ToolCallArgsExtensionKey)
	got = p.buildMergedParallelEvent(context.Background(), nil, nil, nil, []model.ToolCall{mergeArgsCall("call", "{}")}, nil, []*event.Event{nil})
	require.Nil(t, got)
}

func mergeArgsCall(id, args string) model.ToolCall {
	return model.ToolCall{ID: id, Type: "function", Function: model.FunctionDefinitionParam{
		Name: "noop", Arguments: []byte(args),
	}}
}

func mergeArgsInvocation() *agent.Invocation {
	inv := agent.NewInvocation()
	inv.InvocationID = "inv"
	inv.AgentName = "main"
	inv.RunOptions.DisableTracing = true
	return inv
}

func TestBuildMergedParallelEventArgumentAllocationGrowth(t *testing.T) {
	allocations := func(n int) float64 {
		calls := make([]model.ToolCall, n)
		for i := range calls {
			calls[i] = mergeArgsCall(fmt.Sprintf("call_%024d", i), "{}")
		}
		p := NewFunctionCallResponseProcessor(false, nil)
		inv := mergeArgsInvocation()
		rsp := &model.Response{}
		return testing.AllocsPerRun(3, func() {
			merged := p.buildMergedParallelEvent(context.Background(), inv, rsp, nil, calls, nil, nil)
			if len(merged.Choices) != n {
				panic("missing tool results")
			}
		})
	}
	small, large := allocations(32), allocations(128)
	// Four times as many calls should not cause quadratic allocation growth.
	// Leave headroom for map growth and differences between Go toolchains.
	require.Less(t, large, 8*small, "allocations grew from %.0f to %.0f", small, large)
}

func BenchmarkBuildMergedParallelEventArguments(b *testing.B) {
	for _, size := range []int{2, 1024} {
		for _, n := range []int{1, 16, 128, 512} {
			b.Run(fmt.Sprintf("calls=%d/bytes=%d", n, size), func(b *testing.B) {
				calls := make([]model.ToolCall, n)
				results := make([]toolResult, n)
				children := make([]*event.Event, n)
				args := "{}"
				if size > 2 {
					args = `{"query":"` + strings.Repeat("x", size-12) + `"}`
				}
				for i := range calls {
					id := fmt.Sprintf("call_%024d", i)
					calls[i] = mergeArgsCall(id, args)
					results[i] = toolResult{index: i, toolArgs: calls[i].Function.Arguments}
					children[i] = event.New("inv", "main", event.WithResponse(&model.Response{
						Choices: []model.Choice{{Index: i, Message: model.NewToolMessage(id, "noop", "ok")}},
					}))
					require.NoError(b, event.SetExtension(children[i], event.ToolCallArgsExtensionKey, map[string]string{id: args}))
				}
				p := NewFunctionCallResponseProcessor(false, nil)
				inv := mergeArgsInvocation()
				rsp := &model.Response{Model: "mock"}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					merged := p.buildMergedParallelEvent(context.Background(), inv, rsp, nil, calls, results, children)
					if len(merged.Choices) != n {
						b.Fatal("missing tool results")
					}
				}
			})
		}
	}
}
