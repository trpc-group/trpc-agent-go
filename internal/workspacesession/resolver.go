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
	"trpc.group/trpc-go/trpc-agent-go/artifact"
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

// invocationWorkspaceManager limits cache reuse without adding a tool option.
// Managers without this capability keep the existing session-level reuse.
type invocationWorkspaceManager interface {
	WorkspacePerInvocation() bool
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
	handle, err := r.CreateWorkspaceHandle(ctx, eng, name)
	return handle.Workspace, err
}

// CreateWorkspaceHandle acquires the invocation-scoped workspace together with
// the registry token required for ABA-safe conditional invalidation.
func (r *Resolver) CreateWorkspaceHandle(
	ctx context.Context,
	eng codeexecutor.Engine,
	name string,
) (codeexecutor.WorkspaceHandle, error) {
	reg := r.reg
	if reg == nil {
		reg = codeexecutor.NewWorkspaceRegistry()
		r.reg = reg
	}
	mgr := eng.Manager()
	sid := name
	if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil {
		if key := KeyFromInvocation(inv); key != "" {
			sid = key
		}
		if scoped, ok := mgr.(invocationWorkspaceManager); ok &&
			scoped.WorkspacePerInvocation() {
			reg = inv.GetOrCreateWorkspaceRegistry(reg)
		}
		ctx = withWorkspaceArtifactContext(ctx, inv)
	}
	return reg.AcquireHandle(ctx, mgr, sid)
}

// InvalidateWorkspaceHandle conditionally removes the exact registry entry
// represented by handle.
func (r *Resolver) InvalidateWorkspaceHandle(
	handle codeexecutor.WorkspaceHandle,
) bool {
	if r == nil || r.reg == nil {
		return false
	}
	return r.reg.Invalidate(handle)
}

// KeyFromInvocation derives the shared workspace key for an invocation.
func KeyFromInvocation(inv *agent.Invocation) string {
	if inv == nil || inv.Session == nil {
		return ""
	}
	if inv.Session.AppName != "" && inv.Session.UserID != "" && inv.Session.ID != "" {
		return inv.Session.AppName + "/" + inv.Session.UserID + "/" + inv.Session.ID
	}
	return inv.Session.ID
}

// withWorkspaceArtifactContext mirrors internal/workspaceinput.withArtifactContext:
// inject artifact service when present, then session info when Session is set.
// Workspace init hooks and StageInputs during CreateWorkspace then resolve
// artifact:// references consistently with other artifact-backed staging paths.
func withWorkspaceArtifactContext(
	ctx context.Context,
	inv *agent.Invocation,
) context.Context {
	if inv == nil {
		return ctx
	}
	if inv.ArtifactService != nil {
		ctx = codeexecutor.WithArtifactService(ctx, inv.ArtifactService)
	}
	if inv.Session == nil {
		return ctx
	}
	return codeexecutor.WithArtifactSession(ctx, artifact.SessionInfo{
		AppName:   inv.Session.AppName,
		UserID:    inv.Session.UserID,
		SessionID: inv.Session.ID,
	})
}
