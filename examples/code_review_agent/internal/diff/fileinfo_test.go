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
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/finding"
)

func TestExtractFileInfo(t *testing.T) {
	files := []*ChangedFile{
		{File: "main.go", Status: "modified", Additions: 3, Deletions: 1},
		{File: "handler_test.go", Status: "added", Additions: 10, Deletions: 0},
		{File: "old.go", Status: "deleted", Additions: 0, Deletions: 5},
	}
	infos := ExtractFileInfo(files)
	assert.Len(t, infos, 3)

	assert.Equal(t, "main.go", infos[0].File)
	assert.Equal(t, "modified", infos[0].Status)
	assert.Equal(t, 3, infos[0].Additions)
	assert.Equal(t, 1, infos[0].Deletions)
	assert.Equal(t, "", infos[0].Package) // no directory
	assert.False(t, infos[0].IsTestFile)

	assert.True(t, infos[1].IsTestFile) // _test.go
	assert.Equal(t, "deleted", infos[2].Status)
}

func TestExtractFileInfo_Empty(t *testing.T) {
	infos := ExtractFileInfo(nil)
	assert.Empty(t, infos)

	infos = ExtractFileInfo([]*ChangedFile{})
	assert.Empty(t, infos)
}

func TestDiffSummary(t *testing.T) {
	tests := []struct {
		name     string
		files    []*ChangedFile
		contains []string
	}{
		{
			name:     "empty",
			files:    nil,
			contains: []string{"no changes"},
		},
		{
			name: "single modified",
			files: []*ChangedFile{
				{File: "main.go", Status: "modified", Additions: 2, Deletions: 1},
			},
			contains: []string{"1 file", "modified", "2 additions", "1 deletion"},
		},
		{
			name: "multi file",
			files: []*ChangedFile{
				{File: "a.go", Status: "added", Additions: 10, Deletions: 0},
				{File: "b.go", Status: "deleted", Additions: 0, Deletions: 5},
				{File: "c.go", Status: "modified", Additions: 1, Deletions: 1},
			},
			contains: []string{"3 files", "added", "deleted", "modified"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			summary := DiffSummary(tt.files)
			for _, c := range tt.contains {
				assert.Contains(t, summary, c)
			}
		})
	}
}

func TestGoFileFilter(t *testing.T) {
	infos := []finding.ChangedFileInfo{
		{File: "main.go"},
		{File: "README.md"},
		{File: "Makefile"},
		{File: "internal/handler.go"},
	}
	filtered := GoFileFilter(infos)
	assert.Len(t, filtered, 2)
	assert.Equal(t, "main.go", filtered[0].File)
	assert.Equal(t, "internal/handler.go", filtered[1].File)
}

func TestNonTestFiles(t *testing.T) {
	infos := []finding.ChangedFileInfo{
		{File: "handler.go", IsTestFile: false},
		{File: "handler_test.go", IsTestFile: true},
		{File: "main.go", IsTestFile: false},
	}
	filtered := NonTestFiles(infos)
	assert.Len(t, filtered, 2)
	assert.Equal(t, "handler.go", filtered[0].File)
	assert.Equal(t, "main.go", filtered[1].File)
}

func TestPackageFromPath_ExtractFileInfo(t *testing.T) {
	// Verify that ExtractFileInfo uses PackageFromPath correctly.
	files := []*ChangedFile{
		{File: "cmd/server/main.go"},
		{File: "internal/handler/user.go"},
	}
	infos := ExtractFileInfo(files)
	assert.Equal(t, "cmd.server", infos[0].Package)
	assert.Equal(t, "internal.handler", infos[1].Package)
}
