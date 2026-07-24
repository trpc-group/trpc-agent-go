//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package diff parses unified diffs and git workspace changes.
// 解析统一diffs和git工作区变更 成结构体返回
package diff

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// AddedLine is a single added line in a hunk.
type AddedLine struct {
	Line    int
	Content string
}

// Hunk is a contiguous change block in a file.
type Hunk struct {
	File       string
	StartLine  int
	AddedLines []AddedLine
	AllLines   []string
}

// FileChange groups hunks for one file.
type FileChange struct {
	OldPath string // --- a/old.go
	NewPath string // +++ b/new.go
	Hunks   []Hunk // @……@开头及以后部分
}

// Diff is the parsed result of a unified diff.
type Diff struct {
	Files []FileChange // 所有文件的变更
	Raw   string
}

// 匹配hunk头  获得开始删除和开始添加的行号
var hunkHeaderRE = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

const (
	maxDiffLines = 5000
	maxDiffBytes = maxDiffLines * 512
)

// 解析行为
// ParseUnifiedDiff parses unified diff content.
func ParseUnifiedDiff(content string) (*Diff, error) {
	// 内容空 返回空Diff
	if strings.TrimSpace(content) == "" {
		return &Diff{Raw: content}, nil
	}

	// scanner 一行一行切割 读取文本
	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var (
		files       []FileChange
		current     *FileChange
		currentHunk *Hunk
		newLineNo   int
	)

	// 刷新hunk  对一个文件而言
	flushHunk := func() {
		if current == nil || currentHunk == nil {
			return
		}
		if len(currentHunk.AllLines) > 0 || len(currentHunk.AddedLines) > 0 {
			current.Hunks = append(current.Hunks, *currentHunk)
		}
		currentHunk = nil
	}

	// 刷新文件 对所有文件
	flushFile := func() {
		flushHunk()
		if current != nil && len(current.Hunks) > 0 {
			files = append(files, *current)
		}
		current = nil
	}
	// 一行行读
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		// diff 文件第一行
		case strings.HasPrefix(line, "diff --git "):
			flushFile()
			current = &FileChange{}
		case strings.HasPrefix(line, "--- "):
			if current == nil {
				flushFile()
				current = &FileChange{}
			}
			current.OldPath = normalizePath(strings.TrimPrefix(line, "--- "))
		case strings.HasPrefix(line, "+++ "):
			if current == nil {
				current = &FileChange{}
			}
			current.NewPath = normalizePath(strings.TrimPrefix(line, "+++ "))
			// hunk头
		case strings.HasPrefix(line, "@@ "):
			flushHunk()
			m := hunkHeaderRE.FindStringSubmatch(line)
			if m == nil {
				return nil, fmt.Errorf("invalid hunk header: %s", line)
			}
			start, err := strconv.Atoi(m[2])
			if err != nil {
				return nil, fmt.Errorf("invalid hunk start line: %w", err)
			}
			filePath := ""
			if current != nil {
				filePath = current.NewPath
				if filePath == "" {
					filePath = current.OldPath
				}
			}
			currentHunk = &Hunk{File: filePath, StartLine: start}
			newLineNo = start
		default:
			if currentHunk == nil {
				continue
			}
			if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
				content := strings.TrimPrefix(line, "+")
				currentHunk.AddedLines = append(currentHunk.AddedLines, AddedLine{
					Line:    newLineNo,
					Content: content,
				})
				currentHunk.AllLines = append(currentHunk.AllLines, content)
				newLineNo++
				continue
			}
			if strings.HasPrefix(line, " ") {
				currentHunk.AllLines = append(currentHunk.AllLines, strings.TrimPrefix(line, " "))
				newLineNo++
				continue
			}
			if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
				continue
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	flushFile()

	return &Diff{Files: files, Raw: content}, nil
}

// LoadFromFile reads and parses a diff file. //解析入口
func LoadFromFile(path string) (*Diff, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("read diff file: %w", err)
	}
	if info.Size() > maxDiffBytes {
		return nil, fmt.Errorf("diff file too large: %d > %d", info.Size(), maxDiffBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read diff file: %w", err)
	}
	return ParseUnifiedDiff(string(data))
}

// LoadFromRepo loads the final working-tree diff against HEAD.
// A single `git diff HEAD` describes worktree+index versus HEAD coherently;
// concatenating unstaged and cached diffs does not match any one repository state.
func LoadFromRepo(repoPath string) (*Diff, error) {
	repoPath = filepath.Clean(repoPath)
	if !isGitRepo(repoPath) {
		return nil, fmt.Errorf("repo path is not a git repository")
	}
	cmd := exec.Command("git", "diff", "HEAD", "--")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) > 0 {
			return nil, fmt.Errorf("git diff HEAD failed: %s", string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("git diff HEAD failed: %w", err)
	}
	if len(out) > maxDiffBytes {
		return nil, fmt.Errorf("diff too large: %d > %d", len(out), maxDiffBytes)
	}
	if len(out) == 0 {
		return &Diff{}, nil
	}
	return ParseUnifiedDiff(string(out))
}

// AllHunks returns flattened hunks across all files.
// 获取所有hunk 放在一个slice中
func (d *Diff) AllHunks() []Hunk {
	if d == nil {
		return nil
	}
	var hunks []Hunk
	for _, f := range d.Files {
		hunks = append(hunks, f.Hunks...)
	}
	return hunks
}

// ChangedFiles returns unique changed file paths.
// 获取所有变更的去重后的文件路径
func (d *Diff) ChangedFiles() []string {
	if d == nil {
		return nil
	}
	seen := make(map[string]struct{})
	var files []string
	for _, f := range d.Files {
		path := f.NewPath
		if path == "" {
			path = f.OldPath
		}
		if path == "" || path == "/dev/null" {
			continue
		}
		if clean, err := SanitizeRepoRelativePath(path); err != nil {
			continue
		} else {
			path = clean
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		files = append(files, path)
	}
	return files
}

// HasAddedLine reports whether file:line is an added line in this diff.
func (d *Diff) HasAddedLine(file string, line int) bool {
	if d == nil || line <= 0 {
		return false
	}
	clean, err := SanitizeRepoRelativePath(file)
	if err != nil {
		return false
	}
	for _, f := range d.Files {
		path := f.NewPath
		if path == "" {
			path = f.OldPath
		}
		if path != clean {
			continue
		}
		for _, h := range f.Hunks {
			for _, al := range h.AddedLines {
				if al.Line == line {
					return true
				}
			}
		}
	}
	return false
}

// Summary returns a short summary of changed files.
// 统计一下
func (d *Diff) Summary() string {
	if d == nil || len(d.Files) == 0 {
		return "no changes"
	}
	files := d.ChangedFiles()
	if len(files) == 0 {
		return "no changes"
	}
	if len(files) <= 3 {
		return fmt.Sprintf("changed files: %s", strings.Join(files, ", "))
	}
	return fmt.Sprintf("changed files: %s and %d more", strings.Join(files[:3], ", "), len(files)-3)
}

// InferGoPackage infers the Go import path for a file.
func InferGoPackage(file string, repoPath string) string {
	clean, err := SanitizeRepoRelativePath(file)
	if err != nil {
		return ""
	}
	if !strings.HasSuffix(clean, ".go") {
		return ""
	}
	if repoPath != "" {
		if pkg, err := lookupGoPackage(repoPath, clean); err == nil && pkg != "" {
			return pkg
		}
	}
	dir := filepath.Dir(clean)
	if dir == "." {
		return "."
	}
	return filepath.ToSlash(dir)
}

// SanitizeRepoRelativePath rejects absolute paths and ../ escapes.
// It does NOT strip leading a/ or b/ — those may be real top-level directories.
// Git header markers are stripped only once in normalizePath while parsing ---/+++.
func SanitizeRepoRelativePath(file string) (string, error) {
	return sanitizeRepoRelativePath(file)
}

func sanitizeRepoRelativePath(file string) (string, error) {
	file = filepath.ToSlash(strings.TrimSpace(file))
	if file == "" || file == "." {
		return "", fmt.Errorf("empty path")
	}
	if filepath.IsAbs(filepath.FromSlash(file)) {
		return "", fmt.Errorf("absolute path not allowed: %s", file)
	}
	// Reject .. before Clean; on Windows IsLocal may not see '/' separators.
	if file == ".." || strings.HasPrefix(file, "../") || strings.Contains(file, "/../") || strings.HasSuffix(file, "/..") {
		return "", fmt.Errorf("path escapes repository: %s", file)
	}
	if !filepath.IsLocal(filepath.FromSlash(file)) {
		return "", fmt.Errorf("path escapes repository: %s", file)
	}
	file = filepath.ToSlash(filepath.Clean(file))
	if file == ".." || strings.HasPrefix(file, "../") || strings.Contains(file, "/../") {
		return "", fmt.Errorf("path escapes repository: %s", file)
	}
	return file, nil
}

func lookupGoPackage(repoPath, file string) (string, error) {
	clean, err := SanitizeRepoRelativePath(file)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(clean)
	if dir == ".." || strings.HasPrefix(dir, "../") || strings.Contains(dir, "/../") {
		return "", fmt.Errorf("path escapes repository: %s", dir)
	}
	cmd := exec.Command("go", "list", "-f", "{{.ImportPath}}", "./"+dir)
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// 规范化修改文件的路径；Git ---/+++ 头的 a/ 或 b/ 标记只剥一次。
func normalizePath(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "/dev/null" {
		return raw
	}
	if strings.HasPrefix(raw, "+++ ") || strings.HasPrefix(raw, "--- ") {
		raw = strings.TrimSpace(raw[4:])
	}
	if strings.HasPrefix(raw, "a/") || strings.HasPrefix(raw, "b/") {
		raw = raw[2:]
	}
	if clean, err := sanitizeRepoRelativePath(raw); err != nil {
		return ""
	} else {
		return clean
	}
}

func isGitRepo(repoPath string) bool {
	info, err := os.Lstat(filepath.Join(repoPath, ".git"))
	if err != nil {
		return false
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	return info.IsDir() || info.Mode().IsRegular()
}
