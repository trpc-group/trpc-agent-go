// Package readerfactory provides document reader factory implementation.
package readerfactory

import (
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document/reader"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document/reader/csv"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document/reader/json"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document/reader/markdown"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document/reader/pdf"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document/reader/text"
)

// Factory creates appropriate readers based on file type or content.
type Factory struct {
	config *reader.Config
}

// Option represents a functional option for configuring the Factory.
type Option func(*Factory)

// WithConfig sets the reader configuration for the factory.
func WithConfig(config *reader.Config) Option {
	return func(f *Factory) {
		f.config = config
	}
}

// NewFactory creates a new reader factory with the given options.
func NewFactory(opts ...Option) *Factory {
	f := &Factory{
		config: reader.DefaultConfig(),
	}

	// Apply options.
	for _, opt := range opts {
		opt(f)
	}

	return f
}

// CreateReader creates a reader based on file extension.
func (f *Factory) CreateReader(filePath string) reader.Reader {
	ext := strings.ToLower(filepath.Ext(filePath))

	switch ext {
	case ".md", ".markdown":
		return markdown.New(f.config)
	case ".txt", ".text":
		return text.New(f.config)
	case ".csv":
		return csv.New(f.config)
	case ".json":
		return json.New(f.config)
	case ".pdf":
		return pdf.New(f.config)
	default:
		// Default to text reader for unknown extensions.
		return text.New(f.config)
	}
}

// CreateReaderByContentType creates a reader based on content type.
func (f *Factory) CreateReaderByContentType(contentType string) reader.Reader {
	contentType = strings.ToLower(contentType)

	switch {
	case strings.Contains(contentType, "markdown"):
		return markdown.New(f.config)
	case strings.Contains(contentType, "text"):
		return text.New(f.config)
	case strings.Contains(contentType, "csv"):
		return csv.New(f.config)
	case strings.Contains(contentType, "json"):
		return json.New(f.config)
	case strings.Contains(contentType, "pdf"):
		return pdf.New(f.config)
	default:
		// Default to text reader for unknown content types.
		return text.New(f.config)
	}
}
