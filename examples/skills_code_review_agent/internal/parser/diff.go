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

	reader := bufio.NewReader(r)
	for {
		raw, err := reader.ReadString('\n')
		line := strings.TrimRight(raw, "\r\n")
		if line != "" {
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
			case strings.HasPrefix(line, "--- ") && curHunk == nil:
				if cur != nil {
					files = append(files, *cur)
				}
				oldPath := strings.TrimPrefix(line, "--- ")
				oldPath = strings.TrimPrefix(oldPath, "a/")
				cur = &FileDiff{OldPath: oldPath}
			case strings.HasPrefix(line, "+++ ") && cur != nil && curHunk == nil:
				newPath := strings.TrimPrefix(line, "+++ ")
				newPath = strings.TrimPrefix(newPath, "b/")
				cur.NewPath = newPath
			case strings.HasPrefix(line, "@@ ") && cur != nil:
				if curHunk != nil {
					cur.Hunks = append(cur.Hunks, *curHunk)
				}
				startLine := parseHunkStart(line)
				curHunk = &Hunk{StartLine: startLine}
			case curHunk != nil && (strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") || strings.HasPrefix(line, " ")):
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
