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

	// Import readers to trigger their init() functions for registration.
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/csv"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/docx"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/json"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/markdown"
	pdfreader "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/pdf"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/text"
)

// ReaderConfig holds configuration for creating readers.
type ReaderConfig struct {
	chunkSize    int
	chunkOverlap int
	ocrExtractor ocr.Extractor
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

	// If no custom configuration, return default readers from registry
	if config.chunkSize <= 0 && config.chunkOverlap <= 0 && config.ocrExtractor == nil {
		return reader.GetAllReaders()
	}

	// Build chunking options if specified
	var fixedOpts []chunking.Option
	var mdOpts []chunking.MarkdownOption

	if config.chunkSize > 0 {
		fixedOpts = append(fixedOpts, chunking.WithChunkSize(config.chunkSize))
		mdOpts = append(mdOpts, chunking.WithMarkdownChunkSize(config.chunkSize))
	}
	if config.chunkOverlap > 0 {
		fixedOpts = append(fixedOpts, chunking.WithOverlap(config.chunkOverlap))
		mdOpts = append(mdOpts, chunking.WithMarkdownOverlap(config.chunkOverlap))
	}

	// Create readers with custom configuration
	readers := make(map[string]reader.Reader)

	// Configure readers with chunking if specified
	if len(fixedOpts) > 0 || len(mdOpts) > 0 {
		fixedChunk := chunking.NewFixedSizeChunking(fixedOpts...)
		markdownChunk := chunking.NewMarkdownChunking(mdOpts...)

		readers["text"] = text.New(text.WithChunkingStrategy(fixedChunk))
		readers["markdown"] = markdown.New(markdown.WithChunkingStrategy(markdownChunk))
		readers["json"] = json.New(json.WithChunkingStrategy(fixedChunk))
		readers["csv"] = csv.New(csv.WithChunkingStrategy(fixedChunk))
		readers["docx"] = docx.New(docx.WithChunkingStrategy(fixedChunk))
	} else {
		// Use default readers
		readers["text"] = text.New()
		readers["markdown"] = markdown.New()
		readers["json"] = json.New()
		readers["csv"] = csv.New()
		readers["docx"] = docx.New()
	}

	// Configure PDF reader with OCR if specified
	if config.ocrExtractor != nil {
		pdfOpts := []pdfreader.Option{
			pdfreader.WithOCRExtractor(config.ocrExtractor),
		}
		if len(fixedOpts) > 0 {
			pdfOpts = append(pdfOpts, pdfreader.WithChunkingStrategy(chunking.NewFixedSizeChunking(fixedOpts...)))
		}
		readers["pdf"] = pdfreader.New(pdfOpts...)
	} else {
		// Check if PDF reader is registered
		if pdfReader, exists := reader.GetReader(".pdf"); exists {
			readers["pdf"] = pdfReader
		}
	}

	return readers
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
// Deprecated: Use GetReaders(WithChunkSize(chunkSize), WithChunkOverlap(overlap)) instead.
func GetReadersWithChunkConfig(chunkSize, overlap int) map[string]reader.Reader {
	return GetReaders(WithChunkSize(chunkSize), WithChunkOverlap(overlap))
}
