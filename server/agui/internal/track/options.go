//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package track

import "trpc.group/trpc-go/trpc-agent-go/server/agui/aggregator"

// options configures the tracker.
type options struct {
	aggregatorFactory aggregator.Factory  // aggregatorFactory builds aggregators for tracking.
	aggregationOption []aggregator.Option // aggregationOption forwards options to the factory.
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

// newOptions creates a new options instance.
func newOptions(opt ...Option) *options {
	opts := &options{
		aggregatorFactory: aggregator.New,
	}
	for _, o := range opt {
		o(opts)
	}
	return opts
}
