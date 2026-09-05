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
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

// legacyWorkspaceKeyCandidates returns every workspace key form that a
// pre-encoding-change binary could have persisted for the session identity.
//
// Two historical derivations existed and both must be probed:
//   - the sandbox executor's executionIDFromContext joined each non-empty
//     session field with "/" ("app/user/id", "app/id", "user/id", or "id");
//   - workspacesession.KeyFromInvocation — used by the flow processor,
//     skill runs, and openclaw — produced "app/user/id" only when both app
//     and user were present, otherwise just "id".
//
// For sessions with partial identity the two forms differ, and different
// call paths could have persisted either shape for the same session, so
// migration probes all of them and refuses to guess when several exist.
func legacyWorkspaceKeyCandidates(app, user, id string) []string {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	var parts []string
	if app != "" {
		parts = append(parts, app)
	}
	if user != "" {
		parts = append(parts, user)
	}
	parts = append(parts, id)
	joined := strings.Join(parts, "/")
	resolverForm := codeexecutor.LegacySessionWorkspaceKey(app, user, id)
	if joined == resolverForm {
		return []string{joined}
	}
	return []string{joined, resolverForm}
}

// resolveLegacyWorkspaceKeys decides which legacy keys (if any) a
// CreateWorkspace call with the given execID should consider migrating from.
//
// An explicitly attached context value (withLegacyWorkspaceKey) wins as an
// override. Otherwise the keys are derived by shape: framework callers do
// not attach the private context value, they simply pass
// workspacesession.KeyFromInvocation(invocation) — which equals
// codeexecutor.SessionWorkspaceKey(app, user, id) — as the workspace ID.
// When execID matches that value for the invocation's session, the legacy
// forms of the same session's key are returned so the runtime can migrate
// the pre-encoding-change directory. Any other execID (explicit
// caller-chosen IDs, ephemeral keys, "default") returns nil and skips
// migration: those IDs are identical on old and new binaries, so no legacy
// directory exists under a different name.
func resolveLegacyWorkspaceKeys(ctx context.Context, execID string) []string {
	if legacy := legacyWorkspaceKeyFromContext(ctx); legacy != "" {
		return []string{legacy}
	}
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.Session == nil {
		return nil
	}
	sessKey := codeexecutor.SessionWorkspaceKey(
		inv.Session.AppName, inv.Session.UserID, inv.Session.ID)
	if sessKey == "" || execID != sessKey {
		return nil
	}
	return legacyWorkspaceKeyCandidates(
		inv.Session.AppName, inv.Session.UserID, inv.Session.ID)
}

// migrateLegacyWorkspace upgrades a pre-encoding-change PerSession workspace
// to the current key layout. Legacy binaries persisted the workspace under
// one of several historical key forms (see legacyWorkspaceKeyCandidates);
// after the encoding change the same session resolves to a different
// directory and the old one would be orphaned.
//
// Semantics:
//   - No legacy directory on disk (fresh install or already migrated):
//     returns nil silently.
//   - A legacy path that exists but is not a plain directory (symlink,
//     regular file, ...) is an unexpected type and returns an error instead
//     of silently continuing with a fresh workspace.
//   - Several legacy forms existing at once for the same session is
//     ambiguous: which directory holds the state to upgrade cannot be
//     decided safely, so migration fails rather than guesses. Ambiguity is
//     only fatal when a migration would actually run; when the destination
//     already exists (already upgraded or newer data present) the legacy
//     directories are preserved untouched and no choice is needed.
//   - Permission/I/O failures while probing are propagated, never treated
//     as "nothing to migrate".
//   - The rename step is revalidated: after moving the legacy directory to
//     the new path, the destination is Lstat'ed again so a source swapped
//     to a symlink between the initial check and the rename cannot be
//     written through on layout creation.
//
// Migration applies only to session-persistent workspaces; per-turn and
// temporary workspaces are not reused across runs, so their legacy
// directories are left untouched. No data is ever deleted: when the
// destination already exists the legacy directories are preserved as-is.
// Concurrent-safe: if another goroutine wins the rename, the loser detects
// the destination exists and returns nil.
func (r *Runtime) migrateLegacyWorkspace(newKey string, legacyKeys []string) error {
	if r.sessionPolicy.Persistence != SessionPersistencePerSession {
		return nil
	}
	if newKey == "" || len(legacyKeys) == 0 {
		return nil
	}
	// workspacePathForID splits keys on "/" and sanitizes each segment,
	// so a legacy "app/user/id" key resolves to the same directory a
	// pre-change binary used, while the new "sess-<hex>" key is a single
	// segment that sanitizeID passes through unchanged.
	newPath, _ := workspacePathForID(r.root, newKey)
	if newPath == "" {
		return nil
	}
	// Check the destination first. Ambiguous or unexpected legacy
	// forms are only fatal when a migration would actually run; an
	// already-upgraded sess-* directory must stay usable even if
	// several historical directories were preserved beside it.
	exists, err := validateMigrationDestination(r.root, newPath)
	if err != nil {
		return err
	}
	if exists {
		// Destination already present (already migrated, or the session
		// already created a new-style workspace): keep the legacy
		// directory untouched rather than overwrite newer data.
		return nil
	}
	found, err := probeLegacyWorkspace(r.root, newPath, newKey, legacyKeys)
	if err != nil {
		return err
	}
	if found == "" {
		return nil
	}
	// Re-validate immediately before the rename so an intermediate
	// component swapped for a symlink after the probe cannot redirect
	// os.Rename onto a directory outside the configured root.
	if err := inspectContainedPlainDir(r.root, found, "legacy workspace"); err != nil {
		return err
	}
	if err := inspectContainedPlainDir(r.root, filepath.Dir(newPath), "migration destination"); err != nil {
		return err
	}
	if err := os.Rename(found, newPath); err != nil {
		// A concurrent goroutine may have won the rename; if the
		// destination now exists (as a valid plain directory) the
		// upgrade already happened.
		exists, statErr := validateMigrationDestination(r.root, newPath)
		if statErr != nil {
			return statErr
		}
		if exists {
			return nil
		}
		return err
	}
	// Revalidate the destination: if the source was swapped for a symlink
	// between the Lstat probe and the rename, os.Rename moved the link
	// itself and layout creation below would write through it, outside
	// the configured root. Undo the move (best effort) and fail.
	if err := validateMigratedWorkspace(newPath); err != nil {
		_ = os.Rename(newPath, found)
		return fmt.Errorf(
			"sandbox: legacy workspace %s changed during migration: %w", found, err)
	}
	return nil
}

// validateMigrationDestination reports whether an already-present
// migration destination is a plain directory contained under root.
// Every component beneath root is Lstat'ed so a symlink planted at
// the sess-* leaf or in an ancestor (for example root/sandbox) is
// never followed: accepting it would let the subsequent
// MkdirAll/EnsureLayout in CreateWorkspace write outside the
// configured root. Symlinks and non-directories are rejected; a
// missing destination returns exists=false, nil.
func validateMigrationDestination(root, newPath string) (bool, error) {
	if err := inspectContainedPlainDir(root, newPath, "migration destination"); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// probeLegacyWorkspace scans the historical key forms for an existing
// legacy directory to migrate and returns its path, or "" when none
// exists. It fails on unexpected entry types (symlink, non-directory),
// on ambiguity between several existing forms, and on I/O errors, so
// migration never silently discards persisted state behind a fresh
// empty workspace.
func probeLegacyWorkspace(root, newPath, newKey string, legacyKeys []string) (string, error) {
	var found string
	for _, legacyKey := range legacyKeys {
		if legacyKey == "" || legacyKey == newKey {
			continue
		}
		oldPath, _ := workspacePathForID(root, legacyKey)
		if oldPath == "" || oldPath == newPath {
			continue
		}
		// Inspect every component beneath root with Lstat. Historical
		// keys such as "app/user/id" become nested paths; a symlink
		// in a non-final component is followed by pathname resolution
		// and by os.Rename, so Lstat(oldPath) alone cannot prove the
		// workspace is contained in root.
		if err := inspectContainedPlainDir(root, oldPath, "legacy workspace"); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			// Propagate permission/I/O failures and unexpected types
			// instead of silently treating them as "nothing to
			// migrate" — that would orphan persisted state behind a
			// fresh empty workspace, or move a directory reached
			// through a symlink.
			return "", err
		}
		if found != "" {
			return "", fmt.Errorf(
				"sandbox: ambiguous legacy workspaces %s and %s both exist for session; refusing migration",
				found, oldPath)
		}
		found = oldPath
	}
	return found, nil
}

// inspectContainedPlainDir verifies that target is a plain directory
// reached from root without following any path component. root is the
// trust anchor and is not itself required to be a non-symlink; every
// component beneath it is Lstat'ed and must be a directory, never a
// symlink. A missing component is returned as the raw Lstat error so
// callers can treat os.IsNotExist as "nothing here".
func inspectContainedPlainDir(root, target, kind string) error {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("sandbox: %s %s is outside workspace root %s", kind, target, root)
	}
	if rel == "." {
		return lstatPlainDir(target, target, kind)
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			return fmt.Errorf("sandbox: %s %s is outside workspace root %s", kind, target, root)
		}
		current = filepath.Join(current, part)
		if err := lstatPlainDir(current, target, kind); err != nil {
			return err
		}
	}
	return nil
}

func lstatPlainDir(path, display, kind string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		if path == display {
			return fmt.Errorf(
				"sandbox: %s %s is a symlink; refusing migration", kind, path)
		}
		return fmt.Errorf(
			"sandbox: %s path %s contains a symlink at %s; refusing migration",
			kind, display, path)
	}
	if !info.IsDir() {
		return fmt.Errorf(
			"sandbox: %s %s is not a directory; refusing migration", kind, path)
	}
	return nil
}

// validateMigratedWorkspace verifies that a freshly migrated workspace path
// is a plain directory rather than a symlink or other unexpected type.
func validateMigratedWorkspace(newPath string) error {
	info, err := os.Lstat(newPath)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("migrated path %s is a symlink", newPath)
	}
	if !info.IsDir() {
		return fmt.Errorf("migrated path %s is not a directory", newPath)
	}
	return nil
}
