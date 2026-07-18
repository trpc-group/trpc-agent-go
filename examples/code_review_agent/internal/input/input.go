//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package input 把支持的审查输入收敛成 unified diff，并提取最小 Go 工程元数据。
package input

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

// Config 保存输入加载配置。
type Config struct {
	// FixturesRoot 是受控 fixture 目录。
	FixturesRoot string
}

// Request 描述一次审查输入来源。
type Request struct {
	// DiffFile 是外部 unified diff 文件。
	DiffFile string
	// FileList 是按行分隔的变更文件列表。
	FileList string
	// RepoPath 是本地 Git 工作区或普通目录。
	RepoPath string
	// Fixture 是 Config.FixturesRoot 下的 fixture 名称。
	Fixture string
	// BaseRef 是 repo diff 的基础 git ref。
	BaseRef string
	// HeadRef 是 repo diff 的目标 git ref。
	HeadRef string
}

// Read 读取或生成 unified diff 输入。
func Read(cfg Config, req Request) ([]byte, string, error) {
	if req.DiffFile != "" {
		b, err := os.ReadFile(req.DiffFile)
		return b, req.DiffFile, err
	}
	if req.FileList != "" {
		b, err := diffFromFileList(req.FileList, req.RepoPath)
		return b, req.FileList, err
	}
	if req.Fixture != "" {
		return readFixtureInput(cfg.FixturesRoot, req.Fixture)
	}
	if req.RepoPath != "" {
		b, err := diffFromRepo(req.RepoPath, req.BaseRef, req.HeadRef)
		return b, req.RepoPath, err
	}
	return nil, "", errors.New("diff file, file list, repo path, or fixture is required")
}

func readFixtureInput(root string, name string) ([]byte, string, error) {
	if strings.TrimSpace(root) == "" {
		return nil, "", errors.New("fixtures root is required")
	}
	cleanName := filepath.Clean(strings.TrimSpace(name))
	if cleanName == "." || filepath.IsAbs(cleanName) || strings.HasPrefix(cleanName, "..") {
		return nil, "", fmt.Errorf("invalid fixture name %q", name)
	}
	path := filepath.Join(root, cleanName)
	b, err := os.ReadFile(path)
	return b, path, err
}

func diffFromRepo(repoPath string, baseRef string, headRef string) ([]byte, error) {
	if repoPath == "" {
		return nil, errors.New("repo path is required")
	}
	if isGitWorktree(repoPath) {
		if hasExplicitRefs(baseRef, headRef) {
			return runGitDiff(gitDiffArgs(repoPath, baseRef, headRef))
		}
		return diffFromGitWorktree(repoPath)
	}
	return diffFromDirectory(repoPath)
}

func gitDiffArgs(repoPath string, baseRef string, headRef string) []string {
	args := []string{"-C", repoPath, "diff", "--no-ext-diff", "--no-textconv", "--unified=3"}
	if strings.TrimSpace(baseRef) != "" && strings.TrimSpace(headRef) != "" {
		args = append(args, strings.TrimSpace(baseRef)+"..."+strings.TrimSpace(headRef))
	}
	return args
}

func hasExplicitRefs(baseRef string, headRef string) bool {
	return strings.TrimSpace(baseRef) != "" && strings.TrimSpace(headRef) != ""
}

func diffFromGitWorktree(repoPath string) ([]byte, error) {
	tracked, err := diffTrackedGitChanges(repoPath)
	if err != nil {
		return nil, err
	}
	untracked, err := diffUntrackedGitFiles(repoPath)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.Write(tracked)
	if len(tracked) > 0 && len(untracked) > 0 && !strings.HasSuffix(b.String(), "\n") {
		b.WriteByte('\n')
	}
	b.Write(untracked)
	return []byte(b.String()), nil
}

func diffTrackedGitChanges(repoPath string) ([]byte, error) {
	if gitHeadExists(repoPath) {
		return runGitDiff([]string{"-C", repoPath, "diff", "--no-ext-diff", "--no-textconv", "--unified=3", "HEAD"})
	}
	staged, err := runGitDiff([]string{"-C", repoPath, "diff", "--cached", "--no-ext-diff", "--no-textconv", "--unified=3"})
	if err != nil {
		return nil, err
	}
	unstaged, err := runGitDiff([]string{"-C", repoPath, "diff", "--no-ext-diff", "--no-textconv", "--unified=3"})
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.Write(staged)
	if len(staged) > 0 && len(unstaged) > 0 && !strings.HasSuffix(b.String(), "\n") {
		b.WriteByte('\n')
	}
	b.Write(unstaged)
	return []byte(b.String()), nil
}

func diffUntrackedGitFiles(repoPath string) ([]byte, error) {
	repoRoot, err := gitRepoRoot(repoPath)
	if err != nil {
		return nil, err
	}
	out, err := exec.Command("git", "-C", repoPath, "ls-files", "--others", "--exclude-standard", "-z").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w: %s", err, string(out))
	}
	var b strings.Builder
	for _, name := range strings.Split(string(out), "\x00") {
		if name == "" {
			continue
		}
		path := filepath.Join(repoRoot, filepath.FromSlash(name))
		info, err := os.Lstat(path)
		if err != nil {
			return nil, fmt.Errorf("stat untracked file %q: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("untracked file %q is not a regular file", name)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read untracked file %q: %w", name, err)
		}
		diffForNewFile(&b, name, content)
	}
	return []byte(b.String()), nil
}

func gitHeadExists(repoPath string) bool {
	return exec.Command("git", "-C", repoPath, "rev-parse", "--verify", "HEAD").Run() == nil
}

func gitRepoRoot(repoPath string) (string, error) {
	out, err := exec.Command("git", "-C", repoPath, "rev-parse", "--show-toplevel").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git rev-parse --show-toplevel: %w: %s", err, string(out))
	}
	return strings.TrimSpace(string(out)), nil
}

func runGitDiff(args []string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git diff: %w: %s", err, string(out))
	}
	return out, nil
}

func isGitWorktree(repoPath string) bool {
	out, err := exec.Command("git", "-C", repoPath, "rev-parse", "--is-inside-work-tree").Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

func diffFromDirectory(repoPath string) ([]byte, error) {
	var b strings.Builder
	entries, err := os.ReadDir(repoPath)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(repoPath, entry.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		diffForNewFile(&b, entry.Name(), content)
	}
	return []byte(b.String()), nil
}

func diffFromFileList(listPath string, repoPath string) ([]byte, error) {
	content, err := os.ReadFile(listPath)
	if err != nil {
		return nil, err
	}
	baseDir := filepath.Dir(listPath)
	restrictToBase := false
	if strings.TrimSpace(repoPath) != "" {
		baseDir = repoPath
		restrictToBase = true
	}
	var b strings.Builder
	for _, raw := range strings.Split(string(content), "\n") {
		name := strings.TrimSpace(raw)
		if name == "" || strings.HasPrefix(name, "#") {
			continue
		}
		path, display, err := resolveListedFile(name, baseDir, restrictToBase)
		if err != nil {
			return nil, err
		}
		fileContent, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read listed file %q: %w", name, err)
		}
		diffForNewFile(&b, display, fileContent)
	}
	return []byte(b.String()), nil
}

func resolveListedFile(name string, baseDir string, restrictToBase bool) (string, string, error) {
	path := filepath.Clean(name)
	if !filepath.IsAbs(path) {
		path = filepath.Join(baseDir, path)
	}
	display := filepath.Base(path)
	if rel, err := filepath.Rel(baseDir, path); err == nil {
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			if restrictToBase || !filepath.IsAbs(name) {
				return "", "", fmt.Errorf("listed file %q escapes base directory", name)
			}
		} else {
			display = rel
		}
	}
	if !restrictToBase {
		return path, display, nil
	}
	resolvedBase, err := filepath.EvalSymlinks(baseDir)
	if err != nil {
		return "", "", fmt.Errorf("resolve base directory %q: %w", baseDir, err)
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", "", fmt.Errorf("resolve listed file %q: %w", name, err)
	}
	if rel, err := filepath.Rel(resolvedBase, resolvedPath); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("listed file %q escapes base directory", name)
	}
	return resolvedPath, display, nil
}

func diffForNewFile(b *strings.Builder, name string, content []byte) {
	display := filepath.ToSlash(strings.TrimPrefix(filepath.Clean(name), string(filepath.Separator)))
	lines := contentLines(content)
	fmt.Fprintf(b, "diff --git a/%s b/%s\n", display, display)
	fmt.Fprintf(b, "--- /dev/null\n+++ b/%s\n", display)
	fmt.Fprintf(b, "@@ -0,0 +1,%d @@\n", len(lines))
	for _, line := range lines {
		fmt.Fprintf(b, "+%s\n", line)
	}
}

func contentLines(content []byte) []string {
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	if strings.HasSuffix(text, "\n") {
		return lines[:len(lines)-1]
	}
	return lines
}

// Metadata 从 diff 输入中提取最小 Go 工程元数据。
func Metadata(diff []byte, repoPath string) review.InputMetadata {
	parsed, err := review.ParseUnifiedDiff(string(diff))
	if err != nil {
		return review.InputMetadata{ModulePath: modulePath(repoPath)}
	}
	goFiles := map[string]struct{}{}
	packages := map[string]struct{}{}
	testFiles := map[string]struct{}{}
	for _, file := range parsed.Files {
		if !strings.HasSuffix(file.Path, ".go") {
			continue
		}
		goFiles[file.Path] = struct{}{}
		if strings.HasSuffix(file.Path, "_test.go") {
			testFiles[file.Path] = struct{}{}
		}
		for _, hunk := range file.Hunks {
			for _, line := range hunk.Lines {
				if pkg := packageNameFromLine(line.Text); pkg != "" {
					packages[pkg] = struct{}{}
				}
			}
		}
	}
	return review.InputMetadata{
		ChangedGoFiles:   sortedKeys(goFiles),
		PackageNames:     sortedKeys(packages),
		ModulePath:       modulePath(repoPath),
		HasTests:         len(testFiles) > 0,
		TouchedTestFiles: sortedKeys(testFiles),
	}
}

// MetadataForRequest 提取元数据，并补充请求中的 git refs。
func MetadataForRequest(diff []byte, req Request) review.InputMetadata {
	meta := Metadata(diff, req.RepoPath)
	meta.BaseRef = strings.TrimSpace(req.BaseRef)
	meta.HeadRef = strings.TrimSpace(req.HeadRef)
	return meta
}

func packageNameFromLine(line string) string {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) >= 2 && fields[0] == "package" {
		return fields[1]
	}
	return ""
}

func modulePath(repoPath string) string {
	if strings.TrimSpace(repoPath) == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(repoPath, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 2 && fields[0] == "module" {
			return fields[1]
		}
	}
	return ""
}

func sortedKeys(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, filepath.ToSlash(value))
	}
	sort.Strings(out)
	return out
}
