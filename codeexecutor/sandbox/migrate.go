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
	info, err := os.Stat(oldPath)
	if err != nil || !info.IsDir() {
		// No legacy directory on disk (fresh install or already
		// migrated): nothing to upgrade.
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
