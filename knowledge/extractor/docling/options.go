//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package docling provides a content extractor backed by a Docling Serve instance.
package docling

import (
	"net/http"
	"time"
)

// options holds internal configuration for the Docling extractor.
type options struct {
	endpoint     string
	httpClient   *http.Client
	timeout      time.Duration
	ocrEnabled   bool
	imageRefMode ImageRefMode
	formats      []string
}

// ImageRefMode controls how images appear in the markdown output.
type ImageRefMode string

// ImageRefMode constants.
const (
	// ImageRefModeEmbedded includes images as base64 data URIs.
	ImageRefModeEmbedded ImageRefMode = "embedded"
	// ImageRefModePlaceholder replaces images with <!-- image --> placeholders (default).
	ImageRefModePlaceholder ImageRefMode = "placeholder"
	// ImageRefModeReferenced uses external file references for images.
	ImageRefModeReferenced ImageRefMode = "referenced"
)

// defaultFormats lists the file extensions Docling can handle by default.
var defaultFormats = []string{
	".pdf",
	".docx",
	".pptx",
	".xlsx",
	".html",
	".png",
	".jpg",
	".jpeg",
	".tiff",
	".tif",
	".bmp",
	".asciidoc",
	".md",
	".csv",
}

// Option configures the Docling extractor.
type Option func(*options)

// WithEndpoint sets the Docling Serve API base URL (e.g., "http://localhost:5001").
func WithEndpoint(endpoint string) Option {
	return func(o *options) {
		o.endpoint = endpoint
	}
}

// WithHTTPClient sets a custom HTTP client for requests to Docling Serve.
func WithHTTPClient(client *http.Client) Option {
	return func(o *options) {
		o.httpClient = client
	}
}

// WithTimeout sets the request timeout for conversion calls.
func WithTimeout(d time.Duration) Option {
	return func(o *options) {
		o.timeout = d
	}
}

// WithOCR enables or disables OCR during document conversion.
func WithOCR(enabled bool) Option {
	return func(o *options) {
		o.ocrEnabled = enabled
	}
}

// WithFormats overrides the default set of supported file extensions.
func WithFormats(formats []string) Option {
	return func(o *options) {
		o.formats = formats
	}
}

// WithImageRefMode sets how images are represented in the markdown output.
// Default is ImageRefModePlaceholder.
func WithImageRefMode(mode ImageRefMode) Option {
	return func(o *options) {
		o.imageRefMode = mode
	}
}
