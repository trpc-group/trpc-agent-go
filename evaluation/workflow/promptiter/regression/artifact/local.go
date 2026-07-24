//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package artifact writes optimization reports to local storage.
package artifact

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// File describes one generated report file.
type File struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Store owns the resolved directory beneath which report bundles are written.
type Store struct {
	root string
}

// NewStore creates a local report store.
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
	return &Store{root: resolved}, nil
}

func (s *Store) runPath(runID string) (string, error) {
	if s == nil || s.root == "" {
		return "", errors.New("artifact store is not initialized")
	}
	if err := validateRunDirectoryName(runID); err != nil {
		return "", err
	}
	path := filepath.Join(s.root, runID)
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("report bundle %q is a symbolic link", runID)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect report bundle %q: %w", runID, err)
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

func syncDirectory(path string) error {
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
