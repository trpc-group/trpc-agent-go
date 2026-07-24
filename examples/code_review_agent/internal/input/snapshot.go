//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package input

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	maxSnapshotBytes = 512 << 20
	maxSnapshotFiles = 100_000
)

// snapshotRepository copies a repository into a private, read-only tree and
// returns a digest over relative paths and contents.
func snapshotRepository(source string, limits Limits) (string, string, func() error, error) {
	source, err := filepath.EvalSymlinks(source)
	if err != nil {
		return "", "", nil, err
	}
	info, err := os.Stat(source)
	if err != nil || !info.IsDir() {
		return "", "", nil, errors.New("snapshot source is not a directory")
	}
	root, err := os.MkdirTemp("", "code-review-snapshot-*")
	if err != nil {
		return "", "", nil, err
	}
	cleanup := func() error { return removeSnapshot(root) }
	hasher := sha256.New()
	var files int
	var total int64
	err = filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(source, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		if entry.IsDir() && excludedSnapshotDirectory(entry.Name()) {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		if files >= maxSnapshotFiles {
			return fmt.Errorf("snapshot exceeds %d files", maxSnapshotFiles)
		}
		copyPath, fileInfo, statErr := snapshotFileInfo(source, path, entry.Type())
		if statErr != nil {
			return statErr
		}
		if fileInfo.Size() < 0 || fileInfo.Size() > limits.MaxFileBytes {
			return fmt.Errorf("snapshot file exceeds limit: %s", rel)
		}
		rel = filepath.ToSlash(rel)
		target := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		in, openErr := os.Open(copyPath)
		if openErr != nil {
			return openErr
		}
		openedInfo, openErr := in.Stat()
		if openErr != nil || !openedInfo.Mode().IsRegular() {
			_ = in.Close()
			return errors.Join(openErr, fmt.Errorf("snapshot source is not a regular file: %s", rel))
		}
		out, createErr := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if createErr == nil {
			remaining := min64(limits.MaxFileBytes, maxSnapshotBytes-total)
			var data []byte
			data, createErr = io.ReadAll(io.LimitReader(in, remaining+1))
			if createErr == nil && int64(len(data)) > remaining {
				createErr = fmt.Errorf("snapshot content exceeds limit: %s", rel)
			}
			if createErr == nil {
				_, createErr = out.Write(data)
			}
			if createErr == nil {
				_, _ = fmt.Fprintf(hasher, "%s\x00%d\x00", rel, len(data))
				_, _ = hasher.Write(data)
				total += int64(len(data))
				files++
			}
		}
		_ = in.Close()
		if out != nil {
			_ = out.Close()
		}
		return createErr
	})
	if err != nil {
		return "", "", nil, errors.Join(err, cleanup())
	}
	if err := setSnapshotReadOnly(root); err != nil {
		return "", "", nil, errors.Join(err, cleanup())
	}
	return root, hex.EncodeToString(hasher.Sum(nil)), cleanup, nil
}

func digestTree(root string, maxFileBytes int64) (string, error) {
	var paths []string
	var total int64
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if excludedSnapshotDirectory(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			var resolveErr error
			_, info, resolveErr = snapshotFileInfo(root, path, info.Mode())
			if resolveErr != nil {
				return resolveErr
			}
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported repository file: %s", path)
		}
		if len(paths) >= maxSnapshotFiles {
			return fmt.Errorf("repository exceeds %d files", maxSnapshotFiles)
		}
		if info.Size() < 0 || info.Size() > maxFileBytes {
			return fmt.Errorf("repository file exceeds limit: %s", path)
		}
		total += info.Size()
		if total > maxSnapshotBytes {
			return fmt.Errorf("repository exceeds %d bytes", maxSnapshotBytes)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	}); err != nil {
		return "", err
	}
	slices.Sort(paths)
	hasher := sha256.New()
	for _, rel := range paths {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		_, _ = fmt.Fprintf(hasher, "%s\x00%d\x00", rel, len(data))
		_, _ = hasher.Write(data)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func excludedSnapshotDirectory(name string) bool {
	return name == ".git"
}

func snapshotFileInfo(root, path string, mode os.FileMode) (string, os.FileInfo, error) {
	if mode&os.ModeSymlink == 0 {
		info, err := os.Stat(path)
		return path, info, err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", nil, err
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", nil, fmt.Errorf("symlink escapes repository: %s", path)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", nil, err
	}
	if !info.Mode().IsRegular() {
		return "", nil, fmt.Errorf("symlink target is not a regular file: %s", path)
	}
	return resolved, info, nil
}

func min64(left, right int64) int64 {
	if left < right {
		return left
	}
	return right
}

// DigestRepository returns the canonical digest used to bind a repository to
// governance evidence.
func DigestRepository(root string) (string, error) {
	return digestTree(root, defaultMaxFileBytes)
}

func setSnapshotReadOnly(root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		mode := os.FileMode(0o500)
		if !info.IsDir() {
			mode = 0o400
		}
		return os.Chmod(path, mode)
	})
}

func removeSnapshot(root string) error {
	restoreErr := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		mode := os.FileMode(0o700)
		if !info.IsDir() {
			mode = 0o600
		}
		return os.Chmod(path, mode)
	})
	return errors.Join(restoreErr, os.RemoveAll(root))
}
