//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package ocr provides OCR (Optical Character Recognition) interfaces and implementations.
package ocr

import (
	"context"
	"io"
)

// Extractor defines the core interface for text extraction from images.
type Extractor interface {
	// ExtractText extracts text from image data.
	// Returns the recognized text and any error encountered.
	ExtractText(ctx context.Context, imageData []byte, opts ...Option) (string, error)

	// ExtractTextFromReader extracts text from an image reader.
	ExtractTextFromReader(ctx context.Context, reader io.Reader, opts ...Option) (string, error)

	// Close releases any resources held by the OCR engine.
	Close() error
}

// Option defines a function type for configuring OCR operations.
type Option func(*Options)

// Options holds runtime options for OCR operations.
type Options struct {
	// Custom options can be added here in the future
	// For example:
	// - Language override
	// - Confidence threshold override
	// - Preprocessing flags
	// - Output format
}
