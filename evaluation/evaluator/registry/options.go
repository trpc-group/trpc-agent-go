//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package registry

import operatorregistry "trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/registry"

// Option configures the evaluator registry.
type Option func(*options)

type options struct {
	llmOperatorRegistry operatorregistry.Registry
}

// WithLLMOperatorRegistry sets the operator registry used by the default template LLM evaluator.
func WithLLMOperatorRegistry(r operatorregistry.Registry) Option {
	return func(o *options) {
		o.llmOperatorRegistry = r
	}
}
