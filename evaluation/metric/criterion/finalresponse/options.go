//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package finalresponse

import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	cjson "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/json"
	crouge "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/rouge"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
)

// options holds construction-time configuration for FinalResponseCriterion.
type options struct {
	// text configures text-based comparison.
	text *text.TextCriterion
	// json configures JSON-based comparison.
	json *cjson.JSONCriterion
	// rouge configures ROUGE scoring comparison.
	rouge *crouge.RougeCriterion
	// compare overrides built-in comparison when provided.
	compare func(actual, expected *evalset.Invocation) (bool, error)
}

// newOptions applies functional options to build a criterion configuration.
func newOptions(opt ...Option) *options {
	opts := &options{}
	for _, o := range opt {
		o(opts)
	}
	return opts
}

// Option configures FinalResponseCriterion.
type Option func(*options)

// WithTextCriterion sets the text criterion.
func WithTextCriterion(criterion *text.TextCriterion) Option {
	return func(o *options) {
		o.text = criterion
	}
}

// WithJSONCriterion sets the JSON criterion.
func WithJSONCriterion(criterion *cjson.JSONCriterion) Option {
	return func(o *options) {
		o.json = criterion
	}
}

// WithCompare sets the custom compare function.
func WithCompare(compare func(actual, expected *evalset.Invocation) (bool, error)) Option {
	return func(o *options) {
		o.compare = compare
	}
}

// WithRougeCriterion sets the ROUGE criterion.
func WithRougeCriterion(criterion *crouge.RougeCriterion) Option {
	return func(o *options) {
		o.rouge = criterion
	}
}
