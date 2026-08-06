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
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type cliConfig struct {
	diffFile       string
	repoPath       string
	files          string
	fixture        string
	runtime        string
	allowLocal     bool
	dryRun         bool
	fakeModel      bool
	staticcheck    bool
	outputDir      string
	dbPath         string
	skillsRoot     string
	containerImage string
	timeout        time.Duration
}

func main() {
	config := parseFlags()
	if err := runCLI(config); err != nil {
		fmt.Fprintf(os.Stderr, "code review failed: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() cliConfig {
	var config cliConfig
	flag.StringVar(&config.diffFile, "diff-file", "", "unified diff or PR patch")
	flag.StringVar(&config.repoPath, "repo-path", "", "Git working tree to review")
	flag.StringVar(
		&config.files, "files", "",
		"comma-separated repository-relative paths to include",
	)
	flag.StringVar(
		&config.fixture, "fixture", "",
		"fixture name under testdata/fixtures",
	)
	flag.StringVar(
		&config.runtime, "runtime", "container",
		"sandbox runtime: container|local|fake",
	)
	flag.BoolVar(
		&config.allowLocal, "allow-local", false,
		"allow the development-only local runtime",
	)
	flag.BoolVar(
		&config.dryRun, "dry-run", false,
		"run the full pipeline with a fake sandbox",
	)
	flag.BoolVar(
		&config.fakeModel, "fake-model", false,
		"label the deterministic no-API-key path as fake-model",
	)
	flag.BoolVar(
		&config.staticcheck, "staticcheck", false,
		"run optional staticcheck inside the sandbox",
	)
	flag.StringVar(
		&config.outputDir, "output-dir", ".",
		"directory for review_report.json and review_report.md",
	)
	flag.StringVar(
		&config.dbPath, "db", "",
		"SQLite database path (default: <output-dir>/reviews.db)",
	)
	flag.StringVar(
		&config.skillsRoot, "skills-root", "skills",
		"root containing the code-review skill",
	)
	flag.StringVar(
		&config.containerImage, "container-image",
		defaultReviewContainerImage,
		"container image with Go, Bash, and Python",
	)
	flag.DurationVar(
		&config.timeout, "timeout", 2*time.Minute,
		"overall review timeout",
	)
	flag.Parse()
	return config
}

func runCLI(config cliConfig) error {
	if config.timeout <= 0 || config.timeout > 10*time.Minute {
		return errors.New("timeout must be between zero and 10 minutes")
	}
	diff, inputKind, repoPath, err := loadReviewInput(config)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(config.outputDir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	dbPath := config.dbPath
	if dbPath == "" {
		dbPath = filepath.Join(config.outputDir, "reviews.db")
	} else if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return fmt.Errorf("create database directory: %w", err)
	}
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	runtimeName := config.runtime
	if config.dryRun {
		runtimeName = "fake"
	}
	sandbox, err := createSandbox(
		runtimeName, config.allowLocal, config.containerImage,
	)
	if err != nil {
		return err
	}
	defer sandbox.Close()
	reviewer, err := NewReviewer(store, sandbox, config.skillsRoot)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), config.timeout)
	defer cancel()
	report, err := reviewer.Review(ctx, ReviewRequest{
		Diff: diff, InputKind: inputKind, RepoPath: repoPath,
		Runtime: runtimeName, DryRun: config.dryRun,
		FakeModel: config.fakeModel, RunStaticcheck: config.staticcheck,
		OutputDir: config.outputDir,
	})
	if err != nil {
		return err
	}
	fmt.Printf(
		"review %s: %s (%d findings, %d warnings)\n",
		report.TaskID, report.Conclusion,
		len(report.Findings), len(report.Warnings),
	)
	fmt.Printf("reports: %s\n", filepath.Clean(config.outputDir))
	fmt.Printf("database: %s\n", filepath.Clean(dbPath))
	return nil
}

func loadReviewInput(
	config cliConfig,
) ([]byte, string, string, error) {
	selected := 0
	if config.diffFile != "" {
		selected++
	}
	if config.fixture != "" {
		selected++
	}
	if config.repoPath != "" && config.diffFile == "" && config.fixture == "" {
		selected++
	}
	if selected != 1 {
		return nil, "", "", errors.New(
			"select exactly one of --diff-file, --fixture, or --repo-path",
		)
	}
	repoPath := ""
	if config.repoPath != "" {
		absolute, err := filepath.Abs(config.repoPath)
		if err != nil {
			return nil, "", "", fmt.Errorf("resolve repository path: %w", err)
		}
		repoPath = absolute
	}
	switch {
	case config.diffFile != "":
		data, err := os.ReadFile(config.diffFile)
		if err != nil {
			return nil, "", "", fmt.Errorf("read diff file: %w", err)
		}
		return data, "diff_file", repoPath, nil
	case config.fixture != "":
		path, err := fixturePath(config.fixture)
		if err != nil {
			return nil, "", "", err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, "", "", fmt.Errorf("read fixture: %w", err)
		}
		return data, "fixture", repoPath, nil
	default:
		data, err := gitWorkingTreeDiff(repoPath, parseFileList(config.files))
		if err != nil {
			return nil, "", "", err
		}
		return data, "git_worktree", repoPath, nil
	}
}

func fixturePath(name string) (string, error) {
	clean := filepath.Clean(strings.TrimSpace(name))
	if clean == "." || filepath.IsAbs(clean) ||
		clean != filepath.Base(clean) {
		return "", errors.New("fixture must be a simple file name")
	}
	if filepath.Ext(clean) == "" {
		clean += ".diff"
	}
	return filepath.Join("testdata", "fixtures", clean), nil
}

func parseFileList(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		item = filepath.ToSlash(filepath.Clean(strings.TrimSpace(item)))
		if item == "" || item == "." || filepath.IsAbs(item) ||
			item == ".." || strings.HasPrefix(item, "../") {
			continue
		}
		result = append(result, item)
	}
	return result
}

func gitWorkingTreeDiff(
	repoPath string,
	files []string,
) ([]byte, error) {
	if repoPath == "" {
		return nil, errors.New("repository path is required")
	}
	args := []string{"diff", "--no-ext-diff", "--unified=3", "HEAD", "--"}
	args = append(args, files...)
	tracked, err := runGit(repoPath, args, false)
	if err != nil {
		return nil, err
	}
	untracked, err := untrackedFiles(repoPath, files)
	if err != nil {
		return nil, err
	}
	var combined bytes.Buffer
	combined.Write(tracked)
	for _, file := range untracked {
		patch, err := runGit(
			repoPath,
			[]string{"diff", "--no-index", "--unified=3", "--", "/dev/null", file},
			true,
		)
		if err != nil {
			return nil, err
		}
		combined.Write(patch)
	}
	if combined.Len() == 0 {
		return nil, errors.New("repository has no selected changes")
	}
	if combined.Len() > maxDiffBytes {
		return nil, fmt.Errorf("generated diff exceeds %d-byte limit", maxDiffBytes)
	}
	return combined.Bytes(), nil
}

func untrackedFiles(repoPath string, selected []string) ([]string, error) {
	output, err := runGit(
		repoPath,
		[]string{"ls-files", "--others", "--exclude-standard", "-z"},
		false,
	)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]bool, len(selected))
	for _, file := range selected {
		allowed[file] = true
	}
	var files []string
	for _, value := range bytes.Split(output, []byte{0}) {
		file := filepath.ToSlash(string(value))
		if file == "" || (len(allowed) > 0 && !allowed[file]) {
			continue
		}
		if file == ".." || strings.HasPrefix(file, "../") || filepath.IsAbs(file) {
			continue
		}
		files = append(files, file)
	}
	return files, nil
}

func runGit(
	repoPath string,
	args []string,
	allowDiffExit bool,
) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = repoPath
	output, err := command.CombinedOutput()
	if err == nil {
		return output, nil
	}
	var exitError *exec.ExitError
	if allowDiffExit && errors.As(err, &exitError) &&
		exitError.ExitCode() == 1 {
		return output, nil
	}
	return nil, fmt.Errorf("git %s: %s: %w",
		strings.Join(args, " "), Redact(string(output)), err)
}
