//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	projectDocFileName     = "AGENTS.md"
	projectDocOverrideName = "AGENTS.override.md"
	projectDocGitMarker    = ".git"
	projectDocMaxBytes     = 16 * 1024
)

var projectDocFileNames = []string{
	projectDocFileName,
	projectDocOverrideName,
}

func resolveProjectDocs(cwd string) (string, error) {
	paths, err := discoverProjectDocPaths(cwd)
	if err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "", nil
	}

	parts := make([]string, 0, len(paths))
	remaining := projectDocMaxBytes
	for _, path := range paths {
		if remaining <= 0 {
			break
		}
		content, n, err := readTrimmedTextFile(path, remaining)
		if err != nil {
			return "", err
		}
		if content == "" {
			continue
		}
		parts = append(parts, content)
		remaining -= n
	}
	return strings.Join(parts, "\n\n"), nil
}

func discoverProjectDocPaths(cwd string) ([]string, error) {
	dirs, err := projectDocDirs(cwd)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(dirs)*len(projectDocFileNames))
	for _, dir := range dirs {
		for _, name := range projectDocFileNames {
			path := filepath.Join(dir, name)
			info, err := os.Stat(path)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				return nil, fmt.Errorf("stat project doc %s: %w", path, err)
			}
			if info.IsDir() {
				continue
			}
			paths = append(paths, path)
		}
	}
	return paths, nil
}

func projectDocDirs(cwd string) ([]string, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return nil, errors.New("project docs: empty cwd")
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return nil, fmt.Errorf("resolve cwd: %w", err)
	}

	dirs := []string{abs}
	for {
		if hasProjectDocRootMarker(abs) {
			break
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			break
		}
		abs = parent
		dirs = append(dirs, abs)
	}
	reverseStrings(dirs)
	return dirs, nil
}

func hasProjectDocRootMarker(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, projectDocGitMarker))
	return err == nil
}

func readTrimmedTextFile(path string, limit int) (string, int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", 0, fmt.Errorf("read project doc %s: %w", path, err)
	}
	n := len(raw)
	if limit > 0 && n > limit {
		raw = raw[:limit]
		n = limit
	}
	return strings.TrimSpace(string(raw)), n, nil
}

func reverseStrings(values []string) {
	for left, right := 0, len(values)-1; left < right; left, right =
		left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}
