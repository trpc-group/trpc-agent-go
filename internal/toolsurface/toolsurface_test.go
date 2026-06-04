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
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type stubSurfaceTool struct {
	decl *tool.Declaration
}

func (s stubSurfaceTool) Declaration() *tool.Declaration {
	return s.decl
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
