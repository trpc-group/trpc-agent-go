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
	"fmt"
	"strings"

	"github.com/google/uuid"

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
	return localexec.New().Engine()
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
//
// If the invocation carries a Session without a stable ID, an ephemeral
// workspace key (UUID-based) is used so placeholder sessions cannot share
// a durable tool/skill name key. The returned WorkspaceHandle exposes
// the entryToken required for cleanup via InvalidateWorkspaceHandle or
// ReleaseWorkspaceHandle.
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
	if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil {
		ctx = withWorkspaceArtifactContext(ctx, inv)
	}
	sid := workspaceKey(ctx, name)
	if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil && inv.Session != nil {
		if strings.TrimSpace(inv.Session.ID) == "" {
			// Invalid (empty-ID) sessions must not share a workspace
			// via the tool/skill name key. Use a random UUID suffix
			// so each invalid session gets a unique workspace. The
			// returned WorkspaceHandle carries the entryToken needed
			// for explicit cleanup via ReleaseWorkspaceHandle.
			sid = fmt.Sprintf("ephemeral-empty-session-%s", uuid.NewString())
		}
	}
	return reg.AcquireHandle(ctx, eng.Manager(), sid)
}

// ReleaseWorkspaceHandle releases the registry entry identified by
// handle, cleaning up the underlying workspace. This is the public
// cleanup path for workspaces acquired via CreateWorkspaceHandle,
// including ephemeral workspaces created for invalid sessions.
func (r *Resolver) ReleaseWorkspaceHandle(
	ctx context.Context,
	handle codeexecutor.WorkspaceHandle,
) error {
	if r == nil || r.reg == nil {
		return nil
	}
	return r.reg.ReleaseHandle(ctx, handle)
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

func workspaceKey(ctx context.Context, fallback string) string {
	if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil {
		if key := KeyFromInvocation(inv); key != "" {
			return key
		}
	}
	return fallback
}

// KeyFromInvocation derives the shared workspace key for an invocation.
//
// Encoding is injective over (AppName, UserID, ID) via length prefixes.
// Session.ID is required; empty/whitespace ID returns "".
//
// Breaking change: the encoding changed from "app/user/id" (or just "id"
// when fields were missing) to a length-prefixed "len:app/len:user/len:id"
// format to prevent separator-collision attacks. After an upgrade, existing
// PerSession workspaces on disk are orphaned because the same session now
// hashes to a different directory path. Callers that need to locate legacy
// workspaces should use LegacyKeyFromInvocation for a one-time migration
// (e.g. rename or symlink the old directory to the new key's path).
func KeyFromInvocation(inv *agent.Invocation) string {
	if inv == nil || inv.Session == nil {
		return ""
	}
	app := inv.Session.AppName
	user := inv.Session.UserID
	id := inv.Session.ID
	if strings.TrimSpace(id) == "" {
		return ""
	}
	return fmt.Sprintf("%d:%s/%d:%s/%d:%s",
		len(app), app, len(user), user, len(id), id)
}

// LegacyKeyFromInvocation reproduces the pre-migration workspace key format
// ("app/user/id" or just "id") for a one-time upgrade path. Callers should
// check whether a workspace exists at the legacy key's derived path and
// migrate it to the new KeyFromInvocation path if found.
func LegacyKeyFromInvocation(inv *agent.Invocation) string {
	if inv == nil || inv.Session == nil {
		return ""
	}
	app := inv.Session.AppName
	user := inv.Session.UserID
	id := inv.Session.ID
	if strings.TrimSpace(id) == "" {
		return ""
	}
	if app != "" && user != "" {
		return app + "/" + user + "/" + id
	}
	return id
}

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
