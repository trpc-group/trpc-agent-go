//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package source provides internal source utils.
package source

import (
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/chunking"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/ocr"

	_ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/csv"
	_ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/json"
	_ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/markdown"
	_ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/text"
)

// ReaderConfig holds configuration for creating readers.
type ReaderConfig struct {
	chunkSize              int
	chunkOverlap           int
	customChunkingStrategy chunking.Strategy
	ocrExtractor           ocr.Extractor
}

// ReaderOption is a functional option for configuring readers.
type ReaderOption func(*ReaderConfig)

// WithChunkSize sets the chunk size for readers.
func WithChunkSize(size int) ReaderOption {
	return func(c *ReaderConfig) {
		c.chunkSize = size
	}
}

// WithChunkOverlap sets the chunk overlap for readers.
func WithChunkOverlap(overlap int) ReaderOption {
	return func(c *ReaderConfig) {
		c.chunkOverlap = overlap
	}
}

// WithCustomChunkingStrategy sets a custom chunking strategy for readers.
func WithCustomChunkingStrategy(strategy chunking.Strategy) ReaderOption {
	return func(c *ReaderConfig) {
		c.customChunkingStrategy = strategy
	}
}

// WithOCRExtractor sets the OCR extractor for PDF reader.
func WithOCRExtractor(extractor ocr.Extractor) ReaderOption {
	return func(c *ReaderConfig) {
		c.ocrExtractor = extractor
	}
}

// GetReaders returns all available readers configured with the given options.
func GetReaders(opts ...ReaderOption) map[string]reader.Reader {
	config := &ReaderConfig{}
	for _, opt := range opts {
		opt(config)
	}

	// Build reader options
	readerOpts := buildReaderOptions(config)

	// Get readers with options
	return reader.GetAllReaders(readerOpts...)
}

// buildReaderOptions constructs reader options from config.
func buildReaderOptions(config *ReaderConfig) []reader.Option {
	var opts []reader.Option

	// Pass all configurations to readers, let reader layer handle priority
	if config.chunkSize > 0 {
		opts = append(opts, reader.WithChunkSize(config.chunkSize))
	}
	if config.chunkOverlap > 0 {
		opts = append(opts, reader.WithChunkOverlap(config.chunkOverlap))
	}
	if config.customChunkingStrategy != nil {
		opts = append(opts, reader.WithCustomChunkingStrategy(config.customChunkingStrategy))
	}
	if config.ocrExtractor != nil {
		opts = append(opts, reader.WithOCRExtractor(config.ocrExtractor))
	}

	return opts
}

// GetFileType determines the file type based on the file extension.
func GetFileType(filePath string) string {
	ext := filepath.Ext(filePath)
	switch ext {
	case ".txt", ".text":
		return "text"
	case ".pdf":
		return "pdf"
	case ".md", ".markdown":
		return "markdown"
	case ".json":
		return "json"
	case ".csv":
		return "csv"
	case ".docx", ".doc":
		return "docx"
	default:
		return "text"
	}
}

// GetFileTypeFromContentType determines the file type based on content type or file extension.
func GetFileTypeFromContentType(contentType, fileName string) string {
	// First try content type.
	if contentType != "" {
		parts := strings.Split(contentType, ";")
		mainType := strings.TrimSpace(parts[0])

		switch {
		case strings.Contains(mainType, "text/html"):
			return "text"
		case strings.Contains(mainType, "text/plain"):
			return "text"
		case strings.Contains(mainType, "application/json"):
			return "json"
		case strings.Contains(mainType, "text/csv"):
			return "csv"
		case strings.Contains(mainType, "application/pdf"):
			return "pdf"
		case strings.Contains(mainType, "application/vnd.openxmlformats-officedocument.wordprocessingml.document"):
			return "docx"
		}
	}

	// Fall back to file extension.
	ext := filepath.Ext(fileName)
	switch ext {
	case ".txt", ".text", ".html", ".htm":
		return "text"
	case ".pdf":
		return "pdf"
	case ".md", ".markdown":
		return "markdown"
	case ".json":
		return "json"
	case ".csv":
		return "csv"
	case ".docx", ".doc":
		return "docx"
	default:
		return "text"
	}
}

// GetReadersWithChunkConfig is deprecated. Use GetReaders with functional options instead.
// Deprecated: Use GetReaders(WithChunkSize(size), WithChunkOverlap(overlap)) instead.
func GetReadersWithChunkConfig(chunkSize, overlap int) map[string]reader.Reader {
	return GetReaders(WithChunkSize(chunkSize), WithChunkOverlap(overlap))
}
