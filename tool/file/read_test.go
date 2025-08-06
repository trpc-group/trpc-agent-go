//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
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
	fileToolSet := &fileToolSet{baseDir: tempDir}
	// Create a test file first.
	testContent := "Test content for reading"
	testFile := filepath.Join(tempDir, "read_test.txt")
	err := os.WriteFile(testFile, []byte(testContent), 0644)
	assert.NoError(t, err)
	// Test reading the file.
	req := readFileRequest{FileName: "read_test.txt"}
	rsp, err := fileToolSet.readFile(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, testContent, rsp.Contents)
}

func TestFileTool_ReadFile_EmptyFileName(t *testing.T) {
	// Create a temporary directory for testing.
	tempDir := t.TempDir()
	fileToolSet := &fileToolSet{baseDir: tempDir}
	// Test reading with empty file name.
	req := readFileRequest{FileName: ""}
	_, err := fileToolSet.readFile(context.Background(), req)
	assert.Error(t, err)
}

func TestFileTool_ReadFile_NonExistFile(t *testing.T) {
	// Create a temporary directory for testing.
	tempDir := t.TempDir()
	fileToolSet := &fileToolSet{baseDir: tempDir}
	// Test reading with non-existent file name.
	req := readFileRequest{FileName: "non_existent.txt"}
	_, err := fileToolSet.readFile(context.Background(), req)
	assert.Error(t, err)
}

func TestFileTool_ReadFile_Empty(t *testing.T) {
	// Create a temporary directory for testing.
	tempDir := t.TempDir()
	fileToolSet := &fileToolSet{baseDir: tempDir}
	// Create a test file first.
	testFile := filepath.Join(tempDir, "read_test.txt")
	err := os.WriteFile(testFile, []byte{}, 0644)
	assert.NoError(t, err)
	// Test reading with empty file content.
	req := readFileRequest{FileName: "read_test.txt"}
	rsp, err := fileToolSet.readFile(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, "", rsp.Contents)
}

func TestFileTool_ReadFile_Directory(t *testing.T) {
	tempDir := t.TempDir()
	fileToolSet := &fileToolSet{baseDir: tempDir}
	// Create a directory
	dirPath := filepath.Join(tempDir, "testdir")
	err := os.MkdirAll(dirPath, 0755)
	assert.NoError(t, err)
	// Try to read the directory path
	req := readFileRequest{FileName: "testdir"}
	_, err = fileToolSet.readFile(context.Background(), req)
	assert.Error(t, err)
}
