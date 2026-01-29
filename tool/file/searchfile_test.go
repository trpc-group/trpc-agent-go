//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package file

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolcache"
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
		caseSensitive   bool
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
			name:    "WildcardBothSides",
			pattern: "*file*",
			expectedFiles: []string{
				"file1.txt",
				"file2.txt",
				"file3.txt",
				"file_data.csv",
			},
		},
		{
			name:    "ThirdLevelPattern",
			pattern: "subdir1/nested/*.go",
			expectedFiles: []string{
				"subdir1/nested/app.go",
				"subdir1/nested/utils.go",
			},
		},
		{
			name:    "AllGoPattern",
			pattern: "**/*.go",
			expectedFiles: []string{
				"main.go", "helper.go", "util.go",
				"subdir1/helper.go", "subdir1/nested/app.go", "subdir1/nested/utils.go",
				"subdir2/util.go",
				"subdir2/config.go",
				"subdir2/nested/core.go",
				"subdir2/nested/helper.go",
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
			name:    "AllDirectoryPattern",
			pattern: "**/",
			expectedFolders: []string{
				"subdir1",
				"subdir2",
				"subdir1/nested",
				"subdir2/nested",
			},
		},
		{
			name:    "AllPattern",
			pattern: "**",
			expectedFiles: []string{
				"main.go",
				"helper.go",
				"util.go",
				"file1.txt",
				"file2.txt",
				"file3.txt",
				"data1.csv",
				"data2.csv",
				"file_data.csv",
				"README.md",
				"subdir.md",
				"subdir1/helper.go",
				"subdir1/data.txt",
				"subdir1/test.csv",
				"subdir2/util.go",
				"subdir2/config.go",
				"subdir2/backup.txt",
				"subdir1/nested/app.go",
				"subdir1/nested/utils.go",
				"subdir1/nested/data.txt",
				"subdir2/nested/core.go",
				"subdir2/nested/helper.go",
				"subdir2/nested/config.json",
			},
			expectedFolders: []string{
				"subdir1",
				"subdir2",
				"subdir1/nested",
				"subdir2/nested",
			},
		},
		{
			name:    "NestedDirectoryPattern",
			pattern: "**/",
			expectedFolders: []string{
				"subdir1",
				"subdir2",
				"subdir1/nested",
				"subdir2/nested",
			},
		},
		{
			name:    "WithPath",
			path:    "subdir1/nested",
			pattern: "*",
			expectedFiles: []string{
				"subdir1/nested/app.go",
				"subdir1/nested/utils.go",
				"subdir1/nested/data.txt",
			},
		},
		{
			name:    "AllPatternWithPath",
			path:    "subdir1",
			pattern: "**",
			expectedFiles: []string{
				"subdir1/helper.go", "subdir1/data.txt", "subdir1/test.csv",
				"subdir1/nested/app.go",
				"subdir1/nested/utils.go",
				"subdir1/nested/data.txt",
			},
			expectedFolders: []string{"subdir1/nested"},
		},
		{
			name:        "SingleFilePatternWithPath",
			path:        "subdir1/data.txt",
			pattern:     "*",
			expectError: true,
		},
		{
			name:        "path_not_exists",
			path:        "subdir1/nested/not_exist",
			pattern:     "*",
			expectError: true,
		},
		{
			name:          "without_case_sensitive",
			pattern:       "readme*",
			expectedFiles: []string{"README.md"},
			caseSensitive: false,
		},
		{
			name:          "with_case_sensitive",
			pattern:       "readme*",
			caseSensitive: true,
		},
		{
			name:        "EmptyPattern",
			pattern:     "",
			expectError: true,
		},
		{
			name:        "DirTraversal",
			path:        "../",
			pattern:     "*",
			expectError: true,
		},
		{
			name:        "InvalidPattern",
			pattern:     "[",
			expectError: true,
		},
	}
	// Run test cases.
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &searchFileRequest{
				Path:          tt.path,
				Pattern:       tt.pattern,
				CaseSensitive: tt.caseSensitive,
			}
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

func TestFileTool_SearchFile_WorkspaceRef(t *testing.T) {
	fileToolSet := &fileToolSet{baseDir: t.TempDir()}

	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)
	toolcache.StoreSkillRunOutputFiles(inv, []codeexecutor.File{
		{
			Name:     "out/a.txt",
			Content:  "a",
			MIMEType: "text/plain",
		},
		{
			Name:     "out/b.md",
			Content:  "b",
			MIMEType: "text/markdown",
		},
	})

	rsp, err := fileToolSet.searchFile(ctx, &searchFileRequest{
		Path:          "workspace://",
		Pattern:       "**/*.txt",
		CaseSensitive: true,
	})
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"workspace://out/a.txt"}, rsp.Files)
	assert.Empty(t, rsp.Folders)

	rsp, err = fileToolSet.searchFile(ctx, &searchFileRequest{
		Path:          "workspace://",
		Pattern:       "out",
		CaseSensitive: true,
	})
	assert.NoError(t, err)
	assert.Empty(t, rsp.Files)
	assert.ElementsMatch(t, []string{"workspace://out"}, rsp.Folders)
}

func TestFileTool_SearchFile_FallbackToWorkspaceCache(t *testing.T) {
	fileToolSet := &fileToolSet{baseDir: t.TempDir()}

	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)
	toolcache.StoreSkillRunOutputFiles(inv, []codeexecutor.File{
		{
			Name:     "out/a.txt",
			Content:  "a",
			MIMEType: "text/plain",
		},
		{
			Name:     "out/sub/c.txt",
			Content:  "c",
			MIMEType: "text/plain",
		},
	})

	rsp, err := fileToolSet.searchFile(ctx, &searchFileRequest{
		Path:          "out",
		Pattern:       "**/*.txt",
		CaseSensitive: true,
	})
	assert.NoError(t, err)
	assert.Equal(t, "workspace://out", rsp.Path)
	assert.ElementsMatch(t, []string{
		"workspace://out/a.txt",
		"workspace://out/sub/c.txt",
	}, rsp.Files)
	assert.Empty(t, rsp.Folders)
}

func TestFileTool_SearchFile_ArtifactUnsupported(t *testing.T) {
	fileToolSet := &fileToolSet{baseDir: t.TempDir()}
	_, err := fileToolSet.searchFile(
		context.Background(),
		&searchFileRequest{
			Path:    "artifact://x.txt",
			Pattern: "*",
		},
	)
	assert.Error(t, err)
}

func TestFileTool_SearchFile_ParseError(t *testing.T) {
	fileToolSet := &fileToolSet{baseDir: t.TempDir()}
	_, err := fileToolSet.searchFile(
		context.Background(),
		&searchFileRequest{
			Path:    "unknown://x",
			Pattern: "*",
		},
	)
	assert.Error(t, err)
}

func TestFileTool_SearchFile_WorkspaceInvalidPattern(t *testing.T) {
	fileToolSet := &fileToolSet{baseDir: t.TempDir()}

	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)
	toolcache.StoreSkillRunOutputFiles(inv, []codeexecutor.File{
		{
			Name:     "out/a.txt",
			Content:  "a",
			MIMEType: "text/plain",
		},
	})

	_, err := fileToolSet.searchFile(ctx, &searchFileRequest{
		Path:    "workspace://",
		Pattern: "[",
	})
	assert.Error(t, err)
}

func TestBuildWorkspaceIndex_SkipsDotAndStopsAtRoot(t *testing.T) {
	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)
	toolcache.StoreSkillRunOutputFiles(inv, []codeexecutor.File{
		{Name: ".", Content: "x", MIMEType: "text/plain"},
		{Name: "/abs/x.txt", Content: "x", MIMEType: "text/plain"},
		{Name: "out/a.txt", Content: "a", MIMEType: "text/plain"},
	})

	idx := buildWorkspaceIndex(ctx)
	assert.NotEmpty(t, idx.files)
	assert.NotEmpty(t, idx.dirs)
}

func TestMatchWorkspacePaths_EmptyPattern(t *testing.T) {
	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)

	files, dirs, err := matchWorkspacePaths(ctx, "", " ", true)
	assert.NoError(t, err)
	assert.Nil(t, files)
	assert.Nil(t, dirs)
}

func TestMatchWorkspacePaths_DirSlashPattern(t *testing.T) {
	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)
	toolcache.StoreSkillRunOutputFiles(inv, []codeexecutor.File{
		{Name: "a/b.txt", Content: "b", MIMEType: "text/plain"},
	})

	files, dirs, err := matchWorkspacePaths(ctx, "", "a/", true)
	assert.NoError(t, err)
	assert.Empty(t, files)
	assert.Equal(t, []string{"workspace://a"}, dirs)
}
