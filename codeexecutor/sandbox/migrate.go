//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sandbox

import (
	"context"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

type legacyWorkspaceKey struct{}

// withLegacyWorkspaceKey attaches the pre-encoding-change session workspace
// key to ctx so Runtime.CreateWorkspace can migrate the legacy directory to
// the current key layout. An empty legacyKey disables migration for the call.
func withLegacyWorkspaceKey(ctx context.Context, legacyKey string) context.Context {
	return context.WithValue(ctx, legacyWorkspaceKey{}, legacyKey)
}

// legacyWorkspaceKeyFromContext returns the legacy session workspace key
// attached by withLegacyWorkspaceKey, or "" when absent.
func legacyWorkspaceKeyFromContext(ctx context.Context) string {
	key, _ := ctx.Value(legacyWorkspaceKey{}).(string)
	return key
}

// resolveLegacyWorkspaceKey decides which legacy key (if any) a
// CreateWorkspace call with the given execID should migrate from.
//
// An explicitly attached context value (withLegacyWorkspaceKey) wins as an
// override. Otherwise the key is derived by shape: framework callers do not
// attach the private context value, they simply pass
// workspacesession.KeyFromInvocation(invocation) — which equals
// codeexecutor.SessionWorkspaceKey(app, user, id) — as the workspace ID.
// When execID matches that value for the invocation's session, the legacy
// form of the same session's key is returned so the runtime can migrate the
// pre-encoding-change directory. Any other execID (explicit caller-chosen
// IDs, ephemeral keys, "default") returns "" and skips migration: those IDs
// are identical on old and new binaries, so no legacy directory exists
// under a different name.
func resolveLegacyWorkspaceKey(ctx context.Context, execID string) string {
	if legacy := legacyWorkspaceKeyFromContext(ctx); legacy != "" {
		return legacy
	}
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.Session == nil {
		return ""
	}
	sessKey := codeexecutor.SessionWorkspaceKey(
		inv.Session.AppName, inv.Session.UserID, inv.Session.ID)
	if sessKey == "" || execID != sessKey {
		return ""
	}
	return codeexecutor.LegacySessionWorkspaceKey(
		inv.Session.AppName, inv.Session.UserID, inv.Session.ID)
}

// migrateLegacyWorkspace upgrades a pre-encoding-change PerSession workspace
// to the current key layout. Legacy binaries derived the workspace path from
// the legacy key format (app/user/id or id); after the encoding change the
// same session resolves to a different directory and the old one would be
// orphaned. Best effort: when no legacy directory exists (fresh install or
// already migrated) it returns silently. Concurrent-safe: if another
// goroutine wins the rename, the loser detects the destination exists and
// continues.
//
// Migration applies only to session-persistent workspaces; per-turn and
// temporary workspaces are not reused across runs, so their legacy
// directories are left untouched. No data is ever deleted: when the
// destination already exists the legacy directory is preserved as-is.
func (r *Runtime) migrateLegacyWorkspace(newKey, legacyKey string) error {
	if r.sessionPolicy.Persistence != SessionPersistencePerSession {
		return nil
	}
	if legacyKey == "" || newKey == "" || legacyKey == newKey {
		return nil
	}
	// workspacePathForID splits keys on "/" and sanitizes each segment,
	// so the legacy "app/user/id" key resolves to the same directory a
	// pre-change binary used, while the new "sess-<hex>" key is a single
	// segment that sanitizeID passes through unchanged.
	oldPath, _ := workspacePathForID(r.root, legacyKey)
	newPath, _ := workspacePathForID(r.root, newKey)
	if oldPath == newPath {
		return nil
	}
	// Lstat (not Stat) so the final legacy path component is not
	// followed: a workspace root that is itself a symlink would otherwise
	// be moved by os.Rename and then written through on layout creation,
	// escaping the configured workspace root.
	info, err := os.Lstat(oldPath)
	if os.IsNotExist(err) {
		// No legacy directory on disk (fresh install or already
		// migrated): nothing to upgrade.
		return nil
	}
	if err != nil {
		// Propagate permission/I/O failures instead of silently treating
		// them as "nothing to migrate" — that would orphan persisted
		// state and leave a fresh empty workspace in its place.
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		// A symlinked legacy root is untrusted (may point outside the
		// configured root). Never move or follow it; leave it in place.
		return nil
	}
	if !info.IsDir() {
		return nil
	}
	if _, err := os.Stat(newPath); err == nil {
		// Destination already present (already migrated, or the session
		// already created a new-style workspace): keep the legacy
		// directory untouched rather than overwrite newer data.
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		// A concurrent goroutine may have won the rename; if the
		// destination now exists the upgrade already happened.
		if _, statErr := os.Stat(newPath); statErr == nil {
			return nil
		}
		return err
	}
	return nil
}
