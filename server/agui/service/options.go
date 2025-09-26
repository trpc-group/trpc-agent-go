//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package service

// Options holds the options for an AG-UI transport implementation.
type Options struct {
	Path string // Path is the request URL path served by the handler.
}

// Option is a function that configures the options.
type Option func(*Options)

// WithPath sets the request path.
func WithPath(path string) Option {
	return func(s *Options) {
		if path != "" {
			s.Path = path
		}
	}
}
