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
	"bytes"
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
	root := repoPath
	if root == "" {
		return nil, fmt.Errorf("repository path is empty")
	}
	resolvedRoot, err := resolveExistingPath(root)
	if err != nil {
		return nil, fmt.Errorf("resolve repository path: %w", err)
	}
	safeDirectory, err := findGitSafeDirectory(resolvedRoot)
	if err != nil {
		return nil, err
	}
	gitArgs := []string{
		"--no-pager",
		"--literal-pathspecs",
		"-c", "core.fsmonitor=false",
		"-c", "diff.external=",
		"-c", "color.ui=false",
		"-c", "safe.directory=" + filepath.ToSlash(safeDirectory),
	}
	gitArgs = append(gitArgs, args...)
	cmd := exec.CommandContext(ctx, "git", gitArgs...)
	cmd.Dir = resolvedRoot
	cmd.Env = hardenedGitEnvironment(os.Environ())
	return cmd, nil
}

func findGitSafeDirectory(start string) (string, error) {
	current := start
	for {
		_, err := os.Lstat(filepath.Join(current, ".git"))
		switch {
		case err == nil:
			return current, nil
		case !os.IsNotExist(err):
			return "", fmt.Errorf("inspect git worktree marker: %w", err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return start, nil
		}
		current = parent
	}
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

func runGitCommand(
	ctx context.Context,
	repoPath string,
	args []string,
) ([]byte, []byte, error) {
	cmd, err := newHardenedGitCommand(ctx, repoPath, args...)
	if err != nil {
		return nil, nil, err
	}
	var stdout limitBuffer
	var stderr limitBuffer
	stdout.limit = int(maxDiffBytes)
	stderr.limit = int(maxStderrBytes)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	if ctx.Err() != nil {
		return stdout.Bytes(), stderr.Bytes(), fmt.Errorf(
			"git command timed out after %s",
			gitDiffTimeout,
		)
	}
	if stdout.truncated {
		return stdout.Bytes(), stderr.Bytes(), fmt.Errorf(
			"git command output exceeds %d bytes",
			maxDiffBytes,
		)
	}
	if err != nil {
		return stdout.Bytes(), stderr.Bytes(), err
	}
	return stdout.Bytes(), stderr.Bytes(), nil
}

func gitCommandError(operation string, err error, stderr []byte) error {
	message := strings.TrimSpace(string(stderr))
	if message == "" {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return fmt.Errorf("%s: %w: %s", operation, err, message)
}

func validateGitWorktreeRoot(repoPath string, output []byte) (string, error) {
	rootLine, err := parseGitOutputLine(output)
	if err != nil {
		return "", fmt.Errorf("resolve git worktree root: %w", err)
	}
	rootPath := filepath.FromSlash(rootLine)
	if !filepath.IsAbs(rootPath) {
		return "", fmt.Errorf("resolve git worktree root: git returned a non-absolute path")
	}
	resolvedRoot, err := resolveExistingPath(rootPath)
	if err != nil {
		return "", fmt.Errorf("resolve git worktree root: %w", err)
	}
	if err := requireDirectory(resolvedRoot, "git worktree root"); err != nil {
		return "", fmt.Errorf("resolve git worktree root: %w", err)
	}

	resolvedInput, err := resolveExistingPath(repoPath)
	if err != nil {
		return "", fmt.Errorf("resolve repository path: %w", err)
	}
	if err := requireDirectory(resolvedInput, "repository path"); err != nil {
		return "", err
	}
	if !directoryStaysWithin(resolvedRoot, resolvedInput) {
		return "", fmt.Errorf("repository path is outside the resolved git worktree root")
	}
	return resolvedRoot, nil
}

func directoryStaysWithin(root string, candidate string) bool {
	rootInfo, err := os.Stat(root)
	if err != nil {
		return false
	}
	current := candidate
	for {
		currentInfo, err := os.Stat(current)
		if err != nil {
			return false
		}
		if os.SameFile(rootInfo, currentInfo) {
			return true
		}
		parent := filepath.Dir(current)
		if parent == current {
			return false
		}
		current = parent
	}
}

func parseGitOutputLine(output []byte) (string, error) {
	if bytes.IndexByte(output, 0) >= 0 {
		return "", fmt.Errorf("git output contains a NUL byte")
	}
	line := output
	if len(line) > 0 && line[len(line)-1] == '\n' {
		line = line[:len(line)-1]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
	}
	if len(line) == 0 {
		return "", fmt.Errorf("git returned an empty path")
	}
	if bytes.ContainsAny(line, "\r\n") {
		return "", fmt.Errorf("git returned multiple path lines")
	}
	return string(line), nil
}

func requireDirectory(path string, description string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", description, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", description)
	}
	return nil
}
