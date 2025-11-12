//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package dir provides directory-based knowledge source implementation.
package dir

import (
	"trpc.group/trpc-go/trpc-agent-go/knowledge/chunking"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/ocr"
)

// Option represents a functional option for configuring directory sources.
type Option func(*Source)

// WithName sets the name of the directory source.
func WithName(name string) Option {
	return func(s *Source) {
		s.name = name
	}
}

// WithMetadata sets the metadata for the directory source.
func WithMetadata(metadata map[string]any) Option {
	return func(s *Source) {
		for k, v := range metadata {
			s.metadata[k] = v
		}
	}
}

// WithMetadataValue adds a single metadata key-value pair.
func WithMetadataValue(key string, value any) Option {
	return func(s *Source) {
		if s.metadata == nil {
			s.metadata = make(map[string]any)
		}
		s.metadata[key] = value
	}
}

// WithFileExtensions sets the file extensions to filter by.
func WithFileExtensions(extensions []string) Option {
	return func(s *Source) {
		s.fileExtensions = extensions
	}
}

// WithRecursive sets whether to process subdirectories recursively.
func WithRecursive(recursive bool) Option {
	return func(s *Source) {
		s.recursive = recursive
	}
}

// WithCustomChunkingStrategy sets a custom chunking strategy for document splitting.
// This overrides the reader's default chunking strategy.
// Note: Most readers have their own optimal chunking strategy.
func WithCustomChunkingStrategy(strategy chunking.Strategy) Option {
	return func(s *Source) {
		s.customChunkingStrategy = strategy
	}
}

// WithChunkSize sets the chunk size for the reader's default chunking strategy.
func WithChunkSize(size int) Option {
	return func(s *Source) {
		s.chunkSize = size
	}
}

// WithChunkOverlap sets the chunk overlap for the reader's default chunking strategy.
func WithChunkOverlap(overlap int) Option {
	return func(s *Source) {
		s.chunkOverlap = overlap
	}
}

// WithOCRExtractor sets an OCR extractor for processing images in documents (e.g., PDFs).
func WithOCRExtractor(extractor ocr.Extractor) Option {
	return func(s *Source) {
		s.ocrExtractor = extractor
	}
}
