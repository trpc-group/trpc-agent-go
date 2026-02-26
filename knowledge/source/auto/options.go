//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package auto provides auto-detection knowledge source implementation.
package auto

import (
	"trpc.group/trpc-go/trpc-agent-go/knowledge/chunking"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/ocr"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/transform"
)

// Option represents a functional option for configuring auto sources.
type Option func(*Source)

// WithName sets the name of the auto source.
func WithName(name string) Option {
	return func(s *Source) {
		s.name = name
	}
}

// WithMetadata sets the metadata for the auto source.
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

// WithCustomChunkingStrategy sets a custom chunking strategy for document splitting.
// This option will be passed to directory and file sources when auto-detecting the source type.
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
// This option will be passed to directory and file sources when auto-detecting the source type.
func WithOCRExtractor(extractor ocr.Extractor) Option {
	return func(s *Source) {
		s.ocrExtractor = extractor
	}
}

// WithTransformers sets transformers for document processing.
// Transformers are applied before and after chunking.
// This option will be passed to all sub-sources when auto-detecting the source type.
//
// Example:
//
//	source := auto.New(inputs, auto.WithTransformers(
//	    transform.NewCharFilter("\n", "\t"),
//	    transform.NewCharDedup(" "),
//	))
func WithTransformers(transformers ...transform.Transformer) Option {
	return func(s *Source) {
		s.transformers = append(s.transformers, transformers...)
	}
}

// WithFileReaderType sets the file type to use for text input processing.
// This is useful when you want to control which reader is used for text content.
// Use predefined constants from source package for type safety.
// This only affects direct text content processing, not file/URL/directory sources.
//
// Example:
//
//	source := auto.New([]string{"# Title\nContent"}, auto.WithFileReaderType(source.FileReaderTypeMarkdown))
func WithFileReaderType(fileType source.FileReaderType) Option {
	return func(s *Source) {
		s.fileReaderType = fileType
	}
}
