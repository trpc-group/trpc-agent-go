package evalresult

type Options struct {
	BaseDir string
}

func NewOptions(opt ...Option) *Options {
	opts := &Options{
		BaseDir: "evalset_results",
	}
	for _, o := range opt {
		o(opts)
	}
	return opts
}

// Option configures the local evaluation result manager.
type Option func(*Options)

// WithBaseDir overrides the default base directory used to store results.
func WithBaseDir(dir string) Option {
	return func(m *Options) {
		m.BaseDir = dir
	}
}
