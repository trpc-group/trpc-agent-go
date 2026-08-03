//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package diffparse

import (
	"bufio"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

var hunkHeaderRE = regexp.MustCompile(`^@@ -([0-9]+)(?:,([0-9]+))? \+([0-9]+)(?:,([0-9]+))? @@`)

// Parse parses a unified diff into review DiffFile records.
func Parse(diff string) ([]review.DiffFile, error) {
	const (
		diffHeaderComplete = iota
		diffHeaderNeedsNewPath
		diffHeaderNeedsHunk
	)
	// A header after a completed hunk must lead to another hunk, so an extra
	// header-like deletion cannot be silently accepted as a new file.
	var files []review.DiffFile
	var current *review.DiffFile
	var currentHunk *review.DiffHunk
	oldLine := 0
	newLine := 0
	headerState := diffHeaderComplete
	headerAfterCompletedHunk := false

	scanner := bufio.NewScanner(strings.NewReader(diff))
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		switch headerState {
		case diffHeaderNeedsNewPath:
			if !strings.HasPrefix(line, "+++ ") {
				return nil, fmt.Errorf("hunk contains content after declared ranges were consumed: expected +++ file header")
			}
		case diffHeaderNeedsHunk:
			if !strings.HasPrefix(line, "@@ ") {
				return nil, fmt.Errorf("file header is missing a hunk")
			}
		}
		if currentHunk != nil && hunkHasRemaining(currentHunk, oldLine, newLine) && line != "" && !isDiffHunkLine(line) {
			return nil, fmt.Errorf("hunk ended before declared ranges were consumed")
		}
		if currentHunk != nil && isDiffHunkLine(line) && shouldParseHunkLine(currentHunk, oldLine, newLine, line) {
			if err := validateHunkLine(currentHunk, oldLine, newLine, line); err != nil {
				return nil, err
			}
			diffLine, nextOld, nextNew := parseDiffLine(line, oldLine, newLine)
			currentHunk.Lines = append(currentHunk.Lines, diffLine)
			oldLine = nextOld
			newLine = nextNew
			continue
		}
		switch {
		case strings.HasPrefix(line, "diff --git "):
			files = append(files, review.DiffFile{})
			current = &files[len(files)-1]
			if err := parseDiffGitLine(current, line); err != nil {
				return nil, err
			}
			currentHunk = nil
		case strings.HasPrefix(line, "new file mode "):
			if current == nil {
				continue
			}
			current.IsNew = true
		case strings.HasPrefix(line, "deleted file mode "):
			if current == nil {
				continue
			}
			current.IsDeleted = true
		case strings.HasPrefix(line, "--- "):
			headerAfterCompletedHunk = currentHunk != nil && !hunkHasRemaining(currentHunk, oldLine, newLine)
			if current == nil || len(current.Hunks) > 0 {
				files = append(files, review.DiffFile{})
				current = &files[len(files)-1]
				currentHunk = nil
			}
			headerState = diffHeaderNeedsNewPath
			oldPath, err := cleanDiffPath(strings.TrimPrefix(line, "--- "))
			if err != nil {
				return nil, fmt.Errorf("parse old path header: %w", err)
			}
			current.OldPath = oldPath
			current.IsNew = current.OldPath == ""
		case strings.HasPrefix(line, "+++ "):
			if current == nil {
				continue
			}
			newPath, err := cleanDiffPath(strings.TrimPrefix(line, "+++ "))
			if err != nil {
				return nil, fmt.Errorf("parse new path header: %w", err)
			}
			current.NewPath = newPath
			current.IsDeleted = current.NewPath == ""
			current.PackageDir = inferPackageDir(firstNonEmpty(current.NewPath, current.OldPath))
			if headerState == diffHeaderNeedsNewPath {
				if headerAfterCompletedHunk {
					headerState = diffHeaderNeedsHunk
				} else {
					headerState = diffHeaderComplete
				}
			}
		case strings.HasPrefix(line, "@@ "):
			if current == nil {
				continue
			}
			hunk, parsedOldLine, parsedNewLine, err := parseHunkHeader(line)
			if err != nil {
				return nil, err
			}
			current.Hunks = append(current.Hunks, hunk)
			currentHunk = &current.Hunks[len(current.Hunks)-1]
			oldLine = parsedOldLine
			newLine = parsedNewLine
			headerState = diffHeaderComplete
		case currentHunk != nil && hunkHasRemaining(currentHunk, oldLine, newLine):
			continue
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan diff: %w", err)
	}
	if currentHunk != nil && hunkHasRemaining(currentHunk, oldLine, newLine) {
		return nil, fmt.Errorf("hunk ended before declared ranges were consumed")
	}
	if headerState != diffHeaderComplete {
		return nil, fmt.Errorf("hunk contains content after declared ranges were consumed: incomplete diff file header")
	}
	if len(files) == 0 && strings.TrimSpace(diff) != "" {
		return nil, fmt.Errorf("no diff files found")
	}
	return files, nil
}

func shouldParseHunkLine(hunk *review.DiffHunk, oldLine int, newLine int, line string) bool {
	if hunk == nil || !isDiffHunkLine(line) {
		return false
	}
	if line == `\ No newline at end of file` {
		return true
	}
	if !hunkHasRemaining(hunk, oldLine, newLine) && strings.HasPrefix(line, "--- ") {
		return false
	}
	return true
}

func validateHunkLine(hunk *review.DiffHunk, oldLine int, newLine int, line string) error {
	if line == `\ No newline at end of file` {
		return nil
	}
	oldRemaining := lineHasRemaining(oldLine, hunk.OldStart, hunk.OldLines)
	newRemaining := lineHasRemaining(newLine, hunk.NewStart, hunk.NewLines)
	if !oldRemaining && !newRemaining {
		return fmt.Errorf("hunk contains content after declared ranges were consumed")
	}
	switch line[0] {
	case '+':
		if !newRemaining {
			return fmt.Errorf("hunk addition exceeds declared new range")
		}
	case '-':
		if !oldRemaining {
			return fmt.Errorf("hunk deletion exceeds declared old range")
		}
	case ' ':
		if !oldRemaining || !newRemaining {
			return fmt.Errorf("hunk context line exceeds declared range")
		}
	default:
		return fmt.Errorf("invalid hunk line %q", line)
	}
	return nil
}

func hunkHasRemaining(hunk *review.DiffHunk, oldLine int, newLine int) bool {
	return lineHasRemaining(oldLine, hunk.OldStart, hunk.OldLines) || lineHasRemaining(newLine, hunk.NewStart, hunk.NewLines)
}

func lineHasRemaining(line int, start int, count int) bool {
	if count <= 0 {
		return false
	}
	if line < start {
		return true
	}
	return line-start < count
}

func isDiffHunkLine(line string) bool {
	if line == `\ No newline at end of file` {
		return true
	}
	if line == "" {
		return false
	}
	switch line[0] {
	case ' ', '+', '-':
		return true
	default:
		return false
	}
}

func parseDiffGitLine(file *review.DiffFile, line string) error {
	parts, err := parseGitPathTokens(strings.TrimPrefix(line, "diff --git "))
	if err != nil {
		return fmt.Errorf("parse diff git paths: %w", err)
	}
	if len(parts) != 2 {
		return fmt.Errorf("parse diff git paths: expected exactly 2 paths, got %d", len(parts))
	}
	file.OldPath = normalizeDiffPath(parts[0])
	file.NewPath = normalizeDiffPath(parts[1])
	file.PackageDir = inferPackageDir(file.NewPath)
	return nil
}

func parseGitPathTokens(raw string) ([]string, error) {
	var paths []string
	for strings.TrimSpace(raw) != "" {
		path, rest, err := parseGitPathToken(raw)
		if err != nil {
			return nil, err
		}
		paths = append(paths, path)
		raw = rest
	}
	return paths, nil
}

func parseGitPathToken(raw string) (string, string, error) {
	raw = strings.TrimLeft(raw, " \t")
	if raw == "" {
		return "", "", fmt.Errorf("missing Git path token")
	}
	if raw[0] != '"' {
		end := strings.IndexAny(raw, " \t")
		if end < 0 {
			return raw, "", nil
		}
		return raw[:end], raw[end:], nil
	}

	var decoded strings.Builder
	for index := 1; index < len(raw); index++ {
		switch raw[index] {
		case '"':
			return decoded.String(), raw[index+1:], nil
		case '\\':
			value, next, err := decodeGitEscape(raw, index+1)
			if err != nil {
				return "", "", err
			}
			decoded.WriteByte(value)
			index = next - 1
		default:
			decoded.WriteByte(raw[index])
		}
	}
	return "", "", fmt.Errorf("unterminated quoted Git path")
}

func decodeGitEscape(raw string, index int) (byte, int, error) {
	if index >= len(raw) {
		return 0, 0, fmt.Errorf("unterminated Git path escape")
	}
	switch raw[index] {
	case 'a':
		return '\a', index + 1, nil
	case 'b':
		return '\b', index + 1, nil
	case 't':
		return '\t', index + 1, nil
	case 'n':
		return '\n', index + 1, nil
	case 'v':
		return '\v', index + 1, nil
	case 'f':
		return '\f', index + 1, nil
	case 'r':
		return '\r', index + 1, nil
	case '\\', '"':
		return raw[index], index + 1, nil
	}
	if raw[index] < '0' || raw[index] > '7' {
		return 0, 0, fmt.Errorf("unsupported Git path escape \\%c", raw[index])
	}
	value := byte(0)
	end := index
	for end < len(raw) && end < index+3 && raw[end] >= '0' && raw[end] <= '7' {
		value = value*8 + raw[end] - '0'
		end++
	}
	return value, end, nil
}

func parseHunkHeader(line string) (review.DiffHunk, int, int, error) {
	matches := hunkHeaderRE.FindStringSubmatch(line)
	if matches == nil {
		return review.DiffHunk{}, 0, 0, fmt.Errorf("invalid hunk header: %s", line)
	}
	oldStart, err := parseHunkNumber(matches[1], "old start")
	if err != nil {
		return review.DiffHunk{}, 0, 0, err
	}
	oldLines, err := parseOptionalCount(matches[2], "old line count")
	if err != nil {
		return review.DiffHunk{}, 0, 0, err
	}
	newStart, err := parseHunkNumber(matches[3], "new start")
	if err != nil {
		return review.DiffHunk{}, 0, 0, err
	}
	newLines, err := parseOptionalCount(matches[4], "new line count")
	if err != nil {
		return review.DiffHunk{}, 0, 0, err
	}
	if err := validateHunkRange(oldStart, oldLines, "old"); err != nil {
		return review.DiffHunk{}, 0, 0, err
	}
	if err := validateHunkRange(newStart, newLines, "new"); err != nil {
		return review.DiffHunk{}, 0, 0, err
	}
	return review.DiffHunk{
		OldStart: oldStart,
		OldLines: oldLines,
		NewStart: newStart,
		NewLines: newLines,
	}, oldStart, newStart, nil
}

func parseHunkNumber(raw string, name string) (int, error) {
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", name, raw, err)
	}
	return n, nil
}

func parseOptionalCount(raw string, name string) (int, error) {
	if raw == "" {
		return 1, nil
	}
	return parseHunkNumber(raw, name)
}

func validateHunkRange(start int, count int, name string) error {
	maxInt := int(^uint(0) >> 1)
	if count < 0 || start > maxInt-count {
		return fmt.Errorf("%s hunk range overflows int", name)
	}
	return nil
}

func parseDiffLine(line string, oldLine int, newLine int) (review.DiffLine, int, int) {
	if line == `\ No newline at end of file` {
		return review.DiffLine{Kind: "meta", Content: line}, oldLine, newLine
	}
	if line == "" {
		return review.DiffLine{Kind: "context", OldLine: oldLine, NewLine: newLine}, oldLine + 1, newLine + 1
	}
	content := line[1:]
	switch line[0] {
	case '+':
		return review.DiffLine{Kind: "add", NewLine: newLine, Content: content}, oldLine, newLine + 1
	case '-':
		return review.DiffLine{Kind: "delete", OldLine: oldLine, Content: content}, oldLine + 1, newLine
	default:
		if line[0] == ' ' {
			content = line[1:]
		} else {
			content = line
		}
		return review.DiffLine{Kind: "context", OldLine: oldLine, NewLine: newLine, Content: content}, oldLine + 1, newLine + 1
	}
}

func cleanDiffPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	decoded, _, err := parseGitPathToken(path)
	if err != nil {
		return "", fmt.Errorf("decode diff path: %w", err)
	}
	return normalizeDiffPath(decoded), nil
}

func normalizeDiffPath(path string) string {
	if path == "/dev/null" {
		return ""
	}
	path = strings.TrimPrefix(path, "a/")
	path = strings.TrimPrefix(path, "b/")
	return filepath.ToSlash(path)
}

func firstNonEmpty(first string, second string) string {
	if first != "" {
		return first
	}
	return second
}

func inferPackageDir(path string) string {
	if path == "" || !strings.HasSuffix(path, ".go") {
		return ""
	}
	dir := filepath.ToSlash(filepath.Dir(path))
	if dir == "." {
		return ""
	}
	return dir
}
