//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package workspacesession

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

// Resolver owns shared engine and session-workspace resolution for tools
// that should operate on the same invocation workspace.
type Resolver struct {
	exec codeexecutor.CodeExecutor
	reg  *codeexecutor.WorkspaceRegistry
}

// NewResolver creates a workspace-session resolver backed by a single
// registry so multiple tools can share the same session workspace.
func NewResolver(
	exec codeexecutor.CodeExecutor,
	reg *codeexecutor.WorkspaceRegistry,
) *Resolver {
	if reg == nil {
		reg = codeexecutor.NewWorkspaceRegistry()
	}
	return &Resolver{
		exec: exec,
		reg:  reg,
	}
}

// EnsureEngine gets an engine from the configured executor or falls back
// to the local runtime when no EngineProvider is available.
func (r *Resolver) EnsureEngine() codeexecutor.Engine {
	if r != nil {
		if ep, ok := r.exec.(codeexecutor.EngineProvider); ok && ep != nil {
			if e := ep.Engine(); e != nil {
				return e
			}
		}
	}
	log.Warnf(
		"workspacesession: falling back to local engine; " +
			"executor does not expose EngineProvider",
	)
	rt := localexec.NewRuntime("")
	return codeexecutor.NewEngine(rt, rt, rt)
}

// CreateWorkspace acquires the invocation-scoped workspace for a tool run.
func (r *Resolver) CreateWorkspace(
	ctx context.Context,
	eng codeexecutor.Engine,
	name string,
) (codeexecutor.Workspace, error) {
	reg := r.reg
	if reg == nil {
		reg = codeexecutor.NewWorkspaceRegistry()
		r.reg = reg
	}
	sid := name
	if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil {
		if inv.Session != nil && inv.Session.ID != "" {
			sid = inv.Session.ID
		}
	}
	return reg.Acquire(ctx, eng.Manager(), sid)
}
