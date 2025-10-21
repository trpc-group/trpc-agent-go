//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package reader

import (
	"errors"
	"io"
	"net/url"
	"os"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

// dummyReader is a lightweight Reader implementation for tests.
// It does not actually parse files; it only records the last call.
type dummyReader struct {
	name string
	exts []string

	lastFrom string // "reader" | "file" | "url"
}

func (d *dummyReader) ReadFromReader(name string, r io.Reader) ([]*document.Document, error) {
	if r == nil {
		return nil, errors.New("nil reader")
	}
	d.lastFrom = "reader"
	return []*document.Document{{
		Content:  "from-reader",
		Metadata: map[string]any{"name": name},
	}}, nil
}

func (d *dummyReader) ReadFromFile(filePath string) ([]*document.Document, error) {
	if _, err := os.Stat(filePath); err != nil {
		return nil, err
	}
	d.lastFrom = "file"
	return []*document.Document{{
		Content:  "from-file",
		Metadata: map[string]any{"path": filePath},
	}}, nil
}

func (d *dummyReader) ReadFromURL(raw string) ([]*document.Document, error) {
	if _, err := url.Parse(raw); err != nil {
		return nil, err
	}
	d.lastFrom = "url"
	return []*document.Document{{
		Content:  "from-url",
		Metadata: map[string]any{"url": raw},
	}}, nil
}

func (d *dummyReader) Name() string                  { return d.name }
func (d *dummyReader) SupportedExtensions() []string { return d.exts }

func TestRegistry_RegisterAndList(t *testing.T) {
	// Start from a clean state so tests are hermetic.
	ClearRegistry()

	// Register a reader with mixed-case extensions to verify normalization.
	r := &dummyReader{name: "dummy", exts: []string{".TXT", ".Md"}}
	RegisterReader(r.exts, func() Reader { return r })

	// GetRegisteredExtensions should include normalized lowercase extensions as registered.
	exts := GetRegisteredExtensions()
	got := strings.Join(exts, ",")
	// Order is not guaranteed; check by set membership.
	hasTxt, hasMd := false, false
	for _, e := range exts {
		switch e {
		case ".txt":
			hasTxt = true
		case ".md":
			hasMd = true
		}
	}
	if !hasTxt || !hasMd {
		t.Fatalf("expected .txt and .md in registered extensions, got: %s", got)
	}

	// Clean up for subsequent tests.
	ClearRegistry()
}

func TestExtensionToType(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{".txt", "text"},
		{".text", "text"},
		{".md", "markdown"},
		{".markdown", "markdown"},
		{".json", "json"},
		{".csv", "csv"},
		{".pdf", "pdf"},
		{".docx", "docx"},
		{".xlsx", "xlsx"}, // unknown -> passthrough without dot
	}
	for _, c := range cases {
		if got := extensionToType(c.in); got != c.want {
			t.Fatalf("extensionToType(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Intentionally avoid calling GetReader/GetAllReaders here because their
// internal lock upgrade strategy may deadlock under some schedulers.
