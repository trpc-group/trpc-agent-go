//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package input parses and normalizes code review input.
package input

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

const (
	defaultMaxInputBytes   = 10 << 20
	defaultMaxLineBytes    = 1 << 20
	defaultMaxLines        = 1_000_000
	defaultMaxFiles        = 1_000
	defaultMaxHunks        = 10_000
	defaultMaxChangedLines = 100_000
)

var hunkHeaderPattern = regexp.MustCompile(`^@@ -([0-9]+)(?:,([0-9]+))? \+([0-9]+)(?:,([0-9]+))? @@(?: .*)?$`)

// ChangeKind describes how a file changed in a diff.
type ChangeKind string

const (
	// ChangeModified indicates an existing file was modified.
	ChangeModified ChangeKind = "modified"
	// ChangeAdded indicates a file was added.
	ChangeAdded ChangeKind = "added"
	// ChangeDeleted indicates a file was deleted.
	ChangeDeleted ChangeKind = "deleted"
	// ChangeRenamed indicates a file was renamed.
	ChangeRenamed ChangeKind = "renamed"
	// ChangeCopied indicates a file was copied.
	ChangeCopied ChangeKind = "copied"
)

// LineKind describes the role of a line within a hunk.
type LineKind string

const (
	// LineContext indicates a line present in both the old and new file.
	LineContext LineKind = "context"
	// LineDeleted indicates a line present only in the old file.
	LineDeleted LineKind = "deleted"
	// LineAdded indicates a line present only in the new file.
	LineAdded LineKind = "added"
)

// DiffLayer identifies which Git state transition produced a file diff.
type DiffLayer = review.ChangeLayer

const (
	// DiffLayerUnified identifies a standalone diff without repository-layer metadata.
	DiffLayerUnified DiffLayer = review.ChangeLayerUnified
	// DiffLayerStaged identifies the HEAD-to-index transition.
	DiffLayerStaged DiffLayer = review.ChangeLayerStaged
	// DiffLayerWorktree identifies the index-to-working-tree transition.
	DiffLayerWorktree DiffLayer = review.ChangeLayerWorktree
)

// Diff is a parsed unified diff in source order.
type Diff struct {
	// Files contains changed files in source order.
	Files []File
}

// File describes one changed file and its hunks.
type File struct {
	// Layer identifies the repository state transition represented by this file.
	Layer DiffLayer
	// OldPath is the normalized path before the change, or empty for an added file.
	OldPath string
	// NewPath is the normalized path after the change, or empty for a deleted file.
	NewPath string
	// Change identifies the file operation.
	Change ChangeKind
	// Binary reports whether the diff contains binary content for the file.
	Binary bool
	// Hunks contains text hunks in source order.
	Hunks []Hunk
}

// Hunk contains one ordered unified-diff hunk and its declared ranges.
type Hunk struct {
	// OldStart is the first line or empty-range anchor in the old file.
	OldStart int
	// OldLines is the declared number of old-file lines.
	OldLines int
	// NewStart is the first line or empty-range anchor in the new file.
	NewStart int
	// NewLines is the declared number of new-file lines.
	NewLines int
	// Lines contains parsed hunk lines in source order.
	Lines []Line
}

// Line contains hunk text and the line numbers that exist for its kind.
type Line struct {
	// Kind identifies whether the line is context, deleted, or added.
	Kind LineKind
	// Text excludes the unified-diff prefix byte.
	Text string
	// OldNumber is present only when the line exists in the old file.
	OldNumber *int
	// NewNumber is present only when the line exists in the new file.
	NewNumber *int
}

// Limits bounds parser resource use. Zero fields retain their default values.
type Limits struct {
	// MaxInputBytes limits total bytes read from the input. MaxInt64 is rejected
	// because the parser reserves one additional byte to detect oversized input.
	MaxInputBytes int
	// MaxLineBytes limits one line excluding its line ending.
	MaxLineBytes int
	// MaxLines limits physical input lines, including a final line without a
	// line ending.
	MaxLines int
	// MaxFiles limits changed files.
	MaxFiles int
	// MaxHunks limits hunks across all files.
	MaxHunks int
	// MaxChangedLines limits added and deleted lines across all hunks.
	MaxChangedLines int
}

type options struct {
	limits Limits
}

// Option configures Parse.
type Option func(*options)

// WithLimits overrides nonzero parser resource limits.
func WithLimits(limits Limits) Option {
	return func(options *options) {
		mergeLimits(&options.limits, limits)
	}
}

// Parse reads a Git-style unified diff without invoking external commands.
// Git C-quoted paths are decoded before validation. Accepted paths are relative,
// slash-separated, canonical paths without NUL, backslash, empty, dot, or parent
// components.
func Parse(reader io.Reader, opts ...Option) (Diff, error) {
	if reader == nil {
		return Diff{}, errors.New("parse diff: nil reader")
	}
	configuration := options{limits: defaultLimits()}
	for _, option := range opts {
		if option == nil {
			return Diff{}, errors.New("parse diff: nil option")
		}
		option(&configuration)
	}
	if err := validateLimits(configuration.limits); err != nil {
		return Diff{}, err
	}

	parser := diffParser{
		lines:  newBoundedLineReader(reader, configuration.limits),
		limits: configuration.limits,
	}
	return parser.parse()
}

type diffParser struct {
	lines        *boundedLineReader
	limits       Limits
	diff         Diff
	current      *File
	hunk         *hunkState
	hunks        int
	changedLines int
	seenOld      bool
	seenNew      bool
	headerOld    string
	headerNew    string
	headerPaths  []filePaths
	operation    ChangeKind
	seenFrom     bool
	seenTo       bool
	phase        filePhase
	lastOldEnd   int
	lastNewEnd   int
	lastOldEmpty bool
	lastNewEmpty bool
	oldEOF       bool
	newEOF       bool
}

type filePhase uint8

const (
	filePhaseMetadata filePhase = iota
	filePhaseMarkers
	filePhaseText
	filePhaseBinarySummary
	filePhaseBinaryPatch
)

type hunkState struct {
	hunk          Hunk
	oldSeen       int
	newSeen       int
	oldNext       int
	newNext       int
	markerAllowed bool
	previousKind  LineKind
}

type filePaths struct {
	old string
	new string
}

func (p *diffParser) parse() (Diff, error) {
	for {
		line, err := p.lines.next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return Diff{}, err
		}
		if err := p.consume(line); err != nil {
			return Diff{}, err
		}
	}
	if err := p.finishHunk(); err != nil {
		return Diff{}, err
	}
	if err := p.finishFile(); err != nil {
		return Diff{}, err
	}
	if len(p.diff.Files) == 0 {
		return Diff{}, errors.New("parse diff: no files")
	}
	return p.diff, nil
}

func (p *diffParser) consume(line string) error {
	for {
		if p.hunk != nil {
			complete := p.hunk.oldSeen == p.hunk.hunk.OldLines && p.hunk.newSeen == p.hunk.hunk.NewLines
			if !complete {
				if strings.HasPrefix(line, "diff --git ") || strings.HasPrefix(line, "@@ ") {
					return errors.New("parse diff: hunk line count mismatch")
				}
				return p.consumeHunkLine(line)
			}
			if line == `\ No newline at end of file` {
				return p.consumeNoNewlineMarker()
			}
			if line != "" && strings.ContainsRune(" +-", rune(line[0])) {
				return errors.New("parse diff: hunk line count mismatch")
			}
			if err := p.finishHunk(); err != nil {
				return err
			}
			continue
		}

		if strings.HasPrefix(line, "diff --git ") {
			if err := p.finishFile(); err != nil {
				return err
			}
			return p.startFile(line)
		}
		if p.current == nil {
			return errors.New("parse diff: unexpected top-level line")
		}
		return p.consumeFileLine(line)
	}
}

func (p *diffParser) startFile(line string) error {
	if len(p.diff.Files) >= p.limits.MaxFiles {
		return errors.New("parse diff: file limit exceeded")
	}
	headerPaths, err := parseDiffPaths(strings.TrimPrefix(line, "diff --git "))
	if err != nil {
		return err
	}
	p.current = &File{Change: ChangeModified}
	p.headerPaths = headerPaths
	p.setHeaderPaths(headerPaths[0])
	p.seenOld = false
	p.seenNew = false
	p.operation = ""
	p.seenFrom = false
	p.seenTo = false
	p.phase = filePhaseMetadata
	p.lastOldEnd = 0
	p.lastNewEnd = 0
	p.lastOldEmpty = false
	p.lastNewEmpty = false
	p.oldEOF = false
	p.newEOF = false
	return nil
}

func (p *diffParser) consumeFileLine(line string) error {
	switch {
	case strings.HasPrefix(line, "index "), strings.HasPrefix(line, "old mode "),
		strings.HasPrefix(line, "new mode "), strings.HasPrefix(line, "similarity index "),
		strings.HasPrefix(line, "dissimilarity index "):
		if p.phase != filePhaseMetadata {
			return errors.New("parse diff: structural metadata after content")
		}
		return nil
	case strings.HasPrefix(line, "new file mode "):
		return p.consumeModeMetadata(ChangeAdded)
	case strings.HasPrefix(line, "deleted file mode "):
		return p.consumeModeMetadata(ChangeDeleted)
	case strings.HasPrefix(line, "rename from "):
		return p.consumeOperationMetadata(ChangeRenamed, true, strings.TrimPrefix(line, "rename from "))
	case strings.HasPrefix(line, "rename to "):
		return p.consumeOperationMetadata(ChangeRenamed, false, strings.TrimPrefix(line, "rename to "))
	case strings.HasPrefix(line, "copy from "):
		return p.consumeOperationMetadata(ChangeCopied, true, strings.TrimPrefix(line, "copy from "))
	case strings.HasPrefix(line, "copy to "):
		return p.consumeOperationMetadata(ChangeCopied, false, strings.TrimPrefix(line, "copy to "))
	case strings.HasPrefix(line, "--- "):
		if p.seenOld || p.seenNew {
			return errors.New("parse diff: duplicate or out-of-order file marker")
		}
		if p.phase != filePhaseMetadata {
			return errors.New("parse diff: structural metadata after content")
		}
		value, err := normalizeMarkerPath(strings.TrimPrefix(line, "--- "), "a/")
		if err != nil {
			return err
		}
		if err := p.matchOldHeaderPath(value); err != nil {
			return fmt.Errorf("parse diff: file marker: %w", err)
		}
		p.seenOld = true
		p.phase = filePhaseMarkers
		return nil
	case strings.HasPrefix(line, "+++ "):
		if !p.seenOld || p.seenNew {
			return errors.New("parse diff: duplicate or out-of-order file marker")
		}
		if p.phase != filePhaseMarkers {
			return errors.New("parse diff: structural metadata after content")
		}
		value, err := normalizeMarkerPath(strings.TrimPrefix(line, "+++ "), "b/")
		if err != nil {
			return err
		}
		if err := p.matchNewHeaderPath(value); err != nil {
			return fmt.Errorf("parse diff: file marker: %w", err)
		}
		p.seenNew = true
		return nil
	case strings.HasPrefix(line, "Binary files ") && strings.HasSuffix(line, " differ"):
		return p.consumeBinaryLine(line)
	case line == "GIT binary patch":
		return p.startBinaryContent(filePhaseBinaryPatch)
	case strings.HasPrefix(line, "@@ "):
		if !p.seenOld || !p.seenNew {
			return errors.New("parse diff: hunk missing file markers")
		}
		return p.startHunk(line)
	default:
		if p.phase == filePhaseBinaryPatch {
			return nil
		}
		if p.phase == filePhaseBinarySummary {
			return errors.New("parse diff: content after binary summary")
		}
		return errors.New("parse diff: unexpected file line")
	}
}

func (p *diffParser) consumeModeMetadata(kind ChangeKind) error {
	if p.phase != filePhaseMetadata || p.seenOld || p.seenNew {
		return errors.New("parse diff: operation metadata after content started")
	}
	if p.operation != "" {
		return errors.New("parse diff: duplicate or conflicting file operation metadata")
	}
	p.operation = kind
	p.current.Change = kind
	if kind == ChangeAdded {
		p.current.OldPath = ""
	} else {
		p.current.NewPath = ""
	}
	return nil
}

func (p *diffParser) consumeOperationMetadata(kind ChangeKind, from bool, rawPath string) error {
	if p.phase != filePhaseMetadata || p.seenOld || p.seenNew {
		return errors.New("parse diff: operation metadata after content started")
	}
	value, err := decodeAndValidatePath(rawPath)
	if err != nil {
		return err
	}
	if p.operation != "" && p.operation != kind {
		return errors.New("parse diff: mixed file operation metadata")
	}
	if from {
		if p.seenFrom || p.seenTo {
			return errors.New("parse diff: duplicate or out-of-order file operation metadata")
		}
		if err := p.narrowHeaderPaths(&value, nil); err != nil {
			return err
		}
		p.seenFrom = true
		p.current.OldPath = value
	} else {
		if !p.seenFrom || p.seenTo {
			return errors.New("parse diff: duplicate or out-of-order file operation metadata")
		}
		if err := p.narrowHeaderPaths(nil, &value); err != nil {
			return err
		}
		p.seenTo = true
		p.current.NewPath = value
	}
	p.operation = kind
	p.current.Change = kind
	return nil
}

func (p *diffParser) consumeBinaryLine(line string) error {
	if err := p.startBinaryContent(filePhaseBinarySummary); err != nil {
		return err
	}
	paths := strings.TrimSuffix(strings.TrimPrefix(line, "Binary files "), " differ")
	separators, separatorErr := binaryPathSeparators(paths)
	if separatorErr != nil {
		return separatorErr
	}
	var pathErr error
	foundSafePair := false
	for _, separator := range separators {
		oldPath, oldErr := normalizeMarkerPath(paths[:separator.start], "a/")
		newPath, newErr := normalizeMarkerPath(paths[separator.end:], "b/")
		if oldErr == nil && newErr == nil {
			foundSafePair = true
			if p.binaryPathsMatch(oldPath, newPath) {
				p.selectBinaryPaths(oldPath, newPath)
				return nil
			}
		} else if pathErr == nil {
			if oldErr != nil {
				pathErr = oldErr
			} else {
				pathErr = newErr
			}
		}
	}
	if foundSafePair {
		return errors.New("parse diff: binary paths mismatch")
	}
	if pathErr != nil {
		return pathErr
	}
	return errors.New("parse diff: malformed binary paths")
}

func (p *diffParser) startBinaryContent(phase filePhase) error {
	if p.phase != filePhaseMetadata {
		return errors.New("parse diff: conflicting file content")
	}
	p.phase = phase
	p.current.Binary = true
	return nil
}

func (p *diffParser) matchOldHeaderPath(value string) error {
	if p.current.Change == ChangeAdded {
		if value != "" {
			return errors.New("header path mismatch")
		}
		return nil
	}
	return p.narrowHeaderPaths(&value, nil)
}

func (p *diffParser) matchNewHeaderPath(value string) error {
	if p.current.Change == ChangeDeleted {
		if value != "" {
			return errors.New("header path mismatch")
		}
		return nil
	}
	return p.narrowHeaderPaths(nil, &value)
}

func (p *diffParser) narrowHeaderPaths(oldPath, newPath *string) error {
	matched := p.headerPaths[:0]
	for _, candidate := range p.headerPaths {
		if oldPath != nil && candidate.old != *oldPath {
			continue
		}
		if newPath != nil && candidate.new != *newPath {
			continue
		}
		matched = append(matched, candidate)
	}
	if len(matched) == 0 {
		return errors.New("header path mismatch")
	}
	p.headerPaths = matched
	if len(matched) == 1 {
		p.setHeaderPaths(matched[0])
	}
	return nil
}

func (p *diffParser) setHeaderPaths(paths filePaths) {
	p.headerOld = paths.old
	p.headerNew = paths.new
	switch p.current.Change {
	case ChangeAdded:
		p.current.NewPath = paths.new
	case ChangeDeleted:
		p.current.OldPath = paths.old
	default:
		p.current.OldPath = paths.old
		p.current.NewPath = paths.new
	}
}

func (p *diffParser) binaryPathsMatch(oldPath, newPath string) bool {
	for _, candidate := range p.headerPaths {
		expectedOld := candidate.old
		expectedNew := candidate.new
		if p.current.Change == ChangeAdded {
			expectedOld = ""
		}
		if p.current.Change == ChangeDeleted {
			expectedNew = ""
		}
		if oldPath == expectedOld && newPath == expectedNew {
			return true
		}
	}
	return false
}

func (p *diffParser) selectBinaryPaths(oldPath, newPath string) {
	var oldConstraint, newConstraint *string
	if p.current.Change != ChangeAdded {
		oldConstraint = &oldPath
	}
	if p.current.Change != ChangeDeleted {
		newConstraint = &newPath
	}
	_ = p.narrowHeaderPaths(oldConstraint, newConstraint)
}

func (p *diffParser) startHunk(line string) error {
	if p.phase != filePhaseMarkers && p.phase != filePhaseText {
		return errors.New("parse diff: conflicting file content")
	}
	if p.hunks >= p.limits.MaxHunks {
		return errors.New("parse diff: hunk limit exceeded")
	}
	hunk, err := parseHunkHeader(line)
	if err != nil {
		return err
	}
	if hunk.OldLines == 0 && hunk.NewLines == 0 {
		return errors.New("parse diff: empty hunk")
	}
	if p.oldEOF {
		return errors.New("parse diff: old side after end of file")
	}
	if p.newEOF {
		return errors.New("parse diff: new side after end of file")
	}
	if len(p.current.Hunks) > 0 && (hunk.OldStart < p.lastOldEnd || hunk.NewStart < p.lastNewEnd) {
		return errors.New("parse diff: overlapping hunk")
	}
	if p.lastOldEmpty && hunk.OldLines == 0 && hunk.OldStart == p.lastOldEnd {
		return errors.New("parse diff: repeated old empty-range anchor")
	}
	if p.lastNewEmpty && hunk.NewLines == 0 && hunk.NewStart == p.lastNewEnd {
		return errors.New("parse diff: repeated new empty-range anchor")
	}
	p.hunk = &hunkState{
		hunk:    hunk,
		oldNext: hunk.OldStart,
		newNext: hunk.NewStart,
	}
	p.phase = filePhaseText
	p.hunks++
	return nil
}

func (p *diffParser) consumeHunkLine(line string) error {
	if line == `\ No newline at end of file` {
		return p.consumeNoNewlineMarker()
	}
	if line == "" {
		return errors.New("parse diff: malformed hunk line")
	}
	parsed := Line{Text: line[1:]}
	switch line[0] {
	case ' ':
		if p.oldEOF {
			return errors.New("parse diff: old side after end of file")
		}
		if p.newEOF {
			return errors.New("parse diff: new side after end of file")
		}
		parsed.Kind = LineContext
		parsed.OldNumber = numberPointer(p.hunk.oldNext)
		parsed.NewNumber = numberPointer(p.hunk.newNext)
		p.hunk.oldNext++
		p.hunk.newNext++
		p.hunk.oldSeen++
		p.hunk.newSeen++
	case '-':
		if p.oldEOF {
			return errors.New("parse diff: old side after end of file")
		}
		parsed.Kind = LineDeleted
		parsed.OldNumber = numberPointer(p.hunk.oldNext)
		p.hunk.oldNext++
		p.hunk.oldSeen++
		if err := p.countChangedLine(); err != nil {
			return err
		}
	case '+':
		if p.newEOF {
			return errors.New("parse diff: new side after end of file")
		}
		parsed.Kind = LineAdded
		parsed.NewNumber = numberPointer(p.hunk.newNext)
		p.hunk.newNext++
		p.hunk.newSeen++
		if err := p.countChangedLine(); err != nil {
			return err
		}
	default:
		return errors.New("parse diff: malformed hunk line")
	}
	if p.hunk.oldSeen > p.hunk.hunk.OldLines || p.hunk.newSeen > p.hunk.hunk.NewLines {
		return errors.New("parse diff: hunk line count mismatch")
	}
	p.hunk.hunk.Lines = append(p.hunk.hunk.Lines, parsed)
	p.hunk.markerAllowed = true
	p.hunk.previousKind = parsed.Kind
	return nil
}

func (p *diffParser) consumeNoNewlineMarker() error {
	if !p.hunk.markerAllowed {
		return errors.New("parse diff: misplaced no-newline marker")
	}
	valid := false
	switch p.hunk.previousKind {
	case LineDeleted:
		valid = p.hunk.oldSeen == p.hunk.hunk.OldLines
		if valid {
			p.oldEOF = true
		}
	case LineAdded:
		valid = p.hunk.newSeen == p.hunk.hunk.NewLines
		if valid {
			p.newEOF = true
		}
	case LineContext:
		valid = p.hunk.oldSeen == p.hunk.hunk.OldLines && p.hunk.newSeen == p.hunk.hunk.NewLines
		if valid {
			p.oldEOF = true
			p.newEOF = true
		}
	}
	if !valid {
		return errors.New("parse diff: early no-newline marker")
	}
	p.hunk.markerAllowed = false
	return nil
}

func (p *diffParser) countChangedLine() error {
	p.changedLines++
	if p.changedLines > p.limits.MaxChangedLines {
		return errors.New("parse diff: changed line limit exceeded")
	}
	return nil
}

func (p *diffParser) finishHunk() error {
	if p.hunk == nil {
		return nil
	}
	if p.hunk.oldSeen != p.hunk.hunk.OldLines || p.hunk.newSeen != p.hunk.hunk.NewLines {
		return errors.New("parse diff: hunk line count mismatch")
	}
	p.current.Hunks = append(p.current.Hunks, p.hunk.hunk)
	p.lastOldEnd = rangeEnd(p.hunk.hunk.OldStart, p.hunk.hunk.OldLines)
	p.lastNewEnd = rangeEnd(p.hunk.hunk.NewStart, p.hunk.hunk.NewLines)
	p.lastOldEmpty = p.hunk.hunk.OldLines == 0
	p.lastNewEmpty = p.hunk.hunk.NewLines == 0
	p.hunk = nil
	return nil
}

func (p *diffParser) finishFile() error {
	if p.current == nil {
		return nil
	}
	if (p.operation == ChangeRenamed || p.operation == ChangeCopied) && (!p.seenFrom || !p.seenTo) {
		return errors.New("parse diff: incomplete file operation metadata")
	}
	if len(p.headerPaths) != 1 {
		return errors.New("parse diff: ambiguous diff header paths")
	}
	p.setHeaderPaths(p.headerPaths[0])
	if err := validateFile(*p.current); err != nil {
		return err
	}
	p.diff.Files = append(p.diff.Files, *p.current)
	p.current = nil
	return nil
}

func parseDiffPaths(value string) ([]filePaths, error) {
	tokens, tokenErr := lexGitTokens(value, 2)
	if tokenErr == nil && len(tokens) == 2 {
		oldPath, oldErr := normalizeDecodedPrefixedPath(tokens[0], "a/")
		newPath, newErr := normalizeDecodedPrefixedPath(tokens[1], "b/")
		if oldErr == nil && newErr == nil {
			return []filePaths{{old: oldPath, new: newPath}}, nil
		}
		if oldErr != nil {
			return nil, oldErr
		}
		return nil, newErr
	}
	var candidates []filePaths
	var pathErr error
	separators, separatorErr := diffHeaderSeparators(value)
	if separatorErr != nil {
		return nil, separatorErr
	}
	for _, separator := range separators {
		oldRaw := strings.TrimRight(value[:separator.start], " \t")
		newRaw := strings.TrimLeft(value[separator.end:], " \t")
		oldPath, oldErr := normalizePrefixedPath(oldRaw, "a/")
		newPath, newErr := normalizePrefixedPath(newRaw, "b/")
		if oldErr == nil && newErr == nil {
			candidates = append(candidates, filePaths{old: oldPath, new: newPath})
		} else if pathErr == nil {
			if oldErr != nil {
				pathErr = oldErr
			} else {
				pathErr = newErr
			}
		}
	}
	if len(candidates) > 0 {
		return candidates, nil
	}
	if pathErr != nil {
		return nil, pathErr
	}
	return nil, errors.New("parse diff: malformed diff header")
}

type headerSeparator struct {
	start int
	end   int
}

func diffHeaderSeparators(value string) ([]headerSeparator, error) {
	const maxCandidates = 16
	var separators []headerSeparator
	quoted := false
	escaped := false
	for offset := 0; offset < len(value); {
		character := value[offset]
		if quoted {
			offset++
			if escaped {
				escaped = false
				continue
			}
			switch character {
			case '\\':
				escaped = true
			case '"':
				quoted = false
			}
			continue
		}
		if character == '"' {
			quoted = true
			offset++
			continue
		}
		if character != ' ' && character != '\t' {
			offset++
			continue
		}
		start := offset
		for offset < len(value) && (value[offset] == ' ' || value[offset] == '\t') {
			offset++
		}
		right := value[offset:]
		if !strings.HasPrefix(right, "b/") && !strings.HasPrefix(right, `"b/`) {
			continue
		}
		if len(separators) == maxCandidates {
			return nil, errors.New("parse diff: too many ambiguous header paths")
		}
		separators = append(separators, headerSeparator{start: start, end: offset})
	}
	return separators, nil
}

func binaryPathSeparators(value string) ([]headerSeparator, error) {
	const maxCandidates = 16
	var separators []headerSeparator
	quoted := false
	escaped := false
	for offset := 0; offset < len(value); offset++ {
		character := value[offset]
		if quoted {
			if escaped {
				escaped = false
				continue
			}
			switch character {
			case '\\':
				escaped = true
			case '"':
				quoted = false
			}
			continue
		}
		if character == '"' {
			quoted = true
			continue
		}
		if !strings.HasPrefix(value[offset:], " and ") {
			continue
		}
		end := offset + len(" and ")
		right := value[end:]
		if !strings.HasPrefix(right, "b/") && !strings.HasPrefix(right, `"b/`) {
			continue
		}
		if len(separators) == maxCandidates {
			return nil, errors.New("parse diff: too many ambiguous binary paths")
		}
		separators = append(separators, headerSeparator{start: offset, end: end})
		offset = end - 1
	}
	return separators, nil
}

func normalizeMarkerPath(value, prefix string) (string, error) {
	decoded, err := decodeGitPath(value)
	if err != nil {
		return "", err
	}
	return normalizeDecodedMarkerPath(decoded, prefix)
}

func normalizeDecodedMarkerPath(value, prefix string) (string, error) {
	if value == "/dev/null" {
		return "", nil
	}
	return normalizeDecodedPrefixedPath(value, prefix)
}

func normalizePrefixedPath(value, prefix string) (string, error) {
	decoded, err := decodeGitPath(value)
	if err != nil {
		return "", err
	}
	return normalizeDecodedPrefixedPath(decoded, prefix)
}

func normalizeDecodedPrefixedPath(value, prefix string) (string, error) {
	if !strings.HasPrefix(value, prefix) {
		return "", errors.New("parse diff: unsafe path prefix")
	}
	return validatePath(strings.TrimPrefix(value, prefix))
}

func decodeAndValidatePath(value string) (string, error) {
	decoded, err := decodeGitPath(value)
	if err != nil {
		return "", err
	}
	return validatePath(decoded)
}

func decodeGitPath(value string) (string, error) {
	if !strings.HasPrefix(value, `"`) {
		if strings.ContainsRune(value, '"') {
			return "", errors.New("parse diff: malformed quoted path")
		}
		return value, nil
	}
	tokens, err := lexGitTokens(value, 1)
	if err != nil || len(tokens) != 1 {
		return "", errors.New("parse diff: malformed quoted path")
	}
	return tokens[0], nil
}

func lexGitTokens(value string, maxTokens int) ([]string, error) {
	if maxTokens <= 0 {
		return nil, errors.New("parse diff: invalid token limit")
	}
	tokens := make([]string, 0, maxTokens)
	for offset := 0; ; {
		for offset < len(value) && (value[offset] == ' ' || value[offset] == '\t') {
			offset++
		}
		if offset == len(value) {
			return tokens, nil
		}
		if len(tokens) == maxTokens {
			return nil, errors.New("parse diff: too many path tokens")
		}

		var token strings.Builder
		if value[offset] != '"' {
			start := offset
			for offset < len(value) && value[offset] != ' ' && value[offset] != '\t' {
				if value[offset] == '"' {
					return nil, errors.New("parse diff: malformed quoted path")
				}
				offset++
			}
			tokens = append(tokens, value[start:offset])
			continue
		}

		offset++
		closed := false
		for offset < len(value) {
			character := value[offset]
			offset++
			switch character {
			case '"':
				closed = true
			case '\\':
				if offset == len(value) {
					return nil, errors.New("parse diff: malformed quoted path")
				}
				escaped := value[offset]
				offset++
				switch escaped {
				case 'a':
					token.WriteByte('\a')
				case 'b':
					token.WriteByte('\b')
				case 'f':
					token.WriteByte('\f')
				case 'n':
					token.WriteByte('\n')
				case 'r':
					token.WriteByte('\r')
				case 't':
					token.WriteByte('\t')
				case 'v':
					token.WriteByte('\v')
				case '\\', '"':
					token.WriteByte(escaped)
				case '0', '1', '2', '3', '4', '5', '6', '7':
					decoded := int(escaped - '0')
					for digits := 1; digits < 3 && offset < len(value); digits++ {
						next := value[offset]
						if next < '0' || next > '7' {
							break
						}
						decoded = decoded*8 + int(next-'0')
						offset++
					}
					if decoded > 255 {
						return nil, errors.New("parse diff: malformed quoted path")
					}
					token.WriteByte(byte(decoded))
				default:
					return nil, errors.New("parse diff: malformed quoted path")
				}
			default:
				token.WriteByte(character)
			}
			if closed {
				break
			}
		}
		if !closed || (offset < len(value) && value[offset] != ' ' && value[offset] != '\t') {
			return nil, errors.New("parse diff: malformed quoted path")
		}
		tokens = append(tokens, token.String())
	}
}

func validatePath(value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", errors.New("parse diff: unsafe path encoding")
	}
	if strings.ContainsRune(value, '\x00') || strings.Contains(value, `\`) {
		return "", errors.New("parse diff: unsafe path content")
	}
	if value == "" || path.IsAbs(value) {
		return "", errors.New("parse diff: unsafe path form")
	}
	if path.Clean(value) != value {
		return "", errors.New("parse diff: unsafe path form")
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." {
			return "", errors.New("parse diff: unsafe path component")
		}
	}
	return value, nil
}

func parseHunkHeader(line string) (Hunk, error) {
	matches := hunkHeaderPattern.FindStringSubmatch(line)
	if matches == nil {
		return Hunk{}, errors.New("parse diff: invalid hunk header")
	}
	oldStart, err := parseHeaderNumber(matches[1])
	if err != nil {
		return Hunk{}, err
	}
	oldLines, err := parseHeaderCount(matches[2])
	if err != nil {
		return Hunk{}, err
	}
	newStart, err := parseHeaderNumber(matches[3])
	if err != nil {
		return Hunk{}, err
	}
	newLines, err := parseHeaderCount(matches[4])
	if err != nil {
		return Hunk{}, err
	}
	if !validRange(oldStart, oldLines) || !validRange(newStart, newLines) {
		return Hunk{}, errors.New("parse diff: invalid hunk header")
	}
	return Hunk{OldStart: oldStart, OldLines: oldLines, NewStart: newStart, NewLines: newLines}, nil
}

func validRange(start, count int) bool {
	if start == 0 {
		return count == 0
	}
	return count <= int(^uint(0)>>1)-start
}

func parseHeaderNumber(value string) (int, error) {
	number, err := strconv.Atoi(value)
	if err != nil || number < 0 {
		return 0, errors.New("parse diff: invalid hunk header")
	}
	return number, nil
}

func parseHeaderCount(value string) (int, error) {
	if value == "" {
		return 1, nil
	}
	return parseHeaderNumber(value)
}

func validateFile(file File) error {
	switch file.Change {
	case ChangeAdded:
		if file.OldPath != "" || file.NewPath == "" {
			return errors.New("parse diff: invalid added file paths")
		}
	case ChangeDeleted:
		if file.OldPath == "" || file.NewPath != "" {
			return errors.New("parse diff: invalid deleted file paths")
		}
	case ChangeModified, ChangeRenamed, ChangeCopied:
		if file.OldPath == "" || file.NewPath == "" {
			return errors.New("parse diff: missing file path")
		}
	default:
		return errors.New("parse diff: invalid change kind")
	}
	return nil
}

func rangeEnd(start, count int) int {
	if count == 0 {
		return start
	}
	return start + count
}

func numberPointer(value int) *int {
	return &value
}

func defaultLimits() Limits {
	return Limits{
		MaxInputBytes:   defaultMaxInputBytes,
		MaxLineBytes:    defaultMaxLineBytes,
		MaxLines:        defaultMaxLines,
		MaxFiles:        defaultMaxFiles,
		MaxHunks:        defaultMaxHunks,
		MaxChangedLines: defaultMaxChangedLines,
	}
}

func mergeLimits(destination *Limits, source Limits) {
	if source.MaxInputBytes != 0 {
		destination.MaxInputBytes = source.MaxInputBytes
	}
	if source.MaxLineBytes != 0 {
		destination.MaxLineBytes = source.MaxLineBytes
	}
	if source.MaxLines != 0 {
		destination.MaxLines = source.MaxLines
	}
	if source.MaxFiles != 0 {
		destination.MaxFiles = source.MaxFiles
	}
	if source.MaxHunks != 0 {
		destination.MaxHunks = source.MaxHunks
	}
	if source.MaxChangedLines != 0 {
		destination.MaxChangedLines = source.MaxChangedLines
	}
}

func validateLimits(limits Limits) error {
	if limits.MaxInputBytes <= 0 || limits.MaxLineBytes <= 0 || limits.MaxLines <= 0 || limits.MaxFiles <= 0 ||
		limits.MaxHunks <= 0 || limits.MaxChangedLines <= 0 {
		return errors.New("parse diff: limits must be positive")
	}
	if int64(limits.MaxInputBytes) == int64(^uint64(0)>>1) {
		return errors.New("parse diff: input byte limit too large")
	}
	return nil
}

type boundedLineReader struct {
	reader        *bufio.Reader
	limited       *io.LimitedReader
	inputLimit    int64
	maxInputBytes int64
	maxLineBytes  int
	maxLines      int
	lines         int
}

func newBoundedLineReader(reader io.Reader, limits Limits) *boundedLineReader {
	maxInputBytes := int64(limits.MaxInputBytes)
	inputLimit := maxInputBytes + 1
	limited := &io.LimitedReader{R: reader, N: inputLimit}
	return &boundedLineReader{
		reader:        bufio.NewReader(limited),
		limited:       limited,
		inputLimit:    inputLimit,
		maxInputBytes: maxInputBytes,
		maxLineBytes:  limits.MaxLineBytes,
		maxLines:      limits.MaxLines,
	}
}

func (r *boundedLineReader) next() (string, error) {
	var line strings.Builder
	for {
		fragment, prefix, err := r.reader.ReadLine()
		if r.inputLimit-r.limited.N > r.maxInputBytes {
			return "", errors.New("parse diff: input byte limit exceeded")
		}
		if line.Len()+len(fragment) > r.maxLineBytes {
			return "", errors.New("parse diff: line length limit exceeded")
		}
		line.Write(fragment)
		if err != nil {
			if errors.Is(err, io.EOF) && line.Len() > 0 {
				return r.finishLine(line.String())
			}
			return "", err
		}
		if !prefix {
			value := line.String()
			return r.finishLine(value)
		}
	}
}

func (r *boundedLineReader) finishLine(line string) (string, error) {
	if strings.ContainsRune(line, '\x00') {
		return "", errors.New("parse diff: unsafe path or content contains nul byte")
	}
	r.lines++
	if r.lines > r.maxLines {
		return "", errors.New("parse diff: line limit exceeded")
	}
	return line, nil
}
