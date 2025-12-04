//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package aggregator

const (
	defaultEnabled = true // defaultEnabled turns aggregation on by default.
)

// Option is a function that configures the aggregator options.
type Option func(*options)

// options configures the aggregator.
type options struct {
	enabled bool // enabled toggles aggregation behavior.
}

// newOptions creates a new options instance.
func newOptions(opt ...Option) *options {
	opts := &options{enabled: defaultEnabled}
	for _, o := range opt {
		o(opts)
	}
	return opts
}

// WithEnabled toggles aggregation.
// When true (default), adjacent text content events are merged before persistence.
// When false, events pass through without merging.
func WithEnabled(enabled bool) Option {
	return func(o *options) {
		o.enabled = enabled
	}
}
