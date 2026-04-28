//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package memoryfile

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	rootDirName    = "memory"
	memoryFileName = "MEMORY.md"

	dirPerm           = 0o700
	filePerm          = 0o600
	tempPatternSuffix = ".tmp-*"
)

type Store struct {
	root string

	mu sync.Mutex
}

func DefaultRoot(stateDir string) (string, error) {
	stateDir = strings.TrimSpace(stateDir)
	if stateDir == "" {
		return "", errors.New("memoryfile: empty state dir")
	}
	return filepath.Join(stateDir, rootDirName), nil
}

func NewStore(root string) (*Store, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("memoryfile: empty root")
	}
	return &Store{root: filepath.Clean(root)}, nil
}

func (s *Store) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

func (s *Store) MemoryDir(appName string, userID string) (string, error) {
	if s == nil {
		return "", errors.New("memoryfile: nil store")
	}
	app := sanitizePathPart(appName)
	key := sanitizePathPart(userID)
	if app == "" || key == "" {
		return "", errors.New("memoryfile: empty app/user scope")
	}
	return filepath.Join(s.root, app, key), nil
}

func (s *Store) MemoryPath(appName string, userID string) (string, error) {
	dir, err := s.MemoryDir(appName, userID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, memoryFileName), nil
}

func (s *Store) EnsureMemory(
	ctx context.Context,
	appName string,
	userID string,
) (string, error) {
	if err := contextErr(ctx); err != nil {
		return "", err
	}
	path, err := s.MemoryPath(appName, userID)
	if err != nil {
		return "", err
	}
	if fileExists(path) {
		return path, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if fileExists(path) {
		return path, nil
	}
	if err := contextErr(ctx); err != nil {
		return "", err
	}
	if err := writeFileAtomic(path, []byte(DefaultTemplate())); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Store) UpdateMemory(
	ctx context.Context,
	appName string,
	userID string,
	update func(current string) (string, error),
) (string, error) {
	if s == nil {
		return "", errors.New("memoryfile: nil store")
	}
	if update == nil {
		return "", errors.New("memoryfile: nil update func")
	}
	if err := contextErr(ctx); err != nil {
		return "", err
	}
	path, err := s.MemoryPath(appName, userID)
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := contextErr(ctx); err != nil {
		return "", err
	}
	if !fileExists(path) {
		if err := writeFileAtomic(path, []byte(DefaultTemplate())); err != nil {
			return "", err
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	next, err := update(string(raw))
	if err != nil {
		return "", err
	}
	if err := contextErr(ctx); err != nil {
		return "", err
	}
	if err := writeFileAtomic(path, []byte(next)); err != nil {
		return "", err
	}
	return path, nil
}

// SaveResolvedMemoryFile writes a validated file path while holding the
// same store lock used by logical memory updates.
func (s *Store) SaveResolvedMemoryFile(
	ctx context.Context,
	path string,
	content string,
) error {
	if s == nil {
		return errors.New("memoryfile: nil store")
	}
	if err := contextErr(ctx); err != nil {
		return err
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("memoryfile: empty file path")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := contextErr(ctx); err != nil {
		return err
	}
	if !fileExists(path) {
		return os.ErrNotExist
	}
	return writeFileAtomic(path, []byte(content))
}

func (s *Store) ReadFile(path string, maxBytes int) (string, error) {
	if s == nil {
		return "", errors.New("memoryfile: nil store")
	}
	path, err := s.resolveFilePath(path)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if maxBytes > 0 && len(raw) > maxBytes {
		raw = raw[:maxBytes]
	}
	return strings.TrimSpace(string(raw)), nil
}

func (s *Store) resolveFilePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("memoryfile: empty file path")
	}
	rootAbs, err := filepath.Abs(s.root)
	if err != nil {
		return "", fmt.Errorf("memoryfile: resolve root: %w", err)
	}
	pathAbs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("memoryfile: resolve file path: %w", err)
	}
	rel, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil {
		return "", fmt.Errorf("memoryfile: relativize file path: %w", err)
	}
	parentPrefix := ".." + string(filepath.Separator)
	if rel == ".." || strings.HasPrefix(rel, parentPrefix) {
		return "", errors.New("memoryfile: path outside store root")
	}
	return pathAbs, nil
}

func (s *Store) DeleteUser(
	ctx context.Context,
	appName string,
	userID string,
) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if s == nil {
		return nil
	}
	dir, err := s.MemoryDir(appName, userID)
	if err != nil {
		return err
	}
	return s.removeScopedDir(ctx, dir)
}

func (s *Store) removeScopedDir(ctx context.Context, dir string) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := contextErr(ctx); err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("memoryfile: remove dir: %w", err)
	}
	return nil
}

func sanitizePathPart(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func writeFileAtomic(path string, data []byte) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("memoryfile: empty file path")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("memoryfile: create dir: %w", err)
	}

	file, err := os.CreateTemp(
		dir,
		filepath.Base(path)+tempPatternSuffix,
	)
	if err != nil {
		return fmt.Errorf("memoryfile: create temp file: %w", err)
	}
	tmpPath := file.Name()
	removeTemp := true
	defer func() {
		_ = file.Close()
		if removeTemp {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("memoryfile: write temp file: %w", err)
	}
	if err := file.Chmod(filePerm); err != nil {
		return fmt.Errorf("memoryfile: chmod temp file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("memoryfile: close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("memoryfile: replace file: %w", err)
	}
	removeTemp = false
	return nil
}

func fileExists(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
