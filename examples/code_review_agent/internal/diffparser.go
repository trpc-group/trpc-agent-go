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
	sawContent := false

	flushHunk := func() error {
		if currentHunk != nil && current != nil {
			var oldCount, newCount int
			for _, diffLine := range currentHunk.Lines {
				switch diffLine.Type {
				case LineAdded:
					newCount++
				case LineRemoved:
					oldCount++
				default:
					oldCount++
					newCount++
				}
			}
			if oldCount != currentHunk.OldCount || newCount != currentHunk.NewCount {
				return fmt.Errorf(
					"hunk for %q declares -%d,+%d lines but contains -%d,+%d",
					current.Path, currentHunk.OldCount, currentHunk.NewCount, oldCount, newCount,
				)
			}
			current.Hunks = append(current.Hunks, *currentHunk)
			currentHunk = nil
		}
		return nil
	}
	flushFile := func() error {
		if err := flushHunk(); err != nil {
			return err
		}
		if current != nil {
			files = append(files, *current)
			current = nil
		}
		return nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) != "" {
			sawContent = true
		}

		switch {
		case strings.HasPrefix(line, "diff --git "):
			if err := flushFile(); err != nil {
				return nil, err
			}
			oldPath, newPath, err := parseDiffGitHeader(line)
			if err != nil {
				return nil, err
			}
			current = &DiffFile{Path: newPath, OldPath: oldPath}
			currentHunk = nil

		case currentHunk == nil && strings.HasPrefix(line, "--- "):
			if current == nil {
				return nil, fmt.Errorf("old-file marker appears outside a file diff")
			}
			oldPath, isNull, err := parseGitFileMarker(line, "--- ", "a/")
			if err != nil {
				return nil, err
			}
			if isNull {
				current.IsNew = true
			} else {
				current.OldPath = oldPath
			}

		case currentHunk == nil && strings.HasPrefix(line, "+++ "):
			if current == nil {
				return nil, fmt.Errorf("new-file marker appears outside a file diff")
			}
			newPath, isNull, err := parseGitFileMarker(line, "+++ ", "b/")
			if err != nil {
				return nil, err
			}
			if isNull {
				current.IsDeleted = true
			} else {
				current.Path = newPath
			}

		case strings.HasPrefix(line, "@@"):
			m := reHunk.FindStringSubmatch(line)
			if m == nil || current == nil {
				return nil, fmt.Errorf("parse hunk header %q", line)
			}
			if err := flushHunk(); err != nil {
				return nil, err
			}
			if m != nil {
				oldStart, err := parseHunkInteger(m[1], "old start")
				if err != nil {
					return nil, err
				}
				oldCount := 1
				if m[2] != "" {
					oldCount, err = parseHunkInteger(m[2], "old count")
					if err != nil {
						return nil, err
					}
				}
				newStart, err := parseHunkInteger(m[3], "new start")
				if err != nil {
					return nil, err
				}
				newCount := 1
				if m[4] != "" {
					newCount, err = parseHunkInteger(m[4], "new count")
					if err != nil {
						return nil, err
					}
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
			if currentHunk != nil {
				return nil, fmt.Errorf("rename-from metadata appears inside a hunk")
			}
			if current == nil {
				return nil, fmt.Errorf("rename-from metadata appears outside a file diff")
			}
			m := reRenameFrom.FindStringSubmatch(line)
			if m == nil {
				return nil, fmt.Errorf("parse rename-from metadata %q", line)
			}
			decoded, err := decodeGitMetadataPath(m[1])
			if err != nil {
				return nil, fmt.Errorf("parse rename-from path %q: %w", m[1], err)
			}
			current.OldPath = decoded
			current.IsRename = true

		case strings.HasPrefix(line, "rename to "):
			if currentHunk != nil {
				return nil, fmt.Errorf("rename-to metadata appears inside a hunk")
			}
			if current == nil {
				return nil, fmt.Errorf("rename-to metadata appears outside a file diff")
			}
			m := reRenameTo.FindStringSubmatch(line)
			if m == nil {
				return nil, fmt.Errorf("parse rename-to metadata %q", line)
			}
			decoded, err := decodeGitMetadataPath(m[1])
			if err != nil {
				return nil, fmt.Errorf("parse rename-to path %q: %w", m[1], err)
			}
			current.Path = decoded
			current.IsRename = true

		case strings.HasPrefix(line, "new file mode"):
			if currentHunk != nil {
				return nil, fmt.Errorf("new-file metadata appears inside a hunk")
			}
			if current == nil {
				return nil, fmt.Errorf("new-file metadata appears outside a file diff")
			}
			current.IsNew = true

		case strings.HasPrefix(line, "deleted file mode"):
			if currentHunk != nil {
				return nil, fmt.Errorf("deleted-file metadata appears inside a hunk")
			}
			if current == nil {
				return nil, fmt.Errorf("deleted-file metadata appears outside a file diff")
			}
			current.IsDeleted = true

		case line == `\ No newline at end of file`:
			// This marker describes the preceding diff line and consumes no
			// source or destination line number.
			if currentHunk == nil || len(currentHunk.Lines) == 0 {
				return nil, fmt.Errorf("no-newline marker appears outside a hunk")
			}
			continue

		case strings.HasPrefix(line, "+"):
			if currentHunk == nil {
				return nil, fmt.Errorf("added line appears outside a hunk: %q", line)
			}
			currentHunk.Lines = append(currentHunk.Lines, DiffLine{
				Type:    LineAdded,
				Number:  newLine,
				Content: line[1:],
			})
			newLine++

		case strings.HasPrefix(line, "-"):
			if currentHunk == nil {
				return nil, fmt.Errorf("removed line appears outside a hunk: %q", line)
			}
			currentHunk.Lines = append(currentHunk.Lines, DiffLine{
				Type:      LineRemoved,
				OldNumber: oldLine,
				Content:   line[1:],
			})
			oldLine++

		default:
			// Context lines in a unified diff always start with one space. An
			// unprefixed line inside a hunk is malformed and must not be treated
			// as reviewed source.
			if currentHunk != nil {
				if !strings.HasPrefix(line, " ") {
					return nil, fmt.Errorf("invalid unprefixed line inside hunk: %q", line)
				}
				currentHunk.Lines = append(currentHunk.Lines, DiffLine{
					Type:      LineContext,
					Number:    newLine,
					OldNumber: oldLine,
					Content:   line[1:],
				})
				oldLine++
				newLine++
			}
		}
	}

	if err := flushFile(); err != nil {
		return nil, err
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning diff: %w", err)
	}
	if sawContent && len(files) == 0 {
		return nil, fmt.Errorf("input contains no parseable git diff")
	}

	return files, nil
}

func decodeGitMetadataPath(value string) (string, error) {
	if !strings.HasPrefix(value, `"`) {
		return value, nil
	}
	decoded, trailing, err := parseGitPathToken(value)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(trailing) != "" {
		return "", fmt.Errorf("unexpected trailing path data %q", trailing)
	}
	return decoded, nil
}

func parseGitFileMarker(line, marker, sidePrefix string) (string, bool, error) {
	value := strings.TrimPrefix(line, marker)
	decoded, err := decodeGitMetadataPath(value)
	if err != nil {
		return "", false, fmt.Errorf("parse %s path %q: %w", strings.TrimSpace(marker), value, err)
	}
	if decoded == "/dev/null" {
		return "", true, nil
	}
	if !strings.HasPrefix(decoded, sidePrefix) || len(decoded) == len(sidePrefix) {
		return "", false, fmt.Errorf("parse %s path %q: expected %s path", strings.TrimSpace(marker), value, sidePrefix)
	}
	return strings.TrimPrefix(decoded, sidePrefix), false, nil
}

func parseHunkInteger(value, field string) (int, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("parse hunk %s %q: %w", field, value, err)
	}
	return parsed, nil
}

func parseDiffGitHeader(line string) (string, string, error) {
	rest := strings.TrimPrefix(line, "diff --git ")
	oldToken, rest, err := parseGitPathToken(rest)
	if err != nil {
		return "", "", fmt.Errorf("parse diff --git header %q: %w", line, err)
	}
	newToken, trailing, err := parseGitPathToken(strings.TrimLeft(rest, " \t"))
	if err != nil {
		return "", "", fmt.Errorf("parse diff --git header %q: %w", line, err)
	}
	if strings.TrimSpace(trailing) != "" || !strings.HasPrefix(oldToken, "a/") || !strings.HasPrefix(newToken, "b/") {
		return "", "", fmt.Errorf("parse diff --git header %q: invalid path pair", line)
	}
	return strings.TrimPrefix(oldToken, "a/"), strings.TrimPrefix(newToken, "b/"), nil
}

func parseGitPathToken(input string) (string, string, error) {
	if input == "" {
		return "", "", fmt.Errorf("missing path")
	}
	if input[0] != '"' {
		if index := strings.IndexAny(input, " \t"); index >= 0 {
			return input[:index], input[index:], nil
		}
		return input, "", nil
	}
	escaped := false
	for index := 1; index < len(input); index++ {
		switch {
		case escaped:
			escaped = false
		case input[index] == '\\':
			escaped = true
		case input[index] == '"':
			decoded, err := strconv.Unquote(input[:index+1])
			if err != nil {
				return "", "", err
			}
			return decoded, input[index+1:], nil
		}
	}
	return "", "", fmt.Errorf("unterminated quoted path")
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
