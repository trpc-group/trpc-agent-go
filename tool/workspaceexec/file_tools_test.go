//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package workspaceexec

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// callAs marshals the input struct to JSON and invokes the tool
// Call method. All workspace file tools share the same JSON-driven
// contract so a single helper is enough.
func callAs(t *testing.T, tl tool.CallableTool, in any) any {
	t.Helper()
	enc, err := json.Marshal(in)
	require.NoError(t, err)
	res, err := tl.Call(context.Background(), enc)
	require.NoError(t, err)
	return res
}

// callErr is the negative-path counterpart to callAs. It asserts
// the Call returned an error so tests can focus on the message text
// rather than repeating the err!=nil boilerplate.
func callErr(t *testing.T, tl tool.CallableTool, in any) error {
	t.Helper()
	enc, err := json.Marshal(in)
	require.NoError(t, err)
	_, callError := tl.Call(context.Background(), enc)
	require.Error(t, callError)
	return callError
}

// seedWorkspace materializes a handful of files inside the shared
// executor workspace using the same PutFiles path that file tools
// exercise. Seeding through the engine (rather than reaching into
// ws.Path) keeps the tests honest about the production API surface.
func seedWorkspace(
	t *testing.T,
	exec *ExecTool,
	files map[string]string,
) (codeexecutor.Engine, codeexecutor.Workspace) {
	t.Helper()
	eng, ws, err := exec.prepareForFileTool(context.Background())
	require.NoError(t, err)
	puts := make([]codeexecutor.PutFile, 0, len(files))
	for p, c := range files {
		puts = append(puts, codeexecutor.PutFile{
			Path:    p,
			Content: []byte(c),
			Mode:    0o644,
		})
	}
	require.NoError(t, eng.FS().PutFiles(context.Background(), ws, puts))
	return eng, ws
}

func newExecToolForFileTests(t *testing.T) *ExecTool {
	t.Helper()
	return NewExecTool(localexec.New())
}

// ------- ReadFile -------

func TestReadFileTool_ReadsWholeFile(t *testing.T) {
	exec := newExecToolForFileTests(t)
	seedWorkspace(t, exec, map[string]string{
		"work/hello.txt": "line1\nline2\nline3\n",
	})

	tl := newReadFileTool(exec)
	res := callAs(t, tl, readFileInput{Path: "work/hello.txt"}).(readFileOutput)

	require.Equal(t, "work/hello.txt", res.Path)
	require.Equal(t, "line1\nline2\nline3", res.Contents)
	require.Equal(t, 3, res.TotalLines)
	require.Equal(t, 1, res.StartLine)
	require.Equal(t, 3, res.EndLine)
	require.False(t, res.Truncated)
}

func TestReadFileTool_LineWindow(t *testing.T) {
	exec := newExecToolForFileTests(t)
	seedWorkspace(t, exec, map[string]string{
		"work/hello.txt": "l1\nl2\nl3\nl4\nl5\n",
	})

	tl := newReadFileTool(exec)
	res := callAs(t, tl, readFileInput{
		Path: "work/hello.txt", StartLine: 2, NumLines: 2,
	}).(readFileOutput)

	require.Equal(t, "l2\nl3", res.Contents)
	require.Equal(t, 2, res.StartLine)
	require.Equal(t, 3, res.EndLine)
	require.Equal(t, 5, res.TotalLines)
}

func TestReadFileTool_MissingPath(t *testing.T) {
	exec := newExecToolForFileTests(t)
	tl := newReadFileTool(exec)
	err := callErr(t, tl, readFileInput{Path: "work/does-not-exist.txt"})
	require.Contains(t, err.Error(), "not found")
}

func TestReadFileTool_RejectsBinary(t *testing.T) {
	exec := newExecToolForFileTests(t)
	seedWorkspace(t, exec, map[string]string{
		"work/bin.dat": "okay\x00trailer",
	})
	tl := newReadFileTool(exec)
	err := callErr(t, tl, readFileInput{Path: "work/bin.dat"})
	require.Contains(t, err.Error(), "not valid UTF-8 text")
}

// ------- ListDir -------

func TestListDirTool_DirectChildrenOnly(t *testing.T) {
	exec := newExecToolForFileTests(t)
	seedWorkspace(t, exec, map[string]string{
		"work/a.txt":       "a",
		"work/nested/b.txt": "bb",
	})

	tl := newListDirTool(exec)
	res := callAs(t, tl, listDirInput{Path: "work"}).(listDirOutput)

	names := map[string]string{}
	for _, e := range res.Entries {
		names[e.Name] = e.Type
	}
	require.Equal(t, "file", names["a.txt"])
	require.Equal(t, "dir", names["nested"])
	require.NotContains(t, names, "b.txt")
}

func TestListDirTool_NotDir(t *testing.T) {
	exec := newExecToolForFileTests(t)
	seedWorkspace(t, exec, map[string]string{"work/file.txt": "x"})
	tl := newListDirTool(exec)
	err := callErr(t, tl, listDirInput{Path: "work/file.txt"})
	require.Contains(t, err.Error(), "not a directory")
}

// ------- SearchFile -------

func TestSearchFileTool_GlobMatch(t *testing.T) {
	exec := newExecToolForFileTests(t)
	seedWorkspace(t, exec, map[string]string{
		"work/a.go":       "package a",
		"work/b.py":       "print(1)",
		"work/inner/c.go": "package c",
	})

	tl := newSearchFileTool(exec)
	res := callAs(t, tl, searchFileInput{
		Path: "work", Pattern: "**/*.go", FilesOnly: true,
	}).(searchFileOutput)

	paths := make(map[string]bool)
	for _, m := range res.Matches {
		paths[m.Path] = true
	}
	require.True(t, paths["work/a.go"])
	require.True(t, paths["work/inner/c.go"])
	require.False(t, paths["work/b.py"])
}

// TestSearchFileTool_PropagatesTreeTruncation exercises the
// listTreeAll truncation signal. Lowering the tree cap by hand
// (rather than generating >5000 entries) keeps the test fast while
// still proving that Search* propagates the flag instead of
// dropping it inside the backend.
func TestSearchFileTool_PropagatesTreeTruncation(t *testing.T) {
	orig := maxListingEntriesOverride
	maxListingEntriesOverride = 3
	t.Cleanup(func() { maxListingEntriesOverride = orig })

	exec := newExecToolForFileTests(t)
	seedWorkspace(t, exec, map[string]string{
		"work/a.txt": "1",
		"work/b.txt": "2",
		"work/c.txt": "3",
		"work/d.txt": "4",
		"work/e.txt": "5",
	})

	tl := newSearchFileTool(exec)
	res := callAs(t, tl, searchFileInput{
		Path: "work", Pattern: "*", FilesOnly: true,
		MaxResults: 100,
	}).(searchFileOutput)
	require.True(t, res.Truncated)
}

// ------- SearchContent -------

func TestSearchContentTool_RegexMatch(t *testing.T) {
	exec := newExecToolForFileTests(t)
	seedWorkspace(t, exec, map[string]string{
		"work/a.txt": "hello world\nfoo bar\nHELLO AGAIN\n",
		"work/b.txt": "no relevant content\n",
	})

	tl := newSearchContentTool(exec)
	res := callAs(t, tl, searchContentInput{
		Path: "work", Pattern: "hello", CaseInsensitive: true,
	}).(searchContentOutput)

	require.GreaterOrEqual(t, len(res.Matches), 2)
	var linesA []int
	for _, m := range res.Matches {
		if m.Path == "work/a.txt" {
			linesA = append(linesA, m.Line)
		}
	}
	require.Equal(t, []int{1, 3}, linesA)
}

func TestSearchContentTool_RestrictedByFileGlob(t *testing.T) {
	exec := newExecToolForFileTests(t)
	seedWorkspace(t, exec, map[string]string{
		"work/a.txt": "keyword\n",
		"work/a.go":  "keyword\n",
	})
	tl := newSearchContentTool(exec)
	res := callAs(t, tl, searchContentInput{
		Path: "work", Pattern: "keyword", FileGlob: "*.go",
	}).(searchContentOutput)
	require.Len(t, res.Matches, 1)
	require.Equal(t, "work/a.go", res.Matches[0].Path)
}

// TestSearchContentTool_PartialFileFlagged pins down the
// files_partial / truncated behavior added so the model is told
// when a file was only scanned up to max_bytes. Without this flag
// an empty result set would be indistinguishable from the file
// having no matches below the byte cap.
func TestSearchContentTool_PartialFileFlagged(t *testing.T) {
	exec := newExecToolForFileTests(t)
	var big strings.Builder
	for i := 0; i < 10; i++ {
		big.WriteString("filler line nothing here\n")
	}
	big.WriteString("NEEDLE line at the end\n")
	seedWorkspace(t, exec, map[string]string{
		"work/big.txt": big.String(),
	})
	tl := newSearchContentTool(exec)
	res := callAs(t, tl, searchContentInput{
		Path:     "work",
		Pattern:  "NEEDLE",
		MaxBytes: 32, // Force the read to stop inside the prefix.
	}).(searchContentOutput)
	require.Equal(t, 1, res.FilesScanned)
	require.Equal(t, 1, res.FilesPartial)
	require.True(t, res.Truncated)
	require.Empty(t, res.Matches)
}

// ------- WriteFile -------

func TestWriteFileTool_CreatesAndRefusesOverwrite(t *testing.T) {
	exec := newExecToolForFileTests(t)
	// Prime the workspace so PutFiles root exists even if nothing
	// was seeded.
	_, _, err := exec.prepareForFileTool(context.Background())
	require.NoError(t, err)

	tl := newWriteFileTool(exec)
	res := callAs(t, tl, writeFileInput{
		Path: "work/note.txt", Contents: "hi\n",
	}).(writeFileOutput)
	require.Equal(t, 3, res.BytesWritten)
	require.False(t, res.Overwritten)

	// Second write without overwrite must fail.
	err = callErr(t, tl, writeFileInput{
		Path: "work/note.txt", Contents: "again\n",
	})
	require.Contains(t, err.Error(), "already exists")

	// With overwrite=true it should succeed and report overwritten.
	res = callAs(t, tl, writeFileInput{
		Path: "work/note.txt", Contents: "again\n", Overwrite: true,
	}).(writeFileOutput)
	require.True(t, res.Overwritten)
}

func TestWriteFileTool_RejectsProtectedPaths(t *testing.T) {
	exec := newExecToolForFileTests(t)
	_, _, err := exec.prepareForFileTool(context.Background())
	require.NoError(t, err)

	tl := newWriteFileTool(exec)
	err = callErr(t, tl, writeFileInput{
		Path: "work/inputs/evil.txt", Contents: "x",
	})
	require.Contains(t, err.Error(), "managed by the framework")

	err = callErr(t, tl, writeFileInput{
		Path: "skills/evil/SKILL.md", Contents: "x",
	})
	require.Contains(t, err.Error(), "managed by the framework")
}

func TestWriteFileTool_RejectsBootstrapTargets(t *testing.T) {
	exec := NewExecTool(
		localexec.New(),
		WithWorkspaceBootstrap(codeexecutor.WorkspaceBootstrapSpec{
			Files: []codeexecutor.WorkspaceFile{
				{Target: "work/config.json", Content: []byte("{}")},
			},
		}),
	)
	_, _, err := exec.prepareForFileTool(context.Background())
	require.NoError(t, err)

	tl := newWriteFileTool(exec)
	err = callErr(t, tl, writeFileInput{
		Path: "work/config.json", Contents: "{}", Overwrite: true,
	})
	require.Contains(t, err.Error(), "managed by the framework")
}

// ------- ReplaceContent -------

func TestReplaceContentTool_LiteralFirstOccurrence(t *testing.T) {
	exec := newExecToolForFileTests(t)
	seedWorkspace(t, exec, map[string]string{
		"work/note.txt": "alpha beta alpha",
	})
	tl := newReplaceContentTool(exec)
	res := callAs(t, tl, replaceContentInput{
		Path: "work/note.txt", OldString: "alpha", NewString: "gamma",
	}).(replaceContentOutput)
	require.Equal(t, 1, res.NumReplacements)
	require.Equal(t, 2, res.TotalMatches)

	rd := callAs(
		t, newReadFileTool(exec),
		readFileInput{Path: "work/note.txt"},
	).(readFileOutput)
	require.Equal(t, "gamma beta alpha", rd.Contents)
}

func TestReplaceContentTool_RegexReplaceAll(t *testing.T) {
	exec := newExecToolForFileTests(t)
	seedWorkspace(t, exec, map[string]string{
		"work/note.txt": "foo1 foo2 foo3",
	})
	tl := newReplaceContentTool(exec)
	res := callAs(t, tl, replaceContentInput{
		Path: "work/note.txt", OldString: `foo\d`, NewString: "X",
		Regex: true, NumReplacements: -1,
	}).(replaceContentOutput)
	require.Equal(t, 3, res.NumReplacements)
	require.Equal(t, 3, res.TotalMatches)
}

func TestReplaceContentTool_LiteralReplaceAll(t *testing.T) {
	exec := newExecToolForFileTests(t)
	seedWorkspace(t, exec, map[string]string{
		"work/note.txt": "a b a b a",
	})
	tl := newReplaceContentTool(exec)
	res := callAs(t, tl, replaceContentInput{
		Path:            "work/note.txt",
		OldString:       "a",
		NewString:       "Z",
		NumReplacements: -1,
	}).(replaceContentOutput)
	require.Equal(t, 3, res.NumReplacements)
	require.Equal(t, 3, res.TotalMatches)
	rd := callAs(
		t, newReadFileTool(exec),
		readFileInput{Path: "work/note.txt"},
	).(readFileOutput)
	require.Equal(t, "Z b Z b Z", rd.Contents)
}

func TestReplaceContentTool_LimitedReplacements(t *testing.T) {
	exec := newExecToolForFileTests(t)
	seedWorkspace(t, exec, map[string]string{
		"work/note.txt": "a b a b a",
	})
	tl := newReplaceContentTool(exec)
	res := callAs(t, tl, replaceContentInput{
		Path:            "work/note.txt",
		OldString:       "a",
		NewString:       "Z",
		NumReplacements: 2,
	}).(replaceContentOutput)
	require.Equal(t, 2, res.NumReplacements)
	require.Equal(t, 3, res.TotalMatches)
}

// TestReplaceContentTool_OldEqualsNewIsNoOp pins down the tool/file
// parity: when old_string == new_string the tool must succeed with
// zero replacements instead of surfacing an error. The file must be
// left untouched.
func TestReplaceContentTool_OldEqualsNewIsNoOp(t *testing.T) {
	exec := newExecToolForFileTests(t)
	seedWorkspace(t, exec, map[string]string{
		"work/note.txt": "alpha beta",
	})
	tl := newReplaceContentTool(exec)
	res := callAs(t, tl, replaceContentInput{
		Path: "work/note.txt", OldString: "alpha", NewString: "alpha",
	}).(replaceContentOutput)
	require.Equal(t, "work/note.txt", res.Path)
	require.Equal(t, 0, res.NumReplacements)
	require.Equal(t, 0, res.TotalMatches)
	require.Equal(t, 0, res.BytesWritten)

	rd := callAs(
		t, newReadFileTool(exec),
		readFileInput{Path: "work/note.txt"},
	).(readFileOutput)
	require.Equal(t, "alpha beta", rd.Contents)
}

func TestReplaceContentTool_NoMatchFails(t *testing.T) {
	exec := newExecToolForFileTests(t)
	seedWorkspace(t, exec, map[string]string{"work/note.txt": "abc"})
	tl := newReplaceContentTool(exec)
	err := callErr(t, tl, replaceContentInput{
		Path: "work/note.txt", OldString: "zz", NewString: "yy",
	})
	require.Contains(t, err.Error(), "no occurrences")
}

func TestReplaceContentTool_ProtectedPath(t *testing.T) {
	exec := newExecToolForFileTests(t)
	tl := newReplaceContentTool(exec)
	err := callErr(t, tl, replaceContentInput{
		Path: "skills/foo/SKILL.md", OldString: "a", NewString: "b",
	})
	require.Contains(t, err.Error(), "managed by the framework")
}

// ------- NewFileTools + FileToolsOptions -------

func TestNewFileTools_DefaultRegistersAll(t *testing.T) {
	exec := newExecToolForFileTests(t)
	tools := NewFileTools(exec, FileToolsOptions{})
	names := map[string]bool{}
	for _, tl := range tools {
		if d := tl.Declaration(); d != nil {
			names[d.Name] = true
		}
	}
	for _, want := range []string{
		"workspace_read_file",
		"workspace_list_dir",
		"workspace_search_file",
		"workspace_search_content",
		"workspace_write_file",
		"workspace_replace_content",
	} {
		require.True(t, names[want], "missing tool %s", want)
	}
}

func TestNewFileTools_DisableFlags(t *testing.T) {
	exec := newExecToolForFileTests(t)
	tools := NewFileTools(exec, FileToolsOptions{
		DisableWriteFile:      true,
		DisableReplaceContent: true,
	})
	names := map[string]bool{}
	for _, tl := range tools {
		if d := tl.Declaration(); d != nil {
			names[d.Name] = true
		}
	}
	require.False(t, names["workspace_write_file"])
	require.False(t, names["workspace_replace_content"])
	require.True(t, names["workspace_read_file"])
}

func TestNewFileTools_NilExecReturnsNil(t *testing.T) {
	require.Nil(t, NewFileTools(nil, FileToolsOptions{}))
}
