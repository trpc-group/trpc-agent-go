//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package extractor provides content extraction interfaces for converting
// various document formats (PDF, images, presentations, etc.) to text or markdown.
//
// An Extractor converts raw file content into a readable text stream along with
// a format hint (e.g., "markdown" or "text"). The extracted content is then fed
// into the appropriate document reader for chunking and further processing.
//
// Processing flow:
//
//	raw file → Extractor.Extract() → (io.Reader, format) → Reader.ReadFromReader() → []*Document
//
// Example usage:
//
//	import "trpc.group/trpc-go/trpc-agent-go/knowledge/extractor"
//
//	// Use with file source
//	source := file.New(paths, file.WithExtractor(myExtractor))
package extractor

import (
	"context"
	"io"
)

// FormatText indicates the extracted content is plain text.
const FormatText = "text"

// FormatMarkdown indicates the extracted content is markdown.
const FormatMarkdown = "markdown"

// Result holds the output of an extraction operation.
type Result struct {
	// Reader provides the extracted content as a stream.
	Reader io.Reader

	// Format indicates the output format, which maps to a reader type.
	// Typically "text" or "markdown".
	Format string
}

// Extractor defines the interface for content extraction from various formats.
// Implementations may call external services (e.g., LLM vision APIs, document
// parsing services) to convert files into text or markdown.
type Extractor interface {
	// Extract converts the given data into text or markdown.
	// The returned Result contains an io.Reader for the extracted content
	// and a Format field indicating which reader should process it next.
	Extract(ctx context.Context, data []byte, opts ...Option) (*Result, error)

	// ExtractFromReader converts content from a reader.
	ExtractFromReader(ctx context.Context, r io.Reader, opts ...Option) (*Result, error)

	// SupportedFormats returns the file extensions this extractor handles.
	// Extensions should include the dot prefix (e.g., ".pdf", ".png", ".pptx").
	SupportedFormats() []string

	// Close releases any resources held by the extractor.
	Close() error
}

// Option defines a function type for configuring extraction operations.
type Option func(*Options)

// Options holds runtime options for extraction operations.
type Options struct {
	// OutputFormat specifies the desired output format ("text" or "markdown").
	// If empty, the extractor chooses its default format.
	OutputFormat string
}

// WithOutputFormat sets the desired output format for extraction.
func WithOutputFormat(format string) Option {
	return func(o *Options) {
		o.OutputFormat = format
	}
}

// ApplyOptions applies the given options to an Options struct and returns it.
func ApplyOptions(opts ...Option) *Options {
	o := &Options{}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

// Supports checks if the extractor supports the given file extension.
func Supports(e Extractor, ext string) bool {
	for _, f := range e.SupportedFormats() {
		if f == ext {
			return true
		}
	}
	return false
}
