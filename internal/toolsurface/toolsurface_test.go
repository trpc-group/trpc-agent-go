//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolsurface

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type stubSurfaceTool struct {
	decl *tool.Declaration
}

func (s stubSurfaceTool) Declaration() *tool.Declaration {
	return s.decl
}

type stubCallableSurfaceTool struct {
	stubSurfaceTool
	called bool
}

func (s *stubCallableSurfaceTool) Call(context.Context, []byte) (any, error) {
	s.called = true
	return "called", nil
}

type stubStreamableSurfaceTool struct {
	stubSurfaceTool
	called bool
}

func (s *stubStreamableSurfaceTool) StreamableCall(
	context.Context,
	[]byte,
) (*tool.StreamReader, error) {
	s.called = true
	stream := tool.NewStream(1)
	stream.Writer.Close()
	return stream.Reader, nil
}

type semanticSurfaceTool struct {
	stubCallableSurfaceTool
}

func (s *semanticSurfaceTool) CheckPermission(
	context.Context,
	*tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	return tool.DenyPermission("blocked"), nil
}

func (s *semanticSurfaceTool) SkipSummarization() bool { return true }

func (s *semanticSurfaceTool) LongRunning() bool { return true }

func (s *semanticSurfaceTool) StreamInner() bool { return false }

func (s *semanticSurfaceTool) InnerTextMode() tool.InnerTextMode {
	return tool.InnerTextModeExclude
}

func (s *semanticSurfaceTool) ToolSetName() string { return "toolset" }

func (s *semanticSurfaceTool) ToolMetadata() tool.ToolMetadata {
	return tool.ToolMetadata{
		ReadOnly:        true,
		ConcurrencySafe: true,
		MaxResultSize:   42,
	}
}

func (s *semanticSurfaceTool) ShouldDefer(context.Context) bool { return true }

func (s *semanticSurfaceTool) StateDeltaForInvocation(
	*agent.Invocation,
	string,
	[]byte,
	[]byte,
) map[string][]byte {
	return map[string][]byte{"invocation": []byte("delta")}
}

type namedLikeSurfaceTool struct {
	original tool.Tool
	decl     *tool.Declaration
}

func (n *namedLikeSurfaceTool) Declaration() *tool.Declaration {
	return n.decl
}

func (n *namedLikeSurfaceTool) Original() tool.Tool {
	return n.original
}

func (n *namedLikeSurfaceTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	return n.original.(tool.CallableTool).Call(ctx, jsonArgs)
}

func (n *namedLikeSurfaceTool) StreamableCall(
	context.Context,
	[]byte,
) (*tool.StreamReader, error) {
	return nil, nil
}

type stubSurfaceAgent struct {
	tools     []tool.Tool
	userTools []tool.Tool
}

func (s *stubSurfaceAgent) Run(
	context.Context,
	*agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}

func (s *stubSurfaceAgent) Tools() []tool.Tool {
	return s.tools
}

func (s *stubSurfaceAgent) UserTools() []tool.Tool {
	return s.userTools
}

func (s *stubSurfaceAgent) Info() agent.Info {
	return agent.Info{Name: "surface-agent"}
}

func (s *stubSurfaceAgent) SubAgents() []agent.Agent {
	return nil
}

func (s *stubSurfaceAgent) FindSubAgent(string) agent.Agent {
	return nil
}

func TestResolveBase_UserToolTrackingSkipsInvalidTools(t *testing.T) {
	agt := &stubSurfaceAgent{
		tools: []tool.Tool{
			nil,
			surfaceTool("base"),
		},
		userTools: []tool.Tool{
			nil,
			stubSurfaceTool{},
			surfaceTool("base"),
		},
	}
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(agt),
	)

	tools, userToolNames, hasTracking := ResolveBase(context.Background(), inv)

	require.Len(t, tools, 2)
	require.True(t, hasTracking)
	require.Equal(t, map[string]bool{"base": true}, userToolNames)
}

func TestApplyToolFilter_SkipsInvalidToolsAndSortsValidTools(t *testing.T) {
	filteredNames := []string{}
	opts := agent.NewRunOptions(agent.WithToolFilter(
		func(_ context.Context, tl tool.Tool) bool {
			filteredNames = append(filteredNames, tl.Declaration().Name)
			return tl.Declaration().Name != "drop"
		},
	))

	filtered := ApplyToolFilter(
		context.Background(),
		[]tool.Tool{
			surfaceTool("zeta"),
			nil,
			stubSurfaceTool{},
			surfaceTool("drop"),
			surfaceTool("alpha"),
		},
		nil,
		false,
		opts,
	)

	require.Equal(t, []string{"zeta", "drop", "alpha"}, filteredNames)
	requireToolNames(t, filtered, []string{"alpha", "zeta"})
}

func TestEffectiveWithExternal_AppendsAndClassifiesRunOptionTools(t *testing.T) {
	agt := &stubSurfaceAgent{
		tools:     []tool.Tool{surfaceTool("base")},
		userTools: []tool.Tool{surfaceTool("base")},
	}
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(agt),
		agent.WithInvocationRunOptions(agent.NewRunOptions(
			agent.WithAdditionalTools([]tool.Tool{
				nil,
				stubSurfaceTool{},
				surfaceTool("added"),
			}),
			agent.WithExternalTools([]tool.Tool{
				surfaceTool("external"),
				surfaceTool("base"),
			}),
		)),
	)

	tools, userToolNames, externalNames := EffectiveWithExternal(
		context.Background(),
		inv,
	)

	requireToolNames(t, tools, []string{"base", "added", "external"})
	require.Equal(t, map[string]bool{
		"base":     true,
		"added":    true,
		"external": true,
	}, userToolNames)
	require.Equal(t, map[string]bool{"external": true}, externalNames)
}

func TestApplyDeclarations_OverridesDeclarationAndPreservesCall(t *testing.T) {
	base := &stubCallableSurfaceTool{
		stubSurfaceTool: stubSurfaceTool{decl: &tool.Declaration{
			Name:        "search",
			Description: "old",
			InputSchema: &tool.Schema{
				Type: "object",
				Properties: map[string]*tool.Schema{
					"query": {Type: "string", Description: "old query"},
				},
			},
		}},
	}
	patched := itool.ApplyDeclarations([]tool.Tool{base}, []tool.Declaration{{
		Name:        "search",
		Description: "new",
		InputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"query": {Type: "string", Description: "new query"},
			},
		},
	}})
	require.Len(t, patched, 1)
	require.Equal(t, "new", patched[0].Declaration().Description)
	require.Equal(t, "new query", patched[0].Declaration().InputSchema.Properties["query"].Description)
	callable, ok := patched[0].(tool.CallableTool)
	require.True(t, ok)
	result, err := callable.Call(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, "called", result)
	require.True(t, base.called)
}

func TestApplyDeclarations_PreservesStreamableTool(t *testing.T) {
	base := &stubStreamableSurfaceTool{
		stubSurfaceTool: stubSurfaceTool{decl: &tool.Declaration{Name: "stream"}},
	}
	patched := itool.ApplyDeclarations([]tool.Tool{base}, []tool.Declaration{{
		Name:        "stream",
		Description: "patched",
	}})
	streamable, ok := patched[0].(tool.StreamableTool)
	require.True(t, ok)
	reader, err := streamable.StreamableCall(context.Background(), nil)
	require.NoError(t, err)
	require.NotNil(t, reader)
	require.True(t, base.called)
}

func TestApplyDeclarations_DoesNotUnwrapNamedLikeToolSemantics(t *testing.T) {
	original := &semanticSurfaceTool{
		stubCallableSurfaceTool: stubCallableSurfaceTool{
			stubSurfaceTool: stubSurfaceTool{decl: &tool.Declaration{Name: "named"}},
		},
	}
	base := &namedLikeSurfaceTool{
		original: original,
		decl:     &tool.Declaration{Name: "named"},
	}
	patched := itool.ApplyDeclarations([]tool.Tool{base}, []tool.Declaration{{
		Name:        "named",
		Description: "patched",
	}})
	_, ok := patched[0].(tool.StreamableTool)
	require.True(t, ok)
	callable, ok := patched[0].(tool.CallableTool)
	require.True(t, ok)
	result, err := callable.Call(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, "called", result)
	type skipper interface{ SkipSummarization() bool }
	type longRunner interface{ LongRunning() bool }
	type innerTextMode interface{ InnerTextMode() tool.InnerTextMode }
	type toolSetNamer interface{ ToolSetName() string }
	type stateDeltaForInvocation interface {
		StateDeltaForInvocation(*agent.Invocation, string, []byte, []byte) map[string][]byte
	}
	_, ok = patched[0].(skipper)
	require.False(t, ok)
	_, ok = patched[0].(longRunner)
	require.False(t, ok)
	_, ok = patched[0].(innerTextMode)
	require.False(t, ok)
	_, ok = patched[0].(toolSetNamer)
	require.False(t, ok)
	require.Equal(t, tool.ToolMetadata{}, tool.MetadataOf(patched[0]))
	require.False(t, tool.ShouldDefer(context.Background(), patched[0]))
	_, ok = patched[0].(stateDeltaForInvocation)
	require.False(t, ok)
}

func TestApplyDeclarations_DoesNotExposeOptionalRuntimeInterfaces(t *testing.T) {
	base := &semanticSurfaceTool{
		stubCallableSurfaceTool: stubCallableSurfaceTool{
			stubSurfaceTool: stubSurfaceTool{
				decl: &tool.Declaration{Name: "semantic"},
			},
		},
	}
	patched := itool.ApplyDeclarations([]tool.Tool{base}, []tool.Declaration{{
		Name:        "semantic",
		Description: "patched",
	}})
	type skipper interface{ SkipSummarization() bool }
	type longRunner interface{ LongRunning() bool }
	type streamInner interface{ StreamInner() bool }
	type innerTextMode interface{ InnerTextMode() tool.InnerTextMode }
	type toolSetNamer interface{ ToolSetName() string }
	type stateDeltaForInvocation interface {
		StateDeltaForInvocation(*agent.Invocation, string, []byte, []byte) map[string][]byte
	}
	_, ok := patched[0].(skipper)
	require.False(t, ok)
	_, ok = patched[0].(longRunner)
	require.False(t, ok)
	_, ok = patched[0].(streamInner)
	require.False(t, ok)
	_, ok = patched[0].(innerTextMode)
	require.False(t, ok)
	_, ok = patched[0].(toolSetNamer)
	require.False(t, ok)
	require.Equal(t, base, itool.ResolveDeclaration(patched[0]))
	require.Equal(t, tool.ToolMetadata{}, tool.MetadataOf(patched[0]))
	require.False(t, tool.ShouldDefer(context.Background(), patched[0]))
	_, ok = patched[0].(tool.PermissionChecker)
	require.False(t, ok)
	_, ok = patched[0].(stateDeltaForInvocation)
	require.False(t, ok)
}

func surfaceTool(name string) tool.Tool {
	return stubSurfaceTool{decl: &tool.Declaration{Name: name}}
}

func requireToolNames(t *testing.T, tools []tool.Tool, names []string) {
	t.Helper()
	got := make([]string, 0, len(tools))
	for _, tl := range tools {
		got = append(got, tl.Declaration().Name)
	}
	require.Equal(t, names, got)
}
