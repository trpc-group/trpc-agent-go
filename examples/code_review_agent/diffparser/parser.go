//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package diffparser parses unified diffs for the code review example.
package diffparser

import (
	"bufio"
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
)

var (
	hunkRE    = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(.*)$`)
	packageRE = regexp.MustCompile(`^\s*package\s+([A-Za-z_][A-Za-z0-9_]*)\b`)
)

// ParseUnifiedDiff parses a git-style or plain unified diff.
func ParseUnifiedDiff(data []byte) ([]review.ChangedFile, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	state := parserState{}
	for scanner.Scan() {
		if err := state.consumeLine(scanner.Text()); err != nil {
			return nil, err
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	state.flushFile()
	return state.files, nil
}

type parserState struct {
	files       []review.ChangedFile
	current     *review.ChangedFile
	currentHunk *review.Hunk
	oldLine     int
	newLine     int
}

func (p *parserState) consumeLine(line string) error {
	switch {
	case strings.HasPrefix(line, "diff --git "):
		p.flushFile()
		p.current = &review.ChangedFile{}
		if oldPath, newPath, ok := parseGitHeaderPaths(strings.TrimPrefix(line, "diff --git ")); ok {
			p.current.OldPath = cleanDiffPath(oldPath)
			p.current.NewPath = cleanDiffPath(newPath)
		}
	case strings.HasPrefix(line, "rename from "):
		if p.current != nil {
			p.current.OldPath = cleanDiffPath(strings.TrimPrefix(line, "rename from "))
		}
	case strings.HasPrefix(line, "rename to "):
		if p.current != nil {
			p.current.NewPath = cleanDiffPath(strings.TrimPrefix(line, "rename to "))
		}
	case strings.HasPrefix(line, "--- "):
		p.consumeOldPath(line)
	case strings.HasPrefix(line, "+++ "):
		p.ensureFile()
		p.current.NewPath = cleanDiffPath(strings.TrimSpace(strings.TrimPrefix(line, "+++ ")))
	case strings.HasPrefix(line, "@@ "):
		return p.startHunk(line)
	default:
		p.consumeHunkLine(line)
	}
	return nil
}

func (p *parserState) consumeOldPath(line string) {
	// Plain unified diffs start a new file at the next old-path header.
	if p.current != nil && p.current.NewPath != "" &&
		(len(p.current.Hunks) > 0 || p.currentHunk != nil) {
		p.flushFile()
	}
	p.ensureFile()
	p.current.OldPath = cleanDiffPath(strings.TrimSpace(strings.TrimPrefix(line, "--- ")))
}

func (p *parserState) startHunk(line string) error {
	p.ensureFile()
	p.flushHunk()
	m := hunkRE.FindStringSubmatch(line)
	if m == nil {
		return fmt.Errorf("invalid hunk header %q", line)
	}
	oldStart := atoiDefault(m[1], 0)
	newStart := atoiDefault(m[3], 0)
	p.oldLine, p.newLine = oldStart, newStart
	p.currentHunk = &review.Hunk{
		OldStart: oldStart,
		OldCount: atoiDefault(m[2], 1),
		NewStart: newStart,
		NewCount: atoiDefault(m[4], 1),
		Header:   strings.TrimSpace(m[5]),
	}
	return nil
}

func (p *parserState) consumeHunkLine(line string) {
	if p.currentHunk == nil {
		return
	}
	switch {
	case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
		p.currentHunk.Lines = append(p.currentHunk.Lines, review.DiffLine{
			Kind: "added", NewLine: p.newLine, Content: strings.TrimPrefix(line, "+"),
		})
		p.newLine++
	case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
		p.currentHunk.Lines = append(p.currentHunk.Lines, review.DiffLine{
			Kind: "removed", OldLine: p.oldLine, Content: strings.TrimPrefix(line, "-"),
		})
		p.oldLine++
	case strings.HasPrefix(line, " "):
		p.currentHunk.Lines = append(p.currentHunk.Lines, review.DiffLine{
			Kind: "context", OldLine: p.oldLine, NewLine: p.newLine,
			Content: strings.TrimPrefix(line, " "),
		})
		p.oldLine++
		p.newLine++
	}
}

func (p *parserState) ensureFile() {
	if p.current == nil {
		p.current = &review.ChangedFile{}
	}
}

func (p *parserState) flushHunk() {
	if p.current != nil && p.currentHunk != nil {
		p.current.Hunks = append(p.current.Hunks, *p.currentHunk)
		p.currentHunk = nil
	}
}

func (p *parserState) flushFile() {
	p.flushHunk()
	if p.current != nil && p.current.NewPath != "" {
		p.current.Language = languageForPath(p.current.NewPath)
		p.current.PackageName = detectPackage(*p.current)
		p.files = append(p.files, *p.current)
	}
	p.current = nil
}

// cleanDiffPath normalizes a diff header path, dropping a/ b/ prefixes and /dev/null.
func cleanDiffPath(path string) string {
	path = strings.TrimSpace(path)
	if unquoted, err := strconv.Unquote(path); err == nil {
		path = unquoted
	} else {
		path = strings.Trim(path, `"`)
	}
	if path == "/dev/null" {
		return ""
	}
	path = strings.TrimPrefix(path, "a/")
	path = strings.TrimPrefix(path, "b/")
	return filepath.ToSlash(path)
}

func parseGitHeaderPaths(header string) (string, string, bool) {
	first, rest, ok := consumeGitPath(strings.TrimSpace(header))
	if !ok {
		return "", "", false
	}
	second, _, ok := consumeGitPath(strings.TrimSpace(rest))
	return first, second, ok
}

func consumeGitPath(s string) (string, string, bool) {
	if s == "" {
		return "", "", false
	}
	if s[0] != '"' {
		if idx := strings.IndexByte(s, ' '); idx >= 0 {
			return s[:idx], s[idx+1:], true
		}
		return s, "", true
	}
	for i := 1; i < len(s); i++ {
		if s[i] != '"' {
			continue
		}
		backslashes := 0
		for j := i - 1; j >= 0 && s[j] == '\\'; j-- {
			backslashes++
		}
		if backslashes%2 == 0 {
			return s[:i+1], s[i+1:], true
		}
	}
	return "", "", false
}

// atoiDefault parses s as an int, falling back on empty or invalid input.
func atoiDefault(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return n
}

// languageForPath reports the language of a changed file by extension.
func languageForPath(path string) string {
	if strings.EqualFold(filepath.Ext(path), ".go") {
		return "go"
	}
	return ""
}

// detectPackage extracts the Go package name from the file's hunk lines.
func detectPackage(file review.ChangedFile) string {
	for _, h := range file.Hunks {
		for _, line := range h.Lines {
			if line.Kind != "added" && line.Kind != "context" {
				continue
			}
			if m := packageRE.FindStringSubmatch(line.Content); m != nil {
				return m[1]
			}
		}
	}
	return ""
}
