//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package processor

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// safeStubTool has no IsConcurrencySafe method, so it takes the default.
type safeStubTool struct{ name string }

func (s safeStubTool) Declaration() *tool.Declaration { return &tool.Declaration{Name: s.name} }

// unsafeStubTool declares itself unfit for the parallel path via the narrow
// tool.ConcurrencyAware interface, as an agent tool does.
type unsafeStubTool struct{ name string }

func (u unsafeStubTool) Declaration() *tool.Declaration { return &tool.Declaration{Name: u.name} }
func (u unsafeStubTool) IsConcurrencySafe() bool        { return false }

// metadataStubTool publishes full metadata instead, the other supported route.
type metadataStubTool struct {
	name string
	safe bool
}

func (m metadataStubTool) Declaration() *tool.Declaration { return &tool.Declaration{Name: m.name} }
func (m metadataStubTool) ToolMetadata() tool.ToolMetadata {
	return tool.ToolMetadata{ConcurrencySafe: m.safe}
}

func callsFor(names ...string) []model.ToolCall {
	calls := make([]model.ToolCall, 0, len(names))
	for _, n := range names {
		calls = append(calls, model.ToolCall{
			Function: model.FunctionDefinitionParam{Name: n},
		})
	}
	return calls
}

func TestHasConcurrentBatch(t *testing.T) {
	tools := map[string]tool.Tool{
		"read":  safeStubTool{name: "read"},
		"grep":  safeStubTool{name: "grep"},
		"agent": unsafeStubTool{name: "agent"},
		"meta":  metadataStubTool{name: "meta", safe: true},
		"metaX": metadataStubTool{name: "metaX", safe: false},
	}

	tests := []struct {
		name      string
		toolCalls []model.ToolCall
		tools     map[string]tool.Tool
		want      bool
	}{
		{
			name:      "several safe calls batch",
			toolCalls: callsFor("read", "grep"),
			tools:     tools,
			want:      true,
		},
		{
			// One unsafe call sends the whole turn sequential rather than splitting
			// it, so the model's requested ordering is preserved.
			name:      "one unsafe call disqualifies the batch",
			toolCalls: callsFor("read", "agent"),
			tools:     tools,
			want:      false,
		},
		{
			name:      "unsafe call first also disqualifies",
			toolCalls: callsFor("agent", "read"),
			tools:     tools,
			want:      false,
		},
		{
			// A lone call has nothing to run beside it.
			name:      "single call is not a batch",
			toolCalls: callsFor("read"),
			tools:     tools,
			want:      false,
		},
		{
			name:      "no calls",
			toolCalls: nil,
			tools:     tools,
			want:      false,
		},
		{
			// Undeclared tools never execute; they produce a terminal error result
			// and must not constrain their siblings.
			name:      "undeclared tool does not disqualify",
			toolCalls: callsFor("read", "missing"),
			tools:     tools,
			want:      true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasConcurrentBatch(tt.toolCalls, tt.tools); got != tt.want {
				t.Errorf("hasConcurrentBatch() = %v, want %v", got, tt.want)
			}
		})
	}
}
