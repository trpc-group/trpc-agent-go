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
	var files []review.DiffFile
	var current *review.DiffFile
	var currentHunk *review.DiffHunk
	oldLine := 0
	newLine := 0

	scanner := bufio.NewScanner(strings.NewReader(diff))
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if shouldParseHunkLine(currentHunk, oldLine, newLine, line) {
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
			if current == nil || len(current.Hunks) > 0 {
				files = append(files, review.DiffFile{})
				current = &files[len(files)-1]
				currentHunk = nil
			}
			current.OldPath = cleanDiffPath(strings.TrimPrefix(line, "--- "))
			current.IsNew = current.OldPath == ""
		case strings.HasPrefix(line, "+++ "):
			if current == nil {
				continue
			}
			current.NewPath = cleanDiffPath(strings.TrimPrefix(line, "+++ "))
			current.IsDeleted = current.NewPath == ""
			current.PackageDir = inferPackageDir(firstNonEmpty(current.NewPath, current.OldPath))
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
		case currentHunk != nil && hunkHasRemaining(currentHunk, oldLine, newLine):
			continue
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan diff: %w", err)
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
	return hunkHasRemaining(hunk, oldLine, newLine)
}

func hunkHasRemaining(hunk *review.DiffHunk, oldLine int, newLine int) bool {
	return oldLine < hunk.OldStart+hunk.OldLines || newLine < hunk.NewStart+hunk.NewLines
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
	if len(parts) >= 2 {
		file.OldPath = normalizeDiffPath(parts[0])
		file.NewPath = normalizeDiffPath(parts[1])
		file.PackageDir = inferPackageDir(file.NewPath)
	}
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
	oldStart, _ := strconv.Atoi(matches[1])
	oldLines := parseOptionalCount(matches[2])
	newStart, _ := strconv.Atoi(matches[3])
	newLines := parseOptionalCount(matches[4])
	return review.DiffHunk{
		OldStart: oldStart,
		OldLines: oldLines,
		NewStart: newStart,
		NewLines: newLines,
	}, oldStart, newStart, nil
}

func parseOptionalCount(raw string) int {
	if raw == "" {
		return 1
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return n
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

func cleanDiffPath(path string) string {
	path = strings.TrimSpace(path)
	if decoded, _, err := parseGitPathToken(path); err == nil {
		path = decoded
	}
	return normalizeDiffPath(path)
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
