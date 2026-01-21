//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package file

import (
	"context"
	"path/filepath"
	"slices"
	"strings"

	ds "github.com/bmatcuk/doublestar/v4"
	"trpc.group/trpc-go/trpc-agent-go/internal/fileref"
)

type workspaceIndex struct {
	files []string
	dirs  []string
}

func buildWorkspaceIndex(ctx context.Context) workspaceIndex {
	seenDirs := make(map[string]struct{})
	files := make([]string, 0)

	for _, f := range fileref.WorkspaceFiles(ctx) {
		name := filepath.Clean(strings.TrimSpace(f.Name))
		if name == "" || name == "." {
			continue
		}
		files = append(files, name)

		dir := filepath.Dir(name)
		for dir != "." && dir != "" {
			seenDirs[dir] = struct{}{}
			next := filepath.Dir(dir)
			if next == dir {
				break
			}
			dir = next
		}
	}

	dirs := make([]string, 0, len(seenDirs))
	for d := range seenDirs {
		dirs = append(dirs, d)
	}
	slices.Sort(files)
	slices.Sort(dirs)
	return workspaceIndex{files: files, dirs: dirs}
}

func matchWorkspacePaths(
	ctx context.Context,
	dir string,
	pattern string,
	caseSensitive bool,
) ([]string, []string, error) {
	if strings.TrimSpace(pattern) == "" {
		return nil, nil, nil
	}
	sep := string(filepath.Separator)
	base := filepath.Clean(strings.TrimSpace(dir))
	if base == "." {
		base = ""
	}
	prefix := base
	if prefix != "" {
		prefix += sep
	}

	idx := buildWorkspaceIndex(ctx)
	var files []string
	var folders []string

	for _, p := range idx.files {
		if prefix != "" && !strings.HasPrefix(p, prefix) {
			continue
		}
		rel := strings.TrimPrefix(p, prefix)
		ok, err := matchWorkspacePattern(pattern, rel, caseSensitive)
		if err != nil {
			return nil, nil, err
		}
		if ok {
			files = append(files, fileref.WorkspaceRef(p))
		}
	}

	for _, d := range idx.dirs {
		if prefix != "" && !strings.HasPrefix(d, prefix) {
			continue
		}
		rel := strings.TrimPrefix(d, prefix)
		ok, err := matchWorkspacePattern(pattern, rel, caseSensitive)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			ok, err = matchWorkspacePattern(
				pattern,
				rel+sep,
				caseSensitive,
			)
			if err != nil {
				return nil, nil, err
			}
		}
		if ok {
			folders = append(folders, fileref.WorkspaceRef(d))
		}
	}

	slices.Sort(files)
	slices.Sort(folders)
	return files, folders, nil
}

func matchWorkspacePattern(
	pattern string,
	name string,
	caseSensitive bool,
) (bool, error) {
	pat := filepath.ToSlash(strings.TrimSpace(pattern))
	n := filepath.ToSlash(strings.TrimSpace(name))
	if !caseSensitive {
		pat = strings.ToLower(pat)
		n = strings.ToLower(n)
	}
	return ds.Match(pat, n)
}
