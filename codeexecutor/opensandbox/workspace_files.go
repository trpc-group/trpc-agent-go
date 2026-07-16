//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package opensandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	osb "github.com/alibaba/OpenSandbox/sdks/sandbox/go"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

// PutFiles writes files into the sandbox workspace using the SDK's
// native multipart UploadFiles API. Each PutFile is uploaded with its
// declared POSIX mode via FileMetadata.Mode.
func (r *workspaceRuntime) PutFiles(
	ctx context.Context,
	ws codeexecutor.Workspace,
	files []codeexecutor.PutFile,
) error {
	if len(files) == 0 {
		return nil
	}
	if err := r.validateWorkspace(ws); err != nil {
		return err
	}
	sb, err := r.sandbox()
	if err != nil {
		return err
	}

	entries := make([]osb.UploadFileEntry, 0, len(files))
	for _, f := range files {
		clean := path.Clean(filepath.ToSlash(f.Path))
		if clean == "." || clean == "/" || clean == "" {
			return fmt.Errorf("invalid file path: %s", f.Path)
		}
		finalPath := path.Join(ws.Path, clean)
		if !pathUnder(finalPath, ws.Path) {
			return fmt.Errorf("opensandbox: path %q escapes workspace", f.Path)
		}
		// Resolve symlinks in the parent directory to prevent a
		// symlink inside the workspace from redirecting writes
		// outside. Use resolveSandboxAncestor (not resolveSandboxPath)
		// because the parent may not yet exist — e.g. uploading
		// a/b/c/file.txt where a, b, and c are all new directories.
		resolvedParent, err := r.resolveSandboxAncestor(ctx, path.Dir(finalPath), ws.Path)
		if err != nil {
			return err
		}
		finalPath = path.Join(resolvedParent, path.Base(finalPath))
		// Guard against the final component being a pre-existing
		// symlink: resolveSandboxAncestor only resolves symlinks in
		// components that exist *at call time*, but if the final
		// component (e.g. "file.txt") already exists as a symlink to
		// an external path, UploadFiles would follow it and write
		// outside the workspace. Remove any pre-existing symlink at
		// the final path so the upload creates a fresh regular file.
		// This is safe because the caller's intent is to write a new
		// file at this path — overwriting a symlink with a regular
		// file is the expected behaviour.
		if err := r.removeSymlinkIfExists(ctx, finalPath, ws.Path); err != nil {
			return err
		}
		// Ensure the parent directory exists. The SDK's UploadFiles
		// creates intermediate directories, but we create them
		// explicitly to be safe across server versions.
		parent := resolvedParent
		if parent != "." && parent != "/" && parent != ws.Path {
			if err := sb.CreateDirectory(ctx, parent, osb.OctalMode(0o755)); err != nil {
				return fmt.Errorf("create directory %s: %w", parent, err)
			}
		}
		mode := f.Mode
		if mode == 0 {
			mode = 0o644
		}
		entries = append(entries, osb.UploadFileEntry{
			File: bytes.NewReader(f.Content),
			Options: osb.UploadFileOptions{
				FileName: path.Base(clean),
				Metadata: osb.FileMetadata{
					Path: finalPath,
					Mode: osb.OctalMode(os.FileMode(mode)),
				},
			},
		})
	}
	return sb.UploadFiles(ctx, entries)
}

// PutDirectory packs a host directory into tar.gz then uploads and
// extracts it in the sandbox under ws.Path/to. We use the SDK's
// UploadFiles API with one entry per file, walking the host tree with
// filepath.WalkDir and skipping non-regular entries (symlinks,
// devices, etc.) to prevent following symlinks outside hostPath.
func (r *workspaceRuntime) PutDirectory(
	ctx context.Context,
	ws codeexecutor.Workspace,
	hostPath string,
	to string,
) error {
	if strings.TrimSpace(hostPath) == "" {
		return errors.New("hostPath is empty")
	}
	if err := r.validateWorkspace(ws); err != nil {
		return err
	}
	abs, err := filepath.Abs(hostPath)
	if err != nil {
		return err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("hostPath is not a directory: %s", abs)
	}

	dest := ws.Path
	if to != "" {
		dest = path.Join(ws.Path, filepath.ToSlash(to))
	}
	if !pathUnder(dest, ws.Path) {
		return fmt.Errorf("opensandbox: destination %q escapes workspace", to)
	}
	// Resolve symlinks in dest to prevent a symlink inside the
	// workspace from redirecting directory uploads outside. Use
	// resolveSandboxAncestor because dest may not yet exist.
	resolvedDest, err := r.resolveSandboxAncestor(ctx, dest, ws.Path)
	if err != nil {
		return err
	}
	dest = resolvedDest

	sb, err := r.sandbox()
	if err != nil {
		return err
	}
	if err := sb.CreateDirectory(ctx, dest, osb.OctalMode(0o755)); err != nil {
		return fmt.Errorf("create directory %s: %w", dest, err)
	}

	return r.walkAndUpload(ctx, sb, abs, dest)
}

// uploadBatchSize is the maximum number of files uploaded in a single
// UploadFiles call during directory staging. Bounding the batch keeps
// the number of simultaneously open file descriptors well below the
// typical ulimit -n (1024), preventing StageDirectory from failing on
// large workspaces. Each batch's file handles are closed as soon as
// UploadFiles returns, before the next batch is opened.
const uploadBatchSize = 64

// walkAndUpload walks hostRoot with filepath.WalkDir and uploads files
// to destRoot in batches of uploadBatchSize. Empty subdirectories are
// created explicitly via sb.CreateDirectory so they survive in the
// sandbox even when they contain no files (matches the e2b adapter's
// tar TypeDir behaviour).
//
// Non-regular entries (symlinks, devices, sockets, fifos) are skipped:
// d.Info() reports Lstat semantics (it does not follow symlinks), so
// a symlink inside hostRoot cannot cause files outside hostRoot to be
// uploaded. This matches the e2b adapter's behaviour.
//
// Files are opened with os.Open and streamed via the io.Reader
// interface rather than buffered in memory with os.ReadFile, so
// staging a directory with large files does not materialize the full
// tree in the agent process. File handles are closed after each batch
// is uploaded, keeping the open-fd count bounded by uploadBatchSize
// rather than the total file count in the tree.
func (r *workspaceRuntime) walkAndUpload(
	ctx context.Context,
	sb *osb.Sandbox,
	hostRoot, destRoot string,
) error {
	var (
		entries   []osb.UploadFileEntry
		openFiles []*os.File
	)
	// flushBatch uploads the current batch and closes all its file
	// handles. Called when the batch reaches uploadBatchSize and once
	// more after the walk finishes.
	//
	// Before each upload, pre-existing symlinks at every target path
	// are removed in a single bash call. In PerSession mode,
	// previously executed code may have left symlinks at nested
	// destinations pointing outside the workspace; without this guard,
	// UploadFiles would follow them and write outside the workspace.
	// This mirrors the per-file removeSymlinkIfExists guard in
	// PutFiles, but batches the check into one bash call per upload
	// batch to avoid 2N round-trips for N files.
	flushBatch := func() error {
		if len(entries) == 0 {
			return nil
		}
		var rm strings.Builder
		rm.WriteString("for p in")
		for _, e := range entries {
			rm.WriteByte(' ')
			rm.WriteString(shellQuote(e.Options.Metadata.Path))
		}
		rm.WriteString("; do [ -L \"$p\" ] && rm -f \"$p\"; done")
		if _, err := r.runBash(ctx, rm.String(), defaultCreateTimeout); err != nil {
			return fmt.Errorf("opensandbox: batch remove symlinks before upload: %w", err)
		}
		err := sb.UploadFiles(ctx, entries)
		for _, f := range openFiles {
			_ = f.Close()
		}
		entries = entries[:0]
		openFiles = openFiles[:0]
		return err
	}
	walkErr := filepath.WalkDir(hostRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(hostRoot, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		remotePath := path.Join(destRoot, filepath.ToSlash(rel))

		if d.IsDir() {
			if err := sb.CreateDirectory(ctx, remotePath, osb.OctalMode(0o755)); err != nil {
				return fmt.Errorf("create directory %s: %w", remotePath, err)
			}
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		if !shouldUploadFile(info) {
			return nil
		}
		parent := path.Dir(remotePath)
		if parent != "." && parent != "/" && parent != destRoot {
			if err := sb.CreateDirectory(ctx, parent, osb.OctalMode(0o755)); err != nil {
				return fmt.Errorf("create directory %s: %w", parent, err)
			}
		}
		mode := info.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		openFiles = append(openFiles, f)
		entries = append(entries, osb.UploadFileEntry{
			File: f,
			Options: osb.UploadFileOptions{
				FileName: path.Base(remotePath),
				Metadata: osb.FileMetadata{
					Path: remotePath,
					Mode: osb.OctalMode(mode),
				},
			},
		})
		// Upload and close this batch once it reaches the size limit.
		if len(entries) >= uploadBatchSize {
			return flushBatch()
		}
		return nil
	})
	if walkErr != nil {
		// Close any pending handles on error.
		for _, f := range openFiles {
			_ = f.Close()
		}
		return fmt.Errorf("opensandbox: walk and upload %s: %w", hostRoot, walkErr)
	}
	// Flush any remaining entries after the walk completes.
	return flushBatch()
}

// StageDirectory stages a directory with options (ReadOnly).
func (r *workspaceRuntime) StageDirectory(
	ctx context.Context,
	ws codeexecutor.Workspace,
	src string,
	to string,
	opt codeexecutor.StageOptions,
) error {
	if err := r.PutDirectory(ctx, ws, src, to); err != nil {
		return err
	}
	if opt.ReadOnly {
		// Re-resolve dest to ensure chmod operates on the real path
		// (after symlink resolution), not a symlink that might point
		// outside the workspace. PutDirectory already validated this,
		// but we resolve again in case the filesystem changed.
		dest := ws.Path
		if to != "" {
			dest = path.Join(ws.Path, filepath.ToSlash(to))
		}
		resolvedDest, err := r.resolveSandboxAncestor(ctx, dest, ws.Path)
		if err != nil {
			return err
		}
		script := "chmod -R a-w " + shellQuote(resolvedDest)
		if _, err := r.runBash(ctx, script, defaultStageTimeout); err != nil {
			return err
		}
	}
	return nil
}

// shouldUploadFile returns true if the directory entry should be
// uploaded to the sandbox. Non-regular files (symlinks, devices,
// sockets, fifos) are skipped to prevent following symlinks outside
// hostPath, matching the e2b adapter's behaviour.
func shouldUploadFile(info os.FileInfo) bool {
	return info.Mode().IsRegular()
}

// pathUnder reports whether p is equal to base or nested below it.
// Both arguments are expected to be absolute POSIX paths.
func pathUnder(p, base string) bool {
	if base == "" || p == "" {
		return false
	}
	base = strings.TrimRight(base, "/")
	if p == base {
		return true
	}
	return strings.HasPrefix(p, base+"/")
}

// resolveSandboxPath resolves the real path of a target inside the
// sandbox using `readlink -f`, then verifies the resolved path is
// still under the workspace base. This prevents symlink-based escape:
// a symlink inside the workspace pointing to an external directory
// (e.g. /tmp/outside) would pass the lexical pathUnder check but
// cause writes/reads to land outside the workspace.
//
// The target must exist (readlink -f fails on non-existent paths when
// any component other than the last is missing). For targets that may
// not yet exist (e.g. a file about to be created, possibly under
// multiple new directories), use resolveSandboxAncestor instead.
//
// Returns the resolved path if it is under wsBase, or an error
// otherwise.
func (r *workspaceRuntime) resolveSandboxPath(
	ctx context.Context, target, wsBase string,
) (string, error) {
	script := "readlink -f " + shellQuote(target)
	out, err := r.runBash(ctx, script, defaultCreateTimeout)
	if err != nil {
		return "", fmt.Errorf(
			"opensandbox: resolve path %q: %w", target, err,
		)
	}
	resolved := strings.TrimSpace(out)
	if resolved == "" {
		return "", fmt.Errorf(
			"opensandbox: readlink -f returned empty for %q", target,
		)
	}
	if !pathUnder(resolved, wsBase) {
		return "", fmt.Errorf(
			"opensandbox: resolved path %q escapes workspace %q (symlink?)",
			resolved, wsBase,
		)
	}
	return resolved, nil
}

// removeSymlinkIfExists checks if targetPath is a symlink (using
// `test -L`, which does not follow the symlink) and, if so, removes it
// with `rm -f`. This prevents a pre-existing symlink at the final
// component of an upload path from redirecting the write to an external
// location. If targetPath does not exist or is a regular file, this is
// a no-op.
//
// The path is validated against wsBase before the rm to ensure the
// removal cannot be tricked into deleting files outside the workspace.
func (r *workspaceRuntime) removeSymlinkIfExists(
	ctx context.Context, targetPath, wsBase string,
) error {
	if !pathUnder(targetPath, wsBase) {
		// Already caught by earlier validation, but double-check.
		return fmt.Errorf(
			"opensandbox: path %q escapes workspace %q", targetPath, wsBase,
		)
	}
	// test -L returns true for symlinks (does not follow). A regular
	// file or non-existent path returns false. Only remove if it's a
	// symlink.
	out, err := r.runBash(ctx,
		"test -L "+shellQuote(targetPath)+" && echo yes || echo no",
		defaultCreateTimeout)
	if err != nil {
		return fmt.Errorf(
			"opensandbox: check symlink %q: %w", targetPath, err,
		)
	}
	if strings.TrimSpace(out) == "yes" {
		if _, err := r.runBash(ctx, "rm -f "+shellQuote(targetPath), defaultRmTimeout); err != nil {
			return fmt.Errorf(
				"opensandbox: remove symlink %q: %w", targetPath, err,
			)
		}
	}
	return nil
}

// resolveSandboxAncestor resolves the real path of a target that may
// not yet exist (e.g. a/b/c/file.txt where a, b, and c are all new).
//
// It uses `readlink -m` (--canonicalize-missing) which resolves
// symlinks in all *existing* path components (including intermediate
// and final components that already exist) while leaving non-existent
// components as-is. This is superior to the old ancestor-walk approach
// which only resolved the nearest existing ancestor and appended the
// non-existent tail verbatim — if a tail component was itself an
// existing symlink (e.g. target=/ws/a/b/c where /ws/a/b is a symlink
// to /etc and c doesn't exist), the old code would return /ws/a/b/c
// without resolving the /ws/a/b symlink, allowing writes to land in
// /etc/c.
//
// `readlink -m` also fixes the depth-limit issue: the old code walked
// up one component at a time, issuing a `test -e` round-trip per
// level. A deeply nested target (e.g. /a/b/c/.../z/file) required
// O(depth) remote calls with no upper bound. readlink -m resolves
// the entire path in a single call.
//
// After resolution, the result is verified to be under wsBase to
// prevent symlink escape.
func (r *workspaceRuntime) resolveSandboxAncestor(
	ctx context.Context, target, wsBase string,
) (string, error) {
	target = path.Clean(target)
	// Use readlink -m: canonicalizes existing symlink components,
	// leaves non-existent components as-is. Unlike readlink -f, it
	// does not fail when intermediate components don't exist.
	out, err := r.runBash(ctx, "readlink -m "+shellQuote(target), defaultCreateTimeout)
	if err != nil {
		return "", fmt.Errorf(
			"opensandbox: resolve ancestor %q: %w", target, err,
		)
	}
	resolved := strings.TrimSpace(out)
	if resolved == "" {
		return "", fmt.Errorf(
			"opensandbox: readlink -m returned empty for %q", target,
		)
	}
	if !pathUnder(resolved, wsBase) {
		return "", fmt.Errorf(
			"opensandbox: resolved path %q escapes workspace %q (symlink?)",
			resolved, wsBase,
		)
	}
	return resolved, nil
}
