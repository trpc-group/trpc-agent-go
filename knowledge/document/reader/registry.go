//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package reader defines the interface for document readers.
package reader

import (
	"strings"
	"sync"
)

// Builder is a function that creates a new Reader instance with options.
type Builder func(opts ...Option) Reader

// Registry manages registration of document readers.
type Registry struct {
	mu      sync.RWMutex
	readers map[string]Builder // extension -> builder
}

// globalRegistry is the singleton registry instance.
var globalRegistry = &Registry{
	readers: make(map[string]Builder),
}

// RegisterReader registers a reader builder for specific file extensions.
// Extensions should include the dot prefix (e.g., ".pdf", ".txt").
func RegisterReader(extensions []string, builder Builder) {
	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()

	for _, ext := range extensions {
		// Normalize extension to lowercase.
		normalizedExt := strings.ToLower(ext)
		globalRegistry.readers[normalizedExt] = builder
	}
}

// GetReader returns a new reader instance for the given file extension with options.
// The extension should include the dot prefix (e.g., ".pdf").
// Returns nil and false if no reader is registered for the extension.
func GetReader(extension string, opts ...Option) (Reader, bool) {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()

	normalizedExt := strings.ToLower(extension)
	builder, exists := globalRegistry.readers[normalizedExt]
	if !exists {
		return nil, false
	}

	// Create a new instance with options
	return builder(opts...), true
}

// GetAllReaders returns all registered readers as a map of file type to reader.
// The returned map uses simplified type names (e.g., "text", "pdf") as keys.
// Each call creates new reader instances with the provided options.
func GetAllReaders(opts ...Option) map[string]Reader {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()

	result := make(map[string]Reader)
	processedTypes := make(map[string]bool)

	for ext, builder := range globalRegistry.readers {
		typeName := extensionToType(ext)

		// Skip if we've already processed this type.
		if processedTypes[typeName] {
			continue
		}
		processedTypes[typeName] = true

		// Create a new instance with options
		result[typeName] = builder(opts...)
	}
	return result
}

// extensionToType converts a file extension to a simplified type name.
func extensionToType(ext string) string {
	// Remove the dot prefix if present.
	ext = strings.TrimPrefix(ext, ".")

	// Map common extensions to type names.
	switch ext {
	case "txt", "text":
		return "text"
	case "md", "markdown":
		return "markdown"
	case "json":
		return "json"
	case "csv":
		return "csv"
	case "pdf":
		return "pdf"
	case "docx", "doc":
		return "docx"
	default:
		return ext
	}
}

// GetRegisteredExtensions returns all registered file extensions.
func GetRegisteredExtensions() []string {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()

	extensions := make([]string, 0, len(globalRegistry.readers))
	for ext := range globalRegistry.readers {
		extensions = append(extensions, ext)
	}
	return extensions
}

// ClearRegistry clears all registered readers (mainly for testing).
func ClearRegistry() {
	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()

	globalRegistry.readers = make(map[string]Builder)
}
