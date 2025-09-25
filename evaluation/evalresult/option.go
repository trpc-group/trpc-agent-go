//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evalresult

const (
	defaultBaseDir = "evalset_results"
)

// Options holds the options for the evaluation result manager.
type Options struct {
	BaseDir string
}

// NewOptions creates a new Options with the default values.
func NewOptions(opt ...Option) *Options {
	opts := &Options{
		BaseDir: defaultBaseDir,
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
