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
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	require.Contains(t, names, toolWebFetch)
	require.Contains(t, names, toolWebSearch)
	require.NotContains(t, names, "EnterWorktree")
	require.NotContains(t, names, "ExitWorktree")
	require.NotContains(t, names, "WebBrowser")
	require.NotContains(t, names, "browser")
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

func TestNewToolSet_BlankNameFallsBackAndInvalidWebSearchFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ts, err := NewToolSet(WithBaseDir(dir), WithName(" \t "))
	require.NoError(t, err)
	require.Equal(t, defaultToolSetName, ts.Name())
	require.NoError(t, ts.Close())
	_, err = NewToolSet(WithBaseDir(dir), WithWebSearchOptions(WebSearchOptions{
		Provider: "bing",
	}))
	require.EqualError(t, err, "unsupported web search provider: bing")
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

func TestToolSet_BashToolTimeoutsLongCommand(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ts, err := NewToolSet(WithBaseDir(dir))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, ts.Close())
	})
	bashTool := mustCallableTool(t, ts.Tools(context.Background()), toolBash)
	out := callToolAs[bashOutput](t, bashTool, bashInput{
		Command: "sleep 1",
		Timeout: intPtr(1),
	})
	require.True(t, out.TimedOut)
	require.NotEqual(t, 0, out.ExitCode)
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
		Timeout: intPtr(5_000),
	})
	require.Equal(t, "success", blocking.RetrievalStatus)
	require.NotNil(t, blocking.Task)
	require.Equal(t, toolBash, blocking.Task.TaskType)
	require.Contains(t, blocking.Task.Output, "ab")
	require.Equal(t, "completed", blocking.Task.Status)
}

func TestToolSet_TaskOutputCoversPollingBranches(t *testing.T) {
	t.Parallel()
	runtime := newToolRuntime(t.TempDir(), maxEditableFileSize)
	taskOutputTool, err := newTaskOutputTool(runtime)
	require.NoError(t, err)
	callable, ok := taskOutputTool.(tool.CallableTool)
	require.True(t, ok)
	_, err = callToolRaw(callable, taskOutputInput{})
	require.EqualError(t, err, "task_id is required")
	runningLog := filepath.Join(t.TempDir(), "running.log")
	require.NoError(t, os.WriteFile(runningLog, []byte("partial"), 0o644))
	runtime.taskState.tasks["running"] = &backgroundTask{
		ID:         "running",
		Command:    "sleep 10",
		Type:       toolBash,
		OutputPath: runningLog,
		Status:     "running",
	}
	nonBlocking := callToolAs[taskOutputOutput](t, callable, taskOutputInput{
		TaskID: "running",
		Block:  boolPtr(false),
	})
	require.Equal(t, "not_ready", nonBlocking.RetrievalStatus)
	require.NotNil(t, nonBlocking.Task)
	require.Equal(t, "partial", nonBlocking.Task.Output)
	blockingTimeout := callToolAs[taskOutputOutput](t, callable, taskOutputInput{
		TaskID:  "running",
		Timeout: intPtr(0),
	})
	require.Equal(t, "timeout", blockingTimeout.RetrievalStatus)
	finishedLog := filepath.Join(t.TempDir(), "done.log")
	require.NoError(t, os.WriteFile(finishedLog, []byte("done"), 0o644))
	exitCode := 0
	runtime.taskState.tasks["done"] = &backgroundTask{
		ID:         "done",
		Command:    "echo done",
		Type:       toolBash,
		OutputPath: finishedLog,
		Status:     "completed",
		ExitCode:   &exitCode,
	}
	completed := callToolAs[taskOutputOutput](t, callable, taskOutputInput{
		TaskID: "done",
		Block:  boolPtr(false),
	})
	require.Equal(t, "success", completed.RetrievalStatus)
	require.NotNil(t, completed.Task)
	require.Equal(t, "done", completed.Task.Output)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = callToolRawWithContext(callable, ctx, taskOutputInput{
		TaskID:  "running",
		Timeout: intPtr(1000),
	})
	require.ErrorIs(t, err, context.Canceled)
	_, err = snapshotBackgroundTask(runtime, "missing")
	require.EqualError(t, err, "no task found with ID: missing")
}

func TestToolSet_TaskOutputClampsNegativeTimeoutToZero(t *testing.T) {
	t.Parallel()
	runtime := newToolRuntime(t.TempDir(), maxEditableFileSize)
	taskOutputTool, err := newTaskOutputTool(runtime)
	require.NoError(t, err)
	callable, ok := taskOutputTool.(tool.CallableTool)
	require.True(t, ok)
	runtime.taskState.tasks["running"] = &backgroundTask{
		ID:         "running",
		Command:    "sleep 10",
		Type:       toolBash,
		OutputPath: filepath.Join(t.TempDir(), "running.log"),
		Status:     "running",
	}
	out := callToolAs[taskOutputOutput](t, callable, taskOutputInput{
		TaskID:  "running",
		Timeout: intPtr(-1),
	})
	require.Equal(t, "timeout", out.RetrievalStatus)
	require.NotNil(t, out.Task)
	require.Equal(t, "running", out.Task.Status)
}

func TestReadTaskSnapshotHandlesMissingOutputAndCopiesExitCode(t *testing.T) {
	t.Parallel()
	runtime := newToolRuntime(t.TempDir(), maxEditableFileSize)
	exitCode := 7
	runtime.taskState.tasks["done"] = &backgroundTask{
		ID:         "done",
		Command:    "echo done",
		Type:       toolBash,
		OutputPath: filepath.Join(t.TempDir(), "missing.log"),
		Status:     "completed",
		ExitCode:   &exitCode,
	}
	snapshot, err := snapshotBackgroundTask(runtime, "done")
	require.NoError(t, err)
	require.NotNil(t, snapshot.ExitCode)
	require.Equal(t, 7, *snapshot.ExitCode)
	exitCode = 9
	require.Equal(t, 7, *snapshot.ExitCode)
	out, err := readTaskSnapshot(runtime, "done")
	require.NoError(t, err)
	require.Equal(t, "", out.Output)
	require.NotNil(t, out.ExitCode)
	require.Equal(t, 9, *out.ExitCode)
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

func TestToolSet_WriteRejectsPathsOutsideBaseDirAndDirectories(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ts, err := NewToolSet(WithBaseDir(dir))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, ts.Close())
	})
	writeTool := mustCallableTool(t, ts.Tools(context.Background()), toolWrite)
	_, err = callToolRaw(writeTool, writeInput{
		FilePath: "../outside.txt",
		Content:  "blocked",
	})
	require.ErrorContains(t, err, "path is outside base_dir")
	require.NoError(t, os.Mkdir(filepath.Join(dir, "nested"), 0o755))
	_, err = callToolRaw(writeTool, writeInput{
		FilePath: filepath.Join(dir, "nested"),
		Content:  "blocked",
	})
	require.ErrorContains(t, err, "is a directory")
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
	pdftoppmTestMu.Lock()
	t.Cleanup(func() {
		pdftoppmTestMu.Unlock()
	})
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

func TestToolSet_ReadCoversOffsetAndErrorBranches(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ts, err := NewToolSet(WithBaseDir(dir))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, ts.Close())
	})
	readTool := mustCallableTool(t, ts.Tools(context.Background()), toolRead)
	_, err = callToolRaw(readTool, readInput{FilePath: filepath.Join(dir, "missing.txt")})
	require.EqualError(t, err, fmt.Sprintf("File does not exist: %s", filepath.Join(dir, "missing.txt")))
	binaryPath := filepath.Join(dir, "data.bin")
	require.NoError(t, os.WriteFile(binaryPath, []byte{0x00, 0x01, 0x02}, 0o644))
	_, err = callToolRaw(readTool, readInput{FilePath: binaryPath})
	require.EqualError(t, err, "This tool cannot read binary files.")
	textPath := filepath.Join(dir, "notes.txt")
	require.NoError(t, os.WriteFile(textPath, []byte("alpha\nbeta\ngamma\n"), 0o644))
	out := callToolAs[readOutput](t, readTool, readInput{
		FilePath: textPath,
		Offset:   intPtr(2),
		Limit:    intPtr(1),
	})
	require.Equal(t, "text", out.Type)
	require.NotNil(t, out.File)
	require.Equal(t, 2, out.File.StartLine)
	require.Equal(t, 3, out.File.TotalLines)
	require.Equal(t, 1, out.File.NumLines)
	require.Equal(t, "beta", out.File.Content)
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

func TestToolSet_WebFetchRejectsMissingPromptAndBlockedDomain(t *testing.T) {
	t.Parallel()
	ts, err := NewToolSet(WithWebFetchOptions(WebFetchOptions{
		BlockedDomains: []string{"example.com"},
	}))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, ts.Close())
	})
	fetchTool := mustCallableTool(t, ts.Tools(context.Background()), toolWebFetch)
	_, err = callToolRaw(fetchTool, webFetchInput{
		URL: "https://allowed.example.com/page",
	})
	require.EqualError(t, err, "prompt is required")
	_, err = callToolRaw(fetchTool, webFetchInput{
		URL:    "https://example.com/page",
		Prompt: "Summarize the page.",
	})
	require.EqualError(t, err, "url is blocked by domain policy: https://example.com/page")
}

func TestToolSet_WebFetchPropagatesFetchAndPromptProcessorErrors(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("alpha"))
	}))
	defer server.Close()
	ts, err := NewToolSet(WithWebFetchOptions(WebFetchOptions{
		Timeout: time.Second,
		PromptProcessor: func(context.Context, WebFetchPromptInput) (string, error) {
			return "", fs.ErrInvalid
		},
	}))
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, ts.Close())
	})
	fetchTool := mustCallableTool(t, ts.Tools(context.Background()), toolWebFetch)
	_, err = callToolRaw(fetchTool, webFetchInput{
		URL:    "://bad",
		Prompt: "Summarize the page.",
	})
	require.Error(t, err)
	_, err = callToolRaw(fetchTool, webFetchInput{
		URL:    server.URL,
		Prompt: "Summarize the page.",
	})
	require.ErrorIs(t, err, fs.ErrInvalid)
}

func TestFetchURLHandlesRedirectAndBodyErrorBranches(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			http.Redirect(w, r, "/final", http.StatusFound)
		case "/final":
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("redirected content"))
		case "/missing-location":
			w.WriteHeader(http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := &http.Client{
		Timeout: defaultHTTPTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	finalURL, statusCode, statusText, body, contentType, err := fetchURL(context.Background(), client, server.URL+"/start", WebFetchOptions{})
	require.NoError(t, err)
	require.Equal(t, server.URL+"/final", finalURL)
	require.Equal(t, http.StatusOK, statusCode)
	require.Equal(t, "200 OK", statusText)
	require.Equal(t, []byte("redirected content"), body)
	require.Equal(t, "text/plain; charset=utf-8", contentType)
	finalURL, statusCode, statusText, body, contentType, err = fetchURL(context.Background(), client, server.URL+"/missing-location", WebFetchOptions{})
	require.NoError(t, err)
	require.Equal(t, server.URL+"/missing-location", finalURL)
	require.Equal(t, http.StatusFound, statusCode)
	require.Equal(t, "302 Found", statusText)
	require.Nil(t, body)
	require.Empty(t, contentType)
	errorClient := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(errReader{err: fs.ErrInvalid}),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}
	_, _, _, _, _, err = fetchURL(context.Background(), errorClient, server.URL+"/body-error", WebFetchOptions{})
	require.ErrorIs(t, err, fs.ErrInvalid)
}

func TestFetchURLPropagatesRequestAndRedirectParseErrors(t *testing.T) {
	t.Parallel()
	requestFailedClient := &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, fs.ErrInvalid
		}),
	}
	_, _, _, _, _, err := fetchURL(context.Background(), requestFailedClient, "https://example.com/start", WebFetchOptions{})
	require.ErrorIs(t, err, fs.ErrInvalid)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "://bad")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	client := &http.Client{
		Timeout: defaultHTTPTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	_, _, _, _, _, err = fetchURL(context.Background(), client, server.URL, WebFetchOptions{})
	require.Error(t, err)
}

func TestFetchURLReturnsTooManyRedirects(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		step, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/"))
		require.NoError(t, err)
		http.Redirect(w, r, fmt.Sprintf("/%d", step+1), http.StatusFound)
	}))
	defer server.Close()
	client := &http.Client{
		Timeout: defaultHTTPTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	_, _, _, _, _, err := fetchURL(context.Background(), client, server.URL+"/0", WebFetchOptions{})
	require.EqualError(t, err, "too many redirects")
}

func TestProcessFetchedContentAndTrimFetchResultCoverRemainingBranches(t *testing.T) {
	t.Parallel()
	processed, err := processFetchedContent(context.Background(), WebFetchOptions{}, webFetchInput{
		URL:    "https://example.com/page",
		Prompt: "Summarize the page.",
	}, "  alpha beta gamma  ", "text/plain")
	require.NoError(t, err)
	require.Equal(t, "alpha beta gamma", processed)
	_, err = processFetchedContent(context.Background(), WebFetchOptions{
		PromptProcessor: func(context.Context, WebFetchPromptInput) (string, error) {
			return "", fs.ErrInvalid
		},
	}, webFetchInput{
		URL:    "https://example.com/page",
		Prompt: "Summarize the page.",
	}, "content", "text/plain")
	require.ErrorIs(t, err, fs.ErrInvalid)
	require.Equal(t, "abc\n\n[Content truncated due to length.]", trimFetchResult("  abcdef  ", 3))
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

func TestParseDuckDuckGoHTMLLeavesSnippetEmptyWithoutDedicatedSnippetNode(t *testing.T) {
	t.Parallel()
	hits := parseDuckDuckGoHTML([]byte(`
		<html><body>
			<div class="result">
				<div class="result__body">
					<a class="result__a" href="https://example.com/doc">Example Title</a>
					<span class="result__url">example.com/doc</span>
				</div>
			</div>
		</body></html>
	`), webSearchInput{Query: "example"}, 0, 0)
	require.Len(t, hits, 1)
	require.Equal(t, "Example Title", hits[0].Title)
	require.Equal(t, "https://example.com/doc", hits[0].URL)
	require.Empty(t, hits[0].Snippet)
}

func TestGoogleSearchBackendSearchUsesConfiguredOptions(t *testing.T) {
	t.Parallel()
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		require.Equal(t, "api-key", r.URL.Query().Get("key"))
		require.Equal(t, "engine-id", r.URL.Query().Get("cx"))
		require.Equal(t, "golang", r.URL.Query().Get("q"))
		if requestCount == 1 {
			require.Equal(t, "2", r.URL.Query().Get("num"))
			require.Equal(t, "2", r.URL.Query().Get("start"))
			require.Equal(t, "lang_en", r.URL.Query().Get("lr"))
		} else {
			require.Empty(t, r.URL.Query().Get("num"))
			require.Empty(t, r.URL.Query().Get("start"))
			require.Empty(t, r.URL.Query().Get("lr"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"items": [
				{"link": "https://golang.org/doc/", "title": "Go", "snippet": "Official docs."},
				{"link": "https://golang.org/doc/", "title": "Go duplicate", "snippet": "Duplicate."},
				{"link": "https://example.com/", "title": "Example", "snippet": "Filtered out."}
			]
		}`))
	}))
	defer server.Close()
	backend := &googleSearchBackend{
		client: server.Client(),
		options: &WebSearchOptions{
			BaseURL:  server.URL,
			APIKey:   "api-key",
			EngineID: "engine-id",
			Size:     2,
			Offset:   1,
			Lang:     "en",
		},
	}
	hits, err := backend.search(context.Background(), webSearchInput{
		Query:          "golang",
		AllowedDomains: []string{"golang.org"},
	})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.Equal(t, "https://golang.org/doc/", hits[0].URL)
	unwindowedBackend := &googleSearchBackend{
		client: server.Client(),
		options: &WebSearchOptions{
			BaseURL:  server.URL,
			APIKey:   "api-key",
			EngineID: "engine-id",
		},
	}
	unwindowedHits, err := unwindowedBackend.search(context.Background(), webSearchInput{
		Query: "golang",
	})
	require.NoError(t, err)
	require.Len(t, unwindowedHits, 2)
	require.Equal(t, "https://golang.org/doc/", unwindowedHits[0].URL)
}

func TestGoogleSearchBackendSearchUsesEnvironmentFallback(t *testing.T) {
	t.Setenv(envGoogleAPIKey, "env-api-key")
	t.Setenv(envGoogleEngineID, "env-engine-id")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "env-api-key", r.URL.Query().Get("key"))
		require.Equal(t, "env-engine-id", r.URL.Query().Get("cx"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"link":"https://example.com/","title":"Example","snippet":"Snippet"}]}`))
	}))
	defer server.Close()
	backend := &googleSearchBackend{
		client:  server.Client(),
		options: &WebSearchOptions{BaseURL: server.URL},
	}
	hits, err := backend.search(context.Background(), webSearchInput{Query: "example"})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.Equal(t, "https://example.com/", hits[0].URL)
}

func TestGoogleSearchBackendSearchRejectsMissingConfig(t *testing.T) {
	t.Parallel()
	backend := &googleSearchBackend{client: http.DefaultClient}
	_, err := backend.search(context.Background(), webSearchInput{Query: "example"})
	require.EqualError(t, err, "google search config is required")
	backend = &googleSearchBackend{client: http.DefaultClient, options: &WebSearchOptions{}}
	_, err = backend.search(context.Background(), webSearchInput{Query: "example"})
	require.EqualError(t, err, "google search requires api_key and engine_id")
}

func TestWebSearchToolCoversValidationAndProviderBranches(t *testing.T) {
	t.Parallel()
	backend, err := newSearchBackend(&WebSearchOptions{
		Provider: "google",
		Timeout:  2 * time.Second,
	})
	require.NoError(t, err)
	googleBackend, ok := backend.(*googleSearchBackend)
	require.True(t, ok)
	require.Equal(t, 2*time.Second, googleBackend.client.Timeout)
	_, err = newSearchBackend(&WebSearchOptions{Provider: "bing"})
	require.EqualError(t, err, "unsupported web search provider: bing")
	searchTool, err := newWebSearchTool(&WebSearchOptions{
		BaseURL: "http://127.0.0.1",
	})
	require.NoError(t, err)
	callable, ok := searchTool.(tool.CallableTool)
	require.True(t, ok)
	_, err = callToolRaw(callable, webSearchInput{})
	require.EqualError(t, err, "query is required")
	_, err = callToolRaw(callable, webSearchInput{
		Query:          "example",
		AllowedDomains: []string{"example.com"},
		BlockedDomains: []string{"example.org"},
	})
	require.EqualError(t, err, "cannot specify both allowed_domains and blocked_domains")
}

func TestEncodingHelpersPreserveUTF16AndLineEndings(t *testing.T) {
	t.Parallel()
	encoded, err := encodeTextBytes("alpha\nbeta\n", "utf16le", "\r\n")
	require.NoError(t, err)
	decoded, encoding, err := decodeTextBytes(encoded)
	require.NoError(t, err)
	require.Equal(t, "utf16le", encoding)
	require.Equal(t, "alpha\nbeta\n", decoded)
	utf8Decoded, utf8Encoding, err := decodeTextBytes([]byte("one\r\ntwo\r\n"))
	require.NoError(t, err)
	require.Equal(t, "utf8", utf8Encoding)
	require.Equal(t, "one\ntwo\n", utf8Decoded)
}

func TestQuoteHelpersNormalizeAndPreserveSingleQuotes(t *testing.T) {
	t.Parallel()
	require.Equal(t, "\"quote\" and 'apostrophe'", normalizeQuotes("“quote” and ‘apostrophe’"))
	require.Equal(t, "‘quoted text’ and don’t", applyCurlySingleQuotes("'quoted text' and don't"))
	actual := findActualString("‘quoted text’ and don’t", "'quoted text' and don't")
	require.Equal(t, "‘quoted text’ and don’t", actual)
	require.Equal(t, "‘new text’ and can’t", preserveQuoteStyle("'quoted text' and don't", actual, "'new text' and can't"))
}

func TestWriteOutputToEditOutputPreservesOriginalFileAndPatch(t *testing.T) {
	t.Parallel()
	out := writeOutputToEditOutput("/tmp/file.txt", editInput{
		FilePath:   "/tmp/file.txt",
		OldString:  "before",
		NewString:  "after",
		ReplaceAll: true,
	}, strPtr("before"), "after")
	require.Equal(t, "/tmp/file.txt", out.FilePath)
	require.Equal(t, "before", out.OriginalFile)
	require.True(t, out.ReplaceAll)
	require.NotEmpty(t, out.StructuredPatch)
}

func TestOptionHelpersApplyCustomValues(t *testing.T) {
	t.Parallel()
	options := &toolSetOptions{}
	WithName("custom")(options)
	WithMaxFileSize(2048)(options)
	require.Equal(t, "custom", options.name)
	require.EqualValues(t, 2048, options.maxFileSize)
	require.True(t, options.hasMaxSize)
}

func TestEditLocalFileCreatesMissingFileWithoutReadState(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	runtime := newToolRuntime(dir, maxEditableFileSize)
	absPath := filepath.Join(dir, "created.txt")
	out, err := editLocalFile(absPath, editInput{
		FilePath:  absPath,
		OldString: "",
		NewString: "created content\n",
	}, runtime)
	require.NoError(t, err)
	require.Equal(t, "", out.OriginalFile)
	require.NotEmpty(t, out.StructuredPatch)
	raw, readErr := os.ReadFile(absPath)
	require.NoError(t, readErr)
	require.Equal(t, "created content\n", string(raw))
}

func TestEditLocalFileRejectsBinaryAndNoopChanges(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	runtime := newToolRuntime(dir, maxEditableFileSize)
	binaryPath := filepath.Join(dir, "data.bin")
	require.NoError(t, os.WriteFile(binaryPath, []byte{0x00, 0x01, 0x02}, 0o644))
	_, err := editLocalFile(binaryPath, editInput{
		FilePath:  binaryPath,
		OldString: "a",
		NewString: "b",
	}, runtime)
	require.EqualError(t, err, "This tool cannot edit binary files.")
	textPath := filepath.Join(dir, "notes.txt")
	require.NoError(t, os.WriteFile(textPath, []byte("same"), 0o644))
	_, err = editLocalFile(textPath, editInput{
		FilePath:  textPath,
		OldString: "same",
		NewString: "same",
	}, runtime)
	require.EqualError(t, err, "No changes to make: old_string and new_string are exactly the same.")
}

func TestEditLocalFileCoversInsertAndReplaceErrorBranches(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	runtime := newToolRuntime(dir, maxEditableFileSize)
	emptyPath := filepath.Join(dir, "empty.txt")
	require.NoError(t, os.WriteFile(emptyPath, nil, 0o644))
	snapshot, err := readLocalFileSnapshot(emptyPath, maxEditableFileSize)
	require.NoError(t, err)
	storeReadView(runtime.fileState, emptyPath, snapshot.Content, snapshot.Timestamp, nil, nil, "", false, true)
	out, err := editLocalFile(emptyPath, editInput{
		FilePath:  emptyPath,
		OldString: "",
		NewString: "inserted\n",
	}, runtime)
	require.NoError(t, err)
	require.Equal(t, "", out.OriginalFile)
	require.NotEmpty(t, out.StructuredPatch)
	missingPath := filepath.Join(dir, "missing.txt")
	require.NoError(t, os.WriteFile(missingPath, []byte("alpha\nbeta\n"), 0o644))
	snapshot, err = readLocalFileSnapshot(missingPath, maxEditableFileSize)
	require.NoError(t, err)
	storeReadView(runtime.fileState, missingPath, snapshot.Content, snapshot.Timestamp, nil, nil, "", false, true)
	_, err = editLocalFile(missingPath, editInput{
		FilePath:  missingPath,
		OldString: "gamma",
		NewString: "delta",
	}, runtime)
	require.EqualError(t, err, "String to replace not found in file.\nString: gamma")
	duplicatePath := filepath.Join(dir, "duplicate.txt")
	require.NoError(t, os.WriteFile(duplicatePath, []byte("alpha\nalpha\n"), 0o644))
	snapshot, err = readLocalFileSnapshot(duplicatePath, maxEditableFileSize)
	require.NoError(t, err)
	storeReadView(runtime.fileState, duplicatePath, snapshot.Content, snapshot.Timestamp, nil, nil, "", false, true)
	_, err = editLocalFile(duplicatePath, editInput{
		FilePath:  duplicatePath,
		OldString: "alpha",
		NewString: "omega",
	}, runtime)
	require.EqualError(t, err, "Found 2 matches of the string to replace, but replace_all is false. To replace all occurrences, set replace_all to true. To replace only one occurrence, please provide more context to uniquely identify the instance.\nString: alpha")
	replaceAllOut, err := editLocalFile(duplicatePath, editInput{
		FilePath:   duplicatePath,
		OldString:  "alpha",
		NewString:  "omega",
		ReplaceAll: true,
	}, runtime)
	require.NoError(t, err)
	require.True(t, replaceAllOut.ReplaceAll)
	raw, readErr := os.ReadFile(duplicatePath)
	require.NoError(t, readErr)
	require.Equal(t, "omega\nomega\n", string(raw))
}

func TestLoadNotebookEditStateRejectsInvalidInputs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	runtime := newToolRuntime(dir, maxEditableFileSize)
	_, err := loadNotebookEditState(filepath.Join(dir, "plain.txt"), notebookEditInput{}, runtime)
	require.EqualError(t, err, "File must be a Jupyter notebook (.ipynb file).")
	notebookPath := filepath.Join(dir, "test.ipynb")
	require.NoError(t, os.WriteFile(notebookPath, []byte(`{"cells":[],"metadata":{},"nbformat":4,"nbformat_minor":5}`), 0o644))
	snapshot, err := readLocalFileSnapshot(notebookPath, maxEditableFileSize)
	require.NoError(t, err)
	storeReadView(runtime.fileState, notebookPath, snapshot.Content, snapshot.Timestamp, nil, nil, "", false, true)
	_, err = loadNotebookEditState(notebookPath, notebookEditInput{EditMode: "bad"}, runtime)
	require.EqualError(t, err, "Edit mode must be replace, insert, or delete.")
	_, err = loadNotebookEditState(notebookPath, notebookEditInput{EditMode: "insert"}, runtime)
	require.EqualError(t, err, "Cell type is required when using edit_mode=insert.")
	_, err = loadNotebookEditState(notebookPath, notebookEditInput{EditMode: "replace"}, runtime)
	require.EqualError(t, err, "Cell ID must be specified when not inserting a new cell.")
}

func TestLoadNotebookEditStateConvertsReplacePastEndIntoInsert(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	runtime := newToolRuntime(dir, maxEditableFileSize)
	notebookPath := filepath.Join(dir, "test.ipynb")
	require.NoError(t, os.WriteFile(notebookPath, []byte(`{
		"cells":[{"id":"cell-0","cell_type":"markdown","metadata":{},"source":"hello"}],
		"metadata":{"language_info":{"name":"python"}},
		"nbformat":4,
		"nbformat_minor":5
	}`), 0o644))
	snapshot, err := readLocalFileSnapshot(notebookPath, maxEditableFileSize)
	require.NoError(t, err)
	storeReadView(runtime.fileState, notebookPath, snapshot.Content, snapshot.Timestamp, nil, nil, "", false, true)
	state, err := loadNotebookEditState(notebookPath, notebookEditInput{
		EditMode: "replace",
		CellID:   "cell-1",
	}, runtime)
	require.NoError(t, err)
	require.Equal(t, "insert", state.editMode)
	require.Equal(t, "code", state.cellType)
	require.Equal(t, "python", state.language)
	require.Equal(t, 1, state.cellIndex)
}

func TestFileStateHelpersCoverRemainingBranches(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, err := readLocalFileSnapshot(dir, maxEditableFileSize)
	require.ErrorContains(t, err, "is a directory")
	missingSnapshot, err := readLocalFileSnapshot(filepath.Join(dir, "missing.txt"), maxEditableFileSize)
	require.NoError(t, err)
	require.False(t, missingSnapshot.Exists)
	largePath := filepath.Join(dir, "large.txt")
	require.NoError(t, os.WriteFile(largePath, []byte("0123456789"), 0o644))
	_, err = readLocalFileSnapshot(largePath, 4)
	require.ErrorContains(t, err, "exceeds max size")
	writtenPath := filepath.Join(dir, "nested", "written.txt")
	require.NoError(t, writeLocalFile(writtenPath, "alpha\nbeta\n", 0, "utf8", "\n"))
	writtenSnapshot, err := readLocalFileSnapshot(writtenPath, maxEditableFileSize)
	require.NoError(t, err)
	require.True(t, writtenSnapshot.Exists)
	require.Equal(t, "alpha\nbeta\n", writtenSnapshot.Content)
	state := &fileState{views: map[string]fileView{}}
	err = ensureWriteAllowed(writtenPath, writtenSnapshot, state)
	require.EqualError(t, err, "File has not been read yet. Read it first before writing to it.")
	state.views[writtenPath] = fileView{IsPartialView: true}
	err = ensureWriteAllowed(writtenPath, writtenSnapshot, state)
	require.EqualError(t, err, "File has not been read yet. Read it first before writing to it.")
	state.views[writtenPath] = fileView{
		Content:   writtenSnapshot.Content,
		Timestamp: writtenSnapshot.Timestamp - 1,
		FromRead:  true,
	}
	require.NoError(t, ensureWriteAllowed(writtenPath, writtenSnapshot, state))
	state.views[writtenPath] = fileView{
		Content:   "different",
		Timestamp: writtenSnapshot.Timestamp - 1,
		FromRead:  true,
	}
	err = ensureWriteAllowed(writtenPath, writtenSnapshot, state)
	require.EqualError(t, err, "File has been modified since read, either by the user or by a linter. Read it again before attempting to write it.")
	require.True(t, matchesReadView(fileView{
		FromRead: true,
		Offset:   intPtr(1),
		Limit:    intPtr(2),
		Pages:    "1-2",
	}, intPtr(1), intPtr(2), "1-2"))
	require.False(t, matchesReadView(fileView{FromRead: false}, nil, nil, ""))
	require.True(t, intPtrsEqual(nil, nil))
	require.False(t, intPtrsEqual(intPtr(1), nil))
	require.False(t, intPtrsEqual(nil, intPtr(1)))
	require.True(t, intPtrsEqual(intPtr(2), intPtr(2)))
	actualDouble := findActualString("“quoted text”", "\"quoted text\"")
	require.Equal(t, "“quoted text”", actualDouble)
	require.Equal(t, "“new text”", preserveQuoteStyle("\"quoted text\"", actualDouble, "\"new text\""))
	require.Equal(t, "fallback", notebookCellType(map[string]any{}, "fallback"))
}

func TestNotebookHelpersCoverRemainingBranches(t *testing.T) {
	t.Parallel()
	_, _, err := parseNotebook([]byte(`{"cells":{}}`))
	require.ErrorContains(t, err, "notebook cells are invalid")
	_, _, err = parseNotebook([]byte(`{"cells":[1]}`))
	require.ErrorContains(t, err, "notebook cell is invalid")
	cellType, err := normalizeNotebookCellType("")
	require.NoError(t, err)
	require.Empty(t, cellType)
	cellType, err = normalizeNotebookCellType(" markdown ")
	require.NoError(t, err)
	require.Equal(t, "markdown", cellType)
	_, err = normalizeNotebookCellType("raw")
	require.EqualError(t, err, "Cell type must be code or markdown.")
	require.Equal(t, "python", notebookLanguage(map[string]any{}))
	require.Equal(t, "python", notebookLanguage(map[string]any{"metadata": map[string]any{}}))
	require.Equal(t, "python", notebookLanguage(map[string]any{
		"metadata": map[string]any{"language_info": map[string]any{"name": " "}},
	}))
	require.Equal(t, "go", notebookLanguage(map[string]any{
		"metadata": map[string]any{"language_info": map[string]any{"name": "go"}},
	}))
	require.False(t, notebookSupportsCellIDs(map[string]any{"nbformat": 4, "nbformat_minor": 4}))
	require.True(t, notebookSupportsCellIDs(map[string]any{"nbformat": 5, "nbformat_minor": 0}))
	value, ok := notebookInt(float64(4))
	require.True(t, ok)
	require.Equal(t, 4, value)
	value, ok = notebookInt(3)
	require.True(t, ok)
	require.Equal(t, 3, value)
	_, ok = notebookInt("bad")
	require.False(t, ok)
	deleteState := notebookEditState{cells: []map[string]any{}, cellIndex: 0}
	_, _, err = deleteNotebookCell(&deleteState, notebookEditInput{CellID: "missing"})
	require.EqualError(t, err, `Cell with ID "missing" not found in notebook.`)
	replaceState := notebookEditState{
		cells: []map[string]any{{
			"id":              "cell-1",
			"cell_type":       "markdown",
			"execution_count": 1,
			"outputs":         []any{"old"},
		}},
		cellIndex: 0,
		cellType:  "markdown",
	}
	resultCellID, resultCellType, err := replaceNotebookCell(&replaceState, notebookEditInput{
		CellID:    "cell-1",
		NewSource: "updated",
	})
	require.NoError(t, err)
	require.Equal(t, "cell-1", resultCellID)
	require.Equal(t, "markdown", resultCellType)
	require.NotContains(t, replaceState.cells[0], "execution_count")
	require.NotContains(t, replaceState.cells[0], "outputs")
	replaceState = notebookEditState{
		cells:     []map[string]any{{"id": "cell-1"}},
		cellIndex: 1,
	}
	_, _, err = replaceNotebookCell(&replaceState, notebookEditInput{CellID: "missing"})
	require.EqualError(t, err, `Cell with ID "missing" not found in notebook.`)
	_, err = marshalNotebook(map[string]any{"bad": func() {}})
	require.Error(t, err)
	dir := t.TempDir()
	runtime := newToolRuntime(dir, maxEditableFileSize)
	missingPath := filepath.Join(dir, "missing.ipynb")
	_, err = loadNotebookEditState(missingPath, notebookEditInput{
		EditMode: "insert",
		CellType: "code",
	}, runtime)
	require.EqualError(t, err, "Notebook file does not exist.")
	invalidPath := filepath.Join(dir, "invalid.ipynb")
	require.NoError(t, os.WriteFile(invalidPath, []byte("not-json"), 0o644))
	invalidSnapshot, err := readLocalFileSnapshot(invalidPath, maxEditableFileSize)
	require.NoError(t, err)
	storeReadView(runtime.fileState, invalidPath, invalidSnapshot.Content, invalidSnapshot.Timestamp, nil, nil, "", false, true)
	_, err = loadNotebookEditState(invalidPath, notebookEditInput{
		EditMode: "insert",
		CellType: "code",
	}, runtime)
	require.EqualError(t, err, "Notebook is not valid JSON.")
	outOfRangePath := filepath.Join(dir, "out-of-range.ipynb")
	require.NoError(t, os.WriteFile(outOfRangePath, []byte(`{"cells":[{"id":"cell-0","cell_type":"code","metadata":{},"source":"x"}],"metadata":{},"nbformat":4,"nbformat_minor":5}`), 0o644))
	outOfRangeSnapshot, err := readLocalFileSnapshot(outOfRangePath, maxEditableFileSize)
	require.NoError(t, err)
	storeReadView(runtime.fileState, outOfRangePath, outOfRangeSnapshot.Content, outOfRangeSnapshot.Timestamp, nil, nil, "", false, true)
	_, err = loadNotebookEditState(outOfRangePath, notebookEditInput{
		EditMode: "replace",
		CellID:   "2",
	}, runtime)
	require.EqualError(t, err, "Cell with index 2 does not exist in notebook.")
	insertState := notebookEditState{
		notebook: map[string]any{"nbformat": 4, "nbformat_minor": 4},
		cells: []map[string]any{
			{"id": "cell-0", "source": "x = 1"},
			{"id": "cell-1", "source": "x = 2"},
		},
		cellType:  "code",
		cellIndex: 2,
	}
	var insertedCellID string
	insertedCellID = insertNotebookCell(&insertState, notebookEditInput{
		CellID:    "cell-2",
		NewSource: "x = 3",
	})
	require.Equal(t, "cell-2", insertedCellID)
	require.Len(t, insertState.cells, 3)
	require.Equal(t, "x = 3", insertState.cells[2]["source"])
}

func TestCommonHelpersCoverPathAndHTTPBranches(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	relPath, absPath, err := normalizePath(dir, "nested/file.txt")
	require.NoError(t, err)
	require.Equal(t, "nested/file.txt", relPath)
	require.Equal(t, filepath.Join(dir, "nested/file.txt"), absPath)
	_, _, err = normalizePath(dir, "../outside.txt")
	require.EqualError(t, err, "path is outside base_dir: ../outside.txt")
	runtime := newToolRuntime(dir, maxEditableFileSize)
	runtime.setBaseDir(filepath.Join(dir, "other"))
	require.Equal(t, filepath.Join(dir, "other"), runtime.currentBaseDir())
	require.Equal(t, "file.txt", relativePath(dir, filepath.Join(dir, "file.txt")))
	resp := &http.Response{Body: io.NopCloser(strings.NewReader("abcdef"))}
	body, err := readHTTPBody(resp, 3, 0)
	require.EqualError(t, err, "response body exceeded limit of 3 bytes")
	require.Nil(t, body)
	require.Equal(t, 2, countLines("alpha\nbeta"))
	require.True(t, matchDomainRule("docs.example.com", "*.example.com"))
	require.True(t, matchSearchDomainFilters("https://docs.example.com/path", []string{"example.com"}, nil))
	require.False(t, matchSearchDomainFilters("https://docs.example.com/path", nil, []string{"docs.example.com"}))
	require.Equal(t, "docs.example.com", searchURLHost("https://docs.example.com/path"))
}

func TestCommonHelpersCoverTextAndPatchBranches(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	absPath := filepath.Join(dir, "nested", "file.txt")
	relPath, normalizedAbs, err := normalizePath(dir, absPath)
	require.NoError(t, err)
	require.Equal(t, "nested/file.txt", relPath)
	require.Equal(t, filepath.Clean(absPath), normalizedAbs)
	_, _, err = normalizePath(dir, "")
	require.EqualError(t, err, "path is required")
	_, _, err = normalizePath(dir, filepath.Join(filepath.Dir(dir), "outside.txt"))
	require.ErrorContains(t, err, "path is outside base_dir")
	require.Equal(t, filepath.ToSlash(filepath.Clean("\x00")), relativePath(dir, "\x00"))
	body, err := readHTTPBody(&http.Response{}, 8, 0)
	require.NoError(t, err)
	require.Nil(t, body)
	body, err = readHTTPBody(&http.Response{Body: io.NopCloser(strings.NewReader("abcd"))}, 0, 4)
	require.NoError(t, err)
	require.Equal(t, []byte("abcd"), body)
	require.Zero(t, countLines(""))
	require.Equal(t, 2, countLines("alpha\nbeta\n"))
	require.Empty(t, splitTextLines(""))
	require.Equal(t, []string{"alpha", "beta"}, splitTextLines("alpha\nbeta\n"))
	sliced, startLine, totalLines := sliceLines("alpha\nbeta\ngamma\n", 0, intPtr(2))
	require.Equal(t, "alpha\nbeta", sliced)
	require.Equal(t, 1, startLine)
	require.Equal(t, 3, totalLines)
	require.Equal(t, "alpha\nbeta\ngamma\n", normalizeNewlines("alpha\r\nbeta\rgamma\n"))
	require.Equal(t, "\r\n", detectLineEnding([]byte("alpha\r\nbeta")))
	require.Equal(t, "alpha\r\nbeta", applyLineEnding("alpha\nbeta", "\r\n"))
	utf8Encoded, err := encodeTextBytes("alpha\nbeta", "utf8", "\n")
	require.NoError(t, err)
	require.Equal(t, []byte("alpha\nbeta"), utf8Encoded)
	require.Equal(t, "YQ==", fileBase64([]byte("a")))
	require.True(t, isProbablyBinary([]byte("a\x00b")))
	require.False(t, isProbablyBinary([]byte{0xff, 0xfe, 'a', 0x00}))
	patch := buildStructuredPatch("alpha\nbeta\n", "alpha\ngamma\n")
	require.Len(t, patch, 1)
	require.Equal(t, 2, patch[0].OldStart)
	require.Equal(t, []string{"-beta", "+gamma"}, patch[0].Lines)
	require.Nil(t, buildStructuredPatch("same\n", "same\n"))
}

func TestCommonHelpersCoverRemainingErrorBranches(t *testing.T) {
	t.Parallel()
	require.Equal(t, filepath.ToSlash(filepath.Clean("file.txt")), relativePath("\x00", "file.txt"))
	_, err := readHTTPBody(&http.Response{Body: io.NopCloser(errReader{err: fs.ErrInvalid})}, 8, 0)
	require.ErrorIs(t, err, fs.ErrInvalid)
	sliced, startLine, totalLines := sliceLines("alpha\nbeta\n", 10, nil)
	require.Empty(t, sliced)
	require.Equal(t, 10, startLine)
	require.Equal(t, 2, totalLines)
}

func TestGrepTypePatternsExposeKnownAliases(t *testing.T) {
	t.Parallel()
	require.Contains(t, typePatterns("go"), "**/*.go")
	require.Contains(t, typePatterns("js"), "**/*.js")
	require.Contains(t, typePatterns("ts"), "**/*.tsx")
	require.Contains(t, typePatterns("py"), "**/*.py")
	require.Contains(t, typePatterns("java"), "**/*.java")
	require.Contains(t, typePatterns("rs"), "**/*.rs")
	require.Contains(t, typePatterns("json"), "**/*.json")
	require.Contains(t, typePatterns("md"), "**/*.md")
	require.Contains(t, typePatterns("yaml"), "**/*.yaml")
	require.Contains(t, typePatterns("txt"), "**/*.txt")
	require.Equal(t, []string{"**/*.unknown"}, typePatterns("unknown"))
}

func TestGrepAndPDFHelpersCoverRemainingBranches(t *testing.T) {
	t.Parallel()
	items, limit := sliceStrings([]string{"a", "b", "c"}, 1, 1)
	require.Equal(t, []string{"b"}, items)
	require.NotNil(t, limit)
	require.Equal(t, 1, *limit)
	items, limit = sliceStrings([]string{"a", "b", "c"}, 5, 1)
	require.Empty(t, items)
	require.Nil(t, limit)
	require.Equal(t, []string{"-e", "-pattern"}, appendRipgrepPattern(nil, "-pattern"))
	require.Equal(t, []string{"pattern"}, appendRipgrepPattern(nil, "pattern"))
	require.Equal(t, 0, grepOffset(grepInput{}))
	require.Equal(t, 3, grepOffset(grepInput{Offset: intPtr(3)}))
	require.Equal(t, defaultGrepHeadLimit, grepLimit(grepInput{}))
	require.Equal(t, 5, grepLimit(grepInput{HeadLimit: intPtr(5)}))
	rangeAll, err := resolvePDFPageRange("2-", 4)
	require.NoError(t, err)
	require.Equal(t, pdfPageRange{FirstPage: 2, LastPage: 4, Count: 3}, rangeAll)
	_, err = resolvePDFPageRange("", 4)
	require.EqualError(t, err, `Invalid pages parameter: "". Use formats like "1-5", "3", or "10-20". Pages are 1-indexed.`)
	_, err = resolvePDFPageRange("3-1", 4)
	require.EqualError(t, err, `Invalid pages parameter: "3-1". Use formats like "1-5", "3", or "10-20". Pages are 1-indexed.`)
	_, err = resolvePDFPageRange("1-21", 30)
	require.EqualError(t, err, `Page range "1-21" exceeds maximum of 20 pages per request. Please use a smaller range.`)
}

func TestWebSearchHelpersNormalizeWrappedDuckDuckGoURLs(t *testing.T) {
	t.Parallel()
	require.Equal(t, "https://golang.org/doc/", normalizeDuckDuckGoResultURL("https://duckduckgo.com/l/?uddg=https%3A%2F%2Fgolang.org%2Fdoc%2F"))
	require.Equal(t, "https://example.com", normalizeDuckDuckGoResultURL("https://duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com"))
	require.Equal(t, "https://example.com/a%20b", normalizeDuckDuckGoResultURL("https://duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fa%2520b"))
	require.Equal(t, "%ZZ", normalizeDuckDuckGoResultURL("https://duckduckgo.com/l/?uddg=%25ZZ"))
	require.Equal(t, "not a url", normalizeDuckDuckGoResultURL("not a url"))
	require.Empty(t, normalizeDuckDuckGoResultURL("   "))
}

func TestResolveRedirectURLAndDomainFiltersHandleEdgeCases(t *testing.T) {
	t.Parallel()
	nextURL, err := resolveRedirectURL("https://example.com/start", "https://example.com/next")
	require.NoError(t, err)
	require.Equal(t, "https://example.com/next", nextURL)
	_, err = resolveRedirectURL("://bad", "/next")
	require.Error(t, err)
	require.False(t, matchSearchDomainFilters("not a url", []string{"example.com"}, nil))
}

func TestWebSearchBackendsCoverRemainingErrorBranches(t *testing.T) {
	t.Parallel()
	duckBackend := &duckDuckGoSearchBackend{
		client:  http.DefaultClient,
		baseURL: "://bad",
	}
	_, err := duckBackend.search(context.Background(), webSearchInput{Query: "example"})
	require.Error(t, err)
	requestFailedClient := &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, fs.ErrInvalid
		}),
	}
	duckBackend = &duckDuckGoSearchBackend{
		client:  requestFailedClient,
		baseURL: "https://example.com/search",
	}
	_, err = duckBackend.search(context.Background(), webSearchInput{Query: "example"})
	require.ErrorIs(t, err, fs.ErrInvalid)
	bodyFailedClient := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			require.Equal(t, "tester", req.Header.Get("User-Agent"))
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{},
				Body:       io.NopCloser(errReader{err: fs.ErrInvalid}),
				Request:    req,
			}, nil
		}),
	}
	duckBackend = &duckDuckGoSearchBackend{
		client:    bodyFailedClient,
		baseURL:   "https://example.com/search",
		userAgent: "tester",
	}
	_, err = duckBackend.search(context.Background(), webSearchInput{Query: "example"})
	require.ErrorIs(t, err, fs.ErrInvalid)
	statusFailedClient := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Status:     "429 Too Many Requests",
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader("rate limited")),
				Request:    req,
			}, nil
		}),
	}
	duckBackend = &duckDuckGoSearchBackend{
		client:  statusFailedClient,
		baseURL: "https://example.com/search",
	}
	_, err = duckBackend.search(context.Background(), webSearchInput{Query: "example"})
	require.EqualError(t, err, "duckduckgo search request failed: status=429 body=rate limited")
	googleBackend := &googleSearchBackend{
		client:  http.DefaultClient,
		options: &WebSearchOptions{APIKey: "key", EngineID: "engine", BaseURL: "://bad"},
	}
	_, err = googleBackend.search(context.Background(), webSearchInput{Query: "example"})
	require.Error(t, err)
	googleBackend = &googleSearchBackend{
		client: requestFailedClient,
		options: &WebSearchOptions{
			APIKey:   "key",
			EngineID: "engine",
			BaseURL:  "https://example.com/search",
		},
	}
	_, err = googleBackend.search(context.Background(), webSearchInput{Query: "example"})
	require.ErrorIs(t, err, fs.ErrInvalid)
}

func TestExecRipgrepReturnsErrorForInvalidArguments(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep is not available")
	}
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("alpha\n"), 0o644))
	_, err := execRipgrep(context.Background(), dir, "--definitely-invalid-flag", "alpha")
	require.Error(t, err)
	require.Contains(t, err.Error(), "ripgrep failed")
}

func TestNotebookCellHelpersResolveIDsAndIndexes(t *testing.T) {
	t.Parallel()
	idx, ok := parseNotebookCellID(" cell-3 ")
	require.True(t, ok)
	require.Equal(t, 3, idx)
	_, ok = parseNotebookCellID("-1")
	require.False(t, ok)
	cells := []map[string]any{
		{"id": "first"},
		{"id": "second"},
	}
	byID, err := notebookCellIndex(cells, "second")
	require.NoError(t, err)
	require.Equal(t, 1, byID)
	byNumericID, err := notebookCellIndex(cells, "cell-1")
	require.NoError(t, err)
	require.Equal(t, 1, byNumericID)
	_, err = notebookCellIndex(cells, "missing")
	require.EqualError(t, err, `Cell with ID "missing" not found in notebook.`)
}

func TestGrepHelpersHandleContextAndCountOutput(t *testing.T) {
	t.Parallel()
	require.Equal(t, []string{"-C", "2"}, appendRipgrepContext(nil, grepInput{ContextAlt: intPtr(2)}))
	require.Equal(t, []string{"-C", "1"}, appendRipgrepContext(nil, grepInput{Context: intPtr(1)}))
	require.Equal(t, []string{"-B", "3", "-A", "4"}, appendRipgrepContext(nil, grepInput{
		Before: intPtr(3),
		After:  intPtr(4),
	}))
	out := formatRipgrepCountOutput(0, 0, []string{"a.txt:2", "b.txt:3", "raw"})
	require.Equal(t, "count", out.Mode)
	require.Equal(t, 3, out.NumFiles)
	require.Equal(t, 5, out.NumMatches)
	require.Equal(t, "a.txt:2\nb.txt:3\nraw", out.Content)
}

func TestGrepHelpersCoverRemainingFallbackAndRipgrepBranches(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alpha\nbeta\nalpha\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {\nprintln(\"alpha\")\nprintln(\"beta\")\n}\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "note.md"), []byte("alpha beta"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "data.bin"), []byte{0x00, 'a', 'l', 'p', 'h', 'a'}, 0o644))
	runtime := newToolRuntime(dir, maxEditableFileSize)
	_, err := runFallbackGrep(runtime, dir, grepInput{Pattern: "("})
	require.Error(t, err)
	fileCandidates, err := collectGrepCandidates(dir, "a.txt", "", "")
	require.NoError(t, err)
	require.Equal(t, []string{filepath.Join(dir, "a.txt")}, fileCandidates)
	typeCandidates, err := collectGrepCandidates(dir, "", "", "go")
	require.NoError(t, err)
	require.Equal(t, []string{filepath.Join(dir, "main.go")}, typeCandidates)
	globCandidates, err := collectGrepCandidates(dir, "", "*.md", "")
	require.NoError(t, err)
	require.Equal(t, []string{filepath.Join(dir, "note.md")}, globCandidates)
	_, err = collectGrepCandidates(dir, "missing", "", "")
	require.Error(t, err)
	require.False(t, matchesAnyPattern("main.go", []string{"["}))
	require.Equal(t, []string{"*.go", "*.md", "{foo,bar}.txt"}, splitGlobPatterns("*.go,*.md {foo,bar}.txt"))
	contentCollector := newFallbackGrepCollector("content")
	err = collectFallbackLineMatch("alpha\nbeta\n", "a.txt", regexp.MustCompile("alpha"), grepInput{
		ShowLineNum: boolPtr(false),
	}, contentCollector)
	require.NoError(t, err)
	require.Equal(t, []string{"a.txt:alpha"}, contentCollector.contentLines)
	countCollector := newFallbackGrepCollector("count")
	err = collectFallbackMultilineMatch("alpha\nbeta\nalpha\n", "a.txt", regexp.MustCompile("alpha(?s).*beta"), grepInput{}, countCollector)
	require.NoError(t, err)
	require.Equal(t, []string{"a.txt:1"}, countCollector.countLines)
	fileCollector := newFallbackGrepCollector("files_with_matches")
	err = collectFallbackMultilineMatch("alpha\nbeta\n", "a.txt", regexp.MustCompile("alpha(?s).*beta"), grepInput{}, fileCollector)
	require.NoError(t, err)
	require.Equal(t, []string{"a.txt"}, fileCollector.fileMatches)
	fallbackOut, err := runFallbackGrep(runtime, dir, grepInput{
		Pattern:   "alpha",
		HeadLimit: intPtr(1),
	})
	require.NoError(t, err)
	require.Equal(t, "files_with_matches", fallbackOut.Mode)
	require.Len(t, fallbackOut.Filenames, 1)
	rgPath := writeExecutableFile(
		t,
		dir,
		"fake-rg.sh",
		"#!/bin/sh\nprintf 'main.go:1:alpha\\nmain.go:2:beta\\n'\n",
	)
	restore := withRipgrepForTest(func(string) (string, error) {
		return rgPath, nil
	})
	ripgrepContentOut, handled, err := runRipgrepCommand(context.Background(), dir, ".", grepInput{
		Pattern:    "alpha",
		OutputMode: "content",
	})
	restore()
	require.True(t, handled)
	require.NoError(t, err)
	require.Equal(t, "content", ripgrepContentOut.Mode)
	require.Contains(t, ripgrepContentOut.Content, "main.go:1:alpha")
	rgEmptyPath := writeExecutableFile(
		t,
		dir,
		"fake-rg-empty.sh",
		"#!/bin/sh\nexit 1\n",
	)
	restore = withRipgrepForTest(func(string) (string, error) {
		return rgEmptyPath, nil
	})
	lines, err := execRipgrep(context.Background(), dir, "alpha")
	restore()
	require.NoError(t, err)
	require.Empty(t, lines)
	rgErrorPath := writeExecutableFile(
		t,
		dir,
		"fake-rg-error.sh",
		"#!/bin/sh\nexit 2\n",
	)
	restore = withRipgrepForTest(func(string) (string, error) {
		return rgErrorPath, nil
	})
	_, err = execRipgrep(context.Background(), dir, "alpha")
	restore()
	require.EqualError(t, err, "ripgrep failed: ripgrep exited with code 2")
}

func TestExecRipgrepReturnsEmptyOnNoMatches(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep is not available")
	}
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("alpha\nbeta\n"), 0o644))
	lines, err := execRipgrep(context.Background(), dir, "--files-with-matches", "missing-pattern")
	require.NoError(t, err)
	require.Empty(t, lines)
}

func TestRunLocalRipgrepReturnsFalseWhenRipgrepIsUnavailable(t *testing.T) {
	t.Parallel()
	restore := withRipgrepForTest(func(string) (string, error) {
		return "", errors.New("not found")
	})
	defer restore()
	out, ok, err := runLocalRipgrep(context.Background(), t.TempDir(), grepInput{Pattern: "alpha"})
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, grepOutput{}, out)
}

func TestRunLocalRipgrepRejectsPathsOutsideBaseDir(t *testing.T) {
	t.Parallel()
	restore := withRipgrepForTest(func(string) (string, error) {
		return "/bin/true", nil
	})
	defer restore()
	_, ok, err := runLocalRipgrep(context.Background(), t.TempDir(), grepInput{
		Pattern: "alpha",
		Path:    "../outside.txt",
	})
	require.Error(t, err)
	require.True(t, ok)
	require.Contains(t, err.Error(), "path is outside base_dir")
}

func TestRunLocalRipgrepSupportsFilesContentAndCountModes(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep is not available")
	}
	restore := withRipgrepForTest(exec.LookPath)
	defer restore()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alpha\nbeta\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("alpha\n"), 0o644))
	filesOut, ok, err := runLocalRipgrep(context.Background(), dir, grepInput{
		Pattern:    "alpha",
		OutputMode: "files_with_matches",
	})
	require.NoError(t, err)
	require.True(t, ok)
	require.ElementsMatch(t, []string{"a.txt", "b.txt"}, filesOut.Filenames)
	contentOut, ok, err := runLocalRipgrep(context.Background(), dir, grepInput{
		Pattern:     "alpha",
		OutputMode:  "content",
		ContextAlt:  intPtr(1),
		ShowLineNum: boolPtr(true),
	})
	require.NoError(t, err)
	require.True(t, ok)
	require.Contains(t, contentOut.Content, "a.txt:1:alpha")
	countOut, ok, err := runLocalRipgrep(context.Background(), dir, grepInput{
		Pattern:    "alpha",
		OutputMode: "count",
	})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 2, countOut.NumMatches)
}

func TestBashAndProcessHelpersCoverTimeoutAndExitState(t *testing.T) {
	t.Setenv("BASH_DEFAULT_TIMEOUT_MS", "50")
	require.Equal(t, 50, bashTimeout(nil))
	require.Equal(t, defaultBashTimeoutMs, bashTimeout(intPtr(0)))
	require.Equal(t, maxBashTimeoutMs, bashTimeout(intPtr(maxBashTimeoutMs+1)))
	proc, err := os.StartProcess("/bin/true", []string{"true"}, &os.ProcAttr{
		Env:   processEnv(nil),
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
	})
	require.NoError(t, err)
	state, waitErr := proc.Wait()
	require.NoError(t, waitErr)
	require.Equal(t, "completed", backgroundTaskStatus(nil, state))
	require.Equal(t, 0, backgroundTaskExitCode(nil, state))
	require.Equal(t, "exited", backgroundTaskStatus(errors.New("wait failed"), nil))
	require.Equal(t, 1, backgroundTaskExitCode(errors.New("wait failed"), nil))
	require.Equal(t, 0, backgroundTaskExitCode(nil, nil))
	require.Equal(t, "exited", backgroundTaskStatus(nil, nil))
	require.Equal(t, "stdout\nstderr", joinOutput("stdout", "stderr"))
	require.Equal(t, "stderr", joinOutput("", "stderr"))
}

func TestBashHelpersCoverForegroundAndBackgroundErrorBranches(t *testing.T) {
	missingBaseDir := filepath.Join(t.TempDir(), "missing")
	runtime := newToolRuntime(missingBaseDir, maxEditableFileSize)
	out, err := runForegroundCommand(context.Background(), runtime, bashInput{Command: "printf 'hello'"})
	require.NoError(t, err)
	require.Equal(t, 1, out.ExitCode)
	require.False(t, out.TimedOut)
	require.Empty(t, out.Stdout)
	require.Empty(t, out.Stderr)
	tmpRoot := t.TempDir()
	tmpFile := filepath.Join(tmpRoot, "tmp-file")
	require.NoError(t, os.WriteFile(tmpFile, []byte("x"), 0o644))
	t.Setenv("TMPDIR", tmpFile)
	_, err = runBackgroundCommand(newToolRuntime(t.TempDir(), maxEditableFileSize), "printf 'hello'")
	require.Error(t, err)
	_, err = runBackgroundCommand(runtime, "printf 'hello'")
	require.Error(t, err)
}

func TestRunCapturedProcessAndWaitForProcess(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	result, err := runCapturedProcess(context.Background(), dir, []string{"TEST_KEY=VALUE"}, "bash", "-lc", "printf \"$TEST_KEY\"")
	require.NoError(t, err)
	require.Equal(t, "VALUE", string(result.Stdout))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	proc, err := os.StartProcess("/bin/sleep", []string{"sleep", "1"}, &os.ProcAttr{
		Dir:   dir,
		Env:   processEnv(nil),
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
	})
	require.NoError(t, err)
	state := waitForProcess(ctx, proc)
	require.ErrorIs(t, state.Err, context.DeadlineExceeded)
	require.NotNil(t, state.State)
}

func TestProcessPipeHelpersAndTaskStopErrors(t *testing.T) {
	t.Parallel()
	stdin, stdoutReader, stdoutWriter, stderrReader, stderrWriter, closeAll, err := processPipes()
	require.NoError(t, err)
	require.NotNil(t, stdin)
	require.NotNil(t, stdoutReader)
	require.NotNil(t, stdoutWriter)
	require.NotNil(t, stderrReader)
	require.NotNil(t, stderrWriter)
	require.NoError(t, closeAll())
	runtime := newToolRuntime(t.TempDir(), maxEditableFileSize)
	stopTool, err := newTaskStopTool(runtime)
	require.NoError(t, err)
	callable, ok := stopTool.(tool.CallableTool)
	require.True(t, ok)
	_, err = callToolRaw(callable, taskStopInput{})
	require.EqualError(t, err, "Missing required parameter: task_id")
	runtime.taskState.tasks["done"] = &backgroundTask{
		ID:      "done",
		Command: "echo done",
		Type:    toolBash,
		Status:  "completed",
	}
	_, err = callToolRaw(callable, taskStopInput{TaskID: "done"})
	require.EqualError(t, err, "Task done is not running (status: completed)")
	runtime.taskState.tasks["running"] = &backgroundTask{
		ID:      "running",
		Command: "sleep 30",
		Type:    toolBash,
		Status:  "running",
	}
	_, err = callToolRaw(callable, taskStopInput{TaskID: "running"})
	require.EqualError(t, err, "Task running has no running process")
	require.Equal(t, "running", runtime.taskState.tasks["running"].Status)
}

func TestTaskStopAcceptsShellIDAndPropagatesKillErrors(t *testing.T) {
	t.Parallel()
	runtime := newToolRuntime(t.TempDir(), maxEditableFileSize)
	stopTool, err := newTaskStopTool(runtime)
	require.NoError(t, err)
	callable, ok := stopTool.(tool.CallableTool)
	require.True(t, ok)
	_, err = callToolRaw(callable, taskStopInput{ShellID: "missing"})
	require.EqualError(t, err, "No task found with ID: missing")
	proc, err := os.StartProcess("/bin/true", []string{"true"}, &os.ProcAttr{
		Env:   processEnv(nil),
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
	})
	require.NoError(t, err)
	_, err = proc.Wait()
	require.NoError(t, err)
	runtime.taskState.tasks["finished"] = &backgroundTask{
		ID:      "finished",
		Command: "true",
		Type:    toolBash,
		Status:  "running",
		Process: proc,
	}
	_, err = callToolRaw(callable, taskStopInput{ShellID: "finished"})
	require.Error(t, err)
	require.Equal(t, "running", runtime.taskState.tasks["finished"].Status)
}

func TestPDFAndNotebookHelpersCoverFallbackBranches(t *testing.T) {
	t.Parallel()
	pdfBytes := newTestPDF(t, []string{"page one", "page two", "page three"})
	pageCount, err := pdfPageCount(pdfBytes)
	require.NoError(t, err)
	require.Equal(t, 3, pageCount)
	pages, err := resolvePDFPageRange("1-3", 3)
	require.NoError(t, err)
	require.Equal(t, pdfPageRange{FirstPage: 1, LastPage: 3, Count: 3}, pages)
	_, err = resolvePDFPageRange("4", 3)
	require.EqualError(t, err, "Page 4 exceeds the PDF page count of 3.")
	require.Equal(t, "cell-2", notebookResultCellID(map[string]any{}, 2, ""))
	require.Equal(t, "fallback", notebookResultCellID(map[string]any{}, 2, "fallback"))
	value, ok := notebookInt(float64(3))
	require.True(t, ok)
	require.Equal(t, 3, value)
	_, ok = notebookInt("bad")
	require.False(t, ok)
	require.Equal(t, "python", notebookLanguage(map[string]any{}))
	require.Equal(t, "go", notebookLanguage(map[string]any{
		"metadata": map[string]any{
			"language_info": map[string]any{"name": "go"},
		},
	}))
}

func TestPDFHelpersCoverRemainingBranches(t *testing.T) {
	t.Parallel()
	pdftoppmTestMu.Lock()
	t.Cleanup(func() {
		pdftoppmTestMu.Unlock()
	})
	_, err := pdfPageCount([]byte("not-a-pdf"))
	require.ErrorContains(t, err, "failed to create PDF reader")
	rangeOne, err := resolvePDFPageRange("2", 4)
	require.NoError(t, err)
	require.Equal(t, pdfPageRange{FirstPage: 2, LastPage: 2, Count: 1}, rangeOne)
	_, err = resolvePDFPageRange("bad", 4)
	require.EqualError(t, err, `Invalid pages parameter: "bad". Use formats like "1-5", "3", or "10-20". Pages are 1-indexed.`)
	_, err = resolvePDFPageRange("6-", 4)
	require.EqualError(t, err, `Page range "6-" is outside the PDF page count of 4.`)
	_, err = resolvePDFPageRange("5-6", 4)
	require.EqualError(t, err, `Page range "5-6" exceeds the PDF page count of 4.`)
	scriptDir := t.TempDir()
	successScript := filepath.Join(scriptDir, "pdftoppm-success")
	require.NoError(t, os.WriteFile(successScript, []byte("#!/bin/bash\nprefix=\"${@: -1}\"\ntouch \"${prefix}-1.jpg\" \"${prefix}-2.jpg\"\n"), 0o755))
	oldLookPath := pdftoppmLookPath
	oldPath := pdftoppmPath
	oldOnce := pdftoppmOnce
	pdftoppmLookPath = func(string) (string, error) {
		return successScript, nil
	}
	pdftoppmPath = ""
	pdftoppmOnce = sync.Once{}
	t.Cleanup(func() {
		pdftoppmLookPath = oldLookPath
		pdftoppmPath = oldPath
		pdftoppmOnce = oldOnce
	})
	path, err := pdftoppmBinary()
	require.NoError(t, err)
	require.Equal(t, successScript, path)
	outputDir, count, err := extractPDFPages(filepath.Join(t.TempDir(), "fake.pdf"), pdfPageRange{
		FirstPage: 1,
		LastPage:  2,
		Count:     2,
	})
	require.NoError(t, err)
	require.Equal(t, 2, count)
	defer os.RemoveAll(outputDir)
	_, statErr := os.Stat(filepath.Join(outputDir, "page-1.jpg"))
	require.NoError(t, statErr)
	noImageScript := filepath.Join(scriptDir, "pdftoppm-empty")
	require.NoError(t, os.WriteFile(noImageScript, []byte("#!/bin/bash\nexit 0\n"), 0o755))
	pdftoppmPath = noImageScript
	_, _, err = extractPDFPages(filepath.Join(t.TempDir(), "fake.pdf"), pdfPageRange{
		FirstPage: 1,
		LastPage:  1,
		Count:     1,
	})
	require.EqualError(t, err, "failed to extract PDF pages: no rendered page images were produced")
	failScript := filepath.Join(scriptDir, "pdftoppm-fail")
	require.NoError(t, os.WriteFile(failScript, []byte("#!/bin/bash\necho render failed >&2\nexit 1\n"), 0o755))
	pdftoppmPath = failScript
	_, _, err = extractPDFPages(filepath.Join(t.TempDir(), "fake.pdf"), pdfPageRange{
		FirstPage: 1,
		LastPage:  1,
		Count:     1,
	})
	require.EqualError(t, err, "failed to extract PDF pages: render failed")
}

func TestExtractPDFPagesFailsWhenPdftoppmIsUnavailable(t *testing.T) {
	t.Parallel()
	pdftoppmTestMu.Lock()
	t.Cleanup(func() {
		pdftoppmTestMu.Unlock()
	})
	oldLookPath := pdftoppmLookPath
	oldPath := pdftoppmPath
	oldOnce := pdftoppmOnce
	pdftoppmLookPath = func(string) (string, error) {
		return "", errors.New("not found")
	}
	pdftoppmPath = ""
	pdftoppmOnce = sync.Once{}
	t.Cleanup(func() {
		pdftoppmLookPath = oldLookPath
		pdftoppmPath = oldPath
		pdftoppmOnce = oldOnce
	})
	_, _, err := extractPDFPages(filepath.Join(t.TempDir(), "missing.pdf"), pdfPageRange{
		FirstPage: 1,
		LastPage:  1,
		Count:     1,
	})
	require.EqualError(t, err, "pdftoppm is not installed. Install poppler-utils (e.g. `brew install poppler` or `apt-get install poppler-utils`) to enable PDF page rendering.")
}

func TestReadTaskSnapshotHandlesMissingOutputFile(t *testing.T) {
	t.Parallel()
	runtime := newToolRuntime(t.TempDir(), maxEditableFileSize)
	exitCode := 17
	runtime.taskState.tasks["task-1"] = &backgroundTask{
		ID:         "task-1",
		Command:    "echo hi",
		Type:       toolBash,
		OutputPath: filepath.Join(t.TempDir(), "missing.log"),
		Status:     "completed",
		ExitCode:   &exitCode,
	}
	snapshot, err := readTaskSnapshot(runtime, "task-1")
	require.NoError(t, err)
	require.Equal(t, "task-1", snapshot.TaskID)
	require.Equal(t, toolBash, snapshot.TaskType)
	require.Equal(t, "completed", snapshot.Status)
	require.Equal(t, "echo hi", snapshot.Description)
	require.Empty(t, snapshot.Output)
	require.NotNil(t, snapshot.ExitCode)
	require.Equal(t, 17, *snapshot.ExitCode)
}

func TestReadTaskSnapshotReturnsReadErrorForDirectoryOutputPath(t *testing.T) {
	t.Parallel()
	outputDir := t.TempDir()
	runtime := newToolRuntime(t.TempDir(), maxEditableFileSize)
	runtime.taskState.tasks["task-1"] = &backgroundTask{
		ID:         "task-1",
		Command:    "echo hi",
		Type:       toolBash,
		OutputPath: outputDir,
		Status:     "completed",
	}
	_, err := readTaskSnapshot(runtime, "task-1")
	require.Error(t, err)
}

type errReader struct {
	err error
}

func (r errReader) Read(_ []byte) (int, error) {
	return 0, r.err
}

type stubTool struct {
	decl *tool.Declaration
}

func (s stubTool) Declaration() *tool.Declaration {
	return s.decl
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
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

func writeExecutableFile(t *testing.T, dir string, name string, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o755))
	return path
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
var pdftoppmTestMu sync.Mutex

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

func callToolRawWithContext(target tool.CallableTool, ctx context.Context, input any) (any, error) {
	args, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	return target.Call(ctx, args)
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

func strPtr(value string) *string {
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
