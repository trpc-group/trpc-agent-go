// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
// Package input 提供 diff 解析功能
package input

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// DiffParser Diff 解析器
type DiffParser struct {
	repoPath string
}

// NewDiffParser 创建 Diff 解析器
func NewDiffParser(repoPath string) *DiffParser {
	return &DiffParser{repoPath: repoPath}
}

// ParseGitDiff 使用 git diff 命令解析工作区变更
func (p *DiffParser) ParseGitDiff() (*DiffParseResult, error) {
	// 执行 git diff
	cmd := exec.Command("git", "diff", "HEAD")
	cmd.Dir = p.repoPath

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff failed: %w", err)
	}

	if len(output) == 0 {
		// 没有变更，尝试 git diff --cached
		cmd = exec.Command("git", "diff", "--cached")
		cmd.Dir = p.repoPath
		output, err = cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("git diff --cached failed: %w", err)
		}
	}

	if len(output) == 0 {
		return &DiffParseResult{
			Files: make([]DiffFile, 0),
		}, nil
	}

	reader := &bytesReader{data: output}
	return p.Parse(reader)
}

// ParseFileList 解析文件路径列表
func (p *DiffParser) ParseFileList(files []string) (*DiffParseResult, error) {
	result := &DiffParseResult{
		Files: make([]DiffFile, 0),
	}

	for _, file := range files {
		// 读取文件内容
		content, err := os.ReadFile(filepath.Join(p.repoPath, file))
		if err != nil {
			continue
		}

		// 创建一个简单的 diff（整个文件作为新增）
		diffFile := DiffFile{
			Path:      file,
			Status:    "added",
			Additions: len(strings.Split(string(content), "\n")),
			Deletions: 0,
			Hunks: []DiffHunk{
				{
					OldStart: 0,
					OldLines: 0,
					NewStart: 1,
					NewLines: len(strings.Split(string(content), "\n")),
					Changes:  p.contentToChanges(string(content)),
				},
			},
		}

		result.Files = append(result.Files, diffFile)
		result.TotalAdded += diffFile.Additions
	}

	result.TotalFiles = len(result.Files)

	return result, nil
}

// contentToChanges 将内容转换为 Change 列表
func (p *DiffParser) contentToChanges(content string) []Change {
	lines := strings.Split(content, "\n")
	changes := make([]Change, 0, len(lines))

	for i, line := range lines {
		changes = append(changes, Change{
			Type:    "add",
			NewLine: i + 1,
			Content: line,
		})
	}

	return changes
}

// bytesReader 字节读取器
type bytesReader struct {
	data []byte
	pos  int
}

func (r *bytesReader) Read(p []byte) (n int, err error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n = copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

// DiffParseResult 解析结果
type DiffParseResult struct {
	Files        []DiffFile `json:"files"`
	TotalFiles   int        `json:"total_files"`
	TotalAdded   int        `json:"total_added"`
	TotalDeleted int        `json:"total_deleted"`
}

// DiffFile 变更文件
type DiffFile struct {
	Path      string     `json:"path"`
	OldPath   string     `json:"old_path,omitempty"`
	Status    string     `json:"status"`
	Additions int        `json:"additions"`
	Deletions int        `json:"deletions"`
	Hunks     []DiffHunk `json:"hunks"`
}

// DiffHunk Diff 块
type DiffHunk struct {
	OldStart int      `json:"old_start"`
	OldLines int      `json:"old_lines"`
	NewStart int      `json:"new_start"`
	NewLines int      `json:"new_lines"`
	Header   string   `json:"header,omitempty"`
	Changes  []Change `json:"changes"`
}

// Change 变更
type Change struct {
	Type    string `json:"type"`
	OldLine int    `json:"old_line,omitempty"`
	NewLine int    `json:"new_line,omitempty"`
	Content string `json:"content"`
}

var (
	diffGitRegex    = regexp.MustCompile(`^diff --git a/(.*) b/(.*)$`)
	oldFileRegex    = regexp.MustCompile(`^--- (?:a/(.*)|/dev/null)$`)
	newFileRegex    = regexp.MustCompile(`^\+\+\+ (?:b/(.*)|/dev/null)$`)
	hunkHeaderRegex = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(.*)$`)
)

// ParseFile 解析 diff 文件
func (p *DiffParser) ParseFile(diffFilePath string) (*DiffParseResult, error) {
	file, err := os.Open(diffFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open diff file: %w", err)
	}
	defer file.Close()

	return p.Parse(file)
}

// Parse 解析 diff 内容
func (p *DiffParser) Parse(reader interface{ Read([]byte) (int, error) }) (*DiffParseResult, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	result := &DiffParseResult{
		Files: make([]DiffFile, 0),
	}

	var currentFile *DiffFile
	var currentHunk *DiffHunk
	var oldLine, newLine int

	for scanner.Scan() {
		line := scanner.Text()

		if matches := diffGitRegex.FindStringSubmatch(line); matches != nil {
			if currentFile != nil {
				if currentHunk != nil {
					currentFile.Hunks = append(currentFile.Hunks, *currentHunk)
				}
				result.Files = append(result.Files, *currentFile)
			}
			currentFile = &DiffFile{
				Path:  matches[2],
				Hunks: make([]DiffHunk, 0),
			}
			currentHunk = nil
			continue
		}

		if matches := oldFileRegex.FindStringSubmatch(line); matches != nil {
			if currentFile != nil && matches[1] != "" {
				currentFile.OldPath = matches[1]
			}
			continue
		}

		if matches := newFileRegex.FindStringSubmatch(line); matches != nil {
			if currentFile != nil && matches[1] != "" {
				currentFile.Path = matches[1]
			}
			continue
		}

		if matches := hunkHeaderRegex.FindStringSubmatch(line); matches != nil {
			if currentHunk != nil && currentFile != nil {
				currentFile.Hunks = append(currentFile.Hunks, *currentHunk)
			}

			oldStart, _ := strconv.Atoi(matches[1])
			oldLines := 1
			if matches[2] != "" {
				oldLines, _ = strconv.Atoi(matches[2])
			}
			newStart, _ := strconv.Atoi(matches[3])
			newLines := 1
			if matches[4] != "" {
				newLines, _ = strconv.Atoi(matches[4])
			}

			currentHunk = &DiffHunk{
				OldStart: oldStart,
				OldLines: oldLines,
				NewStart: newStart,
				NewLines: newLines,
				Header:   strings.TrimSpace(matches[5]),
				Changes:  make([]Change, 0),
			}

			oldLine = oldStart
			newLine = newStart
			continue
		}

		if currentHunk != nil && len(line) > 0 {
			change := Change{
				Content: line[1:],
			}

			switch line[0] {
			case '+':
				change.Type = "add"
				change.NewLine = newLine
				newLine++
				currentFile.Additions++
				result.TotalAdded++
			case '-':
				change.Type = "delete"
				change.OldLine = oldLine
				oldLine++
				currentFile.Deletions++
				result.TotalDeleted++
			case ' ':
				change.Type = "context"
				change.OldLine = oldLine
				change.NewLine = newLine
				oldLine++
				newLine++
			default:
				continue
			}

			currentHunk.Changes = append(currentHunk.Changes, change)
		}
	}

	if currentFile != nil {
		if currentHunk != nil {
			currentFile.Hunks = append(currentFile.Hunks, *currentHunk)
		}
		if currentFile.Status == "" {
			currentFile.Status = "modified"
		}
		result.Files = append(result.Files, *currentFile)
	}

	result.TotalFiles = len(result.Files)

	// 检查 scanner 错误
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("diff parse error: %w", err)
	}

	return result, nil
}
