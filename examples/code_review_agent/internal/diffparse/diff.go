//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package diffparse parses unified diff output into structured hunks.
package diffparse

import (
	"regexp"
	"strconv"
	"strings"
)

// ChangedLine represents one changed line in a hunk.
// Kind is one of '+', '-', or ' '. Zero OldLine or NewLine means
// the corresponding line number is not applicable.
type ChangedLine struct {
	Kind    string `json:"kind"`
	OldLine int    `json:"old_line"`
	NewLine int    `json:"new_line"`
	Content string `json:"content"`
}

// Hunk represents a single diff hunk.
type Hunk struct {
	OldStart int           `json:"old_start"`
	OldCount int           `json:"old_count"`
	NewStart int           `json:"new_start"`
	NewCount int           `json:"new_count"`
	Lines    []ChangedLine `json:"lines"`
}

// ChangedFile represents a file modified in the diff.
type ChangedFile struct {
	OldPath  string   `json:"old_path"`
	NewPath  string   `json:"new_path"`
	Binary   bool     `json:"binary"`
	Deleted  bool     `json:"deleted"`
	Renamed  bool     `json:"renamed,omitempty"`
	NewFile  bool     `json:"new_file,omitempty"`
	Hunks    []Hunk   `json:"hunks"`
	Extended []string `json:"extended,omitempty"`
}

// ParsedDiff holds the complete parsed diff.
type ParsedDiff struct {
	Files []ChangedFile `json:"files"`
}

var hunkHeaderRE = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(.*)$`)
var fileHeaderRE = regexp.MustCompile(`^diff --git a/(.+) b/(.+)$`)
var oldFileRE = regexp.MustCompile(`^--- (?:a/)?(.+)$`)
var newFileRE = regexp.MustCompile(`^\+\+\+ (?:b/)?(.+)$`)

// Parse parses a unified diff string into structured hunks.
func Parse(diffText string) (*ParsedDiff, error) {
	p := &parser{result: &ParsedDiff{}}
	for _, line := range strings.Split(diffText, "\n") {
		p.processLine(line)
	}
	p.finalize()
	return p.result, nil
}

type parser struct {
	result   *ParsedDiff
	cur      *ChangedFile
	curHunk  *Hunk
	oldLine  int
	newLine  int
	extCount int
}

func (p *parser) processLine(line string) {
	switch {
	case strings.HasPrefix(line, "diff --git "):
		p.handleFileHeader(line)
	case p.cur == nil:
		return
	case isExtendedHeader(line):
		p.handleExtendedHeader(line)
	case strings.HasPrefix(line, "--- "):
		p.handleOldFile(line)
	case strings.HasPrefix(line, "+++ "):
		p.handleNewFile(line)
	case strings.HasPrefix(line, "@@ "):
		p.handleHunkHeader(line)
	case p.curHunk != nil && len(line) > 0:
		p.handleHunkLine(line)
	}
}

func (p *parser) handleFileHeader(line string) {
	p.finalizeCurrent()
	matches := fileHeaderRE.FindStringSubmatch(line)
	if matches != nil {
		p.cur = &ChangedFile{
			OldPath: cleanDiffPath(matches[1]),
			NewPath: cleanDiffPath(matches[2]),
		}
	} else {
		p.cur = &ChangedFile{}
	}
	p.extCount = 0
	p.curHunk = nil
}

func isExtendedHeader(line string) bool {
	return strings.HasPrefix(line, "copy from ") ||
		strings.HasPrefix(line, "copy to ") ||
		strings.HasPrefix(line, "rename from ") ||
		strings.HasPrefix(line, "rename to ") ||
		strings.HasPrefix(line, "index ") ||
		strings.HasPrefix(line, "similarity index ") ||
		strings.HasPrefix(line, "old mode ") ||
		strings.HasPrefix(line, "new mode ") ||
		strings.HasPrefix(line, "deleted file mode ") ||
		strings.HasPrefix(line, "new file mode ") ||
		strings.HasPrefix(line, "Binary files ") ||
		strings.HasPrefix(line, "GIT binary patch")
}

func (p *parser) handleExtendedHeader(line string) {
	p.extCount++
	if strings.HasPrefix(line, "Binary files ") || strings.HasPrefix(line, "GIT binary patch") {
		p.cur.Binary = true
	}
	if p.extCount <= 5 {
		p.cur.Extended = append(p.cur.Extended, line)
	}
	p.updateRenamedFromExtended()
}

func (p *parser) updateRenamedFromExtended() {
	extended := strings.Join(p.cur.Extended, "\n")
	if strings.Contains(extended, "copy from") || strings.Contains(extended, "copy to") {
		p.cur.Renamed = false
	} else if strings.Contains(extended, "rename from") || strings.Contains(extended, "rename to") {
		p.cur.Renamed = true
	}
}

func (p *parser) handleOldFile(line string) {
	matches := oldFileRE.FindStringSubmatch(line)
	if matches != nil {
		if p.cur.OldPath == "" {
			p.cur.OldPath = cleanDiffPath(matches[1])
		}
		if strings.TrimSpace(matches[1]) == "/dev/null" {
			p.cur.NewFile = true
		}
	}
}

func (p *parser) handleNewFile(line string) {
	matches := newFileRE.FindStringSubmatch(line)
	if matches != nil {
		if p.cur.NewPath == "" {
			p.cur.NewPath = cleanDiffPath(matches[1])
		}
		if strings.TrimSpace(matches[1]) == "/dev/null" {
			p.cur.Deleted = true
		}
	}
}

func (p *parser) handleHunkHeader(line string) {
	matches := hunkHeaderRE.FindStringSubmatch(line)
	if matches == nil {
		return
	}
	oldStart, _ := strconv.Atoi(matches[1])
	oldCount := defaultCount(matches[2])
	newStart, _ := strconv.Atoi(matches[3])
	newCount := defaultCount(matches[4])
	p.curHunk = &Hunk{
		OldStart: oldStart,
		OldCount: oldCount,
		NewStart: newStart,
		NewCount: newCount,
	}
	p.cur.Hunks = append(p.cur.Hunks, *p.curHunk)
	p.oldLine = oldStart
	p.newLine = newStart
}

func defaultCount(s string) int {
	if s == "" {
		return 1
	}
	n, _ := strconv.Atoi(s)
	return n
}

func (p *parser) handleHunkLine(line string) {
	switch line[0] {
	case '+':
		p.curHunk.Lines = append(p.curHunk.Lines, ChangedLine{
			Kind: "+", NewLine: p.newLine, Content: line[1:],
		})
		p.newLine++
	case '-':
		p.curHunk.Lines = append(p.curHunk.Lines, ChangedLine{
			Kind: "-", OldLine: p.oldLine, Content: line[1:],
		})
		p.oldLine++
	case ' ':
		p.curHunk.Lines = append(p.curHunk.Lines, ChangedLine{
			Kind: " ", OldLine: p.oldLine, NewLine: p.newLine, Content: line[1:],
		})
		p.oldLine++
		p.newLine++
	}
	p.cur.Hunks[len(p.cur.Hunks)-1] = *p.curHunk
}

func (p *parser) finalizeCurrent() {
	if p.cur == nil {
		return
	}
	if p.cur.Deleted {
		p.cur.NewPath = "/dev/null"
	}
	if p.cur.NewFile {
		p.cur.OldPath = "/dev/null"
	}
	if !p.cur.Deleted && !p.cur.NewFile && p.cur.OldPath != p.cur.NewPath {
		extended := strings.Join(p.cur.Extended, "\n")
		if !strings.Contains(extended, "copy from") && !strings.Contains(extended, "copy to") {
			p.cur.Renamed = true
		}
	}
	p.result.Files = append(p.result.Files, *p.cur)
	p.cur = nil
	p.curHunk = nil
}

func (p *parser) finalize() {
	if p.cur == nil {
		return
	}
	if p.cur.Deleted {
		p.cur.NewPath = "/dev/null"
	}
	if p.cur.NewFile {
		p.cur.OldPath = "/dev/null"
	}
	if !p.cur.Deleted && !p.cur.NewFile && p.cur.OldPath != p.cur.NewPath {
		extended := strings.Join(p.cur.Extended, "\n")
		if !strings.Contains(extended, "copy from") && !strings.Contains(extended, "copy to") {
			p.cur.Renamed = true
		}
	}
	p.result.Files = append(p.result.Files, *p.cur)
}

func cleanDiffPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return path
	}
	// Remove leading a/ or b/ prefixes if present
	if strings.HasPrefix(path, "a/") {
		path = path[2:]
	} else if strings.HasPrefix(path, "b/") {
		path = path[2:]
	}
	if path == "/dev/null" {
		return path
	}
	return path
}

// AddedLines returns lines with kind '+' in the given file.
func AddedLines(cf ChangedFile) []ChangedLine {
	var result []ChangedLine
	for _, h := range cf.Hunks {
		for _, l := range h.Lines {
			if l.Kind == "+" {
				result = append(result, l)
			}
		}
	}
	return result
}

// AllAddedLines returns all added lines across all changed files.
func AllAddedLines(pd *ParsedDiff) map[string][]ChangedLine {
	result := make(map[string][]ChangedLine)
	for _, cf := range pd.Files {
		key := cf.NewPath
		if key == "" || key == "/dev/null" {
			key = cf.OldPath
		}
		lines := AddedLines(cf)
		if len(lines) > 0 {
			result[key] = lines
		}
	}
	return result
}

func parseInt(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}
