package local

// Option configures the local evaluation result manager.
type Option func(*Manager)

// WithBaseDir overrides the default base directory used to store results.
func WithBaseDir(dir string) Option {
	return func(m *Manager) {
		m.baseDir = dir
	}
}

