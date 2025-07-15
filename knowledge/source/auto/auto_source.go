//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

// Package auto provides auto-detection knowledge source implementation.
package auto

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/chunking"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/text"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	dirsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/dir"
	filesource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
	urlsource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/url"
)

const (
	defaultAutoSourceName = "Auto Source"
	autoSourceType        = "auto"
)

// Source represents a knowledge source that automatically detects the source type.
type Source struct {
	inputs       []string
	name         string
	metadata     map[string]interface{}
	textReader   reader.Reader
	chunkSize    int
	chunkOverlap int
}

// New creates a new auto knowledge source.
func New(inputs []string, opts ...Option) *Source {
	sourceObj := &Source{
		inputs:       inputs,
		name:         defaultAutoSourceName,
		metadata:     make(map[string]interface{}),
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
	if s.chunkSize <= 0 && s.chunkOverlap <= 0 {
		s.textReader = text.New()
		return
	}

	// Build chunking options.
	var fixedOpts []chunking.Option
	if s.chunkSize > 0 {
		fixedOpts = append(fixedOpts, chunking.WithChunkSize(s.chunkSize))
	}
	if s.chunkOverlap > 0 {
		fixedOpts = append(fixedOpts, chunking.WithOverlap(s.chunkOverlap))
	}
	fixedChunk := chunking.NewFixedSizeChunking(fixedOpts...)
	s.textReader = text.New(text.WithChunkingStrategy(fixedChunk))
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
	dirSource := dirsource.New(
		[]string{input},
		dirsource.WithChunkSize(s.chunkSize),
		dirsource.WithChunkOverlap(s.chunkOverlap),
	)
	// Copy metadata.
	for k, v := range s.metadata {
		dirSource.SetMetadata(k, v)
	}
	return dirSource.ReadDocuments(ctx)
}

// processAsFile processes the input as a file.
func (s *Source) processAsFile(ctx context.Context, input string) ([]*document.Document, error) {
	fileSource := filesource.New(
		[]string{input},
		filesource.WithChunkSize(s.chunkSize),
		filesource.WithChunkOverlap(s.chunkOverlap),
	)

	// Copy metadata.
	for k, v := range s.metadata {
		fileSource.SetMetadata(k, v)
	}
	return fileSource.ReadDocuments(ctx)
}

// processAsText processes the input as text content.
func (s *Source) processAsText(input string) ([]*document.Document, error) {
	// Create a text reader and process the input as text.
	return s.textReader.ReadFromReader("text_input", strings.NewReader(input))
}
