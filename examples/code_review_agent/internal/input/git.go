//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package input provides git-based diff extraction for the --repo-path input mode.
package input

import (
	"fmt"
	"os/exec"
	"strings"
)

// FromRepo returns the unified diff between baseRef and the current HEAD.
// Uses three-dot syntax: git diff baseRef...HEAD
// Three-dot diff shows changes from the merge-base of baseRef and HEAD to HEAD.
// This is the correct semantic for PR review: it shows what the feature branch
// changed, excluding commits on main that happened after the branch point.
func FromRepo(repoPath, baseRef string) (string, error) {
	if baseRef == "" {
		baseRef = "origin/main"
	}

	// Try baseRef first, fall back to local main, then HEAD~1.
	refs := []string{baseRef, "main", "HEAD~1"}
	var lastErr error
	for _, ref := range refs {
		cmd := exec.Command("git", "diff", ref+"...HEAD")
		cmd.Dir = repoPath
		out, err := cmd.CombinedOutput()
		if err == nil {
			return string(out), nil
		}
		lastErr = fmt.Errorf("git diff %s...HEAD: %w\n%s", ref, err, out)
	}
	return "", lastErr
}

// HasGitRepo checks if the given path is inside a git repository.
func HasGitRepo(path string) bool {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = path
	return cmd.Run() == nil
}

// CurrentBranch returns the current git branch name.
func CurrentBranch(repoPath string) string {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.TrimSuffix(string(out), "\n"))
}
