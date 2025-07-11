//
// Tencent is pleased to support the open source community by making tRPC available.
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

// WithChunkOverlap sets the desired chunk overlap for document splitting.
func WithChunkOverlap(overlap int) Option {
	return func(s *Source) {
		s.chunkOverlap = overlap
	}
}
