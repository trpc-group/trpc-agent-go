//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package inputsource reads the review input modes supported by the example.
package inputsource

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

// Options describes all supported review input sources.
type Options struct {
	FixtureDir string
	DiffFile   string
	RepoPath   string
	FileList   string
}

// Source is the normalized input handed to the review orchestrator.
type Source struct {
	Type         string
	Diff         string
	FixtureNames []string
	FileList     []string
	RepoPath     string
	Summary      string
}

// Read resolves exactly one configured input source. Fixture input remains the
// deterministic default used by tests and golden reports.
func Read(ctx context.Context, opts Options) (Source, error) {
	if err := ctx.Err(); err != nil {
		return Source{}, err
	}
	selected := configured(opts)
	if len(selected) > 1 {
		return Source{}, fmt.Errorf("choose only one input source: %s", strings.Join(selected, ", "))
	}
	switch {
	case strings.TrimSpace(opts.DiffFile) != "":
		return readDiffFile(opts.DiffFile)
	case strings.TrimSpace(opts.RepoPath) != "":
		return readRepoDiff(ctx, opts.RepoPath)
	case strings.TrimSpace(opts.FileList) != "":
		return readFileList(opts.FileList)
	default:
		dir := opts.FixtureDir
		if dir == "" {
			dir = "testdata/fixtures"
		}
		return readFixtures(dir)
	}
}

func configured(opts Options) []string {
	var out []string
	if strings.TrimSpace(opts.DiffFile) != "" {
		out = append(out, "--diff-file")
	}
	if strings.TrimSpace(opts.RepoPath) != "" {
		out = append(out, "--repo-path")
	}
	if strings.TrimSpace(opts.FileList) != "" {
		out = append(out, "--file-list")
	}
	return out
}

func readFixtures(dir string) (Source, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Source{}, fmt.Errorf("read fixture dir: %w", err)
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".diff") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return Source{}, fmt.Errorf("read fixture %s: %w", name, err)
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.Write(raw)
		if !strings.HasSuffix(string(raw), "\n") {
			b.WriteString("\n")
		}
	}
	return Source{
		Type:         review.InputTypeFixture,
		Diff:         b.String(),
		FixtureNames: names,
		Summary:      fmt.Sprintf("Reviewed %d diff fixtures.", len(names)),
	}, nil
}

func readDiffFile(path string) (Source, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Source{}, fmt.Errorf("read diff file: %w", err)
	}
	return Source{
		Type:    review.InputTypeDiffFile,
		Diff:    string(raw),
		Summary: fmt.Sprintf("Reviewed unified diff file %s.", filepath.Base(path)),
	}, nil
}

func readRepoDiff(ctx context.Context, repoPath string) (Source, error) {
	abs, err := filepath.Abs(repoPath)
	if err != nil {
		return Source{}, fmt.Errorf("resolve repo path: %w", err)
	}
	cmd := exec.CommandContext(ctx, "git", "-C", abs, "diff", "--no-ext-diff", "--binary")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		return Source{}, fmt.Errorf("read git diff: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return Source{
		Type:     review.InputTypeRepo,
		Diff:     string(raw),
		RepoPath: abs,
		Summary:  fmt.Sprintf("Reviewed git workspace diff from %s.", abs),
	}, nil
}

func readFileList(path string) (Source, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Source{}, fmt.Errorf("read file list: %w", err)
	}
	var files []string
	for _, line := range strings.Split(string(raw), "\n") {
		file := filepath.ToSlash(strings.TrimSpace(line))
		if file == "" || strings.HasPrefix(file, "#") {
			continue
		}
		files = append(files, file)
	}
	sort.Strings(files)
	return Source{
		Type:     review.InputTypeFileList,
		FileList: files,
		Summary:  fmt.Sprintf("Reviewed %d changed file paths from %s.", len(files), filepath.Base(path)),
	}, nil
}
