//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolloopwarning

// Option configures the tool-loop warning plugin.
type Option func(*options)

type options struct {
	warning           string
	excludedToolNames map[string]struct{}
}

func newOptions(opts ...Option) *options {
	o := &options{warning: defaultWarning}
	for _, opt := range opts {
		if opt != nil {
			opt(o)
		}
	}
	return o
}

// WithWarningMessage sets the synthetic user-role instruction queued after a
// repeated tool round. An empty message leaves the default unchanged.
func WithWarningMessage(message string) Option {
	return func(o *options) {
		if message != "" {
			o.warning = message
		}
	}
}

// WithExcludedToolNames excludes named tools from loop detection. A round
// containing any excluded tool breaks adjacency and never triggers a warning.
func WithExcludedToolNames(names ...string) Option {
	return func(o *options) {
		if o.excludedToolNames == nil {
			o.excludedToolNames = make(map[string]struct{})
		}
		for _, name := range names {
			if name != "" {
				o.excludedToolNames[name] = struct{}{}
			}
		}
	}
}
