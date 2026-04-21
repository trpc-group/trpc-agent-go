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

// WithRepository sets structured repository descriptors explicitly.
func WithRepository(repositories ...Repository) Option {
	return func(s *Source) {
		s.repositories = append([]Repository(nil), repositories...)
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

// WithBranch sets the git branch to checkout when using a git URL.
func WithBranch(branch string) Option {
	return func(s *Source) {
		s.branch = branch
	}
}

// WithTag sets the git tag to checkout when using a git URL.
func WithTag(tag string) Option {
	return func(s *Source) {
		s.tag = tag
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

// WithDirs sets local repository directories explicitly.
func WithDirs(dirs ...string) Option {
	return func(s *Source) {
		s.dirs = append([]string(nil), dirs...)
	}
}

// WithRepoURLs sets remote Git repository URLs explicitly.
func WithRepoURLs(urls ...string) Option {
	return func(s *Source) {
		s.repoURLs = append([]string(nil), urls...)
	}
}

// WithSubdir limits processing to a subdirectory under the repository root.
func WithSubdir(subdir string) Option {
	return func(s *Source) {
		s.subdir = subdir
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
