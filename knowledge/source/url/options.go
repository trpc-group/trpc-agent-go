//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package url provides URL-based knowledge source implementation.
package url

import (
	"net/http"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/chunking"
)

// Option represents a functional option for configuring Source.
type Option func(*Source)

// WithName sets a custom name for the URL source.
func WithName(name string) Option {
	return func(s *Source) {
		s.name = name
	}
}

// WithContentFetchingURL sets the real content fetching URL for the source.
// The real content fetching URL is used to fetch the actual content of the document.
func WithContentFetchingURL(url []string) Option {
	return func(s *Source) {
		s.fetchURLs = url
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

// WithHTTPClient sets a custom HTTP client for URL fetching.
func WithHTTPClient(client *http.Client) Option {
	return func(s *Source) {
		s.httpClient = client
	}
}

// WithCustomChunkingStrategy sets a custom chunking strategy for document splitting.
// This overrides the reader's default chunking strategy.
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
