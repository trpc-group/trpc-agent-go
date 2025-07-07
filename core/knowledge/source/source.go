// Package source defines the interface for knowledge sources.
package source

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document"
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
