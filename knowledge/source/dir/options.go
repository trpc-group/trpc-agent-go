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

// Package dir provides directory-based knowledge source implementation.
package dir

// Option represents a functional option for configuring directory sources.
type Option func(*Source)

// WithName sets the name of the directory source.
func WithName(name string) Option {
	return func(s *Source) {
		s.name = name
	}
}

// WithMetadata sets the metadata for the directory source.
func WithMetadata(metadata map[string]interface{}) Option {
	return func(s *Source) {
		for k, v := range metadata {
			s.metadata[k] = v
		}
	}
}

// WithFileExtensions sets the file extensions to filter by.
func WithFileExtensions(extensions []string) Option {
	return func(s *Source) {
		s.fileExtensions = extensions
	}
}

// WithRecursive sets whether to process subdirectories recursively.
func WithRecursive(recursive bool) Option {
	return func(s *Source) {
		s.recursive = recursive
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
