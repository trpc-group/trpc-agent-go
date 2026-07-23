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
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

const (
	diffLineContext = "context"
	diffLineAdded   = "added"
	diffLineDeleted = "deleted"
)

var hunkHeaderPattern = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

type parsedDiff struct {
	Files    []changedFile
	Warnings []parseWarning
}

type changedFile struct {
	OldPath     string
	NewPath     string
	IsNew       bool
	IsDeleted   bool
	IsRename    bool
	IsBinary    bool
	PackageName string
	Hunks       []diffHunk
}

type diffHunk struct {
	Header   string
	OldStart int
	OldCount int
	NewStart int
	NewCount int
	Lines    []diffLine
}

type diffLine struct {
	Kind           string
	Text           string
	OldLine        int
	NewLine        int
	NoNewlineAtEOF bool
}

type candidateLine struct {
	File      string
	Line      int
	Text      string
	FileIndex int
	HunkIndex int
}

type parseWarning struct {
	File    string
	Line    int
	Message string
}

func parseUnifiedDiff(raw []byte) parsedDiff {
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")

	parser := diffParser{
		currentFile: -1,
		currentHunk: -1,
	}
	for i, line := range lines {
		if i == len(lines)-1 && line == "" {
			break
		}
		parser.consumeLine(line, i+1)
	}

	derivePackageNames(&parser.parsed)
	return parser.parsed
}

type diffParser struct {
	parsed      parsedDiff
	currentFile int
	currentHunk int
	oldCursor   int
	newCursor   int
}

func (p *diffParser) consumeLine(line string, inputLine int) {
	if strings.HasPrefix(line, "diff --git ") {
		p.startFile(line, inputLine)
		return
	}
	if p.currentFile == -1 {
		p.warnBeforeFirstFile(line, inputLine)
		return
	}

	file := &p.parsed.Files[p.currentFile]
	if p.consumeFileMetadata(file, line, inputLine) {
		return
	}
	if p.currentHunk >= 0 {
		p.consumeHunkLine(file, line, inputLine)
		return
	}
	if file.IsBinary || isKnownDiffMetadata(line) || strings.TrimSpace(line) == "" {
		return
	}
	p.addWarning(file.reviewPath(), inputLine, "ignored content outside a hunk")
}

func (p *diffParser) startFile(line string, inputLine int) {
	oldPath, newPath, warning := parseGitDiffPaths(line)
	p.parsed.Files = append(p.parsed.Files, changedFile{
		OldPath: oldPath,
		NewPath: newPath,
	})
	p.currentFile = len(p.parsed.Files) - 1
	p.currentHunk = -1
	if warning != "" {
		p.addWarning(newPath, inputLine, warning)
	}
}

func (p *diffParser) warnBeforeFirstFile(line string, inputLine int) {
	if strings.TrimSpace(line) == "" {
		return
	}
	p.addWarning("", inputLine, "ignored content before first diff header")
}

func (p *diffParser) consumeFileMetadata(file *changedFile, line string, inputLine int) bool {
	switch {
	case strings.HasPrefix(line, "new file mode "):
		file.IsNew = true
	case strings.HasPrefix(line, "deleted file mode "):
		file.IsDeleted = true
	case strings.HasPrefix(line, "rename from "):
		file.IsRename = true
		file.OldPath = cleanDiffPath(strings.TrimPrefix(line, "rename from "))
	case strings.HasPrefix(line, "rename to "):
		file.IsRename = true
		file.NewPath = cleanDiffPath(strings.TrimPrefix(line, "rename to "))
	case strings.HasPrefix(line, "Binary files "), line == "GIT binary patch":
		file.IsBinary = true
		p.currentHunk = -1
	case strings.HasPrefix(line, "--- "):
		p.consumeOldPath(file, line)
	case strings.HasPrefix(line, "+++ "):
		p.consumeNewPath(file, line)
	case strings.HasPrefix(line, "@@"):
		p.consumeHunkHeader(file, line, inputLine)
	default:
		return false
	}
	return true
}

func (p *diffParser) consumeOldPath(file *changedFile, line string) {
	oldPath := cleanDiffMetadataPath(strings.TrimPrefix(line, "--- "))
	if oldPath == "/dev/null" {
		file.OldPath = ""
		file.IsNew = true
		return
	}
	file.OldPath = oldPath
}

func (p *diffParser) consumeNewPath(file *changedFile, line string) {
	newPath := cleanDiffMetadataPath(strings.TrimPrefix(line, "+++ "))
	if newPath == "/dev/null" {
		file.NewPath = ""
		file.IsDeleted = true
		return
	}
	file.NewPath = newPath
}

func (p *diffParser) consumeHunkHeader(file *changedFile, line string, inputLine int) {
	hunk, err := parseHunkHeader(line)
	if err != nil {
		p.currentHunk = -1
		p.addWarning(file.reviewPath(), inputLine, err.Error())
		return
	}
	file.Hunks = append(file.Hunks, hunk)
	p.currentHunk = len(file.Hunks) - 1
	p.oldCursor = hunk.OldStart
	p.newCursor = hunk.NewStart
}

func (p *diffParser) consumeHunkLine(file *changedFile, line string, inputLine int) {
	hunk := &file.Hunks[p.currentHunk]
	if line == `\ No newline at end of file` {
		p.markNoNewlineAtEOF(file, hunk, inputLine)
		return
	}
	if line == "" {
		p.addWarning(file.reviewPath(), inputLine, "empty hunk line missing a diff prefix")
		return
	}

	marker := line[0]
	content := line[1:]
	switch marker {
	case ' ':
		hunk.Lines = append(hunk.Lines, diffLine{
			Kind:    diffLineContext,
			Text:    content,
			OldLine: p.oldCursor,
			NewLine: p.newCursor,
		})
		p.oldCursor++
		p.newCursor++
	case '+':
		hunk.Lines = append(hunk.Lines, diffLine{
			Kind:    diffLineAdded,
			Text:    content,
			NewLine: p.newCursor,
		})
		p.newCursor++
	case '-':
		hunk.Lines = append(hunk.Lines, diffLine{
			Kind:    diffLineDeleted,
			Text:    content,
			OldLine: p.oldCursor,
		})
		p.oldCursor++
	default:
		p.addWarning(file.reviewPath(), inputLine, fmt.Sprintf("malformed hunk line with prefix %q", marker))
	}
}

func (p *diffParser) markNoNewlineAtEOF(file *changedFile, hunk *diffHunk, inputLine int) {
	if len(hunk.Lines) == 0 {
		p.addWarning(file.reviewPath(), inputLine, "no-newline marker without a previous hunk line")
		return
	}
	hunk.Lines[len(hunk.Lines)-1].NoNewlineAtEOF = true
}

func (p *diffParser) addWarning(file string, line int, message string) {
	p.parsed.Warnings = append(p.parsed.Warnings, parseWarning{
		File:    file,
		Line:    line,
		Message: message,
	})
}

func parseGitDiffPaths(line string) (string, string, string) {
	rest := strings.TrimPrefix(line, "diff --git ")
	oldPath, rest, err := parseGitPathToken(rest)
	if err != nil {
		return "", "", "malformed diff header"
	}
	newPath, rest, err := parseGitPathToken(rest)
	if err != nil || strings.TrimSpace(rest) != "" {
		return "", "", "malformed diff header"
	}
	return cleanDiffPath(oldPath), cleanDiffPath(newPath), ""
}

func cleanDiffPath(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, `"`) {
		decoded, rest, err := parseGitPathToken(value)
		if err == nil && strings.TrimSpace(rest) == "" {
			value = decoded
		}
	}
	switch {
	case value == "/dev/null":
		return value
	case strings.HasPrefix(value, "a/"), strings.HasPrefix(value, "b/"):
		return value[2:]
	default:
		return value
	}
}

func cleanDiffMetadataPath(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, `"`) {
		decoded, _, err := parseGitPathToken(value)
		if err == nil {
			return cleanDiffPath(decoded)
		}
	} else if tab := strings.IndexByte(value, '\t'); tab >= 0 {
		value = value[:tab]
	}
	return cleanDiffPath(value)
}

func parseGitPathToken(value string) (string, string, error) {
	value = strings.TrimLeftFunc(value, unicode.IsSpace)
	if value == "" {
		return "", "", fmt.Errorf("missing git path")
	}
	if value[0] != '"' {
		end := strings.IndexFunc(value, unicode.IsSpace)
		if end < 0 {
			return value, "", nil
		}
		return value[:end], value[end:], nil
	}

	escaped := false
	for i := 1; i < len(value); i++ {
		switch {
		case escaped:
			escaped = false
		case value[i] == '\\':
			escaped = true
		case value[i] == '"':
			decoded, err := strconv.Unquote(value[:i+1])
			if err != nil {
				return "", "", fmt.Errorf("decode git path: %w", err)
			}
			return decoded, value[i+1:], nil
		}
	}
	return "", "", fmt.Errorf("unterminated quoted git path")
}

func parseHunkHeader(line string) (diffHunk, error) {
	matches := hunkHeaderPattern.FindStringSubmatch(line)
	if matches == nil {
		return diffHunk{}, fmt.Errorf("malformed hunk header %q", line)
	}

	oldStart, _ := strconv.Atoi(matches[1])
	oldCount := 1
	if matches[2] != "" {
		oldCount, _ = strconv.Atoi(matches[2])
	}
	newStart, _ := strconv.Atoi(matches[3])
	newCount := 1
	if matches[4] != "" {
		newCount, _ = strconv.Atoi(matches[4])
	}
	return diffHunk{
		Header:   line,
		OldStart: oldStart,
		OldCount: oldCount,
		NewStart: newStart,
		NewCount: newCount,
	}, nil
}

func isKnownDiffMetadata(line string) bool {
	prefixes := []string{
		"index ",
		"old mode ",
		"new mode ",
		"similarity index ",
		"dissimilarity index ",
		"copy from ",
		"copy to ",
		"literal ",
		"delta ",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

func derivePackageNames(parsed *parsedDiff) {
	for fileIndex := range parsed.Files {
		file := &parsed.Files[fileIndex]
		if !file.isGoFile() || file.IsDeleted {
			continue
		}
		for _, hunk := range file.Hunks {
			for _, line := range hunk.Lines {
				if line.Kind != diffLineAdded && line.Kind != diffLineContext {
					continue
				}
				fields := strings.Fields(strings.TrimSpace(line.Text))
				if len(fields) >= 2 && fields[0] == "package" {
					file.PackageName = fields[1]
					break
				}
			}
			if file.PackageName != "" {
				break
			}
		}
	}
}

func (p parsedDiff) hunkCount() int {
	count := 0
	for _, file := range p.Files {
		count += len(file.Hunks)
	}
	return count
}

func (p parsedDiff) candidateLines() []candidateLine {
	var candidates []candidateLine
	for fileIndex, file := range p.Files {
		if file.IsDeleted || file.IsBinary {
			continue
		}
		filePath := file.reviewPath()
		for hunkIndex, hunk := range file.Hunks {
			for _, line := range hunk.Lines {
				if line.Kind != diffLineAdded || strings.TrimSpace(line.Text) == "" {
					continue
				}
				candidates = append(candidates, candidateLine{
					File:      filePath,
					Line:      line.NewLine,
					Text:      line.Text,
					FileIndex: fileIndex,
					HunkIndex: hunkIndex,
				})
			}
		}
	}
	return candidates
}

func (f changedFile) reviewPath() string {
	if f.NewPath != "" && !f.IsDeleted {
		return f.NewPath
	}
	return f.OldPath
}

func (f changedFile) isGoFile() bool {
	path := f.reviewPath()
	return strings.HasSuffix(path, ".go")
}
