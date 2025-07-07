// Package file provides file-based knowledge source implementation.
package file

// Option represents a functional option for configuring FileSource.
type Option func(*Source)

// WithName sets a custom name for the file source.
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
