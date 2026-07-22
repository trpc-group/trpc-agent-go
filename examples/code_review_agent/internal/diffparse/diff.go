//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package diffparse converts unified diffs into review-oriented structures.
package diffparse

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	sourcediff "github.com/sourcegraph/go-diff/diff"
)

// ChangedLine is one line from a diff hunk.
type ChangedLine struct {
	Kind    byte
	Content string
	OldLine int32
	NewLine int32
}

// Hunk describes one changed region.
type Hunk struct {
	OldStart int32
	NewStart int32
	Section  string
	Lines    []ChangedLine
}

// ChangedFile describes one file in a unified diff.
type ChangedFile struct {
	OldPath string
	NewPath string
	Renamed bool
	Deleted bool
	Binary  bool
	Hunks   []Hunk
}

// Parse parses a multi-file unified diff with sourcegraph/go-diff.
func Parse(data []byte) ([]ChangedFile, error) {
	parsed, err := sourcediff.ParseMultiFileDiff(data)
	if err != nil {
		return nil, fmt.Errorf("parse unified diff: %w", err)
	}
	if len(parsed) == 0 && len(bytes.TrimSpace(data)) != 0 {
		return nil, fmt.Errorf("parse unified diff: no file headers")
	}
	files := make([]ChangedFile, 0, len(parsed))
	for _, file := range parsed {
		extended := strings.Join(file.Extended, "\n")
		changed := ChangedFile{
			OldPath: cleanDiffPath(file.OrigName),
			NewPath: cleanDiffPath(file.NewName),
			Binary: len(file.Hunks) == 0 && (strings.Contains(extended, "Binary files") ||
				strings.Contains(extended, "GIT binary patch")),
		}
		changed.Deleted = file.NewName == "/dev/null"
		changed.Renamed = changed.OldPath != changed.NewPath && !changed.Deleted && file.OrigName != "/dev/null"
		for _, hunk := range file.Hunks {
			changed.Hunks = append(changed.Hunks, parseHunk(hunk))
		}
		files = append(files, changed)
	}
	return files, nil
}

func parseHunk(hunk *sourcediff.Hunk) Hunk {
	result := Hunk{OldStart: hunk.OrigStartLine, NewStart: hunk.NewStartLine, Section: hunk.Section}
	oldLine, newLine := hunk.OrigStartLine, hunk.NewStartLine
	for _, raw := range bytes.Split(hunk.Body, []byte{'\n'}) {
		if len(raw) == 0 || raw[0] == '\\' {
			continue
		}
		line := ChangedLine{Kind: raw[0], Content: string(raw[1:])}
		switch raw[0] {
		case '+':
			line.NewLine = newLine
			newLine++
		case '-':
			line.OldLine = oldLine
			oldLine++
		default:
			line.OldLine, line.NewLine = oldLine, newLine
			oldLine++
			newLine++
		}
		result.Lines = append(result.Lines, line)
	}
	return result
}

func cleanDiffPath(path string) string {
	path = strings.TrimPrefix(path, "a/")
	path = strings.TrimPrefix(path, "b/")
	if path == "/dev/null" {
		return ""
	}
	return filepath.ToSlash(filepath.Clean(path))
}

// Stats returns total hunk and added-line counts.
func Stats(files []ChangedFile) (int, int) {
	hunks, added := 0, 0
	for _, file := range files {
		hunks += len(file.Hunks)
		for _, hunk := range file.Hunks {
			for _, line := range hunk.Lines {
				if line.Kind == '+' {
					added++
				}
			}
		}
	}
	return hunks, added
}
