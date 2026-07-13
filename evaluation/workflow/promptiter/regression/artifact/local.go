//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package artifact writes immutable optimization reports to local storage.
package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// File describes one content-addressed artifact.
type File struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Store writes immutable files beneath one root directory.
type Store struct {
	root        string
	bundleOps   bundleOperations
	bundleLocks map[string]*bundleLock
	bundleMu    sync.Mutex
}

type bundleLock struct {
	mu    sync.Mutex
	users int
}

type bundleOperations struct {
	mkdirTemp       func(string, string) (string, error)
	removeAll       func(string) error
	rename          func(string, string) error
	writeSyncedFile func(string, []byte) error
	syncDirectory   func(string) error
}

func defaultBundleOperations() bundleOperations {
	return bundleOperations{
		mkdirTemp:       os.MkdirTemp,
		removeAll:       os.RemoveAll,
		rename:          os.Rename,
		writeSyncedFile: writeSyncedFile,
		syncDirectory:   syncDirectory,
	}
}

func (s *Store) effectiveBundleOperations() bundleOperations {
	if s == nil || s.bundleOps.mkdirTemp == nil {
		return defaultBundleOperations()
	}
	return s.bundleOps
}

// NewStore creates a local immutable artifact store.
func NewStore(root string) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("artifact root is empty")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve artifact root: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o750); err != nil {
		return nil, fmt.Errorf("create artifact root: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fmt.Errorf("resolve artifact root symlinks: %w", err)
	}
	return &Store{
		root:        resolved,
		bundleOps:   defaultBundleOperations(),
		bundleLocks: make(map[string]*bundleLock),
	}, nil
}

func (s *Store) lockBundle(directory string) func() {
	s.bundleMu.Lock()
	lock := s.bundleLocks[directory]
	if lock == nil {
		lock = &bundleLock{}
		s.bundleLocks[directory] = lock
	}
	lock.users++
	s.bundleMu.Unlock()

	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		s.bundleMu.Lock()
		lock.users--
		if lock.users == 0 {
			delete(s.bundleLocks, directory)
		}
		s.bundleMu.Unlock()
	}
}

// Write atomically creates a file and refuses different content at the same name.
func (s *Store) Write(ctx context.Context, name string, content []byte) (*File, error) {
	path, err := s.path(name)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create artifact directory: %w", err)
	}
	if err := ensureNoSymlinkPath(s.root, filepath.Dir(path)); err != nil {
		return nil, err
	}
	digestBytes := sha256.Sum256(content)
	digest := hex.EncodeToString(digestBytes[:])
	if existing, statErr := digestFile(path); statErr == nil {
		if existing != digest {
			return nil, fmt.Errorf("artifact %q already exists with different content", name)
		}
		return metadata(name, path, digest), nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect existing artifact: %w", statErr)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".artifact-*")
	if err != nil {
		return nil, fmt.Errorf("create temporary artifact: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return nil, fmt.Errorf("write temporary artifact: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return nil, fmt.Errorf("sync temporary artifact: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return nil, fmt.Errorf("close temporary artifact: %w", err)
	}
	if err := os.Link(temporaryName, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			existing, digestErr := digestFile(path)
			if digestErr == nil && existing == digest {
				return metadata(name, path, digest), nil
			}
		}
		return nil, fmt.Errorf("commit artifact: %w", err)
	}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return nil, fmt.Errorf("sync artifact directory: %w", err)
	}
	return metadata(name, path, digest), nil
}

func (s *Store) path(name string) (string, error) {
	portable := strings.ReplaceAll(name, "\\", "/")
	clean := filepath.Clean(filepath.FromSlash(portable))
	if name == "" || strings.HasPrefix(portable, "/") || filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid artifact name %q", name)
	}
	path := filepath.Join(s.root, clean)
	relative, err := filepath.Rel(s.root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("artifact name %q escapes the store root", name)
	}
	if err := ensureNoSymlinkPath(s.root, filepath.Dir(path)); err != nil {
		return "", err
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("artifact path %q is a symbolic link", name)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect artifact path %q: %w", name, err)
	}
	return path, nil
}

func metadata(name, path, digest string) *File {
	file := &File{Name: name, Path: filepath.ToSlash(path), SHA256: digest}
	if info, err := os.Stat(path); err == nil {
		file.Size = info.Size()
	}
	return file
}

func digestFile(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("artifact path %q is a symbolic link", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:]), nil
}

func ensureNoSymlinkPath(root, target string) error {
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("artifact path %q escapes root %q", target, root)
	}
	current := root
	if relative == "." {
		return nil
	}
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if errors.Is(statErr, os.ErrNotExist) {
			return nil
		}
		if statErr != nil {
			return fmt.Errorf("inspect artifact directory %q: %w", current, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("artifact directory %q is a symbolic link", current)
		}
		if !info.IsDir() {
			return fmt.Errorf("artifact parent %q is not a directory", current)
		}
	}
	return nil
}

func syncDirectory(path string) error {
	// Windows does not support syncing directory handles. File contents are
	// synced before publication, while os.Rename provides the atomic publish
	// step on this platform.
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
