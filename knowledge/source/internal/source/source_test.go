//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package source

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetFileType(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"data.txt", "text"},
		{"foo.pdf", "pdf"},
		{"note.md", "markdown"},
		{"info.json", "json"},
		{"sheet.csv", "csv"},
		{"doc.docx", "docx"},
		{"unknown.bin", "text"},
	}

	for _, c := range cases {
		got := GetFileType(c.path)
		require.Equal(t, c.want, got, "path %s", c.path)
	}
}

func TestGetFileTypeFromContentType(t *testing.T) {
	cases := []struct {
		contentType string
		fileName    string
		want        string
	}{
		{"text/html; charset=utf-8", "", "text"},
		{"application/json", "", "json"},
		{"text/csv", "", "csv"},
		{"application/pdf", "", "pdf"},
		{"application/vnd.openxmlformats-officedocument.wordprocessingml.document", "", "docx"},
		{"", "file.md", "markdown"},
		{"", "fallback.unknown", "text"},
	}

	for _, c := range cases {
		got := GetFileTypeFromContentType(c.contentType, c.fileName)
		require.Equal(t, c.want, got, "ctype %s fname %s", c.contentType, c.fileName)
	}
}

func TestGetReadersWithChunkConfig(t *testing.T) {
	readersDefault := GetReaders()
	readers := GetReadersWithChunkConfig(128, 16)

	// Ensure reader keys match.
	require.Equal(t, len(readersDefault), len(readers))

	// Verify that requesting zero config returns default map object count.
	readersZero := GetReadersWithChunkConfig(0, 0)
	require.Equal(t, len(readersDefault), len(readersZero))
}
