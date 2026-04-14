//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package jsonschema

type options struct {
	strict bool
}

// Option configures a Generator.
type Option func(*options)

// WithStrict enables strict structured-output-compatible schema generation.
func WithStrict() Option {
	return func(o *options) {
		o.strict = true
	}
}

func newOptions(opt ...Option) *options {
	opts := &options{}
	for _, o := range opt {
		if o != nil {
			o(opts)
		}
	}
	return opts
}
