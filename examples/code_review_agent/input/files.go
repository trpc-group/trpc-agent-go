//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package input

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ParseFileList builds a DiffBundle from explicit file paths.
// Each file is treated as fully added content (useful for reviewing a
// candidate file set without a git patch).
func ParseFileList(paths []string) (*DiffBundle, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("empty file list")
	}
	var files []ChangedFile
	var raw strings.Builder
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, err
		}
		b, err := os.ReadFile(abs)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		rel := filepath.ToSlash(p)
		cf := ChangedFile{
			Path:     rel,
			Language: detectLanguage(rel),
		}
		text := strings.ReplaceAll(string(b), "\r\n", "\n")
		sc := bufio.NewScanner(strings.NewReader(text))
		sc.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
		hunk := Hunk{Header: "@@ -0,0 +1 @@", NewStart: 1}
		lineNo := 1
		fmt.Fprintf(&raw, "diff --git a/%s b/%s\n", rel, rel)
		fmt.Fprintf(&raw, "--- /dev/null\n+++ b/%s\n", rel)
		fmt.Fprintf(&raw, "@@ -0,0 +1 @@\n")
		for sc.Scan() {
			line := sc.Text()
			hunk.Lines = append(hunk.Lines, DiffLine{
				Kind:      '+',
				Text:      line,
				NewLineNo: lineNo,
			})
			fmt.Fprintf(&raw, "+%s\n", line)
			lineNo++
		}
		if err := sc.Err(); err != nil {
			return nil, err
		}
		hunk.NewLines = len(hunk.Lines)
		cf.Hunks = []Hunk{hunk}
		cf.Package = detectPackage(cf)
		files = append(files, cf)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no readable files in list")
	}
	rawText := raw.String()
	added, removed := countChanges(files)
	return &DiffBundle{
		Kind:        "file_list",
		Digest:      sha256Hex(rawText),
		Summary:     fmt.Sprintf("%d files, +%d/-%d", len(files), added, removed),
		RawRedacted: rawText,
		Files:       files,
	}, nil
}

// ParseFilesFlag parses a comma-separated --files value and optional
// newline-separated list file path via @path syntax.
func ParseFilesFlag(value string) ([]string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, fmt.Errorf("empty --files value")
	}
	if strings.HasPrefix(value, "@") {
		b, err := os.ReadFile(strings.TrimPrefix(value, "@"))
		if err != nil {
			return nil, err
		}
		var out []string
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			out = append(out, line)
		}
		return out, nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out, nil
}
