//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const maxModuleRoots = 64

func resolveModuleRoots(repoPath string, packages []string) ([]string, error) {
	repoRoot, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("resolve repository root: %w", err)
	}
	repoRoot, err = filepath.EvalSymlinks(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve repository symlinks: %w", err)
	}
	info, err := os.Stat(repoRoot)
	if err != nil || !info.IsDir() {
		return nil, errors.Join(errors.New("repository root is not a directory"), err)
	}
	if len(packages) == 0 {
		return []string{"."}, nil
	}
	selected := make(map[string]struct{})
	for _, packagePath := range packages {
		moduleRoot, findErr := findModuleRoot(repoRoot, packagePath)
		if findErr != nil {
			return nil, findErr
		}
		selected[moduleRoot] = struct{}{}
		if len(selected) > maxModuleRoots {
			return nil, errors.New("changed module root count exceeds limit")
		}
	}
	result := make([]string, 0, len(selected))
	for root := range selected {
		result = append(result, root)
	}
	sort.Strings(result)
	return result, nil
}

func findModuleRoot(repoRoot, packagePath string) (string, error) {
	if !validRelativeModulePath(packagePath) {
		return "", fmt.Errorf("changed package path %q is outside repository bounds", packagePath)
	}
	current := filepath.Join(repoRoot, filepath.FromSlash(packagePath))
	for {
		if info, err := os.Stat(current); err == nil {
			if !info.IsDir() {
				return "", fmt.Errorf("changed package path %q is not a directory", packagePath)
			}
			resolved, resolveErr := filepath.EvalSymlinks(current)
			if resolveErr != nil || !withinRepository(repoRoot, resolved) {
				return "", errors.Join(resolveErr, fmt.Errorf("changed package path %q escapes repository", packagePath))
			}
			current = resolved
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("inspect changed package path %q: %w", packagePath, err)
		}
		goMod := filepath.Join(current, "go.mod")
		if info, err := os.Lstat(goMod); err == nil {
			if !info.Mode().IsRegular() {
				return "", fmt.Errorf("module root %q has unsafe go.mod", packagePath)
			}
			relative, relativeErr := filepath.Rel(repoRoot, current)
			if relativeErr != nil || !validRelativeModulePath(filepath.ToSlash(relative)) {
				return "", errors.Join(relativeErr, fmt.Errorf("module root for %q escapes repository", packagePath))
			}
			return filepath.ToSlash(relative), nil
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("inspect go.mod for %q: %w", packagePath, err)
		}
		if filepath.Clean(current) == filepath.Clean(repoRoot) {
			return "", fmt.Errorf("changed package %q is not contained in a Go module", packagePath)
		}
		parent := filepath.Dir(current)
		if parent == current || !withinRepository(repoRoot, parent) {
			return "", fmt.Errorf("changed package %q escapes repository while resolving module", packagePath)
		}
		current = parent
	}
}

func validRelativeModulePath(value string) bool {
	return value != "" && !filepath.IsAbs(value) && !strings.Contains(value, `\`) &&
		path.Clean(value) == value && value != ".." && !strings.HasPrefix(value, "../")
}

func withinRepository(root, name string) bool {
	relative, err := filepath.Rel(root, name)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
