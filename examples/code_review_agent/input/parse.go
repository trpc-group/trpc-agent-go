//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package input parses review inputs into a DiffBundle.
package input

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// DiffBundle is the normalized review input.
type DiffBundle struct {
	Kind        string
	Digest      string
	Summary     string
	RawRedacted string
	Files       []ChangedFile
}

// ChangedFile describes one file in the diff.
type ChangedFile struct {
	Path     string
	Language string
	Package  string
	Hunks    []Hunk
}

// Hunk is one unified-diff hunk.
type Hunk struct {
	Header   string
	OldStart int
	OldLines int
	NewStart int
	NewLines int
	Lines    []DiffLine
}

// DiffLine is one line inside a hunk.
type DiffLine struct {
	Kind      rune
	Text      string
	NewLineNo int
	OldLineNo int
}

// ParseUnifiedDiff parses unified diff text into a DiffBundle.
func ParseUnifiedDiff(kind, raw string) (*DiffBundle, error) {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	files, err := parseFiles(raw)
	if err != nil {
		return nil, err
	}
	for i := range files {
		files[i].Language = detectLanguage(files[i].Path)
		files[i].Package = detectPackage(files[i])
	}
	added, removed := countChanges(files)
	summary := fmt.Sprintf("%d files, +%d/-%d", len(files), added, removed)
	digest := sha256Hex(raw)
	return &DiffBundle{
		Kind:        kind,
		Digest:      digest,
		Summary:     summary,
		RawRedacted: raw, // caller should redact before persist
		Files:       files,
	}, nil
}

// ParseDiffFile reads and parses a patch file.
func ParseDiffFile(path string) (*DiffBundle, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read diff file: %w", err)
	}
	return ParseUnifiedDiff("diff_file", string(b))
}

// ParseRepoDiff collects git diff from a repository path.
func ParseRepoDiff(repoPath string) (*DiffBundle, error) {
	abs, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, err
	}
	unstaged, err := runGit(abs, "diff")
	if err != nil {
		return nil, err
	}
	staged, err := runGit(abs, "diff", "--cached")
	if err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(unstaged + "\n" + staged)
	if raw == "" {
		return ParseUnifiedDiff("repo", "")
	}
	return ParseUnifiedDiff("repo", raw+"\n")
}

// LoadFixture loads testdata/fixtures/<name>/diff.patch.
func LoadFixture(fixturesRoot, name string) (*DiffBundle, string, error) {
	dir := filepath.Join(fixturesRoot, name)
	patchPath := filepath.Join(dir, "diff.patch")
	b, err := os.ReadFile(patchPath)
	if err != nil {
		return nil, "", fmt.Errorf("read fixture %s: %w", name, err)
	}
	bundle, err := ParseUnifiedDiff("fixture", string(b))
	if err != nil {
		return nil, "", err
	}
	return bundle, dir, nil
}

// runGit runs a git command in dir and returns trimmed stdout.
func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %v: %w (%s)", args, err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// parseFiles parses +++/--- file headers into ChangedFile entries.
func parseFiles(raw string) ([]ChangedFile, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	sc := bufio.NewScanner(strings.NewReader(raw))
	// Large diffs may have long lines.
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 2*1024*1024)

	var files []ChangedFile
	var cur *ChangedFile
	var hunk *Hunk
	oldLine, newLine := 0, 0

	flushHunk := func() {
		if cur != nil && hunk != nil {
			cur.Hunks = append(cur.Hunks, *hunk)
		}
		hunk = nil
	}
	flushFile := func() {
		flushHunk()
		if cur != nil {
			files = append(files, *cur)
		}
		cur = nil
	}

	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flushFile()
			path := parseDiffGitPath(line)
			cur = &ChangedFile{Path: path}
		case strings.HasPrefix(line, "+++ "):
			if cur == nil {
				cur = &ChangedFile{}
			}
			p := strings.TrimPrefix(line, "+++ ")
			p = strings.TrimSpace(p)
			if strings.HasPrefix(p, "b/") {
				p = strings.TrimPrefix(p, "b/")
			}
			if p != "/dev/null" && p != "" {
				cur.Path = p
			}
		case strings.HasPrefix(line, "@@ "):
			flushHunk()
			hs, err := parseHunkHeader(line)
			if err != nil {
				return nil, err
			}
			hunk = &hs
			oldLine = hs.OldStart
			newLine = hs.NewStart
		case hunk != nil && (strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") || strings.HasPrefix(line, " ")):
			kind := rune(line[0])
			text := ""
			if len(line) > 1 {
				text = line[1:]
			}
			dl := DiffLine{Kind: kind, Text: text}
			switch kind {
			case '+':
				dl.NewLineNo = newLine
				newLine++
			case '-':
				dl.OldLineNo = oldLine
				oldLine++
			case ' ':
				dl.OldLineNo = oldLine
				dl.NewLineNo = newLine
				oldLine++
				newLine++
			}
			hunk.Lines = append(hunk.Lines, dl)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	flushFile()
	return files, nil
}

// parseDiffGitPath extracts the path from a diff --git header line.
func parseDiffGitPath(line string) string {
	// diff --git a/path b/path
	parts := strings.Fields(line)
	if len(parts) >= 4 {
		p := parts[3]
		return strings.TrimPrefix(p, "b/")
	}
	if len(parts) >= 3 {
		return strings.TrimPrefix(parts[2], "a/")
	}
	return "unknown"
}

// parseHunkHeader parses a unified-diff @@ hunk header.
func parseHunkHeader(line string) (Hunk, error) {
	// @@ -oldStart,oldLines +newStart,newLines @@ optional
	h := Hunk{Header: line}
	rest := strings.TrimPrefix(line, "@@ ")
	idx := strings.Index(rest, "@@")
	if idx >= 0 {
		rest = strings.TrimSpace(rest[:idx])
	}
	fields := strings.Fields(rest)
	if len(fields) < 2 {
		return h, fmt.Errorf("invalid hunk header: %s", line)
	}
	oldPart := strings.TrimPrefix(fields[0], "-")
	newPart := strings.TrimPrefix(fields[1], "+")
	os, ol, err := parseRange(oldPart)
	if err != nil {
		return h, err
	}
	ns, nl, err := parseRange(newPart)
	if err != nil {
		return h, err
	}
	h.OldStart, h.OldLines = os, ol
	h.NewStart, h.NewLines = ns, nl
	return h, nil
}

// parseRange parses a hunk range like "12,3" into start and count.
func parseRange(s string) (start, lines int, err error) {
	parts := strings.Split(s, ",")
	start, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, err
	}
	lines = 1
	if len(parts) > 1 {
		lines, err = strconv.Atoi(parts[1])
		if err != nil {
			return 0, 0, err
		}
	}
	return start, lines, nil
}

// detectLanguage returns a coarse language tag from a file path.
func detectLanguage(path string) string {
	if strings.HasSuffix(path, ".go") {
		return "go"
	}
	return "other"
}

// detectPackage extracts the Go package name from added lines.
func detectPackage(f ChangedFile) string {
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			if l.Kind == '-' {
				continue
			}
			trim := strings.TrimSpace(l.Text)
			if strings.HasPrefix(trim, "package ") {
				return strings.TrimSpace(strings.TrimPrefix(trim, "package "))
			}
		}
	}
	dir := filepath.Dir(f.Path)
	if dir == "." || dir == "" {
		return "main"
	}
	return filepath.Base(dir)
}

// countChanges counts added and deleted lines in a DiffBundle.
func countChanges(files []ChangedFile) (added, removed int) {
	for _, f := range files {
		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				switch l.Kind {
				case '+':
					added++
				case '-':
					removed++
				}
			}
		}
	}
	return added, removed
}

// sha256Hex returns the hex-encoded SHA-256 digest of s.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// AddedGoFiles returns added/changed Go file paths (non-test).
func (b *DiffBundle) AddedGoFiles() []string {
	var out []string
	for _, f := range b.Files {
		if f.Language != "go" {
			continue
		}
		if strings.HasSuffix(f.Path, "_test.go") {
			continue
		}
		if hasAddedLines(f) {
			out = append(out, f.Path)
		}
	}
	return out
}

// hasAddedLines reports whether the file has any added lines.
func hasAddedLines(f ChangedFile) bool {
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			if l.Kind == '+' {
				return true
			}
		}
	}
	return false
}

// HasTestFile reports whether path's corresponding _test.go appears in the bundle.
func (b *DiffBundle) HasTestFile(path string) bool {
	if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
		return false
	}
	want := strings.TrimSuffix(path, ".go") + "_test.go"
	for _, f := range b.Files {
		if f.Path == want {
			return true
		}
	}
	return false
}
