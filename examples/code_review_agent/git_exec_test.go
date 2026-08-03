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
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestHardenedGitDiffDoesNotExecuteConfiguredPrograms(t *testing.T) {
	repoRoot := t.TempDir()
	mustRunGit(t, repoRoot, "init")
	mustWriteFile(t, filepath.Join(repoRoot, ".gitattributes"), "*.txt diff=reviewevil\n")
	mustWriteFile(t, filepath.Join(repoRoot, "review.txt"), "before\n")
	mustRunGit(t, repoRoot, "add", ".gitattributes", "review.txt")
	mustCommitGit(t, repoRoot)

	markerRoot := t.TempDir()
	externalMarker := filepath.Join(markerRoot, "external.marker")
	textconvMarker := filepath.Join(markerRoot, "textconv.marker")
	fsmonitorMarker := filepath.Join(markerRoot, "fsmonitor.marker")
	environmentMarker := filepath.Join(markerRoot, "environment.marker")
	externalProgram := writeGitMarkerProgram(t, markerRoot, "external", externalMarker)
	textconvProgram := writeGitMarkerProgram(t, markerRoot, "textconv", textconvMarker)
	fsmonitorProgram := writeGitMarkerProgram(t, markerRoot, "fsmonitor", fsmonitorMarker)
	environmentProgram := writeGitMarkerProgram(t, markerRoot, "environment", environmentMarker)

	mustRunGit(t, repoRoot, "config", "diff.external", filepath.ToSlash(externalProgram))
	mustRunGit(t, repoRoot, "config", "diff.reviewevil.textconv", filepath.ToSlash(textconvProgram))
	mustRunGit(t, repoRoot, "config", "core.fsmonitor", filepath.ToSlash(fsmonitorProgram))
	mustWriteFile(t, filepath.Join(repoRoot, "review.txt"), "after\n")

	t.Setenv("GIT_EXTERNAL_DIFF", filepath.ToSlash(environmentProgram))
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "diff.external")
	t.Setenv("GIT_CONFIG_VALUE_0", filepath.ToSlash(environmentProgram))
	t.Setenv("PAGER", filepath.ToSlash(environmentProgram))

	stdout, stderr, err := runGitCommand(context.Background(), repoRoot, []string{
		"diff", "--no-ext-diff", "--no-textconv", "HEAD", "--",
	})
	if err != nil {
		t.Fatalf("run hardened git diff: %v\nstderr: %s", err, stderr)
	}
	diff := string(stdout)
	if !strings.Contains(diff, "-before") || !strings.Contains(diff, "+after") {
		t.Fatalf("ordinary diff output = %q", diff)
	}
	for _, marker := range []string{externalMarker, textconvMarker, fsmonitorMarker, environmentMarker} {
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("external Git program marker %q exists, stat err = %v", marker, err)
		}
	}
}

func TestHardenedGitEnvironmentRemovesGitAndPagerVariables(t *testing.T) {
	got := hardenedGitEnvironment([]string{
		"Path=C:/bin",
		"SystemRoot=C:/Windows",
		"TEMP=C:/Temp",
		"CUSTOM=value",
		"GIT_EXTERNAL_DIFF=evil",
		"git_config_count=1",
		"GIT_CONFIG_KEY_0=diff.external",
		"GIT_CONFIG_VALUE_0=evil",
		"PAGER=evil",
		"LESS=evil",
		"LV=evil",
	})
	joined := "\x00" + strings.Join(got, "\x00") + "\x00"
	for _, want := range []string{
		"Path=C:/bin",
		"SystemRoot=C:/Windows",
		"TEMP=C:/Temp",
		"CUSTOM=value",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_ATTR_NOSYSTEM=1",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_TERMINAL_PROMPT=0",
	} {
		if !strings.Contains(joined, "\x00"+want+"\x00") {
			t.Fatalf("environment = %#v, missing %q", got, want)
		}
	}
	for _, forbidden := range []string{
		"GIT_EXTERNAL_DIFF=evil",
		"git_config_count=1",
		"GIT_CONFIG_KEY_0=diff.external",
		"GIT_CONFIG_VALUE_0=evil",
		"PAGER=evil",
		"LESS=evil",
		"LV=evil",
	} {
		if strings.Contains(joined, "\x00"+forbidden+"\x00") {
			t.Fatalf("environment = %#v, retained %q", got, forbidden)
		}
	}
}

func TestHardenedGitCommandUsesWorktreeRootSafeDirectoryForNestedPath(t *testing.T) {
	repoRoot := t.TempDir()
	mustRunGit(t, repoRoot, "init")
	nestedPath := filepath.Join(repoRoot, "pkg", "nested")
	if err := os.MkdirAll(nestedPath, 0o755); err != nil {
		t.Fatal(err)
	}
	resolvedRoot, err := resolveExistingPath(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	resolvedNested, err := resolveExistingPath(nestedPath)
	if err != nil {
		t.Fatal(err)
	}

	cmd, err := newHardenedGitCommand(
		context.Background(),
		nestedPath,
		"rev-parse", "--show-toplevel",
	)
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Dir != resolvedNested {
		t.Fatalf("git cwd = %q, want nested input %q", cmd.Dir, resolvedNested)
	}
	var safeDirectories []string
	for i := 0; i+1 < len(cmd.Args); i++ {
		if cmd.Args[i] == "-c" && strings.HasPrefix(cmd.Args[i+1], "safe.directory=") {
			safeDirectories = append(safeDirectories, cmd.Args[i+1])
		}
	}
	wantSafeDirectory := "safe.directory=" + filepath.ToSlash(resolvedRoot)
	if len(safeDirectories) != 1 || safeDirectories[0] != wantSafeDirectory {
		t.Fatalf("safe.directory args = %#v, want %q", safeDirectories, wantSafeDirectory)
	}
}

func TestHardenedGitDiffUsesLiteralPathspecs(t *testing.T) {
	repoRoot := t.TempDir()
	mustRunGit(t, repoRoot, "init")
	mustWriteFile(t, filepath.Join(repoRoot, "[ab].go"), "package literal\n\nconst value = 1\n")
	mustWriteFile(t, filepath.Join(repoRoot, "a.go"), "package literal\n\nconst other = 1\n")
	mustRunGit(t, repoRoot, "add", "[ab].go", "a.go")
	mustCommitGit(t, repoRoot)
	mustWriteFile(t, filepath.Join(repoRoot, "[ab].go"), "package literal\n\nconst value = 2\n")
	mustWriteFile(t, filepath.Join(repoRoot, "a.go"), "package literal\n\nconst other = 2\n")

	stdout, stderr, err := runGitCommand(context.Background(), repoRoot, []string{
		"diff", "--no-ext-diff", "--no-textconv", "HEAD", "--", "[ab].go",
	})
	if err != nil {
		t.Fatalf("run literal git diff: %v\nstderr: %s", err, stderr)
	}
	diff := string(stdout)
	if !strings.Contains(diff, "a/[ab].go") || strings.Contains(diff, "diff --git a/a.go b/a.go") {
		t.Fatalf("literal pathspec diff = %s", diff)
	}
}

func TestLiteralWhitespacePathspecScopesDiffAndSnapshot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Win32 path normalization does not preserve boundary spaces")
	}
	repoRoot := t.TempDir()
	mustRunGit(t, repoRoot, "init")
	leading := " leading.go"
	trailing := "trailing.go "
	trimmedLeading := strings.TrimSpace(leading)
	trimmedTrailing := strings.TrimSpace(trailing)
	for _, file := range []string{leading, trailing, trimmedLeading, trimmedTrailing} {
		mustWriteFile(t, filepath.Join(repoRoot, file), "package literal\n\nconst value = 1\n")
	}
	mustRunGit(t, repoRoot, "add", "--", leading, trailing, trimmedLeading, trimmedTrailing)
	mustCommitGit(t, repoRoot)
	for _, file := range []string{leading, trailing, trimmedLeading, trimmedTrailing} {
		mustWriteFile(t, filepath.Join(repoRoot, file), "package literal\n\nconst value = 2\n")
	}

	filters, err := normalizeFileFilters([]string{leading, trailing})
	if err != nil {
		t.Fatal(err)
	}
	args := append(
		[]string{"diff", "--no-ext-diff", "--no-textconv", "HEAD", "--"},
		[]string(filters)...,
	)
	stdout, stderr, err := runGitCommand(context.Background(), repoRoot, args)
	if err != nil {
		t.Fatalf("run literal whitespace git diff: %v\nstderr: %s", err, stderr)
	}
	parsed := parseUnifiedDiff(stdout)
	if len(parsed.Warnings) != 0 || len(parsed.Files) != 2 {
		t.Fatalf("parsed whitespace diff = %+v", parsed)
	}
	changed := make(map[string]bool, len(parsed.Files))
	for _, file := range parsed.Files {
		changed[file.reviewPath()] = true
	}
	if !changed[leading] || !changed[trailing] ||
		changed[trimmedLeading] || changed[trimmedTrailing] {
		t.Fatalf("changed paths = %#v, want only %q and %q", changed, leading, trailing)
	}

	snapshot, err := prepareSandboxRepoSnapshot(
		context.Background(),
		repoRoot,
		[]string(filters),
		defaultSandboxSnapshotLimits(),
	)
	if err != nil {
		t.Fatalf("prepare whitespace path snapshot: %v", err)
	}
	defer os.RemoveAll(snapshot.Root)
	for _, file := range []string{leading, trailing} {
		if _, err := os.Stat(filepath.Join(snapshot.Root, file)); err != nil {
			t.Fatalf("selected snapshot path %q: %v", file, err)
		}
	}
	for _, file := range []string{trimmedLeading, trimmedTrailing} {
		if _, err := os.Stat(filepath.Join(snapshot.Root, file)); !os.IsNotExist(err) {
			t.Fatalf("trimmed snapshot path %q stat error = %v, want not exist", file, err)
		}
	}
}

func TestHardenedGitDiffKeepsOutputLimit(t *testing.T) {
	repoRoot := t.TempDir()
	mustRunGit(t, repoRoot, "init")
	mustWriteFile(t, filepath.Join(repoRoot, "large.txt"), "before\n")
	mustRunGit(t, repoRoot, "add", "large.txt")
	mustCommitGit(t, repoRoot)
	mustWriteFile(t, filepath.Join(repoRoot, "large.txt"), strings.Repeat("x", int(maxDiffBytes)+1024)+"\n")

	_, _, err := runGitCommand(context.Background(), repoRoot, []string{
		"diff", "--no-ext-diff", "--no-textconv", "HEAD", "--",
	})
	if err == nil || !strings.Contains(err.Error(), "output exceeds") {
		t.Fatalf("runGitCommand error = %v, want output limit", err)
	}
}

func mustCommitGit(t *testing.T, repoRoot string) {
	t.Helper()
	mustRunGit(
		t,
		repoRoot,
		"-c", "user.name=Code Review Test",
		"-c", "user.email=code-review@example.invalid",
		"commit", "-m", "baseline",
	)
}

func writeGitMarkerProgram(t *testing.T, dir string, name string, marker string) string {
	t.Helper()
	program := filepath.Join(dir, name+".sh")
	quotedMarker := "'" + strings.ReplaceAll(filepath.ToSlash(marker), "'", "'\"'\"'") + "'"
	mustWriteFile(t, program, "#!/bin/sh\nprintf executed > "+quotedMarker+"\nexit 0\n")
	if err := os.Chmod(program, 0o700); err != nil {
		t.Fatalf("chmod marker program: %v", err)
	}
	return program
}
