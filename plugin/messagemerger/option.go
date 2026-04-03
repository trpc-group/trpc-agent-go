//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package messagemerger

const (
	defaultPluginName = "consecutive_message_merger"
	defaultSeparator  = "\n\n"
)

// Option configures the message merger plugin.
type Option func(*options)

type options struct {
	name      string
	separator string
}

func newOptions(opts ...Option) *options {
	options := &options{
		name:      defaultPluginName,
		separator: defaultSeparator,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(options)
		}
	}
	return options
}

// WithName sets the plugin name.
func WithName(name string) Option {
	return func(opts *options) {
		opts.name = name
	}
}

// WithSeparator sets the separator inserted between merged text segments.
func WithSeparator(separator string) Option {
	return func(opts *options) {
		opts.separator = separator
	}
}
