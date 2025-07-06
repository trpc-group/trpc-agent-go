// Package auto provides auto-detection knowledge source implementation.
package auto

import (
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document/readerfactory"
)

// Option represents a functional option for configuring auto sources.
type Option func(*Source)

// WithName sets the name of the auto source.
func WithName(name string) Option {
	return func(s *Source) {
		s.name = name
	}
}

// WithMetadata sets the metadata for the auto source.
func WithMetadata(metadata map[string]interface{}) Option {
	return func(s *Source) {
		for k, v := range metadata {
			s.metadata[k] = v
		}
	}
}

// WithMetadataValue adds a single metadata key-value pair.
func WithMetadataValue(key string, value interface{}) Option {
	return func(s *Source) {
		if s.metadata == nil {
			s.metadata = make(map[string]interface{})
		}
		s.metadata[key] = value
	}
}

// WithReaderFactory sets the reader factory for the auto source.
func WithReaderFactory(factory *readerfactory.Factory) Option {
	return func(s *Source) {
		s.readerFactory = factory
	}
}
