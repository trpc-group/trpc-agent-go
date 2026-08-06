//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package diffparser parses unified diff input into structured FileChange data.
package diffparser

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/state"
	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/types"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

// Run is the DiffParser GraphAgent node.
// Reads input_diff_file or input_diff_text from state, writes file_changes.
func Run(ctx context.Context, gs graph.State) (any, error) {
	start := time.Now()
	defer func() { gs[state.StateKeyNodeDiffParserMs] = time.Since(start).Milliseconds() }()

	var diffText string
	if path, ok := gs[state.StateKeyInputDiffFile].(string); ok && path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read diff file %s: %w", path, err)
		}
		diffText = string(data)
	} else if text, ok := gs[state.StateKeyInputDiffText].(string); ok && text != "" {
		diffText = text
	} else {
		return nil, fmt.Errorf("no diff input: set %s or %s",
			state.StateKeyInputDiffFile, state.StateKeyInputDiffText)
	}

	changes, err := Parse(diffText)
	if err != nil {
		return nil, fmt.Errorf("parse diff: %w", err)
	}

	gs[state.StateKeyFileChanges] = changes
	return gs, nil
}

// Parse reads a unified diff string and returns structured FileChange data.
func Parse(diffText string) ([]types.FileChange, error) {
	var changes []types.FileChange
	var current *types.FileChange
	var currentHunk *types.Hunk

	fileHeaderRe := regexp.MustCompile(`^diff --git a/(.+) b/(.+)$`)
	oldStartRe := regexp.MustCompile(`^--- a/(.+)$`)
	newStartRe := regexp.MustCompile(`^\+\+\+ b/(.+)$`)
	hunkHeaderRe := regexp.MustCompile(`^@@ -(\d+),?(\d*) \+(\d+),?(\d*) @@(.*)$`)

	scanner := bufio.NewScanner(strings.NewReader(diffText))
	for scanner.Scan() {
		line := scanner.Text()

		if m := fileHeaderRe.FindStringSubmatch(line); m != nil {
			if current != nil {
				if currentHunk != nil {
					current.Hunks = append(current.Hunks, *currentHunk)
				}
				changes = append(changes, *current)
			}
			current = &types.FileChange{
				FilePath: m[2],
				Language: "go",
			}
			currentHunk = nil
			continue
		}

		if current == nil {
			continue
		}

		if m := oldStartRe.FindStringSubmatch(line); m != nil {
			continue // old file path, already captured from header
		}
		if m := newStartRe.FindStringSubmatch(line); m != nil {
			current.FilePath = m[1] // prefer new file path
			continue
		}

		if m := hunkHeaderRe.FindStringSubmatch(line); m != nil {
			if currentHunk != nil {
				current.Hunks = append(current.Hunks, *currentHunk)
			}
			oldStart := parseInt(m[1])
			oldCount := parseIntOr(m[2], 1)
			newStart := parseInt(m[3])
			newCount := parseIntOr(m[4], 1)
			currentHunk = &types.Hunk{
				OldStart: oldStart,
				OldCount: oldCount,
				NewStart: newStart,
				NewCount: newCount,
				Header:   strings.TrimSpace(m[5]),
			}
			// Update file-level line tracking
			if current.NewStart == 0 {
				current.NewStart = newStart
			}
			if current.OldStart == 0 {
				current.OldStart = oldStart
			}
			continue
		}

		if currentHunk == nil {
			continue
		}

		if len(line) == 0 {
			continue
		}
		lineType := " "
		if line[0] == '+' {
			lineType = "+"
		} else if line[0] == '-' {
			lineType = "-"
		}

		currentHunk.Lines = append(currentHunk.Lines, types.Line{
			Type:    lineType,
			OldLine: currentHunk.OldStart + len(currentHunk.Lines),
			NewLine: currentHunk.NewStart + len(currentHunk.Lines),
			Content: line,
		})
	}

	if current != nil {
		if currentHunk != nil {
			current.Hunks = append(current.Hunks, *currentHunk)
		}
		changes = append(changes, *current)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan diff: %w", err)
	}

	// Infer package names from file paths
	for i := range changes {
		changes[i].PackageName = inferPackage(changes[i].FilePath)
	}

	return changes, nil
}

func inferPackage(path string) string {
	parts := strings.Split(path, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if strings.HasSuffix(parts[i], ".go") {
			if i > 0 {
				return parts[i-1]
			}
			return "main"
		}
	}
	return ""
}

func parseInt(s string) int {
	var n int
	fmt.Sscanf(s, "%d", &n)
	return n
}

func parseIntOr(s string, def int) int {
	if s == "" {
		return def
	}
	return parseInt(s)
}
