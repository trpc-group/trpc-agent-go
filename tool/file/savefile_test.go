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
)

func TestFileTool_SaveFile(t *testing.T) {
	// Create a temporary directory for testing.
	tempDir := t.TempDir()
	fileToolSet := &fileToolSet{
		baseDir:        tempDir,
		createDirMode:  defaultCreateDirMode,
		createFileMode: defaultCreateFileMode,
	}
	// Test saving a file.
	req := &saveFileRequest{
		Contents:  "Hello, World!",
		FileName:  "test.txt",
		Overwrite: false,
	}
	rsp, err := fileToolSet.saveFile(context.Background(), req)
	assert.NoError(t, err)
	// Check that the response contains the expected file name.
	assert.Equal(t, "test.txt", rsp.FileName)
	// Verify the file was created.
	filePath := filepath.Join(tempDir, "test.txt")
	assert.FileExists(t, filePath)
	// Read the file to verify content.
	content, err := os.ReadFile(filePath)
	assert.NoError(t, err)
	assert.Equal(t, "Hello, World!", string(content))
}

func TestFileTool_SaveFile_Overwrite_True(t *testing.T) {
	// Create a temporary directory for testing.
	tempDir := t.TempDir()
	fileToolSet := &fileToolSet{
		baseDir:        tempDir,
		createDirMode:  defaultCreateDirMode,
		createFileMode: defaultCreateFileMode,
	}
	// Test saving a file.
	req := &saveFileRequest{
		Contents:  "Hello, World!",
		FileName:  "test.txt",
		Overwrite: true,
	}
	for i := 0; i < 2; i++ {
		// First time save the file.
		rsp, err := fileToolSet.saveFile(context.Background(), req)
		assert.NoError(t, err)
		// Check that the response contains the expected file name.
		assert.Equal(t, "test.txt", rsp.FileName)
		// Verify the file was created.
		filePath := filepath.Join(tempDir, "test.txt")
		assert.FileExists(t, filePath)
		// Read the file to verify content.
		content, err := os.ReadFile(filePath)
		assert.NoError(t, err)
		assert.Equal(t, "Hello, World!", string(content))
	}
}

func TestFileTool_SaveFile_Overwrite_False(t *testing.T) {
	// Create a temporary directory for testing.
	tempDir := t.TempDir()
	fileToolSet := &fileToolSet{
		baseDir:        tempDir,
		createDirMode:  defaultCreateDirMode,
		createFileMode: defaultCreateFileMode,
	}
	// Test saving a file.
	req := &saveFileRequest{
		Contents:  "Hello, World!",
		FileName:  "test.txt",
		Overwrite: false,
	}
	for i := 0; i < 2; i++ {
		// First time save the file.
		rsp, err := fileToolSet.saveFile(context.Background(), req)
		if i == 0 {
			assert.NoError(t, err)
		} else {
			assert.Error(t, err)
		}
		// Check that the response contains the expected file name.
		assert.Equal(t, "test.txt", rsp.FileName)
		// Verify the file was created.
		filePath := filepath.Join(tempDir, "test.txt")
		assert.FileExists(t, filePath)
		// Read the file to verify content.
		content, err := os.ReadFile(filePath)
		assert.NoError(t, err)
		assert.Equal(t, "Hello, World!", string(content))
	}
}

func TestFileTool_SaveFile_EmptyFileName(t *testing.T) {
	// Create a temporary directory for testing.
	tempDir := t.TempDir()
	fileToolSet := &fileToolSet{baseDir: tempDir}
	// Test saving with empty file name.
	req := &saveFileRequest{
		Contents:  "Hello, World!",
		FileName:  "",
		Overwrite: true,
	}
	_, err := fileToolSet.saveFile(context.Background(), req)
	assert.Error(t, err)
}

func TestFileTool_SaveFile_RejectsFileRef(t *testing.T) {
	tempDir := t.TempDir()
	fts := &fileToolSet{baseDir: tempDir}
	_, err := fts.saveFile(context.Background(), &saveFileRequest{
		Contents:  "x",
		FileName:  "workspace://out/a.txt",
		Overwrite: true,
	})
	assert.Error(t, err)
}

func TestFileTool_SaveFile_CustomPermissionsForFile(t *testing.T) {
	// Create a temporary directory for testing.
	tempDir := t.TempDir()
	// Test with custom permissions
	customFileMode := os.FileMode(0600) // rw-------
	fileToolSet := &fileToolSet{
		baseDir:        tempDir,
		createFileMode: customFileMode,
	}
	// Test saving a file with custom permissions.
	req := &saveFileRequest{
		Contents:  "Custom permissions test",
		FileName:  "custom_perms.txt",
		Overwrite: false,
	}
	rsp, err := fileToolSet.saveFile(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, "custom_perms.txt", rsp.FileName)
	// Verify the file was created with custom permissions.
	filePath := filepath.Join(tempDir, "custom_perms.txt")
	assert.FileExists(t, filePath)
	// Check file permissions (only check the permission bits, not the full mode)
	stat, err := os.Stat(filePath)
	assert.NoError(t, err)
	assert.Equal(t, customFileMode, stat.Mode().Perm())
	// Read the file to verify content.
	content, err := os.ReadFile(filePath)
	assert.NoError(t, err)
	assert.Equal(t, "Custom permissions test", string(content))
}

func TestFileTool_SaveFile_WithDirectory(t *testing.T) {
	tempDir := t.TempDir()
	fileToolSet := &fileToolSet{
		baseDir:        tempDir,
		createDirMode:  defaultCreateDirMode,
		createFileMode: defaultCreateFileMode,
	}
	path := filepath.Join("testdir", "testfile.txt")
	// Try to save to the directory path.
	req := &saveFileRequest{
		Contents:  "test content",
		FileName:  path,
		Overwrite: false,
	}
	rsp, err := fileToolSet.saveFile(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, path, rsp.FileName)
	assert.FileExists(t, filepath.Join(tempDir, path))
	content, err := os.ReadFile(filepath.Join(tempDir, path))
	assert.NoError(t, err)
	assert.Equal(t, "test content", string(content))
}

func TestFileTool_SaveFile_CustomPermissionsForDirectory(t *testing.T) {
	// Create a temporary directory for testing.
	tempDir := t.TempDir()
	// Test with custom permissions
	customDirMode := os.FileMode(0700)  // rwx------
	customFileMode := os.FileMode(0600) // rw-------
	fileToolSet := &fileToolSet{
		baseDir:        tempDir,
		createDirMode:  customDirMode,
		createFileMode: customFileMode,
	}
	// Test saving a file with custom permissions.
	req := &saveFileRequest{
		Contents:  "Custom permissions test",
		FileName:  "testdir/custom_perms.txt",
		Overwrite: false,
	}
	rsp, err := fileToolSet.saveFile(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, "testdir/custom_perms.txt", rsp.FileName)
	// Verify the file was created with custom permissions.
	filePath := filepath.Join(tempDir, "testdir", "custom_perms.txt")
	assert.FileExists(t, filePath)
	// Check file permissions (only check the permission bits, not the full mode)
	stat, err := os.Stat(filePath)
	assert.NoError(t, err)
	assert.Equal(t, customFileMode, stat.Mode().Perm())
	// Read the file to verify content.
	content, err := os.ReadFile(filePath)
	assert.NoError(t, err)
	assert.Equal(t, "Custom permissions test", string(content))
	// Check directory permissions.
	dirPath := filepath.Join(tempDir, "testdir")
	stat, err = os.Stat(dirPath)
	assert.NoError(t, err)
	assert.Equal(t, customDirMode, stat.Mode().Perm())
}

func TestFileTool_SaveFile_DirTraversal(t *testing.T) {
	tempDir := t.TempDir()
	toolSet, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet, ok := toolSet.(*fileToolSet)
	assert.True(t, ok)
	// Test saving a file.
	req := &saveFileRequest{FileName: "../"}
	_, err = fileToolSet.saveFile(context.Background(), req)
	assert.Error(t, err)
}
