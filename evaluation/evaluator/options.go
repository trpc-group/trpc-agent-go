//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evaluator

import "trpc.group/trpc-go/trpc-agent-go/evaluation/metric"

// Options holds the configuration for the evaluator.
type Options struct {
	EvalMetric *metric.EvalMetric
}

// Option defines a function type for configuring the evaluator.
type Option func(*Options)

// WithEvalMetric sets the evaluation metric for the evaluator.
func WithEvalMetric(e *metric.EvalMetric) Option {
	return func(o *Options) {
		o.EvalMetric = e
	}
}
