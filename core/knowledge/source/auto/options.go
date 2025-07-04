// Package auto provides auto-deduction knowledge source implementation.
package auto

import "net/http"

// Option represents a functional option for configuring Source.
type Option func(*Source)

// WithName sets a custom name for the auto source.
func WithName(name string) Option {
	return func(s *Source) {
		s.name = name
	}
}

// WithMetadata sets additional metadata for the source.
func WithMetadata(metadata map[string]interface{}) Option {
	return func(s *Source) {
		s.metadata = metadata
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

// WithHTTPClient sets a custom HTTP client for downloading content from URLs.
func WithHTTPClient(client *http.Client) Option {
	return func(s *Source) {
		s.httpClient = client
	}
}
