//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main provides a PR-time module zip sum checker.
package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/sumdb/dirhash"
	modzip "golang.org/x/mod/zip"
)

type moduleEntry struct {
	GoModPath  string
	Dir        string
	ModulePath string
}

type mismatchEntry struct {
	GoModPath  string
	ModulePath string
	VCS        string
	Working    string
}

type unsupportedEntry struct {
	GoModPath  string
	ModulePath string
	Reason     string
}

func main() {
	version := flag.String("version", "v0.0.0", "Synthetic version used for module zip prefix.")
	revision := flag.String("revision", "HEAD", "Git revision used for VCS archive mode.")
	flag.Parse()

	repoRoot, err := gitRepoRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "::error::Unable to determine git repository root: %v\n", err)
		os.Exit(1)
	}

	entries, initialUnsupported, err := discoverModules(repoRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "::error::Unable to discover go modules: %v\n", err)
		os.Exit(1)
	}
	if len(entries) == 0 {
		fmt.Fprintf(os.Stderr, "::error::No go.mod files found.\n")
		os.Exit(1)
	}

	tmpDir, err := os.MkdirTemp("", "trpc-agent-go-sumcheck-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "::error::Unable to create temp dir: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	hasErrors := len(initialUnsupported) > 0
	skipped := []string{}
	mismatches := []mismatchEntry{}
	unsupported := append([]unsupportedEntry(nil), initialUnsupported...)

	for _, entry := range entries {
		if isDoNotUseGoMod(entry.GoModPath) {
			skipped = append(skipped, rel(repoRoot, entry.Dir))
			continue
		}

		subdir := rel(repoRoot, entry.Dir)
		if subdir == "." {
			subdir = ""
		}
		subdir = filepath.ToSlash(strings.TrimPrefix(subdir, "./"))

		fmt.Printf("::group::%s\n", fmt.Sprintf("%s@%s (%s)", entry.ModulePath, *version, humanModuleDir(subdir)))

		m := module.Version{Path: entry.ModulePath, Version: *version}
		if err := module.Check(m.Path, m.Version); err != nil {
			fmt.Fprintf(os.Stderr, "::error file=%s::Invalid synthetic module version for %s: %v\n", rel(repoRoot, entry.GoModPath), entry.ModulePath, err)
			unsupported = append(unsupported, unsupportedEntry{
				GoModPath:  rel(repoRoot, entry.GoModPath),
				ModulePath: entry.ModulePath,
				Reason:     fmt.Sprintf("invalid synthetic module version: %v", err),
			})
			hasErrors = true
			fmt.Println("::endgroup::")
			continue
		}

		vcsZipPath, err := createModuleZipFromVCS(tmpDir, m, repoRoot, *revision, subdir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "::error file=%s::Failed to create VCS module zip for %s: %v\n", rel(repoRoot, entry.GoModPath), entry.ModulePath, err)
			unsupported = append(unsupported, unsupportedEntry{
				GoModPath:  rel(repoRoot, entry.GoModPath),
				ModulePath: entry.ModulePath,
				Reason:     fmt.Sprintf("failed to create VCS zip: %v", err),
			})
			hasErrors = true
			fmt.Println("::endgroup::")
			continue
		}
		vcsSum, files, err := hashZipAndListFiles(vcsZipPath)
		_ = os.Remove(vcsZipPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "::error file=%s::Failed to compute VCS zip sum for %s: %v\n", rel(repoRoot, entry.GoModPath), entry.ModulePath, err)
			unsupported = append(unsupported, unsupportedEntry{
				GoModPath:  rel(repoRoot, entry.GoModPath),
				ModulePath: entry.ModulePath,
				Reason:     fmt.Sprintf("failed to compute VCS zip sum: %v", err),
			})
			hasErrors = true
			fmt.Println("::endgroup::")
			continue
		}

		zipPrefix := fmt.Sprintf("%s@%s/", m.Path, m.Version)
		wtSum, err := hashFilesFromWorkingTree(files, zipPrefix, repoRoot, entry.Dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "::error file=%s::Failed to compute working tree sum for %s: %v\n", rel(repoRoot, entry.GoModPath), entry.ModulePath, err)
			unsupported = append(unsupported, unsupportedEntry{
				GoModPath:  rel(repoRoot, entry.GoModPath),
				ModulePath: entry.ModulePath,
				Reason:     fmt.Sprintf("failed to compute working tree sum: %v", err),
			})
			hasErrors = true
			fmt.Println("::endgroup::")
			continue
		}

		fmt.Printf("vcs zip: %s\n", vcsSum)
		fmt.Printf("wt  zip: %s\n", wtSum)

		if vcsSum != wtSum {
			fmt.Fprintf(os.Stderr, "::error file=%s::Module zip sum mismatch for %s (VCS != working tree).\n", rel(repoRoot, entry.GoModPath), entry.ModulePath)
			mismatches = append(mismatches, mismatchEntry{
				GoModPath:  rel(repoRoot, entry.GoModPath),
				ModulePath: entry.ModulePath,
				VCS:        vcsSum,
				Working:    wtSum,
			})
			hasErrors = true
		}

		fmt.Println("::endgroup::")
	}

	if len(skipped) > 0 {
		sort.Strings(skipped)
		fmt.Println("::group::Skipped modules")
		for _, s := range skipped {
			fmt.Printf("- %s\n", s)
		}
		fmt.Println("::endgroup::")
	}

	if len(mismatches) > 0 {
		sort.Slice(mismatches, func(i, j int) bool {
			return mismatches[i].ModulePath < mismatches[j].ModulePath
		})
		fmt.Println("::group::Mismatched modules")
		for _, m := range mismatches {
			fmt.Printf("- %s (%s)\n", m.ModulePath, m.GoModPath)
			fmt.Printf("  vcs: %s\n", m.VCS)
			fmt.Printf("  wt : %s\n", m.Working)
		}
		fmt.Println("::endgroup::")
	}

	if len(unsupported) > 0 {
		sort.Slice(unsupported, func(i, j int) bool {
			return unsupported[i].ModulePath < unsupported[j].ModulePath
		})
		fmt.Println("::group::Unsupported modules")
		for _, u := range unsupported {
			fmt.Printf("- %s (%s): %s\n", u.ModulePath, u.GoModPath, u.Reason)
		}
		fmt.Println("::endgroup::")
	}

	if hasErrors {
		fmt.Fprintf(os.Stderr, "::error::Some modules have inconsistent sums between VCS and working tree.\n")
		os.Exit(1)
	}

	fmt.Println("All modules match between VCS and working tree.")
}

func discoverModules(repoRoot string) ([]moduleEntry, []unsupportedEntry, error) {
	out, err := git(repoRoot, "ls-files", "--", "go.mod", "**/go.mod")
	if err != nil {
		return nil, nil, err
	}
	lines := splitLines(out)
	entries := make([]moduleEntry, 0, len(lines))
	unsupported := []unsupportedEntry{}
	for _, line := range lines {
		goModPath := filepath.Join(repoRoot, filepath.FromSlash(line))
		dir := filepath.Dir(goModPath)
		modPath, err := readModulePath(goModPath)
		if err != nil {
			reason := fmt.Sprintf("unable to read module path: %v", err)
			if errors.Is(err, os.ErrNotExist) {
				reason = "go.mod is missing from the working tree"
			}
			unsupported = append(unsupported, unsupportedEntry{
				GoModPath:  rel(repoRoot, goModPath),
				ModulePath: "(unknown)",
				Reason:     reason,
			})
			continue
		}
		entries = append(entries, moduleEntry{
			GoModPath:  goModPath,
			Dir:        dir,
			ModulePath: modPath,
		})
	}
	return entries, unsupported, nil
}

func readModulePath(goModPath string) (string, error) {
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return "", err
	}
	f, err := modfile.Parse(goModPath, data, nil)
	if err != nil {
		return "", err
	}
	if f.Module == nil || f.Module.Mod.Path == "" {
		return "", fmt.Errorf("missing module directive")
	}
	return f.Module.Mod.Path, nil
}

func isDoNotUseGoMod(goModPath string) bool {
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return false
	}
	head := data
	if len(head) > 2048 {
		head = head[:2048]
	}
	return bytes.Contains(head, []byte("DO NOT USE!"))
}

func createModuleZipFromVCS(tmpDir string, m module.Version, repoRoot, revision, subdir string) (string, error) {
	f, err := os.CreateTemp(tmpDir, "vcs-*.zip")
	if err != nil {
		return "", err
	}
	defer func() {
		_ = f.Close()
	}()

	if err := modzip.CreateFromVCS(f, m, repoRoot, revision, subdir); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}

	return f.Name(), nil
}

func hashZipAndListFiles(zipPath string) (string, []string, error) {
	sum, err := dirhash.HashZip(zipPath, dirhash.Hash1)
	if err != nil {
		return "", nil, err
	}

	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", nil, err
	}
	defer reader.Close()

	files := make([]string, 0, len(reader.File))
	for _, file := range reader.File {
		files = append(files, file.Name)
	}
	sort.Strings(files)
	return sum, files, nil
}

func hashFilesFromWorkingTree(files []string, zipPrefix, repoRoot, moduleDir string) (string, error) {
	open := func(name string) (io.ReadCloser, error) {
		if !strings.HasPrefix(name, zipPrefix) {
			return nil, fmt.Errorf("unexpected zip entry name: %q", name)
		}
		inner := strings.TrimPrefix(name, zipPrefix)
		inner = filepath.FromSlash(inner)

		candidate := filepath.Join(moduleDir, inner)
		if inner == "LICENSE" {
			if _, err := os.Stat(candidate); err != nil {
				candidate = filepath.Join(repoRoot, "LICENSE")
			}
		}
		return os.Open(candidate)
	}
	return dirhash.Hash1(files, open)
}

func humanModuleDir(subdir string) string {
	if subdir == "" {
		return "root"
	}
	return subdir
}

func gitRepoRoot() (string, error) {
	out, err := git("", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func git(repoRoot string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if repoRoot != "" {
		cmd.Dir = repoRoot
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w: %s", strings.Join(cmd.Args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	raw := strings.Split(s, "\n")
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

func rel(base, target string) string {
	p, err := filepath.Rel(base, target)
	if err != nil {
		return target
	}
	if p == "" {
		return "."
	}
	return filepath.ToSlash(p)
}
