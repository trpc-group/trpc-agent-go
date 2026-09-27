//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package teamtrace provides internal helpers for mounted team node ids.
package teamtrace

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	istructure "trpc.group/trpc-go/trpc-agent-go/internal/structure"
)

const memberTraceRootConfigsKey = "__trpc_agent_internal_team_member_trace_root__"

const memberSurfaceRootStateKey = "__trpc_agent_internal_team_member_surface_root_state__"

type memberMountContextKey struct{}

// CoordinatorLayout describes static coordinator-team node ids under one root.
type CoordinatorLayout struct {
	CoordinatorNodeID string
	MemberNodeIDs     []string
}

// MemberMount carries the concrete node ids for one mounted Team member call.
type MemberMount struct {
	TraceNodeID       string
	SurfaceRootNodeID string
}

// NewCoordinatorLayout allocates coordinator and member node ids like static export.
func NewCoordinatorLayout(rootNodeID string, members []agent.Agent) CoordinatorLayout {
	allocator := istructure.NewPathAllocator(rootNodeID)
	layout := CoordinatorLayout{
		CoordinatorNodeID: allocator.Next("coordinator"),
	}
	if len(members) == 0 {
		return layout
	}
	layout.MemberNodeIDs = make([]string, 0, len(members))
	for _, member := range members {
		memberName := ""
		if member != nil {
			memberName = member.Info().Name
		}
		layout.MemberNodeIDs = append(layout.MemberNodeIDs, allocator.Next(memberName))
	}
	return layout
}

// ContextWithMemberMount stores one mounted Team member path in ctx.
func ContextWithMemberMount(ctx context.Context, mount MemberMount) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if mount.TraceNodeID == "" || mount.SurfaceRootNodeID == "" {
		return ctx
	}
	return context.WithValue(ctx, memberMountContextKey{}, mount)
}

// MemberMountFromContext returns one mounted Team member path from ctx.
func MemberMountFromContext(ctx context.Context) (MemberMount, bool) {
	if ctx == nil {
		return MemberMount{}, false
	}
	mount, ok := ctx.Value(memberMountContextKey{}).(MemberMount)
	if !ok || mount.TraceNodeID == "" || mount.SurfaceRootNodeID == "" {
		return MemberMount{}, false
	}
	return mount, true
}

// RootNodeID returns the mounted surface lookup root node id for one team invocation.
func RootNodeID(inv *agent.Invocation, teamName string) string {
	if inv != nil {
		if nodeID := agent.InvocationSurfaceRootNodeID(inv); nodeID != "" {
			return nodeID
		}
	}
	return istructure.EscapeLocalName(teamName)
}

// TraceRootNodeID returns the execution trace root node id for one team invocation.
func TraceRootNodeID(inv *agent.Invocation, teamName string) string {
	if inv != nil {
		if nodeID := agent.InvocationTraceNodeID(inv); nodeID != "" {
			return nodeID
		}
	}
	return istructure.EscapeLocalName(teamName)
}

// CoordinatorNodeID returns the coordinator node id under one team root.
func CoordinatorNodeID(rootNodeID string) string {
	return istructure.JoinNodeID(rootNodeID, "coordinator")
}

// MemberNodeID returns the member node id under one team root.
func MemberNodeID(rootNodeID string, memberName string) string {
	return istructure.JoinNodeID(rootNodeID, memberName)
}

// WithMemberTraceRoot stores the mounted execution-trace root in custom configs.
func WithMemberTraceRoot(cfgs map[string]any, rootNodeID string) map[string]any {
	if rootNodeID == "" {
		return cfgs
	}
	out := copyConfigs(cfgs)
	out[memberTraceRootConfigsKey] = rootNodeID
	return out
}

// MemberTraceRoot returns the mounted execution-trace root from custom configs.
func MemberTraceRoot(cfgs map[string]any) string {
	if cfgs == nil {
		return ""
	}
	value, ok := cfgs[memberTraceRootConfigsKey]
	if !ok {
		return ""
	}
	rootNodeID, _ := value.(string)
	return rootNodeID
}

// SetMemberTraceRootForInvocation stores the mounted execution-trace root.
func SetMemberTraceRootForInvocation(
	inv *agent.Invocation,
	rootNodeID string,
) {
	agent.SetInvocationTeamMemberTraceRoot(inv, rootNodeID)
}

// ClearMemberTraceRootForInvocation removes the mounted execution-trace root.
func ClearMemberTraceRootForInvocation(inv *agent.Invocation) {
	agent.ClearInvocationTeamMemberTraceRoot(inv)
}

// SetMemberSurfaceRootForInvocation stores the mounted Team member surface root.
func SetMemberSurfaceRootForInvocation(
	inv *agent.Invocation,
	rootNodeID string,
) {
	if inv == nil || rootNodeID == "" {
		return
	}
	inv.SetState(memberSurfaceRootStateKey, rootNodeID)
}

// ClearMemberSurfaceRootForInvocation removes the mounted Team member surface root.
func ClearMemberSurfaceRootForInvocation(inv *agent.Invocation) {
	if inv == nil {
		return
	}
	inv.DeleteState(memberSurfaceRootStateKey)
}

// MemberSurfaceRootForInvocation returns the mounted Team member surface root.
func MemberSurfaceRootForInvocation(inv *agent.Invocation) string {
	if inv == nil {
		return ""
	}
	rootNodeID, _ := agent.GetStateValue[string](inv, memberSurfaceRootStateKey)
	return rootNodeID
}

// MemberTraceRootForInvocation returns the mounted execution-trace root.
func MemberTraceRootForInvocation(inv *agent.Invocation) string {
	if inv == nil {
		return ""
	}
	if rootNodeID := agent.InvocationTeamMemberTraceRoot(inv); rootNodeID != "" {
		return rootNodeID
	}
	return MemberTraceRoot(inv.RunOptions.CustomAgentConfigs)
}

func copyConfigs(in map[string]any) map[string]any {
	if in == nil {
		return make(map[string]any)
	}
	out := make(map[string]any, len(in)+1)
	for key, value := range in {
		out[key] = value
	}
	return out
}
