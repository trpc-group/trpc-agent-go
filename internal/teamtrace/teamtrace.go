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
	"trpc.group/trpc-go/trpc-agent-go/agent"
	istructure "trpc.group/trpc-go/trpc-agent-go/internal/structure"
)

const memberTraceRootConfigsKey = "__trpc_agent_internal_team_member_trace_root__"

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

// WithMemberTraceRoot stores the mounted team root in custom configs.
func WithMemberTraceRoot(cfgs map[string]any, rootNodeID string) map[string]any {
	if rootNodeID == "" {
		return cfgs
	}
	out := copyConfigs(cfgs)
	out[memberTraceRootConfigsKey] = rootNodeID
	return out
}

// MemberTraceRoot returns the mounted team root from custom configs.
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

// SetMemberTraceRootForInvocation stores the mounted team root on one invocation.
func SetMemberTraceRootForInvocation(
	inv *agent.Invocation,
	rootNodeID string,
) {
	agent.SetInvocationTeamMemberTraceRoot(inv, rootNodeID)
}

// ClearMemberTraceRootForInvocation removes the mounted team root from one invocation.
func ClearMemberTraceRootForInvocation(inv *agent.Invocation) {
	agent.ClearInvocationTeamMemberTraceRoot(inv)
}

// MemberTraceRootForInvocation returns the mounted team root for one invocation.
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
