//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package service

// defaultPath is the default path for the AG-UI service.
const defaultPath = "/"

// Options holds the options for an AG-UI transport implementation.
type Options struct {
	Path string // Path is the request URL path served by the handler.
}

// NewOptions creates a new options instance.
func NewOptions(opt ...Option) *Options {
	opts := &Options{}
	for _, o := range opt {
		o(opts)
	}
	if opts.Path == "" {
		opts.Path = defaultPath
	}
	return opts
}

// Option is a function that configures the options.
type Option func(*Options)

// WithPath sets the request path.
func WithPath(path string) Option {
	return func(s *Options) {
		s.Path = path
	}
}
