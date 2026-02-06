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

func TestFileTool_listFile_WorkspaceRef(t *testing.T) {
	tempDir := t.TempDir()
	set, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)

	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)
	toolcache.StoreSkillRunOutputFiles(inv, []codeexecutor.File{
		{
			Name:     ".",
			Content:  "ignored",
			MIMEType: "text/plain",
		},
		{
			Name:     "root.txt",
			Content:  "root",
			MIMEType: "text/plain",
		},
		{
			Name:     "out/a.txt",
			Content:  "a",
			MIMEType: "text/plain",
		},
		{
			Name:     "out/sub/b.txt",
			Content:  "b",
			MIMEType: "text/plain",
		},
	})

	rsp, err := fts.listFile(ctx, &listFileRequest{
		Path: "workspace://",
	})
	assert.NoError(t, err)
	assert.Equal(t, "workspace://", rsp.Path)
	assert.ElementsMatch(t, []string{"workspace://root.txt"}, rsp.Files)
	assert.ElementsMatch(t, []string{"workspace://out"}, rsp.Folders)

	rsp, err = fts.listFile(ctx, &listFileRequest{
		Path: "workspace://out",
	})
	assert.NoError(t, err)
	assert.Equal(t, "workspace://out", rsp.Path)
	assert.ElementsMatch(t, []string{"workspace://out/a.txt"}, rsp.Files)
	assert.ElementsMatch(t, []string{"workspace://out/sub"}, rsp.Folders)
}

func TestFileTool_listFile_ArtifactUnsupported(t *testing.T) {
	set, err := NewToolSet(WithBaseDir(t.TempDir()))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)

	_, err = fts.listFile(context.Background(), &listFileRequest{
		Path: "artifact://x.txt",
	})
	assert.Error(t, err)
}

func TestFileTool_listFile_ParseError(t *testing.T) {
	set, err := NewToolSet(WithBaseDir(t.TempDir()))
	assert.NoError(t, err)
	fts := set.(*fileToolSet)

	_, err = fts.listFile(context.Background(), &listFileRequest{
		Path: "unknown://x",
	})
	assert.Error(t, err)
}

func TestFileTool_listFile_WithSize(t *testing.T) {
	// Create a temporary directory for testing.
	tempDir := t.TempDir()
	fileToolSet := &fileToolSet{baseDir: tempDir}
	// Create some test files with known content sizes.
	testFiles := map[string]string{
		"file1.txt": "hello",        // 5 bytes
		"file2.go":  "package main", // 12 bytes
		"README.md": "# Readme",     // 8 bytes
	}
	for fileName, content := range testFiles {
		filePath := filepath.Join(tempDir, fileName)
		err := os.WriteFile(filePath, []byte(content), 0644)
		assert.NoError(t, err)
	}
	// Create a subdirectory (should not appear in FilesWithSize).
	err := os.MkdirAll(filepath.Join(tempDir, "subdir"), 0755)
	assert.NoError(t, err)

	// Test listing files with WithSize = true.
	req := &listFileRequest{WithSize: true}
	rsp, err := fileToolSet.listFile(context.Background(), req)
	assert.NoError(t, err)
	// Check that the response contains the expected base directory.
	assert.Equal(t, tempDir, rsp.BaseDirectory)
	assert.Equal(t, "", rsp.Path)
	// Check that the number of files matches.
	assert.Equal(t, len(testFiles), len(rsp.Files))
	// Check that FilesWithSize is populated.
	assert.Equal(t, len(testFiles), len(rsp.FilesWithSize))
	// Check that file sizes are correct.
	expectedSizes := map[string]int64{
		"file1.txt": 5,
		"file2.go":  12,
		"README.md": 8,
	}
	for _, fi := range rsp.FilesWithSize {
		expectedSize, ok := expectedSizes[fi.Name]
		assert.True(t, ok, "unexpected file in FilesWithSize: %s", fi.Name)
		assert.Equal(t, expectedSize, fi.Size, "size mismatch for file: %s", fi.Name)
	}
}

func TestFileTool_listFile_FallbackToWorkspaceCache(t *testing.T) {
	tempDir := t.TempDir()
	set, err := NewToolSet(WithBaseDir(tempDir))
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
			Name:     "out/sub/b.txt",
			Content:  "b",
			MIMEType: "text/plain",
		},
	})

	rsp, err := fts.listFile(ctx, &listFileRequest{Path: "out"})
	assert.NoError(t, err)
	assert.Equal(t, "workspace://out", rsp.Path)
	assert.ElementsMatch(t, []string{"workspace://out/a.txt"}, rsp.Files)
	assert.ElementsMatch(t, []string{"workspace://out/sub"}, rsp.Folders)
}
