//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package scripts

import (
	"fmt"
	"sort"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/diffparse"
)

// DiffSummaryResult is the structured output produced by the diff summary
// script helper.
type DiffSummaryResult struct {
	ChangedFileCount int      `json:"changed_file_count"`
	AddedLineCount   int      `json:"added_line_count"`
	DeletedLineCount int      `json:"deleted_line_count"`
	Files            []string `json:"files"`
}

// DiffSummary parses a unified diff and returns a deterministic summary. Skill
// orchestration can call this helper instead of duplicating diff parsing logic.
func DiffSummary(diff string) (DiffSummaryResult, error) {
	files, err := diffparse.Parse(diff)
	if err != nil {
		return DiffSummaryResult{}, err
	}
	result := DiffSummaryResult{ChangedFileCount: len(files)}
	seen := map[string]bool{}
	for _, file := range files {
		name := file.NewPath
		if name == "" {
			name = file.OldPath
		}
		if name != "" && !seen[name] {
			seen[name] = true
			result.Files = append(result.Files, name)
		}
		for _, hunk := range file.Hunks {
			for _, line := range hunk.Lines {
				switch line.Kind {
				case "add":
					result.AddedLineCount++
				case "delete":
					result.DeletedLineCount++
				}
			}
		}
	}
	sort.Strings(result.Files)
	return result, nil
}

// DiffSummaryText renders the summary as stable human-readable text.
func DiffSummaryText(diff string) (string, error) {
	summary, err := DiffSummary(diff)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("changed_files=%d added_lines=%d deleted_lines=%d files=%v",
		summary.ChangedFileCount, summary.AddedLineCount, summary.DeletedLineCount, summary.Files), nil
}
