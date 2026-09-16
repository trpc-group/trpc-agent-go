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
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	artifactinmemory "trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolcache"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	skilltool "trpc.group/trpc-go/trpc-agent-go/tool/skill"
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

func TestFileTool_ReadFile_AbsolutePathUnderExtraReadRoot(t *testing.T) {
	base := t.TempDir()
	extra := t.TempDir()
	fileName := filepath.Join(extra, "derived.json")
	assert.NoError(t, os.WriteFile(fileName, []byte(`{"ok":true}`), 0o644))

	toolSet, err := NewToolSet(
		WithBaseDir(base),
		WithReadOnlyDirs(extra),
	)
	assert.NoError(t, err)
	fileToolSet := toolSet.(*fileToolSet)

	rsp, err := fileToolSet.readFile(
		context.Background(),
		&readFileRequest{FileName: fileName},
	)
	assert.NoError(t, err)
	assert.Equal(t, `{"ok":true}`, rsp.Contents)

	_, err = fileToolSet.readFile(
		context.Background(),
		&readFileRequest{FileName: filepath.Join(t.TempDir(), "x.txt")},
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "outside base_directory and configured read-only roots")
}

func TestFileTool_ReadFile_AbsolutePathUnderBaseDir(t *testing.T) {
	base := t.TempDir()
	fileName := filepath.Join(base, "derived.json")
	assert.NoError(t, os.WriteFile(fileName, []byte(`{"ok":true}`), 0o644))

	toolSet, err := NewToolSet(WithBaseDir(base))
	assert.NoError(t, err)
	fileToolSet := toolSet.(*fileToolSet)

	rsp, err := fileToolSet.readFile(
		context.Background(),
		&readFileRequest{FileName: fileName},
	)
	assert.NoError(t, err)
	assert.Equal(t, `{"ok":true}`, rsp.Contents)
}

func TestFileTool_ReadFile_BlocksSymlinkEscapeFromExtraReadRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on windows")
	}
	base := t.TempDir()
	extra := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	assert.NoError(t, os.WriteFile(secret, []byte("secret"), 0o644))
	link := filepath.Join(extra, "link.txt")
	assert.NoError(t, os.Symlink(secret, link))

	toolSet, err := NewToolSet(
		WithBaseDir(base),
		WithReadOnlyDirs(extra),
	)
	assert.NoError(t, err)
	fileToolSet := toolSet.(*fileToolSet)

	rsp, err := fileToolSet.readFile(
		context.Background(),
		&readFileRequest{FileName: link},
	)
	assert.Error(t, err)
	assert.Empty(t, rsp.Contents)
	assert.Contains(t, err.Error(), "outside base_directory and configured read-only roots")
}

func TestFileTool_ReadFile_BlocksSymlinkEscapeFromBaseDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on windows")
	}
	base := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	assert.NoError(t, os.WriteFile(secret, []byte("secret"), 0o644))
	link := filepath.Join(base, "link.txt")
	assert.NoError(t, os.Symlink(secret, link))

	toolSet, err := NewToolSet(WithBaseDir(base))
	assert.NoError(t, err)
	fileToolSet := toolSet.(*fileToolSet)

	rsp, err := fileToolSet.readFile(
		context.Background(),
		&readFileRequest{FileName: link},
	)
	assert.Error(t, err)
	assert.Empty(t, rsp.Contents)
	assert.Contains(t, err.Error(), "outside base_directory and configured read-only roots")
}

func TestFileTool_ReadFile_NilRequest(t *testing.T) {
	tempDir := t.TempDir()
	toolSet, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet := toolSet.(*fileToolSet)

	rsp, err := fileToolSet.readFile(context.Background(), nil)
	assert.EqualError(t, err, "request cannot be nil")
	assert.NotNil(t, rsp)
	assert.Equal(t, "Error: request cannot be nil", rsp.Message)
}

func TestValidateReadFileRequest_Nil(t *testing.T) {
	assert.Error(t, validateReadFileRequest(nil))
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

func TestFileTool_ReadFile_NonExistFileIncludesHint(t *testing.T) {
	tempDir := t.TempDir()
	toolSet, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet, ok := toolSet.(*fileToolSet)
	assert.True(t, ok)

	assert.NoError(
		t,
		os.Mkdir(filepath.Join(tempDir, "nested"), 0o755),
	)
	assert.NoError(
		t,
		os.WriteFile(
			filepath.Join(tempDir, "README.md"),
			[]byte("hello"),
			0o644,
		),
	)

	rsp, err := fileToolSet.readFile(
		context.Background(),
		&readFileRequest{FileName: "missing.txt"},
	)
	assert.Error(t, err)
	assert.NotNil(t, rsp)
	assert.Contains(t, rsp.Message, missingFileBaseDirPrefix+tempDir)
	assert.Contains(t, rsp.Message, "README.md")
	assert.Contains(
		t,
		rsp.Message,
		"nested"+missingFileDirectorySuffix,
	)
	assert.Contains(t, rsp.Message, missingFileRecoveryGuidance)
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
	req := &readFileRequest{
		FileName:  "multiline.txt",
		StartLine: &startLine,
		NumLines:  &numLines,
	}
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
	req := &readFileRequest{
		FileName:  "single.txt",
		StartLine: &startLine,
		NumLines:  &numLines,
	}
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
	req := &readFileRequest{
		FileName:  "trailing.txt",
		StartLine: &startLine,
		NumLines:  &numLines,
	}
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
	req := &readFileRequest{
		FileName:  "trailing.txt",
		StartLine: &startLine,
		NumLines:  &numLines,
	}
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

func TestFileTool_ReadFile_FromRef_TooLarge(t *testing.T) {
	tempDir := t.TempDir()
	toolSet, err := NewToolSet(WithBaseDir(tempDir), WithMaxFileSize(1))
	assert.NoError(t, err)
	fileToolSet := toolSet.(*fileToolSet)

	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)
	toolcache.StoreSkillRunOutputFiles(inv, []codeexecutor.File{
		{
			Name:     "out/a.txt",
			Content:  "hi",
			MIMEType: "text/plain",
		},
	})

	_, err = fileToolSet.readFile(ctx, &readFileRequest{
		FileName: "workspace://out/a.txt",
	})
	assert.Error(t, err)
}

func TestFileTool_ReadFile_FromRef_EmptyFile(t *testing.T) {
	tempDir := t.TempDir()
	toolSet, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet := toolSet.(*fileToolSet)

	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)
	toolcache.StoreSkillRunOutputFiles(inv, []codeexecutor.File{
		{
			Name:     "out/empty.txt",
			Content:  "",
			MIMEType: "text/plain",
		},
	})

	rsp, err := fileToolSet.readFile(ctx, &readFileRequest{
		FileName: "workspace://out/empty.txt",
	})
	assert.NoError(t, err)
	assert.Empty(t, rsp.Contents)
	assert.Contains(t, rsp.Message, "file is empty")
}

func TestFileTool_ReadFile_FromRef_NonTextFile(t *testing.T) {
	tempDir := t.TempDir()
	toolSet, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet := toolSet.(*fileToolSet)

	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)

	const (
		outAPng     = "out/a.png"
		refAPng     = "workspace://out/a.png"
		pngContent  = "\x89PNG\r\n\x1a\n"
		pngMIMEType = "image/png"
	)
	toolcache.StoreSkillRunOutputFiles(inv, []codeexecutor.File{
		{
			Name:     outAPng,
			Content:  pngContent,
			MIMEType: pngMIMEType,
		},
	})

	rsp, err := fileToolSet.readFile(ctx, &readFileRequest{
		FileName: refAPng,
	})
	assert.Error(t, err)
	assert.NotNil(t, rsp)
	assert.Empty(t, rsp.Contents)
	assert.Contains(t, rsp.Message, pngMIMEType)
}

func TestFileTool_ReadFile_NonTextFileFromDisk(t *testing.T) {
	tempDir := t.TempDir()
	toolSet, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet := toolSet.(*fileToolSet)

	const (
		fileName    = "binary.png"
		pngContent  = "\x89PNG\r\n\x1a\n"
		pngMIMEType = "image/png"
	)
	err = os.WriteFile(
		filepath.Join(tempDir, fileName),
		[]byte(pngContent),
		0644,
	)
	assert.NoError(t, err)

	rsp, err := fileToolSet.readFile(context.Background(), &readFileRequest{
		FileName: fileName,
	})
	assert.Error(t, err)
	assert.NotNil(t, rsp)
	assert.Empty(t, rsp.Contents)
	assert.Contains(t, rsp.Message, pngMIMEType)
}

func TestFileTool_ReadFile_FromRef_InvalidUTF8(t *testing.T) {
	tempDir := t.TempDir()
	toolSet, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet := toolSet.(*fileToolSet)

	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)

	const (
		outATxt       = "out/a.txt"
		refATxt       = "workspace://out/a.txt"
		mimeTextPlain = "text/plain"
		invalidByte   = 0xff
	)
	toolcache.StoreSkillRunOutputFiles(inv, []codeexecutor.File{
		{
			Name:     outATxt,
			Content:  "before" + string([]byte{invalidByte}) + "after",
			MIMEType: mimeTextPlain,
		},
	})

	rsp, err := fileToolSet.readFile(ctx, &readFileRequest{
		FileName: refATxt,
	})
	assert.NoError(t, err)
	assert.NotNil(t, rsp)
	assert.Equal(t, "before\uFFFDafter", rsp.Contents)
	assert.Contains(
		t,
		rsp.Message,
		"Successfully read workspace://out/a.txt from workspace://, "+
			"start line: 1, end line: 1, total lines: 1"+
			" (invalid UTF-8 replaced with U+FFFD)",
	)
}

func TestFileTool_ReadFile_InvalidUTF8FromDisk(t *testing.T) {
	tempDir := t.TempDir()
	toolSet, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet := toolSet.(*fileToolSet)

	const (
		fileName    = "invalid.txt"
		sniffLen    = 512
		invalidByte = byte(0xff)
	)

	prefix := make([]byte, sniffLen)
	for i := range prefix {
		prefix[i] = 'a'
	}
	content := append(prefix, invalidByte)
	err = os.WriteFile(
		filepath.Join(tempDir, fileName),
		content,
		0644,
	)
	assert.NoError(t, err)

	rsp, err := fileToolSet.readFile(
		context.Background(),
		&readFileRequest{FileName: fileName},
	)
	assert.NoError(t, err)
	assert.NotNil(t, rsp)
	assert.Equal(t, string(prefix)+"\uFFFD", rsp.Contents)
	assert.Equal(
		t,
		"Successfully read invalid.txt, start line: 1, end line: 1, "+
			"total lines: 1 (invalid UTF-8 replaced with U+FFFD)",
		rsp.Message,
	)
}

func TestFileTool_ReadFile_FromCache_EmptyFile(t *testing.T) {
	tempDir := t.TempDir()
	toolSet, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet := toolSet.(*fileToolSet)

	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)
	toolcache.StoreSkillRunOutputFiles(inv, []codeexecutor.File{
		{
			Name:     "out/empty.txt",
			Content:  "",
			MIMEType: "text/plain",
		},
	})

	rsp, err := fileToolSet.readFile(ctx, &readFileRequest{
		FileName: "out/empty.txt",
	})
	assert.NoError(t, err)
	assert.Empty(t, rsp.Contents)
	assert.Contains(t, rsp.Message, "file is empty")
}

func TestFileTool_ReadFile_FromCache_InvalidUTF8(t *testing.T) {
	tempDir := t.TempDir()
	toolSet, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet := toolSet.(*fileToolSet)

	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)

	const (
		outATxt       = "out/a.txt"
		mimeTextPlain = "text/plain"
		invalidByte   = 0xff
	)
	toolcache.StoreSkillRunOutputFiles(inv, []codeexecutor.File{
		{
			Name:     outATxt,
			Content:  "line one" + string([]byte{invalidByte}) + "\nline two",
			MIMEType: mimeTextPlain,
		},
	})

	rsp, err := fileToolSet.readFile(ctx, &readFileRequest{
		FileName: outATxt,
	})
	assert.NoError(t, err)
	assert.NotNil(t, rsp)
	assert.Equal(t, "line one\uFFFD\nline two", rsp.Contents)
	assert.Equal(
		t,
		"Loaded out/a.txt from a prior skill_run output_files cache, "+
			"start line: 1, end line: 2, total lines: 2 "+
			"(mime: text/plain)"+
			" (invalid UTF-8 replaced with U+FFFD)",
		rsp.Message,
	)
}

func TestFileTool_ReadFile_FromRef_ParseError(t *testing.T) {
	toolSet, err := NewToolSet(WithBaseDir(t.TempDir()))
	assert.NoError(t, err)
	fileToolSet := toolSet.(*fileToolSet)

	_, err = fileToolSet.readFile(context.Background(), &readFileRequest{
		FileName: "artifact://",
	})
	assert.Error(t, err)
}

func TestFileTool_ReadFile_FromSkillRunCache(t *testing.T) {
	skillRoot := t.TempDir()
	const skillName = "demo"
	skillDir := filepath.Join(skillRoot, skillName)
	assert.NoError(t, os.MkdirAll(skillDir, 0o755))

	skillFile := filepath.Join(skillDir, "SKILL.md")
	skillBody := "---\nname: demo\n" +
		"description: test\n---\n"
	assert.NoError(t, os.WriteFile(skillFile, []byte(skillBody), 0o644))

	repo, err := skill.NewFSRepository(skillRoot)
	assert.NoError(t, err)

	rt := skilltool.NewRunTool(repo, localexec.New())
	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)

	runArgs := map[string]any{
		"skill":        skillName,
		"command":      "mkdir -p out; printf hi > out/a.txt",
		"output_files": []string{"out/a.txt"},
		"timeout":      5,
	}
	raw, err := json.Marshal(runArgs)
	assert.NoError(t, err)
	_, err = rt.Call(ctx, raw)
	assert.NoError(t, err)

	toolSet, err := NewToolSet(WithBaseDir(t.TempDir()))
	assert.NoError(t, err)
	fileToolSet, ok := toolSet.(*fileToolSet)
	assert.True(t, ok)

	rsp, err := fileToolSet.readFile(ctx, &readFileRequest{
		FileName: "out/a.txt",
	})
	assert.NoError(t, err)
	assert.Equal(t, "hi", rsp.Contents)
	assert.Contains(t, rsp.Message, "skill_run")

	rsp, err = fileToolSet.readFile(ctx, &readFileRequest{
		FileName: "workspace://out/a.txt",
	})
	assert.NoError(t, err)
	assert.Equal(t, "hi", rsp.Contents)
}

func TestFileTool_ReadFile_ArtifactRef(t *testing.T) {
	tempDir := t.TempDir()
	toolSet, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet, ok := toolSet.(*fileToolSet)
	assert.True(t, ok)

	svc := artifactinmemory.NewService()
	sess := session.NewSession("app", "user", "sess")
	inv := agent.NewInvocation()
	inv.Session = sess
	inv.ArtifactService = svc
	ctx := agent.NewInvocationContext(context.Background(), inv)

	info := artifact.SessionInfo{
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		SessionID: sess.ID,
	}
	ctxIO := codeexecutor.WithArtifactService(ctx, svc)
	ctxIO = codeexecutor.WithArtifactSession(ctxIO, info)
	_, err = codeexecutor.SaveArtifactHelper(
		ctxIO,
		"x.txt",
		[]byte("hi"),
		"text/plain",
	)
	assert.NoError(t, err)

	rsp, err := fileToolSet.readFile(ctx, &readFileRequest{
		FileName: "artifact://x.txt",
	})
	assert.NoError(t, err)
	assert.Equal(t, "hi", rsp.Contents)
}

func TestRejectNonText(t *testing.T) {
	const (
		mimeTextPlain = "text/plain"
		mimePNG       = "image/png"
	)
	tests := []struct {
		name     string
		content  string
		mimeType string
		wantErr  string
	}{
		{
			name:     "plain text is accepted",
			content:  "hello",
			mimeType: mimeTextPlain,
		},
		{
			name:     "invalid utf8 alone is not a rejection",
			content:  "before" + string([]byte{0xd1}) + "after",
			mimeType: mimeTextPlain,
		},
		{
			name:     "an empty mime type skips the mime check",
			content:  "hello",
			mimeType: "",
		},
		{
			name:     "empty content is accepted whatever the mime type",
			content:  "",
			mimeType: mimePNG,
		},
		{
			name:     "an embedded NUL byte is rejected",
			content:  "text" + string([]byte{0x00}) + "more",
			mimeType: mimeTextPlain,
			wantErr:  "file is not a UTF-8 text file (mime: text/plain)",
		},
		{
			name:     "a non-text mime type is rejected",
			content:  "\x89PNG\r\n\x1a\n",
			mimeType: mimePNG,
			wantErr:  "file is not a UTF-8 text file (mime: image/png)",
		},
		{
			name:     "an embedded NUL byte with no mime type is rejected",
			content:  "text" + string([]byte{0x00}) + "more",
			mimeType: "",
			wantErr:  "file is not a UTF-8 text file",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := rejectNonText(tt.content, tt.mimeType)
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			assert.EqualError(t, err, tt.wantErr)
		})
	}
}

func TestSanitizeText(t *testing.T) {
	tests := []struct {
		name         string
		content      string
		wantContent  string
		wantReplaced bool
	}{
		{
			name:    "empty content is left alone",
			content: "",
		},
		{
			name:        "valid utf8 passes through untouched",
			content:     "héllo wörld",
			wantContent: "héllo wörld",
		},
		{
			name:         "a stray byte is replaced",
			content:      "before" + string([]byte{0xd1}) + "after",
			wantContent:  "before\uFFFDafter",
			wantReplaced: true,
		},
		{
			name:         "a truncated multi-byte rune is replaced",
			content:      "prefix" + string([]byte{0xe4, 0xb8}),
			wantContent:  "prefix\uFFFD",
			wantReplaced: true,
		},
		{
			name:         "a run of invalid bytes collapses to one replacement",
			content:      string([]byte{0xff, 0xfe, 0xfd}) + "tail",
			wantContent:  "\uFFFDtail",
			wantReplaced: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, replaced := sanitizeText(tt.content)
			assert.Equal(t, tt.wantContent, got)
			assert.Equal(t, tt.wantReplaced, replaced)
			assert.True(t, utf8.ValidString(got))
		})
	}
}

func TestFileTool_ReadFile_InvalidUTF8ScriptKeepsSurroundingLines(t *testing.T) {
	tempDir := t.TempDir()
	toolSet, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet := toolSet.(*fileToolSet)

	const fileName = "cloud-init.sh"
	script := "#!/usr/bin/env bash\n" +
		"# run as" + string([]byte{0xd1}) + "root\n" +
		"set -o pipefail\n" +
		"export HOME=/root\n"
	err = os.WriteFile(
		filepath.Join(tempDir, fileName),
		[]byte(script),
		0644,
	)
	assert.NoError(t, err)

	startLine, numLines := 2, 2
	rsp, err := fileToolSet.readFile(context.Background(), &readFileRequest{
		FileName:  fileName,
		StartLine: &startLine,
		NumLines:  &numLines,
	})

	assert.NoError(t, err)
	assert.NotEmpty(t, rsp.Contents)
	assert.Equal(t, "# run as�root\nset -o pipefail", rsp.Contents)
	assert.Equal(
		t,
		"Successfully read cloud-init.sh, start line: 2, end line: 3, "+
			"total lines: 5 (invalid UTF-8 replaced with U+FFFD)",
		rsp.Message,
	)
}

func TestFileTool_ReadFile_ValidUTF8IsNotAnnotated(t *testing.T) {
	tempDir := t.TempDir()
	toolSet, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet := toolSet.(*fileToolSet)

	const fileName = "valid.txt"
	err = os.WriteFile(
		filepath.Join(tempDir, fileName),
		[]byte("héllo\nwörld\n"),
		0644,
	)
	assert.NoError(t, err)

	rsp, err := fileToolSet.readFile(
		context.Background(),
		&readFileRequest{FileName: fileName},
	)

	assert.NoError(t, err)
	assert.Equal(t, "héllo\nwörld\n", rsp.Contents)
	assert.Equal(
		t,
		"Successfully read valid.txt, start line: 1, end line: 3, "+
			"total lines: 3",
		rsp.Message,
	)
}

func TestFileTool_ReadFile_FromCache_NULByteRejected(t *testing.T) {
	tempDir := t.TempDir()
	toolSet, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet := toolSet.(*fileToolSet)

	inv := agent.NewInvocation()
	ctx := agent.NewInvocationContext(context.Background(), inv)

	const (
		outABin       = "out/a.bin"
		mimeTextPlain = "text/plain"
	)
	toolcache.StoreSkillRunOutputFiles(inv, []codeexecutor.File{
		{
			Name:     outABin,
			Content:  "text" + string([]byte{0x00}) + "more",
			MIMEType: mimeTextPlain,
		},
	})

	rsp, err := fileToolSet.readFile(ctx, &readFileRequest{
		FileName: outABin,
	})

	assert.Error(t, err)
	assert.Empty(t, rsp.Contents)
	assert.Equal(
		t,
		"Error: file is not a UTF-8 text file (mime: text/plain)",
		rsp.Message,
	)
}

func TestFileTool_ReadFile_InvalidUTF8AtMaxFileSize(t *testing.T) {
	tempDir := t.TempDir()
	const (
		fileName    = "atlimit.txt"
		maxFileSize = 16
	)
	toolSet, err := NewToolSet(
		WithBaseDir(tempDir),
		WithMaxFileSize(maxFileSize),
	)
	assert.NoError(t, err)
	fileToolSet := toolSet.(*fileToolSet)

	// Exactly maxFileSize source bytes, one of them invalid. U+FFFD is three
	// bytes wide, so measuring the size after substitution would reject a file
	// that is within the configured limit.
	content := append(
		[]byte(strings.Repeat("a", maxFileSize-1)),
		byte(0xd1),
	)
	assert.Len(t, content, maxFileSize)
	err = os.WriteFile(filepath.Join(tempDir, fileName), content, 0644)
	assert.NoError(t, err)

	rsp, err := fileToolSet.readFile(
		context.Background(),
		&readFileRequest{FileName: fileName},
	)

	assert.NoError(t, err)
	assert.Equal(t, strings.Repeat("a", maxFileSize-1)+"�", rsp.Contents)
	assert.Equal(
		t,
		"Successfully read atlimit.txt, start line: 1, end line: 1, "+
			"total lines: 1 (invalid UTF-8 replaced with U+FFFD)",
		rsp.Message,
	)
}
