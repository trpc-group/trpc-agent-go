//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package deps

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDownloadHelpers(t *testing.T) {
	t.Parallel()

	require.Equal(
		t,
		"tool.zip",
		downloadFileName("https://example.com/tool.zip?x=1"),
	)
	require.Equal(t, "", downloadFileName("://bad"))

	path, err := downloadTargetPath(t.TempDir(), "skill", InstallAction{
		URL: "https://example.com/tool.zip",
	})
	require.NoError(t, err)
	require.Contains(t, path, filepath.Join(defaultToolsDir, "skill"))
	require.Contains(t, path, "tool.zip")

	root, err := safeJoin(t.TempDir(), "runtime")
	require.NoError(t, err)
	require.NotEmpty(t, root)

	_, err = safeJoin(t.TempDir(), "../escape")
	require.Error(t, err)
	_, err = skillToolsRoot(t.TempDir(), " ")
	require.Error(t, err)
}

func TestOpenDownloadReader(t *testing.T) {
	t.Parallel()

	filePath := filepath.Join(t.TempDir(), "a.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("file"), 0o644))
	reader, cleanup, err := openDownloadReader(context.Background(), filePath)
	require.NoError(t, err)
	defer cleanup()
	data, readErr := io.ReadAll(reader)
	require.NoError(t, readErr)
	require.Equal(t, "file", string(data))

	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		_, _ = w.Write([]byte("http"))
	}))
	defer server.Close()

	reader, cleanup, err = openDownloadReader(
		context.Background(),
		server.URL,
	)
	require.NoError(t, err)
	defer cleanup()
	data, readErr = io.ReadAll(reader)
	require.NoError(t, readErr)
	require.Equal(t, "http", string(data))

	badServer := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer badServer.Close()

	_, _, err = openDownloadReader(context.Background(), badServer.URL)
	require.ErrorContains(t, err, "502 Bad Gateway")
	_, _, err = openDownloadReader(context.Background(), "ftp://example.com/x")
	require.ErrorContains(t, err, "unsupported download scheme")
}

func TestWriteDownloadFile_CleansUpOnCopyError(t *testing.T) {
	t.Parallel()

	target := filepath.Join(t.TempDir(), "tool.bin")
	err := writeDownloadFile(target, errorReader{})
	require.Error(t, err)
	_, statErr := os.Stat(target)
	require.True(t, errors.Is(statErr, os.ErrNotExist))
}

func TestExecuteDownloadStep(t *testing.T) {
	t.Parallel()

	source := filepath.Join(t.TempDir(), "tool.txt")
	require.NoError(t, os.WriteFile(source, []byte("hello"), 0o644))

	target := filepath.Join(t.TempDir(), "out", "tool.txt")
	out, err := executeDownloadStep(context.Background(), Step{
		URL:        "file://" + source,
		TargetPath: target,
	})
	require.NoError(t, err)
	require.Contains(t, out, target)
	data, readErr := os.ReadFile(target)
	require.NoError(t, readErr)
	require.Equal(t, "hello", string(data))
}

func TestExtractZip_StripsComponents(t *testing.T) {
	t.Parallel()

	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	file, err := writer.Create("pkg/bin/tool")
	require.NoError(t, err)
	_, err = file.Write([]byte("hello"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	target := t.TempDir()
	err = extractZip(bytes.NewReader(archive.Bytes()), target, 1)
	require.NoError(t, err)
	data, readErr := os.ReadFile(filepath.Join(target, "bin", "tool"))
	require.NoError(t, readErr)
	require.Equal(t, "hello", string(data))

	name, ok := archiveTargetName("pkg/bin/tool", 3)
	require.False(t, ok)
	require.Empty(t, name)
}

func TestArchiveHelpers(t *testing.T) {
	t.Parallel()

	require.Equal(t, "tar.gz", normalizeArchiveKind("tgz"))
	require.Equal(t, "zip", normalizeArchiveKind(" zip "))
	require.Equal(t, os.FileMode(0o644), archiveEntryPerm(0))
	require.Equal(
		t,
		os.FileMode(0o755),
		archiveEntryPerm(0o755),
	)
	err := extractArchive(
		bytes.NewReader(nil),
		"rar",
		t.TempDir(),
		0,
	)
	require.ErrorContains(t, err, "unsupported archive kind")
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, errors.New("copy failed")
}
