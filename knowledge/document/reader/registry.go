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

// Constructor is a function that creates a new Reader instance.
type Constructor func() Reader

// Registry manages registration of document readers.
type Registry struct {
	mu          sync.RWMutex
	readers     map[string]Constructor // extension -> constructor
	initialized map[string]Reader      // cache of initialized readers
}

// globalRegistry is the singleton registry instance.
var globalRegistry = &Registry{
	readers:     make(map[string]Constructor),
	initialized: make(map[string]Reader),
}

// RegisterReader registers a reader constructor for specific file extensions.
// Extensions should include the dot prefix (e.g., ".pdf", ".txt").
func RegisterReader(extensions []string, constructor Constructor) {
	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()

	for _, ext := range extensions {
		// Normalize extension to lowercase.
		normalizedExt := strings.ToLower(ext)
		globalRegistry.readers[normalizedExt] = constructor
	}
}

// GetReader returns a reader for the given file extension.
// The extension should include the dot prefix (e.g., ".pdf").
// Returns nil and false if no reader is registered for the extension.
func GetReader(extension string) (Reader, bool) {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()

	// Normalize extension to lowercase.
	normalizedExt := strings.ToLower(extension)

	// Check if we have a cached instance.
	if reader, exists := globalRegistry.initialized[normalizedExt]; exists {
		return reader, true
	}

	// Check if we have a constructor.
	constructor, exists := globalRegistry.readers[normalizedExt]
	if !exists {
		return nil, false
	}

	// Create a new instance.
	// Note: We're still holding the read lock here, which could be
	// optimized if reader construction is expensive.
	reader := constructor()

	// Upgrade to write lock to cache the instance.
	globalRegistry.mu.RUnlock()
	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()
	defer globalRegistry.mu.RLock()

	// Double-check that another goroutine didn't already create it.
	if cachedReader, exists := globalRegistry.initialized[normalizedExt]; exists {
		return cachedReader, true
	}

	globalRegistry.initialized[normalizedExt] = reader
	return reader, true
}

// GetAllReaders returns all registered readers as a map of file type to reader.
// The returned map uses simplified type names (e.g., "text", "pdf") as keys.
func GetAllReaders() map[string]Reader {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()

	result := make(map[string]Reader)
	processedTypes := make(map[string]bool)

	for ext, constructor := range globalRegistry.readers {
		// Convert extension to type name.
		typeName := extensionToType(ext)

		// Skip if we've already processed this type.
		if processedTypes[typeName] {
			continue
		}
		processedTypes[typeName] = true

		// Check if we have a cached instance.
		if reader, exists := globalRegistry.initialized[ext]; exists {
			result[typeName] = reader
		} else {
			// Create a new instance.
			reader := constructor()
			// Cache it for future use.
			globalRegistry.mu.RUnlock()
			globalRegistry.mu.Lock()
			globalRegistry.initialized[ext] = reader
			globalRegistry.mu.Unlock()
			globalRegistry.mu.RLock()
			result[typeName] = reader
		}
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

	globalRegistry.readers = make(map[string]Constructor)
	globalRegistry.initialized = make(map[string]Reader)
}
