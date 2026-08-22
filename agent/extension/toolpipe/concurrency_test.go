//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolpipe

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// TestBeforeModelPreservesConcurrencyObjection pins that augmenting a tool's
// schema does not readmit it to the parallel path.
//
// itool.IsConcurrencySafe is the question both parallel schedulers ask of every
// entry in Request.Tools — the LLMAgent function-call processor and the graph
// Tools node — and a single false keeps their whole batch sequential. Before the
// wrapper delegated it, an objecting tool that ToolPipe had replaced read as
// raising no objection, so a mixed turn ran it beside its siblings.
func TestBeforeModelPreservesConcurrencyObjection(t *testing.T) {
	exclusive := function.NewFunctionTool(
		func(ctx context.Context, _ struct{}) (string, error) { return "", nil },
		function.WithName("exclusive_tool"),
		function.WithDescription("declines to share its turn"),
		function.WithConcurrencySafe(false),
	)
	sibling := function.NewFunctionTool(
		func(ctx context.Context, _ struct{}) (string, error) { return "", nil },
		function.WithName("sibling_tool"),
		function.WithDescription("raises no objection"),
	)

	require.False(t, itool.IsConcurrencySafe(exclusive),
		"precondition: the unwrapped tool objects")

	tp := New(WithToolNames("exclusive_tool", "sibling_tool"))
	req := &model.Request{
		Tools: map[string]tool.Tool{
			"exclusive_tool": exclusive,
			"sibling_tool":   sibling,
		},
	}
	_, err := tp.beforeModel(context.Background(), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)

	require.NotSame(t, tool.Tool(exclusive), req.Tools["exclusive_tool"],
		"precondition: ToolPipe replaced the entry with its own wrapper")

	assert.False(t, itool.IsConcurrencySafe(req.Tools["exclusive_tool"]),
		"a ToolPipe-wrapped tool must keep the objection that serializes its batch")
	assert.True(t, itool.IsConcurrencySafe(req.Tools["sibling_tool"]),
		"the wrapper must not manufacture an objection for a tool that raised none")
}

// TestBeforeModelResolvesNestedWrappers pins that the delegation resolves
// through the wrappers between ToolPipe and the semantic tool rather than
// stopping at the first one. A ToolSet's tools reach Request.Tools as
// *itool.NamedTool, so this is the shape most objecting toolset tools have.
func TestBeforeModelResolvesNestedWrappers(t *testing.T) {
	exclusive := function.NewFunctionTool(
		func(ctx context.Context, _ struct{}) (string, error) { return "", nil },
		function.WithName("exclusive_tool"),
		function.WithDescription("declines to share its turn"),
		function.WithConcurrencySafe(false),
	)
	named := itool.NewUnprefixedNamedTool(exclusive)

	tp := New(WithToolNames("exclusive_tool"))
	req := &model.Request{Tools: map[string]tool.Tool{"exclusive_tool": named}}
	_, err := tp.beforeModel(context.Background(), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)

	assert.False(t, itool.IsConcurrencySafe(req.Tools["exclusive_tool"]))
}
