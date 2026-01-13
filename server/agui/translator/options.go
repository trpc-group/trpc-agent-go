//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package translator

// options configures which graph-related AG-UI events the translator emits.
type options struct {
	graphNodeStartActivityEnabled     bool // graphNodeStartActivityEnabled enables graph node start activity events.
	graphNodeInterruptActivityEnabled bool // graphNodeInterruptActivityEnabled enables graph interrupt activity events.
}

// Option is a function that configures the options.
type Option func(*options)

// newOptions creates a new options instance.
func newOptions(opt ...Option) options {
	opts := options{}
	for _, o := range opt {
		o(&opts)
	}
	return opts
}

// WithGraphNodeStartActivityEnabled controls whether the translator emits
// ACTIVITY_DELTA events with activityType "graph.node.start".
func WithGraphNodeStartActivityEnabled(enabled bool) Option {
	return func(o *options) {
		o.graphNodeStartActivityEnabled = enabled
	}
}

// WithGraphNodeInterruptActivityEnabled controls whether the translator emits
// ACTIVITY_DELTA events with activityType "graph.node.interrupt".
func WithGraphNodeInterruptActivityEnabled(enabled bool) Option {
	return func(o *options) {
		o.graphNodeInterruptActivityEnabled = enabled
	}
}
