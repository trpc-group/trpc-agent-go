//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/diff"
)

// 常见密钥文件名模式，避免把本地凭据带进 sandbox。
var secretNamePatterns = []string{
	".env",
	".env.*",
	"*.pem",
	"*.key",
	"*.p12",
	"*.pfx",
	"id_rsa",
	"id_rsa.*",
	"id_dsa",
	"id_ecdsa",
	"id_ed25519",
	"*credentials*",
	"*secret*",
}

// collectRepoStagePaths 收集可安全放入 sandbox 的仓库文件。
// git 仓库优先用 git ls-files 的 tracked 文件，再合并 diff 中明确变更的路径。
func collectRepoStagePaths(repoPath, diffRaw string) ([]string, error) {
	repoPath, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("resolve repo path: %w", err)
	}
	paths := make(map[string]struct{})

	if isGitRepo(repoPath) {
		tracked, err := gitTrackedFiles(repoPath)
		if err != nil {
			return nil, err
		}
		for _, p := range tracked {
			if isSafeRepoRelativePath(p) {
				paths[p] = struct{}{}
			}
		}
	} else {
		walked, err := walkRepoFiles(repoPath)
		if err != nil {
			return nil, err
		}
		for _, p := range walked {
			if isSafeRepoRelativePath(p) {
				paths[p] = struct{}{}
			}
		}
	}

	if strings.TrimSpace(diffRaw) != "" {
		if parsed, err := diff.ParseUnifiedDiff(diffRaw); err == nil {
			for _, changed := range parsed.ChangedFiles() {
				if isSafeRepoRelativePath(changed) {
					paths[changed] = struct{}{}
				}
			}
		}
	}

	out := make([]string, 0, len(paths))
	for rel := range paths {
		if !isSafeRepoRelativePath(rel) || isExcludedRepoPath(rel) {
			continue
		}
		full, ok := resolveRepoFilePath(repoPath, rel)
		if !ok {
			continue
		}
		info, err := os.Lstat(full)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		out = append(out, rel)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil, fmt.Errorf("no safe files to stage from repo")
	}
	return out, nil
}

func stageCleanRepo(
	ctx context.Context,
	exec workspaceExecutor,
	ws codeexecutor.Workspace,
	repoPath, diffRaw string,
) error {
	repoPath, err := filepath.Abs(repoPath)
	if err != nil {
		return fmt.Errorf("resolve repo path: %w", err)
	}
	paths, err := collectRepoStagePaths(repoPath, diffRaw)
	if err != nil {
		return err
	}

	files := make([]codeexecutor.PutFile, 0, len(paths))
	for _, rel := range paths {
		full, ok := resolveRepoFilePath(repoPath, rel)
		if !ok {
			continue
		}
		info, err := os.Lstat(full)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		files = append(files, codeexecutor.PutFile{
			Path:    filepath.ToSlash(filepath.Join("work", "repo", rel)),
			Content: data,
			Mode:    0o644,
		})
	}
	if len(files) == 0 {
		return fmt.Errorf("no readable files to stage from repo")
	}
	return exec.PutFiles(ctx, ws, files)
}

func isGitRepo(repoPath string) bool {
	_, err := os.Stat(filepath.Join(repoPath, ".git"))
	return err == nil
}

func gitTrackedFiles(repoPath string) ([]string, error) {
	cmd := exec.Command("git", "-C", repoPath, "ls-files", "-z")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	if len(out) == 0 {
		return nil, nil
	}
	parts := strings.Split(string(out), "\x00")
	var files []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		files = append(files, filepath.ToSlash(p))
	}
	return files, nil
}

func walkRepoFiles(repoPath string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(repoPath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(repoPath, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	return files, err
}

// isSafeRepoRelativePath 用 filepath.IsLocal 拒绝 ../ 等路径逃逸。
func isSafeRepoRelativePath(rel string) bool {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	if rel == "" || rel == "." {
		return false
	}
	if filepath.IsAbs(filepath.FromSlash(rel)) {
		return false
	}
	return filepath.IsLocal(rel)
}

// resolveRepoFilePath 把相对路径解析到 repo 内绝对路径，并确认未逃出仓库根目录。
func resolveRepoFilePath(repoPath, rel string) (string, bool) {
	if !isSafeRepoRelativePath(rel) {
		return "", false
	}
	repoClean := filepath.Clean(repoPath)
	full := filepath.Clean(filepath.Join(repoClean, filepath.FromSlash(rel)))
	prefix := repoClean + string(os.PathSeparator)
	if full != repoClean && !strings.HasPrefix(full, prefix) {
		return "", false
	}
	return full, true
}

func isExcludedRepoPath(rel string) bool {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	if rel == "" || rel == ".git" || strings.HasPrefix(rel, ".git/") {
		return true
	}
	base := filepath.Base(rel)
	for _, pat := range secretNamePatterns {
		if matched, _ := filepath.Match(pat, base); matched {
			return true
		}
		if matched, _ := filepath.Match(pat, rel); matched {
			return true
		}
	}
	return false
}
