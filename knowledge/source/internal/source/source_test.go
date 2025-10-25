//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
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
		// Content type based detection
		{"text/html; charset=utf-8", "", "text"},
		{"text/plain", "", "text"},
		{"text/plain; charset=utf-8", "", "text"},
		{"application/json", "", "json"},
		{"application/json; charset=utf-8", "", "json"},
		{"text/csv", "", "csv"},
		{"application/pdf", "", "pdf"},
		{"application/vnd.openxmlformats-officedocument.wordprocessingml.document", "", "docx"},

		// File extension based detection
		{"", "file.md", "markdown"},
		{"", "file.markdown", "markdown"},
		{"", "file.txt", "text"},
		{"", "file.text", "text"},
		{"", "file.html", "text"},
		{"", "file.htm", "text"},
		{"", "file.json", "json"},
		{"", "file.csv", "csv"},
		{"", "file.pdf", "pdf"},
		{"", "file.docx", "docx"},
		{"", "file.doc", "docx"},
		{"", "fallback.unknown", "text"},

		// Content type takes precedence over file extension
		{"application/json", "file.txt", "json"},
		{"text/csv", "file.json", "csv"},
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
