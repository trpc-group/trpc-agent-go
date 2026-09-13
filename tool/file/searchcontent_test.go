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
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
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
		"a.txt":       "hello foo\nnope\nfoo bar foo\n",
		"b.txt":       "bar\nFooBar\nbaz\n",
		"foo.txt":     "hit\n",
		"x.txt":       "ToDo\n",
		"big.log":     "this-is-a-big-file-with-foo",
		"small.log":   "foo\n",
		"beyond.huge": strings.Repeat("padding padding foo\n", 32),
		"sub/c.txt":   "foo\n",
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
		name        string
		opts        []Option
		req         searchContentRequest
		wantErr     bool
		wantFiles   map[string][]int // relative path -> line numbers.
		wantSkipped []string
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
			name: "files above maxFileSize are still searched",
			opts: []Option{WithMaxFileSize(5)},
			req: searchContentRequest{
				Path:           "",
				FilePattern:    "*.log",
				ContentPattern: "foo",
			},
			wantFiles: map[string][]int{
				"small.log": {1},
				"big.log":   {1},
			},
		},
		{
			name: "files above the search cap are reported, not silently skipped",
			opts: []Option{WithMaxFileSize(5)},
			req: searchContentRequest{
				Path:           "",
				FilePattern:    "*.huge",
				ContentPattern: "foo",
			},
			wantFiles:   map[string][]int{},
			wantSkipped: []string{"beyond.huge"},
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
			assert.Equal(t, tc.wantSkipped, rsp.SkippedFiles)
			if len(tc.wantSkipped) > 0 {
				assert.Contains(t, rsp.Message, "were NOT searched")
				for _, name := range tc.wantSkipped {
					assert.Contains(t, rsp.Message, name)
				}
			}
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
	assert.EqualError(t, err, "request cannot be nil")
	assert.NotNil(t, rsp)
	assert.Equal(t, "Error: request cannot be nil", rsp.Message)
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

func TestSearchContent_FilePatternRef_AboveReadLimitStillSearched(t *testing.T) {
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
	assert.NoError(t, err)
	assert.NotNil(t, rsp)
	assert.Len(t, rsp.FileMatches, 1)
	assert.Equal(t, 1, rsp.FileMatches[0].Matches[0].LineNumber)
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
			Content:  strings.Repeat("1", 200),
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
	assert.Contains(t, rsp.Message, "max search size")
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

	matches, tooLarge, ok := fts.searchSingleLocalFile(context.Background(), "", "", re)
	assert.False(t, ok)
	assert.False(t, tooLarge)
	assert.Nil(t, matches)

	matches, tooLarge, ok = fts.searchSingleLocalFile(context.Background(), base, "", re)
	assert.False(t, ok)
	assert.False(t, tooLarge)
	assert.Nil(t, matches)

	withinCap := filepath.Join(base, "big.txt")
	assert.NoError(t, os.WriteFile(withinCap, []byte("123\nfoo"), 0o644))
	matches, tooLarge, ok = fts.searchSingleLocalFile(context.Background(), withinCap, "big.txt", re)
	assert.True(t, ok)
	assert.False(t, tooLarge)
	assert.Len(t, matches, 1)

	tooBig := filepath.Join(base, "huge.txt")
	assert.NoError(t, os.WriteFile(tooBig, []byte(strings.Repeat("foo\n", 100)), 0o644))
	matches, tooLarge, ok = fts.searchSingleLocalFile(context.Background(), tooBig, "huge.txt", re)
	assert.True(t, ok)
	assert.True(t, tooLarge)
	assert.Empty(t, matches)

	noMatch := filepath.Join(base, "nomatch.txt")
	assert.NoError(t, os.WriteFile(noMatch, []byte("bar"), 0o644))
	matches, tooLarge, ok = fts.searchSingleLocalFile(context.Background(), noMatch, "nomatch.txt", re)
	assert.True(t, ok)
	assert.False(t, tooLarge)
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

func TestSearchContent_CancelledContext(t *testing.T) {
	tempDir := t.TempDir()
	assert.NoError(t, os.WriteFile(
		filepath.Join(tempDir, "a.txt"),
		[]byte("foo\nbar\n"),
		0o644,
	))
	set, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := searchContentRequest{
		Path:           "",
		FilePattern:    "*.txt",
		ContentPattern: "foo",
	}
	rsp, err := fts.searchContent(ctx, &req)
	assert.ErrorIs(t, err, context.Canceled)
	assert.NotNil(t, rsp)
	assert.Contains(t, rsp.Message, "context canceled")
}

func TestSearchContentByPath_Guards(t *testing.T) {
	set, err := NewToolSet(WithBaseDir(t.TempDir()))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)
	re := regexp.MustCompile("foo")

	_, _, _, err = fts.searchContentByPath(context.Background(), nil, nil)
	assert.EqualError(t, err, "request cannot be nil")

	_, _, _, err = fts.searchContentByPath(context.Background(),
		&searchContentRequest{Path: "ftp://host/dir"}, re)
	assert.ErrorContains(t, err, "unsupported file ref scheme")
}

// A cancelled context stops a workspace search before it reads any file, and
// is reported rather than returned as an empty result.
func TestSearchContent_WorkspaceRef_CancelledContext(t *testing.T) {
	set, err := NewToolSet(WithBaseDir(t.TempDir()))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)

	inv := agent.NewInvocation()
	ctx, cancel := context.WithCancel(agent.NewInvocationContext(context.Background(), inv))
	cancel()
	toolcache.StoreSkillRunOutputFiles(inv, []codeexecutor.File{{
		Name:     "out/transcript.txt",
		Content:  "foo\n",
		MIMEType: "text/plain",
	}})

	rsp, err := fts.searchContent(ctx, &searchContentRequest{
		Path:           "workspace://",
		FilePattern:    "**/*.txt",
		ContentPattern: "foo",
	})
	assert.ErrorIs(t, err, context.Canceled)
	assert.NotNil(t, rsp)
	assert.Empty(t, rsp.FileMatches)
}

// Searching a single file by path, rather than a directory with a pattern,
// goes through the same size and cancellation rules.
func TestSearchContent_PathIsFile(t *testing.T) {
	tempDir := t.TempDir()
	set, err := NewToolSet(WithBaseDir(tempDir), WithMaxFileSize(8))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)

	assert.NoError(t, os.WriteFile(filepath.Join(tempDir, "small.txt"), []byte("foo\nbar\n"), 0o644))
	assert.NoError(t, os.WriteFile(filepath.Join(tempDir, "huge.txt"),
		[]byte(strings.Repeat("foo\n", 1000)), 0o644))

	t.Run("a file beyond the search cap is reported, not searched", func(t *testing.T) {
		rsp, err := fts.searchContent(context.Background(), &searchContentRequest{
			Path:           "huge.txt",
			FilePattern:    "*",
			ContentPattern: "foo",
		})
		assert.NoError(t, err)
		assert.Empty(t, rsp.FileMatches)
		assert.Equal(t, []string{"huge.txt"}, rsp.SkippedFiles)
		assert.Contains(t, rsp.Message, "huge.txt")
	})

	t.Run("a cancelled context is reported", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := fts.searchContent(ctx, &searchContentRequest{
			Path:           "small.txt",
			FilePattern:    "*",
			ContentPattern: "foo",
		})
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("a file the scanner cannot finish is reported, not a miss", func(t *testing.T) {
		// A line longer than the scanner tolerates makes the scan fail, which
		// is the one way a readable file is not searchable. It is named
		// beside the oversized files rather than reported as zero matches.
		long := filepath.Join(tempDir, "long.txt")
		assert.NoError(t, os.WriteFile(long, []byte("foo "+strings.Repeat("x", maxSearchLineSize+1)+"\n"), 0o644))
		t.Cleanup(func() { _ = os.Remove(long) })
		fts.maxFileSize = int64(maxSearchLineSize * 2)
		t.Cleanup(func() { fts.maxFileSize = 8 })

		rsp, err := fts.searchContent(context.Background(), &searchContentRequest{
			Path:           "long.txt",
			FilePattern:    "*",
			ContentPattern: "foo",
		})
		assert.NoError(t, err)
		assert.Empty(t, rsp.FileMatches)
		assert.Equal(t, []string{"long.txt"}, rsp.SkippedFiles)
		assert.Contains(t, rsp.Message, "NOT searched")

		// The same file reached through a pattern is reported the same way.
		rsp, err = fts.searchContent(context.Background(), &searchContentRequest{
			FilePattern:    "long.txt",
			ContentPattern: "foo",
		})
		assert.NoError(t, err)
		assert.Empty(t, rsp.FileMatches)
		assert.Equal(t, []string{"long.txt"}, rsp.SkippedFiles)
	})
}

// A CRLF line keeps its "\r" in line_content, as it does when the same file is
// searched from the workspace cache, so both backends match the same patterns.
func TestSearchContent_KeepsCarriageReturns(t *testing.T) {
	tempDir := t.TempDir()
	set, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)
	assert.NoError(t, os.WriteFile(filepath.Join(tempDir, "dos.txt"), []byte("foo\r\nbar\r\nfoo"), 0o644))

	rsp, err := fts.searchContent(context.Background(), &searchContentRequest{
		FilePattern:    "dos.txt",
		ContentPattern: "foo\r$",
	})
	assert.NoError(t, err)
	assert.Len(t, rsp.FileMatches, 1)
	assert.Len(t, rsp.FileMatches[0].Matches, 1)
	assert.Equal(t, "foo\r", rsp.FileMatches[0].Matches[0].LineContent)
	assert.Equal(t, 1, rsp.FileMatches[0].Matches[0].LineNumber)

	cached := searchTextContent("dos.txt", "foo\r\nbar\r\nfoo", regexp.MustCompile("foo\r$"))
	assert.Equal(t, rsp.FileMatches[0].Matches, cached.Matches,
		"the local and cached backends must agree on line content")
}

// A read limit large enough that scaling it would overflow saturates, so no
// file is turned away as too large.
func TestSearchSizeCap_Saturates(t *testing.T) {
	set, err := NewToolSet(WithBaseDir(t.TempDir()), WithMaxFileSize(math.MaxInt64/searchSizeCapMultiple+1))
	assert.NoError(t, err)
	assert.Equal(t, int64(math.MaxInt64), set.(*fileToolSet).searchSizeCap())

	set, err = NewToolSet(WithBaseDir(t.TempDir()), WithMaxFileSize(10))
	assert.NoError(t, err)
	assert.Equal(t, int64(10*searchSizeCapMultiple), set.(*fileToolSet).searchSizeCap())
}

// Streaming search stops listing a file's matches at maxMatchesPerFile, says so
// in the message, and checks for cancellation as it scans.
func TestSearchContent_StreamingLimits(t *testing.T) {
	tempDir := t.TempDir()
	set, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)

	assert.NoError(t, os.WriteFile(filepath.Join(tempDir, "many.txt"),
		[]byte(strings.Repeat("foo\n", maxMatchesPerFile+50)), 0o644))
	// sparse.txt is long enough to reach the periodic cancellation check but
	// matches too rarely to hit the per-file cap first.
	assert.NoError(t, os.WriteFile(filepath.Join(tempDir, "sparse.txt"),
		[]byte(strings.Repeat("bar\n", 2000)+"foo\n"), 0o644))
	assert.NoError(t, os.Mkdir(filepath.Join(tempDir, "dir.txt"), 0o755))

	t.Run("matches per file are capped", func(t *testing.T) {
		rsp, err := fts.searchContent(context.Background(), &searchContentRequest{
			FilePattern:    "*.txt",
			ContentPattern: "foo",
		})
		assert.NoError(t, err)
		assert.Len(t, rsp.FileMatches, 2, "the directory matching the pattern is skipped")
		for _, m := range rsp.FileMatches {
			if m.FilePath == "many.txt" {
				assert.Len(t, m.Matches, maxMatchesPerFile)
				assert.True(t, m.Truncated)
			} else {
				assert.False(t, m.Truncated)
			}
		}
	})

	t.Run("a single file searched by path is capped the same way", func(t *testing.T) {
		rsp, err := fts.searchContent(context.Background(), &searchContentRequest{
			Path:           "many.txt",
			FilePattern:    "*",
			ContentPattern: "foo",
		})
		assert.NoError(t, err)
		assert.Len(t, rsp.FileMatches, 1)
		assert.Len(t, rsp.FileMatches[0].Matches, maxMatchesPerFile)
		assert.True(t, rsp.FileMatches[0].Truncated)
	})

	t.Run("the per-file message says where it stopped", func(t *testing.T) {
		assert.Contains(t,
			fileMatchMessage(&fileMatch{Truncated: true, Matches: make([]*lineMatch, maxMatchesPerFile)}, "many.txt"),
			fmt.Sprintf("stopped at the first %d", maxMatchesPerFile))
		assert.Equal(t, "Found 1 matches in file 'a.txt'",
			fileMatchMessage(&fileMatch{Matches: make([]*lineMatch, 1)}, "a.txt"))
	})

	t.Run("cancellation is noticed mid-scan", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := searchFileContent(ctx, filepath.Join(tempDir, "sparse.txt"), regexp.MustCompile("foo"))
		assert.ErrorIs(t, err, context.Canceled)
	})
}

// Oversized files are recorded by the walking loop while goroutines for
// earlier files may be recording scan failures; both must reach skipped_files.
// Run with -race, this is the regression for the unguarded append.
func TestSearchContent_SkippedFilesFromBothPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod 0 does not deny reads on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root can read a mode-0 file")
	}
	tempDir := t.TempDir()
	set, err := NewToolSet(WithBaseDir(tempDir), WithMaxFileSize(8))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)

	var want []string
	for i := 0; i < 8; i++ {
		// Under the cap but unreadable: its goroutine records a scan failure.
		locked := fmt.Sprintf("a-locked-%d.txt", i)
		path := filepath.Join(tempDir, locked)
		assert.NoError(t, os.WriteFile(path, []byte("foo\n"), 0o644))
		assert.NoError(t, os.Chmod(path, 0o000))
		t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
		// Over the cap: the walking loop records it.
		huge := fmt.Sprintf("b-huge-%d.txt", i)
		assert.NoError(t, os.WriteFile(filepath.Join(tempDir, huge),
			[]byte(strings.Repeat("foo\n", 1000)), 0o644))
		want = append(want, locked, huge)
	}
	rsp, err := fts.searchContent(context.Background(), &searchContentRequest{
		FilePattern:    "*.txt",
		ContentPattern: "foo",
	})
	assert.NoError(t, err)
	assert.Empty(t, rsp.FileMatches)
	slices.Sort(want)
	assert.Equal(t, want, rsp.SkippedFiles)
}
