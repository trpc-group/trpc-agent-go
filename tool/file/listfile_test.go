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

func TestFileTool_listFile(t *testing.T) {
	// Create a temporary directory for testing.
	tempDir := t.TempDir()
	fileToolSet := &fileToolSet{baseDir: tempDir}
	// Create some test files.
	testFiles := []string{"file1.txt", "file2.go", "README.md"}
	for _, fileName := range testFiles {
		filePath := filepath.Join(tempDir, fileName)
		err := os.WriteFile(filePath, []byte("test content"), 0644)
		assert.NoError(t, err)
	}
	// Test listing files in base directory.
	req := &listFileRequest{}
	rsp, err := fileToolSet.listFile(context.Background(), req)
	assert.NoError(t, err)
	// Check that the response contains the expected base directory.
	assert.Equal(t, tempDir, rsp.BaseDirectory)
	assert.Equal(t, "", rsp.Path)
	// Check that the number of files matches.
	assert.Equal(t, len(testFiles), len(rsp.Files))
	// Check that all test files are in the response.
	assert.ElementsMatch(t, testFiles, rsp.Files)
	// Check that there are no folders in root.
	assert.Equal(t, 0, len(rsp.Folders))
}

func TestFileTool_listFile_Subdirectory(t *testing.T) {
	// Create a temporary directory for testing.
	tempDir := t.TempDir()
	fileToolSet := &fileToolSet{baseDir: tempDir}
	// Create a subdirectory with files.
	subDir := filepath.Join(tempDir, "subdir")
	err := os.MkdirAll(subDir, 0755)
	assert.NoError(t, err)
	// Create some test files in subdirectory.
	testFiles := []string{"file1.txt", "file2.go", "README.md"}
	for _, fileName := range testFiles {
		filePath := filepath.Join(subDir, fileName)
		err := os.WriteFile(filePath, []byte("test content"), 0644)
		assert.NoError(t, err)
	}
	// Test listing files in subdirectory.
	req := &listFileRequest{Path: "subdir"}
	rsp, err := fileToolSet.listFile(context.Background(), req)
	assert.NoError(t, err)
	// Check that the response contains the expected base directory.
	assert.Equal(t, tempDir, rsp.BaseDirectory)
	assert.Equal(t, "subdir", rsp.Path)
	// Check that the number of files matches.
	assert.Equal(t, len(testFiles), len(rsp.Files))
	// Check that all test files are in the response.
	assert.ElementsMatch(t, testFiles, rsp.Files)
	// Check that there are no folders in subdirectory.
	assert.Equal(t, 0, len(rsp.Folders))
}

func TestFileTool_listFile_WithFolders(t *testing.T) {
	// Create a temporary directory for testing.
	tempDir := t.TempDir()
	fileToolSet := &fileToolSet{baseDir: tempDir}
	// Create some test files.
	testFiles := []string{"file1.txt", "file2.go", "README.md"}
	for _, fileName := range testFiles {
		filePath := filepath.Join(tempDir, fileName)
		err := os.WriteFile(filePath, []byte("test content"), 0644)
		assert.NoError(t, err)
	}
	// Create some test folders.
	testFolders := []string{"docs", "src", "tests"}
	for _, folderName := range testFolders {
		folderPath := filepath.Join(tempDir, folderName)
		err := os.MkdirAll(folderPath, 0755)
		assert.NoError(t, err)
	}
	// Test listing files and folders in base directory.
	req := &listFileRequest{}
	rsp, err := fileToolSet.listFile(context.Background(), req)
	assert.NoError(t, err)
	// Check that the response contains the expected base directory.
	assert.Equal(t, tempDir, rsp.BaseDirectory)
	assert.Equal(t, "", rsp.Path)
	// Check that the number of files matches.
	assert.Equal(t, len(testFiles), len(rsp.Files))
	// Check that all test files are in the response.
	assert.ElementsMatch(t, testFiles, rsp.Files)
	// Check that the number of folders matches.
	assert.Equal(t, len(testFolders), len(rsp.Folders))
	// Check that all test folders are in the response.
	assert.ElementsMatch(t, testFolders, rsp.Folders)
}

func TestFileTool_listFile_DirTraversal(t *testing.T) {
	tempDir := t.TempDir()
	set, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet, ok := set.(*fileToolSet)
	assert.True(t, ok)
	// Test listing files in subdirectory.
	req := &listFileRequest{Path: "../"}
	_, err = fileToolSet.listFile(context.Background(), req)
	assert.Error(t, err)
}

func TestFileTool_listFile_NotExist(t *testing.T) {
	tempDir := t.TempDir()
	set, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet, ok := set.(*fileToolSet)
	assert.True(t, ok)
	// Test listing files in subdirectory.
	req := &listFileRequest{Path: "notexist"}
	_, err = fileToolSet.listFile(context.Background(), req)
	assert.Error(t, err)
}

func TestFileTool_listFile_IsFile(t *testing.T) {
	tempDir := t.TempDir()
	set, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet, ok := set.(*fileToolSet)
	assert.True(t, ok)
	// Create a file.
	file := filepath.Join(tempDir, "file.txt")
	err = os.WriteFile(file, []byte("test content"), 0644)
	assert.NoError(t, err)
	// Test listing files in subdirectory.
	req := &listFileRequest{Path: "file.txt"}
	_, err = fileToolSet.listFile(context.Background(), req)
	assert.Error(t, err)
}
