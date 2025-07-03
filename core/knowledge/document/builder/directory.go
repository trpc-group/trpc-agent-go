// Package builder provides directory document builder logic.
package builder

// DirectoryOption represents a functional option for directory document creation.
type DirectoryOption func(*directoryConfig)

// directoryConfig holds configuration for directory document creation.
type directoryConfig struct {
	fileOptions []FileOption
}

// WithFileOptions sets file options for directory loading.
func WithFileOptions(opts ...FileOption) DirectoryOption {
	return func(c *directoryConfig) {
		c.fileOptions = opts
	}
}
