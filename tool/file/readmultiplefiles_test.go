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

func TestReadMultipleFiles(t *testing.T) {
	// Build the test directory and files once, similar to TestSearchContent.
	base := t.TempDir()
	// Prepare files and directories.
	initial := map[string]string{
		"a.txt":         "hello",
		"foo.txt":       "x",
		"Foo.txt":       "y",
		"f1.go":         "a",
		"f2.go":         "b",
		"empty.txt":     "",
		"multiline.txt": "line1\nline2",
		"big.txt":       "0123456789",
		"dir/main.go":   "main",
		"noaccess.txt":  "data",
	}
	for p, content := range initial {
		fp := filepath.Join(base, p)
		if err := os.MkdirAll(filepath.Dir(fp), 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(fp, []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	assert.NoError(t, os.Chmod(filepath.Join(base, "noaccess.txt"), 0000))
	expectedNoAccess := ""
	if _, err := os.ReadFile(filepath.Join(base, "noaccess.txt")); err == nil {
		expectedNoAccess = "data"
	}
	// Detect whether the underlying filesystem is case-sensitive within
	// the temp base.
	fsCaseSensitive := func(dir string) bool {
		sub := filepath.Join(dir, "_casesens_check")
		_ = os.MkdirAll(sub, 0755)
		a := filepath.Join(sub, "foo.txt")
		b := filepath.Join(sub, "Foo.txt")
		_ = os.WriteFile(a, []byte("x"), 0644)
		_ = os.WriteFile(b, []byte("y"), 0644)
		sa, ea := os.Stat(a)
		sb, eb := os.Stat(b)
		if ea != nil || eb != nil || sa == nil || sb == nil {
			return false
		}
		// If both stats refer to the same file, the FS is case-insensitive.
		return !os.SameFile(sa, sb)
	}(base)

	tests := []struct {
		name             string
		opts             []Option
		req              readMultipleFilesRequest
		expectedContents map[string]string // file_name -> contents
		wantErr          bool
	}{
		{
			name:    "empty patterns",
			req:     readMultipleFilesRequest{Patterns: nil},
			wantErr: true,
		},
		{
			name: "invalid pattern aggregated with valid",
			// Use exact filenames to avoid unintended matches; plus
			// an invalid pattern.
			req: readMultipleFilesRequest{
				Patterns: []string{
					"[",
					"a.txt",
					"foo.txt",
					"Foo.txt",
				},
			},
			expectedContents: map[string]string{
				"a.txt":   "hello",
				"foo.txt": "x",
				"Foo.txt": "y",
			},
		},
		{
			name: "case insensitive",
			req: readMultipleFilesRequest{
				Patterns:      []string{"foo*.txt"},
				CaseSensitive: false,
			},
			expectedContents: map[string]string{
				"foo.txt": "x",
				"Foo.txt": "y",
			},
		},
		{
			name: "case sensitive",
			req: readMultipleFilesRequest{
				Patterns:      []string{"foo*.txt"},
				CaseSensitive: true,
			},
			expectedContents: map[string]string{
				"foo.txt": "x",
			},
		},
		{
			name: "deduplicate across patterns",
			req:  readMultipleFilesRequest{Patterns: []string{"*.go", "f1.go"}},
			expectedContents: map[string]string{
				"f1.go": "a",
				"f2.go": "b",
			},
		},
		{
			name: "exceed max file size",
			opts: []Option{WithMaxFileSize(5)},
			req:  readMultipleFilesRequest{Patterns: []string{"big.txt"}},
			expectedContents: map[string]string{
				"big.txt": "",
			},
		},
		{
			name: "directory recursion",
			req:  readMultipleFilesRequest{Patterns: []string{"**/*.go"}},
			expectedContents: map[string]string{
				"f1.go":       "a",
				"f2.go":       "b",
				"dir/main.go": "main",
			},
		},
		{
			name: "empty file",
			req:  readMultipleFilesRequest{Patterns: []string{"empty.txt"}},
			expectedContents: map[string]string{
				"empty.txt": "",
			},
		},
		{
			name: "read permission denied",
			req:  readMultipleFilesRequest{Patterns: []string{"noaccess.txt"}},
			expectedContents: map[string]string{
				"noaccess.txt": expectedNoAccess,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Skip tests that rely on distinct-case filenames when
			// the filesystem is case-insensitive.
			needsCaseSensitiveFS := tc.name ==
				"invalid pattern aggregated with valid" ||
				tc.name == "case insensitive" ||
				tc.name == "case sensitive"
			if needsCaseSensitiveFS && !fsCaseSensitive {
				t.Skip("filesystem is case-insensitive; skipping case-dependent subtest")
			}
			opts := []Option{WithBaseDir(base)}
			if len(tc.opts) > 0 {
				opts = append(opts, tc.opts...)
			}
			toolSet, err := NewToolSet(opts...)
			assert.NoError(t, err)
			fts := toolSet.(*fileToolSet)
			rsp, err := fts.readMultipleFiles(context.Background(), &tc.req)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			// Build actual maps for comparison.
			actualContents := map[string]string{}
			for _, f := range rsp.Files {
				actualContents[f.FileName] = f.Contents
			}
			if tc.expectedContents != nil {
				assert.Equal(t, tc.expectedContents, actualContents)
			}
		})
	}
}

func TestReadMultipleFiles_WorkspaceRef(t *testing.T) {
	set, err := NewToolSet(WithBaseDir(t.TempDir()))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)

	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)
	toolcache.StoreSkillRunOutputFiles(inv, []codeexecutor.File{
		{
			Name:     "out/a.txt",
			Content:  "a",
			MIMEType: "text/plain",
		},
		{
			Name:     "out/b.txt",
			Content:  "b",
			MIMEType: "text/plain",
		},
	})

	rsp, err := fts.readMultipleFiles(ctx, &readMultipleFilesRequest{
		Patterns: []string{"workspace://out/*.txt"},
	})
	assert.NoError(t, err)
	actual := map[string]string{}
	for _, f := range rsp.Files {
		actual[f.FileName] = f.Contents
	}
	assert.Equal(t, map[string]string{
		"workspace://out/a.txt": "a",
		"workspace://out/b.txt": "b",
	}, actual)

	rsp, err = fts.readMultipleFiles(ctx, &readMultipleFilesRequest{
		Patterns: []string{"out/*.txt"},
	})
	assert.NoError(t, err)
	actual = map[string]string{}
	for _, f := range rsp.Files {
		actual[f.FileName] = f.Contents
	}
	assert.Equal(t, map[string]string{
		"workspace://out/a.txt": "a",
		"workspace://out/b.txt": "b",
	}, actual)
}

func TestReadMultipleFiles_ArtifactGlobRejected(t *testing.T) {
	set, err := NewToolSet(WithBaseDir(t.TempDir()))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)

	rsp, err := fts.readMultipleFiles(
		context.Background(),
		&readMultipleFilesRequest{
			Patterns: []string{"artifact://x*.txt"},
		},
	)
	assert.NoError(t, err)
	assert.Empty(t, rsp.Files)
	assert.Contains(t, rsp.Message, "artifact:// does not support glob")
}

func TestReadMultipleFiles_ArtifactRef_ReadError(t *testing.T) {
	set, err := NewToolSet(WithBaseDir(t.TempDir()))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)

	rsp, err := fts.readMultipleFiles(
		context.Background(),
		&readMultipleFilesRequest{
			Patterns: []string{"artifact://"},
		},
	)
	assert.NoError(t, err)
	assert.Len(t, rsp.Files, 1)
	assert.Equal(t, "artifact://", rsp.Files[0].FileName)
	assert.Contains(t, rsp.Files[0].Message, "artifact name is empty")
}

func TestReadMultipleFiles_WorkspacePatternInvalid(t *testing.T) {
	set, err := NewToolSet(WithBaseDir(t.TempDir()))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)

	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)
	toolcache.StoreSkillRunOutputFiles(inv, []codeexecutor.File{
		{
			Name:     "out/a.txt",
			Content:  "a",
			MIMEType: "text/plain",
		},
	})

	rsp, err := fts.readMultipleFiles(ctx, &readMultipleFilesRequest{
		Patterns: []string{"workspace://["},
	})
	assert.NoError(t, err)
	assert.Empty(t, rsp.Files)
	assert.Contains(t, rsp.Message, "In finding files matched")
}

func TestReadMultipleFiles_WorkspaceFileTooLarge(t *testing.T) {
	set, err := NewToolSet(
		WithBaseDir(t.TempDir()),
		WithMaxFileSize(1),
	)
	assert.NoError(t, err)
	fts := set.(*fileToolSet)

	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)
	toolcache.StoreSkillRunOutputFiles(inv, []codeexecutor.File{
		{
			Name:     "out/a.txt",
			Content:  "aa",
			MIMEType: "text/plain",
		},
	})

	rsp, err := fts.readMultipleFiles(ctx, &readMultipleFilesRequest{
		Patterns: []string{"workspace://out/*.txt"},
	})
	assert.NoError(t, err)
	assert.Len(t, rsp.Files, 1)
	assert.Equal(t, "workspace://out/a.txt", rsp.Files[0].FileName)
	assert.Contains(t, rsp.Files[0].Message, "too large")
}

func TestReadFiles_PathResolveError(t *testing.T) {
	set, err := NewToolSet(WithBaseDir(t.TempDir()))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)

	res := fts.readFiles(
		context.Background(),
		[]string{"../evil.txt"},
	)
	assert.Len(t, res, 1)
	assert.Contains(t, res[0].Message, "cannot resolve")
}
