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
	"testing"

	"github.com/stretchr/testify/require"
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
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
	deferred := &mockTool{decl: pipeTestTool("deferred").decl}
	decision := &itool.Deferral{Filter: tool.NewIncludeToolNamesFilter("deferred")}
	req := &model.Request{Tools: make(map[string]tool.Tool)}
	for _, tl := range []tool.Tool{eager, deferred} {
		req.Tools[tl.Declaration().Name] = itool.WithDeferral(tl, decision)
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

func TestToolPipePreservesToolSetIdentity(t *testing.T) {
	ctx := context.Background()
	set := pipeToolSet{tools: []tool.Tool{
		&mockTool{decl: pipeTestTool("lookup").decl},
		&mockTool{decl: pipeTestTool("orders").decl},
	}}
	for _, mode := range []tool.ToolSetToolNameMode{tool.ToolSetToolNameModeQualified, tool.ToolSetToolNameModeOriginal} {
		members := itool.NewNamedToolSetWithMode(set, mode).
			WithDeferral(&itool.Deferral{Filter: tool.NewIncludeToolNamesFilter("orders")}).Tools(ctx)
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
			deferred := itool.WithDeferral(original, &itool.Deferral{})
			req := &model.Request{Tools: map[string]tool.Tool{"state": deferred}}
			tp := New(WithToolScope(func(tool.Tool) bool { return true }))
			_, err := tp.beforeModel(context.Background(), &model.BeforeModelArgs{Request: req})
			require.NoError(t, err)
			require.Same(t, deferred, req.Tools["state"], "ToolPipe retains its existing framework-tool bypass")
			require.Same(t, original, itool.ResolveSemantic(req.Tools["state"]),
				"execution can still reach the original framework capabilities")
		})
	}
}
