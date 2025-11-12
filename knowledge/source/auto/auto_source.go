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
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/chunking"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/text"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/ocr"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	dirsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/dir"
	filesource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
	urlsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/url"
)

const (
	defaultAutoSourceName = "Auto Source"
)

// Source represents a knowledge source that automatically detects the source type.
type Source struct {
	inputs                 []string
	name                   string
	metadata               map[string]any
	textReader             reader.Reader
	chunkSize              int
	chunkOverlap           int
	customChunkingStrategy chunking.Strategy
	ocrExtractor           ocr.Extractor
}

// New creates a new auto knowledge source.
func New(inputs []string, opts ...Option) *Source {
	sourceObj := &Source{
		inputs:       inputs,
		name:         defaultAutoSourceName,
		metadata:     make(map[string]any),
		chunkSize:    0,
		chunkOverlap: 0,
	}

	// Apply options first so chunk config is captured.
	for _, opt := range opts {
		opt(sourceObj)
	}

	// Initialize readers.
	sourceObj.initializeReaders()

	return sourceObj
}

// initializeReaders initializes all available readers.
func (s *Source) initializeReaders() {
	// Build reader options - pass all configurations to reader layer
	var opts []reader.Option
	if s.chunkSize > 0 {
		opts = append(opts, reader.WithChunkSize(s.chunkSize))
	}
	if s.chunkOverlap > 0 {
		opts = append(opts, reader.WithChunkOverlap(s.chunkOverlap))
	}
	if s.customChunkingStrategy != nil {
		opts = append(opts, reader.WithCustomChunkingStrategy(s.customChunkingStrategy))
	}

	s.textReader = text.New(opts...)
}

// ReadDocuments automatically detects the source type and reads documents.
func (s *Source) ReadDocuments(ctx context.Context) ([]*document.Document, error) {
	if len(s.inputs) == 0 {
		return nil, nil // Skip if no inputs provided.
	}
	var allDocuments []*document.Document
	for _, input := range s.inputs {
		documents, err := s.processInput(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("failed to process input %s: %w", input, err)
		}
		allDocuments = append(allDocuments, documents...)
	}
	return allDocuments, nil
}

// Name returns the name of this source.
func (s *Source) Name() string {
	return s.name
}

// Type returns the type of this source.
func (s *Source) Type() string {
	return source.TypeAuto
}

// processInput determines the input type and processes it accordingly.
func (s *Source) processInput(ctx context.Context, input string) ([]*document.Document, error) {
	// Check if it's a URL.
	if s.isURL(input) {
		return s.processAsURL(ctx, input)
	}
	// Check if it's a directory.
	if s.isDirectory(input) {
		return s.processAsDirectory(ctx, input)
	}
	// Check if it's a file.
	if s.isFile(input) {
		return s.processAsFile(ctx, input)
	}
	// If none of the above, treat as text content.
	return s.processAsText(input)
}

// isURL checks if the input is a valid URL.
func (s *Source) isURL(input string) bool {
	parsedURL, err := url.Parse(input)
	return err == nil && parsedURL.Scheme != "" && parsedURL.Host != ""
}

// isDirectory checks if the input is a directory.
func (s *Source) isDirectory(input string) bool {
	info, err := os.Stat(input)
	return err == nil && info.IsDir()
}

// isFile checks if the input is a file.
func (s *Source) isFile(input string) bool {
	info, err := os.Stat(input)
	return err == nil && info.Mode().IsRegular()
}

// processAsURL processes the input as a URL.
func (s *Source) processAsURL(ctx context.Context, input string) ([]*document.Document, error) {
	urlSource := urlsource.New(
		[]string{input},
		urlsource.WithChunkSize(s.chunkSize),
		urlsource.WithChunkOverlap(s.chunkOverlap),
	)
	// Copy metadata.
	for k, v := range s.metadata {
		urlSource.SetMetadata(k, v)
	}
	return urlSource.ReadDocuments(ctx)
}

// processAsDirectory processes the input as a directory.
func (s *Source) processAsDirectory(ctx context.Context, input string) ([]*document.Document, error) {
	var opts []dirsource.Option

	// If a custom chunking strategy is set, use it
	if s.customChunkingStrategy != nil {
		opts = append(opts, dirsource.WithCustomChunkingStrategy(s.customChunkingStrategy))
	} else {
		// Otherwise, pass chunk size/overlap
		if s.chunkSize > 0 {
			opts = append(opts, dirsource.WithChunkSize(s.chunkSize))
		}
		if s.chunkOverlap > 0 {
			opts = append(opts, dirsource.WithChunkOverlap(s.chunkOverlap))
		}
	}

	if s.ocrExtractor != nil {
		opts = append(opts, dirsource.WithOCRExtractor(s.ocrExtractor))
	}
	dirSource := dirsource.New([]string{input}, opts...)
	// Copy metadata.
	for k, v := range s.metadata {
		dirSource.SetMetadata(k, v)
	}
	return dirSource.ReadDocuments(ctx)
}

// processAsFile processes the input as a file.
func (s *Source) processAsFile(ctx context.Context, input string) ([]*document.Document, error) {
	var opts []filesource.Option

	// If a custom chunking strategy is set, use it
	if s.customChunkingStrategy != nil {
		opts = append(opts, filesource.WithCustomChunkingStrategy(s.customChunkingStrategy))
	} else {
		// Otherwise, pass chunk size/overlap
		if s.chunkSize > 0 {
			opts = append(opts, filesource.WithChunkSize(s.chunkSize))
		}
		if s.chunkOverlap > 0 {
			opts = append(opts, filesource.WithChunkOverlap(s.chunkOverlap))
		}
	}

	if s.ocrExtractor != nil {
		opts = append(opts, filesource.WithOCRExtractor(s.ocrExtractor))
	}
	fileSource := filesource.New([]string{input}, opts...)

	// Copy metadata.
	for k, v := range s.metadata {
		fileSource.SetMetadata(k, v)
	}
	return fileSource.ReadDocuments(ctx)
}

// processAsText processes the input as text content.
func (s *Source) processAsText(input string) ([]*document.Document, error) {
	// Create a text reader and process the input as text.
	docs, err := s.textReader.ReadFromReader("text_input", strings.NewReader(input))
	// calc 256 hash of input
	sha256Hash := sha256.Sum256([]byte(input))

	if err != nil {
		return nil, err
	}
	metadata := make(map[string]any)
	for k, v := range s.metadata {
		metadata[k] = v
	}
	metadata[source.MetaSource] = source.TypeAuto

	metadata[source.MetaURI] = fmt.Sprintf("text://%s", hex.EncodeToString(sha256Hash[:]))
	metadata[source.MetaSourceName] = s.name

	// Add metadata for each document
	for _, doc := range docs {
		if doc.Metadata == nil {
			doc.Metadata = make(map[string]any)
		}
		for k, v := range metadata {
			doc.Metadata[k] = v
		}
	}

	return docs, nil
}

// SetMetadata sets metadata for this source.
func (s *Source) SetMetadata(key string, value any) {
	if s.metadata == nil {
		s.metadata = make(map[string]any)
	}
	s.metadata[key] = value
}

// GetMetadata returns the metadata associated with this source.
func (s *Source) GetMetadata() map[string]any {
	result := make(map[string]any)
	for k, v := range s.metadata {
		result[k] = v
	}
	return result
}
