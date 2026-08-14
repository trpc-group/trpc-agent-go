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
			flushHunk()
			if current != nil {
				parsed.Files = append(parsed.Files, *current)
			}
			current = &ParsedFile{}
			hasTargetFileHeader = false
		case currentHunk == nil && strings.HasPrefix(line, "--- "):
			if current != nil && hasTargetFileHeader {
				parsed.Files = append(parsed.Files, *current)
				current = &ParsedFile{}
				hasTargetFileHeader = false
			}
			if current == nil {
				current = &ParsedFile{}
			}
		case currentHunk == nil && strings.HasPrefix(line, "+++ "):
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
		case currentHunk != nil:
			switch {
			case line == `\ No newline at end of file`:
				// This is diff metadata, not a changed source line.
			case strings.HasPrefix(line, "+"):
				currentHunk.Lines = append(currentHunk.Lines, Line{NewLine: newLine, Kind: "add", Text: strings.TrimPrefix(line, "+")})
				currentHunk.CandidateLines = append(currentHunk.CandidateLines, newLine)
				newLine++
				newRemaining--
			case strings.HasPrefix(line, "-"):
				currentHunk.Lines = append(currentHunk.Lines, Line{OldLine: oldLine, Kind: "del", Text: strings.TrimPrefix(line, "-")})
				oldLine++
				oldRemaining--
			default:
				currentHunk.Lines = append(currentHunk.Lines, Line{OldLine: oldLine, NewLine: newLine, Kind: "context", Text: line})
				currentHunk.Context = append(currentHunk.Context, line)
				oldLine++
				newLine++
				oldRemaining--
				newRemaining--
			}
			if oldRemaining <= 0 && newRemaining <= 0 {
				flushHunk()
			}
		}
		if err == io.EOF {
			break
		}
	}
	flushHunk()
	if current != nil {
		parsed.Files = append(parsed.Files, *current)
	}
	return parsed, nil
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
