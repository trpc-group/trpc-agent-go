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
	"testing"

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

func TestFileTool_ReadFile_NilRequest(t *testing.T) {
	tempDir := t.TempDir()
	toolSet, err := NewToolSet(WithBaseDir(tempDir))
	assert.NoError(t, err)
	fileToolSet := toolSet.(*fileToolSet)

	rsp, err := fileToolSet.readFile(context.Background(), nil)
	assert.Error(t, err)
	assert.NotNil(t, rsp)
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
			Content:  string([]byte{invalidByte}),
			MIMEType: mimeTextPlain,
		},
	})

	rsp, err := fileToolSet.readFile(ctx, &readFileRequest{
		FileName: refATxt,
	})
	assert.Error(t, err)
	assert.NotNil(t, rsp)
	assert.Empty(t, rsp.Contents)
	assert.Contains(t, rsp.Message, mimeTextPlain)
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
		mimeText    = "text/plain"
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
	assert.Error(t, err)
	assert.NotNil(t, rsp)
	assert.Empty(t, rsp.Contents)
	assert.Contains(t, rsp.Message, mimeText)
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
			Content:  string([]byte{invalidByte}),
			MIMEType: mimeTextPlain,
		},
	})

	rsp, err := fileToolSet.readFile(ctx, &readFileRequest{
		FileName: outATxt,
	})
	assert.Error(t, err)
	assert.NotNil(t, rsp)
	assert.Empty(t, rsp.Contents)
	assert.Contains(t, rsp.Message, mimeTextPlain)
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
