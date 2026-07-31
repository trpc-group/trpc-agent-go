//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package input

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Snapshot is an immutable copy of reviewable regular files.
type Snapshot struct {
	Path   string
	Digest string
}

// BuildSnapshot copies regular repository files into a temporary snapshot.
func BuildSnapshot(repoPath string, maxBytes int64) (Snapshot, func() error, error) {
	return BuildGitSnapshot(repoPath, maxBytes)
}

// BuildGitSnapshot copies Git-tracked regular files into an immutable snapshot.
func BuildGitSnapshot(repoPath string, maxBytes int64) (Snapshot, func() error, error) {
	if maxBytes <= 0 {
		maxBytes = 128 << 20
	}
	absRepo, err := filepath.Abs(repoPath)
	if err != nil {
		return Snapshot{}, nil, err
	}
	files, err := gitTrackedFiles(absRepo)
	if err != nil {
		return Snapshot{}, nil, err
	}
	tmp, err := os.MkdirTemp("", "code-review-snapshot-*")
	if err != nil {
		return Snapshot{}, nil, err
	}
	cleanup := func() error { return os.RemoveAll(tmp) }
	var total int64
	var copied []string
	for _, rel := range files {
		if err := validateSnapshotRel(rel); err != nil {
			_ = cleanup()
			return Snapshot{}, nil, err
		}
		src := filepath.Join(absRepo, filepath.FromSlash(rel))
		info, err := os.Lstat(src)
		if err != nil {
			_ = cleanup()
			return Snapshot{}, nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		total += info.Size()
		if total > maxBytes {
			_ = cleanup()
			return Snapshot{}, nil, fmt.Errorf("snapshot exceeds size limit")
		}
		dst := filepath.Join(tmp, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			_ = cleanup()
			return Snapshot{}, nil, err
		}
		if err := copyFile(src, dst, normalizedSnapshotMode(info.Mode().Perm())); err != nil {
			_ = cleanup()
			return Snapshot{}, nil, err
		}
		copied = append(copied, rel)
	}
	sort.Strings(copied)
	h := sha256.New()
	for _, rel := range copied {
		_, _ = h.Write([]byte(rel))
		data, err := os.ReadFile(filepath.Join(tmp, filepath.FromSlash(rel)))
		if err != nil {
			_ = cleanup()
			return Snapshot{}, nil, err
		}
		_, _ = h.Write(data)
	}
	return Snapshot{Path: tmp, Digest: hex.EncodeToString(h.Sum(nil))}, cleanup, nil
}

func gitTrackedFiles(repoPath string) ([]string, error) {
	cmd := exec.Command("git", "-C", repoPath, "ls-files", "-z", "--cached")
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_EXTERNAL_DIFF=",
		"GIT_PAGER=cat",
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	parts := strings.Split(string(out), "\x00")
	files := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		files = append(files, filepath.ToSlash(p))
	}
	sort.Strings(files)
	return files, nil
}

func validateSnapshotRel(rel string) error {
	if rel == "" || rel == "." || filepath.IsAbs(rel) || strings.ContainsRune(rel, 0) {
		return fmt.Errorf("unsafe path %q", rel)
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel)))
	if clean != rel || strings.HasPrefix(clean, "../") || clean == ".." ||
		strings.HasPrefix(clean, ".git/") || clean == ".git" ||
		strings.HasPrefix(clean, ".cache/") || clean == ".cache" {
		return fmt.Errorf("unsafe path %q", rel)
	}
	return nil
}

func normalizedSnapshotMode(mode os.FileMode) os.FileMode {
	if mode&0o111 != 0 {
		return 0o755
	}
	return 0o644
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
