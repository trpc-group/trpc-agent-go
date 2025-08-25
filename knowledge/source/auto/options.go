//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package auto provides auto-detection knowledge source implementation.
package auto

// Option represents a functional option for configuring auto sources.
type Option func(*Source)

// WithName sets the name of the auto source.
func WithName(name string) Option {
	return func(s *Source) {
		s.name = name
	}
}

// WithMetadata sets the metadata for the auto source.
func WithMetadata(metadata map[string]interface{}) Option {
	return func(s *Source) {
		for k, v := range metadata {
			s.metadata[k] = v
		}
	}
}

// WithMetadataValue adds a single metadata key-value pair.
func WithMetadataValue(key string, value interface{}) Option {
	return func(s *Source) {
		if s.metadata == nil {
			s.metadata = make(map[string]interface{})
		}
		s.metadata[key] = value
	}
}

// WithChunkSize sets the desired chunk size for document splitting.
func WithChunkSize(size int) Option {
	return func(s *Source) {
		s.chunkSize = size
	}
}

// WithChunkOverlap sets the chunk overlap for the Source.
func WithChunkOverlap(overlap int) Option {
	return func(s *Source) {
		s.chunkOverlap = overlap
	}
}

// WithSourceID sets the source ID for the Source.
// Note: The provided ID should be globally unique across all sources to avoid conflicts.
// If not set, a default unique ID will be automatically generated.
func WithSourceID(id string) Option {
	return func(s *Source) {
		s.sourceID = id
	}
}
