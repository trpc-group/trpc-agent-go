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
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
)

const (
	maxDiffBytes = 8 << 20
	maxDiffFiles = 2000
	maxDiffLines = 200000
)

var (
	hunkHeaderPattern = regexp.MustCompile(
		`^@@ -([0-9]+)(?:,([0-9]+))? \+([0-9]+)(?:,([0-9]+))? @@(.*)$`,
	)
	packagePattern = regexp.MustCompile(`^\s*package\s+([A-Za-z_][A-Za-z0-9_]*)\b`)
)

// ParseUnifiedDiff parses bounded unified diff input without touching the host.
func ParseUnifiedDiff(data []byte) (ParsedDiff, error) {
	if len(data) == 0 {
		return ParsedDiff{}, errors.New("diff is empty")
	}
	if len(data) > maxDiffBytes {
		return ParsedDiff{}, fmt.Errorf(
			"diff exceeds %d-byte input limit", maxDiffBytes,
		)
	}

	var result ParsedDiff
	var current *ChangedFile
	var hunk *Hunk
	oldLine, newLine := 0, 0
	lineCount := 0
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), maxDiffBytes)
	for scanner.Scan() {
		lineCount++
		if lineCount > maxDiffLines {
			return ParsedDiff{}, fmt.Errorf(
				"diff exceeds %d-line input limit", maxDiffLines,
			)
		}
		line := strings.TrimSuffix(scanner.Text(), "\r")
		switch {
		case strings.HasPrefix(line, "diff --git "):
			var err error
			current, err = appendDiffFile(&result, line)
			if err != nil {
				return ParsedDiff{}, err
			}
			hunk = nil
		case strings.HasPrefix(line, "--- "):
			if current == nil {
				current = &ChangedFile{}
				result.Files = append(result.Files, *current)
				current = &result.Files[len(result.Files)-1]
			}
			current.OldPath = normalizePatchPath(diffMarkerPath(line[4:]))
		case strings.HasPrefix(line, "+++ "):
			if current == nil {
				return ParsedDiff{}, errors.New("new path appears before file header")
			}
			current.Path = normalizePatchPath(diffMarkerPath(line[4:]))
			if err := validateDiffPath(current.Path); err != nil {
				return ParsedDiff{}, err
			}
		case strings.HasPrefix(line, "@@ "):
			if current == nil {
				return ParsedDiff{}, errors.New("hunk appears before file header")
			}
			parsed, err := parseHunkHeader(line)
			if err != nil {
				return ParsedDiff{}, err
			}
			current.Hunks = append(current.Hunks, parsed)
			hunk = &current.Hunks[len(current.Hunks)-1]
			oldLine, newLine = hunk.OldStart, hunk.NewStart
			if current.Package == "" {
				if match := packagePattern.FindStringSubmatch(hunk.Header); len(match) == 2 {
					current.Package = match[1]
				}
			}
		case hunk != nil:
			if strings.HasPrefix(line, `\ No newline at end of file`) {
				continue
			}
			if line == "" {
				return ParsedDiff{}, errors.New("malformed hunk line without prefix")
			}
			entry := DiffLine{Kind: line[0], Text: line[1:]}
			switch entry.Kind {
			case ' ':
				entry.OldLine, entry.NewLine = oldLine, newLine
				oldLine++
				newLine++
			case '-':
				entry.OldLine = oldLine
				oldLine++
			case '+':
				entry.NewLine = newLine
				newLine++
			default:
				return ParsedDiff{}, fmt.Errorf(
					"unsupported hunk line prefix %q", entry.Kind,
				)
			}
			hunk.Lines = append(hunk.Lines, entry)
			if current.Package == "" && entry.Kind != '-' {
				if match := packagePattern.FindStringSubmatch(entry.Text); len(match) == 2 {
					current.Package = match[1]
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return ParsedDiff{}, fmt.Errorf("scan diff: %w", err)
	}
	if len(result.Files) == 0 {
		return ParsedDiff{}, errors.New("diff contains no changed files")
	}
	for i := range result.Files {
		if result.Files[i].Path == "" {
			result.Files[i].Path = result.Files[i].OldPath
		}
		if err := validateDiffPath(result.Files[i].Path); err != nil {
			return ParsedDiff{}, err
		}
	}
	return result, nil
}

func appendDiffFile(result *ParsedDiff, header string) (*ChangedFile, error) {
	if len(result.Files) >= maxDiffFiles {
		return nil, fmt.Errorf("diff exceeds %d-file input limit", maxDiffFiles)
	}
	fields := strings.Fields(header)
	if len(fields) < 4 {
		return nil, fmt.Errorf("malformed diff header %q", header)
	}
	oldPath := normalizePatchPath(fields[2])
	newPath := normalizePatchPath(fields[3])
	if err := validateDiffPath(newPath); err != nil {
		return nil, err
	}
	result.Files = append(result.Files, ChangedFile{
		OldPath: oldPath,
		Path:    newPath,
	})
	return &result.Files[len(result.Files)-1], nil
}

func parseHunkHeader(line string) (Hunk, error) {
	match := hunkHeaderPattern.FindStringSubmatch(line)
	if len(match) != 6 {
		return Hunk{}, fmt.Errorf("malformed hunk header %q", line)
	}
	oldStart, _ := strconv.Atoi(match[1])
	newStart, _ := strconv.Atoi(match[3])
	return Hunk{
		OldStart: oldStart,
		OldCount: parseOptionalCount(match[2]),
		NewStart: newStart,
		NewCount: parseOptionalCount(match[4]),
		Header:   strings.TrimSpace(match[5]),
	}, nil
}

func parseOptionalCount(value string) int {
	if value == "" {
		return 1
	}
	n, _ := strconv.Atoi(value)
	return n
}

func diffMarkerPath(value string) string {
	if tab := strings.IndexByte(value, '\t'); tab >= 0 {
		value = value[:tab]
	}
	return strings.TrimSpace(value)
}

func normalizePatchPath(value string) string {
	value = strings.Trim(strings.TrimSpace(value), `"`)
	if value == "/dev/null" {
		return ""
	}
	value = strings.TrimPrefix(value, "a/")
	value = strings.TrimPrefix(value, "b/")
	return path.Clean(strings.ReplaceAll(value, `\`, "/"))
}

func validateDiffPath(value string) error {
	if value == "" {
		return nil
	}
	if path.IsAbs(value) || value == ".." || strings.HasPrefix(value, "../") {
		return fmt.Errorf("unsafe diff path %q", value)
	}
	return nil
}
