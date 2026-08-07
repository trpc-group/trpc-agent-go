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
	"bufio"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// FileChange represents changes within a single file extracted from a diff.
type FileChange struct {
	OldPath string
	NewPath string
	Package string
	Hunks   []*Hunk
}

// Hunk represents a contiguous block of changes in a diff.
type Hunk struct {
	OldStart int
	OldLines int
	NewStart int
	NewLines int
	Lines    []DiffLine
}

// DiffLine represents a line in a diff hunk.
type DiffLine struct {
	Type    string // "+", "-", " "
	OldLine int
	NewLine int
	Content string
}

var (
	hunkHeaderRegex = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)
	packageRegex    = regexp.MustCompile(`^package\s+([a-zA-Z0-9_]+)`)
)

// ParseUnifiedDiff parses a raw unified diff string into structured FileChanges.
func ParseUnifiedDiff(diff string) ([]FileChange, error) {
	var changes []*FileChange
	var currentFile *FileChange
	var currentHunk *Hunk

	scanner := bufio.NewScanner(strings.NewReader(diff))
	oldLineNum := 0
	newLineNum := 0

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "diff --git") {
			currentHunk = nil
			continue
		}

		if currentHunk == nil && strings.HasPrefix(line, "--- ") {
			path := strings.TrimPrefix(line, "--- ")
			path = strings.TrimPrefix(path, "a/")
			if currentFile == nil {
				currentFile = &FileChange{OldPath: filepath.ToSlash(path)}
			} else {
				currentFile.OldPath = filepath.ToSlash(path)
			}
			continue
		}

		if currentHunk == nil && strings.HasPrefix(line, "+++ ") {
			path := strings.TrimPrefix(line, "+++ ")
			path = strings.TrimPrefix(path, "b/")
			if currentFile == nil {
				currentFile = &FileChange{}
			}
			currentFile.NewPath = filepath.ToSlash(path)
			changes = append(changes, currentFile)
			currentFile = nil
			currentHunk = nil
			continue
		}

		if strings.HasPrefix(line, "@@") {
			matches := hunkHeaderRegex.FindStringSubmatch(line)
			if len(matches) >= 4 && len(changes) > 0 {
				targetFile := changes[len(changes)-1]
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

				hunk := &Hunk{
					OldStart: oldStart,
					OldLines: oldLines,
					NewStart: newStart,
					NewLines: newLines,
				}
				targetFile.Hunks = append(targetFile.Hunks, hunk)
				currentHunk = hunk
				oldLineNum = oldStart
				newLineNum = newStart
			}
			continue
		}

		if currentHunk != nil {
			if strings.HasPrefix(line, `\`) {
				continue
			}

			lineType := " "
			content := ""
			if len(line) > 0 {
				lineType = line[:1]
				content = line[1:]
			}

			if len(changes) > 0 {
				targetFile := changes[len(changes)-1]
				if pkgMatch := packageRegex.FindStringSubmatch(content); len(pkgMatch) > 1 {
					targetFile.Package = pkgMatch[1]
				}
			}

			dl := DiffLine{
				Type:    lineType,
				Content: content,
			}

			switch lineType {
			case "+":
				dl.NewLine = newLineNum
				newLineNum++
			case "-":
				dl.OldLine = oldLineNum
				oldLineNum++
			case " ":
				dl.OldLine = oldLineNum
				dl.NewLine = newLineNum
				oldLineNum++
				newLineNum++
			}
			currentHunk.Lines = append(currentHunk.Lines, dl)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan diff failed: %w", err)
	}

	result := make([]FileChange, 0, len(changes))
	for _, c := range changes {
		result = append(result, *c)
	}
	return result, nil
}
