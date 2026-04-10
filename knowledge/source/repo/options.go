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
	"trpc.group/trpc-go/trpc-agent-go/knowledge/chunking"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/ocr"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/transform"
)

// Option represents a functional option for configuring repository sources.
type Option func(*Source)

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

// WithBranch sets the git branch to checkout when using a git URL.
func WithBranch(branch string) Option {
	return func(s *Source) {
		s.branch = branch
	}
}

// WithCommit sets the git commit to checkout when using a git URL.
func WithCommit(commit string) Option {
	return func(s *Source) {
		s.commit = commit
	}
}

// WithRepoName sets the logical repository name.
func WithRepoName(name string) Option {
	return func(s *Source) {
		s.repoName = name
	}
}

// WithRepoURL sets the logical repository URL.
func WithRepoURL(repoURL string) Option {
	return func(s *Source) {
		s.repoURL = repoURL
	}
}

// WithSubdir limits processing to a subdirectory under the repository root.
func WithSubdir(subdir string) Option {
	return func(s *Source) {
		s.subdir = subdir
	}
}

// WithRecursive sets whether to process subdirectories recursively.
func WithRecursive(recursive bool) Option {
	return func(s *Source) {
		s.recursive = recursive
	}
}

// WithFileExtensions limits processing to the given file extensions.
func WithFileExtensions(extensions []string) Option {
	return func(s *Source) {
		s.fileExtensions = extensions
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

// WithFileReaderType overrides automatic file type detection.
func WithFileReaderType(fileType source.FileReaderType) Option {
	return func(s *Source) {
		s.fileReaderType = fileType
	}
}

// WithCustomChunkingStrategy sets a custom chunking strategy.
func WithCustomChunkingStrategy(strategy chunking.Strategy) Option {
	return func(s *Source) {
		s.customChunkingStrategy = strategy
	}
}

// WithChunkSize sets the chunk size for readers.
func WithChunkSize(size int) Option {
	return func(s *Source) {
		s.chunkSize = size
	}
}

// WithChunkOverlap sets the chunk overlap for readers.
func WithChunkOverlap(overlap int) Option {
	return func(s *Source) {
		s.chunkOverlap = overlap
	}
}

// WithOCRExtractor sets the OCR extractor.
func WithOCRExtractor(extractor ocr.Extractor) Option {
	return func(s *Source) {
		s.ocrExtractor = extractor
	}
}

// WithTransformers sets document transformers.
func WithTransformers(transformers ...transform.Transformer) Option {
	return func(s *Source) {
		s.transformers = append(s.transformers, transformers...)
	}
}
