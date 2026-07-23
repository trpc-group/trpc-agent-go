//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
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

func loadInput(ctx context.Context, opts ReviewOptions) (string, string, error) {
	switch {
	case opts.Fixture != "":
		raw, err := loadFixture(opts.FixtureDir, opts.Fixture)
		return "fixture", raw, err
	case opts.DiffFile != "":
		b, err := os.ReadFile(opts.DiffFile)
		return "diff_file", string(b), err
	case opts.RepoPath != "":
		raw, err := gitDiff(ctx, opts.RepoPath)
		return "repo", raw, err
	case opts.FileList != "":
		raw, err := diffFromFileList(opts.FileList)
		return "file_list", raw, err
	default:
		return "empty", "", nil
	}
}

func validateReviewInputs(opts ReviewOptions) error {
	count := 0
	for _, present := range []bool{
		opts.Fixture != "",
		opts.DiffFile != "",
		opts.RepoPath != "",
		opts.FileList != "",
	} {
		if present {
			count++
		}
	}
	switch {
	case count == 0:
		return fmt.Errorf("exactly one input source is required: --fixture, --diff-file, --repo-path, or --file-list")
	case count > 1:
		return fmt.Errorf("input sources are mutually exclusive: choose exactly one of --fixture, --diff-file, --repo-path, or --file-list")
	default:
		return nil
	}
}

func loadFixture(dir string, name string) (string, error) {
	if dir == "" {
		dir = filepath.Join("code_review_agent", "testdata", "fixtures")
	}
	if name == "all" {
		return "", nil
	}
	name, err := validateFixtureName(name)
	if err != nil {
		return "", err
	}
	if filepath.Ext(name) == "" {
		name += ".diff"
	}
	p := filepath.Join(dir, name)
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func validateFixtureName(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("empty fixture name")
	}
	if filepath.IsAbs(name) || strings.ContainsRune(name, '\x00') {
		return "", fmt.Errorf("invalid fixture name %q", name)
	}
	clean := filepath.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("fixture path traversal is not allowed: %q", name)
	}
	return clean, nil
}

func fixtureNames(dir string) ([]string, error) {
	if dir == "" {
		dir = filepath.Join("code_review_agent", "testdata", "fixtures")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".diff" {
			continue
		}
		names = append(names, strings.TrimSuffix(entry.Name(), ".diff"))
	}
	return names, nil
}

func gitDiff(ctx context.Context, repo string) (string, error) {
	if _, err := gitTrackedFiles(ctx, repo); err != nil {
		return "", err
	}
	if hasHead(ctx, repo) {
		out, err := runGitOutput(ctx, repo, "diff", "--no-ext-diff", "--no-textconv", "HEAD")
		if err != nil {
			return "", err
		}
		return string(out), nil
	}
	cached, err := runGitOutput(ctx, repo, "diff", "--cached", "--no-ext-diff", "--no-textconv", "--root")
	if err != nil {
		return "", err
	}
	worktree, err := runGitOutput(ctx, repo, "diff", "--no-ext-diff", "--no-textconv")
	if err != nil {
		return "", err
	}
	switch {
	case len(cached) == 0:
		return string(worktree), nil
	case len(worktree) == 0:
		return string(cached), nil
	default:
		return string(cached) + "\n" + string(worktree), nil
	}
}

func gitTrackedFiles(ctx context.Context, repo string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "ls-files", "-z")
	cmd.Dir = repo
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w: %s", err, stderr.String())
	}
	files := []string{}
	for _, raw := range bytes.Split(out, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		p, err := validateRepoPath(string(raw))
		if err != nil {
			return nil, fmt.Errorf("git ls-files path: %w", err)
		}
		files = append(files, p)
	}
	return files, nil
}

func hasHead(ctx context.Context, repo string) bool {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--verify", "HEAD")
	cmd.Dir = repo
	return cmd.Run() == nil
}

func runGitOutput(ctx context.Context, repo string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repo
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, stderr.String())
	}
	return out, nil
}

func diffFromFileList(list string) (string, error) {
	parts := strings.Split(list, ",")
	var b strings.Builder
	for _, raw := range parts {
		if raw == "" {
			continue
		}
		p, err := validateRepoPath(raw)
		if err != nil {
			return "", err
		}
		b.WriteString("diff --git a/")
		b.WriteString(p)
		b.WriteString(" b/")
		b.WriteString(p)
		b.WriteString("\n--- a/")
		b.WriteString(p)
		b.WriteString("\n+++ b/")
		b.WriteString(p)
		b.WriteString("\n@@ -0,0 +1,0 @@\n")
	}
	return b.String(), nil
}
