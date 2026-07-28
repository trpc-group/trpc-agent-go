//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package parser provides a minimal unified diff parser.
package parser

import (
	"bufio"
	"io"
	"strconv"
	"strings"
)

// Hunk is a single changed block within a file.
type Hunk struct {
	StartLine int      // 1-based line number in the new file
	Lines     []string // raw diff lines (including +/- prefix)
}

// FileDiff holds all hunks for one changed file.
type FileDiff struct {
	OldPath string
	NewPath string
	Hunks   []Hunk
}

// Parse reads a unified diff and returns per-file changes.
// bufio.Reader is used instead of bufio.Scanner so that arbitrarily long lines
// (generated code, minified assets, large SQL literals) do not abort parsing.
func Parse(r io.Reader) ([]FileDiff, error) {
	var files []FileDiff
	var cur *FileDiff
	var curHunk *Hunk
	// Remaining old/new-side body lines the active hunk still expects, derived
	// from its `@@ -a,b +c,d @@` counts. While a hunk is unfinished, a content
	// line such as a removed `--- ` or added `+++ ` must not be read as a file
	// header (both are legal inside a Go raw string).
	oldRem, newRem := 0, 0

	reader := bufio.NewReader(r)
	for {
		raw, err := reader.ReadString('\n')
		line := strings.TrimRight(raw, "\r\n")
		if line != "" {
			hunkOpen := curHunk != nil && (oldRem > 0 || newRem > 0)
			switch {
			case strings.HasPrefix(line, "diff --git "):
				// Explicit file boundary in git diffs: flush the current file before
				// the next --- / +++ pair arrives.
				if curHunk != nil {
					cur.Hunks = append(cur.Hunks, *curHunk)
					curHunk = nil
				}
				if cur != nil {
					files = append(files, *cur)
					cur = nil
				}
				oldRem, newRem = 0, 0
			case hunkOpen && (strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") || strings.HasPrefix(line, " ")):
				// Hunk body: consume against the declared line counts before any
				// header can be recognised again.
				curHunk.Lines = append(curHunk.Lines, line)
				switch line[0] {
				case '+':
					newRem--
				case '-':
					oldRem--
				default:
					oldRem--
					newRem--
				}
			case strings.HasPrefix(line, "--- "):
				// New file header. A concatenated diff without `diff --git` reaches
				// here with the previous hunk already consumed, so flush it and the
				// previous file before starting this one.
				if curHunk != nil {
					cur.Hunks = append(cur.Hunks, *curHunk)
					curHunk = nil
				}
				if cur != nil {
					files = append(files, *cur)
				}
				oldPath := unquoteGitPath(stripDiffTimestamp(strings.TrimPrefix(line, "--- ")))
				oldPath = strings.TrimPrefix(oldPath, "a/")
				cur = &FileDiff{OldPath: oldPath}
			case strings.HasPrefix(line, "+++ ") && cur != nil && curHunk == nil:
				newPath := unquoteGitPath(stripDiffTimestamp(strings.TrimPrefix(line, "+++ ")))
				newPath = strings.TrimPrefix(newPath, "b/")
				cur.NewPath = newPath
			case strings.HasPrefix(line, "@@ ") && cur != nil:
				if curHunk != nil {
					cur.Hunks = append(cur.Hunks, *curHunk)
				}
				startLine := parseHunkStart(line)
				oldRem, newRem = parseHunkCounts(line)
				curHunk = &Hunk{StartLine: startLine}
			case curHunk != nil && (strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") || strings.HasPrefix(line, " ")):
				// Trailing body lines past the declared counts (malformed headers).
				curHunk.Lines = append(curHunk.Lines, line)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return files, err
		}
	}
	if cur != nil {
		if curHunk != nil {
			cur.Hunks = append(cur.Hunks, *curHunk)
		}
		files = append(files, *cur)
	}
	return files, nil
}

// stripDiffTimestamp removes the tab-separated timestamp that traditional
// `diff -u` appends to a header path (e.g. "foo.go\t2024-01-01 12:00:00 +0000").
// Git C-quotes any literal tab inside a path, so the first tab is the separator.
func stripDiffTimestamp(s string) string {
	if i := strings.IndexByte(s, '\t'); i >= 0 {
		return s[:i]
	}
	return s
}

// unquoteGitPath decodes a Git C-quoted header path. Git wraps paths containing
// non-ASCII or control bytes in double quotes with octal/C escapes, which
// strconv.Unquote understands; other paths are returned unchanged.
func unquoteGitPath(p string) string {
	if len(p) < 2 || p[0] != '"' {
		return p
	}
	if unq, err := strconv.Unquote(p); err == nil {
		return unq
	}
	return p
}

// parseHunkStart extracts the new-file start line from "@@ -a,b +c,d @@" headers.
func parseHunkStart(header string) int {
	plus := strings.Index(header, " +")
	if plus < 0 {
		return 1
	}
	rest := header[plus+2:]
	comma := strings.IndexAny(rest, ", @")
	if comma > 0 {
		rest = rest[:comma]
	}
	n, err := strconv.Atoi(strings.TrimSpace(rest))
	if err != nil {
		return 1
	}
	return n
}

// parseHunkCounts extracts the old (b) and new (d) body-line counts from an
// "@@ -a,b +c,d @@" header. A missing count means 1 (unified-diff default).
func parseHunkCounts(header string) (oldCount, newCount int) {
	body := header
	if i := strings.Index(body[2:], "@@"); i >= 0 {
		body = body[:i+2]
	}
	minus := strings.Index(body, " -")
	plus := strings.Index(body, " +")
	if minus < 0 || plus < 0 {
		return 0, 0
	}
	return sideCount(body[minus+2 : plus]), sideCount(body[plus+2:])
}

// sideCount reads the count from a "start,count" (or bare "start") hunk side.
func sideCount(s string) int {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, ','); i >= 0 {
		if n, err := strconv.Atoi(strings.TrimSpace(s[i+1:])); err == nil {
			return n
		}
		return 1
	}
	return 1
}

// AddedLines returns only the lines added in a hunk ('+' prefix stripped).
func (h *Hunk) AddedLines() []string {
	var out []string
	for _, l := range h.Lines {
		if strings.HasPrefix(l, "+") {
			out = append(out, strings.TrimPrefix(l, "+"))
		}
	}
	return out
}

// AddedLinesNumbered returns added lines alongside their 1-based new-file line numbers.
// Using the index into AddedLines() as a direct offset from StartLine is wrong when
// context lines appear before the target added line inside the same hunk.
func (h *Hunk) AddedLinesNumbered() (lines []string, lineNums []int) {
	cur := h.StartLine
	for _, l := range h.Lines {
		switch {
		case strings.HasPrefix(l, "+"):
			lines = append(lines, strings.TrimPrefix(l, "+"))
			lineNums = append(lineNums, cur)
			cur++
		case strings.HasPrefix(l, "-"):
			// removed lines: no advance in the new file
		default:
			cur++ // context line
		}
	}
	return
}
