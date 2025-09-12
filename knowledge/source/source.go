//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package source defines the interface for knowledge sources.
package source

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

// Source types
const (
	TypeAuto = "auto"
	TypeFile = "file"
	TypeDir  = "dir"
	TypeURL  = "url"
)

const metaPrefix = "trpc_agent_go_"

// Metadata keys
const (
	MetaSource        = metaPrefix + "source"
	MetaFilePath      = metaPrefix + "file_path"
	MetaFileName      = metaPrefix + "file_name"
	MetaFileExt       = metaPrefix + "file_ext"
	MetaFileSize      = metaPrefix + "file_size"
	MetaFileMode      = metaPrefix + "file_mode"
	MetaModifiedAt    = metaPrefix + "modified_at"
	MetaContentLength = metaPrefix + "content_length"
	MetaFileCount     = metaPrefix + "file_count"
	MetaFilePaths     = metaPrefix + "file_paths"
	MetaURL           = metaPrefix + "url"
	MetaURLHost       = metaPrefix + "url_host"
	MetaURLPath       = metaPrefix + "url_path"
	MetaURLScheme     = metaPrefix + "url_scheme"
	MetaInputCount    = metaPrefix + "input_count"
	MetaInputs        = metaPrefix + "inputs"

	MetaChunkType = metaPrefix + "chunk_type"
	MetaChunkSize = metaPrefix + "chunk_size"

	// necessary metadata
	MetaURI        = metaPrefix + "uri"         // URI (absolute path / URL / md5 for pure text)
	MetaSourceName = metaPrefix + "source_name" // source name
	MetaChunkIndex = metaPrefix + "chunk_index" // chunk index
)

// Source represents a knowledge source that can provide documents.
type Source interface {
	// ReadDocuments reads and returns documents representing the source.
	// This method should handle the specific content type and return any errors.
	ReadDocuments(ctx context.Context) ([]*document.Document, error)

	// Name returns a human-readable name for this source.
	Name() string

	// Type returns the type of this source (e.g., "file", "url", "dir").
	Type() string

	// GetMetadata returns the metadata that user set
	GetMetadata() map[string]interface{}
}

// GetAllMetadata returns all metadata collected from sources with deduplication.
func GetAllMetadata(sources []Source) map[string][]interface{} {
	// Use temporary map for deduplication
	tempMetadataMap := make(map[string]map[string]struct{})
	allMetadata := make(map[string][]interface{})

	// Iterate through all sources to collect metadata
	for _, src := range sources {
		metadata := src.GetMetadata()
		for key, value := range metadata {
			// Initialize key in temporary map
			if _, exists := tempMetadataMap[key]; !exists {
				tempMetadataMap[key] = make(map[string]struct{})
				allMetadata[key] = make([]interface{}, 0)
			}

			// Create a unique key that includes type information to avoid conflicts
			valueKey := fmt.Sprintf("%T:%v", value, value)
			if _, exists := tempMetadataMap[key][valueKey]; !exists {
				allMetadata[key] = append(allMetadata[key], value)
				tempMetadataMap[key][valueKey] = struct{}{}
			}
		}
	}
	return allMetadata
}

// GetAllMetadataWithoutValues returns all metadata keys with their string values collected from sources with deduplication.
func GetAllMetadataWithoutValues(sources []Source) map[string][]interface{} {
	result := make(map[string][]interface{})
	for _, src := range sources {
		metadata := src.GetMetadata()
		for key := range metadata {
			if _, exists := result[key]; !exists {
				result[key] = []interface{}{}
			}
		}
	}
	return result
}

// GetAllMetadataKeys returns all metadata keys collected from sources with deduplication.
func GetAllMetadataKeys(sources []Source) []string {
	allMetadata := GetAllMetadataWithoutValues(sources)
	result := make([]string, 0)
	for key := range allMetadata {
		result = append(result, key)
	}
	return result
}
