//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package json

// defaultNumberTolerance is the default number tolerance.
const defaultNumberTolerance = 1e-6

// options configures JSONCriterion.
type options struct {
	ignore          bool
	ignoreTree      map[string]any
	onlyTree        map[string]any
	matchStrategy   JSONMatchStrategy
	numberTolerance *float64
	compare         func(actual, expected any) (bool, error)
}

// newOptions creates a Options with the provided options.
func newOptions(opt ...Option) *options {
	opts := &options{}
	for _, o := range opt {
		o(opts)
	}
	return opts
}

// Option is a function that configures JSONCriterion.
type Option func(*options)

// WithIgnore sets the ignore flag.
func WithIgnore(ignore bool) Option {
	return func(o *options) {
		o.ignore = ignore
	}
}

// WithIgnoreTree sets the ignore tree.
func WithIgnoreTree(ignoreTree map[string]any) Option {
	return func(o *options) {
		o.ignoreTree = ignoreTree
	}
}

// WithOnlyTree sets the only tree.
func WithOnlyTree(onlyTree map[string]any) Option {
	return func(o *options) {
		o.onlyTree = onlyTree
	}
}

// WithMatchStrategy sets the match strategy.
func WithMatchStrategy(matchStrategy JSONMatchStrategy) Option {
	return func(o *options) {
		o.matchStrategy = matchStrategy
	}
}

// WithNumberTolerance sets the number tolerance.
func WithNumberTolerance(tolerance float64) Option {
	return func(o *options) {
		o.numberTolerance = &tolerance
	}
}

// WithCompare sets the compare function.
func WithCompare(compare func(actual, expected any) (bool, error)) Option {
	return func(o *options) {
		o.compare = compare
	}
}
