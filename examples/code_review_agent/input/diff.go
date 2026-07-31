// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
// Package input 提供 diff 解析功能
package input

import (
	"bufio"
	"context"
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

// ParseGitDiff uses git diff command to parse working tree changes
func (p *DiffParser) ParseGitDiff(ctx context.Context) (*DiffParseResult, error) {
	// Execute git diff with context for cancellation
	cmd := exec.CommandContext(ctx, "git", "diff", "HEAD")
	cmd.Dir = p.repoPath

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff failed: %w", err)
	}

	if len(output) == 0 {
		// No changes, try git diff --cached
		cmd = exec.CommandContext(ctx, "git", "diff", "--cached")
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
		path, err := p.resolveRepoPath(file)
		if err != nil {
			return nil, err
		}

		// 读取文件内容
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to read file %q: %w", file, err)
		}

		// Create a simple diff with the whole file as additions.
		changes := p.contentToChanges(string(content))
		diffFile := DiffFile{
			Path:      file,
			Status:    "added",
			Additions: len(changes),
			Deletions: 0,
			Hunks: []DiffHunk{
				{
					OldStart: 0,
					OldLines: 0,
					NewStart: 1,
					NewLines: len(changes),
					Changes:  changes,
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
	content = strings.TrimSuffix(content, "\n")
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
	resolved, err := p.resolveRepoPath(diffFilePath)
	if err != nil {
		return nil, err
	}
	diffFilePath = resolved
	file, err := os.Open(diffFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open diff file: %w", err)
	}
	defer file.Close()

	return p.Parse(file)
}

func (p *DiffParser) resolveRepoPath(name string) (string, error) {
	if p.repoPath == "" {
		return "", fmt.Errorf("repository path is required to resolve %q", name)
	}
	root, err := filepath.Abs(p.repoPath)
	if err != nil {
		return "", fmt.Errorf("resolve repository path: %w", err)
	}
	candidateName := filepath.FromSlash(name)
	candidate, err := filepath.Abs(candidateName)
	if !filepath.IsAbs(candidateName) {
		candidate, err = filepath.Abs(filepath.Join(root, candidateName))
	}
	if err != nil {
		return "", fmt.Errorf("resolve file path %q: %w", name, err)
	}
	if !isWithinPath(root, candidate) {
		return "", fmt.Errorf("file path %q escapes repository", name)
	}

	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve repository path: %w", err)
	}
	canonicalCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve file path %q: %w", name, err)
	}
	if !isWithinPath(canonicalRoot, canonicalCandidate) {
		return "", fmt.Errorf("file path %q escapes repository through symlink", name)
	}
	return candidate, nil
}

func isWithinPath(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func validateDiffPath(path string) error {
	if path == "" || filepath.IsAbs(filepath.FromSlash(path)) || filepath.VolumeName(filepath.FromSlash(path)) != "" {
		return fmt.Errorf("invalid diff path %q", path)
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("diff path %q escapes repository", path)
	}
	return nil
}

// Parse parses diff content from io.Reader
func (p *DiffParser) Parse(reader io.Reader) (*DiffParseResult, error) {
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
			if err := validateDiffPath(matches[2]); err != nil {
				return nil, err
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
			currentFile = &DiffFile{
				Path:  matches[2],
				Hunks: make([]DiffHunk, 0),
			}
			currentHunk = nil
			continue
		}

		if matches := oldFileRegex.FindStringSubmatch(line); matches != nil {
			if matches[1] != "" {
				if err := validateDiffPath(matches[1]); err != nil {
					return nil, err
				}
			}
			if currentFile == nil {
				currentFile = &DiffFile{Hunks: make([]DiffHunk, 0)}
			}
			if matches[1] != "" {
				currentFile.OldPath = matches[1]
				if currentFile.Path == "" {
					currentFile.Path = matches[1]
				}
			} else {
				currentFile.Status = "added"
			}
			continue
		}

		if matches := newFileRegex.FindStringSubmatch(line); matches != nil {
			if matches[1] != "" {
				if err := validateDiffPath(matches[1]); err != nil {
					return nil, err
				}
			}
			if currentFile == nil {
				currentFile = &DiffFile{Hunks: make([]DiffHunk, 0)}
			}
			if matches[1] != "" {
				currentFile.Path = matches[1]
			} else {
				currentFile.Status = "deleted"
			}
			continue
		}

		if matches := hunkHeaderRegex.FindStringSubmatch(line); matches != nil {
			if currentFile == nil {
				currentFile = &DiffFile{Hunks: make([]DiffHunk, 0)}
			}
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
				if currentFile != nil {
					currentFile.Additions++
				}
				result.TotalAdded++
			case '-':
				change.Type = "delete"
				change.OldLine = oldLine
				oldLine++
				if currentFile != nil {
					currentFile.Deletions++
				}
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
