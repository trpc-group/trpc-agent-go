//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
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

	var files []review.ChangedFile
	var current *review.ChangedFile
	var currentHunk *review.Hunk
	oldLine, newLine := 0, 0

	flushHunk := func() {
		if current != nil && currentHunk != nil {
			current.Hunks = append(current.Hunks, *currentHunk)
			currentHunk = nil
		}
	}
	flushFile := func() {
		flushHunk()
		if current != nil && current.NewPath != "" {
			current.Language = languageForPath(current.NewPath)
			current.PackageName = detectPackage(*current)
			files = append(files, *current)
		}
		current = nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flushFile()
			current = &review.ChangedFile{}
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				current.OldPath = cleanDiffPath(parts[2])
				current.NewPath = cleanDiffPath(parts[3])
			}
		case strings.HasPrefix(line, "--- "):
			// A plain unified diff has no "diff --git" header, so a new
			// "--- " line after a file with parsed hunks starts the next
			// file. The hunk check keeps git-style headers (which set
			// NewPath before any hunk) attached to the same file.
			if current != nil && current.NewPath != "" &&
				(len(current.Hunks) > 0 || currentHunk != nil) {
				flushFile()
			}
			if current == nil {
				current = &review.ChangedFile{}
			}
			current.OldPath = cleanDiffPath(strings.TrimSpace(strings.TrimPrefix(line, "--- ")))
		case strings.HasPrefix(line, "+++ "):
			if current == nil {
				current = &review.ChangedFile{}
			}
			current.NewPath = cleanDiffPath(strings.TrimSpace(strings.TrimPrefix(line, "+++ ")))
		case strings.HasPrefix(line, "@@ "):
			if current == nil {
				current = &review.ChangedFile{}
			}
			flushHunk()
			m := hunkRE.FindStringSubmatch(line)
			if m == nil {
				return nil, fmt.Errorf("invalid hunk header %q", line)
			}
			oldStart := atoiDefault(m[1], 0)
			oldCount := atoiDefault(m[2], 1)
			newStart := atoiDefault(m[3], 0)
			newCount := atoiDefault(m[4], 1)
			oldLine, newLine = oldStart, newStart
			currentHunk = &review.Hunk{
				OldStart: oldStart,
				OldCount: oldCount,
				NewStart: newStart,
				NewCount: newCount,
				Header:   strings.TrimSpace(m[5]),
			}
		default:
			if currentHunk == nil {
				continue
			}
			switch {
			case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
				content := strings.TrimPrefix(line, "+")
				currentHunk.Lines = append(currentHunk.Lines, review.DiffLine{
					Kind: "added", NewLine: newLine, Content: content,
				})
				newLine++
			case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
				content := strings.TrimPrefix(line, "-")
				currentHunk.Lines = append(currentHunk.Lines, review.DiffLine{
					Kind: "removed", OldLine: oldLine, Content: content,
				})
				oldLine++
			case strings.HasPrefix(line, " "):
				content := strings.TrimPrefix(line, " ")
				currentHunk.Lines = append(currentHunk.Lines, review.DiffLine{
					Kind: "context", OldLine: oldLine, NewLine: newLine, Content: content,
				})
				oldLine++
				newLine++
			case strings.HasPrefix(line, `\ No newline at end of file`):
				continue
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	flushFile()
	return files, nil
}

// cleanDiffPath normalizes a diff header path, dropping a/ b/ prefixes and /dev/null.
func cleanDiffPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.Trim(path, `"`)
	if path == "/dev/null" {
		return ""
	}
	path = strings.TrimPrefix(path, "a/")
	path = strings.TrimPrefix(path, "b/")
	return filepath.ToSlash(path)
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
