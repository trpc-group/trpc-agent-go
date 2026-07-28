//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package diff

import (
	"path/filepath"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/finding"
)

// ExtractFileInfo extracts ChangedFileInfo from parsed ChangedFile entries.
// It populates the Go package path and test file flag for each file.
func ExtractFileInfo(changedFiles []*ChangedFile) []finding.ChangedFileInfo {
	infos := make([]finding.ChangedFileInfo, 0, len(changedFiles))
	for _, cf := range changedFiles {
		info := finding.ChangedFileInfo{
			File:       cf.File,
			Status:     cf.Status,
			Additions:  cf.Additions,
			Deletions:  cf.Deletions,
			Package:    PackageFromPath(cf.File),
			IsTestFile: IsTestFile(cf.File),
		}
		infos = append(infos, info)
	}
	return infos
}

// DiffSummary generates a human-readable summary of a diff.
func DiffSummary(changedFiles []*ChangedFile) string {
	if len(changedFiles) == 0 {
		return "no changes"
	}

	var parts []string
	parts = append(parts, formatFileCount(changedFiles))

	var added, modified, deleted int
	var totalAdd, totalDel int
	for _, f := range changedFiles {
		switch f.Status {
		case "added":
			added++
		case "deleted":
			deleted++
		default:
			modified++
		}
		totalAdd += f.Additions
		totalDel += f.Deletions
	}
	if added > 0 {
		parts = append(parts, formatCount(added, "added"))
	}
	if modified > 0 {
		parts = append(parts, formatCount(modified, "modified"))
	}
	if deleted > 0 {
		parts = append(parts, formatCount(deleted, "deleted"))
	}
	parts = append(parts, formatCount(totalAdd, "additions"), formatCount(totalDel, "deletions"))
	return strings.Join(parts, ", ")
}

// GoFileFilter returns only the .go files from a list of changed files.
func GoFileFilter(infos []finding.ChangedFileInfo) []finding.ChangedFileInfo {
	var result []finding.ChangedFileInfo
	for _, info := range infos {
		if filepath.Ext(info.File) == ".go" {
			result = append(result, info)
		}
	}
	return result
}

// NonTestFiles returns changed files that are not test files.
func NonTestFiles(infos []finding.ChangedFileInfo) []finding.ChangedFileInfo {
	var result []finding.ChangedFileInfo
	for _, info := range infos {
		if !info.IsTestFile {
			result = append(result, info)
		}
	}
	return result
}

func formatFileCount(files []*ChangedFile) string {
	if len(files) == 1 {
		return "1 file"
	}
	return formatCount(len(files), "files")
}

func formatCount(n int, label string) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n) + " " + label
}
