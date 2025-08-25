//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

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

// Metadata keys
const (
	MetaSource     = "trpc_agent_go_source"
	MetaSourceID   = "trpc_agent_go_source_id"
	MetaFilePath   = "trpc_agent_go_file_path"
	MetaFileName   = "trpc_agent_go_file_name"
	MetaFileExt    = "trpc_agent_go_file_ext"
	MetaFileSize   = "trpc_agent_go_file_size"
	MetaFileMode   = "trpc_agent_go_file_mode"
	MetaModifiedAt = "trpc_agent_go_modified_at"
	MetaURL        = "trpc_agent_go_url"
	MetaURLHost    = "trpc_agent_go_url_host"
	MetaURLPath    = "trpc_agent_go_url_path"
	MetaURLScheme  = "trpc_agent_go_url_scheme"
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

	// SourceID returns a unique identifier for this source.
	// This ID can be used for source management operations like update and delete.
	SourceID() string
}
