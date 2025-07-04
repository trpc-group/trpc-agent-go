// Package source defines the interface for knowledge sources.
package source

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document"
)

// Source types
const (
	TypeAuto     = "auto"
	TypeFile     = "file"
	TypeCSV      = "csv"
	TypeJSON     = "json"
	TypePDF      = "pdf"
	TypeExcel    = "excel"
	TypeTextFile = "textfile"
	TypeURL      = "url"
	TypeString   = "string"
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

// Source represents a knowledge source that can provide a document.
type Source interface {
	// ReadDocument reads and returns a document representing the whole source.
	// This method should handle the specific content type and return any errors.
	ReadDocument(ctx context.Context) (*document.Document, error)

	// Name returns a human-readable name for this source.
	Name() string

	// Type returns the type of this source (e.g., "file", "url", "text").
	Type() string
}
