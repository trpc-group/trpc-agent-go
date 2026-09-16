//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolerror

const defaultPluginName = "tool_error"

// Option configures the tool error plugin.
type Option func(*options)

type options struct {
	name     string
	resolver Resolver
}

func newOptions(opts ...Option) *options {
	o := &options{name: defaultPluginName}
	for _, opt := range opts {
		if opt != nil {
			opt(o)
		}
	}
	return o
}

// WithName sets the plugin name. The name must be unique within a Runner. An
// empty name is ignored and keeps the default name.
func WithName(name string) Option {
	return func(o *options) {
		if name != "" {
			o.name = name
		}
	}
}

// WithResolver sets the application-specific failure resolver.
//
// The resolver runs before the plugin's default execution-error classifier and
// may classify a result that carries a business failure with a nil Go error.
func WithResolver(resolver Resolver) Option {
	return func(o *options) {
		o.resolver = resolver
	}
}
