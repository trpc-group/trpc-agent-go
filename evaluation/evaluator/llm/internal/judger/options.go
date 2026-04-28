//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package judger

import "trpc.group/trpc-go/trpc-agent-go/model"

type options struct {
	structuredOutput *model.StructuredOutput
}

// Option configures the internal judge helper.
type Option func(*options)

func newOptions(opt ...Option) *options {
	opts := &options{}
	for _, o := range opt {
		o(opts)
	}
	return opts
}

// WithStructuredOutput sets the structured output schema for the judge request.
func WithStructuredOutput(out *model.StructuredOutput) Option {
	return func(opts *options) {
		opts.structuredOutput = out
	}
}
