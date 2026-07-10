//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights
// reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package diffparse parses unified diff (git diff) output into structured
// file/hunk/line representations suitable for downstream code-review tooling.
//
// The parser is intentionally tolerant: it ignores blank separator lines that
// appear inside hunks, skips git metadata lines (index, mode, rename, copy,
// similarity, etc.) and handles arbitrarily long lines by reading from a
// bufio.Reader (rather than a bufio.Scanner, which imposes a 64KB per-token
// limit that breaks on long source lines).
//
// File boundaries are identified by the literal "diff --git " prefix. Within an
// active hunk, lines starting with ' ', '+', '-' or '\' are always treated as
// hunk content before any file-header ("--- "/"+++ ") or hunk-header ("@@ ")
// matching is attempted, so that a removed line whose own content begins with
// "---" is not misread as a file header.
package diffparse

import (
	"bufio"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// LineKind classifies a single line within a hunk.
type LineKind int

const (
	// LineContext is an unchanged context line (prefix " ").
	LineContext LineKind = iota
	// LineAdded is an added line (prefix "+").
	LineAdded
	// LineRemoved is a removed line (prefix "-").
	LineRemoved
	// LineNoNewline is a "\ No newline at end of file" marker.
	LineNoNewline
)

// HunkLine is a single line within a hunk.
type HunkLine struct {
	Kind        LineKind
	Content     string // line content without the leading prefix char
	NewFileLine int    // new-file line number for Context/Added; 0 for Removed/NoNewline
}

// Hunk is a contiguous group of changes within a file, introduced by a
// "@@ -a,b +c,d @@ ..." header.
type Hunk struct {
	OldStart int
	OldCount int
	NewStart int
	NewCount int
	Lines    []HunkLine
}

// DiffFile represents one file's worth of diff content.
type DiffFile struct {
	OldPath string // path from "--- " line without the "a/" prefix
	NewPath string // path from "+++ " line without the "b/" prefix
	Hunks   []Hunk
}

// FileDiff is the top-level result of parsing a unified diff: a list of files.
type FileDiff struct {
	Files []DiffFile
}

// hunkHeaderRe captures the numeric components of a "@@ -a,b +c,d @@ ..." header.
// The count segments (b, d) are optional per the unified diff specification.
var hunkHeaderRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// Parse reads a unified diff from r and returns its structured representation.
func Parse(r io.Reader) (*FileDiff, error) {
	p := &parser{reader: bufio.NewReader(r), result: &FileDiff{}}
	if err := p.run(); err != nil {
		return nil, err
	}
	return p.result, nil
}

// parser holds the transient state used while walking the diff.
type parser struct {
	reader  *bufio.Reader
	result  *FileDiff
	file    *DiffFile
	hunk    *Hunk
	inHunk  bool
	newLine int // running new-file line number for the current hunk
}

// run drives the line-by-line parse loop and flushes any trailing state.
func (p *parser) run() error {
	for {
		raw, err := p.reader.ReadString('\n')
		if len(raw) > 0 {
			if perr := p.process(stripEOL(raw)); perr != nil {
				return perr
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	p.flushFile()
	return nil
}

// process dispatches a single line either to hunk-content handling (when inside
// an active hunk) or to header handling. A non-content line encountered inside
// a hunk closes the hunk and is then re-evaluated as a header.
func (p *parser) process(line string) error {
	if p.inHunk {
		if p.handleHunkLine(line) {
			return nil
		}
		p.flushHunk()
		p.inHunk = false
	}
	return p.handleHeader(line)
}

// handleHunkLine appends a content line to the current hunk and reports whether
// the line was consumed as hunk content. Bare empty separator lines are ignored.
func (p *parser) handleHunkLine(line string) bool {
	if line == "" {
		// A bare empty line acting as a separator inside a hunk.
		return true
	}
	if p.hunk == nil {
		return false
	}
	switch line[0] {
	case ' ':
		p.addLine(LineContext, line[1:], p.newLine)
		p.newLine++
		return true
	case '+':
		p.addLine(LineAdded, line[1:], p.newLine)
		p.newLine++
		return true
	case '-':
		p.addLine(LineRemoved, line[1:], 0)
		return true
	case '\\':
		p.addLine(LineNoNewline, line[1:], 0)
		return true
	}
	return false
}

// addLine appends a line to the current hunk.
func (p *parser) addLine(kind LineKind, content string, newFileLine int) {
	p.hunk.Lines = append(p.hunk.Lines, HunkLine{
		Kind:        kind,
		Content:     content,
		NewFileLine: newFileLine,
	})
}

// handleHeader processes lines that appear outside hunk content: file
// boundaries ("diff --git "), file headers ("--- "/"+++ ") and hunk headers
// ("@@ "). Unrecognized metadata lines are ignored.
func (p *parser) handleHeader(line string) error {
	switch {
	case strings.HasPrefix(line, "diff --git "):
		p.flushFile()
		p.file = &DiffFile{}
	case strings.HasPrefix(line, "--- "):
		p.ensureFile()
		p.file.OldPath = stripPath(line[4:])
	case strings.HasPrefix(line, "+++ "):
		p.ensureFile()
		p.file.NewPath = stripPath(line[4:])
	case strings.HasPrefix(line, "@@ "):
		p.ensureFile()
		p.startHunk(line)
	default:
		// Ignore metadata: index, mode, rename, copy, similarity, etc.
	}
	return nil
}

// ensureFile guarantees a current file exists for header attachment.
func (p *parser) ensureFile() {
	if p.file == nil {
		p.file = &DiffFile{}
	}
}

// startHunk parses a "@@ ... @@" header and begins a new hunk. If the header
// does not match the expected shape the line is silently skipped.
func (p *parser) startHunk(line string) {
	m := hunkHeaderRe.FindStringSubmatch(line)
	if m == nil {
		return
	}
	oldStart, _ := strconv.Atoi(m[1])
	newStart, _ := strconv.Atoi(m[3])
	oldCount := 1
	if m[2] != "" {
		oldCount, _ = strconv.Atoi(m[2])
	}
	newCount := 1
	if m[4] != "" {
		newCount, _ = strconv.Atoi(m[4])
	}
	p.flushHunk()
	p.hunk = &Hunk{
		OldStart: oldStart,
		OldCount: oldCount,
		NewStart: newStart,
		NewCount: newCount,
	}
	p.newLine = newStart
	p.inHunk = true
}

// flushHunk appends the current hunk (if any) to the current file.
func (p *parser) flushHunk() {
	if p.hunk != nil && p.file != nil {
		p.file.Hunks = append(p.file.Hunks, *p.hunk)
	}
	p.hunk = nil
}

// flushFile appends the current hunk and file (if any) to the result.
func (p *parser) flushFile() {
	p.flushHunk()
	if p.file != nil {
		p.result.Files = append(p.result.Files, *p.file)
	}
	p.file = nil
}

// stripEOL removes a trailing newline (and optional carriage return) produced
// by bufio.Reader.ReadString.
func stripEOL(s string) string {
	if strings.HasSuffix(s, "\n") {
		s = s[:len(s)-1]
	}
	if strings.HasSuffix(s, "\r") {
		s = s[:len(s)-1]
	}
	return s
}

// stripPath removes the leading "a/" or "b/" prefix used by git diffs and any
// trailing whitespace from a file path taken from a "--- "/"+++ " header.
func stripPath(s string) string {
	s = strings.TrimRight(s, " \t\r")
	if strings.HasPrefix(s, "a/") {
		s = s[2:]
	} else if strings.HasPrefix(s, "b/") {
		s = s[2:]
	}
	return s
}

// AddedLines returns only the added lines across all hunks of the file.
func (f *DiffFile) AddedLines() []HunkLine {
	var out []HunkLine
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			if l.Kind == LineAdded {
				out = append(out, l)
			}
		}
	}
	return out
}

// AddedLinesNumbered returns the added lines of the file with their NewFileLine
// populated, correctly accounting for the context and removed lines that
// precede them within each hunk.
func (f *DiffFile) AddedLinesNumbered() []HunkLine {
	var out []HunkLine
	for i := range f.Hunks {
		out = append(out, f.Hunks[i].AddedLinesNumbered()...)
	}
	return out
}

// AddedLinesNumbered returns the added lines of the hunk with their NewFileLine
// populated. The new-file line counter starts at NewStart and advances for each
// context and added line; removed and no-newline markers do not advance it.
func (h *Hunk) AddedLinesNumbered() []HunkLine {
	var out []HunkLine
	line := h.NewStart
	for _, l := range h.Lines {
		switch l.Kind {
		case LineContext:
			line++
		case LineAdded:
			out = append(out, HunkLine{Kind: l.Kind, Content: l.Content, NewFileLine: line})
			line++
		case LineRemoved, LineNoNewline:
			// Removed lines and no-newline markers do not advance the new-file counter.
		}
	}
	return out
}
