//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package reader defines the interface for document readers.
// This interface allows reading from any io.Reader source, such as files or HTTP responses.
package reader

import (
	"io"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/chunking"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/ocr"
)

// Config holds configuration for readers.
type Config struct {
	Chunk                  bool
	ChunkSize              int
	ChunkOverlap           int
	CustomChunkingStrategy chunking.Strategy
	OCRExtractor           ocr.Extractor
}

// Option is a functional option for configuring readers.
type Option func(*Config)

// WithChunk enables or disables document chunking.
func WithChunk(enabled bool) Option {
	return func(c *Config) {
		c.Chunk = enabled
	}
}

// WithChunkSize sets the chunk size for chunking strategies that support it.
// This will be passed to the reader's default chunking strategy builder.
func WithChunkSize(size int) Option {
	return func(c *Config) {
		c.ChunkSize = size
		c.Chunk = true
	}
}

// WithChunkOverlap sets the chunk overlap for chunking strategies that support it.
// This will be passed to the reader's default chunking strategy builder.
func WithChunkOverlap(overlap int) Option {
	return func(c *Config) {
		c.ChunkOverlap = overlap
		c.Chunk = true
	}
}

// WithCustomChunkingStrategy sets a custom chunking strategy, overriding the reader's default.
// Use this when you need full control over the chunking behavior.
func WithCustomChunkingStrategy(strategy chunking.Strategy) Option {
	return func(c *Config) {
		c.CustomChunkingStrategy = strategy
		c.Chunk = true
	}
}

// WithOCRExtractor sets the OCR extractor (primarily for PDF reader).
func WithOCRExtractor(extractor ocr.Extractor) Option {
	return func(c *Config) {
		c.OCRExtractor = extractor
	}
}

// BuildChunkingStrategy builds a chunking strategy from config.
// If a custom strategy is set, it returns that.
// Otherwise, it calls the provided default builder with chunk size/overlap parameters.
func BuildChunkingStrategy(config *Config, defaultBuilder func(chunkSize, overlap int) chunking.Strategy) chunking.Strategy {
	// If custom strategy is provided, use it
	if config.CustomChunkingStrategy != nil {
		return config.CustomChunkingStrategy
	}

	// Otherwise, use the default builder with size/overlap parameters
	return defaultBuilder(config.ChunkSize, config.ChunkOverlap)
}

// Reader interface for different document readers.
type Reader interface {
	// ReadFromReader reads content from an io.Reader and returns a list of documents.
	// The name parameter is used to identify the source (e.g., filename, URL).
	ReadFromReader(name string, r io.Reader) ([]*document.Document, error)

	// ReadFromFile reads content from a file path and returns a list of documents.
	ReadFromFile(filePath string) ([]*document.Document, error)

	// ReadFromURL reads content from a URL and returns a list of documents.
	ReadFromURL(url string) ([]*document.Document, error)

	// Name returns the name of this reader.
	Name() string

	// SupportedExtensions returns the file extensions this reader supports.
	// Extensions should include the dot prefix (e.g., ".pdf", ".txt").
	SupportedExtensions() []string
}
