//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package internal provides internal types and logic for the code
// review agent example.
package internal

import (
	"bufio"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// LineType describes the kind of line in a diff hunk.
type LineType int

const (
	LineContext LineType = iota
	LineAdded
	LineRemoved
)

// String returns a human-readable name for the line type.
func (t LineType) String() string {
	switch t {
	case LineAdded:
		return "added"
	case LineRemoved:
		return "removed"
	default:
		return "context"
	}
}

// DiffLine represents a single line within a hunk.
type DiffLine struct {
	Type      LineType
	Number    int    // line number in the new file (0 for removed-only lines)
	OldNumber int    // line number in the old file
	Content   string // line content without the leading +, -, or space
}

// DiffHunk represents a contiguous block of changes in a file.
type DiffHunk struct {
	OldStart int
	OldCount int
	NewStart int
	NewCount int
	Lines    []DiffLine
}

// DiffFile represents a file changed in a diff.
type DiffFile struct {
	Path      string
	OldPath   string
	IsNew     bool
	IsDeleted bool
	IsRename  bool
	Hunks     []DiffHunk
}

// Package returns the Go package path derived from the file path, if
// the file is a .go file inside a module.
func (f DiffFile) Package(rootModule string) string {
	if !strings.HasSuffix(f.Path, ".go") {
		return ""
	}
	dir := filepath.Dir(f.Path)
	dir = strings.TrimPrefix(dir, "./")
	if dir == "." || dir == "" {
		return rootModule
	}
	return rootModule + "/" + dir
}

// ChangedGoFiles returns only the .go files that have added lines.
func ChangedGoFiles(files []DiffFile) []DiffFile {
	var out []DiffFile
	for _, f := range files {
		if f.IsDeleted {
			continue
		}
		if !strings.HasSuffix(f.Path, ".go") {
			continue
		}
		out = append(out, f)
	}
	return out
}

// AddedLines returns all added lines across all hunks.
func (f DiffFile) AddedLines() []DiffLine {
	var out []DiffLine
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			if l.Type == LineAdded {
				out = append(out, l)
			}
		}
	}
	return out
}

var (
	// Matches: diff --git a/foo/bar.go b/foo/bar.go
	reDiffGit = regexp.MustCompile(`^diff --git a/(.*) b/(.*)$`)
	// Matches: --- a/foo/bar.go  (or --- /dev/null)
	reOldFile = regexp.MustCompile(`^--- (a/(.+)|/dev/null)$`)
	// Matches: +++ b/foo/bar.go  (or +++ /dev/null)
	reNewFile = regexp.MustCompile(`^\+\+\+ (b/(.+)|/dev/null)$`)
	// Matches: @@ -start,count +start,count @@
	reHunk = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)
	// Matches: rename from old
	reRenameFrom = regexp.MustCompile(`^rename from (.+)$`)
	// Matches: rename to new
	reRenameTo = regexp.MustCompile(`^rename to (.+)$`)
	// Matches: new file mode
	reNewFileMode = regexp.MustCompile(`^new file mode`)
	// Matches: deleted file mode
	reDeletedFileMode = regexp.MustCompile(`^deleted file mode`)
)

// ParseDiff parses a unified diff and returns the list of changed
// files with their hunks and lines.
func ParseDiff(r io.Reader) ([]DiffFile, error) {
	scanner := bufio.NewScanner(r)
	// Increase buffer size for large diffs.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var files []DiffFile
	var current *DiffFile
	var currentHunk *DiffHunk
	var oldLine, newLine int

	flushHunk := func() {
		if currentHunk != nil && current != nil {
			current.Hunks = append(current.Hunks, *currentHunk)
			currentHunk = nil
		}
	}
	flushFile := func() {
		flushHunk()
		if current != nil {
			files = append(files, *current)
			current = nil
		}
	}

	for scanner.Scan() {
		line := scanner.Text()

		switch {
		case strings.HasPrefix(line, "diff --git "):
			flushFile()
			m := reDiffGit.FindStringSubmatch(line)
			if m != nil {
				current = &DiffFile{Path: m[2], OldPath: m[1]}
			}
			currentHunk = nil

		case strings.HasPrefix(line, "--- "):
			// Old file path
			if current != nil {
				m := reOldFile.FindStringSubmatch(line)
				if m != nil {
					if m[2] != "" {
						current.OldPath = m[2]
					} else {
						// /dev/null means new file
						current.IsNew = true
					}
				}
			}

		case strings.HasPrefix(line, "+++ "):
			if current != nil {
				m := reNewFile.FindStringSubmatch(line)
				if m != nil {
					if m[2] != "" {
						current.Path = m[2]
					} else {
						// /dev/null means deleted file
						current.IsDeleted = true
					}
				}
			}

		case strings.HasPrefix(line, "@@"):
			flushHunk()
			m := reHunk.FindStringSubmatch(line)
			if m != nil && current != nil {
				oldStart, _ := strconv.Atoi(m[1])
				oldCount := 1
				if m[2] != "" {
					oldCount, _ = strconv.Atoi(m[2])
				}
				newStart, _ := strconv.Atoi(m[3])
				newCount := 1
				if m[4] != "" {
					newCount, _ = strconv.Atoi(m[4])
				}
				currentHunk = &DiffHunk{
					OldStart: oldStart,
					OldCount: oldCount,
					NewStart: newStart,
					NewCount: newCount,
				}
				oldLine = oldStart
				newLine = newStart
			}

		case strings.HasPrefix(line, "rename from "):
			if current != nil {
				m := reRenameFrom.FindStringSubmatch(line)
				if m != nil {
					current.OldPath = m[1]
					current.IsRename = true
				}
			}

		case strings.HasPrefix(line, "rename to "):
			if current != nil {
				m := reRenameTo.FindStringSubmatch(line)
				if m != nil {
					current.Path = m[1]
					current.IsRename = true
				}
			}

		case strings.HasPrefix(line, "new file mode"):
			if current != nil {
				current.IsNew = true
			}

		case strings.HasPrefix(line, "deleted file mode"):
			if current != nil {
				current.IsDeleted = true
			}

		case strings.HasPrefix(line, "+"):
			if currentHunk != nil {
				currentHunk.Lines = append(currentHunk.Lines, DiffLine{
					Type:    LineAdded,
					Number:  newLine,
					Content: line[1:],
				})
				newLine++
			}

		case strings.HasPrefix(line, "-"):
			if currentHunk != nil {
				currentHunk.Lines = append(currentHunk.Lines, DiffLine{
					Type:      LineRemoved,
					OldNumber: oldLine,
					Content:   line[1:],
				})
				oldLine++
			}

		default:
			// Context line (starts with space or empty)
			if currentHunk != nil {
				content := line
				if strings.HasPrefix(line, " ") {
					content = line[1:]
				}
				currentHunk.Lines = append(currentHunk.Lines, DiffLine{
					Type:      LineContext,
					Number:    newLine,
					OldNumber: oldLine,
					Content:   content,
				})
				oldLine++
				newLine++
			}
		}
	}

	flushFile()

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning diff: %w", err)
	}

	return files, nil
}

// ParseDiffString is a convenience wrapper for ParseDiff that accepts
// a string.
func ParseDiffString(s string) ([]DiffFile, error) {
	return ParseDiff(strings.NewReader(s))
}

// DiffSummary returns a short human-readable summary of the parsed
// diff, e.g. "3 files, +42 -10".
func DiffSummary(files []DiffFile) string {
	var added, removed, fileCount int
	for _, f := range files {
		fileCount++
		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				switch l.Type {
				case LineAdded:
					added++
				case LineRemoved:
					removed++
				}
			}
		}
	}
	return fmt.Sprintf("%d files, +%d -%d", fileCount, added, removed)
}
