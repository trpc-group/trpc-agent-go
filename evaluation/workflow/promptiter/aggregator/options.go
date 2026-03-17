//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package aggregator consolidates gradients and scores produced by sampler traces before optimization.
package aggregator

// options stores optional aggregation behavior.
type options struct {
}

// option mutates aggregator options during construction.
type option func(*options)

// newOptions applies all aggregator options and returns final constructor state.
func newOptions(opt ...option) *options {
	opts := &options{}
	for _, o := range opt {
		o(opts)
	}
	return opts
}
