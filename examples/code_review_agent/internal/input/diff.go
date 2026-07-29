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
)

const (
	defaultMaxInputBytes   = 10 << 20
	defaultMaxLineBytes    = 1 << 20
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

// Diff is a parsed unified diff in source order.
type Diff struct {
	// Files contains changed files in source order.
	Files []File
}

// File describes one changed file and its hunks.
type File struct {
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
	lines          *boundedLineReader
	limits         Limits
	diff           Diff
	current        *File
	hunk           *hunkState
	hunks          int
	changedLines   int
	seenOld        bool
	seenNew        bool
	headerOld      string
	headerNew      string
	headerPaths    []filePaths
	operation      ChangeKind
	seenFrom       bool
	seenTo         bool
	contentStarted bool
	lastOldEnd     int
	lastNewEnd     int
}

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
			return fmt.Errorf("parse diff: unexpected line %q", line)
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
	p.contentStarted = false
	p.lastOldEnd = 0
	p.lastNewEnd = 0
	return nil
}

func (p *diffParser) consumeFileLine(line string) error {
	switch {
	case strings.HasPrefix(line, "index "), strings.HasPrefix(line, "old mode "),
		strings.HasPrefix(line, "new mode "), strings.HasPrefix(line, "similarity index "),
		strings.HasPrefix(line, "dissimilarity index "):
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
		value, err := normalizeMarkerPath(strings.TrimPrefix(line, "--- "), "a/")
		if err != nil {
			return err
		}
		if err := p.matchOldHeaderPath(value); err != nil {
			return fmt.Errorf("parse diff: file marker: %w", err)
		}
		p.seenOld = true
		p.contentStarted = true
		return nil
	case strings.HasPrefix(line, "+++ "):
		if !p.seenOld || p.seenNew {
			return errors.New("parse diff: duplicate or out-of-order file marker")
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
		p.current.Binary = true
		p.contentStarted = true
		return nil
	case strings.HasPrefix(line, "@@ "):
		if !p.seenOld || !p.seenNew {
			return errors.New("parse diff: hunk missing file markers")
		}
		return p.startHunk(line)
	default:
		if p.current.Binary {
			return nil
		}
		return fmt.Errorf("parse diff: unexpected file line %q", line)
	}
}

func (p *diffParser) consumeModeMetadata(kind ChangeKind) error {
	if p.contentStarted {
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
	if p.contentStarted {
		return errors.New("parse diff: operation metadata after content started")
	}
	value, err := validatePath(rawPath)
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
	paths := strings.TrimSuffix(strings.TrimPrefix(line, "Binary files "), " differ")
	var pathErr error
	foundSafePair := false
	for offset := 0; offset < len(paths); {
		separator := strings.Index(paths[offset:], " and ")
		if separator < 0 {
			break
		}
		separator += offset
		oldPath, oldErr := normalizeMarkerPath(paths[:separator], "a/")
		newPath, newErr := normalizeMarkerPath(paths[separator+len(" and "):], "b/")
		if oldErr == nil && newErr == nil {
			foundSafePair = true
			if p.binaryPathsMatch(oldPath, newPath) {
				p.selectBinaryPaths(oldPath, newPath)
				p.current.Binary = true
				p.contentStarted = true
				return nil
			}
		} else if pathErr == nil {
			if oldErr != nil {
				pathErr = oldErr
			} else {
				pathErr = newErr
			}
		}
		offset = separator + len(" and ")
	}
	if foundSafePair {
		return errors.New("parse diff: binary paths mismatch")
	}
	if pathErr != nil {
		return pathErr
	}
	return errors.New("parse diff: malformed binary paths")
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
	if len(p.current.Hunks) > 0 && (hunk.OldStart < p.lastOldEnd || hunk.NewStart < p.lastNewEnd) {
		return errors.New("parse diff: overlapping hunk")
	}
	p.hunk = &hunkState{
		hunk:    hunk,
		oldNext: hunk.OldStart,
		newNext: hunk.NewStart,
	}
	p.contentStarted = true
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
		parsed.Kind = LineContext
		parsed.OldNumber = numberPointer(p.hunk.oldNext)
		parsed.NewNumber = numberPointer(p.hunk.newNext)
		p.hunk.oldNext++
		p.hunk.newNext++
		p.hunk.oldSeen++
		p.hunk.newSeen++
	case '-':
		parsed.Kind = LineDeleted
		parsed.OldNumber = numberPointer(p.hunk.oldNext)
		p.hunk.oldNext++
		p.hunk.oldSeen++
		if err := p.countChangedLine(); err != nil {
			return err
		}
	case '+':
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
	case LineAdded:
		valid = p.hunk.newSeen == p.hunk.hunk.NewLines
	case LineContext:
		valid = p.hunk.oldSeen == p.hunk.hunk.OldLines && p.hunk.newSeen == p.hunk.hunk.NewLines
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
	var candidates []filePaths
	var pathErr error
	for offset := 0; offset < len(value); {
		separator := strings.Index(value[offset:], " b/")
		if separator < 0 {
			break
		}
		separator += offset
		oldPath, oldErr := normalizePrefixedPath(value[:separator], "a/")
		newPath, newErr := normalizePrefixedPath(value[separator+1:], "b/")
		if oldErr == nil && newErr == nil {
			candidates = append(candidates, filePaths{old: oldPath, new: newPath})
		} else if pathErr == nil {
			if oldErr != nil {
				pathErr = oldErr
			} else {
				pathErr = newErr
			}
		}
		offset = separator + len(" b/")
	}
	if len(candidates) > 0 {
		return candidates, nil
	}
	if pathErr != nil {
		return nil, pathErr
	}
	return nil, errors.New("parse diff: malformed diff header")
}

func normalizeMarkerPath(value, prefix string) (string, error) {
	if value == "/dev/null" {
		return "", nil
	}
	return normalizePrefixedPath(value, prefix)
}

func normalizePrefixedPath(value, prefix string) (string, error) {
	if !strings.HasPrefix(value, prefix) {
		return "", fmt.Errorf("parse diff: unsafe path %q", value)
	}
	return validatePath(strings.TrimPrefix(value, prefix))
}

func validatePath(value string) (string, error) {
	if strings.ContainsRune(value, '\x00') || strings.Contains(value, `\`) {
		return "", fmt.Errorf("parse diff: unsafe path %q", value)
	}
	if value == "" || path.IsAbs(value) {
		return "", fmt.Errorf("parse diff: unsafe path %q", value)
	}
	for _, component := range strings.Split(value, "/") {
		if component == ".." {
			return "", fmt.Errorf("parse diff: unsafe path %q", value)
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
	if limits.MaxInputBytes <= 0 || limits.MaxLineBytes <= 0 || limits.MaxFiles <= 0 ||
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
				return line.String(), nil
			}
			return "", err
		}
		if !prefix {
			value := line.String()
			if strings.ContainsRune(value, '\x00') {
				return "", errors.New("parse diff: unsafe path or content contains nul byte")
			}
			return value, nil
		}
	}
}
