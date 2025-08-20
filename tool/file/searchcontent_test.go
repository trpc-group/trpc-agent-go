//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

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
)

func TestSearchContent(t *testing.T) {
	// Create a temporary directory for testing.
	tempDir := t.TempDir()
	// Create test directory structure with files.
	testFiles := map[string]string{
		"a.txt":     "hello foo\nnope\nfoo bar foo\n",
		"b.txt":     "bar\nFooBar\nbaz\n",
		"foo.txt":   "hit\n",
		"x.txt":     "ToDo\n",
		"big.log":   "this-is-a-big-file-with-foo",
		"small.log": "foo\n",
		"sub/c.txt": "foo\n",
	}
	// Create directories and files.
	for filePath, content := range testFiles {
		fullPath := filepath.Join(tempDir, filePath)
		// Ensure parent directory exists.
		parentDir := filepath.Dir(fullPath)
		if parentDir != tempDir {
			err := os.MkdirAll(parentDir, 0755)
			assert.NoError(t, err)
		}
		err := os.WriteFile(fullPath, []byte(content), 0644)
		assert.NoError(t, err)
	}
	tests := []struct {
		name      string
		opts      []Option
		req       searchContentRequest
		wantErr   bool
		wantFiles map[string][]int // relative path -> line numbers.
	}{
		{
			name:    "empty file pattern",
			req:     searchContentRequest{Path: "", FilePattern: "", ContentPattern: "foo"},
			wantErr: true,
		},
		{
			name:    "empty content pattern",
			req:     searchContentRequest{Path: "", FilePattern: "**/*.txt", ContentPattern: ""},
			wantErr: true,
		},
		{
			name: "basic multi-file and one-match-per-line",
			req: searchContentRequest{
				Path:           "",
				FilePattern:    "**/*.txt",
				ContentPattern: "foo",
			},
			wantFiles: map[string][]int{
				"a.txt":     {1, 3},
				"b.txt":     {2},
				"sub/c.txt": {1},
			},
		},
		{
			name: "file case sensitive not match",
			req: searchContentRequest{
				Path:              "",
				FilePattern:       "*.TXT",
				FileCaseSensitive: true,
				ContentPattern:    "hit",
			},
			wantFiles: map[string][]int{},
		},
		{
			name: "file case sensitive match",
			req: searchContentRequest{
				Path:              "",
				FilePattern:       "*.txt",
				FileCaseSensitive: true,
				ContentPattern:    "hit",
			},
			wantFiles: map[string][]int{
				"foo.txt": {1},
			},
		},
		{
			name: "content case sensitive not match",
			req: searchContentRequest{
				Path:                 "",
				FilePattern:          "*.txt",
				ContentPattern:       "todo",
				ContentCaseSensitive: true,
			},
			wantFiles: map[string][]int{},
		},
		{
			name: "content case sensitive match",
			req: searchContentRequest{
				Path:                 "",
				FilePattern:          "*.txt",
				ContentPattern:       "ToDo",
				ContentCaseSensitive: true,
			},
			wantFiles: map[string][]int{
				"x.txt": {1},
			},
		},
		{
			name: "skip large files by maxFileSize",
			opts: []Option{WithMaxFileSize(5)},
			req: searchContentRequest{
				Path:           "",
				FilePattern:    "*.log",
				ContentPattern: "foo",
			},
			wantFiles: map[string][]int{
				"small.log": {1},
			},
		},
		{
			name: "not found",
			req: searchContentRequest{
				Path:           "not-found",
				FilePattern:    "*.txt",
				ContentPattern: "foo",
			},
			wantErr: true,
		},
		{
			name: "not a directory",
			req: searchContentRequest{
				Path:           "a.txt",
				FilePattern:    "*.txt",
				ContentPattern: "foo",
			},
			wantErr: true,
		},
		{
			name: "directory traversal attack",
			req: searchContentRequest{
				Path:           "../",
				FilePattern:    "**/*.txt",
				ContentPattern: "foo",
			},
			wantErr: true,
		},
		{
			name: "invalid content pattern",
			req: searchContentRequest{
				Path:           "",
				FilePattern:    "a.txt",
				ContentPattern: "?",
			},
			wantErr: true,
		},
		{
			name: "invalid file pattern",
			req: searchContentRequest{
				Path:           "",
				FilePattern:    "[",
				ContentPattern: "foo",
			},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Build toolset.
			opts := append([]Option{WithBaseDir(tempDir)}, tc.opts...)
			set, err := NewToolSet(opts...)
			assert.NoError(t, err)
			fts := set.(*fileToolSet)
			// Call search.
			rsp, err := fts.searchContent(context.Background(), &tc.req)
			if tc.wantErr {
				assert.Error(t, err)
				assert.NotNil(t, rsp)
				return
			}
			assert.NoError(t, err)
			assert.NotNil(t, rsp)
			actual := map[string][]int{}
			for _, fm := range rsp.FileMatches {
				for _, lm := range fm.Matches {
					actual[fm.FilePath] = append(actual[fm.FilePath], lm.LineNumber)
				}
			}
			assert.Equal(t, tc.wantFiles, actual)
		})
	}
}
