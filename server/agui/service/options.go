//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package service

// Options holds the options for the SSE service.
type Options struct {
	Address string // Address is the listening address.
	Path    string // Path is the request url path.
}

// Option is a function that configures the options.
type Option func(*Options)

// WithAddress sets the listening address.
func WithAddress(addr string) Option {
	return func(s *Options) {
		s.Address = addr
	}
}

// WithPath sets the request path.
func WithPath(path string) Option {
	return func(s *Options) {
		s.Path = path
	}
}
