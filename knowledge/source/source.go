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

	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
)

// Source types
const (
	TypeAuto = "auto"
	TypeFile = "file"
	TypeDir  = "dir"
	TypeURL  = "url"
)

const metaPrefix = "trpc_agent_go"

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

	// GetMetadata returns the metadata associated with this source.
	GetMetadata() map[string]interface{}
}
