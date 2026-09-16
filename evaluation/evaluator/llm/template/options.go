//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package template

import operatorregistry "trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/registry"

type options struct {
	operatorRegistry operatorregistry.Registry
}

func newOptions(opt ...Option) *options {
	opts := &options{}
	for _, o := range opt {
		o(opts)
	}
	if opts.operatorRegistry == nil {
		opts.operatorRegistry = operatorregistry.New()
	}
	return opts
}

// Option configures the template evaluator.
type Option func(*options)

// WithOperatorRegistry sets the LLM operator registry.
func WithOperatorRegistry(registry operatorregistry.Registry) Option {
	return func(o *options) {
		o.operatorRegistry = registry
	}
}
