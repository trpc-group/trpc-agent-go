//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package input parses review inputs into normalized changed files.
package input

import (
	"bufio"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Limits bounds parser input.
type Limits struct {
	MaxBytes int64
	MaxLines int
}

// Diff is a parsed unified diff.
type Diff struct {
	Files    []FileDiff
	Warnings []string
	Complete bool
}

// FileDiff describes one changed file.
type FileDiff struct {
	OldPath string
	NewPath string
	Package string
	Hunks   []Hunk
	Added   []AddedLine
	Deleted bool
	Renamed bool
	Binary  bool
}

// Hunk describes one unified diff hunk and its review candidates.
type Hunk struct {
	OldStart   int
	OldLines   int
	NewStart   int
	NewLines   int
	Context    []ContextLine
	Candidates []AddedLine
}

// ContextLine is an unchanged line available around candidates.
type ContextLine struct {
	OldLine int
	NewLine int
	Text    string
}

// AddedLine is an added line anchored to the new file.
type AddedLine struct {
	Line    int
	Text    string
	Package string
}

var hunkRE = regexp.MustCompile(`^@@ -([0-9]+)(?:,([0-9]+))? \+([0-9]+)(?:,([0-9]+))? @@`)

// ParseUnifiedDiffString parses a string containing unified diff or PR patch content.
func ParseUnifiedDiffString(s string, limits Limits) (Diff, error) {
	return ParseUnifiedDiff(strings.NewReader(s), limits)
}

// ParseUnifiedDiff parses unified diff or PR patch content.
func ParseUnifiedDiff(r io.Reader, limits Limits) (Diff, error) {
	if limits.MaxBytes <= 0 {
		limits.MaxBytes = 16 << 20
	}
	var d Diff
	d.Complete = true
	br := bufio.NewReader(r)
	var cur *FileDiff
	var hunk *Hunk
	var inHunk bool
	var oldRemain, newRemain, oldLine, newLine int
	var bytesRead int64
	var lines int

	finishHunk := func() {
		if inHunk && (oldRemain != 0 || newRemain != 0) {
			d.Complete = false
			d.Warnings = append(d.Warnings, "truncated or mismatched hunk")
		}
		inHunk = false
		hunk = nil
		oldRemain, newRemain = 0, 0
	}
	ensureFile := func() *FileDiff {
		if cur == nil {
			d.Files = append(d.Files, FileDiff{})
			cur = &d.Files[len(d.Files)-1]
		}
		return cur
	}

	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			bytesRead += int64(len(line))
			lines++
			if bytesRead > limits.MaxBytes || (limits.MaxLines > 0 && lines > limits.MaxLines) {
				return d, fmt.Errorf("diff exceeds parser limits")
			}
			line = strings.TrimSuffix(line, "\n")
			line = strings.TrimSuffix(line, "\r")
			if inHunk {
				if startsDiffStructure(line) {
					finishHunk()
					// Fall through to the outer parser so this structural line is
					// handled as a file header or hunk header instead of content.
				} else {
					switch {
					case strings.HasPrefix(line, " "):
						text := strings.TrimPrefix(line, " ")
						if hunk != nil {
							hunk.Context = append(hunk.Context, ContextLine{OldLine: oldLine, NewLine: newLine, Text: text})
						}
						if pkg := packageName(text); pkg != "" {
							ensureFile().Package = pkg
						}
						oldRemain--
						newRemain--
						oldLine++
						newLine++
						continue
					case strings.HasPrefix(line, "+"):
						text := strings.TrimPrefix(line, "+")
						fd := ensureFile()
						if pkg := packageName(text); pkg != "" && fd.Package == "" {
							fd.Package = pkg
						}
						pkg := fd.Package
						added := AddedLine{Line: newLine, Text: text, Package: pkg}
						fd.Added = append(fd.Added, added)
						if hunk != nil {
							hunk.Candidates = append(hunk.Candidates, added)
						}
						newRemain--
						newLine++
						continue
					case strings.HasPrefix(line, "-"):
						oldRemain--
						oldLine++
						continue
					case strings.HasPrefix(line, `\ No newline`):
						continue
					}
					finishHunk()
				}
			}
			switch {
			case strings.HasPrefix(line, "diff --git "):
				finishHunk()
				parts := splitDiffGit(line)
				fd := FileDiff{}
				if len(parts) >= 2 {
					fd.OldPath = cleanDiffPath(parts[0])
					fd.NewPath = cleanDiffPath(parts[1])
				}
				d.Files = append(d.Files, fd)
				cur = &d.Files[len(d.Files)-1]
			case strings.HasPrefix(line, "rename from "):
				ensureFile().Renamed = true
				ensureFile().OldPath = cleanDiffPath(strings.TrimPrefix(line, "rename from "))
			case strings.HasPrefix(line, "rename to "):
				ensureFile().Renamed = true
				ensureFile().NewPath = cleanDiffPath(strings.TrimPrefix(line, "rename to "))
			case strings.HasPrefix(line, "Binary files "):
				ensureFile().Binary = true
			case strings.HasPrefix(line, "--- "):
				p := cleanHeaderPath(strings.TrimPrefix(line, "--- "))
				if p == "/dev/null" {
					ensureFile().Deleted = false
				} else {
					ensureFile().OldPath = p
				}
			case strings.HasPrefix(line, "+++ "):
				p := cleanHeaderPath(strings.TrimPrefix(line, "+++ "))
				if p == "/dev/null" {
					ensureFile().Deleted = true
				} else {
					ensureFile().NewPath = p
				}
			case strings.HasPrefix(line, "@@ "):
				m := hunkRE.FindStringSubmatch(line)
				if m == nil {
					d.Complete = false
					d.Warnings = append(d.Warnings, "invalid hunk header")
					continue
				}
				oldLine = atoi(m[1])
				oldRemain = countValue(m[2])
				newLine = atoi(m[3])
				newRemain = countValue(m[4])
				fd := ensureFile()
				fd.Hunks = append(fd.Hunks, Hunk{OldStart: oldLine, OldLines: oldRemain, NewStart: newLine, NewLines: newRemain})
				hunk = &fd.Hunks[len(fd.Hunks)-1]
				inHunk = true
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return d, err
		}
	}
	finishHunk()
	for i := range d.Files {
		if d.Files[i].NewPath == "" {
			d.Files[i].NewPath = d.Files[i].OldPath
		}
	}
	return d, nil
}

func startsDiffStructure(line string) bool {
	return strings.HasPrefix(line, "diff --git ") || strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") || strings.HasPrefix(line, "@@ ")
}

func splitDiffGit(line string) []string {
	rest := strings.TrimPrefix(line, "diff --git ")
	var out []string
	for rest != "" {
		rest = strings.TrimLeft(rest, " ")
		if rest == "" {
			break
		}
		if rest[0] == '"' {
			for i := 1; i < len(rest); i++ {
				if rest[i] == '"' && rest[i-1] != '\\' {
					out = append(out, rest[:i+1])
					rest = rest[i+1:]
					break
				}
			}
			if len(out) > 0 && out[len(out)-1][0] == '"' {
				continue
			}
		}
		i := strings.IndexByte(rest, ' ')
		if i < 0 {
			out = append(out, rest)
			break
		}
		out = append(out, rest[:i])
		rest = rest[i+1:]
	}
	return out
}

func cleanHeaderPath(s string) string {
	if i := strings.IndexByte(s, '\t'); i >= 0 {
		s = s[:i]
	}
	return cleanDiffPath(strings.TrimSpace(s))
}

func cleanDiffPath(s string) string {
	s = strings.TrimSpace(s)
	if unq, err := strconv.Unquote(s); err == nil {
		s = unq
	}
	s = strings.TrimPrefix(s, "a/")
	s = strings.TrimPrefix(s, "b/")
	return filepath.ToSlash(s)
}

func atoi(s string) int {
	v, _ := strconv.Atoi(s)
	return v
}

func countValue(s string) int {
	if s == "" {
		return 1
	}
	return atoi(s)
}

func packageName(line string) string {
	fields := strings.Fields(line)
	if len(fields) >= 2 && fields[0] == "package" {
		return fields[1]
	}
	return ""
}

// ChangedPackages returns changed Go packages using the nearest module root.
func ChangedPackages(files []FileDiff, moduleFiles []string) []string {
	moduleSet := map[string]bool{}
	for _, m := range moduleFiles {
		moduleSet[filepath.ToSlash(filepath.Dir(m))] = true
	}
	pkgs := map[string]bool{}
	for _, f := range files {
		p := f.NewPath
		if p == "" || !strings.HasSuffix(p, ".go") {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(p))
		mod := nearestModule(dir, moduleSet)
		rel := strings.TrimPrefix(strings.TrimPrefix(dir, mod), "/")
		if rel == "" {
			pkgs["."] = true
		} else {
			pkgs["./"+rel] = true
		}
	}
	out := make([]string, 0, len(pkgs))
	for p := range pkgs {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i] == "." {
			return false
		}
		if out[j] == "." {
			return true
		}
		return out[i] < out[j]
	})
	return out
}

func nearestModule(dir string, modules map[string]bool) string {
	for {
		if modules[dir] {
			return dir
		}
		next := filepath.ToSlash(filepath.Dir(dir))
		if next == "." || next == "/" || next == dir {
			break
		}
		dir = next
	}
	return "."
}
