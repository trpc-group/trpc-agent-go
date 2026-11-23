//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/stretchr/testify/require"
)

func TestPrepareWorkspaceCreatesFreshRunDirectories(t *testing.T) {
	base := t.TempDir()
	existingInput := filepath.Join(base, inputFilename)
	require.NoError(t, os.WriteFile(existingInput, []byte("keep"), 0o600))

	first, cleanupFirst, err := prepareWorkspace(base)
	require.NoError(t, err)
	t.Cleanup(cleanupFirst)
	require.NotEqual(t, base, first)
	require.NoError(t, os.WriteFile(
		filepath.Join(first, inputFilename),
		[]byte(inputContent),
		0o600,
	))
	content, err := os.ReadFile(existingInput)
	require.NoError(t, err)
	require.Equal(t, "keep", string(content))

	second, cleanupSecond, err := prepareWorkspace(base)
	require.NoError(t, err)
	t.Cleanup(cleanupSecond)
	require.NotEqual(t, first, second)
	_, err = os.Stat(filepath.Join(second, outputFilename))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestReadTextFileRanges(t *testing.T) {
	lineTwo := 2
	lineThree := 3
	oneLine := 1
	maxInt := int(^uint(0) >> 1)

	tests := []struct {
		name    string
		content string
		line    *int
		limit   *int
		want    string
	}{
		{
			name:    "full content",
			content: "one\ntwo\n",
			want:    "one\ntwo\n",
		},
		{
			name:    "single line",
			content: "one\ntwo\nthree\n",
			line:    &lineTwo,
			limit:   &oneLine,
			want:    "two",
		},
		{
			name:    "remaining lines with maximum limit",
			content: "one\ntwo\nthree\n",
			line:    &lineTwo,
			limit:   &maxInt,
			want:    "two\nthree\n",
		},
		{
			name:    "start after end",
			content: "one\ntwo\n",
			line:    &lineThree,
			limit:   &oneLine,
			want:    "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := readTextFile(strings.NewReader(test.content), test.line, test.limit)
			require.NoError(t, err)
			require.Equal(t, test.want, got)
		})
	}
}

func TestReadTextFileSkipsOversizedLinesBeforeRange(t *testing.T) {
	lineTwo := 2
	oneLine := 1
	content := strings.Repeat("x", maxReadTextFileBytes+1) + "\nsmall\n"

	got, err := readTextFile(strings.NewReader(content), &lineTwo, &oneLine)
	require.NoError(t, err)
	require.Equal(t, "small", got)
}

func TestReadTextFileRejectsOversizedSelectedLine(t *testing.T) {
	content := strings.Repeat("x", maxReadTextFileBytes+1)

	_, err := readTextFile(strings.NewReader(content), nil, nil)
	require.ErrorContains(t, err, "response limit")
}

func TestExampleClientRejectsSymlinkEscape(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	require.NoError(t, os.Mkdir(workspace, 0o755))
	outside := filepath.Join(t.TempDir(), "outside.txt")
	require.NoError(t, os.WriteFile(outside, []byte("outside"), 0o600))
	if err := os.Symlink(outside, filepath.Join(workspace, "escape.txt")); err != nil {
		t.Skipf("create symlink: %v", err)
	}

	client := &exampleClient{workspace: workspace}
	_, err := client.ReadTextFile(context.Background(), acpsdk.ReadTextFileRequest{
		Path: "escape.txt",
	})
	require.Error(t, err)

	_, err = client.WriteTextFile(context.Background(), acpsdk.WriteTextFileRequest{
		Path:    "escape.txt",
		Content: "overwrite",
	})
	require.Error(t, err)
	content, readErr := os.ReadFile(outside)
	require.NoError(t, readErr)
	require.Equal(t, "outside", string(content))

	outsideDir := t.TempDir()
	if err := os.Symlink(outsideDir, filepath.Join(workspace, "escape-dir")); err != nil {
		t.Skipf("create directory symlink: %v", err)
	}
	_, err = client.WriteTextFile(context.Background(), acpsdk.WriteTextFileRequest{
		Path:    "escape-dir/new.txt",
		Content: "overwrite",
	})
	require.Error(t, err)
	_, statErr := os.Stat(filepath.Join(outsideDir, "new.txt"))
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestExampleClientFileOperationsStayInWorkspace(t *testing.T) {
	workspace := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(workspace, inputFilename),
		[]byte(inputContent),
		0o600,
	))
	client := &exampleClient{workspace: workspace}

	response, err := client.ReadTextFile(context.Background(), acpsdk.ReadTextFileRequest{
		Path: inputFilename,
	})
	require.NoError(t, err)
	require.Equal(t, inputContent, response.Content)

	_, err = client.WriteTextFile(context.Background(), acpsdk.WriteTextFileRequest{
		Path:    "nested/" + outputFilename,
		Content: inputContent,
	})
	require.NoError(t, err)
	content, err := os.ReadFile(filepath.Join(workspace, "nested", outputFilename))
	require.NoError(t, err)
	require.Equal(t, inputContent, string(content))
}
