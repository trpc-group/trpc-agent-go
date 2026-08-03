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
	pathpkg "path"
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

	headerLine           int
	headerOldPath        string
	headerNewPath        string
	hasNewFileMode       bool
	newFileMode          string
	newFileModeValid     bool
	hasDeletedFileMode   bool
	deletedFileMode      string
	deletedFileModeValid bool
	hasOldMode           bool
	oldMode              string
	oldModeValid         bool
	hasNewMode           bool
	newMode              string
	newModeValid         bool
	hasSimilarityIndex   bool
	similarityIndex      int
	similarityValid      bool
	hasRenameFrom        bool
	hasRenameTo          bool
	renameFromPath       string
	renameToPath         string
	renameFromValid      bool
	renameToValid        bool
	isCopy               bool
	hasCopyFrom          bool
	hasCopyTo            bool
	copyFromPath         string
	copyToPath           string
	copyFromValid        bool
	copyToValid          bool
	binaryLine           int
	hasOldMarker         bool
	hasNewMarker         bool
	sawHunkHeader        bool
	oldIsDevNull         bool
	newIsDevNull         bool
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
		OldPath:       oldPath,
		NewPath:       newPath,
		headerLine:    inputLine,
		headerOldPath: oldPath,
		headerNewPath: newPath,
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
		p.consumeModeMetadata(
			file,
			strings.TrimPrefix(line, "new file mode "),
			inputLine,
			"new file mode",
			&file.hasNewFileMode,
			&file.newFileMode,
			&file.newFileModeValid,
		)
	case strings.HasPrefix(line, "deleted file mode "):
		file.IsDeleted = true
		p.consumeModeMetadata(
			file,
			strings.TrimPrefix(line, "deleted file mode "),
			inputLine,
			"deleted file mode",
			&file.hasDeletedFileMode,
			&file.deletedFileMode,
			&file.deletedFileModeValid,
		)
	case strings.HasPrefix(line, "old mode "):
		p.consumeModeMetadata(
			file,
			strings.TrimPrefix(line, "old mode "),
			inputLine,
			"old mode",
			&file.hasOldMode,
			&file.oldMode,
			&file.oldModeValid,
		)
	case strings.HasPrefix(line, "new mode "):
		p.consumeModeMetadata(
			file,
			strings.TrimPrefix(line, "new mode "),
			inputLine,
			"new mode",
			&file.hasNewMode,
			&file.newMode,
			&file.newModeValid,
		)
	case strings.HasPrefix(line, "similarity index "):
		p.consumeSimilarityIndex(file, line, inputLine)
	case strings.HasPrefix(line, "rename from "):
		p.consumeRenameFrom(file, line, inputLine)
	case strings.HasPrefix(line, "rename to "):
		p.consumeRenameTo(file, line, inputLine)
	case strings.HasPrefix(line, "copy from "):
		p.consumeCopyFrom(file, line, inputLine)
	case strings.HasPrefix(line, "copy to "):
		p.consumeCopyTo(file, line, inputLine)
	case strings.HasPrefix(line, "Binary files "), line == "GIT binary patch":
		file.IsBinary = true
		if file.binaryLine == 0 {
			file.binaryLine = inputLine
		}
		p.currentHunk = -1
	case strings.HasPrefix(line, "--- "):
		p.consumeOldPath(file, line, inputLine)
	case strings.HasPrefix(line, "+++ "):
		p.consumeNewPath(file, line, inputLine)
	case strings.HasPrefix(line, "@@"):
		p.consumeHunkHeader(file, line, inputLine)
	default:
		return false
	}
	return true
}

func (p *diffParser) consumeModeMetadata(
	file *changedFile,
	value string,
	inputLine int,
	label string,
	seen *bool,
	stored *string,
	valid *bool,
) {
	value = strings.TrimSpace(value)
	if *seen {
		message := "duplicate " + label + " metadata"
		if *stored != value {
			message = "conflicting " + label + " metadata"
		}
		p.addWarning(file.reviewPath(), inputLine, message)
		return
	}
	*seen = true
	*stored = value
	*valid = validDiffFileMode(value)
	if !*valid {
		p.addWarning(file.reviewPath(), inputLine, "malformed "+label+" metadata")
	}
}

func validDiffFileMode(value string) bool {
	if len(value) != 6 {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '7' {
			return false
		}
	}
	return true
}

func (p *diffParser) consumeSimilarityIndex(file *changedFile, line string, inputLine int) {
	value := strings.TrimSpace(strings.TrimPrefix(line, "similarity index "))
	if file.hasSimilarityIndex {
		p.addWarning(file.reviewPath(), inputLine, "duplicate similarity index metadata")
		return
	}
	file.hasSimilarityIndex = true
	if !strings.HasSuffix(value, "%") {
		p.addWarning(file.reviewPath(), inputLine, "malformed similarity index metadata")
		return
	}
	parsed, err := strconv.Atoi(strings.TrimSuffix(value, "%"))
	if err != nil || parsed < 0 || parsed > 100 {
		p.addWarning(file.reviewPath(), inputLine, "malformed similarity index metadata")
		return
	}
	file.similarityIndex = parsed
	file.similarityValid = true
}

func (p *diffParser) consumeOldPath(file *changedFile, line string, inputLine int) {
	file.hasOldMarker = true
	file.OldPath = file.headerOldPath
	oldPath, err := parseDiffMarkerPath(strings.TrimPrefix(line, "--- "), 'a')
	if err != nil {
		file.oldIsDevNull = false
		p.addWarning(file.reviewPath(), inputLine, "malformed old path metadata")
		return
	}
	if oldPath == "/dev/null" {
		file.OldPath = ""
		file.IsNew = true
		file.oldIsDevNull = true
		return
	}
	file.oldIsDevNull = false
	if oldPath != file.headerOldPath {
		p.addWarning(file.reviewPath(), inputLine, "old path metadata does not match diff header")
	}
}

func (p *diffParser) consumeNewPath(file *changedFile, line string, inputLine int) {
	file.hasNewMarker = true
	file.NewPath = file.headerNewPath
	newPath, err := parseDiffMarkerPath(strings.TrimPrefix(line, "+++ "), 'b')
	if err != nil {
		file.newIsDevNull = false
		p.addWarning(file.reviewPath(), inputLine, "malformed new path metadata")
		return
	}
	if newPath == "/dev/null" {
		file.NewPath = ""
		file.IsDeleted = true
		file.newIsDevNull = true
		return
	}
	file.newIsDevNull = false
	if newPath != file.headerNewPath {
		p.addWarning(file.reviewPath(), inputLine, "new path metadata does not match diff header")
	}
}

func (p *diffParser) consumeRenameFrom(file *changedFile, line string, inputLine int) {
	file.IsRename = true
	if file.hasRenameFrom {
		p.addWarning(file.reviewPath(), inputLine, "duplicate rename from metadata")
		return
	}
	file.hasRenameFrom = true
	renamePath, err := parseRenamePath(strings.TrimPrefix(line, "rename from "))
	if err != nil {
		p.addWarning(file.reviewPath(), inputLine, "malformed rename from path")
		return
	}
	file.renameFromPath = renamePath
	file.renameFromValid = true
	if renamePath != file.headerOldPath {
		p.addWarning(file.reviewPath(), inputLine, "rename from path does not match diff header")
	}
	p.validateRenamePathPair(file, inputLine)
}

func (p *diffParser) consumeRenameTo(file *changedFile, line string, inputLine int) {
	file.IsRename = true
	if file.hasRenameTo {
		p.addWarning(file.reviewPath(), inputLine, "duplicate rename to metadata")
		return
	}
	file.hasRenameTo = true
	renamePath, err := parseRenamePath(strings.TrimPrefix(line, "rename to "))
	if err != nil {
		p.addWarning(file.reviewPath(), inputLine, "malformed rename to path")
		return
	}
	file.renameToPath = renamePath
	file.renameToValid = true
	if renamePath != file.headerNewPath {
		p.addWarning(file.reviewPath(), inputLine, "rename to path does not match diff header")
	}
	p.validateRenamePathPair(file, inputLine)
}

func (p *diffParser) validateRenamePathPair(file *changedFile, inputLine int) {
	if file.renameFromValid && file.renameToValid && file.renameFromPath == file.renameToPath {
		p.addWarning(file.reviewPath(), inputLine, "rename paths must be different")
	}
}

func (p *diffParser) consumeCopyFrom(file *changedFile, line string, inputLine int) {
	file.isCopy = true
	if file.hasCopyFrom {
		p.addWarning(file.reviewPath(), inputLine, "duplicate copy from metadata")
		return
	}
	file.hasCopyFrom = true
	copyPath, err := parseRenamePath(strings.TrimPrefix(line, "copy from "))
	if err != nil {
		p.addWarning(file.reviewPath(), inputLine, "malformed copy from path")
		return
	}
	file.copyFromPath = copyPath
	file.copyFromValid = true
	if copyPath != file.headerOldPath {
		p.addWarning(file.reviewPath(), inputLine, "copy from path does not match diff header")
	}
	p.validateCopyPathPair(file, inputLine)
}

func (p *diffParser) consumeCopyTo(file *changedFile, line string, inputLine int) {
	file.isCopy = true
	if file.hasCopyTo {
		p.addWarning(file.reviewPath(), inputLine, "duplicate copy to metadata")
		return
	}
	file.hasCopyTo = true
	copyPath, err := parseRenamePath(strings.TrimPrefix(line, "copy to "))
	if err != nil {
		p.addWarning(file.reviewPath(), inputLine, "malformed copy to path")
		return
	}
	file.copyToPath = copyPath
	file.copyToValid = true
	if copyPath != file.headerNewPath {
		p.addWarning(file.reviewPath(), inputLine, "copy to path does not match diff header")
	}
	p.validateCopyPathPair(file, inputLine)
}

func (p *diffParser) validateCopyPathPair(file *changedFile, inputLine int) {
	if file.copyFromValid && file.copyToValid && file.copyFromPath == file.copyToPath {
		p.addWarning(file.reviewPath(), inputLine, "copy paths must be different")
	}
}

func (p *diffParser) consumeHunkHeader(file *changedFile, line string, inputLine int) {
	p.finalizeCurrentHunk()
	file.sawHunkHeader = true
	if file.IsBinary {
		file.IsBinary = false
		p.addWarning(file.reviewPath(), inputLine, "binary metadata conflicts with text hunk")
	}
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
	if file.hasCopyFrom != file.hasCopyTo {
		warn("copy metadata must include both copy from and copy to")
	}
	if file.IsRename && file.isCopy {
		warn("file cannot be both renamed and copied")
	}
	if file.isCopy && (file.IsNew || file.IsDeleted) {
		warn("copy metadata conflicts with new or deleted file status")
	}
	if file.hasSimilarityIndex && !file.IsRename && !file.isCopy {
		warn("similarity metadata requires rename or copy metadata")
	}
	if file.hasOldMode != file.hasNewMode {
		warn("mode-only change must include both old mode and new mode")
	}
	if file.hasOldMode && file.hasNewMode && file.oldModeValid && file.newModeValid &&
		file.oldMode == file.newMode {
		warn("old mode and new mode must be different")
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
		if !file.IsBinary && len(file.Hunks) > 0 && (!file.hasOldMarker || !file.oldIsDevNull) {
			warn("text new file must declare --- /dev/null")
		}
		if !file.IsBinary && len(file.Hunks) > 0 && (!file.hasNewMarker || file.newIsDevNull) {
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
		if !file.IsBinary && len(file.Hunks) > 0 && (!file.hasNewMarker || !file.newIsDevNull) {
			warn("text deleted file must declare +++ /dev/null")
		}
		if !file.IsBinary && len(file.Hunks) > 0 && (!file.hasOldMarker || file.oldIsDevNull) {
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

	newStatusTrusted := !file.IsNew || trustedNewFileStatus(*file)
	deletedStatusTrusted := !file.IsDeleted || trustedDeletedFileStatus(*file)
	if file.IsNew && !newStatusTrusted {
		file.IsNew = false
		file.OldPath = file.headerOldPath
	}
	if file.IsDeleted && !deletedStatusTrusted {
		file.IsDeleted = false
		file.NewPath = file.headerNewPath
	}
	binaryGoPath := file.NewPath
	if !isGoSourcePath(binaryGoPath) {
		binaryGoPath = file.OldPath
	}
	if file.IsBinary && isGoSourcePath(binaryGoPath) {
		line := file.binaryLine
		if line == 0 {
			line = file.headerLine
		}
		p.addWarning(binaryGoPath, line, "Go source path is represented as binary")
	}
	p.validateCurrentFileCompleteness(file, newStatusTrusted, deletedStatusTrusted, warn)
}

func (p *diffParser) validateCurrentFileCompleteness(
	file *changedFile,
	newStatusTrusted bool,
	deletedStatusTrusted bool,
	warn func(string),
) {
	if file.IsBinary {
		return
	}
	if len(file.Hunks) > 0 {
		if !file.hasOldMarker || !file.hasNewMarker {
			warn("text change must include both --- and +++ path markers")
		}
		return
	}
	if file.sawHunkHeader {
		return
	}

	emptyNew := file.IsNew && newStatusTrusted && !file.hasOldMarker && !file.hasNewMarker
	emptyDeleted := file.IsDeleted && deletedStatusTrusted && !file.hasOldMarker && !file.hasNewMarker
	modeOnly := !file.IsNew && !file.IsDeleted && !file.IsRename && !file.isCopy &&
		file.hasOldMode && file.oldModeValid && file.hasNewMode && file.newModeValid &&
		file.oldMode != file.newMode && !file.hasOldMarker && !file.hasNewMarker
	pureRename := file.IsRename && !file.isCopy && file.hasRenameFrom && file.hasRenameTo &&
		file.renameFromValid && file.renameToValid && file.renameFromPath != file.renameToPath &&
		file.hasSimilarityIndex && file.similarityValid && file.similarityIndex == 100 &&
		!file.hasOldMarker && !file.hasNewMarker
	pureCopy := file.isCopy && !file.IsRename && file.hasCopyFrom && file.hasCopyTo &&
		file.copyFromValid && file.copyToValid && file.copyFromPath != file.copyToPath &&
		file.hasSimilarityIndex && file.similarityValid && file.similarityIndex == 100 &&
		!file.hasOldMarker && !file.hasNewMarker
	if emptyNew || emptyDeleted || modeOnly || pureRename || pureCopy {
		return
	}
	warn("text file change is missing a hunk")
}

func trustedNewFileStatus(file changedFile) bool {
	if !file.hasNewFileMode || !file.newFileModeValid || file.hasDeletedFileMode ||
		file.IsDeleted || file.IsRename || file.isCopy ||
		file.headerNewPath == "" || file.hasOldMarker != file.hasNewMarker {
		return false
	}
	if file.IsBinary {
		return len(file.Hunks) == 0 && (!file.hasOldMarker || file.oldIsDevNull && !file.newIsDevNull)
	}
	if len(file.Hunks) == 0 && !file.hasOldMarker {
		return true
	}
	if !file.hasOldMarker || !file.oldIsDevNull || !file.hasNewMarker || file.newIsDevNull {
		return false
	}
	for _, hunk := range file.Hunks {
		if hunk.OldStart != 0 || hunk.OldCount != 0 {
			return false
		}
		for _, line := range hunk.Lines {
			if line.Kind != diffLineAdded {
				return false
			}
		}
	}
	return true
}

func trustedDeletedFileStatus(file changedFile) bool {
	if !file.hasDeletedFileMode || !file.deletedFileModeValid || file.hasNewFileMode ||
		file.IsNew || file.IsRename || file.isCopy ||
		file.headerOldPath == "" || file.hasOldMarker != file.hasNewMarker {
		return false
	}
	if file.IsBinary {
		return len(file.Hunks) == 0 && (!file.hasOldMarker || !file.oldIsDevNull && file.newIsDevNull)
	}
	if len(file.Hunks) == 0 && !file.hasOldMarker {
		return true
	}
	if !file.hasOldMarker || file.oldIsDevNull || !file.hasNewMarker || !file.newIsDevNull {
		return false
	}
	for _, hunk := range file.Hunks {
		if hunk.NewStart != 0 || hunk.NewCount != 0 {
			return false
		}
		for _, line := range hunk.Lines {
			if line.Kind != diffLineDeleted {
				return false
			}
		}
	}
	return true
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
	if validateDiffPathValue(oldPath) != nil || validateDiffPathValue(newPath) != nil {
		return "", "", "malformed diff header"
	}
	return normalizeDiffPath(oldPath, 'a'), normalizeDiffPath(newPath, 'b'), ""
}

func normalizeDiffPath(value string, prefix byte) string {
	if value == "/dev/null" {
		return value
	}
	expectedPrefix := string([]byte{prefix, '/'})
	if strings.HasPrefix(value, expectedPrefix) {
		return value[len(expectedPrefix):]
	}
	return value
}

func parseDiffMarkerPath(value string, prefix byte) (string, error) {
	pathValue, tail, err := parseMetadataPath(value)
	if err != nil {
		return "", err
	}
	if tail != "" {
		if tail[0] != '\t' || len(tail) == 1 || containsInvalidDiffPathByte(tail[1:]) {
			return "", fmt.Errorf("invalid diff path timestamp")
		}
	}
	return normalizeDiffPath(pathValue, prefix), nil
}

func parseMetadataPath(value string) (string, string, error) {
	if value == "" {
		return "", "", fmt.Errorf("missing diff path")
	}
	if value[0] == '"' {
		pathValue, tail, err := parseGitPathToken(value)
		if err != nil {
			return "", "", err
		}
		if err := validateDiffPathValue(pathValue); err != nil {
			return "", "", err
		}
		return pathValue, tail, nil
	}
	if tab := strings.IndexByte(value, '\t'); tab >= 0 {
		pathValue := value[:tab]
		if strings.TrimSpace(pathValue) != pathValue {
			return "", "", fmt.Errorf("unquoted diff path has surrounding whitespace")
		}
		if err := validateDiffPathValue(pathValue); err != nil {
			return "", "", err
		}
		return pathValue, value[tab:], nil
	}
	if strings.TrimSpace(value) != value {
		return "", "", fmt.Errorf("unquoted diff path has surrounding whitespace")
	}
	if err := validateDiffPathValue(value); err != nil {
		return "", "", err
	}
	return value, "", nil
}

func parseRenamePath(value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("missing rename path")
	}
	pathValue := value
	if value[0] == '"' {
		decoded, rest, err := parseGitPathToken(value)
		if err != nil {
			return "", err
		}
		if rest != "" {
			return "", fmt.Errorf("unexpected content after rename path")
		}
		pathValue = decoded
	} else if strings.TrimSpace(value) != value {
		return "", fmt.Errorf("unquoted rename path has surrounding whitespace")
	}
	if err := validateDiffPathValue(pathValue); err != nil {
		return "", err
	}
	if pathValue == "/dev/null" || strings.ContainsRune(pathValue, '\\') || hasWindowsDrive(pathValue) ||
		pathpkg.IsAbs(pathValue) || pathpkg.Clean(pathValue) != pathValue ||
		pathValue == "." || pathValue == ".." || strings.HasPrefix(pathValue, "../") {
		return "", fmt.Errorf("rename path is not repository-relative")
	}
	return pathValue, nil
}

func validateDiffPathValue(value string) error {
	if value == "" {
		return fmt.Errorf("empty diff path")
	}
	if containsInvalidDiffPathByte(value) {
		return fmt.Errorf("diff path contains an invalid byte")
	}
	return nil
}

func containsInvalidDiffPathByte(value string) bool {
	return strings.ContainsAny(value, "\x00\r\n")
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
