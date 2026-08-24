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

// metadataTool publishes ToolMetadata and nothing else. It is the shape that
// separates the two channels: MetadataOf reads the provider, and
// IsConcurrencySafe deliberately ignores it.
type metadataTool struct {
	*mockTool
	metadata tool.ToolMetadata
}

func (t metadataTool) ToolMetadata() tool.ToolMetadata { return t.metadata }

// TestBeforeModelPreservesDescriptiveMetadata pins that the wrapper reports what
// the inner tool publishes rather than a guarantee synthesized from its own
// IsConcurrencySafe.
//
// tool.MetadataOf falls back to ConcurrencyAware for a tool that publishes no
// metadata, so once the wrapper implemented that interface it would have
// answered ToolMetadata{ConcurrencySafe: true} for every tool ToolPipe touched —
// including one, like mockTool here, that implements neither interface. That
// turns "raises no scheduling objection", all IsConcurrencySafe promises, into
// the same-tool reentrancy guarantee the field documents. The LLMAgent
// permission path builds PermissionRequest.Metadata from whatever sits in
// Request.Tools, so a policy would read the invented guarantee off this wrapper.
func TestBeforeModelPreservesDescriptiveMetadata(t *testing.T) {
	plain := &mockTool{decl: &tool.Declaration{
		Name:        "plain_tool",
		Description: "implements neither concurrency interface",
	}}
	published := tool.ToolMetadata{
		ReadOnly:        true,
		OpenWorld:       true,
		ConcurrencySafe: false,
	}
	describing := metadataTool{
		mockTool: &mockTool{decl: &tool.Declaration{
			Name:        "describing_tool",
			Description: "publishes metadata only",
		}},
		metadata: published,
	}

	require.Equal(t, tool.ToolMetadata{}, tool.MetadataOf(plain),
		"precondition: the inner tool publishes nothing")

	tp := New(WithToolNames("plain_tool", "describing_tool"))
	req := &model.Request{
		Tools: map[string]tool.Tool{
			"plain_tool":      plain,
			"describing_tool": describing,
		},
	}
	_, err := tp.beforeModel(context.Background(), &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	require.NotSame(t, tool.Tool(plain), req.Tools["plain_tool"],
		"precondition: ToolPipe replaced the entry with its own wrapper")

	assert.Equal(t, tool.ToolMetadata{}, tool.MetadataOf(req.Tools["plain_tool"]),
		"the wrapper must not invent a reentrancy guarantee for a tool that made none")
	assert.Equal(t, published, tool.MetadataOf(req.Tools["describing_tool"]),
		"the wrapper must report what the inner tool publishes, unchanged")

	// The two channels stay independent: a metadata-only ConcurrencySafe:false
	// is still admitted, exactly as tool.MetadataOf and tool.IsConcurrencySafe
	// document.
	assert.True(t, itool.IsConcurrencySafe(req.Tools["describing_tool"]),
		"metadata alone is not a scheduling objection")
}
