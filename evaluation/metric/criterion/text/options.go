//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package text

type options struct {
	ignore          bool
	caseInsensitive bool
	matchStrategy   TextMatchStrategy
	compare         func(actual, expected string) (bool, error)
}

func newOptions(opt ...Option) *options {
	opts := &options{}
	for _, o := range opt {
		o(opts)
	}
	return opts
}

// Option configures TextCriterion.
type Option func(*options)

// WithIgnore sets the ignore flag.
func WithIgnore(ignore bool) Option {
	return func(o *options) {
		o.ignore = ignore
	}
}

// WithCaseInsensitive sets case-insensitive comparison.
func WithCaseInsensitive(caseInsensitive bool) Option {
	return func(o *options) {
		o.caseInsensitive = caseInsensitive
	}
}

// WithMatchStrategy sets the match strategy.
func WithMatchStrategy(matchStrategy TextMatchStrategy) Option {
	return func(o *options) {
		o.matchStrategy = matchStrategy
	}
}

// WithCompare sets the custom compare function.
func WithCompare(compare func(actual, expected string) (bool, error)) Option {
	return func(o *options) {
		o.compare = compare
	}
}
