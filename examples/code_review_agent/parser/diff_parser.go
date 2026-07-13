//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package parser

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type DiffHunk struct {
	OldStart     int
	OldCount     int
	NewStart     int
	NewCount     int
	Content      []string
	AddedLines   []int
	RemovedLines []int
}

type DiffFile struct {
	OldPath      string
	NewPath      string
	IsNewFile    bool
	IsDeleted    bool
	Hunks        []DiffHunk
	GoPackage    string
	AddedCode    []string
	RemovedCode  []string
	ModifiedCode []string
}

type DiffResult struct {
	Files        []DiffFile
	TotalAdded   int
	TotalRemoved int
	TotalChanged int
}

var (
	diffHeaderRe  = regexp.MustCompile(`^diff --git a/(.*) b/(.*)`)
	newFileRe     = regexp.MustCompile(`^new file mode`)
	deletedFileRe = regexp.MustCompile(`^deleted file mode`)
	hunkHeaderRe  = regexp.MustCompile(`^@@ -(\d+)(,(\d+))? \+(\d+)(,(\d+))? @@`)
)

func ParseDiffFile(path string) (*DiffResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read diff file: %w", err)
	}
	return ParseDiff(string(data))
}

func ParseDiff(diffContent string) (*DiffResult, error) {
	result := &DiffResult{}
	scanner := bufio.NewScanner(strings.NewReader(diffContent))

	const maxBufferSize = 4 * 1024 * 1024
	buf := make([]byte, 0, bufio.MaxScanTokenSize)
	scanner.Buffer(buf, maxBufferSize)

	var currentFile *DiffFile
	var currentHunk *DiffHunk
	inHunk := false
	newLineOffset := 0
	oldLineOffset := 0

	for scanner.Scan() {
		line := scanner.Text()

		if matches := diffHeaderRe.FindStringSubmatch(line); len(matches) == 3 {
			if currentFile != nil {
				result.Files = append(result.Files, *currentFile)
			}
			currentFile = &DiffFile{
				OldPath: matches[1],
				NewPath: matches[2],
			}
			inHunk = false
			continue
		}

		if currentFile == nil {
			continue
		}

		if newFileRe.MatchString(line) {
			currentFile.IsNewFile = true
			continue
		}

		if deletedFileRe.MatchString(line) {
			currentFile.IsDeleted = true
			continue
		}

		if strings.HasPrefix(line, "---") {
			continue
		}

		if strings.HasPrefix(line, "+++") {
			continue
		}

		if matches := hunkHeaderRe.FindStringSubmatch(line); len(matches) >= 5 {
			if currentHunk != nil {
				currentFile.Hunks = append(currentFile.Hunks, *currentHunk)
			}

			oldStart, _ := strconv.Atoi(matches[1])
			oldCount := 1
			if len(matches) > 3 && matches[3] != "" {
				oldCount, _ = strconv.Atoi(matches[3])
			}

			newStart, _ := strconv.Atoi(matches[4])
			newCount := 1
			if len(matches) > 6 && matches[6] != "" {
				newCount, _ = strconv.Atoi(matches[6])
			}

			currentHunk = &DiffHunk{
				OldStart: oldStart,
				OldCount: oldCount,
				NewStart: newStart,
				NewCount: newCount,
			}
			inHunk = true
			newLineOffset = 0
			oldLineOffset = 0
			continue
		}

		if inHunk && currentHunk != nil {
			currentHunk.Content = append(currentHunk.Content, line)

			if len(line) == 0 {
				newLineOffset++
				oldLineOffset++
				continue
			}

			switch line[0] {
			case '+':
				if line != "+++" {
					currentHunk.AddedLines = append(currentHunk.AddedLines, currentHunk.NewStart+newLineOffset)
					currentFile.AddedCode = append(currentFile.AddedCode, line[1:])
					result.TotalAdded++
				}
				newLineOffset++
			case '-':
				if line != "---" {
					currentHunk.RemovedLines = append(currentHunk.RemovedLines, currentHunk.OldStart+oldLineOffset)
					currentFile.RemovedCode = append(currentFile.RemovedCode, line[1:])
					result.TotalRemoved++
				}
				oldLineOffset++
			default:
				currentFile.ModifiedCode = append(currentFile.ModifiedCode, line)
				newLineOffset++
				oldLineOffset++
			}
		}
	}

	if currentHunk != nil && currentFile != nil {
		currentFile.Hunks = append(currentFile.Hunks, *currentHunk)
	}
	if currentFile != nil {
		result.Files = append(result.Files, *currentFile)
	}

	result.TotalChanged = result.TotalAdded + result.TotalRemoved

	for i := range result.Files {
		result.Files[i].GoPackage = extractGoPackage(result.Files[i].NewPath)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan diff content: %w", err)
	}

	return result, nil
}

func extractGoPackage(filePath string) string {
	if !strings.HasSuffix(filePath, ".go") {
		return ""
	}

	dir := filepath.Dir(filePath)
	if dir == "." {
		return ""
	}

	return dir
}

func FilterGoFiles(files []DiffFile) []DiffFile {
	var result []DiffFile
	for _, f := range files {
		if strings.HasSuffix(f.NewPath, ".go") && !strings.HasSuffix(f.NewPath, "_test.go") {
			result = append(result, f)
		}
	}
	return result
}

func GetChangedLines(diff *DiffResult) map[string][]int {
	result := make(map[string][]int)
	for _, file := range diff.Files {
		for _, hunk := range file.Hunks {
			for _, line := range hunk.AddedLines {
				result[file.NewPath] = append(result[file.NewPath], line)
			}
		}
	}
	return result
}

func FormatDiffForReview(diff *DiffResult) string {
	var buf bytes.Buffer
	for _, file := range diff.Files {
		buf.WriteString(fmt.Sprintf("=== File: %s ===\n", file.NewPath))
		if file.GoPackage != "" {
			buf.WriteString(fmt.Sprintf("Package: %s\n", file.GoPackage))
		}
		buf.WriteString(fmt.Sprintf("Added: %d lines, Removed: %d lines\n", len(file.AddedCode), len(file.RemovedCode)))
		buf.WriteString("\n--- Added Code ---\n")
		for _, line := range file.AddedCode {
			buf.WriteString(fmt.Sprintf("+ %s\n", line))
		}
		buf.WriteString("\n")
	}
	return buf.String()
}
