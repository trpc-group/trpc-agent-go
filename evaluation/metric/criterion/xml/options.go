//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package xml

type options struct {
	ignore  bool
	valid   bool
	compare CompareFunc
}

func newOptions(opt ...Option) *options {
	opts := &options{}
	for _, o := range opt {
		o(opts)
	}
	return opts
}

// Option configures XMLCriterion.
type Option func(*options)

// WithIgnore sets the ignore flag.
func WithIgnore(ignore bool) Option {
	return func(o *options) {
		o.ignore = ignore
	}
}

// WithValid sets the XML validity flag.
func WithValid(valid bool) Option {
	return func(o *options) {
		o.valid = valid
	}
}

// WithCompare sets the custom compare function.
func WithCompare(compare CompareFunc) Option {
	return func(o *options) {
		o.compare = compare
	}
}
