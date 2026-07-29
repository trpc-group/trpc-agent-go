//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

const maxSandboxSnapshotBytes = int64(128 * 1024 * 1024)

type repoSnapshot struct {
	Root  string
	Files int
	Bytes int64
}

func prepareSandboxRepoSnapshot(
	ctx context.Context,
	repoRoot string,
	filesScope []string,
	maxBytes int64,
) (repoSnapshot, error) {
	root := strings.TrimSpace(repoRoot)
	if root == "" {
		return repoSnapshot{}, fmt.Errorf("repository path is empty")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return repoSnapshot{}, fmt.Errorf("resolve repository path: %w", err)
	}
	resolvedRoot, err := resolveExistingPath(absRoot)
	if err != nil {
		return repoSnapshot{}, fmt.Errorf("resolve repository symlinks: %w", err)
	}
	scope, err := cleanSnapshotScope(filesScope)
	if err != nil {
		return repoSnapshot{}, err
	}
	tracked, err := gitTrackedFiles(ctx, resolvedRoot, scope)
	if err != nil {
		return repoSnapshot{}, err
	}
	snapshotRoot, err := os.MkdirTemp("", "code-review-repo-snapshot-*")
	if err != nil {
		return repoSnapshot{}, fmt.Errorf("create repository snapshot: %w", err)
	}
	cleanupOnError := true
	defer func() {
		if cleanupOnError {
			_ = os.RemoveAll(snapshotRoot)
		}
	}()

	var total int64
	var fileCount int
	for _, raw := range tracked {
		rel, err := cleanTrackedPath(raw)
		if err != nil {
			return repoSnapshot{}, err
		}
		src := filepath.Join(resolvedRoot, filepath.FromSlash(rel))
		if !pathStaysWithin(resolvedRoot, src) {
			return repoSnapshot{}, fmt.Errorf("tracked path %q escapes the repository", raw)
		}
		info, err := os.Lstat(src)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return repoSnapshot{}, fmt.Errorf("stat tracked file %q: %w", rel, err)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		resolvedSrc, err := resolveExistingPath(src)
		if err != nil {
			return repoSnapshot{}, fmt.Errorf("resolve tracked file %q: %w", rel, err)
		}
		if !pathStaysWithin(resolvedRoot, resolvedSrc) {
			return repoSnapshot{}, fmt.Errorf("tracked path %q resolves outside the repository", raw)
		}
		resolvedInfo, err := os.Stat(resolvedSrc)
		if err != nil {
			return repoSnapshot{}, fmt.Errorf("stat resolved tracked file %q: %w", rel, err)
		}
		if !resolvedInfo.Mode().IsRegular() {
			continue
		}
		if total+resolvedInfo.Size() > maxBytes {
			return repoSnapshot{}, fmt.Errorf(
				"repository snapshot exceeds %d bytes before %q",
				maxBytes,
				rel,
			)
		}
		dst := filepath.Join(snapshotRoot, filepath.FromSlash(rel))
		if err := copyRegularFile(resolvedSrc, dst, resolvedInfo.Mode().Perm()); err != nil {
			return repoSnapshot{}, err
		}
		total += resolvedInfo.Size()
		fileCount++
	}
	cleanupOnError = false
	return repoSnapshot{Root: snapshotRoot, Files: fileCount, Bytes: total}, nil
}

func cleanSnapshotScope(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	cleaned := make([]string, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for _, item := range raw {
		rel, err := cleanTrackedPath(item)
		if err != nil {
			return nil, fmt.Errorf("invalid snapshot scope: %w", err)
		}
		if seen[rel] {
			continue
		}
		seen[rel] = true
		cleaned = append(cleaned, rel)
	}
	return cleaned, nil
}

func gitTrackedFiles(ctx context.Context, repoRoot string, filesScope []string) ([]string, error) {
	args := []string{"--literal-pathspecs", "-C", repoRoot, "ls-files", "-z", "--"}
	args = append(args, filesScope...)
	cmd := exec.CommandContext(ctx, "git", args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		reason := strings.TrimSpace(stderr.String())
		if reason == "" {
			reason = err.Error()
		}
		return nil, fmt.Errorf("git ls-files failed: %s", reason)
	}
	raw := stdout.Bytes()
	if len(raw) == 0 {
		return nil, nil
	}
	parts := bytes.Split(raw, []byte{0})
	tracked := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		tracked = append(tracked, string(part))
	}
	return tracked, nil
}

func cleanTrackedPath(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("tracked path is empty")
	}
	if strings.ContainsRune(raw, '\x00') {
		return "", fmt.Errorf("tracked path contains a NUL byte")
	}
	normalized := strings.ReplaceAll(raw, "\\", "/")
	if path.IsAbs(normalized) || hasWindowsDrive(normalized) {
		return "", fmt.Errorf("tracked path %q must be relative", raw)
	}
	clean := path.Clean(normalized)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("tracked path %q escapes the repository", raw)
	}
	if clean == ".git" || strings.HasPrefix(clean, ".git/") {
		return "", fmt.Errorf("tracked path %q targets Git metadata", raw)
	}
	return clean, nil
}

func pathStaysWithin(root string, candidate string) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absCandidate, err := filepath.Abs(candidate)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absCandidate)
	if err != nil {
		return false
	}
	return rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func resolveExistingPath(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absPath)
	if err == nil {
		return filepath.Abs(resolved)
	}
	hasSymlink, inspectErr := pathHasSymlinkComponent(absPath)
	if inspectErr != nil || hasSymlink {
		return "", err
	}
	// A component-by-component check can still prove that an existing path has
	// no symlinks after EvalSymlinks fails. Only in that case is absPath used for
	// containment checks; symlinks and inspection failures fail closed above.
	return absPath, nil
}

func pathHasSymlinkComponent(path string) (bool, error) {
	current := filepath.Clean(path)
	for {
		info, err := os.Lstat(current)
		if err != nil {
			return false, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return true, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return false, nil
		}
		current = parent
	}
}

func copyRegularFile(src string, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create snapshot directory: %w", err)
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open tracked file %q: %w", src, err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("create snapshot file %q: %w", dst, err)
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return fmt.Errorf("copy tracked file %q: %w", src, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close snapshot file %q: %w", dst, closeErr)
	}
	return nil
}
