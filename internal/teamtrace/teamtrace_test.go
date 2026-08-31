//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package teamtrace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/surfacepatch"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type layoutAgent string

func (a layoutAgent) Run(
	context.Context,
	*agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}

func (a layoutAgent) Tools() []tool.Tool { return nil }

func (a layoutAgent) Info() agent.Info { return agent.Info{Name: string(a)} }

func (a layoutAgent) SubAgents() []agent.Agent { return nil }

func (a layoutAgent) FindSubAgent(string) agent.Agent { return nil }

func TestNewCoordinatorLayout_UsesStaticExportOrder(t *testing.T) {
	layout := NewCoordinatorLayout(
		"workflow/team",
		[]agent.Agent{
			layoutAgent("member/one"),
			layoutAgent("member~two"),
		},
	)
	require.Equal(t, "workflow/team/coordinator", layout.CoordinatorNodeID)
	require.Equal(t, []string{
		"workflow/team/member~1one",
		"workflow/team/member~0two",
	}, layout.MemberNodeIDs)
}

func TestNewCoordinatorLayout_HandlesEmptyAndNilMembers(t *testing.T) {
	empty := NewCoordinatorLayout("workflow/team", nil)
	require.Equal(t, "workflow/team/coordinator", empty.CoordinatorNodeID)
	require.Empty(t, empty.MemberNodeIDs)
	layout := NewCoordinatorLayout(
		"workflow/team",
		[]agent.Agent{
			nil,
			layoutAgent(""),
		},
	)
	require.Equal(t, []string{
		"workflow/team/_",
		"workflow/team/_~2",
	}, layout.MemberNodeIDs)
}

func TestMemberMountContextHelpers(t *testing.T) {
	mount := MemberMount{
		TraceNodeID:       "trace/team/member",
		SurfaceRootNodeID: "surface/team/member",
	}
	ctx := ContextWithMemberMount(context.Background(), mount)
	got, ok := MemberMountFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, mount, got)
	ctx = ContextWithMemberMount(nil, mount)
	got, ok = MemberMountFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, mount, got)
	ctx = ContextWithMemberMount(context.Background(), MemberMount{
		TraceNodeID: "trace/team/member",
	})
	_, ok = MemberMountFromContext(ctx)
	require.False(t, ok)
	_, ok = MemberMountFromContext(context.WithValue(
		context.Background(),
		memberMountContextKey{},
		MemberMount{TraceNodeID: "trace/team/member"},
	))
	require.False(t, ok)
	_, ok = MemberMountFromContext(context.Background())
	require.False(t, ok)
	_, ok = MemberMountFromContext(nil)
	require.False(t, ok)
}

func TestRootNodeID_PrefersMountedSurfaceRoot(t *testing.T) {
	inv := agent.NewInvocation(
		agent.WithInvocationTraceNodeID("trace/team"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			CustomAgentConfigs: surfacepatch.WithRootNodeID(
				nil,
				"workflow/team",
			),
		}),
	)
	require.Equal(t, "workflow/team", RootNodeID(inv, "team"))
}

func TestRootNodeID_FallsBackToTraceNodeAndTeamName(t *testing.T) {
	traceInv := agent.NewInvocation(
		agent.WithInvocationTraceNodeID("trace/team"),
	)
	require.Equal(t, "trace/team", RootNodeID(traceInv, "team"))
	require.Equal(t, "team", RootNodeID(nil, "team"))
}

func TestTraceRootNodeID_PrefersTraceNodeAndFallsBackToTeamName(t *testing.T) {
	traceInv := agent.NewInvocation(
		agent.WithInvocationTraceNodeID("trace/team"),
		agent.WithInvocationRunOptions(agent.RunOptions{
			CustomAgentConfigs: surfacepatch.WithRootNodeID(
				nil,
				"workflow/team",
			),
		}),
	)
	require.Equal(t, "trace/team", TraceRootNodeID(traceInv, "team"))
	require.Equal(t, "team", TraceRootNodeID(nil, "team"))
}

func TestTeamTraceNodeIDHelpers(t *testing.T) {
	require.Equal(t, "workflow/team/coordinator", CoordinatorNodeID("workflow/team"))
	require.Equal(t, "workflow/team/member", MemberNodeID("workflow/team", "member"))
}

func TestMemberTraceRoot_ConfigHelpers(t *testing.T) {
	cfgs := map[string]any{"keep": "value"}
	stored := WithMemberTraceRoot(cfgs, "workflow/team")
	require.Equal(t, "workflow/team", MemberTraceRoot(stored))
	require.Equal(t, "value", stored["keep"])
	require.Equal(t, "value", cfgs["keep"])
	require.Empty(t, MemberTraceRoot(nil))
	require.Empty(t, MemberTraceRoot(map[string]any{memberTraceRootConfigsKey: 123}))
	require.Equal(t, cfgs, WithMemberTraceRoot(cfgs, ""))
}

func TestMemberTraceRootForInvocation_PrefersInvocationStateAndFallsBackToConfigs(t *testing.T) {
	inv := agent.NewInvocation(
		agent.WithInvocationRunOptions(agent.RunOptions{
			CustomAgentConfigs: WithMemberTraceRoot(nil, "workflow/team/config"),
		}),
	)
	require.Equal(t, "workflow/team/config", MemberTraceRootForInvocation(inv))

	SetMemberTraceRootForInvocation(inv, "workflow/team/state")
	require.Equal(t, "workflow/team/state", MemberTraceRootForInvocation(inv))

	ClearMemberTraceRootForInvocation(inv)
	require.Equal(t, "workflow/team/config", MemberTraceRootForInvocation(inv))
}

func TestMemberSurfaceRootForInvocation_StateHelpers(t *testing.T) {
	var nilInv *agent.Invocation
	SetMemberSurfaceRootForInvocation(nilInv, "workflow/team")
	ClearMemberSurfaceRootForInvocation(nilInv)
	require.Empty(t, MemberSurfaceRootForInvocation(nilInv))
	inv := agent.NewInvocation()
	require.Empty(t, MemberSurfaceRootForInvocation(inv))
	SetMemberSurfaceRootForInvocation(inv, "workflow/team")
	require.Equal(t, "workflow/team", MemberSurfaceRootForInvocation(inv))
	SetMemberSurfaceRootForInvocation(inv, "")
	require.Equal(t, "workflow/team", MemberSurfaceRootForInvocation(inv))
	ClearMemberSurfaceRootForInvocation(inv)
	require.Empty(t, MemberSurfaceRootForInvocation(inv))
}

func TestMemberTraceRootForInvocation_NilAndEmptyInput(t *testing.T) {
	var nilInv *agent.Invocation
	SetMemberTraceRootForInvocation(nilInv, "workflow/team")
	ClearMemberTraceRootForInvocation(nilInv)
	require.Empty(t, MemberTraceRootForInvocation(nilInv))

	inv := agent.NewInvocation()
	SetMemberTraceRootForInvocation(inv, "")
	require.Empty(t, MemberTraceRootForInvocation(inv))
}
