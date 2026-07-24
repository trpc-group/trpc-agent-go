//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolresultfile

const (
	defaultPluginName     = "tool_result_file"
	defaultThresholdBytes = 32 * 1024
)

// Option configures the tool-result file plugin.
type Option func(*options)

type options struct {
	name           string
	thresholdBytes int
}

func newOptions(opts ...Option) *options {
	o := &options{
		name:           defaultPluginName,
		thresholdBytes: defaultThresholdBytes,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(o)
		}
	}
	return o
}

// WithName sets the plugin name. It must be unique within a Runner.
func WithName(name string) Option {
	return func(o *options) {
		if name != "" {
			o.name = name
		}
	}
}

// WithThresholdBytes sets the minimum tool-result content size that is
// externalized. Values less than one leave the default threshold unchanged.
func WithThresholdBytes(n int) Option {
	return func(o *options) {
		if n > 0 {
			o.thresholdBytes = n
		}
	}
}
