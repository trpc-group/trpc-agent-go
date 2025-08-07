//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//

package file

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFileTool_SearchFile(t *testing.T) {
	// Create a temporary directory for testing.
	tempDir := t.TempDir()
	fileToolSet := &fileToolSet{baseDir: tempDir}
	// Create test directory structure with files.
	testFiles := map[string]struct{}{
		// Root directory files
		"main.go":       {},
		"helper.go":     {},
		"util.go":       {},
		"file1.txt":     {},
		"file2.txt":     {},
		"file3.txt":     {},
		"data1.csv":     {},
		"data2.csv":     {},
		"file_data.csv": {},
		"README.md":     {},
		"subdir.md":     {},
		// Subdirectory files
		"subdir1/helper.go":  {},
		"subdir1/data.txt":   {},
		"subdir1/test.csv":   {},
		"subdir2/util.go":    {},
		"subdir2/config.go":  {},
		"subdir2/backup.txt": {},
		// Third level directory files
		"subdir1/nested/app.go":      {},
		"subdir1/nested/utils.go":    {},
		"subdir1/nested/data.txt":    {},
		"subdir2/nested/core.go":     {},
		"subdir2/nested/helper.go":   {},
		"subdir2/nested/config.json": {},
	}
	// Create directories and files.
	for filePath := range testFiles {
		fullPath := filepath.Join(tempDir, filePath)
		// Ensure parent directory exists.
		parentDir := filepath.Dir(fullPath)
		if parentDir != tempDir {
			err := os.MkdirAll(parentDir, 0755)
			assert.NoError(t, err)
		}
		err := os.WriteFile(fullPath, []byte{}, 0644)
		assert.NoError(t, err)
	}
	tests := []struct {
		name            string
		path            string
		pattern         string
		expectedFiles   []string
		expectedFolders []string
		expectError     bool
	}{
		{
			name:          "BasicExtensionPattern",
			pattern:       "*.txt",
			expectedFiles: []string{"file1.txt", "file2.txt", "file3.txt"},
		},
		{
			name:          "FilePrefixPattern",
			pattern:       "file*.csv",
			expectedFiles: []string{"file_data.csv"},
		},
		{
			name:          "SubdirPattern",
			pattern:       "subdir2/*.go",
			expectedFiles: []string{"subdir2/util.go", "subdir2/config.go"},
		},
		{
			name:          "WildcardBothSides",
			pattern:       "*file*",
			expectedFiles: []string{"file1.txt", "file2.txt", "file3.txt", "file_data.csv"},
		},
		{
			name:          "ThirdLevelPattern",
			pattern:       "subdir1/nested/*.go",
			expectedFiles: []string{"subdir1/nested/app.go", "subdir1/nested/utils.go"},
		},
		{
			name:    "AllGoPattern",
			pattern: "**/*.go",
			expectedFiles: []string{
				"main.go", "helper.go", "util.go",
				"subdir1/helper.go", "subdir1/nested/app.go", "subdir1/nested/utils.go",
				"subdir2/util.go", "subdir2/config.go", "subdir2/nested/core.go", "subdir2/nested/helper.go",
			},
		},
		{
			name:            "SubdirPattern",
			pattern:         "subdir*",
			expectedFiles:   []string{"subdir.md"},
			expectedFolders: []string{"subdir1", "subdir2"},
		},
		{
			name:            "NestedDirectoryPattern",
			pattern:         "*/nested",
			expectedFolders: []string{"subdir1/nested", "subdir2/nested"},
		},
		{
			name:    "AllPattern",
			pattern: "*",
			expectedFiles: []string{
				"main.go", "helper.go", "util.go", "file1.txt", "file2.txt", "file3.txt",
				"data1.csv", "data2.csv", "file_data.csv", "README.md", "subdir.md",
			},
			expectedFolders: []string{"subdir1", "subdir2"},
		},
		{
			name:            "AllDirectoryPattern",
			pattern:         "**/",
			expectedFolders: []string{"subdir1", "subdir2", "subdir1/nested", "subdir2/nested"},
		},
		{
			name:    "AllPattern",
			pattern: "**",
			expectedFiles: []string{
				"main.go", "helper.go", "util.go", "file1.txt", "file2.txt", "file3.txt",
				"data1.csv", "data2.csv", "file_data.csv", "README.md", "subdir.md",
				"subdir1/helper.go", "subdir1/data.txt", "subdir1/test.csv",
				"subdir2/util.go", "subdir2/config.go", "subdir2/backup.txt",
				"subdir1/nested/app.go", "subdir1/nested/utils.go", "subdir1/nested/data.txt",
				"subdir2/nested/core.go", "subdir2/nested/helper.go", "subdir2/nested/config.json",
			},
			expectedFolders: []string{"subdir1", "subdir2", "subdir1/nested", "subdir2/nested"},
		},
		{
			name:            "NestedDirectoryPattern",
			pattern:         "**/",
			expectedFolders: []string{"subdir1", "subdir2", "subdir1/nested", "subdir2/nested"},
		},
		{
			name:    "WithPath",
			path:    "subdir1/nested",
			pattern: "*",
			expectedFiles: []string{
				"subdir1/nested/app.go", "subdir1/nested/utils.go", "subdir1/nested/data.txt",
			},
		},
		{
			name:    "AllPatternWithPath",
			path:    "subdir1",
			pattern: "**",
			expectedFiles: []string{
				"subdir1/helper.go", "subdir1/data.txt", "subdir1/test.csv",
				"subdir1/nested/app.go", "subdir1/nested/utils.go", "subdir1/nested/data.txt",
			},
			expectedFolders: []string{"subdir1/nested"},
		},
		{
			name:          "SingleFilePatternWithPath",
			path:          "subdir1/data.txt",
			pattern:       "*",
			expectedFiles: []string{"subdir1/data.txt"},
		},
		{
			name:            "SingleFilePatternWithPath_NotExists",
			path:            "subdir1/data.txt",
			pattern:         "not_exist",
			expectedFiles:   []string{},
			expectedFolders: []string{},
		},
		{
			name:            "file_not_exists",
			path:            "subdir1/nested/not_exist",
			pattern:         "*",
			expectedFiles:   []string{},
			expectedFolders: []string{},
			expectError:     true,
		},
		{
			name:        "EmptyPattern",
			pattern:     "",
			expectError: true,
		},
	}
	// Run test cases.
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := searchFileRequest{Path: tt.path, Pattern: tt.pattern}
			rsp, err := fileToolSet.searchFile(context.Background(), req)
			if tt.expectError {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.pattern, rsp.Pattern)
			assert.Equal(t, tempDir, rsp.BaseDirectory)
			assert.ElementsMatch(t, tt.expectedFiles, rsp.Files)
			assert.ElementsMatch(t, tt.expectedFolders, rsp.Folders)
		})
	}
}
