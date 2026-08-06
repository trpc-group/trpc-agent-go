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

type snapshotEntry struct {
	Path       string
	Executable bool
}

// snapshotRepository copies a repository into a private, read-only tree and
// returns a digest over relative paths and contents.
func snapshotRepository(source string, limits Limits) (string, string, func() error, error) {
	paths, err := snapshotTreePaths(source)
	if err != nil {
		return "", "", nil, err
	}
	return snapshotRepositoryPaths(source, paths, limits)
}

func snapshotRepositoryPaths(source string, paths []snapshotEntry, limits Limits) (string, string, func() error, error) {
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
	authorized := authorizedSnapshotPaths(paths)
	var files int
	var total int64
	for _, entry := range paths {
		if files >= maxSnapshotFiles {
			err = fmt.Errorf("snapshot exceeds %d files", maxSnapshotFiles)
			break
		}
		clean, cleanErr := cleanSnapshotPath(entry.Path)
		if cleanErr != nil {
			err = cleanErr
			break
		}
		path := filepath.Join(source, filepath.FromSlash(clean))
		pathInfo, statErr := os.Lstat(path)
		if statErr != nil {
			err = statErr
			break
		}
		copyPath, fileInfo, statErr := snapshotFileInfo(source, path, pathInfo.Mode(), authorized)
		if statErr != nil {
			err = statErr
			break
		}
		target := filepath.Join(root, filepath.FromSlash(clean))
		if mkdirErr := os.MkdirAll(filepath.Dir(target), 0o755); mkdirErr != nil {
			err = mkdirErr
			break
		}
		remaining := min64(limits.MaxFileBytes, maxSnapshotBytes-total)
		data, readErr := readSnapshotFile(copyPath, fileInfo, remaining, clean)
		if readErr != nil {
			err = readErr
			break
		}
		createMode := os.FileMode(0o600)
		if entry.Executable {
			createMode = 0o700
		}
		out, createErr := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, createMode)
		if createErr == nil {
			_, createErr = out.Write(data)
			if createErr == nil {
				total += int64(len(data))
				files++
			}
		}
		if out != nil {
			_ = out.Close()
		}
		if createErr != nil {
			err = createErr
			break
		}
	}
	if err != nil {
		return "", "", nil, errors.Join(err, cleanup())
	}
	if err := setSnapshotReadOnly(root); err != nil {
		return "", "", nil, errors.Join(err, cleanup())
	}
	digest, err := digestPaths(root, paths, limits.MaxFileBytes)
	if err != nil {
		return "", "", nil, errors.Join(err, cleanup())
	}
	return root, digest, cleanup, nil
}

func digestTree(root string, maxFileBytes int64) (string, error) {
	paths, err := snapshotTreePaths(root)
	if err != nil {
		return "", err
	}
	return digestPaths(root, paths, maxFileBytes)
}

func snapshotTreePaths(root string) ([]snapshotEntry, error) {
	var paths []snapshotEntry
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if excludedSnapshotPath(rel) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if len(paths) >= maxSnapshotFiles {
			return fmt.Errorf("repository exceeds %d files", maxSnapshotFiles)
		}
		resolved, statErr := os.Stat(path)
		if statErr != nil {
			return statErr
		}
		if !resolved.Mode().IsRegular() {
			return fmt.Errorf("unsupported repository file: %s", path)
		}
		paths = append(paths, snapshotEntry{Path: rel, Executable: resolved.Mode().Perm()&0o111 != 0})
		return nil
	}); err != nil {
		return nil, err
	}
	slices.SortFunc(paths, func(left, right snapshotEntry) int {
		return strings.Compare(left.Path, right.Path)
	})
	return paths, nil
}

func digestPaths(root string, paths []snapshotEntry, maxFileBytes int64) (string, error) {
	var total int64
	hasher := sha256.New()
	authorized := authorizedSnapshotPaths(paths)
	for _, entry := range paths {
		clean, err := cleanSnapshotPath(entry.Path)
		if err != nil {
			return "", err
		}
		path := filepath.Join(root, filepath.FromSlash(clean))
		info, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		copyPath, resolvedInfo, err := snapshotFileInfo(root, path, info.Mode(), authorized)
		if err != nil {
			return "", err
		}
		remaining := min64(maxFileBytes, maxSnapshotBytes-total)
		data, err := readSnapshotFile(copyPath, resolvedInfo, remaining, clean)
		if err != nil {
			return "", err
		}
		total += int64(len(data))
		executable := 0
		if entry.Executable {
			executable = 1
		}
		_, _ = fmt.Fprintf(hasher, "%s\x00%d\x00%d\x00", clean, executable, len(data))
		_, _ = hasher.Write(data)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func authorizedSnapshotPaths(entries []snapshotEntry) map[string]bool {
	result := make(map[string]bool, len(entries))
	for _, entry := range entries {
		result[filepath.ToSlash(entry.Path)] = true
	}
	return result
}

func cleanSnapshotPath(value string) (string, error) {
	if value == "" || filepath.IsAbs(value) || strings.ContainsRune(value, 0) {
		return "", fmt.Errorf("invalid snapshot path %q", value)
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || excludedSnapshotPath(clean) {
		return "", fmt.Errorf("invalid snapshot path %q", value)
	}
	return clean, nil
}

func excludedSnapshotPath(value string) bool {
	value = filepath.ToSlash(value)
	return value == ".git" || strings.HasPrefix(value, ".git/")
}

func snapshotFileInfo(root, path string, mode os.FileMode, authorized map[string]bool) (string, os.FileInfo, error) {
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
	rel, err = cleanSnapshotPath(filepath.ToSlash(rel))
	if err != nil || !authorized[rel] {
		return "", nil, errors.Join(err, fmt.Errorf("symlink target is outside snapshot inventory: %s", path))
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

func readSnapshotFile(path string, expected os.FileInfo, limit int64, name string) (data []byte, resultErr error) {
	if expected == nil || !expected.Mode().IsRegular() || expected.Size() < 0 || expected.Size() > limit {
		return nil, fmt.Errorf("snapshot file exceeds limit or is not regular: %s", name)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		resultErr = errors.Join(resultErr, file.Close())
	}()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(expected, opened) || opened.Size() != expected.Size() {
		return nil, errors.Join(err, fmt.Errorf("snapshot source changed before read: %s", name))
	}
	data, err = io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("snapshot content exceeds limit: %s", name)
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(opened, after) || after.Size() != opened.Size() || int64(len(data)) != after.Size() {
		return nil, errors.Join(err, fmt.Errorf("snapshot source changed while reading: %s", name))
	}
	return data, nil
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
		mode := os.FileMode(0o555)
		if path == root {
			mode = 0o500
		} else if !info.IsDir() {
			mode = 0o444
			if info.Mode().Perm()&0o111 != 0 {
				mode = 0o555
			}
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
