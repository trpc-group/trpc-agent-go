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
	criterionjson "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/json"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
)

type options struct {
	text    *text.TextCriterion
	json    *criterionjson.JSONCriterion
	compare func(actual, expected *evalset.Invocation) (bool, error)
}

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
func WithJSONCriterion(criterion *criterionjson.JSONCriterion) Option {
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
