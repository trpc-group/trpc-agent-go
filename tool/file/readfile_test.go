//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
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
)

func TestFileTool_ReadFile(t *testing.T) {
	// Create a temporary directory for testing.
	tempDir := t.TempDir()
	toolSet, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet, ok := toolSet.(*fileToolSet)
	assert.True(t, ok)
	// Create a test file first.
	testContent := "Test content for reading"
	testFile := filepath.Join(tempDir, "read_test.txt")
	err = os.WriteFile(testFile, []byte(testContent), 0644)
	assert.NoError(t, err)
	// Test reading the file.
	req := &readFileRequest{FileName: "read_test.txt"}
	rsp, err := fileToolSet.readFile(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, testContent, rsp.Contents)
}

func TestFileTool_ReadFile_EmptyFileName(t *testing.T) {
	// Create a temporary directory for testing.
	tempDir := t.TempDir()
	toolSet, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet, ok := toolSet.(*fileToolSet)
	assert.True(t, ok)
	// Test reading with empty file name.
	req := &readFileRequest{FileName: ""}
	_, err = fileToolSet.readFile(context.Background(), req)
	assert.Error(t, err)
}

func TestFileTool_ReadFile_NonExistFile(t *testing.T) {
	// Create a temporary directory for testing.
	tempDir := t.TempDir()
	toolSet, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet, ok := toolSet.(*fileToolSet)
	assert.True(t, ok)
	// Test reading with non-existent file name.
	req := &readFileRequest{FileName: "non_existent.txt"}
	_, err = fileToolSet.readFile(context.Background(), req)
	assert.Error(t, err)
}

func TestFileTool_ReadFile_Empty(t *testing.T) {
	// Create a temporary directory for testing.
	tempDir := t.TempDir()
	toolSet, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet, ok := toolSet.(*fileToolSet)
	assert.True(t, ok)
	// Create a test file first.
	testFile := filepath.Join(tempDir, "read_test.txt")
	err = os.WriteFile(testFile, []byte{}, 0644)
	assert.NoError(t, err)
	// Test reading with empty file content.
	req := &readFileRequest{FileName: "read_test.txt"}
	rsp, err := fileToolSet.readFile(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, "", rsp.Contents)
}

func TestFileTool_ReadFile_Directory(t *testing.T) {
	tempDir := t.TempDir()
	toolSet, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet, ok := toolSet.(*fileToolSet)
	assert.True(t, ok)
	// Create a directory
	dirPath := filepath.Join(tempDir, "testdir")
	err = os.MkdirAll(dirPath, 0755)
	assert.NoError(t, err)
	// Try to read the directory path
	req := &readFileRequest{FileName: "testdir"}
	_, err = fileToolSet.readFile(context.Background(), req)
	assert.Error(t, err)
}

func TestFileTool_ReadFile_WithOffset(t *testing.T) {
	tempDir := t.TempDir()
	toolSet, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet, ok := toolSet.(*fileToolSet)
	assert.True(t, ok)
	// Create a multi-line test file.
	testContent := "line1\nline2\nline3\nline4\nline5"
	testFile := filepath.Join(tempDir, "multiline.txt")
	err = os.WriteFile(testFile, []byte(testContent), 0644)
	assert.NoError(t, err)
	// Test reading from start line 3.
	startLine := 3
	req := &readFileRequest{FileName: "multiline.txt", StartLine: &startLine}
	rsp, err := fileToolSet.readFile(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, "line3\nline4\nline5", rsp.Contents)
}

func TestFileTool_ReadFile_WithLimit(t *testing.T) {
	tempDir := t.TempDir()
	toolSet, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet, ok := toolSet.(*fileToolSet)
	assert.True(t, ok)
	// Create a multi-line test file.
	testContent := "line1\nline2\nline3\nline4\nline5"
	testFile := filepath.Join(tempDir, "multiline.txt")
	err = os.WriteFile(testFile, []byte(testContent), 0644)
	assert.NoError(t, err)
	// Test reading with limit 3 (should read first 3 lines).
	numLines := 3
	req := &readFileRequest{FileName: "multiline.txt", NumLines: &numLines}
	rsp, err := fileToolSet.readFile(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, "line1\nline2\nline3", rsp.Contents)
}

func TestFileTool_ReadFile_WithOffsetAndLimit(t *testing.T) {
	tempDir := t.TempDir()
	toolSet, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet, ok := toolSet.(*fileToolSet)
	assert.True(t, ok)
	// Create a multi-line test file.
	testContent := "line1\nline2\nline3\nline4\nline5"
	testFile := filepath.Join(tempDir, "multiline.txt")
	err = os.WriteFile(testFile, []byte(testContent), 0644)
	assert.NoError(t, err)
	// Test reading from start line 2 with num lines 2.
	startLine := 2
	numLines := 2
	req := &readFileRequest{FileName: "multiline.txt", StartLine: &startLine, NumLines: &numLines}
	rsp, err := fileToolSet.readFile(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, "line2\nline3", rsp.Contents)
}

func TestFileTool_ReadFile_InvalidOffset(t *testing.T) {
	tempDir := t.TempDir()
	toolSet, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet, ok := toolSet.(*fileToolSet)
	assert.True(t, ok)
	// Create a test file with 3 lines.
	testContent := "line1\nline2\nline3"
	testFile := filepath.Join(tempDir, "test.txt")
	err = os.WriteFile(testFile, []byte(testContent), 0644)
	assert.NoError(t, err)
	// Test start line less than 1.
	startLine := 0
	req := &readFileRequest{FileName: "test.txt", StartLine: &startLine}
	_, err = fileToolSet.readFile(context.Background(), req)
	assert.Error(t, err)
	// Test start line beyond file lines.
	startLine = 4
	req2 := &readFileRequest{FileName: "test.txt", StartLine: &startLine}
	_, err = fileToolSet.readFile(context.Background(), req2)
	assert.Error(t, err)
}

func TestFileTool_ReadFile_InvalidLimit(t *testing.T) {
	tempDir := t.TempDir()
	toolSet, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet, ok := toolSet.(*fileToolSet)
	assert.True(t, ok)
	// Create a test file.
	testContent := "line1\nline2\nline3"
	testFile := filepath.Join(tempDir, "test.txt")
	err = os.WriteFile(testFile, []byte(testContent), 0644)
	assert.NoError(t, err)
	// Test zero num lines.
	numLines := 0
	req := &readFileRequest{FileName: "test.txt", NumLines: &numLines}
	_, err = fileToolSet.readFile(context.Background(), req)
	assert.Error(t, err)
	// Test negative num lines.
	numLines = -1
	req2 := &readFileRequest{FileName: "test.txt", NumLines: &numLines}
	_, err = fileToolSet.readFile(context.Background(), req2)
	assert.Error(t, err)
}

func TestFileTool_ReadFile_OffsetAtEnd(t *testing.T) {
	tempDir := t.TempDir()
	toolSet, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet, ok := toolSet.(*fileToolSet)
	assert.True(t, ok)
	// Create a test file with 3 lines.
	testContent := "line1\nline2\nline3"
	testFile := filepath.Join(tempDir, "test.txt")
	err = os.WriteFile(testFile, []byte(testContent), 0644)
	assert.NoError(t, err)
	// Test start line at the end of file.
	startLine := 4
	req := &readFileRequest{FileName: "test.txt", StartLine: &startLine}
	rsp, err := fileToolSet.readFile(context.Background(), req)
	assert.Error(t, err)
	assert.Equal(t, "", rsp.Contents)
}

func TestFileTool_ReadFile_SingleLine(t *testing.T) {
	tempDir := t.TempDir()
	toolSet, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet, ok := toolSet.(*fileToolSet)
	assert.True(t, ok)
	// Create a single line file.
	testContent := "single line content"
	testFile := filepath.Join(tempDir, "single.txt")
	err = os.WriteFile(testFile, []byte(testContent), 0644)
	assert.NoError(t, err)
	// Test reading with start line 1 and num lines 1.
	startLine := 1
	numLines := 1
	req := &readFileRequest{FileName: "single.txt", StartLine: &startLine, NumLines: &numLines}
	rsp, err := fileToolSet.readFile(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, "single line content", rsp.Contents)
}

func TestFileTool_ReadFile_TrailingNewline(t *testing.T) {
	tempDir := t.TempDir()
	toolSet, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet, ok := toolSet.(*fileToolSet)
	assert.True(t, ok)
	// Create a file with trailing newline.
	testContent := "line1\nline2\n"
	testFile := filepath.Join(tempDir, "trailing.txt")
	err = os.WriteFile(testFile, []byte(testContent), 0644)
	assert.NoError(t, err)
	// Test reading with start line 1 and num lines 2.
	startLine := 1
	numLines := 2
	req := &readFileRequest{FileName: "trailing.txt", StartLine: &startLine, NumLines: &numLines}
	rsp, err := fileToolSet.readFile(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, "line1\nline2", rsp.Contents)
}

func TestFileTool_ReadFile_LimitExceed(t *testing.T) {
	tempDir := t.TempDir()
	toolSet, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet, ok := toolSet.(*fileToolSet)
	assert.True(t, ok)
	// Create a file with trailing newline.
	testContent := "line1\nline2\n"
	testFile := filepath.Join(tempDir, "trailing.txt")
	err = os.WriteFile(testFile, []byte(testContent), 0644)
	assert.NoError(t, err)
	// Test reading with start line 1 and num lines 10.
	startLine := 1
	numLines := 10
	req := &readFileRequest{FileName: "trailing.txt", StartLine: &startLine, NumLines: &numLines}
	rsp, err := fileToolSet.readFile(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, "line1\nline2\n", rsp.Contents)
}

func TestFileTool_ReadFile_DirTraversal(t *testing.T) {
	tempDir := t.TempDir()
	toolSet, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet, ok := toolSet.(*fileToolSet)
	assert.True(t, ok)
	// Test reading with start line 1 and num lines 10.
	req := &readFileRequest{FileName: "../"}
	_, err = fileToolSet.readFile(context.Background(), req)
	assert.Error(t, err)
}

func TestFileTool_ReadFile_ExceedMaxFileSize(t *testing.T) {
	tempDir := t.TempDir()
	toolSet, err := NewToolSet(WithBaseDir(tempDir), WithMaxFileSize(1))
	assert.NoError(t, err)
	fileToolSet, ok := toolSet.(*fileToolSet)
	assert.True(t, ok)
	// Create a file with 2 lines.
	testContent := "line1\nline2"
	testFile := filepath.Join(tempDir, "test.txt")
	err = os.WriteFile(testFile, []byte(testContent), 0644)
	assert.NoError(t, err)
	// Test reading the file.
	req := &readFileRequest{FileName: "test.txt"}
	_, err = fileToolSet.readFile(context.Background(), req)
	assert.Error(t, err)
}
