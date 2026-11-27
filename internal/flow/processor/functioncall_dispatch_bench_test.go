//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package processor

import (
	"context"
	"fmt"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolcall"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type dispatchLatencyTool struct {
	name  string
	delay time.Duration
}

func (t *dispatchLatencyTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: t.name, InputSchema: &tool.Schema{Type: "object"}}
}
func (t *dispatchLatencyTool) Call(ctx context.Context, _ []byte) (any, error) {
	if t.delay > 0 {
		select {
		case <-time.After(t.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return "synthetic tool result", nil
}

// BenchmarkDispatchCompletedBatch includes tool execution and result collection.
// Single-call cases exercise the sequential path; other cases exercise parallel
// dispatch with limited, unlimited, or shared-group capacity. Delays model I/O
// separately from the immediate-return scheduling overhead.
func BenchmarkDispatchCompletedBatch(b *testing.B) {
	for _, s := range []struct {
		name  string
		state int
		delay time.Duration
		limit int
		group bool
	}{
		{"immediate_empty", 0, 0, 4, false},
		{"immediate_64k", 64 * 1024, 0, 4, false},
		{"io5ms_64k", 64 * 1024, 5 * time.Millisecond, 4, false},
		{"io50ms_64k", 64 * 1024, 50 * time.Millisecond, 4, false},
		{"unlimited_64k", 64 * 1024, 0, 0, false},
		{"group1_io5ms_64k", 64 * 1024, 5 * time.Millisecond, 4, true},
	} {
		for _, n := range []int{1, 2, 4, 8, 16, 32} {
			b.Run(fmt.Sprintf("%s/calls=%d", s.name, n), func(b *testing.B) {
				ctx := context.Background()
				cfg := tool.ConcurrencyConfig{MaxConcurrency: s.limit}
				if s.group {
					cfg.Groups = []tool.ConcurrencyGroup{{ToolNames: []string{"grouped"}, Limit: 1}}
				}
				ctx = toolcall.WithLimiter(ctx, toolcall.NewLimiter(cfg))
				inv := agent.NewInvocation(agent.WithInvocationSession(&session.Session{State: session.StateMap{"synthetic": make([]byte, s.state)}}))
				inv.AgentName = "latency-probe"
				inv.RunOptions.DisableTracing = true
				calls := make([]model.ToolCall, n)
				for i := range calls {
					name := "probe"
					if s.group && i < n/2 {
						name = "grouped"
					}
					calls[i] = model.ToolCall{ID: fmt.Sprintf("call-%d", i), Type: "function", Function: model.FunctionDefinitionParam{Name: name, Arguments: []byte(`{}`)}}
				}
				tools := map[string]tool.Tool{"probe": &dispatchLatencyTool{name: "probe", delay: s.delay}, "grouped": &dispatchLatencyTool{name: "grouped", delay: s.delay}}
				rsp := &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, ToolCalls: calls}}}}
				p := NewFunctionCallResponseProcessor(true, nil)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					result, err := p.handleFunctionCalls(ctx, inv, rsp, tools, nil)
					if err != nil {
						b.Fatal(err)
					}
					if result == nil || len(result.Choices) != n {
						b.Fatal("missing tool results")
					}
				}
			})
		}
	}
}
