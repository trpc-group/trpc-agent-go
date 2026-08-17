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
	"errors"
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
// workspace key derived from InvocationID is used so placeholder sessions
// cannot share a durable tool/skill name key, while still allowing multiple
// tool calls within the same invocation to reuse one workspace. The returned
// WorkspaceHandle exposes the entryToken required for cleanup via
// InvalidateWorkspaceHandle or ReleaseWorkspaceHandle.
//
// Callers that acquire an ephemeral handle are responsible for calling
// ReleaseWorkspaceHandle when the workspace is no longer needed, since
// ephemeral workspaces are not tracked by any session-level lifecycle.
func (r *Resolver) CreateWorkspaceHandle(
	ctx context.Context,
	eng codeexecutor.Engine,
	name string,
) (codeexecutor.WorkspaceHandle, error) {
	reg := r.reg
	if reg == nil {
		// Resolver must be constructed via NewResolver, which
		// guarantees r.reg != nil. A nil reg here means a zero-value
		// Resolver was used. Fail closed rather than lazily allocating
		// — lazy init is a data race when multiple goroutines call
		// CreateWorkspaceHandle concurrently (each would create its
		// own registry, and the loser's handles would be unreleaseable
		// via the winner's registry).
		return codeexecutor.WorkspaceHandle{}, errors.New(
			"workspacesession: Resolver not initialized via NewResolver")
	}
	if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil {
		ctx = withWorkspaceArtifactContext(ctx, inv)
	}
	sid := workspaceKey(ctx, name)
	if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil && inv.Session != nil {
		if strings.TrimSpace(inv.Session.ID) == "" {
			// Invalid (empty-ID) sessions must not share a workspace
			// via the tool/skill name key. Derive the key from
			// InvocationID so all tool calls within the same
			// invocation reuse one workspace (bounded leak of 1 per
			// invocation, not 1 per call). Fall back to a random UUID
			// only when InvocationID is also empty.
			suffix := strings.TrimSpace(inv.InvocationID)
			if suffix == "" {
				suffix = uuid.NewString()
			}
			sid = fmt.Sprintf("ephemeral-invocation-%s", suffix)
		}
	}
	return reg.AcquireHandle(ctx, eng.Manager(), sid)
}

// ReleaseWorkspaceHandle releases the registry entry identified by
// handle, cleaning up the underlying workspace via the manager that
// created it. This is the public cleanup path for workspaces acquired
// via CreateWorkspaceHandle, including ephemeral workspaces created for
// invalid sessions.
//
// Callers that acquire an ephemeral handle (empty session ID) SHOULD call
// this when done to avoid leaking the backend workspace. For non-ephemeral
// (session-scoped) workspaces, the handle is cached for reuse and should
// NOT be released until the session ends.
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
// The key is codeexecutor.SessionWorkspaceKey over (AppName, UserID, ID):
// "sess-" followed by 32 lowercase hex characters. It is a single
// filesystem-safe path segment (no separators, no ':' illegal on Windows,
// fixed length) and injective over the identity triple, so distinct
// sessions never share a workspace. Session.ID is required; empty or
// whitespace-only ID returns "".
//
// Breaking change: the encoding changed from "app/user/id" (or just "id"
// when fields were missing) to the hash format above. After an upgrade,
// existing PerSession workspaces on disk are orphaned because the same
// session now hashes to a different directory path. Callers that need to
// locate legacy workspaces should use LegacyKeyFromInvocation (which
// delegates to codeexecutor.LegacySessionWorkspaceKey) for a one-time
// migration (e.g. rename or symlink the old directory to the new key's
// path).
func KeyFromInvocation(inv *agent.Invocation) string {
	if inv == nil || inv.Session == nil {
		return ""
	}
	app := inv.Session.AppName
	user := inv.Session.UserID
	id := inv.Session.ID
	return codeexecutor.SessionWorkspaceKey(app, user, id)
}

// LegacyKeyFromInvocation reproduces the pre-migration workspace key format
// ("app/user/id" or just "id") for a one-time upgrade path. Callers should
// check whether a workspace exists at the legacy key's derived path and
// migrate it to the new KeyFromInvocation path if found.
func LegacyKeyFromInvocation(inv *agent.Invocation) string {
	if inv == nil || inv.Session == nil {
		return ""
	}
	return codeexecutor.LegacySessionWorkspaceKey(
		inv.Session.AppName, inv.Session.UserID, inv.Session.ID)
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
