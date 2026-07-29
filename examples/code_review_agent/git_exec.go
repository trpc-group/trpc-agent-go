//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var hardenedGitEnvironmentValues = []string{
	"GIT_CONFIG_NOSYSTEM=1",
	"GIT_CONFIG_GLOBAL=" + os.DevNull,
	"GIT_ATTR_NOSYSTEM=1",
	"GIT_OPTIONAL_LOCKS=0",
	"GIT_TERMINAL_PROMPT=0",
}

func newHardenedGitCommand(
	ctx context.Context,
	repoPath string,
	args ...string,
) (*exec.Cmd, error) {
	root := strings.TrimSpace(repoPath)
	if root == "" {
		return nil, fmt.Errorf("repository path is empty")
	}
	resolvedRoot, err := resolveExistingPath(root)
	if err != nil {
		return nil, fmt.Errorf("resolve repository path: %w", err)
	}
	gitArgs := []string{
		"--no-pager",
		"--literal-pathspecs",
		"-c", "core.fsmonitor=false",
		"-c", "diff.external=",
		"-c", "color.ui=false",
		"-c", "safe.directory=" + filepath.ToSlash(resolvedRoot),
	}
	gitArgs = append(gitArgs, args...)
	cmd := exec.CommandContext(ctx, "git", gitArgs...)
	cmd.Dir = resolvedRoot
	cmd.Env = hardenedGitEnvironment(os.Environ())
	return cmd, nil
}

func hardenedGitEnvironment(environ []string) []string {
	result := make([]string, 0, len(environ)+len(hardenedGitEnvironmentValues))
	for _, entry := range environ {
		name, _, ok := strings.Cut(entry, "=")
		if !ok {
			result = append(result, entry)
			continue
		}
		upper := strings.ToUpper(name)
		if strings.HasPrefix(upper, "GIT_") || upper == "PAGER" || upper == "LESS" || upper == "LV" {
			continue
		}
		result = append(result, entry)
	}
	return append(result, hardenedGitEnvironmentValues...)
}
