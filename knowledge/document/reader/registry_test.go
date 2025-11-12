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
	RegisterReader(r.exts, func(opts ...Option) Reader { return r })

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

// TestGetReader tests the GetReader function.
func TestGetReader(t *testing.T) {
	testCases := []struct {
		name          string
		setupFn       func()
		extension     string
		expectFound   bool
		expectContent string
	}{
		{
			name: "get unregistered extension",
			setupFn: func() {
				ClearRegistry()
			},
			extension:   ".unknown",
			expectFound: false,
		},
		{
			name: "get registered extension",
			setupFn: func() {
				ClearRegistry()
				RegisterReader([]string{".test"}, func(opts ...Option) Reader {
					return &dummyReader{name: "test-reader", exts: []string{".test"}}
				})
			},
			extension:     ".test",
			expectFound:   true,
			expectContent: "test-reader",
		},
		{
			name: "get with case insensitive extension",
			setupFn: func() {
				ClearRegistry()
				RegisterReader([]string{".TXT"}, func(opts ...Option) Reader {
					return &dummyReader{name: "txt-reader", exts: []string{".txt"}}
				})
			},
			extension:     ".txt",
			expectFound:   true,
			expectContent: "txt-reader",
		},
		{
			name: "get cached reader instance",
			setupFn: func() {
				ClearRegistry()
				RegisterReader([]string{".cached"}, func(opts ...Option) Reader {
					return &dummyReader{name: "cached-reader", exts: []string{".cached"}}
				})
				// First call to cache the instance
				_, _ = GetReader(".cached")
			},
			extension:     ".cached",
			expectFound:   true,
			expectContent: "cached-reader",
		},
		{
			name: "get uppercase extension",
			setupFn: func() {
				ClearRegistry()
				RegisterReader([]string{".md"}, func(opts ...Option) Reader {
					return &dummyReader{name: "md-reader", exts: []string{".md"}}
				})
			},
			extension:     ".MD",
			expectFound:   true,
			expectContent: "md-reader",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tc.setupFn()

			reader, found := GetReader(tc.extension)

			if found != tc.expectFound {
				t.Errorf("GetReader(%q) found = %v, expected %v", tc.extension, found, tc.expectFound)
			}

			if tc.expectFound {
				if reader == nil {
					t.Errorf("GetReader(%q) returned nil reader", tc.extension)
					return
				}
				if reader.Name() != tc.expectContent {
					t.Errorf("GetReader(%q) reader name = %q, expected %q",
						tc.extension, reader.Name(), tc.expectContent)
				}
			} else {
				if reader != nil {
					t.Errorf("GetReader(%q) expected nil reader, got %v", tc.extension, reader)
				}
			}
		})
	}

	// Clean up
	ClearRegistry()
}

// TestGetAllReaders tests the GetAllReaders function.
func TestGetAllReaders(t *testing.T) {
	testCases := []struct {
		name          string
		setupFn       func()
		expectedTypes []string
		expectedCount int
	}{
		{
			name: "no readers registered",
			setupFn: func() {
				ClearRegistry()
			},
			expectedTypes: []string{},
			expectedCount: 0,
		},
		{
			name: "single reader",
			setupFn: func() {
				ClearRegistry()
				RegisterReader([]string{".txt"}, func(opts ...Option) Reader {
					return &dummyReader{name: "text-reader", exts: []string{".txt"}}
				})
			},
			expectedTypes: []string{"text"},
			expectedCount: 1,
		},
		{
			name: "multiple readers",
			setupFn: func() {
				ClearRegistry()
				RegisterReader([]string{".txt"}, func(opts ...Option) Reader {
					return &dummyReader{name: "text-reader", exts: []string{".txt"}}
				})
				RegisterReader([]string{".md"}, func(opts ...Option) Reader {
					return &dummyReader{name: "markdown-reader", exts: []string{".md"}}
				})
				RegisterReader([]string{".json"}, func(opts ...Option) Reader {
					return &dummyReader{name: "json-reader", exts: []string{".json"}}
				})
			},
			expectedTypes: []string{"text", "markdown", "json"},
			expectedCount: 3,
		},
		{
			name: "multiple extensions same type",
			setupFn: func() {
				ClearRegistry()
				RegisterReader([]string{".txt", ".text"}, func(opts ...Option) Reader {
					return &dummyReader{name: "text-reader", exts: []string{".txt", ".text"}}
				})
			},
			expectedTypes: []string{"text"},
			expectedCount: 1,
		},
		{
			name: "with cached readers",
			setupFn: func() {
				ClearRegistry()
				RegisterReader([]string{".txt"}, func(opts ...Option) Reader {
					return &dummyReader{name: "text-reader", exts: []string{".txt"}}
				})
				RegisterReader([]string{".md"}, func(opts ...Option) Reader {
					return &dummyReader{name: "markdown-reader", exts: []string{".md"}}
				})
				// Pre-cache one reader
				_, _ = GetReader(".txt")
			},
			expectedTypes: []string{"text", "markdown"},
			expectedCount: 2,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tc.setupFn()

			readers := GetAllReaders()

			if len(readers) != tc.expectedCount {
				t.Errorf("GetAllReaders() returned %d readers, expected %d",
					len(readers), tc.expectedCount)
			}

			for _, expectedType := range tc.expectedTypes {
				if _, exists := readers[expectedType]; !exists {
					t.Errorf("GetAllReaders() missing expected type %q", expectedType)
				}
			}

			// Verify all readers are non-nil
			for typeName, reader := range readers {
				if reader == nil {
					t.Errorf("GetAllReaders() returned nil reader for type %q", typeName)
				}
			}
		})
	}

	// Clean up
	ClearRegistry()
}

// TestGetReaderConcurrent tests GetReader under concurrent access.
func TestGetReaderConcurrent(t *testing.T) {
	ClearRegistry()

	// Register a reader
	RegisterReader([]string{".concurrent"}, func(opts ...Option) Reader {
		return &dummyReader{name: "concurrent-reader", exts: []string{".concurrent"}}
	})

	// Launch multiple goroutines to access the same reader
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			reader, found := GetReader(".concurrent")
			if !found {
				t.Error("GetReader failed to find registered extension")
			}
			if reader == nil {
				t.Error("GetReader returned nil reader")
			}
			done <- true
		}()
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}

	ClearRegistry()
}

// TestExtensionToTypeEdgeCases tests edge cases in extensionToType.
func TestExtensionToTypeEdgeCases(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "doc extension",
			input:    ".doc",
			expected: "docx",
		},
		{
			name:     "no dot prefix",
			input:    "txt",
			expected: "text",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "dot only",
			input:    ".",
			expected: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := extensionToType(tc.input)
			if result != tc.expected {
				t.Errorf("extensionToType(%q) = %q, expected %q",
					tc.input, result, tc.expected)
			}
		})
	}
}

// TestRegisterReaderMultipleExtensions tests registering a reader with multiple extensions.
func TestRegisterReaderMultipleExtensions(t *testing.T) {
	ClearRegistry()

	extensions := []string{".test1", ".test2", ".test3"}
	RegisterReader(extensions, func(opts ...Option) Reader {
		return &dummyReader{name: "multi-ext-reader", exts: extensions}
	})

	registeredExts := GetRegisteredExtensions()

	if len(registeredExts) != len(extensions) {
		t.Errorf("expected %d registered extensions, got %d",
			len(extensions), len(registeredExts))
	}

	// Verify all extensions are registered (case-insensitive)
	extMap := make(map[string]bool)
	for _, ext := range registeredExts {
		extMap[ext] = true
	}

	for _, ext := range extensions {
		normalized := strings.ToLower(ext)
		if !extMap[normalized] {
			t.Errorf("extension %q not found in registered extensions", normalized)
		}
	}

	ClearRegistry()
}
