// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
// Package diff 提供 unified diff 格式的解析功能。
// 它将 git diff 输出解析为结构化的变更信息，供审查规则使用。
package diff

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// FileDiff 表示一个文件的变更。
type FileDiff struct {
	OldPath string // 原文件路径（--- a/xxx）
	NewPath string // 新文件路径（+++ b/xxx）
	Hunks   []Hunk // 变更块列表
}

// Hunk 表示一个变更块（@@ ... @@ 包围的部分）。
type Hunk struct {
	OldStart int    // 原文件起始行号
	OldLines int    // 原文件行数
	NewStart int    // 新文件起始行号
	NewLines int    // 新文件行数
	Context  string // @@ 后面的函数名/上下文（可选）
	Lines    []Line // 所有行（包括上下文、新增、删除）
}

// Line 表示 diff 中的一行。
type Line struct {
	Type    LineType // 行类型：上下文、新增、删除
	Content string   // 行内容（不含前缀符号）
	OldLine int      // 原文件行号（删除行和上下文行有值，新增行为 0）
	NewLine int      // 新文件行号（新增行和上下文行有值，删除行为 0）
}

// LineType 行类型枚举。
type LineType int

const (
	LineContext LineType = iota // 上下文行（空格开头）
	LineAdded                   // 新增行（+ 开头）
	LineDeleted                 // 删除行（- 开头）
)

// String 返回行类型的可读名称。
func (t LineType) String() string {
	switch t {
	case LineContext:
		return "context"
	case LineAdded:
		return "added"
	case LineDeleted:
		return "deleted"
	default:
		return "unknown"
	}
}

// ========== 解析入口 ==========

// Parse 从 io.Reader 读取 unified diff 并解析为 FileDiff 列表。
//
// 解析流程：
//  1. 逐行读取
//  2. 遇到 "--- a/..." 开始新文件
//  3. 遇到 "+++ b/..." 设置新文件路径
//  4. 遇到 "@@ -x,y +x,y @@" 开始新 hunk
//  5. 其他行按前缀分类为 context/added/deleted
func Parse(reader io.Reader) ([]FileDiff, error) {
	scanner := bufio.NewScanner(reader)
	var files []FileDiff
	var currentFile *FileDiff
	var currentHunk *Hunk

	// hunk 内的行号计数器
	oldLine := 0
	newLine := 0

	for scanner.Scan() {
		line := scanner.Text()

		// --- a/... 标记原文件
		if strings.HasPrefix(line, "--- ") {
			// 保存上一个文件
			if currentFile != nil && currentHunk != nil {
				currentFile.Hunks = append(currentFile.Hunks, *currentHunk)
			}
			if currentFile != nil {
				files = append(files, *currentFile)
			}

			currentFile = &FileDiff{
				OldPath: parseFilePath(line, "--- "),
			}
			currentHunk = nil
			continue
		}

		// +++ b/... 标记新文件
		if strings.HasPrefix(line, "+++ ") && currentFile != nil {
			currentFile.NewPath = parseFilePath(line, "+++ ")
			continue
		}

		// @@ -x,y +x,y @@ 标记 hunk 开始
		if strings.HasPrefix(line, "@@ ") {
			// 保存上一个 hunk
			if currentFile != nil && currentHunk != nil {
				currentFile.Hunks = append(currentFile.Hunks, *currentHunk)
			}

			hunk, err := parseHunkHeader(line)
			if err != nil {
				return nil, fmt.Errorf("解析 hunk 头失败: %w", err)
			}
			currentHunk = hunk
			oldLine = hunk.OldStart
			newLine = hunk.NewStart
			continue
		}

		// diff 行内容
		if currentHunk != nil {
			diffLine := parseDiffLine(line, &oldLine, &newLine)
			currentHunk.Lines = append(currentHunk.Lines, diffLine)
		}
	}

	// 保存最后一个文件和 hunk
	if currentFile != nil && currentHunk != nil {
		currentFile.Hunks = append(currentFile.Hunks, *currentHunk)
	}
	if currentFile != nil {
		files = append(files, *currentFile)
	}

	return files, scanner.Err()
}

// ========== 辅助函数 ==========

// parseFilePath 从 "--- a/pkg/handler.go" 中提取 "pkg/handler.go"。
func parseFilePath(line, prefix string) string {
	path := strings.TrimPrefix(line, prefix)
	// 去掉 a/ 或 b/ 前缀（git diff 标准格式）
	if len(path) >= 2 && path[1] == '/' && (path[0] == 'a' || path[0] == 'b') {
		path = path[2:]
	}
	return path
}

// parseHunkHeader 解析 "@@ -10,6 +10,25 @@ func main()" 这样的 hunk 头。
func parseHunkHeader(line string) (*Hunk, error) {
	// 格式：@@ -oldStart,oldLines +newStart,newLines @@ context
	// 去掉开头的 "@@ "
	trimmed := strings.TrimPrefix(line, "@@ ")
	// 找到结尾的 " @@"
	endIdx := strings.Index(trimmed, " @@")
	if endIdx == -1 {
		return nil, fmt.Errorf("无效的 hunk 头: %s", line)
	}

	ranges := trimmed[:endIdx]
	context := ""
	if endIdx+3 < len(trimmed) {
		context = strings.TrimSpace(trimmed[endIdx+3:])
	}

	// 解析 "-10,6 +10,25"
	parts := strings.Split(ranges, " ")
	if len(parts) != 2 {
		return nil, fmt.Errorf("无效的 hunk 范围: %s", ranges)
	}

	oldStart, oldLines, err := parseRange(parts[0]) // "-10,6"
	if err != nil {
		return nil, fmt.Errorf("解析原文件范围失败: %w", err)
	}

	newStart, newLines, err := parseRange(parts[1]) // "+10,25"
	if err != nil {
		return nil, fmt.Errorf("解析新文件范围失败: %w", err)
	}

	return &Hunk{
		OldStart: oldStart,
		OldLines: oldLines,
		NewStart: newStart,
		NewLines: newLines,
		Context:  context,
	}, nil
}

// parseRange 解析 "-10,6" 为 (10, 6)。
func parseRange(s string) (start, count int, err error) {
	// 去掉前缀 - 或 +
	s = s[1:]
	parts := strings.SplitN(s, ",", 2)

	start, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("解析行号失败: %w", err)
	}

	count = 1 // 默认 1 行
	if len(parts) > 1 {
		count, err = strconv.Atoi(parts[1])
		if err != nil {
			return 0, 0, fmt.Errorf("解析行数失败: %w", err)
		}
	}

	return start, count, nil
}

// parseDiffLine 解析 diff 中的一行，根据前缀判断类型并分配行号。
func parseDiffLine(line string, oldLine, newLine *int) Line {
	if len(line) == 0 {
		// 空行当作上下文行
		*oldLine++
		*newLine++
		return Line{Type: LineContext, Content: "", OldLine: *oldLine - 1, NewLine: *newLine - 1}
	}

	prefix := line[0]
	content := line[1:] // 去掉前缀字符

	switch prefix {
	case '+':
		*newLine++
		return Line{Type: LineAdded, Content: content, NewLine: *newLine - 1}
	case '-':
		*oldLine++
		return Line{Type: LineDeleted, Content: content, OldLine: *oldLine - 1}
	default:
		// 空格开头 = 上下文行
		*oldLine++
		*newLine++
		return Line{Type: LineContext, Content: content, OldLine: *oldLine - 1, NewLine: *newLine - 1}
	}
}

// ========== 辅助方法 ==========

// AddedLines 返回所有新增行。
func (fd *FileDiff) AddedLines() []Line {
	var lines []Line
	for _, hunk := range fd.Hunks {
		for _, line := range hunk.Lines {
			if line.Type == LineAdded {
				lines = append(lines, line)
			}
		}
	}
	return lines
}

// DeletedLines 返回所有删除行。
func (fd *FileDiff) DeletedLines() []Line {
	var lines []Line
	for _, hunk := range fd.Hunks {
		for _, line := range hunk.Lines {
			if line.Type == LineDeleted {
				lines = append(lines, line)
			}
		}
	}
	return lines
}

// IsGoFile 判断是否为 Go 文件。
func (fd *FileDiff) IsGoFile() bool {
	return strings.HasSuffix(fd.NewPath, ".go")
}

// GoPackageName 从 diff 中提取 Go 包名。
//
// 提取策略：
//  1. 先从 hunk 的 Context 字段中查找（git diff -U0 格式有时包含函数名）
//  2. 再从新增行中查找 "package xxx" 声明
//  3. 最后从上下文行中查找
//  4. 都找不到返回空字符串
func (fd *FileDiff) GoPackageName() string {
	// 策略 1: 从 hunk context 提取（有些 git 配置会包含 package 信息）
	for _, hunk := range fd.Hunks {
		if hunk.Context != "" && strings.HasPrefix(hunk.Context, "package ") {
			return strings.TrimPrefix(hunk.Context, "package ")
		}
	}

	// 策略 2: 从新增行中查找 "package xxx"
	for _, line := range fd.AddedLines() {
		if pkg := extractPackageName(line.Content); pkg != "" {
			return pkg
		}
	}

	// 策略 3: 从上下文行中查找
	for _, hunk := range fd.Hunks {
		for _, line := range hunk.Lines {
			if line.Type == LineContext {
				if pkg := extractPackageName(line.Content); pkg != "" {
					return pkg
				}
			}
		}
	}

	return ""
}

// extractPackageName 从一行代码中提取 Go 包名。
// 输入 "package main" 返回 "main"，其他返回 ""。
func extractPackageName(line string) string {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "package ") {
		pkg := strings.TrimPrefix(trimmed, "package ")
		pkg = strings.TrimSpace(pkg)
		if pkg != "" && isValidGoIdentifier(pkg) {
			return pkg
		}
	}
	return ""
}

// isValidGoIdentifier 检查是否是合法的 Go 标识符（简化版）。
func isValidGoIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if !isLetter(r) && r != '_' {
				return false
			}
		} else {
			if !isLetter(r) && !isDigit(r) && r != '_' {
				return false
			}
		}
	}
	return true
}

func isLetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

// ChangedGoFiles 从文件列表中筛选出 Go 文件。
func ChangedGoFiles(files []FileDiff) []FileDiff {
	var goFiles []FileDiff
	for _, f := range files {
		if f.IsGoFile() {
			goFiles = append(goFiles, f)
		}
	}
	return goFiles
}

// ========== 输入读取 ==========

// ReadFromFile 从 .diff 文件读取并解析。
//
// 用法：
//
//	files, err := diff.ReadFromFile("changes.diff")
func ReadFromFile(path string) ([]FileDiff, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开 diff 文件失败: %w", err)
	}
	defer f.Close()

	return Parse(f)
}

// ReadFromGitDiff 在指定仓库目录执行 git diff 并解析输出。
//
// 用法：
//
//	// 解析暂存区变更
//	files, err := diff.ReadFromGitDiff("/path/to/repo")
//
//	// 解析最近一次提交
//	files, err := diff.ReadFromGitDiff("/path/to/repo", "HEAD~1", "HEAD")
//
//	// 解析指定分支差异
//	files, err := diff.ReadFromGitDiff("/path/to/repo", "main", "feature")
func ReadFromGitDiff(repoPath string, args ...string) ([]FileDiff, error) {
	gitArgs := []string{"diff"}
	gitArgs = append(gitArgs, args...)

	cmd := exec.Command("git", gitArgs...)
	cmd.Dir = repoPath

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("执行 git diff 失败: %w", err)
	}

	if len(output) == 0 {
		return nil, nil // 没有变更
	}

	return Parse(strings.NewReader(string(output)))
}
