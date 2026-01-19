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
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/internal/fileref"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolcache"
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
			name: "empty file pattern",
			req: searchContentRequest{
				Path:           "",
				FilePattern:    "",
				ContentPattern: "foo",
			},
			wantErr: true,
		},
		{
			name: "empty content pattern",
			req: searchContentRequest{
				Path:           "",
				FilePattern:    "**/*.txt",
				ContentPattern: "",
			},
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
			wantFiles: map[string][]int{
				"a.txt": {1, 3},
			},
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

func TestSearchContent_FromSkillRunCache(t *testing.T) {
	tempDir := t.TempDir()

	set, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)

	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)
	toolcache.StoreSkillRunOutputFiles(inv, []codeexecutor.File{
		{
			Name:     "out/transcript.txt",
			Content:  "freshly squeezed lemon juice\n",
			MIMEType: "text/plain",
		},
	})

	req := searchContentRequest{
		Path:           filepath.Base(tempDir),
		FilePattern:    "out/transcript.txt",
		ContentPattern: "freshly squeezed lemon juice",
	}
	rsp, err := fts.searchContent(ctx, &req)
	assert.NoError(t, err)
	assert.Equal(t, "", rsp.Path)
	assert.Len(t, rsp.FileMatches, 1)
	assert.Equal(t, "out/transcript.txt", rsp.FileMatches[0].FilePath)
	assert.Len(t, rsp.FileMatches[0].Matches, 1)
	assert.Contains(t, rsp.FileMatches[0].Matches[0].LineContent,
		"freshly squeezed lemon juice")
}

func TestSearchContent_WorkspaceRef(t *testing.T) {
	tempDir := t.TempDir()

	set, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)

	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)
	toolcache.StoreSkillRunOutputFiles(inv, []codeexecutor.File{
		{
			Name:     "out/transcript.txt",
			Content:  "freshly squeezed lemon juice\n",
			MIMEType: "text/plain",
		},
	})

	req := searchContentRequest{
		Path:           "workspace://",
		FilePattern:    "**/*.txt",
		ContentPattern: "freshly squeezed lemon juice",
	}
	rsp, err := fts.searchContent(ctx, &req)
	assert.NoError(t, err)
	assert.Equal(t, "workspace://", rsp.Path)
	assert.Len(t, rsp.FileMatches, 1)
	assert.Equal(
		t,
		fileref.WorkspaceRef("out/transcript.txt"),
		rsp.FileMatches[0].FilePath,
	)
	assert.Len(t, rsp.FileMatches[0].Matches, 1)
}

func TestSearchContent_PathFile_FromSkillRunCache(t *testing.T) {
	tempDir := t.TempDir()

	set, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)

	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)
	toolcache.StoreSkillRunOutputFiles(inv, []codeexecutor.File{
		{
			Name:     "out/transcript.txt",
			Content:  "freshly squeezed lemon juice\n",
			MIMEType: "text/plain",
		},
	})

	req := searchContentRequest{
		Path:           "out/transcript.txt",
		FilePattern:    "*",
		ContentPattern: "freshly squeezed lemon juice",
	}
	rsp, err := fts.searchContent(ctx, &req)
	assert.NoError(t, err)
	assert.Equal(t, "out/transcript.txt", rsp.Path)
	assert.Len(t, rsp.FileMatches, 1)
	assert.Equal(
		t,
		fileref.WorkspaceRef("out/transcript.txt"),
		rsp.FileMatches[0].FilePath,
	)
	assert.Len(t, rsp.FileMatches[0].Matches, 1)
}

func TestSearchContent_FilePattern_WorkspaceRef(t *testing.T) {
	tempDir := t.TempDir()

	set, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)

	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)
	toolcache.StoreSkillRunOutputFiles(inv, []codeexecutor.File{
		{
			Name:     "out/transcript.txt",
			Content:  "freshly squeezed lemon juice\n",
			MIMEType: "text/plain",
		},
	})

	req := searchContentRequest{
		Path:           "",
		FilePattern:    fileref.WorkspaceRef("out/transcript.txt"),
		ContentPattern: "freshly squeezed lemon juice",
	}
	rsp, err := fts.searchContent(ctx, &req)
	assert.NoError(t, err)
	assert.Len(t, rsp.FileMatches, 1)
	assert.Equal(t, fileref.WorkspaceRef("out/transcript.txt"),
		rsp.FileMatches[0].FilePath)
	assert.Len(t, rsp.FileMatches[0].Matches, 1)
}

func TestSearchContent_FromSkillRunCache_JoinsBasename(t *testing.T) {
	tempDir := t.TempDir()

	set, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)

	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)
	toolcache.StoreSkillRunOutputFiles(inv, []codeexecutor.File{
		{
			Name:     "out/transcript.txt",
			Content:  "freshly squeezed lemon juice\n",
			MIMEType: "text/plain",
		},
	})

	req := searchContentRequest{
		Path:           "out",
		FilePattern:    "transcript.txt",
		ContentPattern: "freshly squeezed lemon juice",
	}
	rsp, err := fts.searchContent(ctx, &req)
	assert.NoError(t, err)
	assert.Equal(t, "out", rsp.Path)
	assert.Len(t, rsp.FileMatches, 1)
	assert.Equal(t, "out/transcript.txt", rsp.FileMatches[0].FilePath)
	assert.Len(t, rsp.FileMatches[0].Matches, 1)
}

func TestSearchContent_NilRequest(t *testing.T) {
	set, err := NewToolSet(WithBaseDir(t.TempDir()))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)

	rsp, err := fts.searchContent(context.Background(), nil)
	assert.Error(t, err)
	assert.NotNil(t, rsp)
	assert.Contains(t, rsp.Message, "request cannot be nil")
}

func TestSearchContent_FilePatternRef_NotExported(t *testing.T) {
	set, err := NewToolSet(WithBaseDir(t.TempDir()))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)

	req := searchContentRequest{
		Path:           "",
		FilePattern:    "workspace://out/missing.txt",
		ContentPattern: "foo",
	}
	rsp, err := fts.searchContent(context.Background(), &req)
	assert.Error(t, err)
	assert.NotNil(t, rsp)
	assert.Contains(t, rsp.Message, "workspace file is not exported")
}

func TestSearchContent_FilePatternRef_TooLarge(t *testing.T) {
	set, err := NewToolSet(
		WithBaseDir(t.TempDir()),
		WithMaxFileSize(3),
	)
	assert.NoError(t, err)
	fts := set.(*fileToolSet)

	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)
	toolcache.StoreSkillRunOutputFiles(inv, []codeexecutor.File{
		{
			Name:     "out/a.txt",
			Content:  "1234",
			MIMEType: "text/plain",
		},
	})

	req := searchContentRequest{
		Path:           "",
		FilePattern:    "workspace://out/a.txt",
		ContentPattern: "1",
	}
	rsp, err := fts.searchContent(ctx, &req)
	assert.Error(t, err)
	assert.NotNil(t, rsp)
	assert.Contains(t, rsp.Message, "file size is beyond")
}

func TestSearchContent_FilePatternRef_NoMatches(t *testing.T) {
	set, err := NewToolSet(WithBaseDir(t.TempDir()))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)

	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)
	toolcache.StoreSkillRunOutputFiles(inv, []codeexecutor.File{
		{
			Name:     "out/a.txt",
			Content:  "hello",
			MIMEType: "text/plain",
		},
	})

	req := searchContentRequest{
		Path:           "",
		FilePattern:    "workspace://out/a.txt",
		ContentPattern: "missing",
	}
	rsp, err := fts.searchContent(ctx, &req)
	assert.NoError(t, err)
	assert.Empty(t, rsp.FileMatches)
}

func TestSearchContent_PathArtifactUnsupported(t *testing.T) {
	set, err := NewToolSet(WithBaseDir(t.TempDir()))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)

	req := searchContentRequest{
		Path:           "artifact://x.txt",
		FilePattern:    "*",
		ContentPattern: "foo",
	}
	rsp, err := fts.searchContent(context.Background(), &req)
	assert.Error(t, err)
	assert.NotNil(t, rsp)
	assert.Contains(t, rsp.Message, "artifact://")
}

func TestSearchSingleLocalFile_Branches(t *testing.T) {
	base := t.TempDir()
	set, err := NewToolSet(WithBaseDir(base), WithMaxFileSize(2))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)

	re := regexp.MustCompile("foo")

	matches, ok := fts.searchSingleLocalFile("", "", re)
	assert.False(t, ok)
	assert.Nil(t, matches)

	matches, ok = fts.searchSingleLocalFile(base, "", re)
	assert.False(t, ok)
	assert.Nil(t, matches)

	tooBig := filepath.Join(base, "big.txt")
	assert.NoError(t, os.WriteFile(tooBig, []byte("123"), 0o644))
	matches, ok = fts.searchSingleLocalFile(tooBig, "big.txt", re)
	assert.True(t, ok)
	assert.Empty(t, matches)

	noMatch := filepath.Join(base, "nomatch.txt")
	assert.NoError(t, os.WriteFile(noMatch, []byte("bar"), 0o644))
	matches, ok = fts.searchSingleLocalFile(noMatch, "nomatch.txt", re)
	assert.True(t, ok)
	assert.Empty(t, matches)
}

func TestSearchSkillCache_Branches(t *testing.T) {
	base := t.TempDir()
	set, err := NewToolSet(WithBaseDir(base))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)

	re := regexp.MustCompile("foo")
	matches, ok := fts.searchSkillCache(context.Background(), "", nil, re)
	assert.False(t, ok)
	assert.Nil(t, matches)

	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)
	toolcache.StoreSkillRunOutputFiles(inv, []codeexecutor.File{
		{
			Name:     "out/a.txt",
			Content:  "bar",
			MIMEType: "text/plain",
		},
	})

	req := &searchContentRequest{FilePattern: ""}
	matches, ok = fts.searchSkillCache(ctx, "out", req, re)
	assert.False(t, ok)
	assert.Nil(t, matches)

	req.FilePattern = "missing.txt"
	matches, ok = fts.searchSkillCache(ctx, "out", req, re)
	assert.False(t, ok)
	assert.Nil(t, matches)

	req.FilePattern = "a.txt"
	matches, ok = fts.searchSkillCache(ctx, "out", req, re)
	assert.True(t, ok)
	assert.Empty(t, matches)
}

func TestSearchContent_WorkspaceContent_SortsAndSkips(t *testing.T) {
	base := t.TempDir()
	set, err := NewToolSet(WithBaseDir(base), WithMaxFileSize(8))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)

	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)
	toolcache.StoreSkillRunOutputFiles(inv, []codeexecutor.File{
		{Name: ".", Content: "x", MIMEType: "text/plain"},
		{
			Name:     "dir1/a.txt",
			Content:  "foo\n",
			MIMEType: "text/plain",
		},
		{
			Name:     "dir1/b.txt",
			Content:  "foo\n",
			MIMEType: "text/plain",
		},
		{
			Name:     "dir1/skip.bin",
			Content:  "foo",
			MIMEType: "application/octet-stream",
		},
		{
			Name:     "dir1/nomatch.txt",
			Content:  "bar",
			MIMEType: "text/plain",
		},
		{
			Name:     "dir1/large.txt",
			Content:  strings.Repeat("a", 20),
			MIMEType: "text/plain",
		},
		{
			Name:     "dir2/c.txt",
			Content:  "foo",
			MIMEType: "text/plain",
		},
	})

	req := searchContentRequest{
		Path:           "workspace://dir1",
		FilePattern:    "*.txt",
		ContentPattern: "foo",
	}
	rsp, err := fts.searchContent(ctx, &req)
	assert.NoError(t, err)
	assert.Len(t, rsp.FileMatches, 2)
	assert.Equal(t, fileref.WorkspaceRef("dir1/a.txt"),
		rsp.FileMatches[0].FilePath)
	assert.Equal(t, fileref.WorkspaceRef("dir1/b.txt"),
		rsp.FileMatches[1].FilePath)
}
