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
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolcache"
)

// writeTree creates files under dir from relative path to content.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
	}
}

func newSearchToolSet(t *testing.T, dir string, opts ...Option) *fileToolSet {
	t.Helper()
	ts, err := NewToolSet(append([]Option{WithBaseDir(dir)}, opts...)...)
	require.NoError(t, err)
	return ts.(*fileToolSet)
}

func searchContentPaths(t *testing.T, f *fileToolSet, path, pattern string) []string {
	t.Helper()
	rsp, err := f.searchContent(context.Background(), &searchContentRequest{
		Path:                 path,
		FilePattern:          pattern,
		ContentPattern:       "needle",
		ContentCaseSensitive: true,
	})
	require.NoError(t, err)
	var out []string
	for _, m := range rsp.FileMatches {
		out = append(out, filepath.ToSlash(m.FilePath))
	}
	return out
}

func TestSearchContent_HonoursGitignore(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		".gitignore":                  "node_modules/\n*.log\n!keep.log\n/dist\n",
		"src/main.go":                 "needle\n",
		"src/.gitignore":              "gen/\n",
		"src/gen/out.go":              "needle\n",
		"node_modules/pkg/index.js":   "needle\n",
		"src/node_modules/x/index.js": "needle\n",
		"debug.log":                   "needle\n",
		"keep.log":                    "needle\n",
		"dist/bundle.js":              "needle\n",
		"src/dist/kept.js":            "needle\n",
		".git/config":                 "needle\n",
		".git/hooks/pre-commit":       "needle\n",
	})

	f := newSearchToolSet(t, dir)
	got := searchContentPaths(t, f, "", "**/*")
	assert.ElementsMatch(t, []string{
		"src/main.go",
		"keep.log",
		"src/dist/kept.js",
	}, got)

	off := newSearchToolSet(t, dir, WithSearchGitignore(false))
	got = searchContentPaths(t, off, "", "**/*")
	assert.ElementsMatch(t, []string{
		"src/main.go",
		"src/gen/out.go",
		"node_modules/pkg/index.js",
		"src/node_modules/x/index.js",
		"debug.log",
		"keep.log",
		"dist/bundle.js",
		"src/dist/kept.js",
	}, got, ".git stays out even with ignore files off")
}

func TestSearchContent_AncestorGitignoreAppliesToSubdirectorySearch(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		".gitignore":                "build/\n",
		"projects/.gitignore":       "*.tmp\n",
		"projects/a/src/x.kt":       "needle\n",
		"projects/a/build/y.kt":     "needle\n",
		"projects/a/notes.tmp":      "needle\n",
		"projects/a/sub/.gitignore": "secret.kt\n",
		"projects/a/sub/secret.kt":  "needle\n",
		"projects/a/sub/open.kt":    "needle\n",
	})
	f := newSearchToolSet(t, dir)
	got := searchContentPaths(t, f, "projects/a", "**/*")
	assert.ElementsMatch(t, []string{
		"projects/a/src/x.kt",
		"projects/a/sub/open.kt",
	}, got)
}

func TestSearchContent_ExplicitlyTargetedIgnoredDirectoryIsSearched(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		".gitignore":                   "node_modules/\n*.log\n",
		"src/main.go":                  "needle\n",
		"node_modules/pkg/index.js":    "needle\n",
		"node_modules/pkg/debug.log":   "needle\n",
		"node_modules/pkg/.gitignore":  "dist/\n",
		"node_modules/pkg/dist/out.js": "needle\n",
	})
	f := newSearchToolSet(t, dir)
	assert.ElementsMatch(t, []string{"src/main.go"},
		searchContentPaths(t, f, "", "**/*"))
	assert.ElementsMatch(t, []string{"node_modules/pkg/index.js"},
		searchContentPaths(t, f, "node_modules", "**/*"),
		"the target is searched, ancestor and nested rules still prune inside it")
}

func TestSearchContent_WorkspaceHonoursFileLimit(t *testing.T) {
	f := newSearchToolSet(t, t.TempDir(), WithSearchMaxFiles(2))
	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)
	toolcache.StoreSkillRunOutputFiles(inv, []codeexecutor.File{
		{Name: "out/a.txt", Content: "needle\n", MIMEType: "text/plain"},
		{Name: "out/b.txt", Content: "needle\n", MIMEType: "text/plain"},
		{Name: "out/c.txt", Content: "needle\n", MIMEType: "text/plain"},
	})
	rsp, err := f.searchContent(ctx, &searchContentRequest{
		Path:           "workspace://out",
		FilePattern:    "*.txt",
		ContentPattern: "needle",
	})
	require.Error(t, err)
	var tooMany *tooManyFilesError
	assert.ErrorAs(t, err, &tooMany)
	assert.Contains(t, rsp.Message, "more than 2 entries")

	rsp, err = f.searchContent(ctx, &searchContentRequest{
		Path:           "workspace://out",
		FilePattern:    "[ab].txt",
		ContentPattern: "needle",
	})
	require.NoError(t, err)
	assert.Len(t, rsp.FileMatches, 2)

	_, err = f.searchFile(ctx, &searchFileRequest{
		Path:    "workspace://out",
		Pattern: "*.txt",
	})
	require.Error(t, err)
	assert.ErrorAs(t, err, &tooMany)
	sf, err := f.searchFile(ctx, &searchFileRequest{
		Path:    "workspace://out",
		Pattern: "[ab].txt",
	})
	require.NoError(t, err)
	assert.Len(t, sf.Files, 2)

	// A path that exists only in the workspace falls back to the same
	// entries and is bound by the same limit as the explicit workspace ref.
	sf, err = f.searchFile(ctx, &searchFileRequest{
		Path:    "out",
		Pattern: "*.txt",
	})
	require.Error(t, err)
	assert.ErrorAs(t, err, &tooMany)
	assert.Contains(t, sf.Message, "more than 2 entries")
	sf, err = f.searchFile(ctx, &searchFileRequest{
		Path:    "out",
		Pattern: "[ab].txt",
	})
	require.NoError(t, err)
	assert.Len(t, sf.Files, 2)
}

func TestSearchTextContent_BoundedAndCancellable(t *testing.T) {
	re := regexp.MustCompile("needle")
	content := strings.Repeat("needle\n", maxMatchesPerFile+5)
	m := searchTextContent(context.Background(), "p", content, re)
	assert.Len(t, m.Matches, maxMatchesPerFile)
	assert.True(t, m.Truncated)
	assert.Contains(t, fileMatchMessage(m, "p"), "stopped at the first")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m = searchTextContent(ctx, "p", content, re)
	assert.Empty(t, m.Matches)

	f := newSearchToolSet(t, t.TempDir())
	inv := agent.NewInvocation()
	ictx, icancel := context.WithCancel(agent.NewInvocationContext(ctx, inv))
	icancel()
	toolcache.StoreSkillRunOutputFiles(inv, []codeexecutor.File{
		{Name: "out/a.txt", Content: content, MIMEType: "text/plain"},
	})
	_, err := f.searchContent(ictx, &searchContentRequest{
		Path:           "",
		FilePattern:    "workspace://out/a.txt",
		ContentPattern: "needle",
	})
	assert.ErrorIs(t, err, context.Canceled)
}

func TestSearchContent_RefusesTooManyFiles(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{}
	for i := 0; i < 12; i++ {
		files[fmt.Sprintf("vendor/f%02d.txt", i)] = "needle\n"
	}
	writeTree(t, dir, files)
	f := newSearchToolSet(t, dir, WithSearchMaxFiles(10))
	rsp, err := f.searchContent(context.Background(), &searchContentRequest{
		Path:           "vendor",
		FilePattern:    "*",
		ContentPattern: "needle",
	})
	require.Error(t, err)
	var tooMany *tooManyFilesError
	assert.ErrorAs(t, err, &tooMany)
	assert.Contains(t, err.Error(), "more than 10 entries under 'vendor'")
	assert.Contains(t, err.Error(), "narrow path")
	assert.Contains(t, rsp.Message, "Error:")

	// A narrower pattern under the same limit succeeds.
	got := searchContentPaths(t, f, "vendor", "f0*.txt")
	assert.Len(t, got, 10)
}

func TestSearchFile_RefusesTooManyFilesAndHonoursGitignore(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		".gitignore":              "node_modules/\n",
		"node_modules/a/index.js": "",
		"node_modules/b/index.js": "",
		"src/a.js":                "",
		"src/b.js":                "",
	}
	writeTree(t, dir, files)
	f := newSearchToolSet(t, dir, WithSearchMaxFiles(3))
	rsp, err := f.searchFile(context.Background(), &searchFileRequest{
		Path:    "",
		Pattern: "**/*.js",
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"src/a.js", "src/b.js"}, rsp.Files)

	off := newSearchToolSet(t, dir, WithSearchMaxFiles(3), WithSearchGitignore(false))
	_, err = off.searchFile(context.Background(), &searchFileRequest{
		Path:    "",
		Pattern: "**/*.js",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "more than 3 entries under 'base_directory'")
}

func TestSearchContent_CapsTotalMatches(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"a.txt": "needle 1\nneedle 2\n",
		"b.txt": "needle 1\nneedle 2\n",
		"c.txt": "needle 1\n",
		"d.txt": "nothing\n",
	})
	f := newSearchToolSet(t, dir, WithSearchMaxMatches(3))
	rsp, err := f.searchContent(context.Background(), &searchContentRequest{
		Path:           "",
		FilePattern:    "*.txt",
		ContentPattern: "needle",
	})
	require.NoError(t, err)
	require.Len(t, rsp.FileMatches, 2)
	assert.Equal(t, "a.txt", rsp.FileMatches[0].FilePath)
	assert.Len(t, rsp.FileMatches[0].Matches, 2)
	assert.False(t, rsp.FileMatches[0].Truncated)
	assert.Equal(t, "b.txt", rsp.FileMatches[1].FilePath)
	assert.Len(t, rsp.FileMatches[1].Matches, 1)
	assert.True(t, rsp.FileMatches[1].Truncated)
	assert.Contains(t, rsp.FileMatches[1].Message, "stopped at the result cap")
	assert.Equal(t, 1, rsp.OmittedFiles)
	assert.Contains(t, rsp.Message, "Found 3 files matching")
	assert.Contains(t, rsp.Message, "stopped listing at 3 matching lines")
	assert.Contains(t, rsp.Message, "1 matching file(s) not shown")
}

func TestSearchContent_FilePatternRef_CappedByMaxMatches(t *testing.T) {
	f := newSearchToolSet(t, t.TempDir(), WithSearchMaxMatches(2))
	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)
	toolcache.StoreSkillRunOutputFiles(inv, []codeexecutor.File{
		{Name: "out/a.txt", Content: "needle\nneedle\nneedle\n", MIMEType: "text/plain"},
	})
	rsp, err := f.searchContent(ctx, &searchContentRequest{
		Path:           "",
		FilePattern:    "workspace://out/a.txt",
		ContentPattern: "needle",
	})
	require.NoError(t, err)
	require.Len(t, rsp.FileMatches, 1)
	assert.Len(t, rsp.FileMatches[0].Matches, 2)
	assert.True(t, rsp.FileMatches[0].Truncated)
	assert.Equal(t, 0, rsp.OmittedFiles)
	assert.Contains(t, rsp.Message, "Found 1 files matching")
	assert.Contains(t, rsp.Message, "stopped listing at 2 matching lines")
	assert.NotContains(t, rsp.Message, "not shown")
}

func TestCapSearchMatches(t *testing.T) {
	mk := func(path string, n int) *fileMatch {
		m := &fileMatch{FilePath: path}
		for i := 0; i < n; i++ {
			m.Matches = append(m.Matches, &lineMatch{LineNumber: i + 1})
		}
		return m
	}
	t.Run("no limit leaves input alone", func(t *testing.T) {
		in := []*fileMatch{mk("b", 2), mk("a", 2)}
		out, omitted, capped := capSearchMatches(in, 0)
		assert.Equal(t, 0, omitted)
		assert.False(t, capped)
		assert.Equal(t, "b", out[0].FilePath, "unsorted when uncapped")
	})
	t.Run("everything fits", func(t *testing.T) {
		out, omitted, capped := capSearchMatches([]*fileMatch{mk("b", 2), mk("a", 2)}, 4)
		assert.Equal(t, 0, omitted)
		assert.False(t, capped)
		assert.Equal(t, []string{"a", "b"}, []string{out[0].FilePath, out[1].FilePath})
	})
	t.Run("exact boundary drops the rest whole", func(t *testing.T) {
		out, omitted, capped := capSearchMatches([]*fileMatch{mk("a", 2), mk("b", 2), mk("c", 1)}, 2)
		assert.Equal(t, 2, omitted)
		assert.True(t, capped)
		require.Len(t, out, 1)
		assert.False(t, out[0].Truncated)
	})
}

func TestSearchContent_BoundedConcurrencyFindsEverything(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{}
	for i := 0; i < 200; i++ {
		files[fmt.Sprintf("d%d/f%03d.txt", i%7, i)] = strings.Repeat("filler\n", i%13) + "needle\n"
	}
	writeTree(t, dir, files)
	for _, workers := range []int{1, 3} {
		f := newSearchToolSet(t, dir, WithSearchConcurrency(workers), WithSearchMaxMatches(500))
		got := searchContentPaths(t, f, "", "**/*.txt")
		assert.Len(t, got, 200, "workers=%d", workers)
	}
}

func TestSearchDefaults(t *testing.T) {
	f := &fileToolSet{}
	assert.Equal(t, defaultSearchMaxFiles, f.searchFileLimit())
	assert.Equal(t, defaultSearchMaxMatches, f.searchMatchLimit())
	assert.GreaterOrEqual(t, f.searchWorkers(), minSearchConcurrency)
	assert.GreaterOrEqual(t, f.searchWorkers(), runtime.GOMAXPROCS(0))
	assert.False(t, f.searchIgnoreOff)

	dir := t.TempDir()
	g := newSearchToolSet(t, dir,
		WithSearchMaxFiles(0),
		WithSearchMaxMatches(-1),
		WithSearchConcurrency(0),
	)
	assert.Equal(t, defaultSearchMaxFiles, g.searchFileLimit())
	assert.Equal(t, defaultSearchMaxMatches, g.searchMatchLimit())
	assert.Equal(t, defaultSearchConcurrency(), g.searchWorkers())
}

func TestWalkMatches_TrailingSlashSelectsDirectories(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"a/x.txt":   "",
		"a/b/y.txt": "",
		"c.txt":     "",
	})
	f := newSearchToolSet(t, dir)
	entries, err := f.walkMatches(context.Background(), dir, "**/", true)
	require.NoError(t, err)
	var rels []string
	for _, e := range entries {
		assert.True(t, e.dir)
		rels = append(rels, e.rel)
	}
	assert.ElementsMatch(t, []string{"a", "a/b"}, rels)

	entries, err = f.walkMatches(context.Background(), dir, "/", true)
	require.NoError(t, err)
	assert.Empty(t, entries)

	_, err = f.walkMatches(context.Background(), dir, "", true)
	assert.Error(t, err)
	_, err = f.walkMatches(context.Background(), dir, "[bad", true)
	assert.Error(t, err)
}

func TestWalkMatches_CancelledContext(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"a.txt": "", "b.txt": ""})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := newSearchToolSet(t, dir)
	_, err := f.walkMatches(ctx, dir, "*", true)
	assert.ErrorIs(t, err, context.Canceled)
}
