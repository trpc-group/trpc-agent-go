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
	"bytes"
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
			if current != nil {
				current.OldPath = normalizePath(strings.TrimPrefix(line, "--- "))
			}
		case strings.HasPrefix(line, "+++ "):
			if current != nil {
				current.NewPath = normalizePath(strings.TrimPrefix(line, "+++ "))
			}
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
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read diff file: %w", err)
	}
	return ParseUnifiedDiff(string(data))
}

// LoadFromRepo loads git workspace changes from a repository path.
// 从git仓库加载变更
func LoadFromRepo(repoPath string) (*Diff, error) {
	repoPath = filepath.Clean(repoPath)                                 // 清理路径
	if _, err := os.Stat(filepath.Join(repoPath, ".git")); err != nil { // 检查是否是git仓库
		return nil, fmt.Errorf("repo path is not a git repository: %w", err)
	}
	var buf bytes.Buffer
	for _, args := range [][]string{
		{"diff"},
		{"diff", "--cached"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoPath
		out, err := cmd.Output() // 执行命令
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) > 0 {
				return nil, fmt.Errorf("git %v failed: %s", args, string(exitErr.Stderr))
			}
			return nil, fmt.Errorf("git %v failed: %w", args, err)
		}
		if len(out) > 0 {
			buf.Write(out)
			if !bytes.HasSuffix(out, []byte("\n")) {
				buf.WriteByte('\n')
			}
		}
	}
	if buf.Len() == 0 {
		return &Diff{}, nil
	}
	return ParseUnifiedDiff(buf.String())
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
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		files = append(files, path)
	}
	return files
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
	clean, err := sanitizeRepoRelativePath(file)
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

func sanitizeRepoRelativePath(file string) (string, error) {
	file = strings.TrimPrefix(file, "a/")
	file = strings.TrimPrefix(file, "b/")
	file = filepath.ToSlash(filepath.Clean(file))
	if file == ".." || strings.HasPrefix(file, "../") || strings.Contains(file, "/../") {
		return "", fmt.Errorf("path escapes repository: %s", file)
	}
	return file, nil
}

func lookupGoPackage(repoPath, file string) (string, error) {
	clean, err := sanitizeRepoRelativePath(file)
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

// 规范化修改文件的路径
func normalizePath(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "/dev/null" {
		// 无修改   删除/增加文件 占位符/ 不处理
		return raw
	}
	if strings.HasPrefix(raw, "a/") || strings.HasPrefix(raw, "b/") {
		// 去除a/或b/
		return raw[2:]
	}
	if strings.HasPrefix(raw, "+++ ") || strings.HasPrefix(raw, "--- ") {
		return strings.TrimSpace(raw[4:])
	}
	return raw
}
