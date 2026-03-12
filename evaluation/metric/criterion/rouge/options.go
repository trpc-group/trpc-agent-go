//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package rouge

type options struct {
	ignore         bool
	rougeType      string
	measure        RougeMeasure
	threshold      Score
	useStemmer     bool
	splitSummaries bool
	tokenizerName  string
	tokenizer      Tokenizer
}

func newOptions(opt ...Option) *options {
	opts := &options{}
	for _, o := range opt {
		o(opts)
	}
	return opts
}

// Option configures RougeCriterion.
type Option func(*options)

// WithIgnore sets the ignore flag.
func WithIgnore(ignore bool) Option {
	return func(o *options) {
		o.ignore = ignore
	}
}

// WithRougeType sets the ROUGE variant.
func WithRougeType(rougeType string) Option {
	return func(o *options) {
		o.rougeType = rougeType
	}
}

// WithMeasure sets the primary ROUGE measure.
func WithMeasure(measure RougeMeasure) Option {
	return func(o *options) {
		o.measure = measure
	}
}

// WithThreshold sets the minimum score thresholds.
func WithThreshold(threshold Score) Option {
	return func(o *options) {
		o.threshold = threshold
	}
}

// WithUseStemmer enables Porter stemming for the built-in tokenizer.
func WithUseStemmer(useStemmer bool) Option {
	return func(o *options) {
		o.useStemmer = useStemmer
	}
}

// WithSplitSummaries enables sentence splitting for rougeLsum.
func WithSplitSummaries(splitSummaries bool) Option {
	return func(o *options) {
		o.splitSummaries = splitSummaries
	}
}

// WithTokenizerName sets the name of the registered tokenizer.
func WithTokenizerName(tokenizerName string) Option {
	return func(o *options) {
		o.tokenizerName = tokenizerName
	}
}

// WithTokenizer sets the custom tokenizer.
func WithTokenizer(tokenizer Tokenizer) Option {
	return func(o *options) {
		o.tokenizer = tokenizer
	}
}
