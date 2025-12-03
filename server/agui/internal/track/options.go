//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package track

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/server/agui/aggregator"
)

// DefaultFlushInterval is the default flush interval for the tracker.
const DefaultFlushInterval = time.Second

// options configures the tracker.
type options struct {
	aggregatorFactory aggregator.Factory  // aggregatorFactory builds aggregators for tracking.
	aggregationOption []aggregator.Option // aggregationOption forwards options to the factory.
	flushInterval     time.Duration       // flushInterval is the interval for flushing the session state.
}

// newOptions creates a new options instance.
func newOptions(opt ...Option) *options {
	opts := &options{
		aggregatorFactory: aggregator.New,
		flushInterval:     DefaultFlushInterval,
	}
	for _, o := range opt {
		o(opts)
	}
	return opts
}

// Option is a function that configures the tracker options.
type Option func(*options)

// WithAggregatorFactory sets the aggregator factory for the tracker.
func WithAggregatorFactory(factory aggregator.Factory) Option {
	return func(o *options) {
		o.aggregatorFactory = factory
	}
}

// WithAggregationOption appends aggregator options for tracker-created aggregators.
func WithAggregationOption(option ...aggregator.Option) Option {
	return func(o *options) {
		o.aggregationOption = append(o.aggregationOption, option...)
	}
}

// WithFlushInterval sets the flush interval for the tracker.
func WithFlushInterval(d time.Duration) Option {
	return func(o *options) {
		o.flushInterval = d
	}
}
