//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var hunkRE = regexp.MustCompile(`^@@ -([0-9]+)(?:,([0-9]+))? \+([0-9]+)(?:,([0-9]+))? @@`)

// ParseUnifiedDiff parses a unified diff into changed files and added lines.
func ParseUnifiedDiff(raw string) (DiffSummary, error) {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	sum := sha256.Sum256([]byte(raw))
	summary := DiffSummary{
		Raw:        raw,
		Hash:       hex.EncodeToString(sum[:]),
		Files:      []ChangedFile{},
		AddedLines: []AddedLine{},
		Packages:   []PackageInfo{},
	}
	if raw == "" {
		return summary, nil
	}
	lines := strings.Split(raw, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	summary.LineCount = len(lines)

	var current *ChangedFile
	var h *hunkState
	var lastMinusLine string
	for i, line := range lines {
		state, err := processDiffLine(&summary, diffParseState{
			current:       current,
			hunk:          h,
			lastMinusLine: lastMinusLine,
		}, line, i+1)
		if err != nil {
			return DiffSummary{}, err
		}
		current, h, lastMinusLine = state.current, state.hunk, state.lastMinusLine
	}
	if err := validateHunk(h); err != nil {
		return DiffSummary{}, err
	}
	summary.Packages = collectPackageInfo(summary)
	return summary, nil
}

type diffParseState struct {
	current       *ChangedFile
	hunk          *hunkState
	lastMinusLine string
}

func processDiffLine(summary *DiffSummary, state diffParseState, line string, lineNo int) (diffParseState, error) {
	if shouldConsumeHunkContent(state.hunk, line) {
		return consumeDiffContent(summary, state, line, lineNo)
	}
	switch {
	case strings.HasPrefix(line, "diff --git "):
		return startChangedFile(summary, state, line, lineNo)
	case strings.HasPrefix(line, "+++ "):
		return updateNewPath(summary, state, line, lineNo)
	case strings.HasPrefix(line, "@@ "):
		return startHunk(summary, state, line, lineNo)
	case strings.HasPrefix(line, "--- "):
		return rememberMinusHeader(state, line)
	default:
		return consumeDiffContent(summary, state, line, lineNo)
	}
}

func shouldConsumeHunkContent(h *hunkState, line string) bool {
	if h == nil || h.oldSeen >= h.oldWant && h.newSeen >= h.newWant {
		return false
	}
	if len(line) == 0 {
		return true
	}
	switch line[0] {
	case '+', '-', ' ', '\\':
		return true
	default:
		return false
	}
}

func startChangedFile(summary *DiffSummary, state diffParseState, line string, lineNo int) (diffParseState, error) {
	if err := validateHunk(state.hunk); err != nil {
		return state, err
	}
	oldPath, newPath, err := parseDiffGitLine(line)
	if err != nil {
		return state, fmt.Errorf("line %d: %w", lineNo, err)
	}
	summary.Files = append(summary.Files, ChangedFile{OldPath: oldPath, NewPath: newPath})
	state.current = &summary.Files[len(summary.Files)-1]
	state.hunk = nil
	return state, nil
}

func updateNewPath(summary *DiffSummary, state diffParseState, line string, lineNo int) (diffParseState, error) {
	if state.current == nil {
		next, err := syntheticPlainFile(summary, state.lastMinusLine, lineNo)
		if err != nil {
			return state, fmt.Errorf("line %d: +++ without diff header", lineNo)
		}
		state.current = next
	}
	p, deleted, err := parseHeaderPath(strings.TrimPrefix(line, "+++ "))
	if err != nil {
		return state, fmt.Errorf("line %d: %w", lineNo, err)
	}
	if !deleted {
		state.current.NewPath = p
	}
	state.current.Deleted = deleted
	summary.Files[len(summary.Files)-1] = *state.current
	return state, nil
}

func startHunk(summary *DiffSummary, state diffParseState, line string, lineNo int) (diffParseState, error) {
	if state.current == nil {
		next, err := syntheticPlainFile(summary, state.lastMinusLine, lineNo)
		if err != nil {
			return state, err
		}
		state.current = next
	}
	if err := validateHunk(state.hunk); err != nil {
		return state, err
	}
	next, err := parseHunkHeader(lineNo, line)
	if err != nil {
		return state, err
	}
	state.hunk = &next
	return state, nil
}

func syntheticPlainFile(summary *DiffSummary, lastMinusLine string, lineNo int) (*ChangedFile, error) {
	p, deleted, err := parseHeaderPath(strings.TrimPrefix(lastMinusLine, "--- "))
	if err == nil && !deleted && p != "" {
		summary.Files = append(summary.Files, ChangedFile{OldPath: p, NewPath: p})
		return &summary.Files[len(summary.Files)-1], nil
	}
	return nil, fmt.Errorf("line %d: hunk without file", lineNo)
}

func rememberMinusHeader(state diffParseState, line string) (diffParseState, error) {
	if state.hunk != nil {
		if err := validateHunk(state.hunk); err != nil {
			return state, err
		}
		state.hunk = nil
		state.current = nil
	}
	if state.current == nil {
		state.lastMinusLine = line
	}
	return state, nil
}

func consumeDiffContent(summary *DiffSummary, state diffParseState, line string, lineNo int) (diffParseState, error) {
	if state.hunk == nil {
		return state, nil
	}
	if len(line) == 0 {
		return state, fmt.Errorf("line %d: malformed hunk line", lineNo)
	}
	if err := consumeHunkLine(summary, state.current, state.hunk, line); err != nil {
		return state, fmt.Errorf("line %d: %w", lineNo, err)
	}
	return state, nil
}

type hunkState struct {
	line     int
	oldLine  int
	newLine  int
	oldWant  int
	newWant  int
	oldSeen  int
	newSeen  int
	newStart int
}

func parseHunkHeader(lineNo int, line string) (hunkState, error) {
	m := hunkRE.FindStringSubmatch(line)
	if m == nil {
		return hunkState{}, fmt.Errorf("line %d: invalid hunk header", lineNo)
	}
	oldStart, _ := strconv.Atoi(m[1])
	newStart, _ := strconv.Atoi(m[3])
	oldCount := hunkCount(m[2])
	newCount := hunkCount(m[4])
	return hunkState{
		line:     lineNo,
		oldLine:  oldStart,
		newLine:  newStart,
		oldWant:  oldCount,
		newWant:  newCount,
		newStart: newStart,
	}, nil
}

func hunkCount(raw string) int {
	if raw == "" {
		return 1
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 1
	}
	return n
}

func consumeHunkLine(summary *DiffSummary, file *ChangedFile, h *hunkState, line string) error {
	switch line[0] {
	case '+':
		if file != nil && !file.Deleted {
			summary.AddedLines = append(summary.AddedLines, AddedLine{
				File:    file.NewPath,
				Line:    h.newLine,
				Content: line[1:],
			})
		}
		h.newLine++
		h.newSeen++
	case '-':
		h.oldLine++
		h.oldSeen++
	case ' ':
		h.oldLine++
		h.newLine++
		h.oldSeen++
		h.newSeen++
	case '\\':
	default:
		return errors.New("unknown hunk prefix")
	}
	return nil
}

func validateHunk(h *hunkState) error {
	if h == nil {
		return nil
	}
	if h.oldSeen != h.oldWant || h.newSeen != h.newWant {
		return fmt.Errorf(
			"line %d: hunk count mismatch old=%d/%d new=%d/%d",
			h.line, h.oldSeen, h.oldWant, h.newSeen, h.newWant,
		)
	}
	return nil
}

func parseDiffGitLine(line string) (string, string, error) {
	rest := strings.TrimPrefix(line, "diff --git ")
	left, rest, err := readGitPathToken(rest)
	if err != nil {
		return "", "", err
	}
	right, rest, err := readGitPathToken(strings.TrimLeft(rest, " "))
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(rest) != "" {
		return "", "", fmt.Errorf("unexpected diff header suffix %q", rest)
	}
	oldPath, err := normalizeDiffPath(left)
	if err != nil {
		return "", "", err
	}
	newPath, err := normalizeDiffPath(right)
	if err != nil {
		return "", "", err
	}
	return oldPath, newPath, nil
}

func readGitPathToken(s string) (string, string, error) {
	if s == "" {
		return "", "", errors.New("missing path token")
	}
	if s[0] != '"' {
		i := strings.IndexByte(s, ' ')
		if i < 0 {
			return s, "", nil
		}
		return s[:i], s[i:], nil
	}
	escaped := false
	for i := 1; i < len(s); i++ {
		switch {
		case escaped:
			escaped = false
		case s[i] == '\\':
			escaped = true
		case s[i] == '"':
			u, err := strconv.Unquote(s[:i+1])
			if err != nil {
				return "", "", err
			}
			return u, s[i+1:], nil
		}
	}
	return "", "", errors.New("unterminated quoted path")
}

func parseHeaderPath(raw string) (string, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "/dev/null" {
		return "", true, nil
	}
	p, err := normalizeDiffPath(raw)
	return p, false, err
}

func normalizeDiffPath(raw string) (string, error) {
	if strings.HasPrefix(raw, "\"") {
		unquoted, err := strconv.Unquote(raw)
		if err != nil {
			return "", err
		}
		raw = unquoted
	}
	if strings.HasPrefix(raw, "a/") || strings.HasPrefix(raw, "b/") {
		raw = raw[2:]
	}
	raw = strings.ReplaceAll(raw, "\\", "/")
	return validateRepoPath(raw)
}

func validateRepoPath(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("empty path")
	}
	if strings.ContainsRune(raw, '\x00') {
		return "", fmt.Errorf("path contains NUL: %q", raw)
	}
	if strings.Contains(raw, ":") || strings.HasPrefix(raw, "/") {
		return "", fmt.Errorf("absolute or drive path is not allowed: %q", raw)
	}
	clean := path.Clean(raw)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("path traversal is not allowed: %q", raw)
	}
	return clean, nil
}

func collectPackageInfo(summary DiffSummary) []PackageInfo {
	pkgs := make(map[string]*PackageInfo)
	for _, file := range summary.Files {
		p := file.NewPath
		if p == "" {
			p = file.OldPath
		}
		if filepath.Ext(p) != ".go" {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(p))
		if dir == "." {
			dir = ""
		}
		info := pkgs[dir]
		if info == nil {
			info = &PackageInfo{Dir: dir}
			pkgs[dir] = info
		}
		info.GoFiles++
	}
	for _, line := range summary.AddedLines {
		if filepath.Ext(line.File) != ".go" {
			continue
		}
		name, ok := parsePackageName(line.Content)
		if !ok {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(line.File))
		if dir == "." {
			dir = ""
		}
		info := pkgs[dir]
		if info == nil {
			info = &PackageInfo{Dir: dir}
			pkgs[dir] = info
		}
		if info.Name == "" {
			info.Name = name
		}
	}
	out := make([]PackageInfo, 0, len(pkgs))
	for _, info := range pkgs {
		out = append(out, *info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Dir < out[j].Dir })
	return out
}

func parsePackageName(content string) (string, bool) {
	fields := strings.Fields(content)
	if len(fields) < 2 || fields[0] != "package" {
		return "", false
	}
	name := strings.TrimSpace(fields[1])
	if name == "" {
		return "", false
	}
	for _, r := range name {
		if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return "", false
		}
	}
	return name, true
}
