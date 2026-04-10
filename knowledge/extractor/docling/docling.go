//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package docling provides a content extractor backed by a Docling Serve instance.
//
// Docling Serve (https://github.com/docling-project/docling-serve) is a document
// conversion service that handles PDF, DOCX, PPTX, images, and other formats,
// producing markdown or text output.
//
// Start a Docling Serve instance:
//
//	docker run -p 5001:5001 ghcr.io/docling-project/docling-serve
//
// Use the extractor with a file source:
//
//	ext := docling.New(docling.WithEndpoint("http://localhost:5001"))
//	src := file.New(paths, file.WithExtractor(ext))
package docling

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/extractor"
)

const (
	defaultEndpoint = "http://localhost:5001"
	defaultTimeout  = 5 * time.Minute

	convertFilePath = "/v1/convert/file"
)

// Extractor implements extractor.Extractor by calling a Docling Serve instance.
type Extractor struct {
	opts options
}

// New creates a new Docling extractor.
func New(opts ...Option) *Extractor {
	o := options{
		endpoint:     defaultEndpoint,
		timeout:      defaultTimeout,
		ocrEnabled:   true,
		imageRefMode: ImageRefModePlaceholder,
		formats:      defaultFormats,
	}
	for _, opt := range opts {
		opt(&o)
	}
	if o.httpClient == nil {
		o.httpClient = &http.Client{Timeout: o.timeout}
	}
	return &Extractor{opts: o}
}

// Extract converts the given data by uploading it to Docling Serve.
func (e *Extractor) Extract(ctx context.Context, data []byte, opts ...extractor.Option) (*extractor.Result, error) {
	return e.ExtractFromReader(ctx, bytes.NewReader(data), opts...)
}

// ExtractFromReader converts content from a reader by uploading to Docling Serve.
func (e *Extractor) ExtractFromReader(ctx context.Context, r io.Reader, opts ...extractor.Option) (*extractor.Result, error) {
	eopts := extractor.ApplyOptions(opts...)
	return e.doFileConvert(ctx, r, eopts)
}

// SupportedFormats returns the file extensions this extractor handles.
func (e *Extractor) SupportedFormats() []string {
	return e.opts.formats
}

// Close releases resources. Docling extractor is stateless, so this is a no-op.
func (e *Extractor) Close() error {
	return nil
}
