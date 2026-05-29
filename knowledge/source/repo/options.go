//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package repo provides repository-based knowledge source implementation.
package repo

import (
	"trpc.group/trpc-go/trpc-agent-go/knowledge/transform"
)

// Option represents a functional option for configuring repository sources.
type Option func(*Source)

// WithRepository sets the repository handled by this Source.
func WithRepository(repository Repository) Option {
	return func(s *Source) {
		s.repository = repository
	}
}

// WithName sets the source name.
func WithName(name string) Option {
	return func(s *Source) {
		s.name = name
	}
}

// WithMetadata sets custom metadata for the source.
func WithMetadata(metadata map[string]any) Option {
	return func(s *Source) {
		for k, v := range metadata {
			s.metadata[k] = v
		}
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

// WithFileExtensions limits processing to the given file extensions.
func WithFileExtensions(extensions []string) Option {
	return func(s *Source) {
		s.fileExtensions = append([]string(nil), extensions...)
	}
}

// WithSkipDirs configures directory names to skip during scanning.
func WithSkipDirs(dirs []string) Option {
	return func(s *Source) {
		s.skipDirs = append([]string(nil), dirs...)
	}
}

// WithSkipSuffixes configures file suffixes to skip during scanning.
func WithSkipSuffixes(suffixes []string) Option {
	return func(s *Source) {
		s.skipSuffixes = append([]string(nil), suffixes...)
	}
}

// WithTransformers sets document transformers.
func WithTransformers(transformers ...transform.Transformer) Option {
	return func(s *Source) {
		s.transformers = append(s.transformers, transformers...)
	}
}

// WithDocExtensions sets the file extensions (e.g. ".md", ".txt") that will be
// included as document nodes when ReadGraph is called.
// By default no document files are included in graph output.
func WithDocExtensions(extensions []string) Option {
	return func(s *Source) {
		s.docExtensions = append([]string(nil), extensions...)
	}
}

// WithParseConcurrency sets the concurrency for code AST parsing in ReadGraph.
// Zero or negative values mean use the parser's default.
func WithParseConcurrency(n int) Option {
	return func(s *Source) {
		s.parseConcurrency = n
	}
}
