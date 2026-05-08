//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package length

type options struct {
	ignore bool
	min    *int
	max    *int
}

func newOptions(opt ...Option) *options {
	opts := &options{}
	for _, o := range opt {
		o(opts)
	}
	return opts
}

// Option configures LengthCriterion.
type Option func(*options)

// WithIgnore sets the ignore flag.
func WithIgnore(ignore bool) Option {
	return func(o *options) {
		o.ignore = ignore
	}
}

// WithMin sets the inclusive minimum length.
func WithMin(min int) Option {
	return func(o *options) {
		o.min = &min
	}
}

// WithMax sets the inclusive maximum length.
func WithMax(max int) Option {
	return func(o *options) {
		o.max = &max
	}
}
