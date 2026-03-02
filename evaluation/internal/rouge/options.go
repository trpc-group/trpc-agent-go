//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package rouge

// options holds internal configuration for ROUGE scoring.
type options struct {
	// rougeTypes holds the requested ROUGE types to compute.
	rougeTypes []string
	// useStemmer enables Porter stemming for tokenization.
	useStemmer bool
	// splitSummaries enables sentence splitting for rougeLsum.
	splitSummaries bool
	// tokenizer overrides the built-in tokenizer when provided.
	tokenizer Tokenizer
}

// newOptions applies functional options to build a scoring configuration.
func newOptions(opt ...Option) *options {
	opts := &options{}
	for _, o := range opt {
		o(opts)
	}
	return opts
}

// Option configures ROUGE scoring.
type Option func(*options)

// WithRougeTypes sets the ROUGE types to compute.
func WithRougeTypes(rougeTypes ...string) Option {
	return func(o *options) {
		o.rougeTypes = append([]string(nil), rougeTypes...)
	}
}

// WithStemmer enables or disables Porter stemming in the tokenizer.
func WithStemmer(useStemmer bool) Option {
	return func(o *options) {
		o.useStemmer = useStemmer
	}
}

// WithSplitSummaries splits summaries into sentences for rougeLsum.
func WithSplitSummaries(splitSummaries bool) Option {
	return func(o *options) {
		o.splitSummaries = splitSummaries
	}
}

// WithTokenizer overrides the built-in tokenizer when provided.
func WithTokenizer(tokenizer Tokenizer) Option {
	return func(o *options) {
		o.tokenizer = tokenizer
	}
}
