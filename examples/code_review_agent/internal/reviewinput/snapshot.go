//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package reviewinput

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func createGitSnapshot(
	ctx context.Context,
	client gitClient,
	root string,
	tempRoot string,
	limits Limits,
) (snapshot string, cleanup func() error, err error) {
	// listSnapshotFiles reads index modes and omits 160000 gitlinks. A checked
	// out submodule is a directory owned by another repository; recursively
	// copying it would both cross the review-root boundary and make ordinary
	// superproject reviews fail the regular-file check.
	files, err := client.listSnapshotFiles(ctx, root)
	if err != nil {
		return "", nil, err
	}
	return createSnapshot(root, files, tempRoot, limits)
}

// createDirectorySnapshot supports simple, non-Git public fixtures while
// excluding any accidental .git metadata from the staged review repository.
func createDirectorySnapshot(
	root string,
	tempRoot string,
	limits Limits,
) (snapshot string, cleanup func() error, err error) {
	var files []string
	err = filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		if rel == ".git" || strings.HasPrefix(filepath.ToSlash(rel), ".git/") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if rel != "." && !entry.IsDir() {
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return "", nil, fmt.Errorf("enumerate fixture repo: %w", err)
	}
	return createSnapshot(root, files, tempRoot, limits)
}

// createSnapshot owns the temporary directory and copies only the enumerated
// repository entries. Returning cleanup with the path makes lifetime ownership
// explicit to PreparedInput and prevents touching the user's worktree.
func createSnapshot(
	root string,
	files []string,
	tempRoot string,
	limits Limits,
) (snapshot string, cleanup func() error, err error) {
	limits = limits.withDefaults()
	taskRoot, err := os.MkdirTemp(tempRoot, "code-review-input-*")
	if err != nil {
		return "", nil, fmt.Errorf("create task input directory: %w", err)
	}
	cleanup = func() error { return os.RemoveAll(taskRoot) }
	snapshot = filepath.Join(taskRoot, "repo")
	if err := os.MkdirAll(snapshot, 0o755); err != nil {
		_ = cleanup()
		return "", nil, fmt.Errorf("create repo snapshot: %w", err)
	}
	var copiedBytes int64
	for _, rel := range files {
		if err := copySnapshotEntry(
			root,
			snapshot,
			rel,
			limits,
			&copiedBytes,
		); err != nil {
			_ = cleanup()
			return "", nil, err
		}
	}
	return snapshot, cleanup, nil
}

// copySnapshotEntry preserves regular-file executability and safe relative
// symlinks, but rejects special files and links that can resolve outside the
// copied repository. This is the host-to-workspace trust boundary.
func copySnapshotEntry(
	root string,
	snapshot string,
	rel string,
	limits Limits,
	copiedBytes *int64,
) error {
	normalized, err := normalizeRequestedPath(rel)
	if err != nil {
		return err
	}
	src := filepath.Join(root, filepath.FromSlash(normalized))
	dst := filepath.Join(snapshot, filepath.FromSlash(normalized))
	info, err := os.Lstat(src)
	if os.IsNotExist(err) {
		// A tracked deletion is part of the diff but has no file to copy.
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect snapshot input %s: %w", normalized, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create snapshot directory for %s: %w", normalized, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return fmt.Errorf("read symlink %s: %w", normalized, err)
		}
		if filepath.IsAbs(target) {
			return fmt.Errorf("symlink %s points outside the repository", normalized)
		}
		resolved := filepath.Clean(filepath.Join(filepath.Dir(normalized), target))
		if !filepath.IsLocal(resolved) {
			return fmt.Errorf("symlink %s escapes the repository", normalized)
		}
		if err := os.Symlink(target, dst); err != nil {
			return fmt.Errorf("copy symlink %s: %w", normalized, err)
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("snapshot input %s is not a regular file", normalized)
	}
	if info.Size() > limits.MaxSnapshotFileBytes {
		return fmt.Errorf(
			"%s exceeds the %d-byte snapshot file limit",
			normalized,
			limits.MaxSnapshotFileBytes,
		)
	}
	if info.Size() > limits.MaxSnapshotBytes-*copiedBytes {
		return fmt.Errorf(
			"snapshot exceeds the %d-byte snapshot limit while copying %s",
			limits.MaxSnapshotBytes,
			normalized,
		)
	}
	source, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open snapshot input %s: %w", normalized, err)
	}
	defer source.Close()
	mode := fs.FileMode(0o644)
	if info.Mode()&0o111 != 0 {
		mode = 0o755
	}
	destination, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("create snapshot input %s: %w", normalized, err)
	}
	remainingTotal := limits.MaxSnapshotBytes - *copiedBytes
	copyLimit := min(limits.MaxSnapshotFileBytes, remainingTotal)
	written, copyErr := io.Copy(destination, io.LimitReader(source, copyLimit))
	closeErr := destination.Close()
	if copyErr != nil {
		return fmt.Errorf("copy snapshot input %s: %w", normalized, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close snapshot input %s: %w", normalized, closeErr)
	}
	var extra [1]byte
	extraBytes, readErr := source.Read(extra[:])
	if readErr != nil && readErr != io.EOF {
		return fmt.Errorf("check snapshot input size %s: %w", normalized, readErr)
	}
	if extraBytes > 0 {
		if copyLimit == limits.MaxSnapshotFileBytes {
			return fmt.Errorf(
				"%s exceeds the %d-byte snapshot file limit",
				normalized,
				limits.MaxSnapshotFileBytes,
			)
		}
		return fmt.Errorf(
			"snapshot exceeds the %d-byte snapshot limit while copying %s",
			limits.MaxSnapshotBytes,
			normalized,
		)
	}
	*copiedBytes += written
	return nil
}
