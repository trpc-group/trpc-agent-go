//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package file provides file-based knowledge source implementation.
package file

import (
	"trpc.group/trpc-go/trpc-agent-go/knowledge/chunking"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/ocr"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/transform"
)

// Option represents a functional option for configuring FileSource.
type Option func(*Source)

// WithName sets a custom name for the file source.
func WithName(name string) Option {
	return func(s *Source) {
		s.name = name
	}
}

// WithMetadata sets additional metadata for the source.
func WithMetadata(metadata map[string]any) Option {
	return func(s *Source) {
		s.metadata = metadata
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
// This overrides the reader's default chunking strategy.
// For example: WithCustomChunkingStrategy(chunking.NewRecursiveChunking())
// Note: Most readers have their own optimal chunking strategy (JSON->JSONChunking, Markdown->MarkdownChunking, etc.)
func WithCustomChunkingStrategy(strategy chunking.Strategy) Option {
	return func(s *Source) {
		s.customChunkingStrategy = strategy
	}
}

// WithChunkSize sets the chunk size for the reader's default chunking strategy.
// Each reader will use its own optimal chunking strategy with this size parameter.
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

// WithTransformers sets transformers for document processing.
// Transformers are applied before and after chunking.
//
// Example:
//
//	source := file.New(paths, file.WithTransformers(
//	    transform.NewCharFilter("\n", "\t"),
//	    transform.NewCharDedup(" "),
//	))
func WithTransformers(transformers ...transform.Transformer) Option {
	return func(s *Source) {
		s.transformers = append(s.transformers, transformers...)
	}
}

// WithFileReaderType overrides the automatic file type detection and forces using the specified file type.
// This is useful when file extensions don't match the actual content format.
// Use predefined constants from source package for type safety.
//
// Example:
//
//	source := file.New([]string{"data.txt"}, file.WithFileReaderType(source.FileReaderTypeJSON))
func WithFileReaderType(fileType source.FileReaderType) Option {
	return func(s *Source) {
		s.fileReaderType = fileType
	}
}
