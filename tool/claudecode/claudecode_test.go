//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-pdf/fpdf"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestNewToolSet_DefaultTools(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ts, err := NewToolSet(WithBaseDir(dir))
	require.NoError(t, err)
	require.NotNil(t, ts)
	require.Equal(t, defaultToolSetName, ts.Name())
	names := toolNames(ts.Tools(context.Background()))
	require.Contains(t, names, toolBash)
	require.Contains(t, names, toolRead)
	require.Contains(t, names, toolWrite)
	require.Contains(t, names, toolEdit)
	require.Contains(t, names, toolNotebookEdit)
	require.Contains(t, names, toolGlob)
	require.Contains(t, names, toolGrep)
	require.Contains(t, names, toolTaskStop)
	require.Contains(t, names, toolTaskOutput)
	require.Contains(t, names, toolToolSearch)
	require.Contains(t, names, toolWebFetch)
	require.Contains(t, names, toolWebSearch)
	require.NotContains(t, names, "EnterWorktree")
	require.NotContains(t, names, "ExitWorktree")
	require.NotContains(t, names, "WebBrowser")
	require.NotContains(t, names, "browser")
	require.NotContains(t, names, toolAskUser)
	require.NotContains(t, names, "LSP")
}

func TestNewToolSet_UsesRichToolDescriptions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ts, err := NewToolSet(WithBaseDir(dir))
	require.NoError(t, err)
	descriptions := map[string]string{}
	for _, candidate := range ts.Tools(context.Background()) {
		if candidate == nil || candidate.Declaration() == nil {
			continue
		}
		descriptions[candidate.Declaration().Name] = candidate.Declaration().Description
	}
	require.Contains(t, descriptions[toolBash], "NEVER use bash grep or rg")
	require.Contains(t, descriptions[toolRead], "PDF")
	require.Contains(t, descriptions[toolWrite], "read it with Read first")
	require.Contains(t, descriptions[toolEdit], "old_string must match")
	require.Contains(t, descriptions[toolGlob], "doublestar-style globs")
	require.Contains(t, descriptions[toolGrep], "ALWAYS use Grep")
	require.Contains(t, descriptions[toolWebFetch], "prompt is required")
	require.Contains(t, descriptions[toolWebSearch], "allowed_domains and blocked_domains")
	require.NotContains(t, descriptions, "LSP")
}

func TestNewToolSet_ReadOnlyOmitsWriteAndEdit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ts, err := NewToolSet(WithBaseDir(dir), WithReadOnly(true))
	require.NoError(t, err)
	require.NotNil(t, ts)
	names := toolNames(ts.Tools(context.Background()))
	require.Contains(t, names, toolRead)
	require.NotContains(t, names, toolWrite)
	require.NotContains(t, names, toolEdit)
	require.NotContains(t, names, toolNotebookEdit)
}

func TestToolSet_BashToolRunsCommand(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ts, err := NewToolSet(WithBaseDir(dir))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, ts.Close())
	})
	bashTool := mustCallableTool(t, ts.Tools(context.Background()), toolBash)
	out := callToolAs[bashOutput](t, bashTool, bashInput{
		Command: "printf 'hello'",
	})
	require.Equal(t, 0, out.ExitCode)
	require.Equal(t, "hello", out.Stdout)
	require.Equal(t, "hello", out.Output)
	require.False(t, out.TimedOut)
}

func TestToolSet_TaskStopStopsBackgroundBashTask(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ts, err := NewToolSet(WithBaseDir(dir))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, ts.Close())
	})
	bashTool := mustCallableTool(t, ts.Tools(context.Background()), toolBash)
	stopTool := mustCallableTool(t, ts.Tools(context.Background()), toolTaskStop)
	bgOut := callToolAs[bashOutput](t, bashTool, bashInput{
		Command:         "sleep 30",
		RunInBackground: true,
	})
	require.NotEmpty(t, bgOut.BackgroundTaskID)
	stopOut := callToolAs[taskStopOutput](t, stopTool, taskStopInput{
		TaskID: bgOut.BackgroundTaskID,
	})
	require.Equal(t, bgOut.BackgroundTaskID, stopOut.TaskID)
	require.Equal(t, toolBash, stopOut.TaskType)
	require.Contains(t, stopOut.Message, "Successfully stopped task")
}

func TestToolSet_TaskOutputReadsBackgroundBashTask(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ts, err := NewToolSet(WithBaseDir(dir))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, ts.Close())
	})
	bashTool := mustCallableTool(t, ts.Tools(context.Background()), toolBash)
	taskOutputTool := mustCallableTool(t, ts.Tools(context.Background()), toolTaskOutput)
	bgOut := callToolAs[bashOutput](t, bashTool, bashInput{
		Command:         "printf 'a'; sleep 0.3; printf 'b'",
		RunInBackground: true,
	})
	require.NotEmpty(t, bgOut.BackgroundTaskID)
	nonBlocking := callToolAs[taskOutputOutput](t, taskOutputTool, taskOutputInput{
		TaskID: bgOut.BackgroundTaskID,
		Block:  boolPtr(false),
	})
	require.NotNil(t, nonBlocking.Task)
	require.Contains(t, []string{"not_ready", "success"}, nonBlocking.RetrievalStatus)
	blocking := callToolAs[taskOutputOutput](t, taskOutputTool, taskOutputInput{
		TaskID:  bgOut.BackgroundTaskID,
		Timeout: intPtr(2_000),
	})
	require.Equal(t, "success", blocking.RetrievalStatus)
	require.NotNil(t, blocking.Task)
	require.Equal(t, toolBash, blocking.Task.TaskType)
	require.Contains(t, blocking.Task.Output, "ab")
	require.Equal(t, "completed", blocking.Task.Status)
}

func TestToolSet_ReadWriteEditFlow(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ts, err := NewToolSet(WithBaseDir(dir))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, ts.Close())
	})
	filePath := filepath.Join(dir, "notes.txt")
	writeTool := mustCallableTool(t, ts.Tools(context.Background()), toolWrite)
	readTool := mustCallableTool(t, ts.Tools(context.Background()), toolRead)
	editTool := mustCallableTool(t, ts.Tools(context.Background()), toolEdit)
	writeOut := callToolAs[writeOutput](t, writeTool, writeInput{
		FilePath: filePath,
		Content:  "hello\nworld\n",
	})
	require.Equal(t, "create", writeOut.Type)
	require.Equal(t, filePath, writeOut.FilePath)
	require.Nil(t, writeOut.OriginalFile)
	require.NotEmpty(t, writeOut.StructuredPatch)
	readOut := callToolAs[readOutput](t, readTool, readInput{
		FilePath: filePath,
	})
	require.Equal(t, "text", readOut.Type)
	require.NotNil(t, readOut.File)
	require.Equal(t, "hello\nworld\n", readOut.File.Content)
	require.Equal(t, 2, readOut.File.TotalLines)
	readDedup := callToolAs[readOutput](t, readTool, readInput{
		FilePath: filePath,
	})
	require.Equal(t, "file_unchanged", readDedup.Type)
	editOut := callToolAs[editOutput](t, editTool, editInput{
		FilePath:  filePath,
		OldString: "world",
		NewString: "claude",
	})
	require.Equal(t, filePath, editOut.FilePath)
	require.Equal(t, "world", editOut.OldString)
	require.Equal(t, "claude", editOut.NewString)
	require.Equal(t, "hello\nworld\n", editOut.OriginalFile)
	require.NotEmpty(t, editOut.StructuredPatch)
	updated := callToolAs[readOutput](t, readTool, readInput{
		FilePath: filePath,
	})
	require.Equal(t, "text", updated.Type)
	require.Equal(t, "hello\nclaude\n", updated.File.Content)
}

func TestToolSet_WriteRequiresFullReadAndRejectsStaleFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	filePath := filepath.Join(dir, "notes.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("hello\n"), 0o644))
	ts, err := NewToolSet(WithBaseDir(dir))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, ts.Close())
	})
	writeTool := mustCallableTool(t, ts.Tools(context.Background()), toolWrite)
	readTool := mustCallableTool(t, ts.Tools(context.Background()), toolRead)
	_, err = callToolRaw(writeTool, writeInput{
		FilePath: filePath,
		Content:  "rewrite\n",
	})
	require.ErrorContains(t, err, "File has not been read yet")
	_ = callToolAs[readOutput](t, readTool, readInput{
		FilePath: filePath,
		Limit:    intPtr(1),
	})
	_, err = callToolRaw(writeTool, writeInput{
		FilePath: filePath,
		Content:  "rewrite\n",
	})
	require.ErrorContains(t, err, "File has not been read yet")
	_ = callToolAs[readOutput](t, readTool, readInput{
		FilePath: filePath,
	})
	future := time.Now().Add(2 * time.Second)
	require.NoError(t, os.WriteFile(filePath, []byte("hello\n"), 0o644))
	require.NoError(t, os.Chtimes(filePath, future, future))
	out := callToolAs[writeOutput](t, writeTool, writeInput{
		FilePath: filePath,
		Content:  "rewrite\n",
	})
	require.Equal(t, "update", out.Type)
	require.Equal(t, "hello\n", derefString(out.OriginalFile))
	_ = callToolAs[readOutput](t, readTool, readInput{
		FilePath: filePath,
	})
	require.NoError(t, os.WriteFile(filePath, []byte("user update\n"), 0o644))
	require.NoError(t, os.Chtimes(filePath, future.Add(2*time.Second), future.Add(2*time.Second)))
	_, err = callToolRaw(writeTool, writeInput{
		FilePath: filePath,
		Content:  "rewrite again\n",
	})
	require.ErrorContains(t, err, "File has been modified since read")
}

func TestToolSet_EditRejectsNotebookAndPreservesCurlyQuotes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ts, err := NewToolSet(WithBaseDir(dir))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, ts.Close())
	})
	editTool := mustCallableTool(t, ts.Tools(context.Background()), toolEdit)
	readTool := mustCallableTool(t, ts.Tools(context.Background()), toolRead)
	notebookPath := filepath.Join(dir, "book.ipynb")
	require.NoError(t, os.WriteFile(notebookPath, []byte(`{"cells":[]}`), 0o644))
	_, err = callToolRaw(editTool, editInput{
		FilePath:  notebookPath,
		OldString: "{}",
		NewString: "[]",
	})
	require.ErrorContains(t, err, "NotebookEdit")
	filePath := filepath.Join(dir, "quotes.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("“hello”\n"), 0o644))
	_ = callToolAs[readOutput](t, readTool, readInput{
		FilePath: filePath,
	})
	out := callToolAs[editOutput](t, editTool, editInput{
		FilePath:  filePath,
		OldString: "\"hello\"",
		NewString: "\"world\"",
	})
	require.Equal(t, "\"hello\"", out.OldString)
	updated, err := os.ReadFile(filePath)
	require.NoError(t, err)
	require.Equal(t, "“world”\n", string(updated))
}

func TestToolSet_ReadSupportsNotebookImageAndDedup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ts, err := NewToolSet(WithBaseDir(dir))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, ts.Close())
	})
	readTool := mustCallableTool(t, ts.Tools(context.Background()), toolRead)
	notebookPath := filepath.Join(dir, "book.ipynb")
	require.NoError(t, os.WriteFile(notebookPath, []byte(`{"cells":[{"cell_type":"markdown","source":["hello"]}]}`), 0o644))
	notebookOut := callToolAs[readOutput](t, readTool, readInput{
		FilePath: notebookPath,
	})
	require.Equal(t, "notebook", notebookOut.Type)
	require.Len(t, notebookOut.File.Cells, 1)
	imagePath := filepath.Join(dir, "tiny.png")
	require.NoError(t, os.WriteFile(imagePath, tinyPNGBytes, 0o644))
	imageOut := callToolAs[readOutput](t, readTool, readInput{
		FilePath: imagePath,
	})
	require.Equal(t, "image", imageOut.Type)
	require.NotEmpty(t, imageOut.File.Base64)
	require.Contains(t, imageOut.File.MediaType, "image/png")
	imageDedup := callToolAs[readOutput](t, readTool, readInput{
		FilePath: imagePath,
	})
	require.Equal(t, "file_unchanged", imageDedup.Type)
}

func TestToolSet_ReadSupportsPDFAndPageRanges(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pdfPath := filepath.Join(dir, "paper.pdf")
	require.NoError(t, os.WriteFile(pdfPath, newTestPDF(t, []string{"Page 1", "Page 2"}), 0o644))
	ts, err := NewToolSet(WithBaseDir(dir))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, ts.Close())
	})
	readTool := mustCallableTool(t, ts.Tools(context.Background()), toolRead)
	fullOut := callToolAs[readOutput](t, readTool, readInput{FilePath: pdfPath})
	require.Equal(t, "pdf", fullOut.Type)
	require.NotEmpty(t, fullOut.File.Base64)
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		_, err = callToolRaw(readTool, readInput{
			FilePath: pdfPath,
			Pages:    "1",
		})
		require.ErrorContains(t, err, "pdftoppm is not installed")
		return
	}
	pageOut := callToolAs[readOutput](t, readTool, readInput{
		FilePath: pdfPath,
		Pages:    "1",
	})
	require.Equal(t, "parts", pageOut.Type)
	require.Equal(t, 1, pageOut.File.Count)
	require.NotEmpty(t, pageOut.File.OutputDir)
	rendered, err := filepath.Glob(filepath.Join(pageOut.File.OutputDir, "*.jpg"))
	require.NoError(t, err)
	require.Len(t, rendered, 1)
}

func TestToolSet_ReadRejectsLargePDFWithoutPages(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pdfPath := filepath.Join(dir, "large.pdf")
	pages := make([]string, 0, pdfMaxPagesPerRead+5)
	for idx := 0; idx < pdfMaxPagesPerRead+5; idx++ {
		pages = append(pages, "Page "+strconvString(idx+1))
	}
	require.NoError(t, os.WriteFile(pdfPath, newTestPDF(t, pages), 0o644))
	ts, err := NewToolSet(WithBaseDir(dir))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, ts.Close())
	})
	readTool := mustCallableTool(t, ts.Tools(context.Background()), toolRead)
	_, err = callToolRaw(readTool, readInput{FilePath: pdfPath})
	require.ErrorContains(t, err, "too many to read at once")
	_, err = callToolRaw(readTool, readInput{
		FilePath: pdfPath,
		Pages:    "1-21",
	})
	require.ErrorContains(t, err, "exceeds maximum")
}

func TestToolSet_NotebookEditFlow(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	notebookPath := filepath.Join(dir, "book.ipynb")
	require.NoError(t, os.WriteFile(notebookPath, []byte(`{"cells":[{"id":"intro","cell_type":"markdown","source":"hello","metadata":{}}],"metadata":{"language_info":{"name":"python"}},"nbformat":4,"nbformat_minor":5}`), 0o644))
	ts, err := NewToolSet(WithBaseDir(dir))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, ts.Close())
	})
	readTool := mustCallableTool(t, ts.Tools(context.Background()), toolRead)
	notebookEditTool := mustCallableTool(t, ts.Tools(context.Background()), toolNotebookEdit)
	_ = callToolAs[readOutput](t, readTool, readInput{
		FilePath: notebookPath,
	})
	replaceOut := callToolAs[notebookEditOutput](t, notebookEditTool, notebookEditInput{
		NotebookPath: notebookPath,
		CellID:       "intro",
		NewSource:    "updated",
		EditMode:     "replace",
	})
	require.Equal(t, "replace", replaceOut.EditMode)
	require.Equal(t, "intro", replaceOut.CellID)
	require.Equal(t, "markdown", replaceOut.CellType)
	require.Equal(t, "python", replaceOut.Language)
	insertOut := callToolAs[notebookEditOutput](t, notebookEditTool, notebookEditInput{
		NotebookPath: notebookPath,
		CellID:       "intro",
		NewSource:    "print(1)",
		CellType:     "code",
		EditMode:     "insert",
	})
	require.Equal(t, "insert", insertOut.EditMode)
	require.NotEmpty(t, insertOut.CellID)
	deleteOut := callToolAs[notebookEditOutput](t, notebookEditTool, notebookEditInput{
		NotebookPath: notebookPath,
		CellID:       insertOut.CellID,
		NewSource:    "",
		EditMode:     "delete",
	})
	require.Equal(t, "delete", deleteOut.EditMode)
	rawNotebook, err := os.ReadFile(notebookPath)
	require.NoError(t, err)
	var decoded struct {
		Cells []map[string]any `json:"cells"`
	}
	require.NoError(t, json.Unmarshal(rawNotebook, &decoded))
	require.Len(t, decoded.Cells, 1)
	require.Equal(t, "updated", decoded.Cells[0]["source"])
}

func TestToolSet_NotebookEditRejectsUnreadAndStaleNotebook(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	notebookPath := filepath.Join(dir, "book.ipynb")
	require.NoError(t, os.WriteFile(notebookPath, []byte(`{"cells":[{"id":"intro","cell_type":"markdown","source":"hello","metadata":{}}],"metadata":{"language_info":{"name":"python"}},"nbformat":4,"nbformat_minor":5}`), 0o644))
	ts, err := NewToolSet(WithBaseDir(dir))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, ts.Close())
	})
	readTool := mustCallableTool(t, ts.Tools(context.Background()), toolRead)
	notebookEditTool := mustCallableTool(t, ts.Tools(context.Background()), toolNotebookEdit)
	_, err = callToolRaw(notebookEditTool, notebookEditInput{
		NotebookPath: notebookPath,
		CellID:       "intro",
		NewSource:    "updated",
		EditMode:     "replace",
	})
	require.ErrorContains(t, err, "File has not been read yet")
	_ = callToolAs[readOutput](t, readTool, readInput{
		FilePath: notebookPath,
	})
	future := time.Now().Add(2 * time.Second)
	require.NoError(t, os.WriteFile(notebookPath, []byte(`{"cells":[{"id":"intro","cell_type":"markdown","source":"external","metadata":{}}],"metadata":{"language_info":{"name":"python"}},"nbformat":4,"nbformat_minor":5}`), 0o644))
	require.NoError(t, os.Chtimes(notebookPath, future, future))
	_, err = callToolRaw(notebookEditTool, notebookEditInput{
		NotebookPath: notebookPath,
		CellID:       "intro",
		NewSource:    "updated",
		EditMode:     "replace",
	})
	require.ErrorContains(t, err, "File has been modified since read")
}

func TestToolSet_GlobStandalone(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for idx := 0; idx < 120; idx++ {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "f"+strconvString(idx)+".txt"), []byte("x"), 0o644))
	}
	ts, err := NewToolSet(WithBaseDir(dir))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, ts.Close())
	})
	globTool := mustCallableTool(t, ts.Tools(context.Background()), toolGlob)
	out := callToolAs[globOutput](t, globTool, globInput{
		Pattern: "*.txt",
	})
	require.Equal(t, defaultGlobHeadLimit, out.NumFiles)
	require.True(t, out.Truncated)
	require.NotZero(t, out.DurationMs)
}

func TestToolSet_GlobPathValidationErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hello"), 0o644))
	ts, err := NewToolSet(WithBaseDir(dir))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, ts.Close())
	})
	globTool := mustCallableTool(t, ts.Tools(context.Background()), toolGlob)
	_, err = callToolRaw(globTool, globInput{
		Pattern: "*.txt",
		Path:    "missing",
	})
	require.ErrorContains(t, err, "Directory does not exist: missing")
	_, err = callToolRaw(globTool, globInput{
		Pattern: "*.txt",
		Path:    "note.txt",
	})
	require.ErrorContains(t, err, "Path is not a directory: note.txt")
}

func TestToolSet_GrepFallbackAndRipgrepModes(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alpha\nhello\nbeta\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("hello again\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {\n\tprintln(\"alpha\")\n\tprintln(\"beta\")\n}\n"), 0o644))
	restore := withRipgrepForTest(func(string) (string, error) {
		return "", errors.New("not found")
	})
	defer restore()
	ts, err := NewToolSet(WithBaseDir(dir))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, ts.Close())
	})
	grepTool := mustCallableTool(t, ts.Tools(context.Background()), toolGrep)
	filesOut := callToolAs[grepOutput](t, grepTool, grepInput{
		Pattern:    "hello",
		Glob:       "*.txt",
		OutputMode: "files_with_matches",
	})
	require.ElementsMatch(t, []string{"a.txt", "b.txt"}, filesOut.Filenames)
	countOut := callToolAs[grepOutput](t, grepTool, grepInput{
		Pattern:    "hello",
		Glob:       "*.txt",
		OutputMode: "count",
	})
	require.Equal(t, 2, countOut.NumMatches)
	contentOut := callToolAs[grepOutput](t, grepTool, grepInput{
		Pattern:    "hello",
		Glob:       "*.txt",
		OutputMode: "content",
		Context:    intPtr(1),
	})
	require.Contains(t, contentOut.Content, "a.txt:1:alpha")
	require.Contains(t, contentOut.Content, "a.txt:2:hello")
	require.Contains(t, contentOut.Content, "a.txt:3:beta")
	multilineOut := callToolAs[grepOutput](t, grepTool, grepInput{
		Pattern:     "alpha.*beta",
		OutputMode:  "content",
		Type:        "go",
		Multiline:   true,
		ContextAlt:  intPtr(1),
		ShowLineNum: boolPtr(true),
	})
	require.Contains(t, multilineOut.Content, "main.go:4:\tprintln(\"alpha\")")
	require.Contains(t, multilineOut.Content, "main.go:5:\tprintln(\"beta\")")
}

func TestToolSet_GrepRipgrepAdvancedOptions(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep is not available")
	}
	restore := withRipgrepForTest(exec.LookPath)
	defer restore()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {\n\tprintln(\"alpha\")\n\tprintln(\"beta\")\n}\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("alpha\nbeta\n"), 0o644))
	ts, err := NewToolSet(WithBaseDir(dir))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, ts.Close())
	})
	grepTool := mustCallableTool(t, ts.Tools(context.Background()), toolGrep)
	out := callToolAs[grepOutput](t, grepTool, grepInput{
		Pattern:     "alpha.*beta",
		OutputMode:  "content",
		Type:        "go",
		Multiline:   true,
		ContextAlt:  intPtr(1),
		ShowLineNum: boolPtr(true),
	})
	require.Contains(t, out.Content, "main.go:4:\tprintln(\"alpha\")")
	require.Contains(t, out.Content, "main.go:5:\tprintln(\"beta\")")
}

func TestToolSet_ToolSearchFindsCurrentTools(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ts, err := NewToolSet(WithBaseDir(dir))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, ts.Close())
	})
	searchTool := mustCallableTool(t, ts.Tools(context.Background()), toolToolSearch)
	selectOut := callToolAs[toolSearchOutput](t, searchTool, toolSearchInput{
		Query: "select:Read,Grep",
	})
	require.Equal(t, []string{toolRead, toolGrep}, selectOut.Matches)
	require.Zero(t, selectOut.TotalDeferredTools)
	keywordOut := callToolAs[toolSearchOutput](t, searchTool, toolSearchInput{
		Query: "read file",
	})
	require.Contains(t, keywordOut.Matches, toolRead)
	require.Zero(t, keywordOut.TotalDeferredTools)
	prefixOut := callToolAs[toolSearchOutput](t, searchTool, toolSearchInput{
		Query: strings.ToLower(toolRead),
	})
	require.Equal(t, []string{toolRead}, prefixOut.Matches)
	requiredOut := callToolAs[toolSearchOutput](t, searchTool, toolSearchInput{
		Query: "+read file",
	})
	require.Contains(t, requiredOut.Matches, toolRead)
}

func TestToolSet_AskUserQuestionPassesThroughValidatedPayload(t *testing.T) {
	t.Parallel()
	askTool, err := newAskUserQuestionTool()
	require.NoError(t, err)
	callable, ok := askTool.(tool.CallableTool)
	require.True(t, ok)
	out := callToolAs[askUserQuestionOutput](t, callable, askUserQuestionInput{
		Questions: []askUserQuestion{{
			Question: "Which path should we take?",
			Header:   "Path",
			Options: []askUserQuestionOption{
				{Label: "Fast", Description: "Ship with the minimum change."},
				{Label: "Safe", Description: "Do the more conservative refactor."},
			},
		}},
		Answers: map[string]string{
			"Which path should we take?": "Safe",
		},
		Annotations: map[string]askUserQuestionAnnotation{
			"Which path should we take?": {
				Notes: "Prefer lower migration risk.",
			},
		},
		Metadata: &askUserQuestionMetadata{Source: "test"},
	})
	require.Len(t, out.Questions, 1)
	require.Equal(t, "Safe", out.Answers["Which path should we take?"])
	require.Equal(t, "Prefer lower migration risk.", out.Annotations["Which path should we take?"].Notes)
}

func TestToolSet_AskUserQuestionRejectsDuplicateQuestionText(t *testing.T) {
	t.Parallel()
	askTool, err := newAskUserQuestionTool()
	require.NoError(t, err)
	callable, ok := askTool.(tool.CallableTool)
	require.True(t, ok)
	_, err = callToolRaw(callable, askUserQuestionInput{
		Questions: []askUserQuestion{
			{
				Question: "Which path should we take?",
				Header:   "One",
				Options: []askUserQuestionOption{
					{Label: "A", Description: "A."},
					{Label: "B", Description: "B."},
				},
			},
			{
				Question: "Which path should we take?",
				Header:   "Two",
				Options: []askUserQuestionOption{
					{Label: "C", Description: "C."},
					{Label: "D", Description: "D."},
				},
			},
		},
	})
	require.ErrorContains(t, err, "question texts must be unique")
}

func TestToolSet_WebFetchTool(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><body><h1>Hello</h1><p>world</p></body></html>"))
	}))
	defer server.Close()
	ts, err := NewToolSet()
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, ts.Close())
	})
	fetchTool := mustCallableTool(t, ts.Tools(context.Background()), toolWebFetch)
	out := callToolAs[webFetchOutput](t, fetchTool, webFetchInput{
		URL:    server.URL,
		Prompt: "Summarize the page.",
	})
	require.Equal(t, 200, out.Code)
	require.Contains(t, out.Result, "Hello")
	require.Contains(t, out.Result, "world")
	require.NotZero(t, out.DurationMs)
}

func TestToolSet_WebFetchReturnsExtractedContentByDefault(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><body><p>Alpha release date is April 1.</p><p>Beta secret is 42.</p><p>Gamma is unrelated.</p></body></html>"))
	}))
	defer server.Close()
	ts, err := NewToolSet()
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, ts.Close())
	})
	fetchTool := mustCallableTool(t, ts.Tools(context.Background()), toolWebFetch)
	out := callToolAs[webFetchOutput](t, fetchTool, webFetchInput{
		URL:    server.URL,
		Prompt: "What is the beta secret?",
	})
	require.Contains(t, out.Result, "Alpha release date is April 1.")
	require.Contains(t, out.Result, "Beta secret is 42.")
	require.Contains(t, out.Result, "Gamma is unrelated.")
}

func TestToolSet_WebFetchDetectsCrossHostRedirect(t *testing.T) {
	t.Parallel()
	redirectTarget := "http://localhost/target"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, redirectTarget, http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	ts, err := NewToolSet()
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, ts.Close())
	})
	fetchTool := mustCallableTool(t, ts.Tools(context.Background()), toolWebFetch)
	out := callToolAs[webFetchOutput](t, fetchTool, webFetchInput{
		URL:    server.URL + "/start",
		Prompt: "Summarize the page.",
	})
	require.Contains(t, out.Result, "REDIRECT DETECTED")
}

func TestToolSet_WebFetchUsesPromptProcessorWhenConfigured(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`
			<html><body>
				<a href="/docs">Documentation</a>
				<a href="https://example.com/blog">Blog</a>
			</body></html>
		`))
	}))
	defer server.Close()
	ts, err := NewToolSet(WithWebFetchOptions(WebFetchOptions{
		AllowAll: true,
		PromptProcessor: func(_ context.Context, in WebFetchPromptInput) (string, error) {
			return "prompt=" + in.Prompt + "\ncontent=" + in.Content, nil
		},
	}))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, ts.Close())
	})
	fetchTool := mustCallableTool(t, ts.Tools(context.Background()), toolWebFetch)
	out := callToolAs[webFetchOutput](t, fetchTool, webFetchInput{
		URL:    server.URL,
		Prompt: "List the links on the page.",
	})
	require.Contains(t, out.Result, "prompt=List the links on the page.")
	require.Contains(t, out.Result, "Documentation")
	require.Contains(t, out.Result, "Blog")
}

func TestToolSet_WebSearchDuckDuckGoLikeHTML(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`
			<html><body>
				<div class="result">
					<a class="result__a" href="https://golang.org/doc/">The Go Programming Language</a>
					<a class="result__snippet">Go documentation.</a>
				</div>
				<div class="result">
					<a class="result__a" href="https://example.com/">Example</a>
					<a class="result__snippet">Example snippet.</a>
				</div>
			</body></html>
		`))
	}))
	defer server.Close()
	ts, err := NewToolSet(WithWebSearchOptions(WebSearchOptions{
		Provider: "duckduckgo",
		BaseURL:  server.URL,
	}))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, ts.Close())
	})
	searchTool := mustCallableTool(t, ts.Tools(context.Background()), toolWebSearch)
	out := callToolAs[webSearchOutput](t, searchTool, webSearchInput{
		Query:          "golang",
		AllowedDomains: []string{"golang.org"},
	})
	require.Equal(t, "golang", out.Query)
	require.Len(t, out.Results, 1)
	require.Len(t, out.Results[0].Content, 1)
	require.Equal(t, "https://golang.org/doc/", out.Results[0].Content[0].URL)
	require.NotZero(t, out.DurationSeconds)
}

func TestToolSet_WebSearchDuckDuckGoNormalizesWrappedLinksAndDeduplicates(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`
			<html><body>
				<div class="result">
					<a class="result__a" href="https://duckduckgo.com/l/?uddg=https%3A%2F%2Fgolang.org%2Fdoc%2F">The Go Programming Language</a>
					<a class="result__snippet">Go documentation.</a>
				</div>
				<div class="result">
					<a class="result__a" href="https://golang.org/doc/">Go Docs Duplicate</a>
					<a class="result__snippet">Duplicate hit.</a>
				</div>
			</body></html>
		`))
	}))
	defer server.Close()
	ts, err := NewToolSet(WithWebSearchOptions(WebSearchOptions{
		Provider: "duckduckgo",
		BaseURL:  server.URL,
	}))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, ts.Close())
	})
	searchTool := mustCallableTool(t, ts.Tools(context.Background()), toolWebSearch)
	out := callToolAs[webSearchOutput](t, searchTool, webSearchInput{
		Query:          "golang",
		AllowedDomains: []string{"golang.org"},
	})
	require.Len(t, out.Results, 1)
	require.Len(t, out.Results[0].Content, 1)
	require.Equal(t, "https://golang.org/doc/", out.Results[0].Content[0].URL)
}

func TestToolSet_WebSearchDuckDuckGoAppliesConfiguredWindowAndReturnsNoResultBlocksWhenEmpty(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`
			<html><body>
				<div class="result">
					<a class="result__a" href="https://one.example.com/">One</a>
					<a class="result__snippet">First.</a>
				</div>
				<div class="result">
					<a class="result__a" href="https://two.example.com/">Two</a>
					<a class="result__snippet">Second.</a>
				</div>
				<div class="result">
					<a class="result__a" href="https://three.example.com/">Three</a>
					<a class="result__snippet">Third.</a>
				</div>
			</body></html>
		`))
	}))
	defer server.Close()
	ts, err := NewToolSet(WithWebSearchOptions(WebSearchOptions{
		Provider: "duckduckgo",
		BaseURL:  server.URL,
		Size:     1,
		Offset:   1,
	}))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, ts.Close())
	})
	searchTool := mustCallableTool(t, ts.Tools(context.Background()), toolWebSearch)
	out := callToolAs[webSearchOutput](t, searchTool, webSearchInput{
		Query: "example",
	})
	require.Len(t, out.Results, 1)
	require.Len(t, out.Results[0].Content, 1)
	require.Equal(t, "https://two.example.com/", out.Results[0].Content[0].URL)
	emptyOut := callToolAs[webSearchOutput](t, searchTool, webSearchInput{
		Query:          "example",
		AllowedDomains: []string{"missing.example.com"},
	})
	require.Empty(t, emptyOut.Results)
}

func newTestPDF(t *testing.T, pages []string) []byte {
	t.Helper()
	pdfDoc := fpdf.New("P", "mm", "A4", "")
	pdfDoc.SetFont("Helvetica", "", 12)
	for _, pageText := range pages {
		pdfDoc.AddPage()
		pdfDoc.Cell(40, 10, pageText)
	}
	var buf bytes.Buffer
	require.NoError(t, pdfDoc.Output(&buf))
	return buf.Bytes()
}

var tinyPNGBytes = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41,
	0x54, 0x78, 0x9c, 0x63, 0xf8, 0xcf, 0xc0, 0x00,
	0x00, 0x03, 0x01, 0x01, 0x00, 0xc9, 0xfe, 0x92,
	0xef, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e,
	0x44, 0xae, 0x42, 0x60, 0x82,
}

var ripgrepTestMu sync.Mutex

func mustCallableTool(t *testing.T, tools []tool.Tool, name string) tool.CallableTool {
	t.Helper()
	for _, candidate := range tools {
		if candidate.Declaration() == nil || candidate.Declaration().Name != name {
			continue
		}
		callable, ok := candidate.(tool.CallableTool)
		require.True(t, ok)
		return callable
	}
	t.Fatalf("tool %s not found", name)
	return nil
}

func callToolRaw(target tool.CallableTool, input any) (any, error) {
	args, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	return target.Call(context.Background(), args)
}

func callToolAs[T any](t *testing.T, target tool.CallableTool, input any) T {
	t.Helper()
	out, err := callToolRaw(target, input)
	require.NoError(t, err)
	data, err := json.Marshal(out)
	require.NoError(t, err)
	var decoded T
	require.NoError(t, json.Unmarshal(data, &decoded))
	return decoded
}

func toolNames(tools []tool.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, candidate := range tools {
		if candidate == nil || candidate.Declaration() == nil {
			continue
		}
		names = append(names, candidate.Declaration().Name)
	}
	return names
}

func intPtr(value int) *int {
	return &value
}

func boolPtr(value bool) *bool {
	return &value
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func strconvString(value int) string {
	return strconv.Itoa(value)
}

func withRipgrepForTest(lookPath func(string) (string, error)) func() {
	ripgrepTestMu.Lock()
	oldLookPath := ripgrepLookPath
	oldPath := ripgrepPath
	ripgrepLookPath = lookPath
	ripgrepPath = ""
	ripgrepOnce = sync.Once{}
	return func() {
		ripgrepLookPath = oldLookPath
		ripgrepPath = oldPath
		ripgrepOnce = sync.Once{}
		ripgrepTestMu.Unlock()
	}
}
