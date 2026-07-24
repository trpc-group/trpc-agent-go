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
	tracked, err := gitTrackedFiles(ctx, absRoot)
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
	var files int
	for _, raw := range tracked {
		rel, err := cleanTrackedPath(raw)
		if err != nil {
			return repoSnapshot{}, err
		}
		src := filepath.Join(absRoot, filepath.FromSlash(rel))
		if !pathStaysWithin(absRoot, src) {
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
		if total+info.Size() > maxBytes {
			return repoSnapshot{}, fmt.Errorf(
				"repository snapshot exceeds %d bytes before %q",
				maxBytes,
				rel,
			)
		}
		dst := filepath.Join(snapshotRoot, filepath.FromSlash(rel))
		if err := copyRegularFile(src, dst, info.Mode().Perm()); err != nil {
			return repoSnapshot{}, err
		}
		total += info.Size()
		files++
	}
	cleanupOnError = false
	return repoSnapshot{Root: snapshotRoot, Files: files, Bytes: total}, nil
}

func gitTrackedFiles(ctx context.Context, repoRoot string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "ls-files", "-z", "--")
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
	files := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		files = append(files, string(part))
	}
	return files, nil
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
