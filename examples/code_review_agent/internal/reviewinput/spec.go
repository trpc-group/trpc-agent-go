//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package reviewinput

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

type resolvedSpec struct {
	inputKind        string
	diffFile         string
	repoPath         string
	paths            []string
	fixtureRepoIsGit bool
}

// resolveSpec expands external CLI choices into concrete sources while keeping
// fixture lookup and path-file I/O inside the input module. It intentionally
// leaves Git and diff parsing to Prepare so all input kinds share that path.
func resolveSpec(spec Spec, fixtureRoot string) (resolved resolvedSpec, err error) {
	inputKind, err := deriveInputKind(spec)
	if err != nil {
		return resolvedSpec{}, err
	}

	paths, err := loadRequestedPaths(spec.Paths, spec.PathsFile)
	if err != nil {
		return resolvedSpec{}, err
	}
	if spec.Fixture != "" {
		if len(paths) > 0 {
			return resolvedSpec{}, errors.New("fixture owns its path scope and cannot be combined with paths or paths-file")
		}
		return resolveFixture(spec.Fixture, fixtureRoot)
	}
	return resolvedSpec{
		inputKind: inputKind,
		diffFile:  strings.TrimSpace(spec.DiffFile),
		repoPath:  strings.TrimSpace(spec.RepoPath),
		paths:     paths,
	}, nil
}

// deriveInputKind validates only the flag shape. It intentionally performs no
// filesystem I/O so missing diff, paths, or fixture files fail after the task
// exists and are captured by the task lifecycle.
func deriveInputKind(spec Spec) (inputKind string, err error) {
	diffSet := strings.TrimSpace(spec.DiffFile) != ""
	repoSet := strings.TrimSpace(spec.RepoPath) != ""
	fixtureSet := strings.TrimSpace(spec.Fixture) != ""
	if !diffSet && !repoSet && !fixtureSet {
		return "", errors.New("one of diff-file, repo-path, or fixture is required")
	}
	if fixtureSet && (diffSet || repoSet) {
		return "", errors.New("fixture cannot be combined with diff-file or repo-path")
	}
	if fixtureSet && (len(spec.Paths) > 0 || strings.TrimSpace(spec.PathsFile) != "") {
		return "", errors.New("fixture owns its path scope and cannot be combined with paths or paths-file")
	}
	switch {
	case fixtureSet:
		return InputKindFixture, nil
	case diffSet:
		return InputKindDiffFile, nil
	default:
		return InputKindRepoPath, nil
	}
}

// loadRequestedPaths merges comma-separated CLI values and line-oriented path
// files, normalizes them once, and returns deterministic ordering for task
// summaries and path-scoped diff rendering.
func loadRequestedPaths(inline []string, pathsFile string) (paths []string, err error) {
	paths = append([]string(nil), inline...)
	if strings.TrimSpace(pathsFile) != "" {
		f, err := os.Open(pathsFile)
		if err != nil {
			return nil, fmt.Errorf("open paths file: %w", err)
		}
		defer f.Close()
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			paths = append(paths, line)
		}
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("read paths file: %w", err)
		}
	}

	unique := make(map[string]struct{}, len(paths))
	for _, candidate := range paths {
		for _, item := range strings.Split(candidate, ",") {
			normalized, err := normalizeRequestedPath(item)
			if err != nil {
				return nil, err
			}
			if normalized != "" {
				unique[normalized] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(unique))
	for item := range unique {
		out = append(out, item)
	}
	sort.Strings(out)
	return out, nil
}

// normalizeRequestedPath establishes the repository-relative path invariant at
// the CLI boundary, before a value can reach Git arguments or snapshot joins.
func normalizeRequestedPath(candidate string) (normalized string, err error) {
	candidate = strings.TrimSpace(strings.ReplaceAll(candidate, "\\", "/"))
	if candidate == "" {
		return "", nil
	}
	cleaned := path.Clean(candidate)
	if cleaned == "." || path.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("review path %q must stay within the review root", candidate)
	}
	return cleaned, nil
}

// resolveFixture recognizes only the same change.diff, repo/, and paths.txt
// inputs accepted by the normal pipeline. Test-only metadata such as
// expectations.json stays outside the Agent workspace and requires no Skill
// knowledge.
func resolveFixture(name, fixtureRoot string) (resolved resolvedSpec, err error) {
	name = strings.TrimSpace(name)
	if name == "" || !filepath.IsLocal(name) {
		return resolvedSpec{}, fmt.Errorf("fixture %q must be a local name", name)
	}
	if fixtureRoot == "" {
		fixtureRoot = filepath.Join("testdata", "fixtures")
	}
	root, err := filepath.Abs(filepath.Join(fixtureRoot, name))
	if err != nil {
		return resolvedSpec{}, fmt.Errorf("resolve fixture: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return resolvedSpec{}, fmt.Errorf("open fixture %q: %w", name, err)
	}
	if !info.IsDir() {
		return resolvedSpec{}, fmt.Errorf("fixture %q is not a directory", name)
	}

	resolved = resolvedSpec{inputKind: InputKindFixture}
	diffPath := filepath.Join(root, "change.diff")
	diffExists, err := optionalFixtureEntry(diffPath, false)
	if err != nil {
		return resolvedSpec{}, fmt.Errorf("inspect fixture diff: %w", err)
	}
	if diffExists {
		resolved.diffFile = diffPath
	}

	repoPath := filepath.Join(root, "repo")
	repoExists, err := optionalFixtureEntry(repoPath, true)
	if err != nil {
		return resolvedSpec{}, fmt.Errorf("inspect fixture repo: %w", err)
	}
	if repoExists {
		resolved.repoPath = repoPath
		resolved.fixtureRepoIsGit, err = pathExists(filepath.Join(repoPath, ".git"))
		if err != nil {
			return resolvedSpec{}, fmt.Errorf("inspect fixture Git metadata: %w", err)
		}
	}

	pathsPath := filepath.Join(root, "paths.txt")
	pathsExist, err := optionalFixtureEntry(pathsPath, false)
	if err != nil {
		return resolvedSpec{}, fmt.Errorf("inspect fixture paths: %w", err)
	}
	if pathsExist {
		resolved.paths, err = loadRequestedPaths(nil, pathsPath)
		if err != nil {
			return resolvedSpec{}, err
		}
	}
	if resolved.diffFile == "" && resolved.repoPath == "" {
		return resolvedSpec{}, fmt.Errorf("fixture %q must contain change.diff or repo/", name)
	}
	return resolved, nil
}

// optionalFixtureEntry reports whether a fixture entry exists with the
// expected directory shape. Missing or wrong-shaped optional entries are
// treated as absent; filesystem access failures remain explicit errors.
func optionalFixtureEntry(path string, wantDirectory bool) (exists bool, err error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.IsDir() == wantDirectory, nil
}

// pathExists is used for .git, which may be either a directory or a worktree
// pointer file.
func pathExists(path string) (exists bool, err error) {
	_, err = os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}
