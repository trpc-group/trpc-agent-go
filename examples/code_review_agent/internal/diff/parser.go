//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package diff provides unified diff parsing for code review.
// It supports parsing unified diff text, git diff output, and
// extracting file-level metadata such as Go package names.
package diff

import (
	"bufio"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Hunk represents a single diff hunk (change block).
type Hunk struct {
	ID       string   `json:"id"`
	OldStart int      `json:"old_start"`
	OldCount int      `json:"old_count"`
	NewStart int      `json:"new_start"`
	NewCount int      `json:"new_count"`
	Lines    []string `json:"lines"`
	Package  string   `json:"package,omitempty"`
}

// HunkID generates a stable identifier for a hunk within a file.
func HunkID(file string, idx int) string {
	return fmt.Sprintf("%s-hunk-%d", file, idx)
}

// ChangedFile describes a file that was changed in the diff.
type ChangedFile struct {
	File        string `json:"file"`
	Status      string `json:"status"`
	Additions   int    `json:"additions"`
	Deletions   int    `json:"deletions"`
	Hunks       []*Hunk
	FullContent string `json:"full_content,omitempty"`
}

var (
	diffFileHeader = regexp.MustCompile(`^diff --git a/(.+?) b/(.+?)$`)
	renameFrom     = regexp.MustCompile(`^--- a/(.+?)$`)
	renameTo       = regexp.MustCompile(`^\+\+\+ b/(.+?)$`)
	hunkHeader     = regexp.MustCompile(`^@@ -(\d+),?(\d*) \+(\d+),?(\d*) @@(.*)$`)
	newFileMode    = regexp.MustCompile(`^new file mode \d+$`)
	deletedFile    = regexp.MustCompile(`^deleted file mode \d+$`)
	indexLine      = regexp.MustCompile(`^index [0-9a-f]+\.\.[0-9a-f]+`)
)

// ParseUnifiedDiff parses a unified diff string and returns the list of
// changed files with their hunks.
func ParseUnifiedDiff(diffContent string) ([]*ChangedFile, error) {
	if strings.TrimSpace(diffContent) == "" {
		return nil, fmt.Errorf("empty diff content")
	}

	var files []*ChangedFile
	var currentFile *ChangedFile
	var currentHunk *Hunk

	scanner := bufio.NewScanner(strings.NewReader(diffContent))
	hunkIndex := 0

	for scanner.Scan() {
		line := scanner.Text()

		switch {
		case diffFileHeader.MatchString(line):
			currentFile, currentHunk, hunkIndex = startNewFile(line)
			files = append(files, currentFile)

		case newFileMode.MatchString(line) && currentFile != nil:
			currentFile.Status = "added"

		case deletedFile.MatchString(line) && currentFile != nil:
			currentFile.Status = "deleted"

		case renameFrom.MatchString(line) && currentFile != nil:
			// Track renames via ---/+++ pair.
			_ = line

		case hunkHeader.MatchString(line):
			currentFile, currentHunk = parseHunkHeader(line, currentFile, currentHunk, hunkIndex)
			if currentHunk != nil && currentFile != nil {
				currentFile.Hunks = append(currentFile.Hunks, currentHunk)
				hunkIndex++
			}

		default:
			if currentHunk != nil {
				currentHunk.Lines = append(currentHunk.Lines, line)
			}
		}
	}

	// Count additions and deletions.
	countAdditionsAndDeletions(files)

	return files, nil
}

// countAdditionsAndDeletions iterates over all files and hunks to count +/- lines.
func countAdditionsAndDeletions(files []*ChangedFile) {
	for _, f := range files {
		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				if strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++") {
					f.Additions++
				} else if strings.HasPrefix(l, "-") && !strings.HasPrefix(l, "---") {
					f.Deletions++
				}
			}
		}
	}
}

// ParseGitDiff runs git diff in the given repository and returns parsed results.
// This function is a stub that shells out to git diff. The diff output is then
// parsed by ParseUnifiedDiff.
func ParseGitDiff(repoPath string, args ...string) ([]*ChangedFile, error) {
	if repoPath == "" {
		return nil, fmt.Errorf("repoPath is required")
	}
	// The actual git command execution will be implemented when the
	// runner.SandboxManager is ready. For now, priority is on
	// parsing pre-generated diff files.
	return nil, fmt.Errorf("ParseGitDiff: not yet implemented, use ParseUnifiedDiff for file-based diffs")
}

// startNewFile handles a "diff --git" header line and returns the new file state.
func startNewFile(line string) (*ChangedFile, *Hunk, int) {
	matches := diffFileHeader.FindStringSubmatch(line)
	cf := &ChangedFile{
		File:   matches[2],
		Status: "modified",
	}
	return cf, nil, 0
}

// parseHunkHeader parses a hunk header line and returns the updated file and hunk.
// If no file is active, it returns nil for both.
func parseHunkHeader(line string, currentFile *ChangedFile, _ *Hunk, hunkIndex int) (*ChangedFile, *Hunk) {
	if currentFile == nil {
		return nil, nil
	}
	matches := hunkHeader.FindStringSubmatch(line)
	oldStart, _ := strconv.Atoi(matches[1])
	oldCount := 0
	if matches[2] != "" {
		oldCount, _ = strconv.Atoi(matches[2])
	}
	newStart, _ := strconv.Atoi(matches[3])
	newCount := 0
	if matches[4] != "" {
		newCount, _ = strconv.Atoi(matches[4])
	}
	hunk := &Hunk{
		ID:       HunkID(currentFile.File, hunkIndex),
		OldStart: oldStart,
		OldCount: oldCount,
		NewStart: newStart,
		NewCount: newCount,
	}
	return currentFile, hunk
}

// PackageFromPath extracts the Go package path from a file path.
// It returns the directory part of the path, which typically corresponds
// to the Go package name.
func PackageFromPath(filePath string) string {
	idx := strings.LastIndex(filePath, "/")
	if idx < 0 {
		return ""
	}
	pkg := filePath[:idx]
	if strings.HasSuffix(pkg, "/internal") {
		pkg = pkg[:len(pkg)-len("/internal")]
	}
	return strings.ReplaceAll(pkg, "/", ".")
}

// IsTestFile returns true if the file path indicates a Go test file.
func IsTestFile(filePath string) bool {
	return strings.HasSuffix(filePath, "_test.go")
}
