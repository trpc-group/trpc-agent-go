//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolpipe

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/deferred"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

type deferredPipeTool struct{ mockTool }

func (*deferredPipeTool) ShouldDefer(context.Context) bool { return true }

type pipeToolSet struct{ tools []tool.Tool }

func (s pipeToolSet) Tools(context.Context) []tool.Tool { return s.tools }
func (pipeToolSet) Name() string                        { return "crm" }
func (pipeToolSet) Description() string                 { return "Customer records." }
func (pipeToolSet) Close() error                        { return nil }

func pipeTestTool(name string) mockTool {
	return mockTool{decl: &tool.Declaration{Name: name, InputSchema: &tool.Schema{Type: "object"}}}
}

func TestToolPipePreservesDeferral(t *testing.T) {
	ctx := context.Background()
	eager := &deferredPipeTool{pipeTestTool("eager")}
	marked := &mockTool{decl: pipeTestTool("deferred").decl}
	filter := tool.NewIncludeToolNamesFilter("deferred")
	req := &model.Request{Tools: make(map[string]tool.Tool)}
	for _, tl := range []tool.Tool{eager, marked} {
		req.Tools[tl.Declaration().Name] = deferred.Tool(tl, filter)
	}
	tp := New(WithToolScope(func(tool.Tool) bool { return true }))
	_, err := tp.beforeModel(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	require.True(t, tool.ShouldDefer(ctx, req.Tools["deferred"]))
	require.False(t, tool.ShouldDefer(ctx, req.Tools["eager"]), "an explicit eager decision overrides the tool's default")
	for _, tl := range req.Tools {
		require.Contains(t, tl.Declaration().InputSchema.Properties, "result_filter",
			"preserving deferral does not disable ToolPipe's schema augmentation")
	}
}

func TestToolPipePreservesDeferredSkipSummarization(t *testing.T) {
	for _, skip := range []bool{false, true} {
		t.Run(fmt.Sprintf("skip=%t", skip), func(t *testing.T) {
			original := function.NewFunctionTool(
				func(context.Context, struct{}) (string, error) { return "ok", nil },
				function.WithName("lookup"),
				function.WithSkipSummarization(skip),
			)
			marked := deferred.Tool(original, nil)
			req := &model.Request{Tools: map[string]tool.Tool{"lookup": marked}}
			tp := New(WithToolScope(func(tool.Tool) bool { return true }))
			_, err := tp.beforeModel(context.Background(), &model.BeforeModelArgs{Request: req})
			require.NoError(t, err)
			wrapped := req.Tools["lookup"]
			require.NotSame(t, marked, wrapped, "ToolPipe must apply its declaration wrapper")
			require.Contains(t, wrapped.Declaration().InputSchema.Properties, "result_filter")
			require.True(t, tool.ShouldDefer(context.Background(), wrapped))
			skipper, ok := wrapped.(interface{ SkipSummarization() bool })
			require.True(t, ok)
			require.Equal(t, skip, skipper.SkipSummarization())
		})
	}
}

func TestToolPipePreservesToolSetIdentity(t *testing.T) {
	ctx := context.Background()
	set := pipeToolSet{tools: []tool.Tool{
		&mockTool{decl: pipeTestTool("lookup").decl},
		&mockTool{decl: pipeTestTool("orders").decl},
	}}
	for _, mode := range []tool.ToolSetToolNameMode{tool.ToolSetToolNameModeQualified, tool.ToolSetToolNameModeOriginal} {
		members := itool.NewNamedToolSetWithMode(
			deferred.ToolSet(set, tool.NewIncludeToolNamesFilter("orders")), mode).Tools(ctx)
		for _, member := range members {
			tp := New(WithToolScope(func(tool.Tool) bool { return true }))
			name := member.Declaration().Name
			req := &model.Request{Tools: map[string]tool.Tool{name: member}}
			_, err := tp.beforeModel(ctx, &model.BeforeModelArgs{Request: req})
			require.NoError(t, err)
			wrapped := req.Tools[name]
			namespace, original, description, ok := itool.ToolSetMember(wrapped)
			require.True(t, ok)
			require.Equal(t, "crm", namespace)
			require.Equal(t, "Customer records.", description)
			require.Equal(t, original == "orders", tool.ShouldDefer(ctx, wrapped))
			require.Contains(t, wrapped.Declaration().InputSchema.Properties, "result_filter")
		}
	}
}

func TestToolPipePreservesDeferredFrameworkTools(t *testing.T) {
	for name, original := range map[string]tool.Tool{
		"state delta":            &mockStateDeltaTool{pipeTestTool("state")},
		"invocation state delta": &mockStateDeltaForInvocationTool{pipeTestTool("state")},
		"long running":           &mockLongRunningTool{pipeTestTool("state")},
		"agent streaming":        &mockFrameworkTool{pipeTestTool("state")},
	} {
		t.Run(name, func(t *testing.T) {
			marked := deferred.Tool(original, nil)
			req := &model.Request{Tools: map[string]tool.Tool{"state": marked}}
			tp := New(WithToolScope(func(tool.Tool) bool { return true }))
			_, err := tp.beforeModel(context.Background(), &model.BeforeModelArgs{Request: req})
			require.NoError(t, err)
			require.Same(t, marked, req.Tools["state"], "ToolPipe retains its existing framework-tool bypass")
			require.Same(t, original, itool.ResolveSemantic(req.Tools["state"]),
				"execution can still reach the original framework capabilities")
		})
	}
}
