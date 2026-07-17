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
		if !pathUnder(finalPath, ws.Path) {
			return fmt.Errorf("opensandbox: path %q escapes workspace", f.Path)
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
	// Immediately before UploadFiles: re-resolve parents (TOCTOU between
	// CreateDirectory and upload) and remove any leaf symlinks. Process
	// in uploadBatchSize chunks so a large PutFiles call cannot build an
	// ARG_MAX-sized shell command or one giant multipart body.
	for start := 0; start < len(entries); start += uploadBatchSize {
		end := start + uploadBatchSize
		if end > len(entries) {
			end = len(entries)
		}
		batch := entries[start:end]
		for i := range batch {
			p := batch[i].Options.Metadata.Path
			resolvedParent, err := r.resolveSandboxAncestor(ctx, path.Dir(p), ws.Path)
			if err != nil {
				return err
			}
			finalPath := path.Join(resolvedParent, path.Base(p))
			if !pathUnder(finalPath, ws.Path) {
				return fmt.Errorf("opensandbox: path %q escapes workspace", finalPath)
			}
			batch[i].Options.Metadata.Path = finalPath
		}
		if err := r.removeSymlinksBatch(ctx, uploadPaths(batch), ws.Path); err != nil {
			return err
		}
		if err := sb.UploadFiles(ctx, batch); err != nil {
			return err
		}
	}
	return nil
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

	return r.walkAndUpload(ctx, sb, abs, dest, ws.Path)
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
//
// wsBase is the workspace root used for symlink-escape checks. Every
// destination parent is resolved with resolveSandboxAncestor before
// CreateDirectory/UploadFiles so an intermediate directory that is a
// pre-existing symlink outside the workspace cannot redirect writes
// (e.g. dest/hijack -> /tmp/outside, upload dest/hijack/file.txt).
func (r *workspaceRuntime) walkAndUpload(
	ctx context.Context,
	sb *osb.Sandbox,
	hostRoot, destRoot, wsBase string,
) error {
	uploader := &batchUploader{r: r, sb: sb, destRoot: destRoot, wsBase: wsBase}
	walkErr := filepath.WalkDir(hostRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return uploader.visit(ctx, hostRoot, destRoot, p, d)
	})
	if walkErr != nil {
		uploader.closePending()
		return fmt.Errorf("opensandbox: walk and upload %s: %w", hostRoot, walkErr)
	}
	// Flush any remaining entries after the walk completes.
	return uploader.flush(ctx)
}

// batchUploader accumulates files opened during a WalkDir and uploads
// them in batches of uploadBatchSize. Each flush re-validates target
// parents, removes pre-existing leaf symlinks in a single bash call,
// then closes all file handles in the batch.
type batchUploader struct {
	r         *workspaceRuntime
	sb        *osb.Sandbox
	destRoot  string
	wsBase    string
	entries   []osb.UploadFileEntry
	openFiles []*os.File
}

// visit handles one WalkDir entry: creates directories, skips
// non-regular files, opens regular files, and flushes a batch when it
// reaches uploadBatchSize.
func (u *batchUploader) visit(
	ctx context.Context,
	hostRoot, destRoot, p string,
	d fs.DirEntry,
) error {
	rel, err := filepath.Rel(hostRoot, p)
	if err != nil {
		return err
	}
	if rel == "." {
		return nil
	}
	remotePath := path.Join(destRoot, filepath.ToSlash(rel))

	if d.IsDir() {
		// Resolve intermediate destination components so a pre-existing
		// directory symlink under destRoot cannot redirect CreateDirectory
		// (or later nested uploads) outside the workspace.
		resolved, err := u.r.resolveSandboxAncestor(ctx, remotePath, u.wsBase)
		if err != nil {
			return err
		}
		if err := u.sb.CreateDirectory(ctx, resolved, osb.OctalMode(0o755)); err != nil {
			return fmt.Errorf("create directory %s: %w", resolved, err)
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
	// Resolve the parent of the leaf so intermediate directory symlinks
	// (e.g. dest/hijack -> /tmp/outside with leaf file.txt) are rejected
	// before CreateDirectory/UploadFiles. Mirrors PutFiles.
	resolvedParent, err := u.r.resolveSandboxAncestor(ctx, path.Dir(remotePath), u.wsBase)
	if err != nil {
		return err
	}
	remotePath = path.Join(resolvedParent, path.Base(remotePath))
	if !pathUnder(remotePath, u.wsBase) {
		return fmt.Errorf("opensandbox: path %q escapes workspace", remotePath)
	}
	parent := resolvedParent
	if parent != "." && parent != "/" && parent != destRoot && parent != u.wsBase {
		if err := u.sb.CreateDirectory(ctx, parent, osb.OctalMode(0o755)); err != nil {
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
	u.openFiles = append(u.openFiles, f)
	u.entries = append(u.entries, osb.UploadFileEntry{
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
	if len(u.entries) >= uploadBatchSize {
		return u.flush(ctx)
	}
	return nil
}

// flush uploads the current batch and closes all its file handles.
// Called when the batch reaches uploadBatchSize and once more after
// the walk finishes.
//
// Immediately before upload:
//  1. Re-resolve every target parent (TOCTOU between visit and flush).
//  2. Remove pre-existing leaf symlinks in one bash call. The shell
//     loop must exit 0 when no path is a symlink — the usual upload
//     case — so use `if [ -L ]; then rm; fi` rather than
//     `[ -L ] && rm`, which leaves exit status 1 when the last path is
//     a normal file or missing.
func (u *batchUploader) flush(ctx context.Context) error {
	if len(u.entries) == 0 {
		return nil
	}
	// Re-validate parents right before upload so a symlink planted after
	// visit cannot redirect the write. Update Metadata.Path in place.
	for i := range u.entries {
		p := u.entries[i].Options.Metadata.Path
		resolvedParent, err := u.r.resolveSandboxAncestor(ctx, path.Dir(p), u.wsBase)
		if err != nil {
			u.closePending()
			return err
		}
		finalPath := path.Join(resolvedParent, path.Base(p))
		if !pathUnder(finalPath, u.wsBase) {
			u.closePending()
			return fmt.Errorf("opensandbox: path %q escapes workspace", finalPath)
		}
		u.entries[i].Options.Metadata.Path = finalPath
	}
	if err := u.r.removeSymlinksBatch(ctx, uploadPaths(u.entries), u.wsBase); err != nil {
		u.closePending()
		return err
	}
	err := u.sb.UploadFiles(ctx, u.entries)
	u.closePending()
	return err
}

// closePending closes all open file handles in the current batch and
// resets the batch state. Safe to call multiple times.
func (u *batchUploader) closePending() {
	for _, f := range u.openFiles {
		_ = f.Close()
	}
	u.entries = u.entries[:0]
	u.openFiles = u.openFiles[:0]
}

// uploadPaths returns Metadata.Path for every upload entry.
func uploadPaths(entries []osb.UploadFileEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Options.Metadata.Path
	}
	return out
}

// removeSymlinksBatch removes pre-existing symlinks at the given paths
// in a single bash call. The loop always exits 0 when no path is a
// symlink (if/fi, not `&&`), so normal uploads are not aborted.
// Each path must already be pathUnder(wsBase).
func (r *workspaceRuntime) removeSymlinksBatch(
	ctx context.Context, paths []string, wsBase string,
) error {
	if len(paths) == 0 {
		return nil
	}
	var rm strings.Builder
	rm.WriteString("for p in")
	for _, p := range paths {
		if !pathUnder(p, wsBase) {
			return fmt.Errorf(
				"opensandbox: path %q escapes workspace %q", p, wsBase,
			)
		}
		rm.WriteByte(' ')
		rm.WriteString(shellQuote(p))
	}
	rm.WriteString(`; do if [ -L "$p" ]; then rm -f -- "$p" || exit; fi; done`)
	if _, err := r.runBash(ctx, rm.String(), defaultCreateTimeout); err != nil {
		return fmt.Errorf("opensandbox: batch remove symlinks before upload: %w", err)
	}
	return nil
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
