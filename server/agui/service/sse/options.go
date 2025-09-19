//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sse

// options holds the options for the SSE service.
type options struct {
	addr string
	path string
}

// newOptions creates a new options instance.
func newOptions(opt ...Option) *options {
	opts := &options{}
	for _, o := range opt {
		o(opts)
	}
	return opts
}

// Option is a function that configures the options.
type Option func(*options)

// WithAddress sets the listening address.
func WithAddress(addr string) Option {
	return func(s *options) {
		s.addr = addr
	}
}

// WithPath sets the request path.
func WithPath(path string) Option {
	return func(s *options) {
		s.path = path
	}
}
