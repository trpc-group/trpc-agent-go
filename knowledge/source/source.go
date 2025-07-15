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

// Metadata keys
const (
	MetaSource        = "source"
	MetaFilePath      = "file_path"
	MetaFileName      = "file_name"
	MetaFileExt       = "file_ext"
	MetaFileSize      = "file_size"
	MetaFileMode      = "file_mode"
	MetaModifiedAt    = "modified_at"
	MetaContentLength = "content_length"
	MetaFileCount     = "file_count"
	MetaFilePaths     = "file_paths"
	MetaURL           = "url"
	MetaURLHost       = "url_host"
	MetaURLPath       = "url_path"
	MetaURLScheme     = "url_scheme"
	MetaInputCount    = "input_count"
	MetaInputs        = "inputs"
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
}
