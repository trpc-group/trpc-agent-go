//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package input loads review inputs from diff files, git worktrees,
// and repository snapshots.
package input

import (
	"os"
	"strings"
)

// Input represents the loaded review input from a source.
type Input struct {
	DiffText   string
	RepoPath   string
	SourceType string // "diff_file", "repo_path", "git_worktree"
}

// LoadFromDiffFile reads a unified diff from a file path.
func LoadFromDiffFile(path string) (*Input, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return &Input{
		DiffText:   string(data),
		SourceType: "diff_file",
	}, nil
}

// LoadFromRepoPath loads a diff from a repository working directory.
// For now it returns a placeholder; full git integration uses the git CLI.
func LoadFromRepoPath(path string) (*Input, error) {
	return &Input{
		RepoPath:   path,
		SourceType: "repo_path",
	}, nil
}

// SnapshotSource provides access to a repository's files for analysis.
type SnapshotSource struct {
	RepoPath string
}

// Snapshot takes a snapshot of the repository at repoPath.
// Returns a map of relative file path to its content.
func Snapshot(repoPath string) (map[string]string, error) {
	entries, err := os.ReadDir(repoPath)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.HasSuffix(entry.Name(), ".go") {
			data, err := os.ReadFile(repoPath + "/" + entry.Name())
			if err != nil {
				continue
			}
			result[entry.Name()] = string(data)
		}
	}
	return result, nil
}

// CollectFileList returns a list of Go source files in the repo.
func CollectFileList(repoPath string) ([]string, error) {
	entries, err := os.ReadDir(repoPath)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.HasSuffix(entry.Name(), ".go") && !strings.HasSuffix(entry.Name(), "_test.go") {
			files = append(files, entry.Name())
		}
	}
	return files, nil
}
