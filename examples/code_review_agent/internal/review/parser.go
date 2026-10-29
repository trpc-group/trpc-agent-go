//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package review

import (
	"bufio"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var hunkHeader = regexp.MustCompile(`^@@ -(\d+),?(\d*) \+(\d+),?(\d*) @@`)

// ParseUnifiedDiff 解析 unified diff。
func ParseUnifiedDiff(input string) (ParsedDiff, error) {
	var parsed ParsedDiff
	reader := bufio.NewReader(strings.NewReader(input))

	var current *ParsedFile
	var currentHunk *Hunk
	hasTargetFileHeader := false
	oldLine := 0
	newLine := 0
	oldRemaining := 0
	newRemaining := 0
	sawDiffStructure := false

	flushHunk := func() {
		// 切换文件或 hunk 前先保存当前 hunk。
		if current == nil || currentHunk == nil {
			return
		}
		current.Hunks = append(current.Hunks, *currentHunk)
		currentHunk = nil
	}

	for {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return ParsedDiff{}, err
		}
		if len(line) == 0 && err == io.EOF {
			break
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		switch {
		case currentHunk == nil && strings.HasPrefix(line, "diff --git "):
			sawDiffStructure = true
			flushHunk()
			if current != nil {
				parsed.Files = append(parsed.Files, *current)
			}
			current = &ParsedFile{}
			hasTargetFileHeader = false
		case currentHunk == nil && strings.HasPrefix(line, "--- "):
			sawDiffStructure = true
			if current != nil && hasTargetFileHeader {
				parsed.Files = append(parsed.Files, *current)
				current = &ParsedFile{}
				hasTargetFileHeader = false
			}
			if current == nil {
				current = &ParsedFile{}
			}
		case currentHunk == nil && strings.HasPrefix(line, "+++ "):
			sawDiffStructure = true
			if current == nil {
				current = &ParsedFile{}
			}
			hasTargetFileHeader = true
			path := normalizeDiffPath(strings.TrimPrefix(line, "+++ "))
			if path != "/dev/null" {
				current.Path = filepath.ToSlash(path)
				current.Language = languageForPath(path)
				current.IsTestFile = strings.HasSuffix(path, "_test.go")
			}
		case currentHunk == nil && strings.HasPrefix(line, "@@ "):
			sawDiffStructure = true
			flushHunk()
			if current == nil || !hasTargetFileHeader {
				return ParsedDiff{}, fmt.Errorf("hunk header without target file header: %q", line)
			}
			m := hunkHeader.FindStringSubmatch(line)
			if len(m) != 5 {
				return ParsedDiff{}, fmt.Errorf("invalid hunk header: %q", line)
			}
			oldLine, _ = strconv.Atoi(m[1])
			newLine, _ = strconv.Atoi(m[3])
			oldRemaining = hunkLineCount(m[2])
			newRemaining = hunkLineCount(m[4])
			currentHunk = &Hunk{
				File:     current.Path,
				OldStart: oldLine,
				OldLines: hunkLineCount(m[2]),
				NewStart: newLine,
				NewLines: hunkLineCount(m[4]),
			}
			if oldRemaining == 0 && newRemaining == 0 {
				flushHunk()
			}
		case currentHunk != nil:
			switch {
			case line == `\ No newline at end of file`:
				// This is diff metadata, not a changed source line.
			case strings.HasPrefix(line, "+") && newRemaining > 0:
				currentHunk.Lines = append(currentHunk.Lines, Line{NewLine: newLine, Kind: "add", Text: strings.TrimPrefix(line, "+")})
				currentHunk.CandidateLines = append(currentHunk.CandidateLines, newLine)
				newLine++
				newRemaining--
			case strings.HasPrefix(line, "-") && oldRemaining > 0:
				currentHunk.Lines = append(currentHunk.Lines, Line{OldLine: oldLine, Kind: "del", Text: strings.TrimPrefix(line, "-")})
				oldLine++
				oldRemaining--
			case strings.HasPrefix(line, " ") && oldRemaining > 0 && newRemaining > 0:
				currentHunk.Lines = append(currentHunk.Lines, Line{OldLine: oldLine, NewLine: newLine, Kind: "context", Text: line})
				currentHunk.Context = append(currentHunk.Context, line)
				oldLine++
				newLine++
				oldRemaining--
				newRemaining--
			default:
				return ParsedDiff{}, fmt.Errorf("invalid or excess hunk line: %q", line)
			}
			if oldRemaining == 0 && newRemaining == 0 {
				flushHunk()
			}
		case strings.TrimSpace(line) != "" && !isUnifiedDiffMetadata(line):
			return ParsedDiff{}, fmt.Errorf("invalid unified diff line: %q", line)
		}
		if err == io.EOF {
			break
		}
	}
	if currentHunk != nil && (oldRemaining != 0 || newRemaining != 0) {
		return ParsedDiff{}, fmt.Errorf(
			"incomplete hunk: %d old and %d new lines remain",
			oldRemaining, newRemaining,
		)
	}
	flushHunk()
	if current != nil {
		parsed.Files = append(parsed.Files, *current)
	}
	if strings.TrimSpace(input) != "" && !sawDiffStructure {
		return ParsedDiff{}, fmt.Errorf("input is not a unified diff")
	}
	return parsed, nil
}

func isUnifiedDiffMetadata(line string) bool {
	return line == `\ No newline at end of file` ||
		strings.HasPrefix(line, "index ") ||
		strings.HasPrefix(line, "new file mode ") ||
		strings.HasPrefix(line, "deleted file mode ") ||
		strings.HasPrefix(line, "old mode ") ||
		strings.HasPrefix(line, "new mode ") ||
		strings.HasPrefix(line, "similarity index ") ||
		strings.HasPrefix(line, "rename from ") ||
		strings.HasPrefix(line, "rename to ") ||
		strings.HasPrefix(line, "Binary files ") ||
		strings.HasPrefix(line, "GIT binary patch") ||
		strings.HasPrefix(line, "literal ") ||
		strings.HasPrefix(line, "delta ")
}

func hunkLineCount(raw string) int {
	if raw == "" {
		return 1
	}
	count, _ := strconv.Atoi(raw)
	return count
}

func normalizeDiffPath(raw string) string {
	path := strings.TrimSpace(raw)
	if len(path) >= 2 && path[0] == '"' && path[len(path)-1] == '"' {
		if unquoted, err := strconv.Unquote(path); err == nil {
			path = unquoted
		}
	} else if idx := strings.IndexByte(path, '\t'); idx >= 0 {
		path = strings.TrimSpace(path[:idx])
	}
	return NormalizeDiffPath(path)
}

// NormalizeDiffPath removes exactly one synthetic unified-diff side prefix.
// A repository path may itself begin with a/ or b/, so these prefixes must not
// be stripped sequentially.
func NormalizeDiffPath(path string) string {
	if strings.HasPrefix(path, "a/") || strings.HasPrefix(path, "b/") {
		path = path[2:]
	}
	return filepath.ToSlash(path)
}

func languageForPath(path string) string {
	switch ext := strings.ToLower(filepath.Ext(path)); ext {
	case ".go":
		return "go"
	case ".md", ".markdown":
		return "markdown"
	case "":
		return ""
	default:
		return strings.TrimPrefix(ext, ".")
	}
}
