//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package debuglog

const defaultPluginName = "debug_log"

// Option configures the debug log plugin.
type Option func(*options)

type options struct {
	name                        string
	eventEnabled                bool
	modelPartialResponseEnabled bool
}

func newOptions(opts ...Option) *options {
	o := &options{
		name: defaultPluginName,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(o)
		}
	}
	return o
}

// WithName sets the plugin name. The name must be unique within a Runner.
func WithName(name string) Option {
	return func(o *options) {
		if name != "" {
			o.name = name
		}
	}
}

// WithEventEnabled controls whether runner events are logged.
func WithEventEnabled(enabled bool) Option {
	return func(o *options) {
		o.eventEnabled = enabled
	}
}

// WithModelPartialResponseEnabled controls whether partial model responses are logged.
func WithModelPartialResponseEnabled(enabled bool) Option {
	return func(o *options) {
		o.modelPartialResponseEnabled = enabled
	}
}
