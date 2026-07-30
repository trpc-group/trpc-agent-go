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

	headerLine         int
	hasNewFileMode     bool
	hasDeletedFileMode bool
	hasRenameFrom      bool
	hasRenameTo        bool
	hasOldMarker       bool
	hasNewMarker       bool
	oldIsDevNull       bool
	newIsDevNull       bool
}

type diffHunk struct {
	Header    string
	OldStart  int
	OldCount  int
	NewStart  int
	NewCount  int
	Lines     []diffLine
	inputLine int
}

type diffLine struct {
	Kind           string
	Text           string
	OldLine        int
	NewLine        int
	NoNewlineAtEOF bool
}

type candidateLine struct {
	File          string
	Line          int
	Text          string
	FileIndex     int
	HunkIndex     int
	HunkLineIndex int
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
	parser.finalizeCurrentFile()

	derivePackageNames(&parser.parsed)
	return parser.parsed
}

type diffParser struct {
	parsed      parsedDiff
	currentFile int
	currentHunk int
	oldCursor   int
	newCursor   int
	oldConsumed int
	newConsumed int
	hunkLine    int
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
	if p.currentHunk >= 0 && !strings.HasPrefix(line, "@@") {
		p.consumeHunkLine(file, line, inputLine)
		return
	}
	if p.consumeFileMetadata(file, line, inputLine) {
		return
	}
	if file.IsBinary || isKnownDiffMetadata(line) || strings.TrimSpace(line) == "" {
		return
	}
	p.addWarning(file.reviewPath(), inputLine, "ignored content outside a hunk")
}

func (p *diffParser) startFile(line string, inputLine int) {
	p.finalizeCurrentFile()
	oldPath, newPath, warning := parseGitDiffPaths(line)
	p.parsed.Files = append(p.parsed.Files, changedFile{
		OldPath:    oldPath,
		NewPath:    newPath,
		headerLine: inputLine,
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
		file.hasNewFileMode = true
	case strings.HasPrefix(line, "deleted file mode "):
		file.IsDeleted = true
		file.hasDeletedFileMode = true
	case strings.HasPrefix(line, "rename from "):
		file.IsRename = true
		file.hasRenameFrom = true
		file.OldPath = cleanDiffPath(strings.TrimPrefix(line, "rename from "))
	case strings.HasPrefix(line, "rename to "):
		file.IsRename = true
		file.hasRenameTo = true
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
	file.hasOldMarker = true
	oldPath := cleanDiffMetadataPath(strings.TrimPrefix(line, "--- "))
	if oldPath == "/dev/null" {
		file.OldPath = ""
		file.IsNew = true
		file.oldIsDevNull = true
		return
	}
	file.oldIsDevNull = false
	file.OldPath = oldPath
}

func (p *diffParser) consumeNewPath(file *changedFile, line string) {
	file.hasNewMarker = true
	newPath := cleanDiffMetadataPath(strings.TrimPrefix(line, "+++ "))
	if newPath == "/dev/null" {
		file.NewPath = ""
		file.IsDeleted = true
		file.newIsDevNull = true
		return
	}
	file.newIsDevNull = false
	file.NewPath = newPath
}

func (p *diffParser) consumeHunkHeader(file *changedFile, line string, inputLine int) {
	p.finalizeCurrentHunk()
	hunk, err := parseHunkHeader(line)
	if err != nil {
		p.currentHunk = -1
		p.addWarning(file.reviewPath(), inputLine, err.Error())
		return
	}
	if err := validateHunkOrder(file.Hunks, hunk); err != nil {
		p.currentHunk = -1
		p.addWarning(file.reviewPath(), inputLine, err.Error())
		return
	}
	hunk.inputLine = inputLine
	file.Hunks = append(file.Hunks, hunk)
	p.currentHunk = len(file.Hunks) - 1
	p.oldCursor = hunk.OldStart
	p.newCursor = hunk.NewStart
	p.oldConsumed = 0
	p.newConsumed = 0
	p.hunkLine = inputLine
}

func (p *diffParser) finalizeCurrentFile() {
	if p.currentFile < 0 {
		return
	}
	p.finalizeCurrentHunk()
	p.validateCurrentFileStatus()
}

func (p *diffParser) validateCurrentFileStatus() {
	file := &p.parsed.Files[p.currentFile]
	warn := func(message string) {
		p.addWarning(file.reviewPath(), file.headerLine, message)
	}
	if file.hasNewFileMode && file.hasDeletedFileMode {
		warn("file cannot be both new and deleted")
	}
	if file.IsNew && file.IsDeleted {
		warn("file status is both new and deleted")
	}
	if file.IsRename && (file.IsNew || file.IsDeleted) {
		warn("rename metadata conflicts with new or deleted file status")
	}
	if file.hasRenameFrom != file.hasRenameTo {
		warn("rename metadata must include both rename from and rename to")
	}

	if file.IsNew {
		if !file.hasNewFileMode {
			warn("new file is missing new file mode metadata")
		}
		if file.hasOldMarker && !file.oldIsDevNull {
			warn("new file old path must be /dev/null")
		}
		if file.hasNewMarker && file.newIsDevNull {
			warn("new file new path must not be /dev/null")
		}
		if !file.IsBinary && (!file.hasOldMarker || !file.oldIsDevNull) {
			warn("text new file must declare --- /dev/null")
		}
		if !file.IsBinary && (!file.hasNewMarker || file.newIsDevNull) {
			warn("text new file must declare a non-null +++ path")
		}
		for _, hunk := range file.Hunks {
			if hunk.OldStart != 0 || hunk.OldCount != 0 {
				p.addWarning(file.reviewPath(), hunk.inputLine, "new file old hunk range must be 0,0")
			}
		}
	}

	if file.IsDeleted {
		if !file.hasDeletedFileMode {
			warn("deleted file is missing deleted file mode metadata")
		}
		if file.hasNewMarker && !file.newIsDevNull {
			warn("deleted file new path must be /dev/null")
		}
		if file.hasOldMarker && file.oldIsDevNull {
			warn("deleted file old path must not be /dev/null")
		}
		if !file.IsBinary && (!file.hasNewMarker || !file.newIsDevNull) {
			warn("text deleted file must declare +++ /dev/null")
		}
		if !file.IsBinary && (!file.hasOldMarker || file.oldIsDevNull) {
			warn("text deleted file must declare a non-null --- path")
		}
		for _, hunk := range file.Hunks {
			if hunk.NewStart != 0 || hunk.NewCount != 0 {
				p.addWarning(file.reviewPath(), hunk.inputLine, "deleted file new hunk range must be 0,0")
			}
		}
	}

	if !file.IsNew && !file.IsDeleted {
		if file.oldIsDevNull || file.newIsDevNull {
			warn("regular file paths must not use /dev/null")
		}
		if file.hasNewFileMode || file.hasDeletedFileMode {
			warn("file mode metadata does not match the file status")
		}
	}
}

func (p *diffParser) finalizeCurrentHunk() {
	if p.currentFile < 0 || p.currentHunk < 0 {
		return
	}
	file := &p.parsed.Files[p.currentFile]
	hunk := &file.Hunks[p.currentHunk]
	oldActual := p.oldConsumed
	newActual := p.newConsumed
	if oldActual != hunk.OldCount || newActual != hunk.NewCount {
		p.addWarning(
			file.reviewPath(),
			p.hunkLine,
			fmt.Sprintf(
				"hunk line count mismatch: declared old=%d new=%d, consumed old=%d new=%d",
				hunk.OldCount,
				hunk.NewCount,
				oldActual,
				newActual,
			),
		)
	}
	p.currentHunk = -1
	p.oldConsumed = 0
	p.newConsumed = 0
	p.hunkLine = 0
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
		if p.oldCursor == maxIntValue() || p.newCursor == maxIntValue() {
			p.addWarning(file.reviewPath(), inputLine, "hunk line number overflow")
			p.currentHunk = -1
			return
		}
		hunk.Lines = append(hunk.Lines, diffLine{
			Kind:    diffLineContext,
			Text:    content,
			OldLine: p.oldCursor,
			NewLine: p.newCursor,
		})
		p.oldCursor++
		p.newCursor++
		p.oldConsumed++
		p.newConsumed++
	case '+':
		if p.newCursor == maxIntValue() {
			p.addWarning(file.reviewPath(), inputLine, "new hunk line number overflow")
			p.currentHunk = -1
			return
		}
		hunk.Lines = append(hunk.Lines, diffLine{
			Kind:    diffLineAdded,
			Text:    content,
			NewLine: p.newCursor,
		})
		p.newCursor++
		p.newConsumed++
	case '-':
		if p.oldCursor == maxIntValue() {
			p.addWarning(file.reviewPath(), inputLine, "old hunk line number overflow")
			p.currentHunk = -1
			return
		}
		hunk.Lines = append(hunk.Lines, diffLine{
			Kind:    diffLineDeleted,
			Text:    content,
			OldLine: p.oldCursor,
		})
		p.oldCursor++
		p.oldConsumed++
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

	oldStart, err := parseHunkNumber(matches[1], 0, "old start", line)
	if err != nil {
		return diffHunk{}, err
	}
	oldCount := 1
	if matches[2] != "" {
		oldCount, err = parseHunkNumber(matches[2], 1, "old count", line)
		if err != nil {
			return diffHunk{}, err
		}
	}
	newStart, err := parseHunkNumber(matches[3], 0, "new start", line)
	if err != nil {
		return diffHunk{}, err
	}
	newCount := 1
	if matches[4] != "" {
		newCount, err = parseHunkNumber(matches[4], 1, "new count", line)
		if err != nil {
			return diffHunk{}, err
		}
	}
	if err := validateHunkRange(oldStart, oldCount, "old", line); err != nil {
		return diffHunk{}, err
	}
	if err := validateHunkRange(newStart, newCount, "new", line); err != nil {
		return diffHunk{}, err
	}
	return diffHunk{
		Header:   line,
		OldStart: oldStart,
		OldCount: oldCount,
		NewStart: newStart,
		NewCount: newCount,
	}, nil
}

func validateHunkRange(start int, count int, side string, header string) error {
	if start < 0 || count < 0 {
		return fmt.Errorf("invalid %s hunk range in %q", side, header)
	}
	if start == 0 && count != 0 {
		return fmt.Errorf("%s hunk range starting at 0 must be empty in %q", side, header)
	}
	if count > 0 && start < 1 {
		return fmt.Errorf("non-empty %s hunk range must start at line 1 or later in %q", side, header)
	}
	if count > maxIntValue()-start {
		return fmt.Errorf("%s hunk range overflows line numbers in %q", side, header)
	}
	return nil
}

func validateHunkOrder(previous []diffHunk, current diffHunk) error {
	if len(previous) == 0 {
		return nil
	}
	last := previous[len(previous)-1]
	if err := validateOrderedRange(last.OldStart, last.OldCount, current.OldStart, current.OldCount, "old"); err != nil {
		return err
	}
	return validateOrderedRange(last.NewStart, last.NewCount, current.NewStart, current.NewCount, "new")
}

func validateOrderedRange(
	previousStart int,
	previousCount int,
	currentStart int,
	currentCount int,
	side string,
) error {
	previousEnd := previousStart + previousCount
	if currentStart < previousStart {
		return fmt.Errorf("%s hunk ranges are not ordered", side)
	}
	if currentStart < previousEnd {
		return fmt.Errorf("%s hunk ranges overlap", side)
	}
	if currentStart == previousStart && (previousCount == 0 || currentCount == 0) {
		return fmt.Errorf("%s hunk ranges reuse a zero-length anchor", side)
	}
	return nil
}

func maxIntValue() int {
	return int(^uint(0) >> 1)
}

func parseHunkNumber(value string, defaultValue int, label string, header string) (int, error) {
	if value == "" {
		return defaultValue, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("malformed hunk header %q: invalid %s", header, label)
	}
	return parsed, nil
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
			for lineIndex, line := range hunk.Lines {
				if line.Kind != diffLineAdded || strings.TrimSpace(line.Text) == "" {
					continue
				}
				candidates = append(candidates, candidateLine{
					File:          filePath,
					Line:          line.NewLine,
					Text:          line.Text,
					FileIndex:     fileIndex,
					HunkIndex:     hunkIndex,
					HunkLineIndex: lineIndex,
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
