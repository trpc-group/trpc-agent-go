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

func TestFileTool_ReplaceContent_DefaultOneReplacement(t *testing.T) {
	// Prepare test file.
	tempDir := t.TempDir()
	fts := &fileToolSet{baseDir: tempDir}
	fileName := "dir/test.txt"
	fullPath := filepath.Join(tempDir, fileName)
	assert.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0755))
	assert.NoError(t, os.WriteFile(fullPath, []byte("foo foo bar"), 0644))
	// Replace.
	req := &replaceContentRequest{
		FileName:  fileName,
		OldString: "foo",
		NewString: "baz",
	}
	rsp, err := fts.replaceContent(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, tempDir, rsp.BaseDirectory)
	assert.Equal(t, fileName, rsp.FileName)
	content, err := os.ReadFile(fullPath)
	assert.NoError(t, err)
	assert.Equal(t, "baz foo bar", string(content))
}

func TestFileTool_ReplaceContent_ReplaceAll(t *testing.T) {
	// Prepare test file.
	tempDir := t.TempDir()
	fts := &fileToolSet{baseDir: tempDir}
	fileName := "test2.txt"
	fullPath := filepath.Join(tempDir, fileName)
	assert.NoError(t, os.WriteFile(fullPath, []byte("foo foo bar"), 0644))
	// Replace.
	req := &replaceContentRequest{
		FileName:        fileName,
		OldString:       "foo",
		NewString:       "baz",
		NumReplacements: -1,
	}
	rsp, err := fts.replaceContent(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, tempDir, rsp.BaseDirectory)
	assert.Equal(t, fileName, rsp.FileName)
	data, err := os.ReadFile(fullPath)
	assert.NoError(t, err)
	assert.Equal(t, "baz baz bar", string(data))
}

func TestFileTool_ReplaceContent_MaxExceedsCount(t *testing.T) {
	// Prepare test file.
	tempDir := t.TempDir()
	fts := &fileToolSet{baseDir: tempDir}
	fileName := "test3.txt"
	fullPath := filepath.Join(tempDir, fileName)
	assert.NoError(t, os.WriteFile(fullPath, []byte("abc abc"), 0644))
	// Replace.
	req := &replaceContentRequest{
		FileName:        fileName,
		OldString:       "abc",
		NewString:       "x",
		NumReplacements: 5,
	}
	rsp, err := fts.replaceContent(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, tempDir, rsp.BaseDirectory)
	assert.Equal(t, fileName, rsp.FileName)
	content, err := os.ReadFile(fullPath)
	assert.NoError(t, err)
	assert.Equal(t, "x x", string(content))
}

func TestFileTool_ReplaceContent_NotFound(t *testing.T) {
	// Prepare test file.
	tempDir := t.TempDir()
	fts := &fileToolSet{baseDir: tempDir}
	fileName := "notfound.txt"
	fullPath := filepath.Join(tempDir, fileName)
	assert.NoError(t, os.WriteFile(fullPath, []byte("hello"), 0644))
	// Replace.
	req := &replaceContentRequest{
		FileName:        fileName,
		OldString:       "xxx",
		NewString:       "yyy",
		NumReplacements: -1,
	}
	rsp, err := fts.replaceContent(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, tempDir, rsp.BaseDirectory)
	assert.Equal(t, fileName, rsp.FileName)
	data, err := os.ReadFile(fullPath)
	assert.NoError(t, err)
	assert.Equal(t, "hello", string(data))
}

func TestFileTool_ReplaceContent_OldEqualsNew(t *testing.T) {
	// Prepare test file.
	tempDir := t.TempDir()
	fts := &fileToolSet{baseDir: tempDir}
	fileName := "equal.txt"
	fullPath := filepath.Join(tempDir, fileName)
	assert.NoError(t, os.WriteFile(fullPath, []byte("foo bar"), 0644))
	// Replace.
	req := &replaceContentRequest{
		FileName:        fileName,
		OldString:       "foo",
		NewString:       "foo",
		NumReplacements: -1,
	}
	rsp, err := fts.replaceContent(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, tempDir, rsp.BaseDirectory)
	assert.Equal(t, fileName, rsp.FileName)
	data, err := os.ReadFile(fullPath)
	assert.NoError(t, err)
	assert.Equal(t, "foo bar", string(data))
}

func TestFileTool_ReplaceContent_TargetIsDirectory(t *testing.T) {
	// Prepare test file.
	tempDir := t.TempDir()
	fts := &fileToolSet{baseDir: tempDir}
	dirName := "somedir"
	assert.NoError(t, os.MkdirAll(filepath.Join(tempDir, dirName), 0755))
	// Replace.
	req := &replaceContentRequest{
		FileName:        dirName,
		OldString:       "a",
		NewString:       "b",
		NumReplacements: -1,
	}
	_, err := fts.replaceContent(context.Background(), req)
	assert.Error(t, err)
}

func TestFileTool_ReplaceContent_InvalidAbsolutePath(t *testing.T) {
	// Prepare test file.
	tempDir := t.TempDir()
	fts := &fileToolSet{baseDir: tempDir}
	absPath := filepath.Join(tempDir, "abs.txt")
	// Replace.
	req := &replaceContentRequest{
		FileName:        absPath, // absolute path is not allowed by resolvePath
		OldString:       "a",
		NewString:       "b",
		NumReplacements: 1,
	}
	_, err := fts.replaceContent(context.Background(), req)
	assert.Error(t, err)
}

func TestFileTool_ReplaceContent_RejectsFileRef(t *testing.T) {
	tempDir := t.TempDir()
	fts := &fileToolSet{baseDir: tempDir}
	_, err := fts.replaceContent(context.Background(), &replaceContentRequest{
		FileName:        "workspace://out/a.txt",
		OldString:       "a",
		NewString:       "b",
		NumReplacements: 1,
	})
	assert.Error(t, err)
}

func TestFileTool_ReplaceContent_PreservePermissions(t *testing.T) {
	// Prepare test file.
	tempDir := t.TempDir()
	fts := &fileToolSet{baseDir: tempDir}
	fileName := "perm.txt"
	fullPath := filepath.Join(tempDir, fileName)
	mode := os.FileMode(0600)
	assert.NoError(t, os.WriteFile(fullPath, []byte("aaa bbb aaa"), mode))
	// Replace.
	req := &replaceContentRequest{
		FileName:        fileName,
		OldString:       "aaa",
		NewString:       "xxx",
		NumReplacements: -1,
	}
	rsp, err := fts.replaceContent(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, tempDir, rsp.BaseDirectory)
	assert.Equal(t, fileName, rsp.FileName)
	stat, serr := os.Stat(fullPath)
	assert.NoError(t, serr)
	assert.Equal(t, mode, stat.Mode().Perm())
	data, rerr := os.ReadFile(fullPath)
	assert.NoError(t, rerr)
	assert.Equal(t, "xxx bbb xxx", string(data))
}

func TestFileTool_MultiLine(t *testing.T) {
	// Prepare test file.
	tempDir := t.TempDir()
	fts := &fileToolSet{baseDir: tempDir}
	fileName := "multiline.txt"
	fullPath := filepath.Join(tempDir, fileName)
	mode := os.FileMode(0600)
	assert.NoError(
		t,
		os.WriteFile(fullPath, []byte("123aaa\nbbb\naaa456"), mode),
	)
	// Replace.
	req := &replaceContentRequest{
		FileName:        fileName,
		OldString:       "aaa\nbbb\naaa",
		NewString:       "xxx\nxxx\nxxx",
		NumReplacements: -1,
	}
	rsp, err := fts.replaceContent(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, tempDir, rsp.BaseDirectory)
	assert.Equal(t, fileName, rsp.FileName)
	data, err := os.ReadFile(fullPath)
	assert.NoError(t, err)
	assert.Equal(t, "123xxx\nxxx\nxxx456", string(data))
}

func TestFileTool_ReplaceContent_DirTraversal(t *testing.T) {
	tempDir := t.TempDir()
	toolSet, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet, ok := toolSet.(*fileToolSet)
	assert.True(t, ok)
	req := &replaceContentRequest{FileName: "../", OldString: "a"}
	_, err = fileToolSet.replaceContent(context.Background(), req)
	assert.Error(t, err)
}

func TestFileTool_ReplaceContent_NotExist(t *testing.T) {
	tempDir := t.TempDir()
	toolSet, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet, ok := toolSet.(*fileToolSet)
	assert.True(t, ok)
	req := &replaceContentRequest{FileName: "notexist.txt", OldString: "a"}
	_, err = fileToolSet.replaceContent(context.Background(), req)
	assert.Error(t, err)
}
