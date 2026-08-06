//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package diff provides a unified-diff parser that produces structured
// file-change records suitable for rule-based code review.
//
// The parser understands the standard unified diff format produced by
// git diff, hg diff, and diff -u. It extracts per-file metadata (old/new
// paths, added/deleted line counts) and per-hunk line ranges so review
// rules can anchor findings to specific locations in the new file.
package diff

import (
	"bufio"
	"fmt"
	"strings"
)

// FileChange describes the diff for a single file.
type FileChange struct {
	// OldPath is the a/path component, empty for new files.
	OldPath string

	// NewPath is the b/path component, empty for deleted files.
	NewPath string

	// Status classifies the change: "added", "modified", "deleted",
	// "renamed".
	Status string

	// AddedLines counts lines starting with "+" (excluding headers).
	AddedLines int

	// DeletedLines counts lines starting with "-" (excluding headers).
	DeletedLines int

	// Hunks is the list of hunks in this file's diff.
	Hunks []Hunk

	// AddedLineNumbers holds the 1-based line numbers in the new
	// file for every added line, so rules can report precise
	// locations without re-parsing.
	AddedLineNumbers []int
}

// Hunk is a single contiguous diff region inside a FileChange.
type Hunk struct {
	// OldStart is the starting line number in the old file.
	OldStart int

	// OldCount is the number of lines the hunk covers in the old
	// file.
	OldCount int

	// NewStart is the starting line number in the new file.
	NewStart int

	// NewCount is the number of lines the hunk covers in the new
	// file.
	NewCount int

	// Lines holds the raw diff body lines of the hunk, each starting
	// with " ", "+", or "-".
	Lines []string
}

// AddedContent returns the content of all added lines in the file
// change, joined with newlines. This is what most rules inspect.
func (fc *FileChange) AddedContent() string {
	var b strings.Builder
	for _, h := range fc.Hunks {
		for _, line := range h.Lines {
			if strings.HasPrefix(line, "+") {
				b.WriteString(line[1:])
				b.WriteByte('\n')
			}
		}
	}
	return b.String()
}

// AllContent returns the content of all non-header lines in the file
// change, joined with newlines. Context lines are prefixed with a
// space, removed lines with "-", added lines with "+".
func (fc *FileChange) AllContent() string {
	var b strings.Builder
	for _, h := range fc.Hunks {
		for _, line := range h.Lines {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// Parse parses a unified diff and returns one FileChange per file
// referenced in the diff. Returns an error if the diff is malformed
// beyond recovery.
func Parse(diffText string) ([]FileChange, error) {
	scanner := bufio.NewScanner(strings.NewReader(diffText))
	// Allow long lines (up to 1 MB) to handle generated or
	// minified files.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var changes []FileChange
	var current *FileChange
	var currentHunk *Hunk
	var newLineNum int

	finishHunk := func() {
		if currentHunk != nil && current != nil {
			current.Hunks = append(current.Hunks, *currentHunk)
			currentHunk = nil
		}
	}
	finishFile := func() {
		finishHunk()
		if current != nil {
			changes = append(changes, *current)
			current = nil
		}
	}

	for scanner.Scan() {
		line := scanner.Text()

		// File header: "diff --git a/path b/path"
		if strings.HasPrefix(line, "diff --git ") {
			finishFile()
			current = &FileChange{Status: "modified"}
			paths := parseGitPaths(line)
			if len(paths) == 2 {
				current.OldPath = paths[0]
				current.NewPath = paths[1]
			}
			continue
		}

		// Old file marker: "--- a/path" or "--- /dev/null"
		if strings.HasPrefix(line, "--- ") {
			if current == nil {
				current = &FileChange{Status: "modified"}
			}
			current.OldPath = parseDiffPath(line[4:])
			if current.OldPath == "/dev/null" {
				current.OldPath = ""
				current.Status = "added"
			}
			continue
		}

		// New file marker: "+++ b/path" or "+++ /dev/null"
		if strings.HasPrefix(line, "+++ ") {
			if current == nil {
				current = &FileChange{Status: "modified"}
			}
			current.NewPath = parseDiffPath(line[4:])
			if current.NewPath == "/dev/null" {
				current.NewPath = ""
				current.Status = "deleted"
			}
			continue
		}

		// Rename detection
		if strings.HasPrefix(line, "rename from ") {
			if current != nil {
				current.Status = "renamed"
			}
			continue
		}
		if strings.HasPrefix(line, "rename to ") {
			if current != nil {
				current.Status = "renamed"
			}
			continue
		}

		// New file mode
		if strings.HasPrefix(line, "new file mode") {
			if current != nil {
				current.Status = "added"
			}
			continue
		}
		if strings.HasPrefix(line, "deleted file mode") {
			if current != nil {
				current.Status = "deleted"
			}
			continue
		}

		// Hunk header: "@@ -oldStart,oldCount +newStart,newCount @@"
		if strings.HasPrefix(line, "@@") {
			finishHunk()
			h, ns, err := parseHunkHeader(line)
			if err != nil {
				continue // skip malformed hunks
			}
			currentHunk = &h
			newLineNum = ns
			continue
		}

		// Diff body
		if currentHunk != nil {
			// Git emits "\ No newline at end of file" inside the
			// hunk body. This marker is not a content line: it must
			// not be stored in Hunk.Lines (rules would scan it as
			// source code) and must not increment newLineNum (every
			// subsequent added line would be offset by 1).
			if strings.HasPrefix(line, `\ `) {
				continue
			}
			currentHunk.Lines = append(currentHunk.Lines, line)
			switch {
			case strings.HasPrefix(line, "+"):
				if current != nil {
					current.AddedLines++
					current.AddedLineNumbers = append(
						current.AddedLineNumbers, newLineNum)
				}
				newLineNum++
			case strings.HasPrefix(line, "-"):
				if current != nil {
					current.DeletedLines++
				}
			default:
				newLineNum++
			}
		}
	}
	finishFile()

	if err := scanner.Err(); err != nil {
		return changes, fmt.Errorf("scan diff: %w", err)
	}
	return changes, nil
}

// parseDiffPath strips the leading "a/" or "b/" prefix from a diff
// path. If the path is "/dev/null" it is returned as-is so the caller
// can detect file creation/deletion.
func parseDiffPath(s string) string {
	s = strings.TrimSpace(s)
	// Strip surrounding quotes for paths with spaces.
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	if strings.HasPrefix(s, "a/") {
		return s[2:]
	}
	if strings.HasPrefix(s, "b/") {
		return s[2:]
	}
	return s
}

// parseGitPaths extracts the two paths from a "diff --git a/x b/y"
// header line.
func parseGitPaths(line string) []string {
	rest := strings.TrimPrefix(line, "diff --git ")
	parts := strings.Fields(rest)
	if len(parts) >= 2 {
		return []string{
			parseDiffPath(parts[0]),
			parseDiffPath(parts[1]),
		}
	}
	return nil
}

// parseHunkHeader parses a "@@ -oStart,oCount +nStart,nCount @@"
// header and returns the Hunk plus the new-file starting line number.
func parseHunkHeader(line string) (Hunk, int, error) {
	var h Hunk
	// Strip the leading "@@ " and trailing " @@".
	rest := strings.TrimPrefix(line, "@@ ")
	if idx := strings.Index(rest, "@@"); idx >= 0 {
		rest = rest[:idx]
	}
	rest = strings.TrimSpace(rest)

	var oldPart, newPart string
	parts := strings.Fields(rest)
	if len(parts) < 2 {
		return h, 0, fmt.Errorf("malformed hunk header: %s", line)
	}
	// Find the "-" and "+" parts.
	for _, p := range parts {
		if strings.HasPrefix(p, "-") {
			oldPart = p[1:]
		} else if strings.HasPrefix(p, "+") {
			newPart = p[1:]
		}
	}
	if oldPart == "" || newPart == "" {
		return h, 0, fmt.Errorf("missing range in hunk header: %s", line)
	}

	oldStart, oldCount, err := parseRange(oldPart)
	if err != nil {
		return h, 0, err
	}
	newStart, newCount, err := parseRange(newPart)
	if err != nil {
		return h, 0, err
	}
	h.OldStart = oldStart
	h.OldCount = oldCount
	h.NewStart = newStart
	h.NewCount = newCount
	return h, newStart, nil
}

// parseRange parses "start,count" or just "start" (count defaults
// to 1).
func parseRange(s string) (int, int, error) {
	if idx := strings.Index(s, ","); idx >= 0 {
		start := atoi(s[:idx])
		count := atoi(s[idx+1:])
		return start, count, nil
	}
	n := atoi(s)
	return n, 1, nil
}

// atoi is a small, panic-free integer parser used only for diff line
// numbers. Returns 0 on parse failure.
func atoi(s string) int {
	n := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0
		}
		n = n*10 + int(ch-'0')
	}
	return n
}
